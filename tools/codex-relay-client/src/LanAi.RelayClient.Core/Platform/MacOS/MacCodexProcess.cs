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
    /// tested on Windows; what is unverified here is narrow: that <c>open</c> accepts
    /// the discovered app path and that a quit request through Apple Events is granted.
/// </para>
/// <para>
    /// Installation and launch deliberately use the same app bundle paths. Finder opens
    /// the app by path, so using <c>open &lt;path-to-app&gt;</c> matches the action users
    /// know works and avoids depending on a hard-coded ChatGPT bundle identifier.
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
    private const string ApplicationName = "ChatGPT";
    private const string ProcessName = "ChatGPT";

    private static readonly TimeSpan QuitTimeout = TimeSpan.FromSeconds(10);

    private readonly string[] _bundlePaths;

    public MacCodexProcess(string[]? bundlePaths = null)
    {
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
    public bool IsInstalled() => FindInstalledBundlePath() is not null;

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
        if (!RunAndWait("/usr/bin/osascript", ["-e", $"tell application \"{ApplicationName}\" to quit"]))
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

    public bool Launch()
    {
        string? appPath = FindInstalledBundlePath();
        return appPath is not null && RunAndWait("/usr/bin/open", [appPath]);
    }

    private string? FindInstalledBundlePath() =>
        _bundlePaths.FirstOrDefault(Directory.Exists);

    /// <remarks>
    /// <c>ArgumentList</c>, never a shell command line, and the exit code is read
    /// rather than assumed so an OS-level launch failure is surfaced to the caller.
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
