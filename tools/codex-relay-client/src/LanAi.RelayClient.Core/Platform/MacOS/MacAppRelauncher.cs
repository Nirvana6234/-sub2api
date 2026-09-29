using System.ComponentModel;
using System.Diagnostics;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// Opens this app again once the current process has exited — what macOS asks for after the user
/// grants 「屏幕录制」: the permission reaches the app only when it starts anew.
/// </summary>
/// <remarks>
/// A small shell waits for this process to go (so the single-instance check does not meet the old
/// one) and then runs <c>open</c> on the bundle. It is a child of this process, but it outlives it:
/// macOS has no kill-on-close job, and the shell is re-parented to launchd. Written without a Mac to
/// run it on, like the rest of the macOS layer.
/// </remarks>
internal static class MacAppRelauncher
{
    /// <summary>The <c>.app</c> bundle this process runs from, or null when it is not in one.</summary>
    /// <param name="baseDirectory"><c>…/共飞-ChatGPT助手.app/Contents/MacOS/</c> in an installed client.</param>
    internal static string? BundleOf(string baseDirectory)
    {
        var directory = new DirectoryInfo(baseDirectory.TrimEnd('/', '\\'));
        if (directory.Name != "MacOS" || directory.Parent is not { Name: "Contents" } contents || contents.Parent is not { } bundle)
        {
            return null;
        }

        return bundle.Name.EndsWith(".app", StringComparison.OrdinalIgnoreCase) ? bundle.FullName : null;
    }

    /// <returns>False when not on macOS, not in a bundle, or the shell would not start.</returns>
    public static bool ScheduleRelaunch()
    {
        if (!OperatingSystem.IsMacOS() || BundleOf(AppContext.BaseDirectory) is not { } bundle)
        {
            return false;
        }

        var info = new ProcessStartInfo("/bin/sh") { UseShellExecute = false, CreateNoWindow = true };
        info.ArgumentList.Add("-c");
        info.ArgumentList.Add("while kill -0 \"$1\" 2>/dev/null; do sleep 0.5; done; exec /usr/bin/open \"$2\"");
        info.ArgumentList.Add("relaunch");
        info.ArgumentList.Add(Environment.ProcessId.ToString(System.Globalization.CultureInfo.InvariantCulture));
        info.ArgumentList.Add(bundle);
        try
        {
            using Process? process = Process.Start(info);
            return process is not null;
        }
        catch (Exception ex) when (ex is Win32Exception or InvalidOperationException or IOException)
        {
            ClientLog.Warning("安排重新启动失败", ex);
            return false;
        }
    }
}
