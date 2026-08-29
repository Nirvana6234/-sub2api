using System.Net.Http;
using System.Windows;
using Application = System.Windows.Application;
using MessageBox = System.Windows.MessageBox;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using LanAi.Workspace.Injection;

namespace LanAi.RelayClient;

/// <summary>
/// Composition root.
/// </summary>
/// <remarks>
/// Wiring is done by hand rather than through a container: the object graph is
/// small, and an explicit graph makes the lifetime of the credential-holding
/// services obvious at a glance.
/// </remarks>
public partial class App : Application
{
    private const string SingleInstanceMutexName = "Global\\LanAi.RelayClient.SingleInstance";
    private const string SingleInstanceEventName = "Global\\LanAi.RelayClient.Activate";

    private HttpClient? _http;
    private TrayPresence? _tray;
    private ClientShutdownCoordinator? _shutdownCoordinator;
    private ISingleInstanceCoordinator? _singleInstance;

    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);

        MainWindow? clientWindow = null;
        var singleInstance = SingleInstance.Create(
            SingleInstanceMutexName,
            SingleInstanceEventName,
            () => Dispatcher.BeginInvoke(new Action(() => clientWindow?.RestoreFromTray())));
        if (!singleInstance.IsPrimary)
        {
            bool activated = singleInstance.TryActivateExistingInstance();
            ClientLog.Info(activated
                ? "检测到重复启动，已请求现有客户端显示主界面"
                : "检测到重复启动，但现有客户端未能接收激活请求");
            singleInstance.Dispose();
            Shutdown();
            return;
        }

        _singleInstance = singleInstance;

        InstallCrashHandlers();
        ClientLog.Info($"客户端启动，服务器 {ClientOptions.ServerAddress}");

        _http = new HttpClient
        {
            BaseAddress = new Uri(ClientOptions.ServerAddress),
            Timeout = TimeSpan.FromSeconds(30),
        };

        var relay = new RelayServerClient(_http);
        var session = new RelaySessionManager(relay, SecureStorage.CreateSessionStore(), ClientOptions.ServerAddress);
        var signIn = new SignInViewModel(session, relay.GetPublicSettingsAsync);
        var clientUpdate = new ClientUpdateViewModel(
            new ClientVersionChecker(_http, ClientOptions.CurrentVersion).CheckAsync);
        var registration = new RegistrationViewModel(
            session,
            relay,
            (interval, onTick) => new DispatcherUiTimer(interval, onTick));
        var keyNaming = new ManagedKeyNaming(new InstallId());
        var safeAsync = new SafeAsyncRunner(
            report: _ =>
            {
                MessageBox.Show(
                    "操作没有完成，客户端仍在运行。\n详细信息已记录到：\n" + ClientLog.FilePath,
                    "共飞-ChatGPT助手",
                    MessageBoxButton.OK,
                    MessageBoxImage.Warning);
                return Task.CompletedTask;
            });
        var codexConfig = new CodexConfigWriter(
            new CodexPaths(),
            SecureStorage.CreateSnapshotProtector(),
            AppPaths.CodexSnapshotRoot,
            AppPaths.CodexAuthSnapshotFile);
        var codex = new CodexStartup(
            relay,
            session,
            keyNaming,
            codexConfig,
            new CodexAppLauncherAdapter(new CodexAppLauncher()),
            new RelayInjectionHost(codexConfig));

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

        var exitCoordinator = new ClientExitCoordinator(codex, session);
        _shutdownCoordinator = new ClientShutdownCoordinator(
            () => exitCoordinator.ReleaseForExitAsync());

        var announcements = new AnnouncementsViewModel(
            new AnnouncementMonitor(
                relay,
                session,
                new AnnouncementNotifyStateStore(ClientOptions.ServerAddress)),
            relay,
            session);

        var window = new MainWindow(
            session,
            signIn,
            registration,
            dashboard,
            clientUpdate,
            exitCoordinator,
            announcements,
            safeAsync,
            relay,
            new QRCoderRenderer(),
            announcementImageLoader: new AnnouncementImageLoader(_http, ClientOptions.ServerAddress));
        clientWindow = window;
        MainWindow = window;

        // Without this the process would end the moment the window is hidden,
        // which is exactly what the tray exists to prevent.
        ShutdownMode = ShutdownMode.OnExplicitShutdown;

        _tray = new TrayPresence(
            onShowWindow: window.RestoreFromTray,
            onStartCodex: window.StartCodexFromTray,
            onShowAnnouncements: window.ShowAnnouncements,
            onExit: async () =>
            {
                // The window is brought back first: a modal question from a hidden
                // window can appear behind everything else, and the user would be
                // left with an application that seems to have frozen.
                window.RestoreFromTray();

                if (!window.ConfirmExit())
                {
                    return;
                }

                await _shutdownCoordinator.ReleaseAsync().ConfigureAwait(true);
                window.ExitRequested = true;
                Shutdown();
            });

        window.Tray = _tray;
        _singleInstance.StartListening();

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

        window.Show();
    }

    protected override void OnExit(ExitEventArgs e)
    {
        ClientLog.Info("客户端退出");
        _shutdownCoordinator?.ReleaseBeforeProcessExit();
        _singleInstance?.Dispose();
        _tray?.Dispose();
        _http?.Dispose();
        base.OnExit(e);
    }

    /// <summary>
    /// Catches what would otherwise end the process without explanation.
    /// </summary>
    /// <remarks>
    /// Three separate hooks because they cover disjoint failures: exceptions on the
    /// UI thread, exceptions on any other thread, and faulted tasks nobody awaited.
    /// A client whose users cannot read a stack trace has to leave the trace in a
    /// file instead — otherwise every report is "打不开" with nothing behind it.
    /// </remarks>
    private void InstallCrashHandlers()
    {
        DispatcherUnhandledException += (_, args) =>
        {
            ClientLog.Error("界面线程未处理异常", args.Exception);

            // Handled so the window survives a fault in one interaction. Letting it
            // tear the process down would lose the user's session for what may be a
            // cosmetic bug, and they would have no idea what happened.
            args.Handled = true;
            ShowFailureNotice(args.Exception);
        };

        AppDomain.CurrentDomain.UnhandledException += (_, args) =>
        {
            // Not recoverable — the runtime is already on its way down. The only
            // useful act left is leaving a record behind.
            ClientLog.Error("后台线程未处理异常，进程即将退出", args.ExceptionObject as Exception);
        };

        TaskScheduler.UnobservedTaskException += (_, args) =>
        {
            // Fire-and-forget work that faulted. Observed here so it is recorded
            // rather than vanishing, which is how a "点了没反应" bug hides.
            ClientLog.Error("未观察的任务异常", args.Exception);
            args.SetObserved();
        };
    }

    /// <remarks>
    /// States plainly that the log exists and where, because the user is the one
    /// who will have to retrieve it. The exception text is included but secondary:
    /// it means nothing to them and everything to whoever reads the report.
    /// </remarks>
    private static void ShowFailureNotice(Exception exception)
    {
        try
        {
            MessageBox.Show(
                $"操作出错了，客户端仍在运行。\n\n{exception.Message}\n\n详细信息已记录到：\n{ClientLog.FilePath}",
                "共飞-ChatGPT助手",
                MessageBoxButton.OK,
                MessageBoxImage.Warning);
        }
        catch (Exception ex)
        {
            // A dialog that cannot open must not itself become the crash.
            ClientLog.Error("提示框弹出失败", ex);
        }
    }
}
