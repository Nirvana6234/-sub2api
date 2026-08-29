using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Platform.MacOS;
using LanAi.RelayClient.Services;
using LanAi.Workspace.Injection;

namespace LanAi.RelayClient.Platform;

/// <summary>Picks how ChatGPT is started, and whether the overlay is available.</summary>
/// <remarks>
/// <para>
/// The fifth of these factories, alongside <see cref="SingleInstance"/>,
/// <see cref="SecureStorage"/>, <see cref="StartupRegistrations"/> and
/// <see cref="NotificationPresenters"/>. It exists so the composition root stops
/// naming Windows types directly — it used to construct <c>CodexAppLauncher</c> and
/// <c>RelayInjectionHost</c> outright, both of which are Windows-only.
/// </para>
/// <para>
/// The two decisions travel together on purpose. The overlay attaches over a DevTools
/// port that only the Windows launcher negotiates, so a host paired with the wrong
/// launcher would sit waiting for a port nobody opened.
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
    /// The status overlay and limit sentinel, where the platform has them.
    /// </summary>
    /// <remarks>
    /// macOS v1 gets <see cref="NullCodexEnhancementHost"/> — a decision, not a gap.
    /// The overlay drives the official app over CDP, and the client relays through
    /// <c>~/.codex</c> perfectly well without it; what a Mac user loses is the in-app
    /// status strip and the automatic switch on hitting a limit, both of which are
    /// visible on the dashboard instead.
    /// </remarks>
    public static ICodexEnhancementHost CreateEnhancementHost(CodexConfigWriter config)
    {
        ArgumentNullException.ThrowIfNull(config);

        return OperatingSystem.IsWindows()
            ? new RelayInjectionHost(config)
            : new NullCodexEnhancementHost();
    }
}
