using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.Versioning;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// Drives the ChatGPT app through <c>open</c> and <c>osascript</c>.
/// </summary>
/// <remarks>
/// <para>
/// <b>Written without a Mac to run it on.</b> The decision table that uses it is
/// tested on Windows; what is unverified here is narrow: the bundle identifier, that
/// <c>open -b</c> accepts it, and that a quit request through Apple Events is granted.
/// </para>
/// <para>
/// <b>The bundle identifier is the single most likely thing to be wrong</b>, so it is
/// a constructor parameter rather than a buried constant, and installation is detected
/// from the app bundle on disk instead of from the identifier — a wrong identifier
/// then shows up as "ChatGPT 未安装" on a machine where it plainly is, which is a
/// legible symptom, rather than as a launch that silently does nothing.
/// </para>
/// <para>
/// Quitting goes through Apple Events (<c>tell application … to quit</c>) rather than
/// a signal, so the app closes the way it would if the user chose Quit. Killing it
/// would discard whatever the user has in flight, and the whole reason the client asks
/// before restarting is that this is expensive.
/// </para>
/// </remarks>
[SupportedOSPlatform("macos")]
internal sealed class MacCodexProcess : IMacCodexProcess
{
    /// <remarks>Unverified. See the class remarks — this is the value to check first.</remarks>
    public const string DefaultBundleIdentifier = "com.openai.chat";

    private const string ProcessName = "ChatGPT";

    private static readonly TimeSpan QuitTimeout = TimeSpan.FromSeconds(10);

    private readonly string _bundleIdentifier;
    private readonly string[] _bundlePaths;

    public MacCodexProcess(string? bundleIdentifier = null, string[]? bundlePaths = null)
    {
        _bundleIdentifier = bundleIdentifier ?? DefaultBundleIdentifier;
        _bundlePaths = bundlePaths ??
        [
            "/Applications/ChatGPT.app",
            Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
                "Applications",
                "ChatGPT.app"),
        ];
    }

    /// <remarks>
    /// A <c>.app</c> is a directory, so this is a directory check. Both the system and
    /// the per-user location are considered: a Mac user who cannot write
    /// <c>/Applications</c> installs into their own, and treating that as "not
    /// installed" would send them to download an app they already have.
    /// </remarks>
    public bool IsInstalled() => _bundlePaths.Any(Directory.Exists);

    public bool IsRunning()
    {
        try
        {
            return Process.GetProcessesByName(ProcessName).Length > 0;
        }
        catch (Exception ex) when (ex is InvalidOperationException or PlatformNotSupportedException)
        {
            // Treated as not running. The consequence of being wrong here is a launch
            // request for an app that is already up, which macOS answers by bringing it
            // forward — harmless. Claiming it is running would instead block the user
            // behind a restart prompt they cannot satisfy.
            ClientLog.Warning("无法检查 ChatGPT 是否在运行", ex);
            return false;
        }
    }

    public bool Quit()
    {
        if (!RunAndWait("/usr/bin/osascript", ["-e", $"tell application id \"{_bundleIdentifier}\" to quit"]))
        {
            return false;
        }

        // Quitting is asynchronous even when the event is accepted: the app decides
        // when to go. Reporting success while it is still up would have the client
        // relaunch into an instance that never left.
        var deadline = DateTime.UtcNow + QuitTimeout;
        while (DateTime.UtcNow < deadline)
        {
            if (!IsRunning())
            {
                return true;
            }

            Thread.Sleep(200);
        }

        return false;
    }

    public bool Launch() => RunAndWait("/usr/bin/open", ["-b", _bundleIdentifier]);

    /// <remarks>
    /// <c>ArgumentList</c>, never a shell command line, and the exit code is read
    /// rather than assumed: <c>open</c> reports an unknown bundle identifier by
    /// failing, and that is the only signal that the identifier is wrong.
    /// </remarks>
    private static bool RunAndWait(string fileName, string[] arguments)
    {
        try
        {
            var startInfo = new ProcessStartInfo(fileName)
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardError = true,
            };

            foreach (string argument in arguments)
            {
                startInfo.ArgumentList.Add(argument);
            }

            using Process? process = Process.Start(startInfo);
            if (process is null)
            {
                return false;
            }

            process.WaitForExit((int)TimeSpan.FromSeconds(20).TotalMilliseconds);
            if (!process.HasExited)
            {
                ClientLog.Warning($"{fileName} 未在预期时间内结束");
                return false;
            }

            if (process.ExitCode != 0)
            {
                ClientLog.Warning(
                    $"{fileName} 返回 {process.ExitCode}：{process.StandardError.ReadToEnd().Trim()}");
                return false;
            }

            return true;
        }
        catch (Exception ex) when (ex is Win32Exception
            or InvalidOperationException
            or PlatformNotSupportedException
            or FileNotFoundException)
        {
            ClientLog.Warning($"执行 {fileName} 失败", ex);
            return false;
        }
    }
}
