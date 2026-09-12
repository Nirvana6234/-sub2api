using LanAi.RelayClient.CodexBinding;
using LanAi.Workspace.Injection;

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

/// <summary>Keeps Codex pointed at the relay for as long as the session lasts.</summary>
/// <remarks>
/// Returns no success flag on purpose: starting the watch cannot partly work, and a
/// bool here previously carried the CDP overlay's outcome, which the caller then had
/// to translate into user-facing text about a feature that no longer exists.
/// </remarks>
internal interface ICodexRouteGuardHost
{
    Task StartAsync(string apiKey, string baseUrl, CancellationToken cancellationToken = default);

    Task StopAsync();
}

internal sealed class NullCodexRouteGuardHost : ICodexRouteGuardHost
{
    public Task StartAsync(string apiKey, string baseUrl, CancellationToken cancellationToken = default) =>
        Task.CompletedTask;

    public Task StopAsync() => Task.CompletedTask;
}

/// <summary>
/// Watches <c>config.toml</c> and puts the relay route back when something removes it.
/// </summary>
/// <remarks>
/// <para>
/// This used to also own a CDP session that drew a 共飞 status bar inside the official
/// client and watched for usage-limit dialogs. That is gone: on current ChatGPT builds
/// the DevTools endpoint answers <c>/json/version</c> and then stops answering while
/// the app finishes starting, so the overlay mostly failed anyway — and it failed
/// loudly, filling the log with a stack trace on every launch for a feature that was
/// never load-bearing.
/// </para>
/// <para>
/// The guard is the part that always mattered, and it is the reason this class still
/// exists. Completing an official ChatGPT sign-in rewrites <c>config.toml</c> wholesale
/// and drops every key it does not recognise, <c>[model_providers.gongfei]</c>
/// included. Nothing about that is visible to the user: the client keeps reporting
/// 就绪 while the traffic has quietly gone back to their ChatGPT plan. The guard
/// notices and writes the route back.
/// </para>
/// </remarks>
internal sealed class CodexRouteGuardHost(CodexConfigWriter config) : ICodexRouteGuardHost
{
    private readonly CodexConfigWriter _config = config ?? throw new ArgumentNullException(nameof(config));
    private CodexRouteGuard? _guard;

    public async Task StartAsync(
        string apiKey,
        string baseUrl,
        CancellationToken cancellationToken = default)
    {
        await StopAsync().ConfigureAwait(false);

        _guard = new CodexRouteGuard(
            () => _config.IsRelayRoute(baseUrl, apiKey),
            _ =>
            {
                _config.Apply(apiKey, baseUrl);
                ClientLog.Info("Codex 路由被重写，已自动重新应用共飞路由");
                return Task.CompletedTask;
            });
        _guard.Start();
    }

    public async Task StopAsync()
    {
        if (_guard is not null)
        {
            await _guard.StopAsync().ConfigureAwait(false);
            _guard = null;
        }
    }
}
