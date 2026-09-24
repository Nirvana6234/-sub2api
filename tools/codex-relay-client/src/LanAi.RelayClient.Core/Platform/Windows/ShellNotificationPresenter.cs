using System.Runtime.InteropServices;
using System.Runtime.Versioning;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform.Windows;

/// <summary>
/// Windows desktop notifications, via a notification-area entry of the client's own.
/// </summary>
/// <remarks>
/// <para>
/// <b>Why a second icon rather than the one already in the tray.</b> Balloons belong
/// to a notification-area entry, addressed by the <c>hWnd</c> and <c>uID</c> it was
/// registered with. Avalonia's <c>TrayIcon</c> exposes neither, and reaching for them
/// by reflection is exactly the pattern this project removed everywhere else to keep
/// the publish trimmable. So this class registers an entry of its own and uses it
/// purely as a notification channel.
/// </para>
/// <para>
/// That entry carries <c>NIS_HIDDEN</c>, so the user still sees exactly one icon — the
/// Avalonia one, with the menu on it. A hidden entry still shows balloons: that was
/// measured, not assumed, on Windows 11 26200 before this class was written, together
/// with the message-only owner window and both notification-icon versions.
/// </para>
/// <para>
/// <b>The owner window must be created on a thread that pumps messages, and the same
/// one for its whole life.</b> The shell delivers the click callback with
/// <c>SendNotifyMessage</c> semantics, straight into the window procedure rather than
/// through the message queue — which is why <see cref="NotificationRequest.OnActivated"/>
/// runs on the UI thread, and why constructing this off the UI thread would leave
/// clicks undelivered while everything else appeared to work.
/// </para>
/// </remarks>
[SupportedOSPlatform("windows")]
internal sealed class ShellNotificationPresenter : INotificationPresenter
{
    private const int WM_APP = 0x8000;
    private const int CallbackMessage = WM_APP + 0x21;
    private const int NIN_BALLOONUSERCLICK = 0x405;

    private const uint NIM_ADD = 0;
    private const uint NIM_MODIFY = 1;
    private const uint NIM_DELETE = 2;

    private const uint NIF_MESSAGE = 0x01;
    private const uint NIF_ICON = 0x02;
    private const uint NIF_TIP = 0x04;
    private const uint NIF_STATE = 0x08;
    private const uint NIF_INFO = 0x10;

    private const uint NIS_HIDDEN = 0x01;

    private const uint NIIF_INFO = 0x01;
    private const uint NIIF_WARNING = 0x02;

    private const int IDI_APPLICATION = 32512;
    private const uint IconId = 1;
    private const string ClassName = "LanAiRelayClientNotifications";

    /// <summary>Held in a field so the GC cannot collect the thunk Win32 holds.</summary>
    private readonly WindowProcedure _procedure;

    private readonly IntPtr _window;
    private readonly bool _registered;
    private bool _classRegistered;

    private Action? _pendingClick;
    private bool _disposed;

    public ShellNotificationPresenter()
    {
        _procedure = OnMessage;
        _window = CreateOwnerWindow();

        if (_window == IntPtr.Zero)
        {
            ClientLog.Warning("无法创建通知窗口，桌面通知将不可用");
            return;
        }

        NOTIFYICONDATAW data = Describe();
        data.uFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP | NIF_STATE;
        data.uCallbackMessage = CallbackMessage;
        data.hIcon = LoadIconW(IntPtr.Zero, IDI_APPLICATION);
        data.szTip = "共飞-ChatGPT助手";

        // Hidden from the moment it is added, so it never flickers into the tray
        // alongside the icon the user is meant to see.
        data.dwState = NIS_HIDDEN;
        data.dwStateMask = NIS_HIDDEN;

        _registered = Shell_NotifyIconW(NIM_ADD, ref data);
        if (!_registered)
        {
            ClientLog.Warning("无法注册通知项，桌面通知将不可用");
        }
    }

    public void Show(NotificationRequest request)
    {
        if (_disposed || !_registered)
        {
            return;
        }

        try
        {
            // Rewritten on every notification, including to null. Setting it only
            // where a click matters would leave the previous notification's action
            // armed behind the next one — the same trap the WinForms tray documented.
            _pendingClick = request.OnActivated;

            NOTIFYICONDATAW data = Describe();
            data.uFlags = NIF_INFO;
            data.dwInfoFlags = request.Severity == NotificationSeverity.Warning ? NIIF_WARNING : NIIF_INFO;
            data.szInfoTitle = Fit(request.Title, 63);
            data.szInfo = Fit(request.Body, 255);

            if (!Shell_NotifyIconW(NIM_MODIFY, ref data))
            {
                ClientLog.Warning("桌面通知发送失败：" + request.Title);
            }
        }
        catch (Exception ex) when (ex is EntryPointNotFoundException or DllNotFoundException)
        {
            ClientLog.Warning("桌面通知不可用", ex);
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _pendingClick = null;

        if (_registered)
        {
            NOTIFYICONDATAW data = Describe();
            Shell_NotifyIconW(NIM_DELETE, ref data);
        }

        if (_window != IntPtr.Zero)
        {
            DestroyWindow(_window);
        }

        // After the window, never before: the class cannot be unregistered while a
        // window of it exists. Releasing it is what makes a later presenter able to
        // register its own procedure rather than inherit this one's dead thunk.
        if (_classRegistered)
        {
            _classRegistered = false;
            UnregisterClassW(ClassName, GetModuleHandleW(null));
        }
    }

    private NOTIFYICONDATAW Describe() => new()
    {
        cbSize = (uint)Marshal.SizeOf<NOTIFYICONDATAW>(),
        hWnd = _window,
        uID = IconId,
        szTip = string.Empty,
        szInfo = string.Empty,
        szInfoTitle = string.Empty,
    };

    /// <remarks>
    /// <para>
    /// Windows truncates a longer string itself, but silently and at its own boundary.
    /// Trimming here keeps the ellipsis ours, and keeps the fixed-length buffers from
    /// being overrun by an operator-authored announcement title.
    /// </para>
    /// <para>
    /// The cut steps back off a low surrogate. These limits are in UTF-16 units, and
    /// splitting a surrogate pair would put half a code point in the buffer — an emoji
    /// in an announcement title is enough to reach that, and Chinese text never is,
    /// which is exactly why it would survive every plausible manual check.
    /// </para>
    /// </remarks>
    private static string Fit(string? text, int limit)
    {
        if (string.IsNullOrEmpty(text) || text.Length <= limit)
        {
            return text ?? string.Empty;
        }

        int cut = limit - 1;
        if (char.IsLowSurrogate(text[cut]))
        {
            cut--;
        }

        return string.Concat(text.AsSpan(0, cut), "…");
    }

    private IntPtr OnMessage(IntPtr window, uint message, IntPtr wParam, IntPtr lParam)
    {
        // The notification event is in the low word of lParam under every icon
        // version this runs on.
        if (message == CallbackMessage && ((long)lParam & 0xFFFF) == NIN_BALLOONUSERCLICK)
        {
            Action? pending = _pendingClick;
            _pendingClick = null;

            try
            {
                pending?.Invoke();
            }
            catch (Exception ex)
            {
                // This runs inside a window procedure the OS called. Letting an
                // exception escape from here tears down the message loop, and with it
                // the whole client — over a click on a notification.
                ClientLog.Error("通知点击处理失败", ex);
            }
        }

        return DefWindowProcW(window, message, wParam, lParam);
    }

    /// <remarks>
    /// A message-only window: never visible, never in the taskbar, never in Alt-Tab,
    /// and a valid owner for a notification-area entry — measured, alongside the
    /// ordinary hidden top-level window it replaced.
    /// </remarks>
    private IntPtr CreateOwnerWindow()
    {
        const int HWND_MESSAGE = -3;

        try
        {
            var windowClass = new WNDCLASSEXW
            {
                cbSize = (uint)Marshal.SizeOf<WNDCLASSEXW>(),
                lpfnWndProc = Marshal.GetFunctionPointerForDelegate(_procedure),
                hInstance = GetModuleHandleW(null),
                lpszClassName = ClassName,
            };

            // A zero return means the class name is already taken, which can only
            // happen if an earlier presenter in this process failed to unregister.
            // It is NOT harmless: the surviving registration still holds that
            // presenter's window-procedure thunk, and building a window on it would
            // dispatch into a delegate the GC may already have collected — a hard
            // crash, arriving on a click rather than at startup. Dispose unregisters
            // for exactly this reason; if it somehow did not, refuse the window and
            // lose notifications instead.
            if (RegisterClassExW(ref windowClass) == 0)
            {
                ClientLog.Warning("通知窗口类已被占用，本次不注册通知项");
                return IntPtr.Zero;
            }

            _classRegistered = true;

            return CreateWindowExW(
                0, ClassName, ClassName, 0, 0, 0, 0, 0,
                new IntPtr(HWND_MESSAGE), IntPtr.Zero, windowClass.hInstance, IntPtr.Zero);
        }
        catch (Exception ex) when (ex is EntryPointNotFoundException or DllNotFoundException)
        {
            ClientLog.Warning("通知窗口创建失败", ex);
            return IntPtr.Zero;
        }
    }

    private delegate IntPtr WindowProcedure(IntPtr window, uint message, IntPtr wParam, IntPtr lParam);

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct NOTIFYICONDATAW
    {
        public uint cbSize;
        public IntPtr hWnd;
        public uint uID;
        public uint uFlags;
        public uint uCallbackMessage;
        public IntPtr hIcon;

        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)]
        public string szTip;

        public uint dwState;
        public uint dwStateMask;

        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)]
        public string szInfo;

        public uint uVersionOrTimeout;

        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 64)]
        public string szInfoTitle;

        public uint dwInfoFlags;
        public Guid guidItem;
        public IntPtr hBalloonIcon;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct WNDCLASSEXW
    {
        public uint cbSize;
        public uint style;
        public IntPtr lpfnWndProc;
        public int cbClsExtra;
        public int cbWndExtra;
        public IntPtr hInstance;
        public IntPtr hIcon;
        public IntPtr hCursor;
        public IntPtr hbrBackground;

        [MarshalAs(UnmanagedType.LPWStr)]
        public string? lpszMenuName;

        [MarshalAs(UnmanagedType.LPWStr)]
        public string lpszClassName;

        public IntPtr hIconSm;
    }

    [DllImport("shell32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool Shell_NotifyIconW(uint message, ref NOTIFYICONDATAW data);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr LoadIconW(IntPtr instance, int name);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern ushort RegisterClassExW(ref WNDCLASSEXW windowClass);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateWindowExW(
        uint exStyle,
        string className,
        string windowName,
        uint style,
        int x,
        int y,
        int width,
        int height,
        IntPtr parent,
        IntPtr menu,
        IntPtr instance,
        IntPtr param);

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern IntPtr DefWindowProcW(IntPtr window, uint message, IntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool DestroyWindow(IntPtr window);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool UnregisterClassW(string className, IntPtr instance);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr GetModuleHandleW(string? name);
}
