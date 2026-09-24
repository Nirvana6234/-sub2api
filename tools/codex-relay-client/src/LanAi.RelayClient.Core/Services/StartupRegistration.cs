namespace LanAi.RelayClient.Services;

/// <summary>How the client asks to be started when the user signs in.</summary>
/// <remarks>
/// The contract lives here while the implementation stays platform-side: Windows
/// writes an HKCU Run value, macOS will write a LaunchAgent plist, and neither
/// belongs in code that has to compile for both.
/// </remarks>
internal interface IStartupRegistration
{
    bool EnsureDefaultEnabled();

    bool SetEnabled(bool enabled);
}

/// <summary>The parts of the decision that are the same on every platform.</summary>
internal static class StartupRegistrationPolicy
{
    public static bool DefaultEnabled(string? storedPreference) =>
        !string.Equals(storedPreference, "disabled", StringComparison.OrdinalIgnoreCase);

    public static string CommandFor(string executablePath) =>
        $"\"{executablePath}\"";
}

/// <summary>Used when no platform implementation was supplied.</summary>
/// <remarks>
/// Reports failure rather than success. Silently claiming to have enabled autostart
/// would leave the checkbox ticked and the client not actually starting — the kind of
/// mismatch a novice user cannot diagnose. The composition root in App.xaml.cs passes
/// the real implementation; tests that do not care about autostart get this.
/// </remarks>
internal sealed class UnsupportedStartupRegistration : IStartupRegistration
{
    public bool EnsureDefaultEnabled() => false;

    public bool SetEnabled(bool enabled) => false;
}
