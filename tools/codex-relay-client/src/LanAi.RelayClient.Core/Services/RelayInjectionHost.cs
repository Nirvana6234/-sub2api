using LanAi.RelayClient.CodexBinding;
using LanAi.Workspace.Injection;
using LanAi.Workspace.Injection.Sentinel;

namespace LanAi.RelayClient.Services;

internal interface ICodexAppLauncher
{
    bool IsInstalled { get; }

    Task<CodexLaunchResult> EnsureDebugPortAsync(
        CodexLaunchRequest request,
        CancellationToken cancellationToken = default);
}

internal sealed class CodexAppLauncherAdapter(CodexAppLauncher launcher) : ICodexAppLauncher
{
    private readonly CodexAppLauncher _launcher = launcher ?? throw new ArgumentNullException(nameof(launcher));

    public bool IsInstalled => _launcher.IsInstalled;

    public Task<CodexLaunchResult> EnsureDebugPortAsync(
        CodexLaunchRequest request,
        CancellationToken cancellationToken = default) =>
        _launcher.EnsureDebugPortAsync(request, cancellationToken);
}

internal interface ICodexEnhancementHost
{
    Task<bool> StartAsync(string apiKey, string baseUrl, CancellationToken cancellationToken = default);

    Task StopAsync();
}

internal sealed class NullCodexEnhancementHost : ICodexEnhancementHost
{
    public Task<bool> StartAsync(string apiKey, string baseUrl, CancellationToken cancellationToken = default) =>
        Task.FromResult(true);

    public Task StopAsync() => Task.CompletedTask;
}

/// <summary>Owns the optional CDP overlay and the mandatory silent route guard.</summary>
internal sealed class RelayInjectionHost(CodexConfigWriter config) : ICodexEnhancementHost
{
    private readonly CodexConfigWriter _config = config ?? throw new ArgumentNullException(nameof(config));
    private CodexInjectionSession? _injection;
    private CodexRouteGuard? _guard;

    public async Task<bool> StartAsync(
        string apiKey,
        string baseUrl,
        CancellationToken cancellationToken = default)
    {
        await StopAsync().ConfigureAwait(false);

        var gateway = new RelayInjectionGateway(_config, apiKey, baseUrl);
        _guard = new CodexRouteGuard(
            () => _config.IsRelayRoute(baseUrl, apiKey),
            _ =>
            {
                _config.Apply(apiKey, baseUrl);
                ClientLog.Info("Codex 路由被重写，已自动重新应用共飞路由");
                return Task.CompletedTask;
            });
        _guard.Start();

        try
        {
            _injection = new CodexInjectionSession(gateway);
            CodexInjectionStartResult result = await _injection
                .StartAsync(cancellationToken)
                .ConfigureAwait(false);
            if (!result.Started)
            {
                ClientLog.Warning($"Codex 注入已降级：{result.Message}");
            }

            return result.Started;
        }
        catch (Exception ex)
        {
            ClientLog.Warning("Codex 注入失败，官方客户端继续运行", ex);
            _injection?.Dispose();
            _injection = null;
            return false;
        }
    }

    public async Task StopAsync()
    {
        if (_guard is not null)
        {
            await _guard.StopAsync().ConfigureAwait(false);
            _guard = null;
        }

        _injection?.Dispose();
        _injection = null;
    }

    private sealed class RelayInjectionGateway(
        CodexConfigWriter config,
        string apiKey,
        string baseUrl) : IRelaySwitchGateway
    {
        public Task<RelayRoutingState> ReadRoutingAsync(CancellationToken cancellationToken) =>
            Task.FromResult(new RelayRoutingState(
                config.IsRelayRoute(baseUrl, apiKey),
                baseUrl,
                "gongfei"));

        public Task<RelaySwitchOutcome> SwitchToRelayAsync(CancellationToken cancellationToken)
        {
            config.Apply(apiKey, baseUrl);
            return Task.FromResult(new RelaySwitchOutcome(true, "已重新应用共飞路由。"));
        }

        public Task<RelaySwitchOutcome> SwitchToOfficialAsync(CancellationToken cancellationToken) =>
            Task.FromResult(new RelaySwitchOutcome(false, "直连客户端不管理官方账号路由。"));
    }
}
