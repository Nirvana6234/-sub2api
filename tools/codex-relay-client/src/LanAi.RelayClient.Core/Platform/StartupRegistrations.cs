using LanAi.RelayClient.Platform.MacOS;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform;

/// <summary>Picks the "start with the system" mechanism for this platform.</summary>
/// <remarks>
/// <para>
/// The third of these factories, alongside <see cref="SingleInstance"/> and
/// <see cref="SecureStorage"/>. Windows writes a per-user <c>Run</c> registry value;
/// macOS writes a LaunchAgent plist.
/// </para>
/// <para>
/// Unlike <see cref="SecureStorage"/>, an unknown platform here falls back to
/// <see cref="UnsupportedStartupRegistration"/> rather than throwing. The asymmetry is
/// deliberate: failing to store a session means credentials would have to go somewhere
/// unsafe, whereas failing to register for startup only means the user launches the
/// client themselves. One is a security decision, the other an inconvenience.
/// </para>
/// <para>
/// <b>The macOS branch has never been run.</b> It is written but unverified — no Mac
/// was available — so it is wired here rather than left unreachable, and must be
/// exercised before the macOS build ships.
/// </para>
/// </remarks>
internal static class StartupRegistrations
{
    public static IStartupRegistration Create()
    {
        if (OperatingSystem.IsWindows())
        {
            return new WindowsStartupRegistration();
        }

        if (OperatingSystem.IsMacOS())
        {
            return new LaunchAgentStartupRegistration();
        }

        return new UnsupportedStartupRegistration();
    }
}
