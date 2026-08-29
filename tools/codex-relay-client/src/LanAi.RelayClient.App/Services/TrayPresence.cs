using Avalonia.Controls;
using Avalonia.Platform;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.App.Services;

/// <summary>
/// The client's presence in the notification area — the Windows tray, the macOS menu bar.
/// </summary>
/// <remarks>
/// <para>
/// The Avalonia counterpart of the WinForms <c>NotifyIcon</c> version. It exists for one
/// load-bearing reason: without it, closing the window ends the process, and the panel's
/// own instruction — 保持客户端运行，ChatGPT 才能继续使用共飞额度 — is contradicted by the
/// window's own close button.
/// </para>
/// <para>
/// <b>Notifications do not live here.</b> Avalonia's <see cref="TrayIcon"/> has no
/// notification API and exposes neither the window handle nor the icon id a balloon
/// would need, so the two that carry real information — new announcements and low
/// balance — go through <see cref="INotificationPresenter"/> instead, which has a
/// Windows and a macOS implementation behind one call.
/// </para>
/// <para>
/// The third balloon the WinForms tray had is not one of them. The first hide-to-tray
/// hint is shown exactly once, ever, and a dialog says that more clearly than a banner
/// the user may never see — so <see cref="ClaimFirstHideHint"/> stayed here.
/// </para>
/// </remarks>
internal sealed class TrayPresence : IDisposable
{
    private readonly TrayIcon _icon;
    private readonly NativeMenuItem _statusItem;
    private readonly NativeMenuItem _announcementItem;
    private readonly string _firstHideMarkerPath;
    private bool _disposed;

    public TrayPresence(
        Action onShowWindow,
        Action onStartCodex,
        Func<Task> onExit,
        Action? onShowAnnouncements = null,
        string? firstHideMarkerPath = null)
    {
        ArgumentNullException.ThrowIfNull(onShowWindow);
        ArgumentNullException.ThrowIfNull(onStartCodex);
        ArgumentNullException.ThrowIfNull(onExit);

        // Deliberately the same marker the WinForms tray used. A user upgrading from
        // the WPF client has already been told the client keeps running; telling them
        // again would read as the upgrade having forgotten who they are.
        _firstHideMarkerPath = firstHideMarkerPath ?? AppPaths.InData("tray-tip-shown");

        // Not clickable: it states which group is billing, which is the one thing a
        // user needs from the tray without opening anything.
        _statusItem = new NativeMenuItem("未登录") { IsEnabled = false };

        // Present even with nothing unread, so the tray is a dependable way in rather
        // than an entry point that appears only when there is news.
        _announcementItem = new NativeMenuItem("公告") { IsEnabled = onShowAnnouncements is not null };
        _announcementItem.Click += (_, _) => onShowAnnouncements?.Invoke();

        var showItem = new NativeMenuItem("显示主界面");
        showItem.Click += (_, _) => onShowWindow();

        var startItem = new NativeMenuItem("启动 ChatGPT");
        startItem.Click += (_, _) => onStartCodex();

        var exitItem = new NativeMenuItem("退出");
        exitItem.Click += async (_, _) => await onExit().ConfigureAwait(true);

        var menu = new NativeMenu();
        menu.Add(_statusItem);
        menu.Add(new NativeMenuItemSeparator());
        menu.Add(showItem);
        menu.Add(startItem);
        menu.Add(_announcementItem);
        menu.Add(new NativeMenuItemSeparator());
        menu.Add(exitItem);

        _icon = new TrayIcon
        {
            Icon = LoadIcon(),
            ToolTipText = "共飞-ChatGPT助手",
            Menu = menu,
            IsVisible = true,
        };

        _icon.Clicked += (_, _) => onShowWindow();
    }

    /// <summary>Updates the status line and hover tooltip.</summary>
    /// <remarks>
    /// The 63-character cap the WinForms tooltip had was a Win32 limit that threw
    /// rather than truncating. Avalonia does not have it, but the text is still kept
    /// short here: a tooltip long enough to need wrapping is not readable at a glance,
    /// which is the only thing this line is for.
    /// </remarks>
    public void UpdateStatus(string statusLine)
    {
        if (string.IsNullOrWhiteSpace(statusLine))
        {
            return;
        }

        _statusItem.Header = statusLine;
        _icon.ToolTipText = statusLine.Length > 63 ? statusLine[..63] : statusLine;
    }

    public void UpdateAnnouncements(string label) =>
        _announcementItem.Header = string.IsNullOrWhiteSpace(label) ? "公告" : label;

    /// <summary>
    /// Whether the "still running" hint is still owed to this user.
    /// </summary>
    /// <remarks>
    /// The marker is written here, and the caller shows the message, because showing it
    /// needs a window to own the dialog and this class has none. Returning false on a
    /// write failure would risk repeating the hint forever; returning true repeats it at
    /// worst once more, which is the cheaper mistake.
    /// </remarks>
    public bool ClaimFirstHideHint()
    {
        if (File.Exists(_firstHideMarkerPath))
        {
            return false;
        }

        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_firstHideMarkerPath)!);
            File.WriteAllText(_firstHideMarkerPath, DateTimeOffset.Now.ToString("o"));
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("无法记录托盘首次提示标记", ex);
        }

        return true;
    }

    /// <remarks>
    /// <para>
    /// <b>A PNG, not the .ico the window used to load.</b> Avalonia decodes with Skia,
    /// which is not obliged to handle .ico off Windows — and this method falls back to
    /// <c>null</c> rather than throwing, so a decode miss on macOS would surface as a
    /// blank spot in the menu bar with nothing to explain it. Both files are generated
    /// from the same source art by <c>packaging/macos/build-icns.py</c>, so the two
    /// platforms cannot show different icons.
    /// </para>
    /// <para>
    /// The fallback itself is kept: a tray entry with no icon is still clickable and
    /// still keeps the client alive, whereas throwing here would take the whole client
    /// down over a missing resource.
    /// </para>
    /// </remarks>
    private static WindowIcon? LoadIcon()
    {
        try
        {
            using Stream stream = AssetLoader.Open(
                new Uri("avares://LanAi.RelayClient.App/Assets/LanAi.RelayClient.png"));
            return new WindowIcon(stream);
        }
        catch (Exception ex) when (ex is FileNotFoundException or ArgumentException)
        {
            ClientLog.Warning("托盘图标加载失败，使用空图标继续", ex);
            return null;
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _icon.IsVisible = false;
        _icon.Dispose();
    }
}
