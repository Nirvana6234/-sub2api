using System.Net.Http;
using Avalonia;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.App.Services;
using LanAi.RelayClient.App.Views;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using LanAi.Workspace.Injection;

namespace LanAi.RelayClient.App;

/// <summary>
/// Composition root for the cross-platform head.
/// </summary>
/// <remarks>
/// <para>
/// Wired by hand, like the WPF head it will replace: the graph is small, and writing
/// it out makes the lifetime of the credential-holding services visible at a glance.
/// </para>
/// <para>
/// <b>Nothing below names a platform, and that is the point.</b> Every Windows- or
/// macOS-specific choice is made by a factory in the platform layer —
/// <see cref="SecureStorage"/>, <see cref="StartupRegistrations"/>,
/// <see cref="NotificationPresenters"/>, <see cref="SingleInstance"/> and
/// <see cref="CodexHosts"/>. This file reads the same on both targets, which is what
/// makes a missing macOS implementation surface as one failing factory rather than as
/// a branch buried in the wiring.
/// </para>
/// <para>
/// The remaining macOS gaps are packaging rather than code: the <c>.app</c> bundle and
/// its install script. The blind spots — Keychain, <c>open -b</c>, <c>osascript</c>,
/// LaunchAgent — are written and unverified, each recorded as such at its own class.
/// </para>
/// </remarks>
public partial class App : Application
{
    private HttpClient? _http;
    private ISingleInstanceCoordinator? _singleInstance;
    private ClientShutdownCoordinator? _shutdown;
    private TrayPresence? _tray;
    private INotificationPresenter? _notifications;

    /// <summary>
    /// Set once the shell exists, so the single-instance listener can raise it.
    /// </summary>
    /// <remarks>
    /// A field rather than a parameter because the instance claim has to happen before
    /// anything is built — a second launch must not construct an HttpClient, a session
    /// store or a window before discovering it is the second launch.
    /// </remarks>
    private ShellWindow? _shell;

    public override void Initialize() => AvaloniaXamlLoader.Load(this);

    public override void OnFrameworkInitializationCompleted()
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime desktop)
        {
            if (!TryClaimSingleInstance(desktop))
            {
                return;
            }

            InstallCrashHandlers();
            ClientLog.Info($"客户端启动（跨平台头），服务器 {ClientOptions.ServerAddress}");

            // Without this the process would end the moment the window is hidden,
            // which is exactly what the tray exists to prevent.
            desktop.ShutdownMode = Avalonia.Controls.ShutdownMode.OnExplicitShutdown;
            desktop.MainWindow = BuildShell(desktop);
            desktop.Exit += (_, _) => OnExit();
        }

        base.OnFrameworkInitializationCompleted();
    }

    private bool TryClaimSingleInstance(IClassicDesktopStyleApplicationLifetime desktop)
    {
        // The names are shared with the WPF head on purpose: the two builds must
        // exclude each other, not merely each exclude itself. Two clients running at
        // once would both write ~/.codex and both try to own the managed key.
        ISingleInstanceCoordinator singleInstance = SingleInstance.Create(
            @"Global\LanAi.RelayClient.SingleInstance",
            @"Global\LanAi.RelayClient.Activate",
            activate: RaiseExistingWindow);

        if (!singleInstance.IsPrimary)
        {
            bool activated = singleInstance.TryActivateExistingInstance();
            ClientLog.Info(activated
                ? "检测到重复启动，已请求现有客户端显示主界面"
                : "检测到重复启动，但现有客户端未能接收激活请求");
            singleInstance.Dispose();
            desktop.Shutdown();
            return false;
        }

        _singleInstance = singleInstance;
        _singleInstance.StartListening();
        return true;
    }

    private ShellWindow BuildShell(IClassicDesktopStyleApplicationLifetime desktopLifetime)
    {
        _http = new HttpClient
        {
            BaseAddress = new Uri(ClientOptions.ServerAddress),
            Timeout = TimeSpan.FromSeconds(30),
        };

        var relay = new RelayServerClient(_http);
        var session = new RelaySessionManager(relay, SecureStorage.CreateSessionStore(), ClientOptions.ServerAddress);
        var shell = new ShellWindow();
        _shell = shell;

        // Logging alone is not enough here. SafeAsyncRunner records every fault, but a
        // click that silently does nothing gives the user no reason to look for a log
        // — or to mention it in a report. The dialog is what turns "点了没反应" into
        // something answerable.
        var safeAsync = new SafeAsyncRunner(
            report: exception => NoticeDialog.ShowFailureAsync(shell, exception));

        var signIn = new SignInViewModel(session, relay.GetPublicSettingsAsync);
        var clientUpdate = new ClientUpdateViewModel(
            new ClientVersionChecker(relay.GetPublicSettingsAsync, ClientOptions.CurrentVersion).CheckAsync);

        var keyNaming = new ManagedKeyNaming(new InstallId());
        var codexConfig = new CodexConfigWriter(
            new CodexPaths(),
            SecureStorage.CreateSnapshotProtector(),
            AppPaths.CodexSnapshotRoot,
            AppPaths.CodexAuthSnapshotFile);

        // Both chosen by platform rather than named here: the Windows launcher and the
        // CDP overlay are Windows-only types, and the head must not know that.
        var codex = new CodexStartup(
            relay,
            session,
            keyNaming,
            codexConfig,
            CodexHosts.CreateLauncher(),
            CodexHosts.CreateEnhancementHost(codexConfig));

        var dashboard = new DashboardViewModel(
            relay,
            session,
            new GroupPreferenceStore(ClientOptions.ServerAddress),
            keyNaming,
            codex,
            codexInstaller: new CodexInstaller(),
            codexAccountStore: new CodexAccountStore(),
            startupRegistration: StartupRegistrations.Create(),
            safeAsync: safeAsync);

        var announcements = new AnnouncementsViewModel(
            new AnnouncementMonitor(
                relay,
                session,
                new AnnouncementNotifyStateStore(ClientOptions.ServerAddress)),
            relay,
            session);

        var exitCoordinator = new ClientExitCoordinator(codex, session);
        _shutdown = new ClientShutdownCoordinator(() => exitCoordinator.ReleaseForExitAsync());

        var dashboardPage = new DashboardPageViewModel(dashboard, clientUpdate, announcements, session);
        // Created here, on the UI thread, because the Windows implementation owns a
        // window whose procedure receives the click callback on its creating thread.
        _notifications = NotificationPresenters.Create();

        var dashboardView = new DashboardView(
            dashboardPage,
            session,
            new BalanceActivityMonitor(relay, session),
            safeAsync)
        {
            Confirm = message => ConfirmDialog.AskAsync(shell, message),
            Notifications = _notifications,
        };

        var signInView = new SignInView(new SignInPageViewModel(signIn, clientUpdate), safeAsync);

        dashboardView.RechargeRequested += (_, _) => _ = safeAsync.RunAsync(async () =>
        {
            var payment = new PaymentViewModel(relay, session, new QRCoderRenderer(), dashboard.BalanceText);
            var window = new PaymentWindow(payment);
            await window.ShowDialog(shell);

            // Only on success. A cancelled or expired order changed nothing, and an
            // extra refresh would make the balance flicker for no reason.
            if (window.IsCompleted)
            {
                await dashboard.RefreshAsync().ConfigureAwait(true);
            }
        });

        // A singleton, not a new window per click. Both the bell and the tray menu lead
        // here; each opening its own copy would leave the user with stacked windows
        // showing the same list.
        AnnouncementWindow? announcementWindow = null;
        var announcementImages = new AnnouncementImageLoader(_http, ClientOptions.ServerAddress);

        dashboardView.AnnouncementsRequested += (_, _) =>
        {
            if (announcementWindow is not null)
            {
                announcementWindow.Activate();
                return;
            }

            announcementWindow = new AnnouncementWindow(
                announcements,
                new Uri(ClientOptions.ServerAddress),
                announcementImages);
            announcementWindow.Closed += (_, _) => announcementWindow = null;
            announcementWindow.Show(shell);
        };

        var registration = new RegistrationViewModel(
            session,
            relay,
            (interval, onTick) => new AvaloniaUiTimer(interval, onTick));
        var registrationView = new RegistrationView(registration, safeAsync);

        signInView.RegistrationRequested += (_, _) =>
        {
            // Shown before it is prepared, not after: Prepare focuses the email field,
            // and Focus on a control that is not yet in the visual tree does nothing
            // at all — silently, which is how it would have shipped.
            shell.Show(registrationView);
            registrationView.Prepare();
        };

        registrationView.BackToSignInRequested += (_, _) =>
        {
            shell.Show(signInView);
            signInView.ResetEntry();
        };

        // Both the dashboard and the registration form are driven by the server's
        // public settings, and both are wrong in a way that looks right without them:
        // the dashboard falls back to a conservative low-balance threshold, and the
        // registration form hides the very fields the server requires and then has its
        // submission rejected. Applied from one place so the two cannot drift.
        void ApplyEffectiveSettings()
        {
            PublicSettings effective = signIn.Settings with
            {
                ApiBaseUrl = RelayEndpointResolver.ResolveApiBaseUrl(
                    ClientOptions.ServerAddress,
                    signIn.Settings.ApiBaseUrl),
            };

            registration.ApplySettings(effective);
            dashboard.ApplySettings(effective);
        }

        signInView.SurfaceLoaded += (_, _) => ApplyEffectiveSettings();

        // The session is the single source of truth for which surface is showing.
        // Driving it from the event rather than from each call site means a sign-out
        // triggered by an expired token lands on the sign-in form just as a deliberate
        // one does.
        session.StateChanged += (_, _) => Avalonia.Threading.Dispatcher.UIThread.Post(ShowCurrentSurface);

        void ShowCurrentSurface()
        {
            if (session.IsSignedIn)
            {
                shell.Show(dashboardView);
                dashboardView.Start();
            }
            else
            {
                dashboardView.Stop();
                shell.Show(signInView);
            }
        }

        dashboardView.SignedOut += (_, _) => _ = safeAsync.RunAsync(() => session.SignOutAsync());

        _tray = new TrayPresence(
            onShowWindow: shell.RestoreFromTray,
            onStartCodex: () =>
            {
                // Same route the panel button takes, so the tray cannot start Codex a
                // different way from the window.
                shell.RestoreFromTray();
                dashboardView.StartCodexFromTray();
            },
            onShowAnnouncements: () =>
            {
                shell.RestoreFromTray();
                dashboardView.RequestAnnouncements();
            },
            onExit: async () =>
            {
                // The window comes back first: a modal question from a hidden window can
                // appear behind everything else, and the user would be left with an
                // application that seems to have frozen.
                shell.RestoreFromTray();

                ExitChoice choice = await ExitConfirmationDialog
                    .AskAsync(shell, dashboard.IsCodexRunning)
                    .ConfigureAwait(true);

                if (choice == ExitChoice.FullExit)
                {
                    await QuitAsync().ConfigureAwait(true);
                }
                else if (choice == ExitChoice.SignOut)
                {
                    await session.SignOutAsync().ConfigureAwait(true);
                }
            });

        // One quit path for both entry points. The tray's 退出 and the panel's 退出 must
        // release the managed key and restore the user's Codex config identically —
        // two routines would eventually differ, and the one that got it wrong would
        // leave a user's ChatGPT pointing at a revoked key.
        async Task QuitAsync()
        {
            await _shutdown!.ReleaseAsync().ConfigureAwait(true);
            shell.ExitRequested = true;
            desktopLifetime.Shutdown();
        }

        dashboardView.AskExitChoice = isCodexRunning =>
            ExitConfirmationDialog.AskAsync(shell, isCodexRunning);

        dashboardView.ExitRequested += (_, _) => _ = safeAsync.RunAsync(QuitAsync);

        dashboardView.MinimizeRequested += (_, _) =>
        {
            shell.Hide();
            shell.OnFirstHide?.Invoke();
        };

        shell.HasTray = true;
        shell.OnFirstHide = () =>
        {
            if (_tray.ClaimFirstHideHint())
            {
                string area = PlatformWords.NotificationArea;
                _ = NoticeDialog.ShowNoticeAsync(
                    shell,
                    $"""
                    已最小化到{area}。保持运行，ChatGPT 才能继续使用共飞额度。

                    从{area}图标可以随时打开或退出。
                    """);
            }
        };

        // The tray's status line is the only thing a user sees while the window is
        // hidden, so it tracks the same values the group card shows.
        dashboard.PropertyChanged += (_, args) =>
        {
            if (args.PropertyName is nameof(DashboardViewModel.CurrentGroupName)
                or nameof(DashboardViewModel.CurrentGroupRate)
                or nameof(DashboardViewModel.BalanceText))
            {
                _tray?.UpdateStatus(
                    $"共飞 · {dashboard.CurrentGroupName} {dashboard.CurrentGroupRate} · 余额 {dashboard.BalanceText}");
            }
        };

        announcements.PropertyChanged += (_, args) =>
        {
            if (args.PropertyName == nameof(AnnouncementsViewModel.TrayLabel))
            {
                _tray?.UpdateAnnouncements(announcements.TrayLabel);
            }
        };

        // One notification however many arrived: the operator publishes a batch
        // together, and one banner per announcement would bury the screen.
        announcements.Arrived += (_, arrival) => Avalonia.Threading.Dispatcher.UIThread.Post(() =>
            _notifications?.Show(new NotificationRequest(
                arrival.Count > 1
                    ? $"共飞-ChatGPT助手有 {arrival.Count} 条新公告"
                    : "共飞-ChatGPT助手有新公告",
                string.IsNullOrWhiteSpace(arrival.LatestTitle)
                    ? "点击查看。"
                    : arrival.LatestTitle + Environment.NewLine + "点击查看。",
                NotificationSeverity.Information,

                // The same route the tray menu takes, so the two entry points cannot
                // open the reader differently. Never runs on macOS, where the platform
                // gives no click callback — which is why the unread badge stays the
                // dependable way in.
                OnActivated: () =>
                {
                    shell.RestoreFromTray();
                    dashboardView.RequestAnnouncements();
                })));

        shell.Show(signInView);
        shell.Opened += (_, _) => _ = safeAsync.RunAsync(async () =>
        {
            // Applies the settings through SurfaceLoaded, which 重新获取配置 also raises.
            await signInView.LoadAsync().ConfigureAwait(true);

            // A session left by an earlier run is restored before showing the form, so
            // a returning user is not asked for a password they already gave.
            await session.RestoreAsync().ConfigureAwait(true);
            ShowCurrentSurface();


        });

        return shell;
    }

    /// <summary>
    /// Brings this client's window back when another launch asks for it.
    /// </summary>
    /// <remarks>
    /// <para>
    /// Runs on the coordinator's listener thread, so the actual work is posted to the
    /// UI thread. This was an empty lambda at first, which meant a second launch
    /// reported "已请求现有客户端显示主界面" to its log and then exited while the
    /// running client did nothing at all — the user double-clicks the icon and sees
    /// no response whatsoever.
    /// </para>
    /// <para>
    /// It matters most across the two builds. A user with the Avalonia preview hidden
    /// in the tray who launches the WPF client gets exactly that silence, and nothing
    /// on screen explains that a client is already running.
    /// </para>
    /// </remarks>
    private void RaiseExistingWindow() =>
        Avalonia.Threading.Dispatcher.UIThread.Post(() => _shell?.RestoreFromTray());

    private void OnExit()
    {
        ClientLog.Info("客户端退出");
        _shutdown?.ReleaseBeforeProcessExit();
        _singleInstance?.Dispose();
        _tray?.Dispose();
        _notifications?.Dispose();
        _http?.Dispose();
    }

    /// <remarks>
    /// <para>
    /// All three of the WPF head's hooks, contrary to what this comment used to claim.
    /// Avalonia does have a UI-thread counterpart —
    /// <c>Dispatcher.UIThread.UnhandledException</c>, the same shape as WPF's
    /// <c>DispatcherUnhandledException</c> down to the <c>Handled</c> flag. It was
    /// recorded here as missing on the strength of the <c>Application</c> class not
    /// having one; it is on <c>Dispatcher</c>.
    /// </para>
    /// <para>
    /// It matters more than it looks. <see cref="SafeAsyncRunner"/> already covers
    /// every click handler, so what is left is synchronous faults raised on the UI
    /// thread outside it — layout, rendering, binding callbacks. Uncaught, those end
    /// the process, and the user loses a session to what may be a cosmetic bug with
    /// nothing on screen to explain it.
    /// </para>
    /// </remarks>
    private void InstallCrashHandlers()
    {
        Avalonia.Threading.Dispatcher.UIThread.UnhandledException += (_, args) =>
        {
            ClientLog.Error("界面线程未处理异常", args.Exception);

            // Handled so the window survives a fault in one interaction, matching the
            // WPF head. Letting it tear the process down would take the relay with it.
            args.Handled = true;

            // Only once a window exists to own the dialog. Before that there is
            // nothing to parent it to, and the log is the whole of what can be done.
            if (_shell is { } shell)
            {
                _ = NoticeDialog.ShowFailureAsync(shell, args.Exception);
            }
        };

        AppDomain.CurrentDomain.UnhandledException += (_, args) =>
            ClientLog.Error("后台线程未处理异常，进程即将退出", args.ExceptionObject as Exception);

        TaskScheduler.UnobservedTaskException += (_, args) =>
        {
            ClientLog.Error("未观察的任务异常", args.Exception);
            args.SetObserved();
        };
    }
}
