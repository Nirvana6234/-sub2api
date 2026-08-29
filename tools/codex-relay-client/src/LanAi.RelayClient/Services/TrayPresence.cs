using System.Drawing;
using System.Globalization;
using System.IO;
using System.Windows.Forms;

using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>
/// Keeps the client alive in the notification area (F9.3, F9.4).
/// </summary>
/// <remarks>
/// <para>
/// This is not a convenience. The managed key is a one-day lease that only stays
/// alive while the client is running to renew it (F3.2), so a client that exits
/// when its window closes gives the user a Codex that works today and fails
/// tomorrow — the failure the requirements single out as the one users will read
/// as a bug in the product.
/// </para>
/// <para>
/// Uses the Windows Forms notification icon rather than a package: it ships with
/// the desktop runtime the app already needs, so it costs the installer nothing.
/// </para>
/// </remarks>
internal sealed class TrayPresence : IDisposable
{
    private readonly NotifyIcon _icon;
    private readonly ToolStripMenuItem _statusItem;
    private readonly ToolStripMenuItem _announcementItem;
    private readonly string _firstHideMarkerPath;

    /// <summary>
    /// What clicking the balloon currently on screen should do.
    /// </summary>
    /// <remarks>
    /// <see cref="NotifyIcon.BalloonTipClicked"/> belongs to the icon, not to a
    /// balloon: it fires for whichever tip was shown last. Since this icon shows
    /// three different kinds, the action has to travel with the tip, or a click on
    /// the low-balance reminder would open the announcement reader.
    /// </remarks>
    private Action? _pendingBalloonClick;

    private bool _disposed;

    /// <param name="onShowWindow">Brings the main window back.</param>
    /// <param name="onStartCodex">Runs the same action as the main button.</param>
    /// <param name="onExit">Ends the process for real; only the menu may do this.</param>
    /// <param name="onShowAnnouncements">Opens the announcement reader.</param>
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

        _firstHideMarkerPath = firstHideMarkerPath ?? DefaultMarkerPath();

        // Not clickable: it states which group is billing, which is the one thing
        // a user needs from the tray without opening anything.
        _statusItem = new ToolStripMenuItem("未登录") { Enabled = false };

        // Present even with nothing unread, so the tray is a dependable way in
        // rather than an entry point that appears only when there is news.
        _announcementItem = new ToolStripMenuItem("公告", null, (_, _) => onShowAnnouncements?.Invoke())
        {
            Enabled = onShowAnnouncements is not null,
        };

        var menu = new ContextMenuStrip();
        menu.Items.Add(_statusItem);
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add("显示主界面", null, (_, _) => onShowWindow());
        menu.Items.Add("启动 ChatGPT", null, (_, _) => onStartCodex());
        menu.Items.Add(_announcementItem);
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add("退出", null, async (_, _) => await onExit().ConfigureAwait(true));

        _icon = new NotifyIcon
        {
            Icon = LoadIcon(),
            Text = "共飞-ChatGPT助手",
            ContextMenuStrip = menu,
            Visible = true,
        };

        _icon.DoubleClick += (_, _) => onShowWindow();
        _icon.BalloonTipClicked += (_, _) =>
        {
            Action? pending = _pendingBalloonClick;
            _pendingBalloonClick = null;
            pending?.Invoke();
        };

        // Cleared when the tip goes away on its own, so a click on the next one
        // cannot run the previous one's action.
        _icon.BalloonTipClosed += (_, _) => _pendingBalloonClick = null;
    }

    /// <summary>Updates the status line and hover tooltip.</summary>
    /// <remarks>
    /// The tooltip is capped at 63 characters by Windows; a longer string throws
    /// rather than truncating, which would turn a cosmetic update into a crash.
    /// </remarks>
    public void UpdateStatus(string statusLine)
    {
        string text = string.IsNullOrWhiteSpace(statusLine) ? "共飞-ChatGPT助手" : statusLine;

        _statusItem.Text = text;
        _icon.Text = text.Length <= 63 ? text : text[..63];
    }

    /// <summary>
    /// Tells the user once that closing the window did not close the program.
    /// </summary>
    /// <remarks>
    /// Shown only the first time, ever — the marker survives restarts. Repeating
    /// it would train the user to dismiss notifications from this app without
    /// reading them, which is worse than never showing it.
    /// </remarks>
    public void NotifyStillRunningOnce()
    {
        if (File.Exists(_firstHideMarkerPath))
        {
            return;
        }

        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_firstHideMarkerPath)!);
            File.WriteAllText(_firstHideMarkerPath, DateTimeOffset.Now.ToString("o"));
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Worst case the tip appears again next time; not worth failing over.
            ClientLog.Warning("无法记录托盘首次提示标记", ex);
        }

        ShowBalloon(
            "共飞-ChatGPT助手仍在运行",
            "已最小化到托盘。保持运行，ChatGPT 才能继续使用共飞额度。",
            ToolTipIcon.Info);
    }

    /// <summary>Shows a low-balance reminder while the client is in use.</summary>
    public void NotifyLowBalance(double balance)
    {
        string balanceText = balance.ToString("0.####", CultureInfo.InvariantCulture);
        ShowBalloon(
            "共飞-ChatGPT助手余额提醒",
            $"检测到 ChatGPT 正在使用，当前余额仅 ¥{balanceText}，请及时充值。",
            ToolTipIcon.Warning);
    }

    /// <summary>Updates the announcement menu entry with the unread count.</summary>
    public void UpdateAnnouncements(string label)
    {
        _announcementItem.Text = string.IsNullOrWhiteSpace(label) ? "公告" : label;
    }

    /// <summary>
    /// Tells the user that announcements have arrived.
    /// </summary>
    /// <remarks>
    /// One balloon however many arrived: a tip per announcement would bury the
    /// screen for a batch the operator published together. Windows may suppress
    /// this entirely — focus assist, or notifications turned off for this app —
    /// so it is a convenience on top of the unread badge, never the only way to
    /// find out.
    /// </remarks>
    public void NotifyNewAnnouncement(int count, string? latestTitle, Action? onClick = null)
    {
        string title = count > 1 ? $"共飞-ChatGPT助手有 {count} 条新公告" : "共飞-ChatGPT助手有新公告";
        string body = string.IsNullOrWhiteSpace(latestTitle)
            ? "点击查看。"
            : $"{Shorten(latestTitle)}\r\n点击查看。";

        ShowBalloon(title, body, ToolTipIcon.Info, onClick);
    }

    /// <remarks>
    /// Every balloon goes through here so <see cref="_pendingBalloonClick"/> is
    /// always rewritten — including to null for the tips that do nothing when
    /// clicked. Setting it only where a click matters would leave a stale action
    /// armed behind the next tip.
    /// </remarks>
    private void ShowBalloon(string title, string body, ToolTipIcon icon, Action? onClick = null)
    {
        _pendingBalloonClick = onClick;
        _icon.ShowBalloonTip(5000, title, body, icon);
    }

    /// <remarks>Windows truncates a long balloon body; trimming keeps the tail ours.</remarks>
    private static string Shorten(string text) =>
        text.Length <= 60 ? text : text[..60] + "…";

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;

        // Hidden before disposal: an icon disposed while visible can be left
        // behind in the notification area until the user hovers over it.
        _icon.Visible = false;
        _icon.ContextMenuStrip?.Dispose();
        _icon.Dispose();
    }

    private static string DefaultMarkerPath() => AppPaths.InData("tray-tip-shown");

    /// <remarks>
    /// Falls back to a stock icon rather than failing: an unrecognisable tray icon
    /// is a blemish, but no tray icon at all would leave the user with a process
    /// they cannot reach or quit.
    /// </remarks>
    private static Icon LoadIcon()
    {
        try
        {
            string? executable = Environment.ProcessPath;
            if (executable is not null)
            {
                Icon? extracted = Icon.ExtractAssociatedIcon(executable);
                if (extracted is not null)
                {
                    return extracted;
                }
            }
        }
        catch (Exception ex) when (ex is ArgumentException or IOException)
        {
            ClientLog.Warning("无法读取程序图标，托盘使用系统默认图标", ex);
        }

        return SystemIcons.Application;
    }
}
