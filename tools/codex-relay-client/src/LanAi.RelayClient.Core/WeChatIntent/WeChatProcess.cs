using System.Diagnostics;
using System.Runtime.InteropServices;

namespace LanAi.RelayClient.WeChatIntent;

/// <summary>Whether WeChat 4.x is running, and whether the window in front is this client's.</summary>
internal static class WeChatProcess
{
    /// <summary>
    /// WeChat 4.x is running (docs §3.2): on Windows a <c>Weixin</c> process with a main window;
    /// on macOS a <c>WeChat</c> process (the executable of com.tencent.xinWeChat — .NET has no
    /// main-window handle there, so the reader decides whether a window is up). Whether it is
    /// signed in or still on the login screen cannot be told yet (§10.2); the reader then finds
    /// no chat area.
    /// </summary>
    public static bool IsRunning()
    {
        if (!OperatingSystem.IsWindows() && !OperatingSystem.IsMacOS())
        {
            return false;
        }

        Process[] processes = Process.GetProcessesByName(OperatingSystem.IsWindows() ? "Weixin" : "WeChat");
        try
        {
            return OperatingSystem.IsWindows() ? processes.Any(HasWindow) : processes.Length > 0;
        }
        finally
        {
            foreach (Process process in processes)
            {
                process.Dispose();
            }
        }
    }

    /// <summary>
    /// The foreground window belongs to this process — a card or the main window. Windows only;
    /// on macOS the reader reports the front process instead (<see cref="ReaderEvent.FrontPid"/>).
    /// </summary>
    public static bool ForegroundIsOurs()
    {
        if (!OperatingSystem.IsWindows())
        {
            return false;
        }

        IntPtr foreground = GetForegroundWindow();
        if (foreground == IntPtr.Zero)
        {
            return false;
        }

        GetWindowThreadProcessId(foreground, out uint processId);
        return processId == (uint)Environment.ProcessId;
    }

    private static bool HasWindow(Process process)
    {
        try
        {
            return process.MainWindowHandle != IntPtr.Zero;
        }
        catch (InvalidOperationException)
        {
            return false;
        }
    }

    [DllImport("user32.dll")]
    private static extern IntPtr GetForegroundWindow();

    [DllImport("user32.dll")]
    private static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint processId);
}
