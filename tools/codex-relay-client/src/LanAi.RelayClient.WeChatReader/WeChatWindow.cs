using System.Diagnostics;
using System.Runtime.InteropServices;

namespace LanAi.RelayClient.WeChatReader;

/// <summary>Finds WeChat's main window and reports where it is and whether it is in front.</summary>
/// <remarks>
/// Polled rather than hooked (<c>SetWinEventHook</c>): a hook needs a message loop on its
/// thread, and a 200 ms poll of three cheap calls is well within what phase 1 needs.
/// Only the 4.x client (<c>Weixin.exe</c>) is recognised; 3.x (<c>WeChat.exe</c>) draws a
/// different interface and is out of scope (docs §4.3).
/// </remarks>
internal sealed class WeChatWindow
{
    private const string ProcessName = "Weixin";
    private static readonly TimeSpan RefindInterval = TimeSpan.FromSeconds(2);

    private IntPtr _hwnd;
    private DateTime _lastFind = DateTime.MinValue;

    public IntPtr Handle => _hwnd;

    /// <summary>Refreshes the handle when it has gone stale, at most every two seconds.</summary>
    public void Refresh()
    {
        if (_hwnd != IntPtr.Zero && IsWindow(_hwnd) && IsWindowVisible(_hwnd))
        {
            return;
        }

        DateTime now = DateTime.UtcNow;
        if (now - _lastFind < RefindInterval)
        {
            return;
        }

        _lastFind = now;
        _hwnd = Find();
    }

    public string State()
    {
        if (_hwnd == IntPtr.Zero || !IsWindow(_hwnd))
        {
            return LanAi.RelayClient.WeChatIntent.ReaderEvent.StateGone;
        }

        if (IsIconic(_hwnd))
        {
            return LanAi.RelayClient.WeChatIntent.ReaderEvent.StateMinimized;
        }

        IntPtr foreground = GetForegroundWindow();
        bool inFront = foreground == _hwnd || GetAncestor(foreground, GaRootOwner) == _hwnd;
        return inFront
            ? LanAi.RelayClient.WeChatIntent.ReaderEvent.StateForeground
            : LanAi.RelayClient.WeChatIntent.ReaderEvent.StateBackground;
    }

    /// <summary>The visible bounds, without the invisible resize border GetWindowRect includes.</summary>
    public (int X, int Y, int W, int H)? Bounds()
    {
        if (_hwnd == IntPtr.Zero)
        {
            return null;
        }

        if (DwmGetWindowAttribute(_hwnd, DwmwaExtendedFrameBounds, out Rect r, Marshal.SizeOf<Rect>()) != 0
            && !GetWindowRect(_hwnd, out r))
        {
            return null;
        }

        return (r.Left, r.Top, r.Right - r.Left, r.Bottom - r.Top);
    }

    /// <summary>The process owning the foreground window, for the client to recognise its own cards.</summary>
    public static int? FrontPid()
    {
        IntPtr foreground = GetForegroundWindow();
        if (foreground == IntPtr.Zero)
        {
            return null;
        }

        GetWindowThreadProcessId(foreground, out uint processId);
        return processId == 0 ? null : (int)processId;
    }

    public double Scale()
    {
        if (_hwnd == IntPtr.Zero)
        {
            return 1.0;
        }

        uint dpi = GetDpiForWindow(_hwnd);
        return dpi == 0 ? 1.0 : dpi / 96.0;
    }

    /// <summary>
    /// The visible top-level window of a Weixin process — the one in front when there are
    /// several (a second instance, or a pop-out chat window, which is not read in phase 1).
    /// </summary>
    private static IntPtr Find()
    {
        var candidates = new List<IntPtr>();
        foreach (Process process in Process.GetProcessesByName(ProcessName))
        {
            using (process)
            {
                try
                {
                    IntPtr main = process.MainWindowHandle;
                    if (main != IntPtr.Zero && IsWindowVisible(main))
                    {
                        candidates.Add(main);
                    }
                }
                catch (InvalidOperationException)
                {
                    // Exited between the listing and the read.
                }
            }
        }

        if (candidates.Count == 0)
        {
            return IntPtr.Zero;
        }

        IntPtr foreground = GetForegroundWindow();
        return candidates.Contains(foreground) ? foreground : candidates[0];
    }

    private const uint GaRootOwner = 3;
    private const int DwmwaExtendedFrameBounds = 9;

    [StructLayout(LayoutKind.Sequential)]
    private struct Rect
    {
        public int Left;
        public int Top;
        public int Right;
        public int Bottom;
    }

    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool IsWindow(IntPtr hwnd);

    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool IsWindowVisible(IntPtr hwnd);

    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool IsIconic(IntPtr hwnd);

    [DllImport("user32.dll")]
    private static extern IntPtr GetForegroundWindow();

    [DllImport("user32.dll")]
    private static extern IntPtr GetAncestor(IntPtr hwnd, uint flags);

    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetWindowRect(IntPtr hwnd, out Rect rect);

    [DllImport("user32.dll")]
    private static extern uint GetDpiForWindow(IntPtr hwnd);

    [DllImport("user32.dll")]
    private static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint processId);

    [DllImport("dwmapi.dll")]
    private static extern int DwmGetWindowAttribute(IntPtr hwnd, int attribute, out Rect value, int size);
}
