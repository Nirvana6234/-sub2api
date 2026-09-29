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
    private IntPtr Find()
    {
        var candidates = new List<IntPtr>();
        var pids = new HashSet<uint>();
        foreach (Process process in Process.GetProcessesByName(ProcessName))
        {
            using (process)
            {
                try
                {
                    pids.Add((uint)process.Id);
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

        IntPtr foreground = GetForegroundWindow();
        IntPtr chosen = candidates.Count == 0 ? IntPtr.Zero : candidates.Contains(foreground) ? foreground : candidates[0];
        LogFind(chosen, pids, candidates.Count);
        return chosen;
    }

    private IntPtr _loggedChoice = new(-1);
    private string? _loggedCounts;

    /// <summary>
    /// When the choice changes: which window was taken, and every visible top-level window the
    /// Weixin processes have — so a log shows whether the right one was picked.
    /// </summary>
    private void LogFind(IntPtr chosen, HashSet<uint> pids, int candidates)
    {
        string counts = $"Weixin 进程 {pids.Count} 个，主窗口候选 {candidates} 个";
        if (chosen == _loggedChoice && counts == _loggedCounts)
        {
            return;
        }

        _loggedChoice = chosen;
        _loggedCounts = counts;
        Diag.Log(chosen == IntPtr.Zero ? $"查找微信窗口：{counts}，没有可用窗口" : $"查找微信窗口：{counts}，选中 {Describe(chosen)}");
        List<IntPtr> all = TopLevelWindowsOf(pids);
        foreach (IntPtr hwnd in all.Take(10))
        {
            Diag.Log($"  微信的顶层窗口：{Describe(hwnd)}");
        }

        if (all.Count > 10)
        {
            Diag.Log($"  ……另有 {all.Count - 10} 个");
        }
    }

    /// <summary>Class, size, visibility and owner of a window — never its title, which can name a chat.</summary>
    public static string Describe(IntPtr hwnd)
    {
        if (hwnd == IntPtr.Zero)
        {
            return "（无）";
        }

        if (!IsWindow(hwnd))
        {
            return $"0x{hwnd.ToInt64():X} 已失效";
        }

        var name = new char[256];
        int length = GetClassName(hwnd, name, name.Length);
        string cls = length > 0 ? new string(name, 0, length) : "?";
        GetWindowRect(hwnd, out Rect r);
        bool cloaked = DwmGetWindowAttribute(hwnd, DwmwaCloaked, out int cloak, sizeof(int)) == 0 && cloak != 0;
        GetWindowThreadProcessId(hwnd, out uint pid);
        return $"0x{hwnd.ToInt64():X} 类 {cls} 位置 {r.Left},{r.Top} 尺寸 {r.Right - r.Left}x{r.Bottom - r.Top}"
            + $" 可见 {(IsWindowVisible(hwnd) ? "是" : "否")} 最小化 {(IsIconic(hwnd) ? "是" : "否")} 被遮蔽 {(cloaked ? "是" : "否")}"
            + $" pid {pid} DPI {GetDpiForWindow(hwnd)}";
    }

    /// <summary>The window in front and its process name, for telling why WeChat does not count as in front.</summary>
    public static string DescribeForeground()
    {
        IntPtr foreground = GetForegroundWindow();
        if (foreground == IntPtr.Zero)
        {
            return "（无）";
        }

        GetWindowThreadProcessId(foreground, out uint pid);
        string process = "?";
        try
        {
            using Process p = Process.GetProcessById((int)pid);
            process = p.ProcessName;
        }
        catch (Exception ex) when (ex is ArgumentException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
        }

        IntPtr root = GetAncestor(foreground, GaRootOwner);
        return $"进程 {process}，{Describe(foreground)}{(root != foreground ? $"，根窗口 0x{root.ToInt64():X}" : string.Empty)}";
    }

    [ThreadStatic]
    private static List<IntPtr>? _enumerated;

    [ThreadStatic]
    private static HashSet<uint>? _enumeratePids;

    private static unsafe List<IntPtr> TopLevelWindowsOf(HashSet<uint> pids)
    {
        _enumerated = [];
        _enumeratePids = pids;
        try
        {
            EnumWindows(&CollectWindow, IntPtr.Zero);
            return _enumerated;
        }
        finally
        {
            _enumerated = null;
            _enumeratePids = null;
        }
    }

    [UnmanagedCallersOnly(CallConvs = [typeof(System.Runtime.CompilerServices.CallConvStdcall)])]
    private static int CollectWindow(IntPtr hwnd, IntPtr lParam)
    {
        GetWindowThreadProcessId(hwnd, out uint pid);
        if (_enumeratePids is { } pids && pids.Contains(pid) && IsWindowVisible(hwnd))
        {
            _enumerated?.Add(hwnd);
        }

        return 1;
    }

    private const uint GaRootOwner = 3;
    private const int DwmwaExtendedFrameBounds = 9;
    private const int DwmwaCloaked = 14;

    [DllImport("user32.dll")]
    private static extern unsafe int EnumWindows(delegate* unmanaged[Stdcall]<IntPtr, IntPtr, int> callback, IntPtr lParam);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetClassNameW")]
    private static extern int GetClassName(IntPtr hwnd, [Out] char[] name, int count);

    [DllImport("dwmapi.dll", EntryPoint = "DwmGetWindowAttribute")]
    private static extern int DwmGetWindowAttribute(IntPtr hwnd, int attribute, out int value, int size);

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
