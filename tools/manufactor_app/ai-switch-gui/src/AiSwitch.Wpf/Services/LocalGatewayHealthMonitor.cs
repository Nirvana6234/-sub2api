using AiSwitchGui;
using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Services;

internal enum LocalGatewayRecoveryState
{
    Monitoring,
    Healthy,
    Degraded,
    Recovering,
    Recovered,
    Failed,
    Suspended,
}

internal sealed record LocalGatewayRecoveryUpdate(
    LocalGatewayRecoveryState State,
    string Message,
    int AttemptsInWindow = 0);

internal interface ILocalGatewayHealthMonitor : IAsyncDisposable
{
    event Action<LocalGatewayRecoveryUpdate>? StateChanged;

    void Start(CancellationToken cancellationToken);
}

internal sealed class LocalGatewayHealthMonitor : ILocalGatewayHealthMonitor
{
    private static readonly TimeSpan DefaultProbeInterval = TimeSpan.FromSeconds(30);
    private static readonly TimeSpan DefaultRecoveryTimeout = TimeSpan.FromSeconds(60);
    private static readonly TimeSpan DefaultAttemptWindow = TimeSpan.FromMinutes(10);
    private static readonly TimeSpan DefaultBaseCooldown = TimeSpan.FromSeconds(30);

    private readonly ILocalGatewayController _controller;
    private readonly TimeProvider _timeProvider;
    private readonly TimeSpan _probeInterval;
    private readonly TimeSpan _recoveryTimeout;
    private readonly TimeSpan _attemptWindow;
    private readonly TimeSpan _baseCooldown;
    private readonly int _failureThreshold;
    private readonly int _maximumAttempts;
    private readonly Queue<DateTimeOffset> _attempts = new();
    private readonly SemaphoreSlim _checkGate = new(1, 1);
    private CancellationTokenSource? _lifetime;
    private Task? _loopTask;
    private DateTimeOffset _nextRecoveryAt;
    private int _consecutiveFailures;
    private LocalGatewayRecoveryUpdate? _lastUpdate;

    public LocalGatewayHealthMonitor(ILocalGatewayController controller)
        : this(controller, TimeProvider.System)
    {
    }

    internal LocalGatewayHealthMonitor(
        ILocalGatewayController controller,
        TimeProvider timeProvider,
        TimeSpan? probeInterval = null,
        TimeSpan? recoveryTimeout = null,
        TimeSpan? attemptWindow = null,
        TimeSpan? baseCooldown = null,
        int failureThreshold = 2,
        int maximumAttempts = 3)
    {
        _controller = controller ?? throw new ArgumentNullException(nameof(controller));
        _timeProvider = timeProvider ?? throw new ArgumentNullException(nameof(timeProvider));
        _probeInterval = RequirePositive(probeInterval ?? DefaultProbeInterval, nameof(probeInterval));
        _recoveryTimeout = RequirePositive(recoveryTimeout ?? DefaultRecoveryTimeout, nameof(recoveryTimeout));
        _attemptWindow = RequirePositive(attemptWindow ?? DefaultAttemptWindow, nameof(attemptWindow));
        _baseCooldown = RequirePositive(baseCooldown ?? DefaultBaseCooldown, nameof(baseCooldown));
        _failureThreshold = failureThreshold > 0
            ? failureThreshold
            : throw new ArgumentOutOfRangeException(nameof(failureThreshold));
        _maximumAttempts = maximumAttempts > 0
            ? maximumAttempts
            : throw new ArgumentOutOfRangeException(nameof(maximumAttempts));
    }

    public event Action<LocalGatewayRecoveryUpdate>? StateChanged;

    public void Start(CancellationToken cancellationToken)
    {
        if (_loopTask is not null)
        {
            return;
        }

        _lifetime = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        Publish(LocalGatewayRecoveryState.Monitoring, "本地网关后台监测已启动。");
        _loopTask = RunAsync(_lifetime.Token);
    }

    internal async Task CheckOnceAsync(CancellationToken cancellationToken)
    {
        if (!await _checkGate.WaitAsync(0, cancellationToken).ConfigureAwait(false))
        {
            return;
        }

        try
        {
            LocalGatewayStatus status;
            try
            {
                status = await _controller.GetStatusAsync(cancellationToken).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                throw;
            }
            catch (Exception exception)
            {
                await HandleFailureAsync(
                        controlAvailable: true,
                        $"本地网关状态检查失败：{Compact(exception.Message)}",
                        cancellationToken)
                    .ConfigureAwait(false);
                return;
            }

            if (IsHealthy(status))
            {
                _consecutiveFailures = 0;
                Publish(LocalGatewayRecoveryState.Healthy, "本地网关运行正常。", CountRecentAttempts());
                return;
            }

            await HandleFailureAsync(
                    status.ControlAvailable,
                    status.ControlAvailable
                        ? $"检测到本地网关异常：{Compact(status.Summary)}"
                        : "未找到完整的本地网关安装目录，自动恢复不可用。",
                    cancellationToken)
                .ConfigureAwait(false);
        }
        finally
        {
            _checkGate.Release();
        }
    }

    public async ValueTask DisposeAsync()
    {
        CancellationTokenSource? lifetime = _lifetime;
        Task? loopTask = _loopTask;
        _lifetime = null;
        _loopTask = null;
        if (lifetime is null)
        {
            return;
        }

        lifetime.Cancel();
        if (loopTask is not null)
        {
            try
            {
                await loopTask.ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
            }
        }
        lifetime.Dispose();
    }

    private async Task RunAsync(CancellationToken cancellationToken)
    {
        try
        {
            while (true)
            {
                await Task.Delay(_probeInterval, _timeProvider, cancellationToken).ConfigureAwait(false);
                await CheckOnceAsync(cancellationToken).ConfigureAwait(false);
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
        }
    }

    private async Task HandleFailureAsync(
        bool controlAvailable,
        string message,
        CancellationToken cancellationToken)
    {
        _consecutiveFailures++;
        if (!controlAvailable)
        {
            Publish(LocalGatewayRecoveryState.Failed, message, CountRecentAttempts());
            return;
        }

        Publish(LocalGatewayRecoveryState.Degraded, message, CountRecentAttempts());
        if (_consecutiveFailures < _failureThreshold)
        {
            return;
        }

        DateTimeOffset now = _timeProvider.GetUtcNow();
        PruneAttempts(now);
        if (_attempts.Count >= _maximumAttempts)
        {
            Publish(
                LocalGatewayRecoveryState.Suspended,
                $"自动恢复已暂停：{_attemptWindow.TotalMinutes:0} 分钟内已尝试 {_maximumAttempts} 次。请查看本地网关页面的故障信息。",
                _attempts.Count);
            return;
        }
        if (now < _nextRecoveryAt)
        {
            return;
        }

        _attempts.Enqueue(now);
        int attemptNumber = _attempts.Count;
        Publish(LocalGatewayRecoveryState.Recovering, $"正在自动恢复本地网关（第 {attemptNumber} 次）。", attemptNumber);

        CommandResult startResult;
        try
        {
            startResult = await _controller.StartAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
            throw;
        }
        catch (Exception exception)
        {
            ScheduleCooldown(now, attemptNumber);
            Publish(
                LocalGatewayRecoveryState.Failed,
                $"自动恢复启动失败：{Compact(exception.Message)}",
                attemptNumber);
            return;
        }

        if (!startResult.Success)
        {
            ScheduleCooldown(now, attemptNumber);
            Publish(
                LocalGatewayRecoveryState.Failed,
                $"自动恢复失败：{Compact(startResult.CombinedOutput)}",
                attemptNumber);
            return;
        }

        bool ready = await _controller
            .WaitForWebAsync(_recoveryTimeout, cancellationToken)
            .ConfigureAwait(false);
        if (!ready)
        {
            ScheduleCooldown(now, attemptNumber);
            Publish(
                LocalGatewayRecoveryState.Failed,
                "自动恢复已执行，但本地网关未在限定时间内恢复健康。",
                attemptNumber);
            return;
        }

        LocalGatewayStatus verified = await _controller
            .GetStatusAsync(cancellationToken)
            .ConfigureAwait(false);
        if (!IsHealthy(verified))
        {
            ScheduleCooldown(now, attemptNumber);
            Publish(
                LocalGatewayRecoveryState.Failed,
                $"自动恢复后仍有服务异常：{Compact(verified.Summary)}",
                attemptNumber);
            return;
        }

        _consecutiveFailures = 0;
        _nextRecoveryAt = DateTimeOffset.MinValue;
        Publish(LocalGatewayRecoveryState.Recovered, "本地网关已自动恢复。", attemptNumber);
    }

    private int CountRecentAttempts()
    {
        PruneAttempts(_timeProvider.GetUtcNow());
        return _attempts.Count;
    }

    private void PruneAttempts(DateTimeOffset now)
    {
        while (_attempts.Count > 0 && now - _attempts.Peek() >= _attemptWindow)
        {
            _attempts.Dequeue();
        }
    }

    private void ScheduleCooldown(DateTimeOffset now, int attemptNumber)
    {
        double multiplier = Math.Pow(2, Math.Min(attemptNumber - 1, 4));
        _nextRecoveryAt = now + TimeSpan.FromTicks((long)(_baseCooldown.Ticks * multiplier));
    }

    private void Publish(LocalGatewayRecoveryState state, string message, int attemptsInWindow = 0)
    {
        var update = new LocalGatewayRecoveryUpdate(state, message, attemptsInWindow);
        if (_lastUpdate == update)
        {
            return;
        }

        _lastUpdate = update;
        StateChanged?.Invoke(update);
    }

    internal static bool IsHealthy(LocalGatewayStatus status) =>
        status.ControlAvailable &&
        status.WebReachable &&
        status.Services.Count > 0 &&
        status.Services.All(service => service.IsHealthyEnough);

    private static string Compact(string? value)
    {
        string compact = string.Join(
            " ",
            (value ?? string.Empty).Split(['\r', '\n'], StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries));
        if (string.IsNullOrWhiteSpace(compact))
        {
            return "未返回具体错误。";
        }
        return compact.Length <= 360 ? compact : compact[..360] + "...";
    }

    private static TimeSpan RequirePositive(TimeSpan value, string parameterName) =>
        value > TimeSpan.Zero ? value : throw new ArgumentOutOfRangeException(parameterName);
}
