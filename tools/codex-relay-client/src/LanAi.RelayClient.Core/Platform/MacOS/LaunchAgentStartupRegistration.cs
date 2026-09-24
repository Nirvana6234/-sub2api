using System.Diagnostics;
using System.Runtime.Versioning;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// Starts the client at login on macOS by writing a LaunchAgent plist.
/// </summary>
/// <remarks>
/// <para>
/// <b>Written without a Mac to run it on. Treat every claim here as unverified until
/// it has been exercised on real hardware.</b> The plist content is covered by unit
/// tests; the file placement and the <c>launchctl</c> handshake are not, and are the
/// parts most likely to be wrong.
/// </para>
/// <para>
/// Mirrors <c>StartupRegistration</c> on Windows, including the part that matters
/// most: the stored preference is what the user chose, and it is read back rather
/// than inferred from whether the agent file happens to exist. A user who removes the
/// plist by hand should not have it silently reinstated on next launch.
/// </para>
/// </remarks>
[SupportedOSPlatform("macos")]
internal sealed class LaunchAgentStartupRegistration : IStartupRegistration
{
    private readonly string _agentDirectory;
    private readonly string _preferenceFile;
    private readonly string _executablePath;

    public LaunchAgentStartupRegistration(
        string? agentDirectory = null,
        string? preferenceFile = null,
        string? executablePath = null)
    {
        _agentDirectory = agentDirectory ?? Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            "Library",
            "LaunchAgents");
        _preferenceFile = preferenceFile ?? AppPaths.InData("startup-preference");
        _executablePath = executablePath ?? Environment.ProcessPath ?? string.Empty;
    }

    private string PlistPath => Path.Combine(_agentDirectory, LaunchAgentPlist.FileName);

    public bool EnsureDefaultEnabled()
    {
        string? stored = ReadPreference();

        // Same rule as Windows: absent means "not chosen yet", and the product
        // decision (F9.1) is that autostart defaults to on.
        bool enabled = StartupRegistrationPolicy.DefaultEnabled(stored);
        return SetEnabled(enabled);
    }

    public bool SetEnabled(bool enabled)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_preferenceFile)!);
            File.WriteAllText(_preferenceFile, enabled ? "enabled" : "disabled");

            if (enabled)
            {
                if (string.IsNullOrWhiteSpace(_executablePath))
                {
                    return false;
                }

                Directory.CreateDirectory(_agentDirectory);
                WriteAtomically(PlistPath, LaunchAgentPlist.Build(_executablePath));
                Load(PlistPath);
            }
            else if (File.Exists(PlistPath))
            {
                Unload(PlistPath);
                File.Delete(PlistPath);
            }

            return true;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("设置开机自启失败", ex);
            return false;
        }
    }

    private string? ReadPreference()
    {
        try
        {
            return File.Exists(_preferenceFile) ? File.ReadAllText(_preferenceFile).Trim() : null;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("读取开机自启偏好失败", ex);
            return null;
        }
    }

    /// <remarks>
    /// Written to a sibling then moved. <c>launchd</c> watches this directory, and a
    /// partially written plist is a plist it will reject — leaving autostart broken
    /// until the next time the user toggles it.
    /// </remarks>
    private static void WriteAtomically(string path, string content)
    {
        string temporary = path + ".tmp";
        File.WriteAllText(temporary, content);
        File.Move(temporary, path, overwrite: true);
    }

    /// <remarks>
    /// Without this the agent only takes effect at the next login, so the checkbox
    /// would appear to do nothing today and something tomorrow. Failure is ignored on
    /// purpose: the plist is already on disk, so login-time startup works regardless,
    /// and there is nothing useful to tell a novice user about launchctl.
    /// </remarks>
    private static void Load(string plistPath) => RunLaunchctl("load", plistPath);

    private static void Unload(string plistPath) => RunLaunchctl("unload", plistPath);

    private static void RunLaunchctl(string verb, string plistPath)
    {
        try
        {
            using Process? process = Process.Start(new ProcessStartInfo("/bin/launchctl")
            {
                ArgumentList = { verb, plistPath },
                UseShellExecute = false,
                RedirectStandardError = true,
                RedirectStandardOutput = true,
            });

            process?.WaitForExit(milliseconds: 5000);
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            ClientLog.Warning($"launchctl {verb} 未成功，开机自启将在下次登录时生效", ex);
        }
    }
}
