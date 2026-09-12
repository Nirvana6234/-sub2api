using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Platform.MacOS;
using LanAi.RelayClient.Services;
using LanAi.Workspace.Injection;

namespace LanAi.RelayClient.Platform;

/// <summary>Picks how ChatGPT is started on this platform.</summary>
/// <remarks>
/// <para>
/// The fifth of these factories, alongside <see cref="SingleInstance"/>,
/// <see cref="SecureStorage"/>, <see cref="StartupRegistrations"/> and
/// <see cref="NotificationPresenters"/>. It exists so the composition root stops
/// naming Windows types directly — it used to construct <c>CodexAppLauncher</c>
/// outright, which is Windows-only.
/// </para>
/// <para>
/// Only the launcher varies now. This used to also choose whether the CDP status
/// overlay was available, and the two had to travel together because the overlay
/// attached over a DevTools port only the Windows launcher negotiated. The overlay is
/// gone; the route guard that remains is file IO and runs everywhere.
/// </para>
/// </remarks>
internal static class CodexHosts
{
    /// <exception cref="PlatformNotSupportedException">
    /// No way to start ChatGPT on this platform. Thrown rather than returning a
    /// launcher that always fails, because 启动 ChatGPT is the client's whole purpose
    /// and a silent no-op is the worst way to say it is unavailable.
    /// </exception>
    public static ICodexAppLauncher CreateLauncher()
    {
        if (OperatingSystem.IsWindows())
        {
            return new CodexAppLauncherAdapter(new CodexAppLauncher());
        }

        if (OperatingSystem.IsMacOS())
        {
            return new MacCodexAppLauncher(new MacCodexProcess());
        }

        throw new PlatformNotSupportedException("当前平台无法启动 ChatGPT 桌面版。");
    }

    /// <summary>
    /// The watch that keeps <c>config.toml</c> pointed at the relay.
    /// </summary>
    /// <remarks>
    /// Every platform now, where macOS used to get a null host. That split existed
    /// because this host also owned a CDP status overlay, which is Windows-only — the
    /// guard itself is file IO on <c>~/.codex</c> and always worked anywhere. With the
    /// overlay gone the split had no reason left, and keeping it would have meant Mac
    /// users silently kept the one failure the guard exists to catch: an official
    /// ChatGPT sign-in rewriting the file and dropping the relay route.
    /// </remarks>
    public static ICodexRouteGuardHost CreateRouteGuardHost(CodexConfigWriter config)
    {
        ArgumentNullException.ThrowIfNull(config);

        return new CodexRouteGuardHost(config);
    }
}
