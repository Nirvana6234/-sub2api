using System.IO;

namespace LanAi.RelayClient.Services;

/// <summary>Reapplies the relay route when the official client rewrites its config.</summary>
internal sealed class CodexRouteGuard
{
    private static readonly TimeSpan DefaultInterval = TimeSpan.FromSeconds(30);

    private readonly Func<bool> _routeIsCurrent;
    private readonly Func<CancellationToken, Task> _reapply;
    private readonly Func<TimeSpan, CancellationToken, Task> _delay;
    private readonly TimeSpan _interval;

    private CancellationTokenSource? _shutdown;
    private Task? _watch;

    public CodexRouteGuard(
        Func<bool> routeIsCurrent,
        Func<CancellationToken, Task> reapply,
        TimeSpan? interval = null,
        Func<TimeSpan, CancellationToken, Task>? delay = null)
    {
        _routeIsCurrent = routeIsCurrent ?? throw new ArgumentNullException(nameof(routeIsCurrent));
        _reapply = reapply ?? throw new ArgumentNullException(nameof(reapply));
        _interval = interval ?? DefaultInterval;
        _delay = delay ?? Task.Delay;
    }

    public void Start()
    {
        if (_watch is { IsCompleted: false })
        {
            return;
        }

        _shutdown?.Dispose();
        _shutdown = new CancellationTokenSource();
        _watch = WatchAsync(_shutdown.Token);
    }

    public async Task StopAsync()
    {
        CancellationTokenSource? shutdown = _shutdown;
        Task? watch = _watch;
        if (shutdown is null || watch is null)
        {
            return;
        }

        shutdown.Cancel();
        try
        {
            await watch.ConfigureAwait(false);
        }
        catch (OperationCanceledException) when (shutdown.IsCancellationRequested)
        {
        }
        finally
        {
            shutdown.Dispose();
            _shutdown = null;
            _watch = null;
        }
    }

    internal async Task CheckOnceAsync(CancellationToken cancellationToken)
    {
        if (!_routeIsCurrent())
        {
            await _reapply(cancellationToken).ConfigureAwait(false);
        }
    }

    private async Task WatchAsync(CancellationToken cancellationToken)
    {
        while (true)
        {
            await _delay(_interval, cancellationToken).ConfigureAwait(false);
            try
            {
                await CheckOnceAsync(cancellationToken).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                throw;
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
            {
                ClientLog.Warning("路由守护检查失败，稍后重试", ex);
            }
        }
    }
}
