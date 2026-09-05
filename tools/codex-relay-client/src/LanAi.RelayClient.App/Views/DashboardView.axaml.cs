using System.Globalization;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using Avalonia.Threading;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views;

/// <summary>The signed-in surface.</summary>
/// <remarks>
/// <para>
/// Carries the three polling loops the WPF window ran, because they belong to this
/// screen rather than to the application: they start when the dashboard appears and
/// stop when it goes away. In the WPF version they lived on the window alongside the
/// sign-in form, which is why sign-out had to remember to stop each one by hand.
/// </para>
/// <para>
/// Three buttons here lead to screens that are not ported yet — recharge,
/// announcements, and the tray behind sign-out's "minimise" choice. They raise events
/// rather than doing nothing, so the composition root decides what a click means while
/// the port is in progress. A visible button that silently does nothing is the failure
/// this codebase keeps producing; it is not repeated here.
/// </para>
/// </remarks>
public partial class DashboardView : UserControl
{
    /// <summary>How often the cards are refreshed.</summary>
    /// <remarks>
    /// The panel endpoints sit behind a per-user rate limiter, so this interval is a
    /// budget as much as a freshness target. It read 30 seconds during the port — half
    /// the WPF value, changed by nothing more deliberate than my retyping it — which
    /// doubles every client's call rate against that limiter for no gain the user can
    /// see. The dashboard's own <c>IsRateLimited</c> banner is what they would have
    /// got instead.
    /// </remarks>
    private static readonly TimeSpan PollInterval = TimeSpan.FromSeconds(60);

    /// <summary>How often active ChatGPT use is checked for a low-balance reminder.</summary>
    private static readonly TimeSpan ActivityMonitorInterval = TimeSpan.FromSeconds(15);

    /// <summary>
    /// How often announcements are checked.
    /// </summary>
    /// <remarks>
    /// Its own timer rather than a ride on the card refresh: an announcement has no
    /// minute-level urgency, and the endpoints behind it run a per-user subscription
    /// query for every call. For scale, the web panel does not poll at all — it
    /// refetches on navigation behind a 20-minute throttle. This too had drifted during
    /// the port, to 5 minutes: three times the load, for news that is not urgent.
    /// </remarks>
    private static readonly TimeSpan AnnouncementPollInterval = TimeSpan.FromMinutes(15);

    private readonly DashboardPageViewModel? _page;
    private readonly RelaySessionManager? _session;
    private readonly BalanceActivityMonitor? _balanceActivity;
    private readonly SafeAsyncRunner? _safeAsync;
    private readonly DispatcherTimer? _pollTimer;
    private readonly DispatcherTimer? _activityTimer;
    private readonly DispatcherTimer? _announcementTimer;
    private bool _isRunning;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public DashboardView()
    {
        InitializeComponent();
    }

    internal DashboardView(
        DashboardPageViewModel page,
        RelaySessionManager session,
        BalanceActivityMonitor balanceActivity,
        SafeAsyncRunner safeAsync)
    {
        _page = page ?? throw new ArgumentNullException(nameof(page));
        _session = session ?? throw new ArgumentNullException(nameof(session));
        _balanceActivity = balanceActivity ?? throw new ArgumentNullException(nameof(balanceActivity));
        _safeAsync = safeAsync ?? throw new ArgumentNullException(nameof(safeAsync));

        InitializeComponent();
        DataContext = page;

        _pollTimer = new DispatcherTimer { Interval = PollInterval };
        _pollTimer.Tick += (_, _) => _ = _safeAsync.RunAsync(RefreshAndMonitorAsync);
        _activityTimer = new DispatcherTimer { Interval = ActivityMonitorInterval };
        _activityTimer.Tick += (_, _) => _ = _safeAsync.RunAsync(MonitorBalanceActivityAsync);
        _announcementTimer = new DispatcherTimer { Interval = AnnouncementPollInterval };
        _announcementTimer.Tick += (_, _) => _ = _safeAsync.RunAsync(RefreshAnnouncementsAsync);
    }

    /// <summary>Raised when the user asks to sign out and has confirmed it.</summary>
    public event EventHandler? SignedOut;

    /// <summary>Raised when the user asks to quit the client entirely.</summary>
    public event EventHandler? ExitRequested;

    /// <summary>Raised when the user asks to hide the window and keep relaying.</summary>
    public event EventHandler? MinimizeRequested;

    /// <summary>Raised when the user asks for the recharge screen.</summary>
    public event EventHandler? RechargeRequested;

    /// <summary>Raised when the user asks for the announcement reader.</summary>
    public event EventHandler? AnnouncementsRequested;

    /// <summary>Asks a yes/no question. Supplied by the host, which owns a window.</summary>
    internal Func<string, Task<bool>>? Confirm { get; set; }

    /// <summary>
    /// Shows the low-balance reminder. Supplied by the host, which owns the presenter.
    /// </summary>
    /// <remarks>
    /// A property in the same style as <see cref="Confirm"/> rather than a constructor
    /// argument, because the presenter's lifetime belongs to the application and this
    /// view is one of two things that use it.
    /// </remarks>
    internal INotificationPresenter? Notifications { get; set; }

    /// <summary>
    /// Asks what 退出 should mean. Supplied by the host, which owns a window.
    /// </summary>
    /// <param name="isCodexRunning">Changes what the choice costs, so it changes the wording.</param>
    internal Func<bool, Task<ExitChoice>>? AskExitChoice { get; set; }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    /// <summary>Starts the polling loops and does the first refresh.</summary>
    /// <remarks>
    /// Idempotent, and deliberately so. Restoring a saved session raises
    /// <c>StateChanged</c> <i>and</i> returns to a caller that then shows the surface
    /// itself, so this is reached twice on almost every launch by a signed-in user.
    /// Without the guard that is two of every poll — two refreshes, two announcement
    /// fetches, two balance observations — from one start.
    /// </remarks>
    internal void Start()
    {
        if (_page is null || _safeAsync is null || _isRunning)
        {
            return;
        }

        _isRunning = true;
        _page.Refresh();
        _page.Dashboard.InitializeStartupPreference();

        _pollTimer!.Start();
        _activityTimer!.Start();
        _announcementTimer!.Start();

        _ = _safeAsync.RunAsync(RefreshAndMonitorAsync);
        _ = _safeAsync.RunAsync(RefreshAnnouncementsAsync);
    }

    /// <summary>
    /// Stops polling and drops the previous account's figures.
    /// </summary>
    /// <remarks>
    /// The reset is not tidiness. Both the cards and the announcement list are
    /// per-account, so leaving either populated would show one user another user's
    /// data on the next sign-in.
    /// </remarks>
    internal void Stop()
    {
        if (!_isRunning)
        {
            return;
        }

        _isRunning = false;
        _pollTimer?.Stop();
        _activityTimer?.Stop();
        _announcementTimer?.Stop();

        _balanceActivity?.Reset();
        _page?.Dashboard.Reset();
        _page?.Announcements.Reset();
        _page?.Refresh();
    }

    private async Task RefreshAndMonitorAsync()
    {
        if (_page is null)
        {
            return;
        }

        await _page.Dashboard.RefreshAndMonitorAsync().ConfigureAwait(true);
        await MonitorBalanceActivityAsync().ConfigureAwait(true);
    }

    /// <remarks>
    /// The observation is taken on every tick even when it will not be shown, because
    /// the monitor's own state depends on it — skipping the check while Codex is idle
    /// would leave it primed to fire the moment the user comes back.
    /// </remarks>
    private async Task MonitorBalanceActivityAsync()
    {
        if (_page is null || _session is null || _balanceActivity is null)
        {
            return;
        }

        if (!_session.IsSignedIn || !_page.Dashboard.IsCodexRunning)
        {
            _balanceActivity.Reset();
            return;
        }

        BalanceActivityObservation observation = await _balanceActivity.CheckAsync().ConfigureAwait(true);
        if (observation.ShouldNotify)
        {
            string balance = observation.Balance.ToString("0.####", CultureInfo.InvariantCulture);
            Notifications?.Show(new NotificationRequest(
                "共飞-ChatGPT助手余额提醒",
                $"检测到 ChatGPT 正在使用，当前余额仅 ¥{balance}，请及时充值。",
                NotificationSeverity.Warning));
        }
    }

    private Task RefreshAnnouncementsAsync() =>
        _session?.IsSignedIn == true && _page is not null
            ? _page.Announcements.RefreshAsync()
            : Task.CompletedTask;

    /// <summary>Starts Codex from the tray, by the same route as the panel button.</summary>
    /// <remarks>
    /// Routed through here rather than reaching into the view model from the tray, so
    /// the two entry points cannot diverge — including the restart confirmation, which
    /// the tray would otherwise skip.
    /// </remarks>
    internal void StartCodexFromTray() => _ = _safeAsync?.RunAsync(StartOrInstallCodexAsync);

    /// <summary>Opens the announcement reader from the tray.</summary>
    internal void RequestAnnouncements() => AnnouncementsRequested?.Invoke(this, EventArgs.Empty);

    private void Refresh_OnClick(object? sender, RoutedEventArgs e) =>
        _ = _safeAsync?.RunAsync(RefreshAndMonitorAsync);

    private void StartCodex_OnClick(object? sender, RoutedEventArgs e) =>
        _ = _safeAsync?.RunAsync(StartOrInstallCodexAsync);

    private Task StartOrInstallCodexAsync()
    {
        if (_page is null)
        {
            return Task.CompletedTask;
        }

        return _page.Dashboard.CodexNotInstalled
            ? _page.Dashboard.InstallCodexAsync()
            : _page.Dashboard.StartCodexAsync(ConfirmCodexRestartAsync);
    }

    /// <remarks>
    /// Restarting discards whatever the user has in flight in Codex, so it is only
    /// ever done after they say so — and the prompt says what they stand to lose
    /// rather than asking an abstract yes/no. If no host supplied a
    /// <see cref="Confirm"/> callback the answer is no, never a silent yes.
    /// </remarks>
    private Task<bool> ConfirmCodexRestartAsync(string message) =>
        Confirm?.Invoke(message + "\n\n要现在重启 ChatGPT 吗？") ?? Task.FromResult(false);

    private void SignOut_OnClick(object? sender, RoutedEventArgs e) =>
        _ = _safeAsync?.RunAsync(SignOutAsync);

    /// <remarks>
    /// <para>
    /// 退出 is asked, not assumed. It previously ran a single yes/no confirmation that
    /// signed the user out — which was wrong in both directions: someone who wanted the
    /// window out of the way lost their session, and someone who wanted the client
    /// stopped found it still running and still billing.
    /// </para>
    /// <para>
    /// Dismissing the dialog does nothing at all. A close-box on a question about
    /// quitting must never be read as an answer to it.
    /// </para>
    /// </remarks>
    private async Task SignOutAsync()
    {
        if (AskExitChoice is null)
        {
            return;
        }

        ExitChoice choice = await AskExitChoice(_page?.Dashboard.IsCodexRunning ?? false)
            .ConfigureAwait(true);

        switch (choice)
        {
            case ExitChoice.FullExit:
                ExitRequested?.Invoke(this, EventArgs.Empty);
                break;

            case ExitChoice.MinimizeToTray:
                MinimizeRequested?.Invoke(this, EventArgs.Empty);
                break;

            case ExitChoice.SignOut:
                SignedOut?.Invoke(this, EventArgs.Empty);
                break;

            case ExitChoice.None:
            default:
                break;
        }
    }

    private void Recharge_OnClick(object? sender, RoutedEventArgs e)
    {
        if (_session?.IsSignedIn == true)
        {
            RechargeRequested?.Invoke(this, EventArgs.Empty);
        }
    }

    private void ShowAnnouncements_OnClick(object? sender, RoutedEventArgs e) =>
        AnnouncementsRequested?.Invoke(this, EventArgs.Empty);

    private void CheckUpdate_OnClick(object? sender, RoutedEventArgs e) =>
        _ = _safeAsync?.RunAsync(() => _page!.ClientUpdate.CheckAsync());

    private void OpenContactPage_OnClick(object? sender, RoutedEventArgs e) =>
        BrowserLauncher.TryOpenRelayPage("contact");

    private void OpenUpdatePage_OnClick(object? sender, RoutedEventArgs e)
    {
        if (_page?.ClientUpdate.DownloadPage is { } page)
        {
            BrowserLauncher.TryOpen(page);
        }
    }
}
