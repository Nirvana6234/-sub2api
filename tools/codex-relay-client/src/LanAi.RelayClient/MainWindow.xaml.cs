using System.Diagnostics;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Threading;
using KeyEventArgs = System.Windows.Input.KeyEventArgs;
using MessageBox = System.Windows.MessageBox;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient;

public partial class MainWindow : Window
{
    /// <summary>
    /// How often the cards re-read the server (F4.1).
    /// </summary>
    /// <remarks>
    /// The panel endpoints sit behind a per-user rate limiter, so this interval is
    /// a budget as much as a freshness target.
    /// </remarks>
    private static readonly TimeSpan PollInterval = TimeSpan.FromSeconds(60);

    /// <summary>How often active ChatGPT use is checked for a low-balance reminder.</summary>
    private static readonly TimeSpan ActivityMonitorInterval = TimeSpan.FromSeconds(15);

    /// <summary>
    /// How often announcements are checked.
    /// </summary>
    /// <remarks>
    /// Its own timer rather than a ride on the 60-second card refresh: an
    /// announcement has no minute-level urgency, and the endpoints behind it run a
    /// per-user subscription query for every call. Most ticks cost only the
    /// summary probe; the full list is pulled just when that says something moved.
    /// For scale, the web panel does not poll at all — it refetches on navigation
    /// behind a 20-minute throttle.
    /// </remarks>
    private static readonly TimeSpan AnnouncementPollInterval = TimeSpan.FromMinutes(15);

    private readonly RelaySessionManager _session;
    private readonly IRelayServerClient _relay;
    private readonly IQRCodeRenderer _qrRenderer;
    private readonly SignInViewModel _signIn;
    private readonly RegistrationViewModel _registration;
    private readonly DashboardViewModel _dashboard;
    private readonly ClientUpdateViewModel _clientUpdate;
    private readonly ClientExitCoordinator _exitCoordinator;
    private readonly SafeAsyncRunner _safeAsync;
    private readonly DispatcherTimer _pollTimer;
    private readonly BalanceActivityMonitor _balanceActivityMonitor;
    private readonly DispatcherTimer _activityMonitorTimer;
    private readonly AnnouncementsViewModel _announcements;
    private readonly IAnnouncementImageLoader? _announcementImageLoader;
    private readonly DispatcherTimer _announcementTimer;

    private AnnouncementWindow? _announcementWindow;

    internal MainWindow(
        RelaySessionManager session,
        SignInViewModel signIn,
        RegistrationViewModel registration,
        DashboardViewModel dashboard,
        ClientUpdateViewModel clientUpdate,
        ClientExitCoordinator exitCoordinator,
        AnnouncementsViewModel announcements,
        SafeAsyncRunner? safeAsync = null,
        IRelayServerClient? relay = null,
        IQRCodeRenderer? qrRenderer = null,
        BalanceActivityMonitor? balanceActivityMonitor = null,
        IAnnouncementImageLoader? announcementImageLoader = null)
    {
        _session = session;
        _relay = relay ?? throw new ArgumentNullException(nameof(relay));
        _qrRenderer = qrRenderer ?? throw new ArgumentNullException(nameof(qrRenderer));
        _signIn = signIn;
        _registration = registration;
        _dashboard = dashboard;
        _clientUpdate = clientUpdate;
        _exitCoordinator = exitCoordinator;
        _announcements = announcements ?? throw new ArgumentNullException(nameof(announcements));
        _announcementImageLoader = announcementImageLoader;
        _safeAsync = safeAsync ?? new SafeAsyncRunner();
        _balanceActivityMonitor = balanceActivityMonitor ?? new BalanceActivityMonitor(_relay, _session);

        // Assigned BEFORE InitializeComponent, and it has to stay that way.
        //
        // The XAML binds these through {Binding X.Y, ElementName=RootWindow}, and
        // InitializeComponent is where those bindings attach and first evaluate.
        // Neither property raises change notification — MainWindow is not an
        // INotifyPropertyChanged — so a binding that reads null here reads null
        // forever, and the control silently renders empty. Assigning afterwards is
        // what left the announcement button blank and the version label missing.
        ClientUpdate = _clientUpdate;
        Announcements = _announcements;

        InitializeComponent();
        MoveRecentSpendCardToEnd();
        DataContext = _signIn;
        RegistrationPanel.DataContext = _registration;
        SignedInPanel.DataContext = _dashboard;

        _pollTimer = new DispatcherTimer { Interval = PollInterval };
        _pollTimer.Tick += (_, _) => _ = _safeAsync.RunAsync(RefreshAndMonitorAsync);
        _activityMonitorTimer = new DispatcherTimer { Interval = ActivityMonitorInterval };
        _activityMonitorTimer.Tick += (_, _) => _ = _safeAsync.RunAsync(MonitorBalanceActivityAsync);
        _announcementTimer = new DispatcherTimer { Interval = AnnouncementPollInterval };
        _announcementTimer.Tick += (_, _) => _ = _safeAsync.RunAsync(RefreshAnnouncementsAsync);

        _announcements.Arrived += Announcements_OnArrived;
        _announcements.PropertyChanged += (_, args) =>
        {
            if (args.PropertyName == nameof(AnnouncementsViewModel.TrayLabel))
            {
                Tray?.UpdateAnnouncements(_announcements.TrayLabel);
            }
        };

        _session.StateChanged += (_, _) => Dispatcher.Invoke(ApplySessionState);

        Loaded += MainWindow_OnLoaded;
        Closing += MainWindow_OnClosing;
        Closed += (_, _) =>
        {
            _pollTimer.Stop();
            _activityMonitorTimer.Stop();
            _announcementTimer.Stop();
            _balanceActivityMonitor.Reset();
        };
    }

    private void MoveRecentSpendCardToEnd()
    {
        if (RecentSpendCard.Parent is not Panel panel)
        {
            return;
        }

        panel.Children.Remove(RecentSpendCard);
        panel.Children.Add(RecentSpendCard);
    }

    /// <summary>The tray, once the composition root has built it.</summary>
    internal TrayPresence? Tray { get; set; }

    public ClientUpdateViewModel ClientUpdate { get; }

    /// <summary>Bound by the title-bar bell, and shared with the tray and the reader.</summary>
    public AnnouncementsViewModel Announcements { get; }

    /// <summary>Set only by the tray's 退出 item, which is the sole way out (F9.4).</summary>
    internal bool ExitRequested { get; set; }

    /// <summary>
    /// Turns the close button into "minimise to tray" (F9.3).
    /// </summary>
    /// <remarks>
    /// Not a preference. The managed key is a one-day lease that only survives
    /// while this process runs to renew it, so exiting on close would hand the
    /// user a Codex that stops working tomorrow for no visible reason.
    /// </remarks>
    private void MainWindow_OnClosing(object? sender, System.ComponentModel.CancelEventArgs e)
    {
        if (ExitRequested || Tray is null)
        {
            return;
        }

        e.Cancel = true;
        Hide();
        Tray.NotifyStillRunningOnce();
    }

    /// <summary>Brings the window back from the tray.</summary>
    internal void RestoreFromTray()
    {
        Show();
        WindowState = WindowState.Normal;
        Activate();
    }

    /// <summary>
    /// Asks before ending the process, and says what it costs (F9.4).
    /// </summary>
    /// <remarks>
    /// Quitting is not symmetrical with closing the window: it stops the renewals
    /// the lease depends on, so Codex keeps working only until the current lease
    /// lapses. A user who quits expecting "close this window" would find Codex
    /// broken the next day with nothing on screen to connect the two, so the
    /// warning names the consequence rather than asking an abstract "are you sure".
    /// </remarks>
    internal bool ConfirmExit()
    {
        string consequence = _dashboard.IsCodexRunning
            ? "ChatGPT 正在运行。退出后它将无法继续使用共飞额度。"
            : "退出后将不再自动续签授权，ChatGPT 到期后无法继续使用共飞额度。";

        string message = consequence + Environment.NewLine + Environment.NewLine + "仍要退出吗？";

        return MessageBox.Show(
            message,
            "共飞-ChatGPT助手",
            MessageBoxButton.YesNo,
            MessageBoxImage.Warning,
            MessageBoxResult.No) == MessageBoxResult.Yes;
    }

    /// <summary>Runs the same action as the main button, for the tray menu.</summary>
    internal void StartCodexFromTray() =>
        _ = _safeAsync.RunAsync(StartOrInstallCodexAsync);

    /// <summary>
    /// Opens the announcement reader, reusing the one already open.
    /// </summary>
    /// <remarks>
    /// Shared by all three entry points. Non-modal, and it does not restore the
    /// main window: a user who reached this from the tray asked to read an
    /// announcement, not to be handed the whole client back.
    /// </remarks>
    internal void ShowAnnouncements()
    {
        if (_announcementWindow is not null)
        {
            _announcementWindow.Activate();
            return;
        }

        var window = new AnnouncementWindow(
            _announcements,
            new Uri(ClientOptions.ServerAddress),
            _announcementImageLoader)
        {
            Owner = this,

            // Centring on an owner that is hidden in the tray would put the reader
            // wherever the invisible window happens to be.
            WindowStartupLocation = IsVisible
                ? WindowStartupLocation.CenterOwner
                : WindowStartupLocation.CenterScreen,
        };

        window.Closed += (_, _) => _announcementWindow = null;
        _announcementWindow = window;
        window.Show();
    }

    private void ShowAnnouncements_OnClick(object sender, RoutedEventArgs e) => ShowAnnouncements();

    private void Announcements_OnArrived(object? sender, AnnouncementArrival arrival) =>
        Dispatcher.Invoke(() => Tray?.NotifyNewAnnouncement(
            arrival.Count,
            arrival.LatestTitle,
            ShowAnnouncements));

    private Task RefreshAnnouncementsAsync() =>
        _session.IsSignedIn ? _announcements.RefreshAsync() : Task.CompletedTask;

    private void MainWindow_OnLoaded(object sender, RoutedEventArgs e)
    {
        _ = _safeAsync.RunAsync(LoadWindowAsync);
    }

    private async Task LoadWindowAsync()
    {
        await _signIn.LoadSurfaceAsync().ConfigureAwait(true);
        await _clientUpdate.CheckAsync().ConfigureAwait(true);
        ApplyPublicSettings();

        // The cards need the same server-driven settings the sign-in form does —
        // the low-balance threshold and the server's timezone both come from there.
        _dashboard.InitializeStartupPreference();

        // A session left by an earlier run is restored before showing the form, so
        // a returning user is not asked for a password they already gave.
        await _session.RestoreAsync().ConfigureAwait(true);

        ApplySessionState();

        if (!_session.IsSignedIn)
        {
            PasswordInput.Focus();
        }
    }

    private void RetrySurface_OnClick(object sender, RoutedEventArgs e) =>
        _ = _safeAsync.RunAsync(RetrySurfaceAsync);

    private async Task RetrySurfaceAsync()
    {
        await _signIn.LoadSurfaceAsync().ConfigureAwait(true);
        ApplyPublicSettings();
    }

    private void ApplyPublicSettings()
    {
        PublicSettings effectiveSettings = _signIn.Settings with
        {
            ApiBaseUrl = RelayEndpointResolver.ResolveApiBaseUrl(
                ClientOptions.ServerAddress,
                _signIn.Settings.ApiBaseUrl),
        };
        _registration.ApplySettings(effectiveSettings);

        // The cards need the same server-driven settings the sign-in form does.
        // The low-balance threshold and server timezone both come from there.
        _dashboard.ApplySettings(effectiveSettings);
    }

    private void ApplySessionState()
    {
        bool signedIn = _session.IsSignedIn;
        SignInPanel.Visibility = signedIn ? Visibility.Collapsed : Visibility.Visible;
        RegistrationPanel.Visibility = Visibility.Collapsed;
        SignedInPanel.Visibility = signedIn ? Visibility.Visible : Visibility.Collapsed;

        if (signedIn)
        {
            WelcomeText.Text = $"你好，{_session.UserDisplayName}";
            _pollTimer.Start();
            _activityMonitorTimer.Start();
            _announcementTimer.Start();
            _ = _safeAsync.RunAsync(RefreshAndMonitorAsync);
            _ = _safeAsync.RunAsync(RefreshAnnouncementsAsync);
        }
        else
        {
            _pollTimer.Stop();
            _activityMonitorTimer.Stop();
            _announcementTimer.Stop();
            _balanceActivityMonitor.Reset();

            // Dropped before the next sign-in, so one account can never see the
            // previous account's figures still on the cards.
            _dashboard.Reset();

            // Same rule for announcements: targeting is per account, so one user
            // must never be left looking at another's list.
            _announcements.Reset();
            _announcementWindow?.Close();

            if (_session.LastSignOutReason == SignOutReason.SessionExpired)
            {
                _signIn.ErrorMessage = "登录已过期，请重新登录。";
            }
        }
    }

    private void Refresh_OnClick(object sender, RoutedEventArgs e) =>
        _ = _safeAsync.RunAsync(RefreshAndMonitorAsync);

    private async Task RefreshAndMonitorAsync()
    {
        await _dashboard.RefreshAndMonitorAsync().ConfigureAwait(true);
        await MonitorBalanceActivityAsync().ConfigureAwait(true);
    }

    private async Task MonitorBalanceActivityAsync()
    {
        if (!_session.IsSignedIn || !_dashboard.IsCodexRunning)
        {
            _balanceActivityMonitor.Reset();
            return;
        }

        BalanceActivityObservation observation = await _balanceActivityMonitor.CheckAsync().ConfigureAwait(true);
        if (observation.ShouldNotify)
        {
            Tray?.NotifyLowBalance(observation.Balance);
        }
    }

    private void StartCodex_OnClick(object sender, RoutedEventArgs e) =>
        _ = _safeAsync.RunAsync(StartOrInstallCodexAsync);

    private Task StartOrInstallCodexAsync() => _dashboard.CodexNotInstalled
        ? _dashboard.InstallCodexAsync()
        : _dashboard.StartCodexAsync(ConfirmCodexRestartAsync);

    /// <remarks>
    /// Restarting discards whatever the user has in flight in Codex, so it is only
    /// ever done after they say so — and the prompt says what they stand to lose
    /// rather than asking an abstract yes/no.
    /// </remarks>
    private Task<bool> ConfirmCodexRestartAsync(string message)
    {
        MessageBoxResult answer = MessageBox.Show(
            message + "\n\n要现在重启 ChatGPT 吗？",
            "共飞-ChatGPT助手",
            MessageBoxButton.YesNo,
            MessageBoxImage.Question);

        return Task.FromResult(answer == MessageBoxResult.Yes);
    }

    private void Recharge_OnClick(object sender, RoutedEventArgs e)
    {
        if (!_session.IsSignedIn)
        {
            return;
        }

        var viewModel = new PaymentViewModel(_relay, _session, _qrRenderer, _dashboard.BalanceText);
        var window = new PaymentWindow(viewModel) { Owner = this };
        window.ShowDialog();
        if (viewModel.IsCompleted)
        {
            _ = _safeAsync.RunAsync(() => _dashboard.RefreshAsync());
        }
    }

    private void Submit_OnClick(object sender, RoutedEventArgs e) => _ = _safeAsync.RunAsync(SubmitAsync);

    private void Register_OnClick(object sender, RoutedEventArgs e)
    {
        if (!_signIn.CanRegister)
        {
            return;
        }

        SignInPanel.Visibility = Visibility.Collapsed;
        RegistrationPanel.Visibility = Visibility.Visible;
        _registration.Reset();
        RegistrationEmailInput.Focus();
    }

    private void BackToLogin_OnClick(object sender, RoutedEventArgs e)
    {
        _registration.Reset();
        RegistrationPanel.Visibility = Visibility.Collapsed;
        SignInPanel.Visibility = Visibility.Visible;
        PasswordInput.Clear();
        EmailInput.Focus();
    }

    private void SendVerifyCode_OnClick(object sender, RoutedEventArgs e) =>
        _ = _safeAsync.RunAsync(() => _registration.SendVerifyCodeAsync());

    private void SubmitRegistration_OnClick(object sender, RoutedEventArgs e) =>
        _ = _safeAsync.RunAsync(SubmitRegistrationAsync);

    private void OpenRegistrationInBrowser_OnClick(object sender, RoutedEventArgs e) =>
        OpenInBrowser(new Uri(new Uri(ClientOptions.ServerAddress), "register"));

    private async Task SubmitRegistrationAsync()
    {
        bool succeeded = await _registration
            .SubmitAsync(
                RegistrationPasswordInput.Password,
                RegistrationConfirmPasswordInput.Password)
            .ConfigureAwait(true);

        if (succeeded)
        {
            RegistrationPasswordInput.Clear();
            RegistrationConfirmPasswordInput.Clear();
            ApplySessionState();
        }
    }

    private void Password_OnKeyDown(object sender, KeyEventArgs e)
    {
        if (e.Key == Key.Enter)
        {
            _ = _safeAsync.RunAsync(SubmitAsync);
        }
    }

    private async Task SubmitAsync()
    {
        // Read at the moment of use and passed straight through: the password is
        // never stored on the view model or exposed as a bindable property.
        string password = PasswordInput.Password;

        if (await _signIn.SubmitAsync(password).ConfigureAwait(true))
        {
            PasswordInput.Clear();
        }
    }

    private void CancelTwoFactor_OnClick(object sender, RoutedEventArgs e) => _signIn.CancelTwoFactor();

    private void SignOut_OnClick(object sender, RoutedEventArgs e)
    {
        var dialog = new SignOutConfirmationDialog(_dashboard.IsCodexRunning) { Owner = this };
        if (dialog.ShowDialog() != true)
        {
            return;
        }

        if (dialog.Choice == SignOutChoice.MinimizeToTray)
        {
            Hide();
            Tray?.NotifyStillRunningOnce();
            return;
        }

        if (dialog.Choice != SignOutChoice.SignOut)
        {
            return;
        }

        _ = _safeAsync.RunAsync(async () =>
        {
            await _exitCoordinator.SignOutAsync().ConfigureAwait(true);
            PasswordInput.Clear();
        });
    }

    private void ResetPassword_OnClick(object sender, RoutedEventArgs e) =>
        OpenInBrowser(new Uri(new Uri(ClientOptions.ServerAddress), "forgot-password"));

    private void CheckUpdate_OnClick(object sender, RoutedEventArgs e) =>
        _ = _safeAsync.RunAsync(() => _clientUpdate.CheckAsync());

    private void OpenUpdatePage_OnClick(object sender, RoutedEventArgs e)
    {
        if (_clientUpdate.DownloadPage is { } page)
        {
            OpenInBrowser(page);
        }
    }

    private void OpenContactPage_OnClick(object sender, RoutedEventArgs e) =>
        OpenInBrowser(new Uri(new Uri(ClientOptions.ServerAddress), "contact"));

    private static void OpenInBrowser(Uri uri)
    {
        try
        {
            Process.Start(new ProcessStartInfo(uri.ToString()) { UseShellExecute = true });
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            MessageBox.Show("无法打开浏览器，请手动访问：" + uri, "共飞-ChatGPT助手");
        }
    }
}
