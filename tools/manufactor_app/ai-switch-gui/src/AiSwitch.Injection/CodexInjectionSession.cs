using System.Text.Json.Nodes;
using LanAi.Workspace.Injection.Cdp;
using LanAi.Workspace.Injection.Sentinel;

namespace LanAi.Workspace.Injection;

public sealed record CodexInjectionSessionOptions
{
    public int Port { get; init; } = 9777;

    /// <summary>
    /// Permits restarting a running official client that has no debug port. Defaults to
    /// <c>false</c> so the user is asked first — a restart discards their in-flight turn.
    /// </summary>
    public bool AllowTerminateExisting { get; init; }

    public CodexLimitSentinelOptions Sentinel { get; init; } = new();

    public RelaySwitchOrchestratorOptions Orchestrator { get; init; } = new();
}

public sealed record CodexInjectionStartResult(
    bool Started,
    CodexLaunchOutcome LaunchOutcome,
    string Message)
{
    /// <summary>
    /// True when starting requires the user's permission to restart the official app.
    /// </summary>
    public bool NeedsRestartConsent => LaunchOutcome == CodexLaunchOutcome.BlockedByRunningInstance;
}

/// <summary>
/// Composition root for the injection stack: launches or attaches to the official
/// client, installs the overlay, starts the limit sentinel, and wires the switch
/// orchestrator to it.
/// </summary>
/// <remarks>
/// The whole stack is additive. Any failure leaves the official client fully usable,
/// so <see cref="StartAsync"/> reports rather than throws, and the UI can simply run
/// without the overlay.
/// </remarks>
public sealed class CodexInjectionSession : IDisposable
{
    private readonly IRelaySwitchGateway _gateway;
    private readonly CodexInjectionSessionOptions _options;
    private readonly CdpTargetLocator _locator = new();

    private CdpConnection? _connection;
    private CoflyOverlayInjector? _overlay;
    private CodexLimitSentinel? _sentinel;
    private RelaySwitchOrchestrator? _orchestrator;
    private bool _disposed;

    public CodexInjectionSession(
        IRelaySwitchGateway gateway,
        CodexInjectionSessionOptions? options = null)
    {
        _gateway = gateway ?? throw new ArgumentNullException(nameof(gateway));
        _options = options ?? new CodexInjectionSessionOptions();
    }

    /// <summary>Raised when the user should be offered the relay switch.</summary>
    public event EventHandler<RelaySwitchPrompt>? PromptRequested;

    /// <summary>Raised whenever the observed limit state changes materially.</summary>
    public event EventHandler<CodexLimitSnapshot>? LimitStateChanged;

    public CodexLimitSnapshot? CurrentLimit => _sentinel?.Current;

    public bool IsRunning => _connection is not null;

    public async Task<CodexInjectionStartResult> StartAsync(CancellationToken cancellationToken)
    {
        if (IsRunning)
        {
            return new CodexInjectionStartResult(true, CodexLaunchOutcome.AttachedToExisting, "注入已在运行。");
        }

        var launcher = new CodexAppLauncher(_locator);
        var launch = await launcher
            .EnsureDebugPortAsync(
                new CodexLaunchRequest
                {
                    Port = _options.Port,
                    AllowTerminateExisting = _options.AllowTerminateExisting,
                },
                cancellationToken)
            .ConfigureAwait(false);

        if (!launch.CanAttach)
        {
            return new CodexInjectionStartResult(false, launch.Outcome, launch.Message);
        }

        var target = await FindTargetAsync(cancellationToken).ConfigureAwait(false);
        if (target?.WebSocketDebuggerUrl is null)
        {
            return new CodexInjectionStartResult(false, launch.Outcome, "未找到可注入的页面目标。");
        }

        try
        {
            var connection = new CdpConnection(new CdpWebSocketTransport());
            await connection.ConnectAsync(new Uri(target.WebSocketDebuggerUrl), cancellationToken)
                .ConfigureAwait(false);
            _connection = connection;

            _overlay = new CoflyOverlayInjector(connection);
            await _overlay.InstallAsync(CoflyOverlayScript.Source, cancellationToken).ConfigureAwait(false);

            _sentinel = new CodexLimitSentinel(connection, _options.Sentinel);
            _sentinel.StateChanged += OnLimitStateChanged;
            await _sentinel.StartAsync(cancellationToken).ConfigureAwait(false);

            _orchestrator = new RelaySwitchOrchestrator(_gateway, _options.Orchestrator);
            _orchestrator.PromptRequested += OnPromptRequested;
            _orchestrator.Attach(_sentinel);
            _ = _orchestrator.StartRoutingWatchAsync();

            await RenderAsync(_sentinel.Current, cancellationToken).ConfigureAwait(false);

            return new CodexInjectionStartResult(true, launch.Outcome, launch.Message);
        }
        catch (Exception exception) when (
            exception is CdpProtocolException or CdpScriptException or IOException)
        {
            TearDown();
            return new CodexInjectionStartResult(false, launch.Outcome, $"注入失败：{exception.Message}");
        }
    }

    /// <summary>Performs the switch the user accepted.</summary>
    public Task<RelaySwitchOutcome> AcceptAsync(CancellationToken cancellationToken)
        => _orchestrator?.AcceptAsync(cancellationToken)
            ?? Task.FromResult(new RelaySwitchOutcome(false, "注入未启动。"));

    /// <summary>Records the user's refusal for the rest of this limit episode.</summary>
    public void Decline() => _orchestrator?.Decline();

    private async Task<CdpTarget?> FindTargetAsync(CancellationToken cancellationToken)
    {
        // A freshly launched app needs a moment before its page target is listed.
        for (var attempt = 0; attempt < 30; attempt++)
        {
            var target = await _locator.FindPageTargetAsync(_options.Port, cancellationToken)
                .ConfigureAwait(false);
            if (target is not null)
            {
                return target;
            }

            await Task.Delay(TimeSpan.FromMilliseconds(500), cancellationToken).ConfigureAwait(false);
        }

        return null;
    }

    private void OnLimitStateChanged(object? sender, CodexLimitSnapshot snapshot)
    {
        LimitStateChanged?.Invoke(this, snapshot);
        _ = RenderAsync(snapshot, CancellationToken.None);
    }

    private void OnPromptRequested(object? sender, RelaySwitchPrompt prompt)
        => PromptRequested?.Invoke(this, prompt);

    private async Task RenderAsync(CodexLimitSnapshot? snapshot, CancellationToken cancellationToken)
    {
        var overlay = _overlay;
        if (overlay is null)
        {
            return;
        }

        var (tone, label) = Describe(snapshot);
        try
        {
            await overlay
                .PushStateAsync(
                    new JsonObject
                    {
                        ["tone"] = tone,
                        ["label"] = label,
                        ["detail"] = snapshot?.Facts.ResetText ?? string.Empty,
                    },
                    cancellationToken)
                .ConfigureAwait(false);
        }
        catch (Exception exception) when (
            exception is CdpProtocolException or CdpScriptException or IOException or OperationCanceledException)
        {
            // The bar simply stays as it was; the official app is unaffected.
        }
    }

    internal static (string Tone, string Label) Describe(CodexLimitSnapshot? snapshot)
        => snapshot?.Level switch
        {
            CodexLimitLevel.Reached => ("reached", "共飞 · 官方额度已用尽"),
            CodexLimitLevel.Approaching => (
                "approaching",
                snapshot.Facts.UsedPercent is { } percent
                    ? $"共飞 · 官方额度 {percent:0}%"
                    : "共飞 · 官方额度接近上限"),
            CodexLimitLevel.Normal => ("normal", "共飞 · 就绪"),
            _ => ("unknown", "共飞 · 检测中"),
        };

    private void TearDown()
    {
        if (_sentinel is not null)
        {
            _sentinel.StateChanged -= OnLimitStateChanged;
            if (_orchestrator is not null)
            {
                _orchestrator.Detach(_sentinel);
            }

            _sentinel.Dispose();
            _sentinel = null;
        }

        if (_orchestrator is not null)
        {
            _orchestrator.PromptRequested -= OnPromptRequested;
            _orchestrator.Dispose();
            _orchestrator = null;
        }

        _overlay?.Dispose();
        _overlay = null;
        _connection?.Dispose();
        _connection = null;
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        TearDown();
        _locator.Dispose();
    }
}
