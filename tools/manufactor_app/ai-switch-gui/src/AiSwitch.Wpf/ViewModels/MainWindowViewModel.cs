using System.Collections.ObjectModel;
using System.Diagnostics;
using System.IO;
using System.Windows.Media;
using AiSwitchGui;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Injection;
using LanAi.Workspace.Injection.Sentinel;
using LanAi.Workspace.Terminal;
using LanAi.Workspace.Wpf.Controls;
using LanAi.Workspace.Wpf.Services;
using Microsoft.Win32;

namespace LanAi.Workspace.Wpf.ViewModels;

public partial class MainWindowViewModel : ObservableObject, IDisposable
{
    internal const string ExtensionCenterWarningTitle = "进入扩展中心前请确认";
    internal const string ExtensionCenterWarningMessage =
        "扩展中心会直接修改 Codex、Claude Code 和 Gemini CLI 的 MCP、提示词与 Skills 配置。\n\n" +
        "如果随意删除、覆盖或同步错误，可能导致客户端无法启动、扩展失效，部分配置也无法自动恢复，并可能造成严重后果。\n\n" +
        "请确认已经理解风险并自行做好备份。是否继续进入扩展中心？";

    private static readonly HttpClient NetworkProbeClient = new()
    {
        Timeout = TimeSpan.FromSeconds(3),
    };

    private const int NetworkProbeDiagnosticMaximumBytes = 256 * 1024;

    private readonly WorkspaceDataService _dataService;
    private readonly ILocalTelemetryRepository _localTelemetryRepository;
    private readonly IDisposable? _ownedLocalTelemetryRepository;
    private readonly IOfficialCliUsageHistoryImporter _officialUsageHistoryImporter;
    private readonly IDisposable? _ownedOfficialUsageHistoryImporter;
    private readonly ISub2ApiSessionManager _sub2ApiSessionManager;
    private readonly bool _ownsSub2ApiSessionManager;
    private readonly EndpointProbeService _endpointProbeService;
    private readonly Sub2ApiServiceSummaryClient _serviceSummaryClient;
    private readonly LocalCloudStatisticsClient _cloudStatisticsClient;
    private readonly IReadOnlyDictionary<string, PageViewModel> _pages;
    private readonly ProjectsViewModel _projects;
    private readonly HistoryViewModel _history;
    private readonly ConnectionsViewModel _connections;
    private readonly ILegacySwitchCoordinator _legacySwitchCoordinator;
    private CodexInjectionSession? _codexInjectionSession;
    private readonly ILocalGatewayController _localGatewayController;
    private readonly ILocalGatewayHealthMonitor _localGatewayHealthMonitor;
    private readonly Func<string?> _localControlTokenProvider;
    private readonly GatewayViewModel _gateway;
    private readonly AccountCenterViewModel _accountCenter;
    private readonly TransitCenterViewModel _transitCenter;
    private readonly StatsViewModel _stats;
    private readonly ExtensionsViewModel _extensions;
    private readonly WorkspaceFeatureStore _workspaceFeatureStore;
    private readonly OfficialClientExtensionSynchronizer _extensionSynchronizer;
    private readonly ProjectWorkspaceProfileService _projectProfileService;
    private readonly DesktopSettingsStore _desktopSettingsStore;
    private readonly ApplicationUpdateService _applicationUpdateService;
    private readonly SettingsViewModel _settings;
    private readonly ProfileViewModel _profile;
    private readonly OverviewViewModel _overview;
    private readonly Func<bool> _confirmExtensionCenterAccess;
    private readonly TerminalViewModel _terminal;
    private readonly IChatSessionController _chatController;
    private readonly ChatViewModel _chat;
    private readonly ProjectSessionsViewModel _projectSessions;
    private readonly SemaphoreSlim _networkProbeGate = new(1, 1);
    private readonly CancellationTokenSource _lifetime = new();
    private CancellationTokenSource? _networkProbeLoopCts;
    private Task? _networkProbeLoopTask;
    private IReadOnlyList<ConnectionProfile> _networkProbeProfiles = Array.Empty<ConnectionProfile>();
    private int _networkProbeIntervalMinutes = 3;
    private int _networkProbeRequested;
    private bool _networkProbeLoopStarted;
    private bool _extensionCenterAccessConfirmed;
    private Task? _shutdownTask;
    private int _legacyHistoryCleanupAttempted;
    private bool _disposed;

    [ObservableProperty]
    private PageViewModel currentPage = null!;

    [ObservableProperty]
    private bool isLoading;

    [ObservableProperty]
    private string workspaceStatus = "正在准备本地工作区…";

    [ObservableProperty]
    private string dataSourceStatus = "等待首次同步";

    [ObservableProperty]
    private bool hasLoadErrors;

    [ObservableProperty]
    private string chatBreadcrumb = "项目中心 / 项目会话 / AI 对话";

    [ObservableProperty]
    private string chatReturnLabel = "返回项目中心";

    [ObservableProperty]
    private string codexInjectionStatusLabel = "启动 Codex";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanStartCodexInjection))]
    private bool isCodexInjectionBusy;

    [ObservableProperty]
    private bool isCodexInjectionActive;

    public bool CanStartCodexInjection => !IsCodexInjectionBusy;

    private string _chatReturnPageId = "projects";
    private string _terminalReturnPageId = "projects";

    public MainWindowViewModel()
        : this(new WorkspaceDataService())
    {
    }

    internal MainWindowViewModel(WorkspaceDataService dataService)
        : this(dataService, chatController: null, transcriptReader: null)
    {
    }

    internal MainWindowViewModel(
        WorkspaceDataService dataService,
        IChatSessionController? chatController)
        : this(dataService, chatController, transcriptReader: null)
    {
    }

    internal MainWindowViewModel(
        WorkspaceDataService dataService,
        IChatSessionController? chatController,
        IConversationTranscriptReader? transcriptReader,
        ILocalTelemetryRepository? localTelemetryRepository = null,
        IOfficialCliUsageHistoryImporter? officialUsageHistoryImporter = null,
        ISub2ApiSessionManager? sub2ApiSessionManager = null,
        Func<bool>? confirmExtensionCenterAccess = null,
        ILocalGatewayController? localGatewayController = null,
        Func<string?>? localControlTokenProvider = null,
        ILocalGatewayHealthMonitor? localGatewayHealthMonitor = null)
    {
        _dataService = dataService ?? throw new ArgumentNullException(nameof(dataService));
        _confirmExtensionCenterAccess = confirmExtensionCenterAccess ?? ShowExtensionCenterAccessWarning;
        AppDataPaths appDataPaths = AppDataPaths.CreateDefault();
        _localTelemetryRepository = localTelemetryRepository ?? new SqliteLocalTelemetryRepository(appDataPaths);
        _ownedLocalTelemetryRepository = localTelemetryRepository is null
            ? _localTelemetryRepository as IDisposable
            : null;
        _officialUsageHistoryImporter = officialUsageHistoryImporter
            ?? new OfficialCliUsageHistoryImporter(appDataPaths, _localTelemetryRepository);
        _ownedOfficialUsageHistoryImporter = officialUsageHistoryImporter is null
            ? _officialUsageHistoryImporter
            : null;
        _sub2ApiSessionManager = sub2ApiSessionManager ?? new Sub2ApiSessionManager();
        _ownsSub2ApiSessionManager = sub2ApiSessionManager is null;
        var localGatewayEndpointResolver = new ConnectionProfileLocalGatewayEndpointResolver(
            _dataService.ConnectionProfileReader);
        var backendProbe = new LocalGatewayStatsProbe();
        _endpointProbeService = new EndpointProbeService(
            _dataService.CredentialProvider,
            _localTelemetryRepository);
        _serviceSummaryClient = new Sub2ApiServiceSummaryClient();
        _cloudStatisticsClient = new LocalCloudStatisticsClient();
        _workspaceFeatureStore = new WorkspaceFeatureStore(appDataPaths);
        _desktopSettingsStore = new DesktopSettingsStore(appDataPaths);
        _applicationUpdateService = new ApplicationUpdateService(appDataPaths.UpdatesDirectory);
        _extensionSynchronizer = new OfficialClientExtensionSynchronizer(appDataPaths);
        _extensions = new ExtensionsViewModel(_workspaceFeatureStore, _extensionSynchronizer, appDataPaths);
        _projectProfileService = new ProjectWorkspaceProfileService(
            _workspaceFeatureStore,
            _extensionSynchronizer,
            _dataService.ConnectionProfileEditor);
        _settings = new SettingsViewModel(
            _desktopSettingsStore,
            new WindowsStartupRegistrationService(),
            _applicationUpdateService,
            appDataPaths);

        var workspaceProjects = new ObservableCollection<ProjectCardViewModel>();
        _projects = new ProjectsViewModel(
            workspaceProjects,
            AddProjectFromFolderAsync,
            DeleteProjectAsync,
            RefreshAsync,
            OpenProject);
        _history = new HistoryViewModel(RefreshAsync, ResumeConversation);
        _localGatewayController = localGatewayController ?? new LocalGatewayController();
        _localGatewayHealthMonitor = localGatewayHealthMonitor ?? new LocalGatewayHealthMonitor(_localGatewayController);
        _localGatewayHealthMonitor.StateChanged += OnLocalGatewayRecoveryStateChanged;
        _localControlTokenProvider = localControlTokenProvider ?? (() =>
            LocalControlTokenStore.Load(_localGatewayController.GetStartupStatus().NativeRoot));
        _legacySwitchCoordinator = new LegacySwitchCoordinator(
            appDataPaths,
            _sub2ApiSessionManager,
            _localControlTokenProvider);
        _connections = new ConnectionsViewModel(
            RefreshAsync,
            _dataService.ConnectionProfileEditor,
            _legacySwitchCoordinator,
            new ConnectionProfileTransferService(appDataPaths),
            _localGatewayController);
        _gateway = new GatewayViewModel(
            _localGatewayController,
            _sub2ApiSessionManager,
            localGatewayEndpointResolver,
            _endpointProbeService,
            _serviceSummaryClient,
            backendProbe,
            localControlCenterMode: true);
        _accountCenter = new AccountCenterViewModel(
            _sub2ApiSessionManager,
            localControlTokenProvider: _localControlTokenProvider);
        _transitCenter = new TransitCenterViewModel(_connections, _accountCenter);
        var cloudUsageSnapshotCache = new CloudUsageSnapshotCache();
        _stats = new StatsViewModel(
            new StatsController(),
            _localTelemetryRepository,
            localCloudStatisticsClient: _cloudStatisticsClient,
            connectionProfileReader: _dataService.ConnectionProfileReader,
            localGatewayEndpointResolver: localGatewayEndpointResolver,
            sub2ApiSessionManager: _sub2ApiSessionManager,
            cloudUsageSnapshotCache: cloudUsageSnapshotCache);
        _overview = new OverviewViewModel(
            workspaceProjects,
            _localTelemetryRepository,
            _sub2ApiSessionManager,
            _cloudStatisticsClient,
            OnNetworkProbeIntervalChanged,
            cloudUsageSnapshotCache);
        _sub2ApiSessionManager.SessionChanged += OnSharedSub2ApiSessionChanged;
        _profile = new ProfileViewModel(_sub2ApiSessionManager);
        SignInPrompt = new SignInPromptViewModel(
            _sub2ApiSessionManager,
            ResolveIdentityApiBaseUri);
        RelaySwitchPrompt = new RelaySwitchPromptViewModel(
            AcceptRelaySwitchAsync,
            DeclineRelaySwitch);
        _terminal = new TerminalViewModel(
            workspaceProjects,
            _dataService.ConnectionProfileReader,
            _dataService.CredentialProvider,
            ReturnFromTerminal);
        if (chatController is null)
        {
            var chatCommandFactory = new CliTerminalCommandFactory(_dataService.CredentialProvider);
            _chatController = new ChatSessionController(
                cli => cli switch
                {
                    CliKind.Codex => new CodexAppServerEngine(chatCommandFactory),
                    CliKind.ClaudeCode => new ClaudeStreamJsonEngine(chatCommandFactory),
                    CliKind.GeminiCli => new GeminiAcpEngine(chatCommandFactory),
                    _ => throw new ArgumentOutOfRangeException(nameof(cli), cli, "不支持的图形聊天客户端。"),
                },
                profileReader: _dataService.ConnectionProfileReader,
                ownsProfileReader: false);
        }
        else
        {
            _chatController = chatController;
        }
        _chat = new ChatViewModel(
            _terminal,
            _chatController,
            OpenAdvancedTerminalAsync,
            transcriptReader ?? new OfficialConversationTranscriptReader(appDataPaths),
            _localTelemetryRepository,
            _officialUsageHistoryImporter);
        _projectSessions = new ProjectSessionsViewModel(
            StartNewProjectConversationAsync,
            ContinueProjectConversationAsync,
            () => NavigateTo("projects"),
            CaptureProjectProfileAsync,
            ApplyProjectProfileAsync);

        _pages = new Dictionary<string, PageViewModel>(StringComparer.OrdinalIgnoreCase)
        {
            ["overview"] = _overview,
            ["projects"] = _projects,
            ["project-sessions"] = _projectSessions,
            ["chat"] = _chat,
            ["terminal"] = _terminal,
            ["history"] = _history,
            ["connections"] = _connections,
            ["gateway"] = _gateway,
            ["account-center"] = _accountCenter,
            ["transit-center"] = _transitCenter,
            ["stats"] = _stats,
            ["extensions"] = _extensions,
            ["settings"] = _settings,
            ["profile"] = _profile,
        };

        NavigationItems = new ObservableCollection<NavigationItemViewModel>
        {
            new("overview", "工作台", "Overview", true),
            new("transit-center", "中转中心", "Connections"),
            new("stats", "用量仪表盘", "Stats"),
            new("projects", "项目中心", "Projects"),
            new("extensions", "扩展中心", "Extensions"),
            new("settings", "设置", "Settings"),
        };

        CurrentPage = _pages["overview"];
    }

    public ObservableCollection<NavigationItemViewModel> NavigationItems { get; }

    public IConnectionCredentialProvider CredentialProvider => _dataService.CredentialProvider;

    public IConnectionProfileReader ConnectionProfileReader => _dataService.ConnectionProfileReader;

    public SettingsViewModel Settings => _settings;

    public async Task InitializeAsync()
    {
        await EnsureLocalGatewayStartedAsync().ConfigureAwait(true);
        await EnsureLocalAdministratorSessionAsync().ConfigureAwait(true);
        _localGatewayHealthMonitor.Start(_lifetime.Token);

        OperationResult resumeResult = await _legacySwitchCoordinator
            .ResumeLastApplicationStateAsync(_lifetime.Token)
            .ConfigureAwait(true);
        if (!resumeResult.Success)
        {
            WorkspaceStatus = resumeResult.Summary;
            HasLoadErrors = true;
        }

        await Task.WhenAll(
                RefreshAsync(),
                _extensions.InitializeAsync(_lifetime.Token),
                _settings.InitializeAsync(_lifetime.Token))
            .ConfigureAwait(true);

        _networkProbeIntervalMinutes = NormalizeNetworkProbeInterval(
            _settings.CurrentSettings.NetworkProbeIntervalMinutes);
        _overview.SetNetworkProbeInterval(_networkProbeIntervalMinutes);
        _networkProbeLoopStarted = true;
        RestartNetworkProbeLoop();
    }

    private async Task EnsureLocalAdministratorSessionAsync()
    {
        var localApiBaseUri = new Uri("http://127.0.0.1:8080/");
        try
        {
            // A loopback address does not identify the database behind it. An
            // older installation can leave a valid refresh session for a
            // different backend that later reuses 127.0.0.1:8080. Prove the
            // current workspace on every startup with its local control token.
            await LoginLocalAdministratorAsync(localApiBaseUri).ConfigureAwait(true);
        }
        catch (OperationCanceledException) when (_lifetime.IsCancellationRequested)
        {
        }
        catch (Sub2ApiSessionException exception)
        {
            WorkspaceStatus = $"本机管理权限初始化失败：{DescribeLocalSessionFailure(exception.Failure)}";
            HasLoadErrors = true;
        }
    }

    private async Task LoginLocalAdministratorAsync(Uri localApiBaseUri)
    {
        string? token = _localControlTokenProvider();
        if (string.IsNullOrWhiteSpace(token))
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.AuthorizationUnavailable);
        }

        try
        {
            await _sub2ApiSessionManager
                .LoginLocalControlAsync(localApiBaseUri, token, _lifetime.Token)
                .ConfigureAwait(true);
            return;
        }
        catch (Sub2ApiSessionException exception) when (
            exception.Failure is Sub2ApiSessionFailure.InvalidCredentials or
                Sub2ApiSessionFailure.GatewayUnavailable or
                Sub2ApiSessionFailure.AuthorizationUnavailable)
        {
            LocalGatewayStatus status = _localGatewayController.GetStartupStatus();
            if (!status.ControlAvailable)
            {
                throw;
            }
        }

        CommandResult restartResult = await _localGatewayController
            .RestartAsync(_lifetime.Token)
            .ConfigureAwait(true);
        if (!restartResult.Success ||
            !await _localGatewayController.WaitForWebAsync(TimeSpan.FromSeconds(45), _lifetime.Token).ConfigureAwait(true))
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.GatewayUnavailable);
        }

        token = _localControlTokenProvider();
        if (string.IsNullOrWhiteSpace(token))
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.AuthorizationUnavailable);
        }
        await _sub2ApiSessionManager
            .LoginLocalControlAsync(localApiBaseUri, token, _lifetime.Token)
            .ConfigureAwait(true);
    }

    private static string DescribeLocalSessionFailure(Sub2ApiSessionFailure failure) => failure switch
    {
        Sub2ApiSessionFailure.AuthorizationUnavailable => "安装目录中的本机控制令牌不存在或无法读取，请重新运行安装程序。",
        Sub2ApiSessionFailure.InvalidCredentials => "当前 8080 端口上的后台与本安装目录不匹配。",
        Sub2ApiSessionFailure.GatewayUnavailable => "本机后台未响应或启动失败。",
        Sub2ApiSessionFailure.SecureStorageUnavailable => "Windows 安全存储不可用，无法保存管理员会话。",
        _ => "本机后台未能建立管理员会话。",
    };

    private async Task EnsureLocalGatewayStartedAsync()
    {
        try
        {
            LocalGatewayStatus status = await _localGatewayController
                .GetStatusAsync(_lifetime.Token)
                .ConfigureAwait(true);
            if (!status.ControlAvailable)
            {
                return;
            }

            if (!ShouldStartLocalGateway(status, _localControlTokenProvider()))
            {
                return;
            }

            CommandResult startResult = await _localGatewayController
                .StartAsync(_lifetime.Token)
                .ConfigureAwait(true);
            if (!startResult.Success)
            {
                string failure = $"本机中转自动启动失败：{DescribeGatewayCommandFailure(startResult)}";
                WorkspaceStatus = failure;
                _gateway.ApplyRecoveryUpdate(new LocalGatewayRecoveryUpdate(
                    LocalGatewayRecoveryState.Failed,
                    failure));
                HasLoadErrors = true;
                return;
            }

            bool ready = await _localGatewayController
                .WaitForWebAsync(TimeSpan.FromSeconds(45), _lifetime.Token)
                .ConfigureAwait(true);
            if (!ready)
            {
                WorkspaceStatus = "本机中转仍在启动，请稍后重试。";
                _gateway.ApplyRecoveryUpdate(new LocalGatewayRecoveryUpdate(
                    LocalGatewayRecoveryState.Degraded,
                    WorkspaceStatus));
            }
        }
        catch (OperationCanceledException) when (_lifetime.IsCancellationRequested)
        {
        }
        catch (Exception exception) when (
            exception is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            WorkspaceStatus = $"本机中转自动启动失败：{exception.Message}";
            _gateway.ApplyRecoveryUpdate(new LocalGatewayRecoveryUpdate(
                LocalGatewayRecoveryState.Failed,
                WorkspaceStatus));
            HasLoadErrors = true;
        }
    }

    internal static bool ShouldStartLocalGateway(LocalGatewayStatus status, string? localControlToken)
    {
        return !status.WebReachable ||
               string.IsNullOrWhiteSpace(localControlToken) ||
               status.Services.Any(service => !service.IsHealthyEnough);
    }

    internal static string DescribeGatewayCommandFailure(CommandResult result)
    {
        string message = string.Join(
            " ",
            result.CombinedOutput.Split(
                ['\r', '\n'],
                StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries));
        if (string.IsNullOrWhiteSpace(message))
        {
            return $"启动程序退出码为 {result.ExitCode}，但没有返回具体错误。";
        }
        return message.Length <= 420 ? message : message[..420] + "...";
    }

    public Task ShutdownAsync(TimeSpan timeout)
    {
        if (timeout <= TimeSpan.Zero)
        {
            throw new ArgumentOutOfRangeException(nameof(timeout));
        }

        return _shutdownTask ??= ShutdownCoreAsync(timeout);
    }

    private async Task ShutdownCoreAsync(TimeSpan timeout)
    {
        _lifetime.Cancel();
        var stopwatch = Stopwatch.StartNew();
        Task disposeGatewayMonitor = _localGatewayHealthMonitor.DisposeAsync().AsTask();
        // Persist the workspace''s desired state before restoring the official
        // clients. This state is reapplied on the next launch, while the launch
        // snapshot below keeps Codex/Claude/Gemini/Grok clean after this process exits.
        _ = await _legacySwitchCoordinator.SaveApplicationStateAsync().ConfigureAwait(false);
        // Restoring the launch-time Codex/Claude/Gemini/Grok configuration is
        // transactional state recovery, not optional cleanup. The window must
        // not close while this task is still writing auth and config files.
        _ = await _legacySwitchCoordinator.RestoreApplicationSessionAsync().ConfigureAwait(false);

        Task disposeChat = Task.WhenAll(_chat.DisposeAsync().AsTask(), disposeGatewayMonitor);
        TimeSpan remaining = timeout - stopwatch.Elapsed;
        if (remaining <= TimeSpan.Zero)
        {
            _ = ObserveShutdownTaskAsync(disposeChat);
            return;
        }
        try
        {
            await disposeChat.WaitAsync(remaining).ConfigureAwait(false);
        }
        catch (TimeoutException)
        {
            _ = ObserveShutdownTaskAsync(disposeChat);
        }
    }

    private static async Task ObserveShutdownTaskAsync(Task task)
    {
        try
        {
            await task.ConfigureAwait(false);
        }
        catch
        {
            // Window shutdown is already committed; observe late cleanup faults.
        }
    }

    [RelayCommand]
    private void Navigate(NavigationItemViewModel? item)
    {
        if (item is null)
        {
            return;
        }

        if (!CanNavigateToExtensionCenter(
                item.Id,
                ref _extensionCenterAccessConfirmed,
                _confirmExtensionCenterAccess))
        {
            return;
        }

        NavigateTo(item.Id);
    }

    internal static bool CanNavigateToExtensionCenter(
        string targetPageId,
        ref bool accessConfirmed,
        Func<bool> confirmation)
    {
        ArgumentNullException.ThrowIfNull(confirmation);
        if (!string.Equals(targetPageId, "extensions", StringComparison.OrdinalIgnoreCase) || accessConfirmed)
        {
            return true;
        }

        if (!confirmation())
        {
            return false;
        }

        accessConfirmed = true;
        return true;
    }

    private static bool ShowExtensionCenterAccessWarning()
        => System.Windows.MessageBox.Show(
               ExtensionCenterWarningMessage,
               ExtensionCenterWarningTitle,
               System.Windows.MessageBoxButton.YesNo,
               System.Windows.MessageBoxImage.Warning,
               System.Windows.MessageBoxResult.No) == System.Windows.MessageBoxResult.Yes;

    [RelayCommand]
    private void OpenUsageDashboard() => NavigateTo("stats");

    [RelayCommand]
    private async Task RefreshAsync()
    {
        if (IsLoading || _disposed)
        {
            return;
        }

        IsLoading = true;
        HasLoadErrors = false;
        WorkspaceStatus = "正在并行读取项目、CLI、连接与历史…";
        DataSourceStatus = "同步中";
        SetChildLoadingState(true);

        try
        {
            int discardedLegacyHistoryRows = await RemoveLegacyHistoryImportEventsOnceAsync()
                .ConfigureAwait(true);
            WorkspaceDataSnapshot snapshot = await _dataService.LoadAsync(_lifetime.Token);

            TerminalProjectRefreshState projectRefreshState = _terminal.BeginProjectSnapshotRefresh();
            try
            {
                _projects.ApplySnapshot(snapshot);
                if (projectRefreshState.PendingConversation is { } pendingConversation &&
                    !_projects.WorkspaceProjects.Any(candidate =>
                        string.Equals(
                            candidate.Id,
                            pendingConversation.ProjectId,
                            StringComparison.OrdinalIgnoreCase) ||
                        string.Equals(
                            candidate.PathFingerprint,
                            pendingConversation.ProjectId,
                            StringComparison.OrdinalIgnoreCase) ||
                        PathsEqual(candidate.Path, pendingConversation.OriginalWorkingDirectory)))
                {
                    CreateTransientResumeProject(pendingConversation);
                }
            }
            finally
            {
                _terminal.CompleteProjectSnapshotRefresh(projectRefreshState);
            }
            _history.ApplySnapshot(snapshot);
            if (_projectSessions.CurrentProject is { } selectedProject)
            {
                _projectSessions.RefreshSessions(_history.GetProjectSessions(selectedProject));
            }
            _connections.ApplySnapshot(snapshot);
            _gateway.ApplyConnections(
                snapshot.Connections,
                snapshot.ConnectionSelection,
                snapshot.ConnectionRouting);
            _accountCenter.ApplyConnections(
                snapshot.Connections,
                snapshot.ConnectionSelection,
                snapshot.ConnectionRouting);
            _stats.ApplyConnections(
                snapshot.Connections,
                snapshot.ConnectionSelection,
                snapshot.ConnectionRouting);
            _gateway.ApplyCliInstallations(snapshot.CliInstallations);
            _overview.ApplySnapshot(snapshot);
            await _overview.RefreshLocalTelemetryAsync(_lifetime.Token);
            _networkProbeProfiles = FindBackupProbeProfiles(snapshot);
            QueueBackupConnectionsProbe();
            _terminal.ApplyConnections(
                snapshot.Connections,
                snapshot.ConnectionSelection,
                snapshot.ConnectionRouting);
            _chat.RefreshContext();

            HasLoadErrors = snapshot.Errors.Count > 0;
            WorkspaceStatus = snapshot.Errors.Count == 0
                ? $"已载入 {snapshot.Projects.Count} 个项目和 {snapshot.Conversations.Count} 条会话"
                : $"已载入可用数据，{snapshot.Errors.Count} 个来源需要检查";
            DataSourceStatus = discardedLegacyHistoryRows > 0
                ? $"已移除 {discardedLegacyHistoryRows:N0} 条无法核验的官方历史累计记录；本地仪表盘仅统计工作台实际会话"
                : snapshot.DiscoveredProjectCount > 0
                    ? $"新发现 {snapshot.DiscoveredProjectCount} 个项目"
                    : $"更新于 {snapshot.LoadedAt:HH:mm:ss}";
        }
        catch (OperationCanceledException) when (_lifetime.IsCancellationRequested)
        {
            WorkspaceStatus = "工作区已停止同步";
            DataSourceStatus = "已取消";
        }
        catch (Exception exception)
        {
            HasLoadErrors = true;
            WorkspaceStatus = "工作区数据加载失败";
            DataSourceStatus = exception.Message;
            _projects.SetLoadFailure(exception.Message);
            _history.SetLoadFailure(exception.Message);
            _connections.SetLoadFailure(exception.Message);
        }
        finally
        {
            IsLoading = false;
            SetChildLoadingState(false);
            if (_disposed)
            {
                _dataService.Dispose();
            }
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _lifetime.Cancel();
        _localGatewayHealthMonitor.StateChanged -= OnLocalGatewayRecoveryStateChanged;
        _ = ObserveShutdownTaskAsync(_localGatewayHealthMonitor.DisposeAsync().AsTask());
        _networkProbeLoopCts?.Cancel();
        if (_networkProbeLoopCts is { } loopCts)
        {
            _ = DisposeNetworkProbeLoopAsync(_networkProbeLoopTask, loopCts);
            _networkProbeLoopCts = null;
            _networkProbeLoopTask = null;
        }
        _lifetime.Dispose();
        if (!IsLoading)
        {
            _dataService.Dispose();
        }

        _ownedLocalTelemetryRepository?.Dispose();
        _ownedOfficialUsageHistoryImporter?.Dispose();
        _endpointProbeService.Dispose();
        _serviceSummaryClient.Dispose();
        _cloudStatisticsClient.Dispose();
        if (_codexInjectionSession is { } codexInjectionSession)
        {
            codexInjectionSession.PromptRequested -= OnRelaySwitchPromptRequested;
            codexInjectionSession.Dispose();
        }
        (_legacySwitchCoordinator as IDisposable)?.Dispose();
        _sub2ApiSessionManager.SessionChanged -= OnSharedSub2ApiSessionChanged;
        _accountCenter.Dispose();
        _extensions.Dispose();
        _extensionSynchronizer.Dispose();
        _workspaceFeatureStore.Dispose();
        _applicationUpdateService.Dispose();
        _desktopSettingsStore.Dispose();
        if (_ownsSub2ApiSessionManager)
        {
            _sub2ApiSessionManager.Dispose();
        }
    }

    private void OnLocalGatewayRecoveryStateChanged(LocalGatewayRecoveryUpdate update)
    {
        if (_disposed)
        {
            return;
        }

        void ApplyUpdate()
        {
            if (_disposed)
            {
                return;
            }

            _gateway.ApplyRecoveryUpdate(update);
            if (update.State is LocalGatewayRecoveryState.Failed or LocalGatewayRecoveryState.Suspended)
            {
                WorkspaceStatus = update.Message;
                HasLoadErrors = true;
            }
            else if (update.State == LocalGatewayRecoveryState.Recovered)
            {
                WorkspaceStatus = update.Message;
            }
        }

        System.Windows.Threading.Dispatcher? dispatcher = System.Windows.Application.Current?.Dispatcher;
        if (dispatcher is not null && !dispatcher.CheckAccess())
        {
            _ = dispatcher.BeginInvoke(ApplyUpdate);
        }
        else
        {
            ApplyUpdate();
        }
    }

    private void OnSharedSub2ApiSessionChanged(object? sender, EventArgs args)
    {
        if (_disposed)
        {
            return;
        }

        void RefreshOverview()
        {
            if (!_disposed)
            {
                _ = _overview.RefreshLocalTelemetryAsync(_lifetime.Token);
                RefreshIdentityBadge();
            }
        }

        if (System.Windows.Application.Current?.Dispatcher is { } dispatcher && !dispatcher.CheckAccess())
        {
            _ = dispatcher.BeginInvoke(RefreshOverview);
        }
        else
        {
            RefreshOverview();
        }
    }

    /// <summary>
    /// Identity badge shown at the bottom of the sidebar. It is the only always
    /// visible sign-in indicator, so it must distinguish a machine-local
    /// control session from a real account sign-in.
    /// </summary>
    public string IdentityDisplayName => IdentityBadge.DisplayName(_sub2ApiSessionManager.Current);

    public string IdentityStatusLabel => IdentityBadge.StatusLabel(_sub2ApiSessionManager.Current);

    /// <summary>Single letter rendered inside the avatar circle.</summary>
    public string IdentityInitial => IdentityBadge.Initial(_sub2ApiSessionManager.Current);

    public bool IsIdentitySignedIn => _sub2ApiSessionManager.Current.IsAuthenticated;

    /// <summary>
    /// The sign-in card raised when the badge is clicked while signed out.
    /// Public because WPF cannot bind to internal members.
    /// </summary>
    public SignInPromptViewModel SignInPrompt { get; }

    /// <summary>
    /// Badge click: sign in when there is no session, otherwise show the account.
    /// </summary>
    [RelayCommand]
    private void OpenIdentity()
    {
        if (_sub2ApiSessionManager.Current.IsAuthenticated)
        {
            NavigateTo("profile");
            return;
        }

        SignInPrompt.Show();
    }

    /// <summary>
    /// Signing in from the badge targets the source the session already uses, and
    /// falls back to this machine's own gateway when there is no session yet.
    /// </summary>
    private Uri? ResolveIdentityApiBaseUri()
        => _sub2ApiSessionManager.Current.ApiBaseUri ?? new Uri("http://127.0.0.1:8080/");

    private void RefreshIdentityBadge()
    {
        OnPropertyChanged(nameof(IdentityDisplayName));
        OnPropertyChanged(nameof(IdentityStatusLabel));
        OnPropertyChanged(nameof(IdentityInitial));
        OnPropertyChanged(nameof(IsIdentitySignedIn));
    }

    /// <summary>
    /// The relay-switch card raised when the official account's allowance runs out
    /// while the injection session is watching it. Public because WPF cannot bind to
    /// internal members.
    /// </summary>
    public RelaySwitchPromptViewModel RelaySwitchPrompt { get; }

    /// <summary>
    /// Launches (or attaches to) the official Codex desktop app with its DevTools port
    /// open, installs the 共飞 status overlay, and starts watching its usage limit. A
    /// running instance without a debug port can only be reached by restarting it,
    /// which drops the user's in-flight turn — so that path always asks first.
    /// </summary>
    [RelayCommand]
    private async Task StartCodexInjectionAsync()
    {
        if (IsCodexInjectionBusy)
        {
            return;
        }

        if (_codexInjectionSession is { IsRunning: true })
        {
            CodexInjectionStatusLabel = "已连接";
            return;
        }

        IsCodexInjectionBusy = true;
        CodexInjectionStatusLabel = "启动中…";
        try
        {
            var gateway = new RelaySwitchGatewayAdapter(_legacySwitchCoordinator);
            var session = new CodexInjectionSession(gateway);
            CodexInjectionStartResult result = await session
                .StartAsync(_lifetime.Token)
                .ConfigureAwait(true);

            if (result.NeedsRestartConsent)
            {
                bool consent = System.Windows.MessageBox.Show(
                    result.Message + "\n\n是否重启官方应用以继续？",
                    "共飞AI工作台",
                    System.Windows.MessageBoxButton.YesNo,
                    System.Windows.MessageBoxImage.Warning) == System.Windows.MessageBoxResult.Yes;
                if (!consent)
                {
                    CodexInjectionStatusLabel = "启动 Codex";
                    return;
                }

                session.Dispose();
                session = new CodexInjectionSession(
                    gateway,
                    new CodexInjectionSessionOptions { AllowTerminateExisting = true });
                result = await session.StartAsync(_lifetime.Token).ConfigureAwait(true);
            }

            if (result.Started)
            {
                _codexInjectionSession?.Dispose();
                _codexInjectionSession = session;
                session.PromptRequested += OnRelaySwitchPromptRequested;
                IsCodexInjectionActive = true;
            }
            else
            {
                session.Dispose();
                IsCodexInjectionActive = false;
            }

            CodexInjectionStatusLabel = result.Message;
        }
        catch (OperationCanceledException) when (_lifetime.IsCancellationRequested)
        {
            CodexInjectionStatusLabel = "启动 Codex";
        }
        finally
        {
            IsCodexInjectionBusy = false;
        }
    }

    private Task<RelaySwitchOutcome> AcceptRelaySwitchAsync(CancellationToken cancellationToken)
        => _codexInjectionSession?.AcceptAsync(cancellationToken)
            ?? Task.FromResult(new RelaySwitchOutcome(false, "注入未启动。"));

    private void DeclineRelaySwitch() => _codexInjectionSession?.Decline();

    private void OnRelaySwitchPromptRequested(object? sender, RelaySwitchPrompt prompt)
    {
        void Show()
        {
            if (!_disposed)
            {
                RelaySwitchPrompt.Show(prompt);
            }
        }

        if (System.Windows.Application.Current?.Dispatcher is { } dispatcher && !dispatcher.CheckAccess())
        {
            _ = dispatcher.BeginInvoke(Show);
        }
        else
        {
            Show();
        }
    }

    private async Task<int> RemoveLegacyHistoryImportEventsOnceAsync()
    {
        if (Interlocked.Exchange(ref _legacyHistoryCleanupAttempted, 1) != 0)
        {
            return 0;
        }

        try
        {
            return await _localTelemetryRepository
                .RemoveLegacyHistoryImportEventsAsync(_lifetime.Token)
                .ConfigureAwait(true);
        }
        catch (OperationCanceledException) when (_lifetime.IsCancellationRequested)
        {
            return 0;
        }
        catch
        {
            // A derived-metric cleanup must not prevent opening projects or
            // chats. It will be retried on the next application start.
            Interlocked.Exchange(ref _legacyHistoryCleanupAttempted, 0);
            return 0;
        }
    }

    private async Task<string> CaptureProjectProfileAsync(ProjectCardViewModel project)
    {
        ProjectWorkspaceProfile profile = await _projectProfileService
            .CaptureAsync(project.Id, _lifetime.Token)
            .ConfigureAwait(true);
        return $"已保存项目工作配置 · {profile.UpdatedAt.ToLocalTime():HH:mm:ss}";
    }

    private async Task<string> ApplyProjectProfileAsync(ProjectCardViewModel project)
    {
        ProjectProfileOperationResult result = await _projectProfileService
            .ApplyAsync(project.Id, _lifetime.Token)
            .ConfigureAwait(true);
        await RefreshAsync().ConfigureAwait(true);
        return result.Warnings.Count == 0
            ? "项目工作配置已应用到连接、MCP、提示词和 Skills。"
            : $"项目配置已应用，{result.Warnings.Count} 项引用需要检查：{string.Join("；", result.Warnings)}";
    }

    private async Task AddProjectFromFolderAsync(string folderPath)
    {
        await _dataService.AddProjectAsync(folderPath, _lifetime.Token);
        await RefreshAsync();
    }

    internal async Task<ProjectRemovalOutcome> DeleteProjectAsync(ProjectCardViewModel project)
    {
        if (string.Equals(
                _chatController.ActiveProjectFingerprint,
                project.PathFingerprint,
                StringComparison.OrdinalIgnoreCase))
        {
            using var chatStopTimeout = CancellationTokenSource.CreateLinkedTokenSource(_lifetime.Token);
            chatStopTimeout.CancelAfter(TimeSpan.FromSeconds(12));
            try
            {
                await _chatController.CancelTurnAsync(chatStopTimeout.Token);
                await _chatController.ResetAsync(chatStopTimeout.Token)
                    .WaitAsync(TimeSpan.FromSeconds(12), _lifetime.Token);
            }
            catch (TimeoutException)
            {
                return ProjectRemovalOutcome.Failed(
                    "该项目的图形 AI 会话仍在运行，12 秒内未能安全停止，因此没有删除任何历史或项目记录。");
            }
            catch (OperationCanceledException) when (!_lifetime.IsCancellationRequested)
            {
                return ProjectRemovalOutcome.Failed(
                    "该项目的图形 AI 会话仍在运行，12 秒内未能安全停止，因此没有删除任何历史或项目记录。");
            }
            catch (OperationCanceledException)
            {
                throw;
            }
            catch (Exception exception)
            {
                return ProjectRemovalOutcome.Failed(
                    $"停止该项目的图形 AI 会话失败：{exception.Message}。没有删除任何历史或项目记录。");
            }
        }

        TerminalDisplayMetadata? activeTerminal = TerminalHost.Shared.ActiveMetadata;
        if (activeTerminal is not null && PathsEqual(activeTerminal.WorkingDirectory, project.Path))
        {
            using var stopTimeout = CancellationTokenSource.CreateLinkedTokenSource(_lifetime.Token);
            stopTimeout.CancelAfter(TimeSpan.FromSeconds(12));
            try
            {
                await TerminalHost.Shared.StopAsync(stopTimeout.Token)
                    .WaitAsync(TimeSpan.FromSeconds(12), _lifetime.Token);
            }
            catch (TimeoutException)
            {
                return ProjectRemovalOutcome.Failed(
                    "该项目的官方 CLI 仍在运行，12 秒内未能安全停止，因此没有删除任何历史或项目记录。");
            }
            catch (OperationCanceledException) when (!_lifetime.IsCancellationRequested)
            {
                return ProjectRemovalOutcome.Failed(
                    "该项目的官方 CLI 仍在运行，12 秒内未能安全停止，因此没有删除任何历史或项目记录。");
            }
            catch (OperationCanceledException)
            {
                throw;
            }
            catch (Exception exception)
            {
                return ProjectRemovalOutcome.Failed(
                    $"停止该项目的官方 CLI 失败：{exception.Message}。没有删除任何历史或项目记录。");
            }

            activeTerminal = TerminalHost.Shared.ActiveMetadata;
            if (activeTerminal is not null && PathsEqual(activeTerminal.WorkingDirectory, project.Path))
            {
                return ProjectRemovalOutcome.Failed(
                    "该项目的官方 CLI 尚未完全停止，因此没有删除任何历史或项目记录。");
            }
        }

        ProjectDeletionResult result = await _dataService.DeleteProjectAsync(project.Record, _lifetime.Token);
        if (!result.Conversations.Succeeded)
        {
            string details = string.Join(
                "；",
                result.Conversations.Issues
                    .Take(4)
                    .Select(issue =>
                        $"{WorkspaceDisplay.CliName(issue.Client)} {issue.Item}：{issue.Message}"));
            if (result.Conversations.Issues.Count > 4)
            {
                details += $"；另有 {result.Conversations.Issues.Count - 4} 项失败";
            }

            return ProjectRemovalOutcome.Failed(
                $"官方历史未能完整删除，项目记录已保留。{details}");
        }

        if (!result.ProjectRecordDeleted)
        {
            return ProjectRemovalOutcome.Failed(
                result.ProjectRecordError is { Length: > 0 } error
                    ? $"已删除 {result.Conversations.DeletedCount} 条官方历史，但本机项目记录删除失败：{error}"
                    : "官方历史已删除，但本机项目记录仍然存在。");
        }

        return ProjectRemovalOutcome.Completed(
            result.Conversations.DeletedCount == 0
                ? "已从本机项目列表删除；未发现官方历史。源码文件夹没有删除。"
                : $"已从本机删除项目记录，并永久删除 {result.Conversations.DeletedCount} 条官方历史。源码文件夹没有删除。");
    }

    private void OpenProject(ProjectCardViewModel project)
    {
        _terminal.PrepareProject(project);
        _projectSessions.OpenProject(project, _history.GetProjectSessions(project));
        NavigateTo("project-sessions");
    }

    private async Task StartNewProjectConversationAsync(ProjectCardViewModel project)
    {
        _terminal.PrepareProject(project);
        await _chat.StartNewConversationAsync();
        ConfigureChatNavigation(project, "project-sessions", "返回项目会话", "项目中心");
        NavigateTo("chat");
    }

    private Task ContinueProjectConversationAsync(
        ProjectCardViewModel project,
        HistorySessionViewModel session)
    {
        _terminal.PrepareResume(project, session.Record);
        _terminal.RequestAutoStart();
        _terminalReturnPageId = "project-sessions";
        NavigateTo("terminal");
        return Task.CompletedTask;
    }

    private void ResumeConversation(HistorySessionViewModel session)
    {
        ProjectCardViewModel? project = _projects.WorkspaceProjects.FirstOrDefault(candidate =>
            string.Equals(candidate.Id, session.Record.ProjectId, StringComparison.OrdinalIgnoreCase) ||
            string.Equals(candidate.PathFingerprint, session.Record.ProjectId, StringComparison.OrdinalIgnoreCase));
        project ??= _projects.WorkspaceProjects.FirstOrDefault(candidate =>
            PathsEqual(candidate.Path, session.Record.OriginalWorkingDirectory));
        project ??= CreateTransientResumeProject(session.Record);

        _terminal.PrepareResume(project, session.Record);
        _chat.RefreshContext();
        ConfigureChatNavigation(project, "history", "返回历史会话", "历史会话");
        NavigateTo("chat");
    }

    [RelayCommand]
    private void ReturnFromChat()
    {
        string targetPageId = _chatReturnPageId;
        if (string.Equals(targetPageId, "project-sessions", StringComparison.OrdinalIgnoreCase))
        {
            if (_projectSessions.CurrentProject is { } project)
            {
                _projectSessions.RefreshSessions(_history.GetProjectSessions(project));
            }
            else
            {
                targetPageId = "projects";
            }
        }

        NavigateTo(_pages.ContainsKey(targetPageId) ? targetPageId : "projects");
    }

    private void ConfigureChatNavigation(
        ProjectCardViewModel? project,
        string returnPageId,
        string returnLabel,
        string originLabel)
    {
        _chatReturnPageId = returnPageId;
        ChatReturnLabel = returnLabel;
        string projectLabel = project?.Name ?? "当前项目";
        ChatBreadcrumb = string.Equals(originLabel, "项目中心", StringComparison.Ordinal)
            ? $"项目中心 / {projectLabel} / 项目会话 / AI 对话"
            : $"{originLabel} / {projectLabel} / AI 对话";
    }

    private async Task OpenAdvancedTerminalAsync()
    {
        try
        {
            await _chatController.CancelTurnAsync(_lifetime.Token);
            await _chatController.ResetAsync(_lifetime.Token);
        }
        catch (OperationCanceledException) when (_lifetime.IsCancellationRequested)
        {
            return;
        }

        _terminalReturnPageId = "chat";
        NavigateTo("terminal");
    }

    private void ReturnFromTerminal()
    {
        string targetPageId = _terminalReturnPageId;
        if (string.Equals(targetPageId, "project-sessions", StringComparison.OrdinalIgnoreCase))
        {
            if (_projectSessions.CurrentProject is { } project)
            {
                _projectSessions.RefreshSessions(_history.GetProjectSessions(project));
            }
            else
            {
                targetPageId = "projects";
            }
        }

        NavigateTo(_pages.ContainsKey(targetPageId) ? targetPageId : "projects");
    }

    private ProjectCardViewModel? CreateTransientResumeProject(ConversationRecord conversation)
    {
        string normalizedPath;
        try
        {
            normalizedPath = PathIdentity.Normalize(conversation.OriginalWorkingDirectory);
        }
        catch (Exception exception) when (
            exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            return null;
        }

        string fingerprint = PathIdentity.CreateStableId(normalizedPath);
        bool pathAvailable = Directory.Exists(normalizedPath);
        string? pinnedConnectionId = conversation.ResumePolicy == ResumePolicy.PinnedConnection
            ? conversation.LastSourceProfileId ?? conversation.SourceProfileIdAtStart
            : null;
        var record = new ProjectRecord
        {
            Id = fingerprint,
            DisplayName = WorkspaceDisplay.PathName(normalizedPath),
            RootPath = normalizedPath,
            PathFingerprint = fingerprint,
            DefaultCli = conversation.NativeClient,
            DefaultConnectionProfileId = pinnedConnectionId,
            ResumePolicy = conversation.ResumePolicy,
            CreatedAt = conversation.CreatedAt,
            LastOpenedAt = conversation.UpdatedAt,
        };
        var project = new ProjectCardViewModel(
            record,
            WorkspaceDisplay.CliName(conversation.NativeClient),
            pinnedConnectionId is null ? "启动时选择" : "会话绑定来源",
            pathAvailable ? "临时恢复项目" : "原目录不可用",
            WorkspaceDisplay.RelativeTime(conversation.UpdatedAt),
            WorkspaceDisplay.Monogram(record.DisplayName),
            conversationCount: 1,
            codexConversationCount: conversation.NativeClient == CliKind.Codex ? 1 : 0,
            claudeConversationCount: conversation.NativeClient == CliKind.ClaudeCode ? 1 : 0,
            geminiConversationCount: conversation.NativeClient == CliKind.GeminiCli ? 1 : 0,
            pathAvailable);

        _projects.AddTransientProject(project);
        return project;
    }

    private static bool PathsEqual(string left, string right)
    {
        try
        {
            return string.Equals(
                PathIdentity.Normalize(left),
                PathIdentity.Normalize(right),
                OperatingSystem.IsWindows()
                    ? StringComparison.OrdinalIgnoreCase
                    : StringComparison.Ordinal);
        }
        catch (Exception exception) when (
            exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            return false;
        }
    }

    private void NavigateTo(string pageId)
    {
        if (!_pages.TryGetValue(pageId, out PageViewModel? page))
        {
            return;
        }

        string selectedNavigationId = pageId switch
        {
            "project-sessions" => "projects",
            "chat" when string.Equals(_chatReturnPageId, "history", StringComparison.OrdinalIgnoreCase) => "history",
            "chat" => "projects",
            _ => pageId,
        };
        foreach (NavigationItemViewModel navigationItem in NavigationItems)
        {
            navigationItem.IsSelected = string.Equals(
                navigationItem.Id,
                selectedNavigationId,
                StringComparison.OrdinalIgnoreCase);
        }

        CurrentPage = page;
        RefreshLocalTelemetryForPage(pageId);
    }

    public void NavigateFromDesktopShell(string pageId) => NavigateTo(pageId);

    private void RefreshLocalTelemetryForPage(string pageId)
    {
        if (_disposed)
        {
            return;
        }

        if (string.Equals(pageId, "overview", StringComparison.OrdinalIgnoreCase))
        {
            _ = _overview.RefreshLocalTelemetryAsync(_lifetime.Token);
        }
        else if (string.Equals(pageId, "stats", StringComparison.OrdinalIgnoreCase))
        {
            _ = _stats.RefreshLocalStatisticsAsync();
        }
    }

    private async Task ProbeBackupConnectionsAsync(
        IReadOnlyList<ConnectionProfile> profiles,
        CancellationToken cancellationToken)
    {
        try
        {
            await _networkProbeGate.WaitAsync(cancellationToken).ConfigureAwait(true);

            try
            {
                profiles = await LoadBackupProbeProfilesAsync(cancellationToken)
                    .ConfigureAwait(true);
                if (profiles.Count == 0)
                {
                    return;
                }

                WriteNetworkProbeDiagnostic($"set-loaded count={profiles.Count}");
                foreach (ConnectionProfile profile in profiles)
                {
                    await ProbeBackupConnectionAsync(profile, cancellationToken).ConfigureAwait(true);
                }

                await _overview.RefreshLocalTelemetryAsync(cancellationToken).ConfigureAwait(true);
            }
            finally
            {
                _networkProbeGate.Release();
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            // Window shutdown intentionally cancels the optional background probe.
        }
        catch
        {
            // The probe is best-effort and must never turn a normal workspace refresh into a failure.
            WriteNetworkProbeDiagnostic("set-failed");
        }
    }

    private async Task ProbeBackupConnectionAsync(
        ConnectionProfile profile,
        CancellationToken cancellationToken)
    {
        Stopwatch stopwatch = Stopwatch.StartNew();
        bool succeeded = false;
        int? elapsedMilliseconds = null;
        try
        {
            WriteNetworkProbeDiagnostic($"probe-start source={profile.Id}");
            Uri healthUri = CreateHealthProbeUri(profile);
            using var request = new HttpRequestMessage(HttpMethod.Get, healthUri);
            using HttpResponseMessage response = await NetworkProbeClient
                .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                .ConfigureAwait(true);
            stopwatch.Stop();
            elapsedMilliseconds = (int)Math.Min(stopwatch.ElapsedMilliseconds, int.MaxValue);
            // A 4xx response still proves that a configured backup is reachable.
            succeeded = (int)response.StatusCode < 500;
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (Exception exception)
        {
            stopwatch.Stop();
            elapsedMilliseconds = stopwatch.ElapsedMilliseconds > 0
                ? (int)Math.Min(stopwatch.ElapsedMilliseconds, int.MaxValue)
                : null;
            WriteNetworkProbeDiagnostic($"probe-request-failed source={profile.Id} type={exception.GetType().Name}");
        }

        LocalNetworkHealthProbe probe = CreateNetworkHealthProbe(
            profile,
            succeeded,
            elapsedMilliseconds);
        bool recorded = await RecordNetworkProbeAsync(probe, cancellationToken).ConfigureAwait(true);
        WriteNetworkProbeDiagnostic($"probe-complete source={profile.Id} recorded={recorded} succeeded={succeeded}");
    }

    private void OnNetworkProbeIntervalChanged(int minutes)
    {
        int normalized = NormalizeNetworkProbeInterval(minutes);
        if (_networkProbeIntervalMinutes == normalized)
        {
            return;
        }

        _networkProbeIntervalMinutes = normalized;
        if (_networkProbeLoopStarted)
        {
            RestartNetworkProbeLoop();
        }

        _ = _settings.SetNetworkProbeIntervalAsync(normalized, _lifetime.Token);
    }

    private void RestartNetworkProbeLoop()
    {
        CancellationTokenSource? previousCts = _networkProbeLoopCts;
        Task? previousTask = _networkProbeLoopTask;
        previousCts?.Cancel();
        if (previousCts is not null)
        {
            _ = DisposeNetworkProbeLoopAsync(previousTask, previousCts);
        }

        if (_disposed || _lifetime.IsCancellationRequested)
        {
            _networkProbeLoopCts = null;
            _networkProbeLoopTask = null;
            return;
        }

        _networkProbeLoopCts = CancellationTokenSource.CreateLinkedTokenSource(_lifetime.Token);
        _networkProbeLoopTask = RunNetworkProbeLoopAsync(_networkProbeLoopCts.Token);
    }

    private async Task RunNetworkProbeLoopAsync(CancellationToken cancellationToken)
    {
        try
        {
            while (true)
            {
                await Task.Delay(
                        TimeSpan.FromMinutes(_networkProbeIntervalMinutes),
                        cancellationToken)
                    .ConfigureAwait(true);
                _networkProbeProfiles = await LoadBackupProbeProfilesAsync(cancellationToken)
                    .ConfigureAwait(true);
                QueueBackupConnectionsProbe();
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            // Changing the interval or closing the app intentionally restarts/stops the loop.
        }
    }

    private void QueueBackupConnectionsProbe()
    {
        if (Interlocked.Exchange(ref _networkProbeRequested, 1) != 0 ||
            _disposed ||
            _lifetime.IsCancellationRequested)
        {
            return;
        }

        _ = ProcessQueuedBackupProbesAsync();
    }

    private async Task ProcessQueuedBackupProbesAsync()
    {
        try
        {
            while (Interlocked.Exchange(ref _networkProbeRequested, 0) != 0)
            {
                await ProbeBackupConnectionsAsync(_networkProbeProfiles, _lifetime.Token)
                    .ConfigureAwait(true);
            }
        }
        catch (OperationCanceledException) when (_lifetime.IsCancellationRequested)
        {
            // Closing the application intentionally stops queued background probes.
        }
    }

    private static async Task DisposeNetworkProbeLoopAsync(
        Task? loopTask,
        CancellationTokenSource cancellationTokenSource)
    {
        try
        {
            if (loopTask is not null)
            {
                await loopTask.ConfigureAwait(false);
            }
        }
        catch (OperationCanceledException)
        {
        }
        finally
        {
            cancellationTokenSource.Dispose();
        }
    }

    internal static int NormalizeNetworkProbeInterval(int minutes)
        => minutes switch
        {
            2 => 2,
            5 => 5,
            _ => 3,
        };

    internal static IReadOnlyList<ConnectionProfile> FindBackupProbeProfiles(WorkspaceDataSnapshot snapshot)
    {
        IReadOnlyList<string> backupIds = snapshot.ConnectionRouting?.BackupProfileIds ?? [];
        return backupIds
            .Select(id => snapshot.Connections.FirstOrDefault(profile =>
                profile.Kind == ConnectionProfileKind.Cloud &&
                string.Equals(profile.Id, id, StringComparison.OrdinalIgnoreCase)))
            .Where(profile => profile is not null)
            .Cast<ConnectionProfile>()
            .ToArray();
    }

    private async Task<IReadOnlyList<ConnectionProfile>> LoadBackupProbeProfilesAsync(
        CancellationToken cancellationToken)
    {
        try
        {
            WorkspaceDataSnapshot snapshot = await _dataService.LoadAsync(cancellationToken)
                .ConfigureAwait(true);
            return FindBackupProbeProfiles(snapshot);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch
        {
            // Keep the last verified routing when an optional background reload is unavailable.
            return _networkProbeProfiles;
        }
    }

    private async Task<bool> RecordNetworkProbeAsync(
        LocalNetworkHealthProbe probe,
        CancellationToken cancellationToken)
    {
        for (int attempt = 0; attempt < 2; attempt++)
        {
            try
            {
                await _localTelemetryRepository.RecordNetworkProbeAsync(probe, cancellationToken)
                    .ConfigureAwait(true);
                return true;
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                throw;
            }
            catch when (attempt == 0)
            {
                await Task.Delay(TimeSpan.FromMilliseconds(150), cancellationToken).ConfigureAwait(true);
            }
            catch
            {
                return false;
            }
        }

        return false;
    }

    internal static LocalNetworkHealthProbe CreateNetworkHealthProbe(
        ConnectionProfile profile,
        bool succeeded,
        int? latencyMilliseconds)
    {
        ArgumentNullException.ThrowIfNull(profile);
        DateTimeOffset timestamp = DateTimeOffset.UtcNow;
        try
        {
            return new LocalNetworkHealthProbe(
                timestamp,
                profile.Id,
                profile.Name,
                succeeded,
                latencyMilliseconds);
        }
        catch (ArgumentException)
        {
            // A user-supplied display name can be an endpoint URL.  Telemetry
            // intentionally rejects URLs and credentials, while the profile ID
            // remains the stable, privacy-safe key used by the UI.
            return new LocalNetworkHealthProbe(
                timestamp,
                profile.Id,
                "备用上游",
                succeeded,
                latencyMilliseconds);
        }
    }

    private static void WriteNetworkProbeDiagnostic(string message)
    {
        try
        {
            string logDirectory = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                "LanAi.Workspace",
                "Logs");
            Directory.CreateDirectory(logDirectory);
            string path = Path.Combine(logDirectory, "network-probe-diagnostics.log");
            if (File.Exists(path) && new FileInfo(path).Length > NetworkProbeDiagnosticMaximumBytes)
            {
                File.WriteAllText(path, string.Empty);
            }

            File.AppendAllText(
                path,
                $"{DateTimeOffset.UtcNow:O} {message}{Environment.NewLine}");
        }
        catch
        {
            // Diagnostics are optional and must not affect availability probing.
        }
    }

    private static Uri CreateHealthProbeUri(ConnectionProfile profile)
    {
        string endpoint = !string.IsNullOrWhiteSpace(profile.BaseUrl)
            ? profile.BaseUrl
            : profile.ClientBaseUrls.Values.FirstOrDefault() ?? string.Empty;
        if (!Uri.TryCreate(endpoint, UriKind.Absolute, out Uri? endpointUri) ||
            endpointUri.Scheme is not ("http" or "https"))
        {
            throw new InvalidOperationException("当前来源没有可探测的 HTTP 地址。");
        }

        return new Uri($"{endpointUri.Scheme}://{endpointUri.Authority}/health", UriKind.Absolute);
    }

    private void SetChildLoadingState(bool value)
    {
        _projects.IsLoading = value;
        _history.IsLoading = value;
        _connections.IsLoading = value;
    }
}

public abstract class PageViewModel : ObservableObject
{
    protected PageViewModel(string title, string subtitle)
    {
        Title = title;
        Subtitle = subtitle;
    }

    public string Title { get; }

    public string Subtitle { get; }
}

public partial class NavigationItemViewModel : ObservableObject
{
    public NavigationItemViewModel(string id, string label, string iconKey, bool isSelected = false)
    {
        Id = id;
        Label = label;
        IconKey = iconKey;
        this.isSelected = isSelected;
    }

    public string Id { get; }

    public string Label { get; }

    public string IconKey { get; }

    [ObservableProperty]
    private bool isSelected;
}

public partial class OverviewViewModel : PageViewModel
{
    private readonly ILocalTelemetryRepository _localTelemetryRepository;
    private readonly ISub2ApiSessionManager? _sessionManager;
    private readonly ILocalCloudStatisticsClient? _cloudStatisticsClient;
    private readonly Action<int>? _networkProbeIntervalChanged;
    private readonly ICloudUsageSnapshotCache _cloudUsageSnapshotCache;
    private readonly SemaphoreSlim _telemetryRefreshGate = new(1, 1);
    private bool _isApplyingNetworkProbeInterval;
    private string? _activeConnectionProfileId;
    private Sub2ApiEndpointTarget? _activeBackendTarget;
    private IReadOnlyList<ConnectionProfile> _backupProfiles = Array.Empty<ConnectionProfile>();

    public OverviewViewModel(
        ObservableCollection<ProjectCardViewModel> projects,
        ILocalTelemetryRepository? localTelemetryRepository = null)
        : this(
            projects,
            localTelemetryRepository,
            sessionManager: null,
            cloudStatisticsClient: null,
            networkProbeIntervalChanged: null,
            cloudUsageSnapshotCache: null)
    {
    }

    internal OverviewViewModel(
        ObservableCollection<ProjectCardViewModel> projects,
        ILocalTelemetryRepository? localTelemetryRepository,
        ISub2ApiSessionManager? sessionManager,
        ILocalCloudStatisticsClient? cloudStatisticsClient,
        Action<int>? networkProbeIntervalChanged = null,
        ICloudUsageSnapshotCache? cloudUsageSnapshotCache = null)
        : base("工作台", "先看这台电脑真实产生的用量、体验质量和当前连接状态。")
    {
        Projects = projects;
        _localTelemetryRepository = localTelemetryRepository ?? EmptyLocalTelemetryRepository.Instance;
        _sessionManager = sessionManager;
        _cloudStatisticsClient = cloudStatisticsClient;
        _networkProbeIntervalChanged = networkProbeIntervalChanged;
        _cloudUsageSnapshotCache = cloudUsageSnapshotCache ?? new CloudUsageSnapshotCache();
        WeeklyTrend = new ObservableCollection<UsageLineChartPoint>();
        NetworkTimeline = new ObservableCollection<NetworkTimelineBarViewModel>();
        BackupSources = new ObservableCollection<BackupSourceMonitorViewModel>();
        NetworkProbeIntervals = NetworkProbeIntervalOption.All;
        selectedNetworkProbeInterval = NetworkProbeIntervals.Single(option => option.Minutes == 3);
        ResetNetworkTimeline();
    }

    public ObservableCollection<ProjectCardViewModel> Projects { get; }

    public ObservableCollection<UsageLineChartPoint> WeeklyTrend { get; }

    public ObservableCollection<NetworkTimelineBarViewModel> NetworkTimeline { get; }

    public ObservableCollection<BackupSourceMonitorViewModel> BackupSources { get; }

    public IReadOnlyList<NetworkProbeIntervalOption> NetworkProbeIntervals { get; }

    [ObservableProperty]
    private NetworkProbeIntervalOption selectedNetworkProbeInterval = null!;

    [ObservableProperty]
    private string relayStatus = "正在检测";

    [ObservableProperty]
    private string cliStatusDetail = "等待 CLI 探测";

    [ObservableProperty]
    private string activeConnection = "正在读取";

    [ObservableProperty]
    private string connectionStatusDetail = "等待旧连接配置";

    [ObservableProperty]
    private string indexedSessions = "0";

    [ObservableProperty]
    private string sessionStatusDetail = "等待历史索引";

    [ObservableProperty]
    private bool isTelemetryLoading;

    [ObservableProperty]
    private bool hasLocalUsage;

    [ObservableProperty]
    private string telemetryStatusNotice = "正在读取本机聚合统计…";

    [ObservableProperty]
    private string telemetryLastUpdated = "尚未读取";

    [ObservableProperty]
    private string usageDataSourceLabel = "本机后台";

    [ObservableProperty]
    private string usageTrendTitle = "本机用量趋势";

    [ObservableProperty]
    private string recentHourlyTokens = "—";

    [ObservableProperty]
    private string sevenDayRequestsDetail = "0 次工作台请求";

    [ObservableProperty]
    private string cacheHitRateDetail = "近 7 日缓存复用占比";

    [ObservableProperty]
    private string averageResponseDetail = "只统计有耗时记录的请求";

    [ObservableProperty]
    private string usageEmptyNotice = "开始一次工作台对话后，这里会显示每日 Token 趋势。";

    [ObservableProperty]
    private string todayTokens = "—";

    [ObservableProperty]
    private string todayRequests = "—";

    [ObservableProperty]
    private string sevenDayTokens = "—";

    [ObservableProperty]
    private string sevenDayRequests = "—";

    [ObservableProperty]
    private string cacheHitRate = "—";

    [ObservableProperty]
    private string averageResponseTime = "—";

    [ObservableProperty]
    private string inputTokens = "—";

    [ObservableProperty]
    private string outputTokens = "—";

    [ObservableProperty]
    private string cachedInputTokens = "—";

    [ObservableProperty]
    private string activeConnectionDetail = "正在读取连接中心设置";

    [ObservableProperty]
    private string networkHealth = "尚未检测";

    [ObservableProperty]
    private string networkHealthDetail = "刷新工作区后会检查当前连接。";

    [ObservableProperty]
    private bool isNetworkHealthy;

    [ObservableProperty]
    private string activeConnectionKind = "尚未选择";

    [ObservableProperty]
    private string activeConnectionClients = "0 个客户端";

    [ObservableProperty]
    private string networkLatency = "—";

    [ObservableProperty]
    private string networkAvailability = "—";

    [ObservableProperty]
    private Brush networkAvailabilityBrush = Brushes.Gray;

    [ObservableProperty]
    private string networkProbeCount = "暂无记录";

    [ObservableProperty]
    private string networkLastChecked = "等待探测";

    public bool HasNoWeeklyTrend => HasLocalUsage && WeeklyTrend.Count == 0;

    public bool HasNoLocalUsage => !HasLocalUsage;

    public string UsageToday => TodayTokens;

    internal void ApplySnapshot(WorkspaceDataSnapshot snapshot)
    {
        int installed = snapshot.CliInstallations.Count(installation => installation.IsInstalled);
        RelayStatus = $"{installed}/{Enum.GetValues<CliKind>().Length} 可用";
        CliStatusDetail = installed == 0
            ? "未检测到官方 CLI"
            : string.Join(
                " · ",
                snapshot.CliInstallations
                    .Where(installation => installation.IsInstalled)
                    .Select(installation => WorkspaceDisplay.CliName(installation.Kind)));

        ConnectionProfile? activeProfile = ConnectionSourceResolver.FindActiveProfile(
            snapshot.Connections,
            snapshot.ConnectionSelection,
            snapshot.ConnectionRouting);
        _activeConnectionProfileId = activeProfile?.Id;
        ConnectionProfile? localBackendProfile = snapshot.Connections.FirstOrDefault(connection =>
            string.Equals(connection.Id, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase));
        _activeBackendTarget = localBackendProfile is not null &&
            Sub2ApiEndpointSelector.TryCreate(localBackendProfile, out Sub2ApiEndpointTarget? localBackendTarget)
                ? localBackendTarget
                : null;
        ActiveConnection = activeProfile?.Name ?? "未选择来源";
        int fixedCount = snapshot.Connections.Count(connection =>
            connection.Kind is ConnectionProfileKind.Local or ConnectionProfileKind.Lan);
        ConnectionStatusDetail = activeProfile is null
            ? fixedCount > 0
                ? "请在连接中心选择本机、局域网或远程来源"
                : "尚未发现可用的连接来源"
            : activeProfile.Kind switch
            {
                ConnectionProfileKind.Local => "当前电脑上的本机中转",
                ConnectionProfileKind.Lan => "局域网中另一台电脑提供的中转",
                _ => "当前选择的远程中转来源",
            };
        ActiveConnectionDetail = activeProfile is null
            ? "尚未选择连接；网络探测会在来源可用后开始。"
            : $"{ConnectionStatusDetail} · 已配置 {activeProfile.EnabledClients.Count} 个客户端";
        ActiveConnectionKind = activeProfile?.Kind switch
        {
            ConnectionProfileKind.Local => "本机中转",
            ConnectionProfileKind.Lan => "局域网中转",
            ConnectionProfileKind.Cloud => "云端来源",
            _ => "尚未选择",
        };
        ActiveConnectionClients = activeProfile is null
            ? "0 个客户端"
            : $"{activeProfile.EnabledClients.Count:N0} 个客户端";

        IReadOnlyList<string> backupIds = snapshot.ConnectionRouting?.BackupProfileIds ?? [];
        _backupProfiles = backupIds
            .Select(id => snapshot.Connections.FirstOrDefault(profile =>
                profile.Kind == ConnectionProfileKind.Cloud &&
                string.Equals(profile.Id, id, StringComparison.OrdinalIgnoreCase)))
            .Where(profile => profile is not null)
            .Cast<ConnectionProfile>()
            .ToArray();
        BackupSources.Clear();
        foreach (ConnectionProfile profile in _backupProfiles)
        {
            BackupSources.Add(new BackupSourceMonitorViewModel(
                BackupSources.Count + 1,
                profile.Name,
                summary: null,
                Array.Empty<LocalNetworkHealthProbe>()));
        }

        IndexedSessions = snapshot.Conversations.Count.ToString(System.Globalization.CultureInfo.InvariantCulture);
        SessionStatusDetail = snapshot.Conversations.Count == 0
            ? "官方会话目录中暂无记录"
            : $"跨 {snapshot.Conversations.Select(item => item.NativeClient).Distinct().Count()} 类 CLI";
    }

    [RelayCommand]
    private Task RefreshOverviewAsync()
        => RefreshLocalTelemetryAsync();

    internal async Task RefreshLocalTelemetryAsync(CancellationToken cancellationToken = default)
    {
        if (!await _telemetryRefreshGate.WaitAsync(0, cancellationToken).ConfigureAwait(true))
        {
            return;
        }

        IsTelemetryLoading = true;
        try
        {
            Task<StatsSnapshot?> usageTask = TryLoadLocalSub2ApiUsageAsync(cancellationToken);
            Task<IReadOnlyList<LocalNetworkHealthSummary>> summariesTask = _backupProfiles.Count == 0
                ? Task.FromResult<IReadOnlyList<LocalNetworkHealthSummary>>(Array.Empty<LocalNetworkHealthSummary>())
                : _localTelemetryRepository.GetNetworkHealthSummariesAsync(
                    DateTimeOffset.UtcNow.AddDays(-7),
                    cancellationToken);
            Task<IReadOnlyList<LocalNetworkHealthProbe>[]> historyTask = Task.WhenAll(
                _backupProfiles.Select(profile => _localTelemetryRepository.GetRecentNetworkProbesAsync(
                    profile.Id,
                    60,
                    cancellationToken)));

            await Task.WhenAll(usageTask, summariesTask, historyTask).ConfigureAwait(true);
            ApplyBackendUsageSnapshot(await usageTask.ConfigureAwait(true));
            ApplyBackupSourceMonitor(
                await summariesTask.ConfigureAwait(true),
                await historyTask.ConfigureAwait(true));
            TelemetryLastUpdated = $"本机后台 · 更新于 {DateTimeOffset.Now:HH:mm:ss}";
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            TelemetryStatusNotice = "本机后台用量读取已取消。";
        }
        catch
        {
            TelemetryStatusNotice = "本机后台用量暂时无法读取，请确认后台已启动并完成授权。";
        }
        finally
        {
            IsTelemetryLoading = false;
            _telemetryRefreshGate.Release();
        }
    }

    private async Task<StatsSnapshot?> TryLoadLocalSub2ApiUsageAsync(CancellationToken cancellationToken)
    {
        if (_activeBackendTarget is null || _sessionManager is null || _cloudStatisticsClient is null)
        {
            return null;
        }

        try
        {
            bool sameAuthenticatedEndpoint = _sessionManager.Current is
            {
                IsAuthenticated: true,
                ApiBaseUri: not null,
            } current && SameEndpoint(current.ApiBaseUri, _activeBackendTarget.ApiBaseUri);
            if (!sameAuthenticatedEndpoint)
            {
                await _sessionManager
                    .RestoreAsync(_activeBackendTarget.ApiBaseUri, cancellationToken)
                    .ConfigureAwait(true);
            }

            if (_sessionManager.Current is not
                {
                    IsAuthenticated: true,
                    ApiBaseUri: not null,
                } restored || !SameEndpoint(restored.ApiBaseUri, _activeBackendTarget.ApiBaseUri))
            {
                return null;
            }

            Sub2ApiSessionAccess access = await _sessionManager
                .GetAccessAsync(_activeBackendTarget.ApiBaseUri, cancellationToken)
                .ConfigureAwait(true);
            CloudUsageSnapshotCacheResult cached = await _cloudUsageSnapshotCache
                .GetOrLoadAsync(
                    _activeBackendTarget.ApiBaseUri,
                    $"user:{access.UserId}:{(access.IsAdministrator ? "admin" : "user")}",
                    trendDays: 7,
                    forceRefresh: false,
                    token => _cloudStatisticsClient.RefreshWithAccessTokenAsync(
                        _activeBackendTarget.ApiBaseUri.AbsoluteUri,
                        access.AccessToken,
                        trendDays: 7,
                        access.IsAdministrator,
                        token),
                    cancellationToken)
                .ConfigureAwait(true);
            return cached.Snapshot;
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch
        {
            return null;
        }
    }

    internal bool ApplyBackendUsageSnapshot(StatsSnapshot? snapshot)
    {
        UsageDataSourceLabel = "本机后台";
        UsageTrendTitle = "本机用量趋势";
        if (snapshot is null)
        {
            TelemetryStatusNotice = "本机后台用量暂时无法读取，请确认后台已启动并完成授权。";
            return false;
        }

        ApplySub2ApiUsageSnapshot(snapshot);
        return true;
    }

    private void ApplySub2ApiUsageSnapshot(StatsSnapshot snapshot)
    {
        StatsOverview today = snapshot.Overview;
        UsageRangeOverview range = snapshot.RangeOverview ?? UsageRangeOverview.FromTrend(snapshot.Trend);
        HasLocalUsage = today.TodayRequests > 0 || range.TotalRequests > 0;
        TodayTokens = FormatCount(today.TodayTokens);
        TodayRequests = FormatCount(today.TodayRequests);
        SevenDayTokens = FormatCount(range.TotalTokens);
        SevenDayRequests = FormatCount(range.TotalRequests);
        AverageResponseTime = FormatLatency(range.AverageDurationMs > 0 ? range.AverageDurationMs : null);
        InputTokens = FormatCount(range.TotalInputTokens);
        OutputTokens = FormatCount(range.TotalOutputTokens);
        CachedInputTokens = FormatCount(range.TotalCacheReadTokens);
        double cacheDenominator = range.TotalInputTokens +
            range.TotalCacheReadTokens +
            range.TotalCacheCreationTokens;
        CacheHitRate = cacheDenominator > 0
            ? FormatPercent(range.TotalCacheReadTokens * 100d / cacheDenominator)
            : "—";
        UsageDataSourceLabel = "本机后台";
        UsageTrendTitle = "本机用量趋势";
        SevenDayRequestsDetail = $"{FormatCount(range.TotalRequests)} 次后台请求";
        CacheHitRateDetail = "近 7 日缓存复用占比";
        AverageResponseDetail = "本机后台近 7 日平均响应";
        UsageEmptyNotice = "本机后台近 7 日没有用量记录。";

        WeeklyTrend.Clear();
        foreach (TrendPoint point in snapshot.Trend.OrderBy(item => item.Date, StringComparer.OrdinalIgnoreCase))
        {
            WeeklyTrend.Add(new UsageLineChartPoint(
                FormatCloudChartDate(point.Date),
                point.TotalTokens,
                $"{FormatCount(point.Requests)} 次请求 · {FormatCount(point.TotalTokens)} Token"));
        }

        TelemetryStatusNotice = "数据来自本机后台。";
        OnPropertyChanged(nameof(HasNoWeeklyTrend));
        OnPropertyChanged(nameof(UsageToday));
    }

    private static string FormatCloudChartDate(string value)
        => DateOnly.TryParseExact(
            value,
            "yyyy-MM-dd",
            System.Globalization.CultureInfo.InvariantCulture,
            System.Globalization.DateTimeStyles.None,
            out DateOnly date)
            ? date.ToString("M/d", System.Globalization.CultureInfo.CurrentCulture)
            : value;

    private static bool SameEndpoint(Uri left, Uri right)
        => Uri.Compare(
            left,
            right,
            UriComponents.SchemeAndServer | UriComponents.Path,
            UriFormat.SafeUnescaped,
            StringComparison.OrdinalIgnoreCase) == 0;

    private void ApplyNetworkHealth(LocalNetworkHealthStatus? status)
    {
        if (status is null)
        {
            NetworkHealth = "尚未检测";
            NetworkHealthDetail = "刷新工作区后会探测当前来源；只记录状态、耗时和来源名称。";
            IsNetworkHealthy = false;
            return;
        }

        string source = string.IsNullOrWhiteSpace(status.SourceLabel)
            ? "当前来源"
            : status.SourceLabel;
        string latency = status.LatencyMilliseconds is { } milliseconds
            ? $"{milliseconds:N0} ms"
            : "未返回耗时";
        NetworkHealth = status.Succeeded ? "连接正常" : "连接异常";
        NetworkHealthDetail = $"{source} · {latency} · {status.CheckedAt.ToLocalTime():HH:mm} 探测";
        IsNetworkHealthy = status.Succeeded;
    }

    internal void ApplySourceMonitor(
        LocalNetworkHealthSummary? summary,
        IReadOnlyList<LocalNetworkHealthProbe> history)
    {
        ArgumentNullException.ThrowIfNull(history);
        NetworkTimeline.Clear();
        int realCount = Math.Min(60, history.Count);
        for (int index = realCount; index < 60; index++)
        {
            NetworkTimeline.Add(NetworkTimelineBarViewModel.Empty);
        }

        foreach (LocalNetworkHealthProbe point in history.TakeLast(60))
        {
            NetworkTimeline.Add(NetworkTimelineBarViewModel.FromProbe(point));
        }

        NetworkProbeCount = realCount == 0 ? "暂无记录" : $"已记录 {realCount:N0} 次";
        NetworkAvailability = summary?.SuccessRatePercent is { } availability
            ? $"{availability:N2}%"
            : "—";
        NetworkAvailabilityBrush = CreateAvailabilityBrush(summary?.SuccessRatePercent);

        LocalNetworkHealthProbe? latest = history.LastOrDefault();
        if (latest is null)
        {
            NetworkLatency = "—";
            NetworkLastChecked = "等待探测";
            return;
        }

        NetworkLatency = latest.LatencyMilliseconds is { } latency
            ? $"{latency:N0} ms"
            : "—";
        NetworkLastChecked = $"{latest.Timestamp.ToLocalTime():HH:mm} 探测";
        NetworkHealth = latest.Succeeded ? "连接正常" : "连接异常";
        NetworkHealthDetail = latest.Succeeded
            ? "当前来源可以正常访问"
            : DescribeProbeFailure(latest.StatusCategory);
        IsNetworkHealthy = latest.Succeeded;
    }

    private void ApplyBackupSourceMonitor(
        IReadOnlyList<LocalNetworkHealthSummary> summaries,
        IReadOnlyList<IReadOnlyList<LocalNetworkHealthProbe>> histories)
    {
        BackupSources.Clear();
        for (int index = 0; index < _backupProfiles.Count; index++)
        {
            ConnectionProfile profile = _backupProfiles[index];
            LocalNetworkHealthSummary? summary = summaries.FirstOrDefault(item =>
                string.Equals(item.SourceId, profile.Id, StringComparison.OrdinalIgnoreCase));
            IReadOnlyList<LocalNetworkHealthProbe> history = index < histories.Count
                ? histories[index]
                : Array.Empty<LocalNetworkHealthProbe>();
            BackupSources.Add(new BackupSourceMonitorViewModel(index + 1, profile.Name, summary, history));
        }
    }

    internal void SetNetworkProbeInterval(int minutes)
    {
        int normalized = MainWindowViewModel.NormalizeNetworkProbeInterval(minutes);
        NetworkProbeIntervalOption option = NetworkProbeIntervals
            .Single(candidate => candidate.Minutes == normalized);
        _isApplyingNetworkProbeInterval = true;
        try
        {
            SelectedNetworkProbeInterval = option;
        }
        finally
        {
            _isApplyingNetworkProbeInterval = false;
        }
    }

    private void ResetNetworkTimeline()
    {
        NetworkTimeline.Clear();
        for (int index = 0; index < 60; index++)
        {
            NetworkTimeline.Add(NetworkTimelineBarViewModel.Empty);
        }
    }

    private static string DescribeProbeFailure(string? category)
        => category?.Trim().ToLowerInvariant() switch
        {
            "authentication" => "身份验证失败，请检查当前来源的密钥",
            "timeout" => "连接超时，请检查网络或代理",
            "dns" => "域名解析失败，请检查网络设置",
            "rate_limit" => "当前来源触发限流，请稍后重试",
            _ => "当前来源暂时无法访问，请重新检测",
        };

    private static Brush CreateAvailabilityBrush(double? percentage)
    {
        if (percentage is null || double.IsNaN(percentage.Value))
        {
            return Brushes.Gray;
        }

        double hue = Math.Clamp(percentage.Value, 0d, 100d) * 1.2d;
        Color color = ColorFromHsl(hue, 0.72d, 0.42d);
        var brush = new SolidColorBrush(color);
        brush.Freeze();
        return brush;
    }

    private static Color ColorFromHsl(double hue, double saturation, double lightness)
    {
        double chroma = (1d - Math.Abs((2d * lightness) - 1d)) * saturation;
        double hueSection = hue / 60d;
        double component = chroma * (1d - Math.Abs((hueSection % 2d) - 1d));
        (double red, double green, double blue) = hueSection switch
        {
            < 1d => (chroma, component, 0d),
            < 2d => (component, chroma, 0d),
            < 3d => (0d, chroma, component),
            < 4d => (0d, component, chroma),
            < 5d => (component, 0d, chroma),
            _ => (chroma, 0d, component),
        };
        double match = lightness - (chroma / 2d);
        return Color.FromRgb(
            (byte)Math.Round((red + match) * 255d),
            (byte)Math.Round((green + match) * 255d),
            (byte)Math.Round((blue + match) * 255d));
    }

    private static string FormatCount(long value)
    {
        if (Math.Abs(value) >= 100_000_000)
        {
            return $"{value / 100_000_000d:N2}亿";
        }

        if (Math.Abs(value) >= 10_000)
        {
            return $"{value / 10_000d:N2}万";
        }

        return value.ToString("N0", System.Globalization.CultureInfo.CurrentCulture);
    }

    private static string FormatPercent(double? value)
        => value is null ? "—" : $"{value.Value:N1}%";

    private static string FormatLatency(double? value)
        => value is null ? "—" : $"{value.Value:N0} ms";

    partial void OnHasLocalUsageChanged(bool value)
    {
        OnPropertyChanged(nameof(HasNoWeeklyTrend));
        OnPropertyChanged(nameof(HasNoLocalUsage));
    }

    partial void OnSelectedNetworkProbeIntervalChanged(NetworkProbeIntervalOption value)
    {
        if (!_isApplyingNetworkProbeInterval && value is not null)
        {
            _networkProbeIntervalChanged?.Invoke(value.Minutes);
        }
    }

    private sealed class EmptyLocalTelemetryRepository : ILocalTelemetryRepository
    {
        public static EmptyLocalTelemetryRepository Instance { get; } = new();

        public Task RecordUsageAsync(
            LocalUsageTelemetryEvent telemetryEvent,
            CancellationToken cancellationToken = default)
            => Task.CompletedTask;

        public Task RecordNetworkProbeAsync(
            LocalNetworkHealthProbe probe,
            CancellationToken cancellationToken = default)
            => Task.CompletedTask;

        public Task<LocalTelemetrySnapshot> GetSnapshotAsync(
            TimeZoneInfo? timeZone = null,
            CancellationToken cancellationToken = default)
            => Task.FromResult(new LocalTelemetrySnapshot(
                DateTimeOffset.UtcNow,
                LocalTelemetryUsageSummary.Empty,
                LocalTelemetryUsageSummary.Empty,
                Array.Empty<LocalTelemetryDailyUsage>(),
                null));
    }
}

public sealed record NetworkProbeIntervalOption(int Minutes, string Label)
{
    public static IReadOnlyList<NetworkProbeIntervalOption> All { get; } =
    [
        new(2, "2 分钟"),
        new(3, "3 分钟"),
        new(5, "5 分钟"),
    ];
}

public sealed class BackupSourceMonitorViewModel
{
    public BackupSourceMonitorViewModel(
        int order,
        string name,
        LocalNetworkHealthSummary? summary,
        IReadOnlyList<LocalNetworkHealthProbe> history)
    {
        Order = $"备用 {order}";
        Name = string.IsNullOrWhiteSpace(name) ? "未命名备用上游" : name;
        Availability = summary?.SuccessRatePercent is { } value ? $"{value:N2}%" : "暂无记录";
        ProbeCount = summary is null ? "0 次" : $"{summary.ProbeCount:N0} 次";
        Status = summary?.LatestStatusCategory is null ? "待命" : DescribeStatus(summary.LatestStatusCategory);
        Timeline = new ObservableCollection<NetworkTimelineBarViewModel>();
        int realCount = Math.Min(60, history.Count);
        for (int index = realCount; index < 60; index++)
        {
            Timeline.Add(NetworkTimelineBarViewModel.Empty);
        }
        foreach (LocalNetworkHealthProbe point in history.TakeLast(60))
        {
            Timeline.Add(NetworkTimelineBarViewModel.FromProbe(point));
        }
    }

    public string Order { get; }

    public string Name { get; }

    public string Availability { get; }

    public string ProbeCount { get; }

    public string Status { get; }

    public ObservableCollection<NetworkTimelineBarViewModel> Timeline { get; }

    private static string DescribeStatus(string category)
        => category.Equals("success", StringComparison.OrdinalIgnoreCase) ? "最近正常" : "最近异常";
}

public sealed class NetworkTimelineBarViewModel
{
    private static readonly Brush EmptyBrush = CreateFrozenBrush(0xD1, 0xD5, 0xDB);
    private static readonly Brush SuccessBrush = CreateFrozenBrush(0x10, 0xB9, 0x81);
    private static readonly Brush FailureBrush = CreateFrozenBrush(0xEF, 0x44, 0x44);

    private NetworkTimelineBarViewModel(double height, Brush fill, string toolTip)
    {
        Height = height;
        Fill = fill;
        ToolTip = toolTip;
    }

    public static NetworkTimelineBarViewModel Empty { get; } = new(4d, EmptyBrush, "尚无探测记录");

    public double Height { get; }

    public Brush Fill { get; }

    public string ToolTip { get; }

    public static NetworkTimelineBarViewModel FromProbe(LocalNetworkHealthProbe probe)
    {
        string status = probe.Succeeded ? "连接正常" : "连接失败";
        string latency = probe.LatencyMilliseconds is { } milliseconds ? $"{milliseconds:N0} ms" : "未返回耗时";
        return new NetworkTimelineBarViewModel(
            probe.Succeeded ? 24d : 10d,
            probe.Succeeded ? SuccessBrush : FailureBrush,
            $"{probe.Timestamp.ToLocalTime():M/d HH:mm} · {status} · {latency}");
    }

    private static Brush CreateFrozenBrush(byte red, byte green, byte blue)
    {
        var brush = new SolidColorBrush(Color.FromRgb(red, green, blue));
        brush.Freeze();
        return brush;
    }
}

public partial class ProjectsViewModel : PageViewModel
{
    private readonly Func<string, Task> _addProject;
    private readonly Func<ProjectCardViewModel, Task<ProjectRemovalOutcome>> _deleteProject;
    private readonly Func<Task> _refresh;
    private readonly Action<ProjectCardViewModel> _openProject;

    [ObservableProperty]
    private string searchText = string.Empty;

    [ObservableProperty]
    private bool isLoading;

    [ObservableProperty]
    private bool hasProjects;

    [ObservableProperty]
    private string statusNotice = "首次启动会从官方历史记录中自动发现项目。";

    [ObservableProperty]
    private bool isCleanupConfirmationVisible;

    [ObservableProperty]
    private bool isCleaningInvalidProjects;

    public ProjectsViewModel(
        ObservableCollection<ProjectCardViewModel> workspaceProjects,
        Func<string, Task> addProject,
        Func<ProjectCardViewModel, Task<ProjectRemovalOutcome>> deleteProject,
        Func<Task> refresh,
        Action<ProjectCardViewModel> openProject)
        : base("项目中心", "项目与 API 配置相互独立；切换连接不会改变项目归属。")
    {
        WorkspaceProjects = workspaceProjects ?? throw new ArgumentNullException(nameof(workspaceProjects));
        _addProject = addProject ?? throw new ArgumentNullException(nameof(addProject));
        _deleteProject = deleteProject ?? throw new ArgumentNullException(nameof(deleteProject));
        _refresh = refresh ?? throw new ArgumentNullException(nameof(refresh));
        _openProject = openProject ?? throw new ArgumentNullException(nameof(openProject));
        Projects = new ObservableCollection<ProjectCardViewModel>();
    }

    public ObservableCollection<ProjectCardViewModel> WorkspaceProjects { get; }

    public ObservableCollection<ProjectCardViewModel> Projects { get; }

    public int InvalidProjectCount => WorkspaceProjects.Count(project => !project.PathAvailable);

    public bool HasInvalidProjects => InvalidProjectCount > 0;

    public string CleanupWarning
    {
        get
        {
            ProjectCardViewModel[] invalid = WorkspaceProjects
                .Where(project => !project.PathAvailable)
                .ToArray();
            return $"将永久删除 {invalid.Length} 个失效项目及已索引官方历史：Codex {invalid.Sum(project => project.CodexConversationCount)} 条 · Claude Code {invalid.Sum(project => project.ClaudeConversationCount)} 条 · Gemini CLI {invalid.Sum(project => project.GeminiConversationCount)} 条。有 Claude Code 会话时会用官方 project purge 清理；源码文件夹不会删除。";
        }
    }

    internal void ApplySnapshot(WorkspaceDataSnapshot snapshot)
    {
        var connectionNames = snapshot.Connections.ToDictionary(
            connection => connection.Id,
            connection => connection.Name,
            StringComparer.OrdinalIgnoreCase);
        var installations = snapshot.CliInstallations.ToDictionary(
            installation => installation.Kind,
            installation => installation);

        ProjectCardViewModel[] cards = snapshot.Projects
            .Select(project =>
            {
                ConversationRecord[] conversations = snapshot.Conversations
                    .Where(conversation =>
                        string.Equals(conversation.ProjectId, project.PathFingerprint, StringComparison.OrdinalIgnoreCase) ||
                        string.Equals(conversation.ProjectId, project.Id, StringComparison.OrdinalIgnoreCase))
                    .ToArray();
                DateTimeOffset? latestActivity = conversations.Length > 0
                    ? conversations.Max(conversation => conversation.UpdatedAt)
                    : project.LastOpenedAt;
                installations.TryGetValue(project.DefaultCli, out CliInstallation? installation);
                bool pathAvailable = Directory.Exists(project.RootPath);
                string status = !pathAvailable
                    ? "目录不可用"
                    : installation?.IsInstalled == false
                        ? $"{WorkspaceDisplay.CliName(project.DefaultCli)} 未安装"
                        : $"{conversations.Length} 条会话";

                string connectionName = project.DefaultConnectionProfileId is { Length: > 0 } profileId &&
                                        connectionNames.TryGetValue(profileId, out string? configuredName)
                    ? configuredName
                    : "启动时选择";

                int codexConversations = conversations.Count(conversation =>
                    conversation.NativeClient == CliKind.Codex);
                int claudeConversations = conversations.Count(conversation =>
                    conversation.NativeClient == CliKind.ClaudeCode);
                int geminiConversations = conversations.Count(conversation =>
                    conversation.NativeClient == CliKind.GeminiCli);

                return new ProjectCardViewModel(
                    project,
                    WorkspaceDisplay.CliName(project.DefaultCli),
                    connectionName,
                    status,
                    WorkspaceDisplay.RelativeTime(latestActivity),
                    WorkspaceDisplay.Monogram(project.DisplayName),
                    conversations.Length,
                    codexConversations,
                    claudeConversations,
                    geminiConversations,
                    pathAvailable);
            })
            .ToArray();

        WorkspaceProjects.Clear();
        foreach (ProjectCardViewModel card in cards)
        {
            WorkspaceProjects.Add(card);
        }

        ApplyFilter();
        IsCleanupConfirmationVisible = false;
        NotifyInvalidProjectState();
        StatusNotice = snapshot.Errors.Count == 0
            ? snapshot.DiscoveredProjectCount > 0
                ? $"已从官方历史中发现并保存 {snapshot.DiscoveredProjectCount} 个项目。"
                : $"项目数据库已同步，共 {cards.Length} 个项目。"
            : WorkspaceDisplay.ErrorSummary(snapshot.Errors);
    }

    internal void SetLoadFailure(string message) => StatusNotice = $"项目加载失败：{message}";

    internal void AddTransientProject(ProjectCardViewModel project)
    {
        if (WorkspaceProjects.Any(candidate =>
                string.Equals(candidate.PathFingerprint, project.PathFingerprint, StringComparison.OrdinalIgnoreCase)))
        {
            return;
        }

        WorkspaceProjects.Insert(0, project);
        ApplyFilter();
        NotifyInvalidProjectState();
        StatusNotice = project.PathAvailable
            ? "已按历史会话的原工作目录创建临时恢复项目。"
            : "历史会话的原工作目录当前不可用；恢复意图已保留。";
    }

    partial void OnSearchTextChanged(string value) => ApplyFilter();

    [RelayCommand]
    private async Task AddProjectAsync()
    {
        var dialog = new OpenFolderDialog
        {
            Title = "选择项目根目录",
            Multiselect = false,
        };

        if (dialog.ShowDialog() != true)
        {
            return;
        }

        try
        {
            StatusNotice = "正在保存项目…";
            await _addProject(dialog.FolderName);
        }
        catch (Exception exception)
        {
            StatusNotice = $"添加项目失败：{exception.Message}";
        }
    }

    [RelayCommand]
    private Task RefreshProjectsAsync() => _refresh();

    [RelayCommand]
    private void RequestDelete(ProjectCardViewModel? project)
    {
        if (project is null || project.IsDeleting)
        {
            return;
        }

        foreach (ProjectCardViewModel candidate in WorkspaceProjects)
        {
            candidate.IsDeleteConfirmationVisible = ReferenceEquals(candidate, project);
        }
    }

    [RelayCommand]
    private static void CancelDelete(ProjectCardViewModel? project)
    {
        if (project is not null && !project.IsDeleting)
        {
            project.IsDeleteConfirmationVisible = false;
        }
    }

    [RelayCommand]
    private async Task ConfirmDeleteAsync(ProjectCardViewModel? project)
    {
        if (project is null || project.IsDeleting)
        {
            return;
        }

        project.IsDeleting = true;
        StatusNotice = $"正在永久删除“{project.Name}”的官方历史与本机项目记录…";
        try
        {
            ProjectRemovalOutcome outcome = await _deleteProject(project);
            if (outcome.Succeeded)
            {
                WorkspaceProjects.Remove(project);
                ApplyFilter();
                NotifyInvalidProjectState();
                await _refresh();
            }

            StatusNotice = outcome.Message;
        }
        catch (OperationCanceledException)
        {
            StatusNotice = "删除已取消；项目记录与尚未删除的官方历史均已保留。";
        }
        catch (Exception exception)
        {
            StatusNotice = $"删除失败：{exception.Message}；项目记录已保留。";
        }
        finally
        {
            project.IsDeleting = false;
            project.IsDeleteConfirmationVisible = false;
        }
    }

    [RelayCommand]
    private void RequestCleanupInvalidProjects()
    {
        if (HasInvalidProjects && !IsCleaningInvalidProjects)
        {
            IsCleanupConfirmationVisible = true;
        }
    }

    [RelayCommand]
    private void CancelCleanupInvalidProjects()
    {
        if (!IsCleaningInvalidProjects)
        {
            IsCleanupConfirmationVisible = false;
        }
    }

    [RelayCommand]
    private async Task ConfirmCleanupInvalidProjectsAsync()
    {
        if (!HasInvalidProjects || IsCleaningInvalidProjects)
        {
            IsCleanupConfirmationVisible = false;
            return;
        }

        ProjectCardViewModel[] invalidProjects = WorkspaceProjects
            .Where(project => !project.PathAvailable)
            .ToArray();
        IsCleaningInvalidProjects = true;
        StatusNotice = $"正在清理 {invalidProjects.Length} 个失效项目及其官方历史…";
        int deleted = 0;
        var failures = new List<string>();
        try
        {
            // Official clients update shared indexes, so project deletion is
            // deliberately serialized to avoid file-lock and lost-update races.
            foreach (ProjectCardViewModel project in invalidProjects)
            {
                ProjectRemovalOutcome outcome = await _deleteProject(project);
                if (outcome.Succeeded)
                {
                    WorkspaceProjects.Remove(project);
                    deleted++;
                }
                else
                {
                    failures.Add($"{project.Name}：{outcome.Message}");
                }
            }

            ApplyFilter();
            NotifyInvalidProjectState();
            if (deleted > 0)
            {
                await _refresh();
            }

            StatusNotice = failures.Count == 0
                ? $"已清理 {deleted} 个失效项目及其官方历史；源码文件夹没有删除。"
                : $"已清理 {deleted} 个；{failures.Count} 个保留。{string.Join("；", failures.Take(3))}";
        }
        catch (OperationCanceledException)
        {
            StatusNotice = $"批量清理已取消；此前已完成 {deleted} 个项目。";
        }
        finally
        {
            IsCleaningInvalidProjects = false;
            IsCleanupConfirmationVisible = false;
        }
    }

    [RelayCommand]
    private void OpenProject(ProjectCardViewModel? project)
    {
        if (project is not null)
        {
            _openProject(project);
        }
    }

    private void ApplyFilter()
    {
        string query = SearchText.Trim();
        IEnumerable<ProjectCardViewModel> filtered = WorkspaceProjects;
        if (query.Length > 0)
        {
            filtered = filtered.Where(project =>
                project.Name.Contains(query, StringComparison.OrdinalIgnoreCase) ||
                project.Path.Contains(query, StringComparison.OrdinalIgnoreCase));
        }

        Projects.Clear();
        foreach (ProjectCardViewModel project in filtered)
        {
            Projects.Add(project);
        }

        HasProjects = Projects.Count > 0;
    }

    private void NotifyInvalidProjectState()
    {
        OnPropertyChanged(nameof(InvalidProjectCount));
        OnPropertyChanged(nameof(HasInvalidProjects));
        OnPropertyChanged(nameof(CleanupWarning));
    }
}

public partial class ProjectCardViewModel : ObservableObject
{
    public ProjectCardViewModel(
        ProjectRecord record,
        string preferredCli,
        string connection,
        string status,
        string lastActivity,
        string monogram,
        int conversationCount,
        int codexConversationCount,
        int claudeConversationCount,
        int geminiConversationCount,
        bool pathAvailable)
    {
        Record = record;
        PreferredCli = preferredCli;
        Connection = connection;
        Status = status;
        LastActivity = lastActivity;
        Monogram = monogram;
        ConversationCount = conversationCount;
        CodexConversationCount = codexConversationCount;
        ClaudeConversationCount = claudeConversationCount;
        GeminiConversationCount = geminiConversationCount;
        PathAvailable = pathAvailable;
    }

    public ProjectRecord Record { get; }

    public string Id => Record.Id;

    public string PathFingerprint => Record.PathFingerprint;

    public string Name => Record.DisplayName;

    public string Path => Record.RootPath;

    public string PreferredCli { get; }

    public string Connection { get; }

    public string Status { get; }

    public string LastActivity { get; }

    public string Monogram { get; }

    public int ConversationCount { get; }

    public int CodexConversationCount { get; }

    public int ClaudeConversationCount { get; }

    public int GeminiConversationCount { get; }

    public string DeleteWarning => ConversationCount == 0
        ? "将从本机项目列表永久删除。未找到已索引官方历史，因此不会调用三类官方删除命令。源码文件夹不会删除。"
        : $"同时永久删除已索引官方历史：Codex {CodexConversationCount} 条 · Claude Code {ClaudeConversationCount} 条 · Gemini CLI {GeminiConversationCount} 条。有 Claude Code 会话时会用官方 project purge 清理；源码文件夹不会删除。";

    public bool PathAvailable { get; }

    [ObservableProperty]
    private bool isDeleteConfirmationVisible;

    [ObservableProperty]
    private bool isDeleting;
}

public sealed record ProjectRemovalOutcome(bool Succeeded, string Message)
{
    public static ProjectRemovalOutcome Completed(string message) => new(true, message);

    public static ProjectRemovalOutcome Failed(string message) => new(false, message);
}

internal sealed record TerminalProjectRefreshState(
    ProjectCardViewModel? SelectedProject,
    string SelectedCli,
    ConversationRecord? PendingConversation);

public partial class TerminalViewModel : PageViewModel
{
    private readonly Dictionary<string, string> _connectionLabels = new(StringComparer.OrdinalIgnoreCase);
    private readonly Action? _returnToPreviousPage;
    private const string MissingPinnedConnectionPrefix = "绑定来源不可用 · ";
    private bool _isApplyingProjectSnapshot;
    private bool _autoStartRequested;
    private string? _activeConnectionProfileId;
    private string selectedConnection = "连接中心尚未选择有效来源";

    [ObservableProperty]
    private ProjectCardViewModel? selectedProject;

    [ObservableProperty]
    private string selectedCli = "Codex";

    [ObservableProperty]
    private string terminalNotice = "选择项目与官方 CLI 后即可启动；连接来源由连接中心统一控制。";

    [ObservableProperty]
    private ConversationRecord? pendingConversation;

    public TerminalViewModel(
        ObservableCollection<ProjectCardViewModel> projects,
        IConnectionProfileReader? connectionProfileReader = null,
        IConnectionCredentialProvider? credentialProvider = null,
        Action? returnToPreviousPage = null)
        : base("命令行对话", "在项目目录中承载官方 CLI；保留斜杠命令、工具调用与权限确认。")
    {
        if ((connectionProfileReader is null) != (credentialProvider is null))
        {
            throw new ArgumentException("终端连接读取器与凭据提供器必须同时提供。 ");
        }

        Projects = projects;
        ConnectionProfileReader = connectionProfileReader;
        CredentialProvider = credentialProvider;
        _returnToPreviousPage = returnToPreviousPage;
        CliOptions = new ObservableCollection<string> { "Codex", "Claude Code", "Gemini CLI" };
        Projects.CollectionChanged += (_, _) =>
        {
            if (!_isApplyingProjectSnapshot)
            {
                EnsureSelectedProject();
            }
        };
    }

    public ObservableCollection<ProjectCardViewModel> Projects { get; }

    public ObservableCollection<string> CliOptions { get; }

    internal IConnectionProfileReader? ConnectionProfileReader { get; }

    internal IConnectionCredentialProvider? CredentialProvider { get; }

    public bool RuntimeConnected => false;

    public ProjectRecord? SelectedProjectRecord => SelectedProject?.Record;

    public CliKind SelectedCliKind => WorkspaceDisplay.ParseCli(SelectedCli);

    /// <summary>
    /// A display-only description of the source currently supplied by
    /// Connection Center, or the source pinned by a historical conversation.
    /// It deliberately has no public setter: project and terminal pages must
    /// not drift away from the active Connection Center selection.
    /// </summary>
    public string SelectedConnection => selectedConnection;

    public string? SelectedConnectionProfileId => EffectiveConnectionProfileId;

    public string? EffectiveConnectionProfileId
    {
        get
        {
            if (PendingConversation?.ResumePolicy == ResumePolicy.PinnedConnection)
            {
                return PendingConversation.LastSourceProfileId ?? PendingConversation.SourceProfileIdAtStart;
            }

            return _activeConnectionProfileId;
        }
    }

    internal TerminalProjectRefreshState BeginProjectSnapshotRefresh()
    {
        _isApplyingProjectSnapshot = true;
        return new TerminalProjectRefreshState(
            SelectedProject,
            SelectedCli,
            PendingConversation);
    }

    internal void CompleteProjectSnapshotRefresh(TerminalProjectRefreshState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        try
        {
            if (state.PendingConversation is { } pendingConversation)
            {
                SelectedProject = Projects.FirstOrDefault(candidate =>
                    MatchesConversation(candidate, pendingConversation));
                SelectedCli = WorkspaceDisplay.CliName(pendingConversation.NativeClient);
                PendingConversation = pendingConversation;
                if (pendingConversation.ResumePolicy == ResumePolicy.PinnedConnection)
                {
                    ApplyPinnedConnectionSelection(pendingConversation);
                }
                else
                {
                    ApplyActiveConnectionSelection();
                }
            }
            else
            {
                ProjectCardViewModel? equivalent = FindEquivalentProject(state.SelectedProject);
                SelectedProject = equivalent ?? Projects.FirstOrDefault();
                PendingConversation = null;
                SelectedCli = equivalent is not null && CliOptions.Contains(state.SelectedCli)
                    ? state.SelectedCli
                    : SelectedProject?.PreferredCli ?? "Codex";
                ApplyActiveConnectionSelection();
            }
        }
        finally
        {
            _isApplyingProjectSnapshot = false;
            NotifyEffectiveConnectionChanged();
        }
    }

    internal void ApplyConnections(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileSelection? selection = null,
        ConnectionProfileRouting? routing = null)
    {
        _connectionLabels.Clear();

        foreach (ConnectionProfile connection in connections)
        {
            string label = connection.Name;
            if (_connectionLabels.Values.Contains(label, StringComparer.OrdinalIgnoreCase))
            {
                label = $"{connection.Name} · {connection.Id}";
            }

            _connectionLabels[connection.Id] = label;
        }

        _activeConnectionProfileId = ConnectionSourceResolver.ResolveActiveProfileId(
            connections,
            selection,
            routing);

        if (PendingConversation?.ResumePolicy == ResumePolicy.PinnedConnection)
        {
            ApplyPinnedConnectionSelection(PendingConversation);
        }
        else
        {
            ApplyActiveConnectionSelection();
        }

        EnsureSelectedProject();
    }

    internal void PrepareProject(ProjectCardViewModel project)
    {
        _autoStartRequested = false;
        PendingConversation = null;
        SelectedProject = project;
        SelectedCli = project.PreferredCli;
        ApplyActiveConnectionSelection();
        TerminalNotice = $"已选择项目“{project.Name}”，可开始新的 {SelectedCli} 会话。";
    }

    internal void PrepareResume(ProjectCardViewModel? project, ConversationRecord conversation)
    {
        _autoStartRequested = false;
        PendingConversation = null;
        SelectedProject = project;
        SelectedCli = WorkspaceDisplay.CliName(conversation.NativeClient);
        PendingConversation = conversation;
        if (conversation.ResumePolicy == ResumePolicy.PinnedConnection)
        {
            ApplyPinnedConnectionSelection(conversation);
        }
        else
        {
            ApplyActiveConnectionSelection();
        }

        string title = conversation.Title ?? conversation.NativeSessionId;
        TerminalNotice = project is null
            ? $"已保留会话“{title}”的恢复意图，但原项目路径无效，暂时无法启动。"
            : !project.PathAvailable
                ? $"已保留会话“{title}”的恢复意图，但原项目目录当前不可用。"
                : conversation.ResumePolicy == ResumePolicy.PinnedConnection
                    ? $"已选择会话“{title}”，将按会话绑定来源恢复。"
                    : $"已选择会话“{title}”，启动时将使用官方恢复参数。";
    }

    internal void RequestAutoStart() => _autoStartRequested = true;

    internal bool ConsumeAutoStartRequest()
    {
        bool requested = _autoStartRequested;
        _autoStartRequested = false;
        return requested;
    }

    internal void ReturnToPreviousPage() => _returnToPreviousPage?.Invoke();

    partial void OnSelectedProjectChanged(ProjectCardViewModel? value)
    {
        OnPropertyChanged(nameof(SelectedProjectRecord));
        if (_isApplyingProjectSnapshot)
        {
            return;
        }

        if (PendingConversation is not null && value is not null &&
            !string.Equals(PendingConversation.ProjectId, value.Id, StringComparison.OrdinalIgnoreCase) &&
            !string.Equals(PendingConversation.ProjectId, value.PathFingerprint, StringComparison.OrdinalIgnoreCase) &&
            !PathsEqual(PendingConversation.OriginalWorkingDirectory, value.Path))
        {
            PendingConversation = null;
        }

        if (value is not null && PendingConversation is null)
        {
            SelectedCli = value.PreferredCli;
            ApplyActiveConnectionSelection();
        }
    }

    partial void OnSelectedCliChanged(string value)
    {
        OnPropertyChanged(nameof(SelectedCliKind));
        if (_isApplyingProjectSnapshot)
        {
            return;
        }

        if (PendingConversation is not null && PendingConversation.NativeClient != SelectedCliKind)
        {
            PendingConversation = null;
        }
    }

    partial void OnPendingConversationChanged(ConversationRecord? oldValue, ConversationRecord? newValue)
    {
        if (newValue is null)
        {
            ApplyActiveConnectionSelection();
        }

        NotifyEffectiveConnectionChanged();
    }

    [RelayCommand]
    private void StartTerminal()
    {
        TerminalNotice = SelectedProject is null
            ? "请先选择一个项目。"
            : "终端控件正在接管启动请求。";
    }

    [RelayCommand]
    private void StopTerminal()
    {
        TerminalNotice = "已请求停止当前终端进程。";
    }

    private void EnsureSelectedProject()
    {
        if (SelectedProject is not null && Projects.Contains(SelectedProject))
        {
            return;
        }

        SelectedProject = Projects.FirstOrDefault();
    }

    private ProjectCardViewModel? FindEquivalentProject(ProjectCardViewModel? project)
    {
        if (project is null)
        {
            return null;
        }

        return Projects.FirstOrDefault(candidate =>
            string.Equals(candidate.Id, project.Id, StringComparison.OrdinalIgnoreCase) ||
            string.Equals(
                candidate.PathFingerprint,
                project.PathFingerprint,
                StringComparison.OrdinalIgnoreCase) ||
            PathsEqual(candidate.Path, project.Path));
    }

    private static bool MatchesConversation(
        ProjectCardViewModel project,
        ConversationRecord conversation) =>
        string.Equals(project.Id, conversation.ProjectId, StringComparison.OrdinalIgnoreCase) ||
        string.Equals(
            project.PathFingerprint,
            conversation.ProjectId,
            StringComparison.OrdinalIgnoreCase) ||
        PathsEqual(project.Path, conversation.OriginalWorkingDirectory);

    private void ApplyPinnedConnectionSelection(ConversationRecord conversation)
    {
        if (conversation.ResumePolicy != ResumePolicy.PinnedConnection)
        {
            return;
        }

        string? profileId = conversation.LastSourceProfileId ?? conversation.SourceProfileIdAtStart;
        if (string.IsNullOrWhiteSpace(profileId))
        {
            SetSelectedConnection("会话未记录连接来源");
            return;
        }

        if (_connectionLabels.TryGetValue(profileId, out string? label))
        {
            SetSelectedConnection($"会话绑定 · {label}");
            return;
        }

        label = MissingPinnedConnectionPrefix + profileId;
        SetSelectedConnection(label);
    }

    private void ApplyActiveConnectionSelection()
    {
        if (!string.IsNullOrWhiteSpace(_activeConnectionProfileId) &&
            _connectionLabels.TryGetValue(_activeConnectionProfileId, out string? label))
        {
            SetSelectedConnection($"连接中心当前来源 · {label}");
            return;
        }

        SetSelectedConnection("连接中心尚未选择有效来源");
    }

    private static string? ResolveActiveConnectionId(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileSelection? selection)
    {
        if (selection is null)
        {
            return null;
        }

        // ActiveProfileId is authoritative.  LocalProfileId is only the
        // backward-compatible fallback for documents written before the
        // explicit active-source field existed; it must not silently replace
        // an explicit but now-invalid active source.
        string? candidateId = !string.IsNullOrWhiteSpace(selection.ActiveProfileId)
            ? selection.ActiveProfileId
            : selection.LocalProfileId;
        if (string.IsNullOrWhiteSpace(candidateId))
        {
            return null;
        }

        return connections.Any(connection =>
            string.Equals(connection.Id, candidateId, StringComparison.OrdinalIgnoreCase))
            ? candidateId
            : null;
    }

    private void SetSelectedConnection(string value)
    {
        if (SetProperty(ref selectedConnection, value))
        {
            NotifyEffectiveConnectionChanged();
        }
        else
        {
            // A reloaded source can retain the same display label while its
            // identifier changes, so the effective profile must still notify
            // consumers such as the graphical chat launcher.
            NotifyEffectiveConnectionChanged();
        }
    }

    private void NotifyEffectiveConnectionChanged()
    {
        OnPropertyChanged(nameof(SelectedConnectionProfileId));
        OnPropertyChanged(nameof(EffectiveConnectionProfileId));
    }

    private static bool PathsEqual(string left, string right)
    {
        try
        {
            return string.Equals(
                PathIdentity.Normalize(left),
                PathIdentity.Normalize(right),
                OperatingSystem.IsWindows()
                    ? StringComparison.OrdinalIgnoreCase
                    : StringComparison.Ordinal);
        }
        catch (Exception exception) when (
            exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            return false;
        }
    }
}

public partial class HistoryViewModel : PageViewModel
{
    private readonly Func<Task> _refresh;
    private readonly Action<HistorySessionViewModel> _resume;
    private readonly List<HistorySessionViewModel> _allSessions = new();

    [ObservableProperty]
    private string searchText = string.Empty;

    [ObservableProperty]
    private string selectedCliFilter = "全部 CLI";

    [ObservableProperty]
    private bool isLoading;

    [ObservableProperty]
    private bool hasSessions;

    [ObservableProperty]
    private string statusNotice = "正在等待首次历史扫描。";

    public HistoryViewModel(Func<Task> refresh, Action<HistorySessionViewModel> resume)
        : base("历史会话", "历史按项目归档，与当前 API 和中转连接解耦。")
    {
        _refresh = refresh ?? throw new ArgumentNullException(nameof(refresh));
        _resume = resume ?? throw new ArgumentNullException(nameof(resume));
        CliFilters = new ObservableCollection<string> { "全部 CLI", "Codex", "Claude Code", "Gemini CLI" };
        Sessions = new ObservableCollection<HistorySessionViewModel>();
    }

    public ObservableCollection<string> CliFilters { get; }

    public ObservableCollection<HistorySessionViewModel> Sessions { get; }

    internal void ApplySnapshot(WorkspaceDataSnapshot snapshot)
    {
        var projectNames = snapshot.Projects
            .SelectMany(project => new[]
            {
                new KeyValuePair<string, string>(project.Id, project.DisplayName),
                new KeyValuePair<string, string>(project.PathFingerprint, project.DisplayName),
            })
            .GroupBy(pair => pair.Key, StringComparer.OrdinalIgnoreCase)
            .ToDictionary(group => group.Key, group => group.First().Value, StringComparer.OrdinalIgnoreCase);
        var connectionNames = snapshot.Connections.ToDictionary(
            connection => connection.Id,
            connection => connection.Name,
            StringComparer.OrdinalIgnoreCase);
        var installations = snapshot.CliInstallations.ToDictionary(
            installation => installation.Kind,
            installation => installation);

        _allSessions.Clear();
        foreach (ConversationRecord conversation in snapshot.Conversations)
        {
            projectNames.TryGetValue(conversation.ProjectId, out string? projectName);
            projectName ??= WorkspaceDisplay.PathName(conversation.OriginalWorkingDirectory);

            string connection = conversation.LastSourceProfileId is { Length: > 0 } profileId &&
                                connectionNames.TryGetValue(profileId, out string? sourceName)
                ? sourceName
                : "与当前连接独立";

            installations.TryGetValue(conversation.NativeClient, out CliInstallation? installation);
            string status = conversation.Status switch
            {
                ConversationStatus.SourceMissing => "原来源缺失",
                ConversationStatus.ClientMissing => "客户端缺失",
                ConversationStatus.Archived => "已归档",
                _ when installation?.IsInstalled == false => "CLI 未安装",
                _ => "可继续",
            };

            _allSessions.Add(new HistorySessionViewModel(
                conversation,
                conversation.Title ?? $"{WorkspaceDisplay.CliName(conversation.NativeClient)} 会话",
                projectName,
                WorkspaceDisplay.CliName(conversation.NativeClient),
                connection,
                WorkspaceDisplay.RelativeTime(conversation.UpdatedAt),
                status,
                $"工作目录 · {conversation.OriginalWorkingDirectory}"));
        }

        ApplyFilter();
        StatusNotice = snapshot.Errors.Count == 0
            ? $"已只读索引 {snapshot.Conversations.Count} 条官方会话；正文会在打开时按需读取，不写入工作区数据库。"
            : WorkspaceDisplay.ErrorSummary(snapshot.Errors);
    }

    internal void SetLoadFailure(string message) => StatusNotice = $"历史加载失败：{message}";

    internal IReadOnlyList<HistorySessionViewModel> GetProjectSessions(ProjectCardViewModel project)
    {
        ArgumentNullException.ThrowIfNull(project);
        return _allSessions
            .Where(session => MatchesProject(project, session.Record))
            .OrderByDescending(session => session.Record.UpdatedAt)
            .ToArray();
    }

    partial void OnSearchTextChanged(string value) => ApplyFilter();

    partial void OnSelectedCliFilterChanged(string value) => ApplyFilter();

    [RelayCommand]
    private Task RefreshHistoryAsync() => _refresh();

    [RelayCommand]
    private void Resume(HistorySessionViewModel? session)
    {
        if (session is not null)
        {
            _resume(session);
        }
    }

    private void ApplyFilter()
    {
        string query = SearchText.Trim();
        IEnumerable<HistorySessionViewModel> filtered = _allSessions;

        if (!string.Equals(SelectedCliFilter, "全部 CLI", StringComparison.Ordinal))
        {
            filtered = filtered.Where(session =>
                string.Equals(session.Cli, SelectedCliFilter, StringComparison.Ordinal));
        }

        if (query.Length > 0)
        {
            filtered = filtered.Where(session =>
                session.Title.Contains(query, StringComparison.OrdinalIgnoreCase) ||
                session.Project.Contains(query, StringComparison.OrdinalIgnoreCase) ||
                session.Summary.Contains(query, StringComparison.OrdinalIgnoreCase));
        }

        Sessions.Clear();
        foreach (HistorySessionViewModel session in filtered)
        {
            Sessions.Add(session);
        }

        HasSessions = Sessions.Count > 0;
    }

    private static bool MatchesProject(
        ProjectCardViewModel project,
        ConversationRecord conversation) =>
        string.Equals(project.Id, conversation.ProjectId, StringComparison.OrdinalIgnoreCase) ||
        string.Equals(
            project.PathFingerprint,
            conversation.ProjectId,
            StringComparison.OrdinalIgnoreCase) ||
        PathsEqual(project.Path, conversation.OriginalWorkingDirectory);

    private static bool PathsEqual(string left, string right)
    {
        try
        {
            return string.Equals(
                PathIdentity.Normalize(left),
                PathIdentity.Normalize(right),
                OperatingSystem.IsWindows()
                    ? StringComparison.OrdinalIgnoreCase
                    : StringComparison.Ordinal);
        }
        catch (Exception exception) when (
            exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            return false;
        }
    }
}

public sealed class HistorySessionViewModel
{
    public HistorySessionViewModel(
        ConversationRecord record,
        string title,
        string project,
        string cli,
        string connection,
        string updatedAt,
        string status,
        string summary)
    {
        Record = record;
        Title = title;
        Project = project;
        Cli = cli;
        Connection = connection;
        UpdatedAt = updatedAt;
        Status = status;
        Summary = summary;
    }

    public ConversationRecord Record { get; }

    public string Title { get; }

    public string Project { get; }

    public string Cli { get; }

    public string Connection { get; }

    public string UpdatedAt { get; }

    public string Status { get; }

    public string Summary { get; }
}

public partial class ConnectionsViewModel : PageViewModel
{
    private readonly Func<Task> _refresh;

    [ObservableProperty]
    private bool isLoading;

    [ObservableProperty]
    private bool hasConnections;

    [ObservableProperty]
    private string statusNotice = "正在等待旧连接配置。";

    public ConnectionsViewModel(Func<Task> refresh)
        : base("外部来源与客户端路由", "维护外部来源，并分别决定 Codex、Claude、Gemini 与 Grok 使用哪个来源。")
    {
        _refresh = refresh ?? throw new ArgumentNullException(nameof(refresh));
        Connections = new ObservableCollection<ConnectionCardViewModel>();
        BackupConnections = new ObservableCollection<ConnectionCardViewModel>();
        ExternalSources = new ObservableCollection<ConnectionCardViewModel>();
        Routing = new ConnectionRoutingViewModel();
    }

    public ObservableCollection<ConnectionCardViewModel> Connections { get; }

    /// <summary>
    /// Rows shown in the backup-upstream table. The local machine gateway is a
    /// mandatory fixed entry and is intentionally excluded from this list.
    /// </summary>
    public ObservableCollection<ConnectionCardViewModel> BackupConnections { get; }

    public ObservableCollection<ConnectionCardViewModel> ExternalSources { get; }

    public ConnectionRoutingViewModel Routing { get; }

    internal void ApplySnapshot(WorkspaceDataSnapshot snapshot)
    {
        string? selectedLibrarySourceId = SelectedLibrarySource?.Record.Id ?? ConnectionEditor?.Original?.Id;
        Connections.Clear();
        BackupConnections.Clear();
        ExternalSources.Clear();
        string? activeProfileId = ConnectionSourceResolver.ResolveActiveProfileId(
            snapshot.Connections,
            snapshot.ConnectionSelection,
            snapshot.ConnectionRouting);
        HashSet<string> externalSourceIds = snapshot.Connections
            .Where(profile => profile.Kind == ConnectionProfileKind.Cloud)
            .Select(profile => profile.Id)
            .ToHashSet(StringComparer.OrdinalIgnoreCase);
        IReadOnlyList<string> backupIds = (snapshot.ConnectionRouting?.BackupProfileIds ?? [])
            .Where(externalSourceIds.Contains)
            .ToArray();
        IsBackupUpstreamEnabled = snapshot.ConnectionRouting?.BackupUpstreamEnabled == true;
        foreach (ConnectionProfile profile in snapshot.Connections
                     .OrderBy(profile => profile.Kind)
                     .ThenBy(profile => BackupOrder(profile.Id, backupIds) == 0 ? int.MaxValue : BackupOrder(profile.Id, backupIds))
                     .ThenBy(profile => profile.Name, StringComparer.CurrentCultureIgnoreCase))
        {
            string endpoint = !string.IsNullOrWhiteSpace(profile.BaseUrl)
                ? profile.BaseUrl
                : profile.ClientBaseUrls.Values.FirstOrDefault() ?? "尚未配置地址";
            string clients = profile.EnabledClients.Count == 0
                ? "未配置客户端"
                : string.Join(" · ", profile.EnabledClients.Select(WorkspaceDisplay.CliName));
            bool isFixed = ConnectionProfileIds.IsFixed(profile.Id);
            bool isSupported = isFixed || profile.Kind == ConnectionProfileKind.Cloud;
            bool isSelected = string.Equals(
                profile.Id,
                activeProfileId,
                StringComparison.OrdinalIgnoreCase);
            int backupOrder = BackupOrder(profile.Id, backupIds);
            bool isBackupEnabled = backupOrder > 0;
            string description = profile.Kind switch
            {
                ConnectionProfileKind.Local => "当前电脑上的本机中转来源",
                ConnectionProfileKind.Lan => "局域网中另一台电脑提供的中转来源",
                _ => "可维护的远程中转来源",
            };
            if (!isSupported)
            {
                description = "旧版遗留本地来源。当前版本只保留本机中转和局域网中转两个固定配置。";
                isSelected = false;
            }

            var card = new ConnectionCardViewModel(
                profile,
                description,
                endpoint,
                clients,
                profile.Kind == ConnectionProfileKind.Cloud
                    ? isBackupEnabled ? $"备用第 {backupOrder} 顺位" : "未启用备用"
                    : isFixed ? "固定入口" : "旧版遗留",
                isFixed,
                isSupported,
                isSelected,
                profile.ApiKeyCredentialId is null ? "未检测到凭据" : "已检测到凭据引用",
                profile.Kind switch
                {
                    ConnectionProfileKind.Local => "LocalGateway",
                    ConnectionProfileKind.Lan => "LanGateway",
                    _ => "CloudGateway",
                },
                isBackupEnabled,
                backupOrder,
                backupIds.Count);
            Connections.Add(card);
            if (profile.Kind == ConnectionProfileKind.Cloud)
            {
                BackupConnections.Add(card);
            }
            if (profile.Kind == ConnectionProfileKind.Cloud)
            {
                ExternalSources.Add(card);
            }
        }

        Routing.ApplySnapshot(ExternalSources, snapshot.ConnectionRouting);
        SelectedLibrarySource = ExternalSources.FirstOrDefault(source =>
                                    string.Equals(source.Record.Id, selectedLibrarySourceId, StringComparison.OrdinalIgnoreCase))
                                ?? ExternalSources.FirstOrDefault();
        _ = RefreshLocalDashboardActionAsync();
        _ = RefreshActiveBackupStatusAsync();
        RefreshActiveClientStatus();
        RefreshClaudeGptStateAndSources();
        RefreshCodexClaudeStateAndSources();
        HasConnections = ExternalSources.Count > 0;
        StatusNotice = snapshot.Errors.Count == 0
            ? $"已读取 {ExternalSources.Count} 个外部来源，其中 {backupIds.Count} 个已启用为备用上游。"
            : WorkspaceDisplay.ErrorSummary(snapshot.Errors);
    }

    private static int BackupOrder(string profileId, IReadOnlyList<string> backupIds)
    {
        for (int index = 0; index < backupIds.Count; index++)
        {
            if (string.Equals(profileId, backupIds[index], StringComparison.OrdinalIgnoreCase))
            {
                return index + 1;
            }
        }
        return 0;
    }

    private async Task RefreshActiveBackupStatusAsync()
    {
        if (_switchCoordinator is null)
        {
            return;
        }
        try
        {
            IReadOnlySet<string> activeSourceIds = await _switchCoordinator.GetActiveBackupSourceIdsAsync();
            foreach (ConnectionCardViewModel source in ExternalSources)
            {
                source.SetActiveUsage(activeSourceIds.Contains(source.Record.Id));
            }
        }
        catch (Exception exception) when (
            exception is Sub2ApiSessionException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            // Recent usage is supplemental; source management remains available when it cannot be read.
        }
    }

    internal void SetLoadFailure(string message) => StatusNotice = $"连接加载失败：{message}";

    [RelayCommand]
    private Task RefreshConnectionsAsync() => _refresh();
}

public sealed partial class ConnectionCardViewModel : ObservableObject
{
    public ConnectionCardViewModel(
        ConnectionProfile record,
        string description,
        string endpoint,
        string clients,
        string badge,
        bool isFixed,
        bool isSupported,
        bool isSelected,
        string credentialState,
        string iconKey,
        bool isBackupEnabled = false,
        int backupOrder = 0,
        int backupCount = 0)
    {
        Record = record;
        Description = description;
        Endpoint = endpoint;
        Clients = clients;
        _baseBadge = badge;
        Badge = badge;
        IsFixed = isFixed;
        IsSupported = isSupported;
        IsSelected = isSelected;
        CredentialState = credentialState;
        IconKey = iconKey;
        IsBackupEnabled = isBackupEnabled;
        BackupOrder = backupOrder;
        BackupCount = backupCount;
        if (record.Kind == ConnectionProfileKind.Local &&
            string.Equals(record.Id, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase))
        {
            SetDashboardAction(false, "正在检查本机后台…", "正在检查本机中转服务是否可用。");
        }
        else
        {
            CanOpenDashboard = isSupported;
        }
    }

    public ConnectionProfile Record { get; }

    public string Name => Record.Name;

    public string LibraryDisplayName
    {
        get
        {
            string address = Record.DashboardUrl ?? Endpoint;
            if (Uri.TryCreate(address, UriKind.Absolute, out Uri? uri))
            {
                address = uri.IsDefaultPort ? uri.Host : $"{uri.Host}:{uri.Port}";
            }
            return string.IsNullOrWhiteSpace(address) ? Name : $"{Name} · {address}";
        }
    }

    public string Description { get; }

    public string Endpoint { get; }

    public string Clients { get; }

    public string Status => Clients;

    private readonly string _baseBadge;

    [ObservableProperty]
    private string badge;

    public bool IsFixed { get; }

    public bool IsSupported { get; }

    public bool IsSelected { get; }

    public bool CanRename => IsSupported && !IsFixed;

    public bool CanDelete => IsSupported && !IsFixed;

    public bool CanOperate => IsSupported;

    public bool IsBackupEnabled { get; }

    public int BackupOrder { get; }

    public int BackupCount { get; }

    public bool CanApply => CanOperate && Record.Kind == ConnectionProfileKind.Cloud;

    public bool CanMoveBackupUp => IsBackupEnabled && BackupOrder > 1;

    public bool CanMoveBackupDown => IsBackupEnabled && BackupOrder < BackupCount;

    [ObservableProperty]
    private bool isDropTarget;

    public string ApplyActionLabel => IsBackupEnabled ? "停用备用" : "设为备用";

    public string CredentialState { get; }

    public string IconKey { get; }

    [ObservableProperty]
    private bool canOpenDashboard;

    [ObservableProperty]
    private string dashboardActionLabel = "打开后台";

    [ObservableProperty]
    private string dashboardActionHint = "在系统浏览器中打开这个来源的管理后台。";

    internal void SetDashboardAction(bool canOpen, string label, string hint)
    {
        CanOpenDashboard = canOpen;
        DashboardActionLabel = label;
        DashboardActionHint = hint;
    }

    internal void SetActiveUsage(bool isActive)
    {
        Badge = isActive ? $"正在使用 · {_baseBadge}" : _baseBadge;
    }
}

public partial class SettingsViewModel : PageViewModel
{
    private readonly IDesktopSettingsStore _store;
    private readonly IWindowsStartupRegistrationService _startup;
    private readonly ApplicationUpdateService _updates;
    private readonly AppDataPaths _paths;
    private readonly SemaphoreSlim _saveGate = new(1, 1);
    private WorkspaceDesktopSettings _settings = new();
    private bool _isApplying;

    [ObservableProperty]
    private bool startWithWindows;

    [ObservableProperty]
    private bool minimizeToTray = true;

    [ObservableProperty]
    private bool preserveSessionIndex = true;

    [ObservableProperty]
    private bool collectAnonymousDiagnostics;

    [ObservableProperty]
    private bool checkUpdatesAutomatically = true;

    [ObservableProperty]
    private bool isCheckingUpdates;

    public bool HasUpdateSource => !string.IsNullOrWhiteSpace(_settings.UpdateManifestUrl);

    public bool CanCheckUpdates => HasUpdateSource && !IsCheckingUpdates;

    [ObservableProperty]
    private string settingsStatus = "正在读取设置…";

    [ObservableProperty]
    private string updateStatus = "尚未检查更新";

    public SettingsViewModel(
        IDesktopSettingsStore store,
        IWindowsStartupRegistrationService startup,
        ApplicationUpdateService updates,
        AppDataPaths paths)
        : base("设置", "控制工作台行为、历史索引与本地隐私选项。")
    {
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _startup = startup ?? throw new ArgumentNullException(nameof(startup));
        _updates = updates ?? throw new ArgumentNullException(nameof(updates));
        _paths = paths ?? throw new ArgumentNullException(nameof(paths));
    }

    public event EventHandler? SettingsChanged;

    public WorkspaceDesktopSettings CurrentSettings => _settings;

    internal async Task SetNetworkProbeIntervalAsync(
        int minutes,
        CancellationToken cancellationToken = default)
    {
        int normalized = MainWindowViewModel.NormalizeNetworkProbeInterval(minutes);
        try
        {
            await _saveGate.WaitAsync(cancellationToken).ConfigureAwait(true);
            try
            {
                _settings = _settings with { NetworkProbeIntervalMinutes = normalized };
                await _store.SaveAsync(_settings, cancellationToken).ConfigureAwait(true);
                SettingsStatus = $"网络采样间隔已设为 {normalized} 分钟";
                SettingsChanged?.Invoke(this, EventArgs.Empty);
            }
            finally
            {
                _saveGate.Release();
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            SettingsStatus = $"采样间隔保存失败：{SanitizeSettingMessage(exception.Message)}";
        }
    }

    public async Task InitializeAsync(CancellationToken cancellationToken = default)
    {
        _isApplying = true;
        try
        {
            _settings = await _store.LoadAsync(cancellationToken).ConfigureAwait(true);
#if PUBLIC_RELEASE
            _settings = _settings with
            {
                CheckUpdatesAutomatically = false,
                UpdateManifestUrl = string.Empty,
            };
#endif
            bool startupMatchesCurrentExecutable = _startup.IsEnabled();
            StartWithWindows = startupMatchesCurrentExecutable || _settings.StartWithWindows;
            if (StartWithWindows && !startupMatchesCurrentExecutable)
            {
                _startup.SetEnabled(true);
            }
            MinimizeToTray = _settings.MinimizeToTray;
            PreserveSessionIndex = _settings.PreserveSessionIndex;
            CollectAnonymousDiagnostics = _settings.CollectAnonymousDiagnostics;
            CheckUpdatesAutomatically = HasUpdateSource && _settings.CheckUpdatesAutomatically;
            _settings = CreateSettings();
            SettingsStatus = "设置已载入";
            UpdateStatus = HasUpdateSource ? "尚未检查更新" : "公开版未配置更新源";
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidDataException or InvalidOperationException)
        {
            SettingsStatus = $"设置读取失败：{SanitizeSettingMessage(exception.Message)}";
        }
        finally
        {
            _isApplying = false;
        }

        if (HasUpdateSource && CheckUpdatesAutomatically)
        {
            _ = CheckUpdatesAsync();
        }
    }

    partial void OnStartWithWindowsChanged(bool value) => QueueSave(updateStartup: true);

    partial void OnMinimizeToTrayChanged(bool value) => QueueSave();

    partial void OnPreserveSessionIndexChanged(bool value) => QueueSave();

    partial void OnCollectAnonymousDiagnosticsChanged(bool value) => QueueSave();

    partial void OnCheckUpdatesAutomaticallyChanged(bool value) => QueueSave();

    partial void OnIsCheckingUpdatesChanged(bool value) => OnPropertyChanged(nameof(CanCheckUpdates));

    private async void QueueSave(bool updateStartup = false)
    {
        if (_isApplying) return;
        await _saveGate.WaitAsync().ConfigureAwait(true);
        try
        {
            if (updateStartup)
            {
                _startup.SetEnabled(StartWithWindows);
            }

            _settings = CreateSettings();
            await _store.SaveAsync(_settings).ConfigureAwait(true);
            SettingsStatus = "设置已保存";
            SettingsChanged?.Invoke(this, EventArgs.Empty);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            SettingsStatus = $"设置保存失败：{SanitizeSettingMessage(exception.Message)}";
        }
        finally
        {
            _saveGate.Release();
        }
    }

    [RelayCommand]
    private async Task CheckUpdatesAsync()
    {
        if (IsCheckingUpdates) return;
        IsCheckingUpdates = true;
        try
        {
            AppUpdateCheckResult result = await _updates.CheckAsync(_settings.UpdateManifestUrl).ConfigureAwait(true);
            UpdateStatus = result.Message;
            if (result.HasUpdate && result.Manifest is not null)
            {
                UpdateStatus = $"{result.Message}，正在下载并校验…";
                string package = await _updates.DownloadVerifiedAsync(result.Manifest).ConfigureAwait(true);
                UpdateStatus = $"新版本已安全下载：{Path.GetFileName(package)}";
            }
        }
        catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException or IOException or InvalidDataException or InvalidOperationException)
        {
            UpdateStatus = $"更新检查失败：{SanitizeSettingMessage(exception.Message)}";
        }
        finally
        {
            IsCheckingUpdates = false;
        }
    }

    [RelayCommand]
    private void OpenDataDirectory()
    {
        Directory.CreateDirectory(_paths.AppDataRoot);
        Process.Start(new ProcessStartInfo("explorer.exe", _paths.AppDataRoot) { UseShellExecute = true });
    }

    private WorkspaceDesktopSettings CreateSettings() => _settings with
    {
        StartWithWindows = StartWithWindows,
        MinimizeToTray = MinimizeToTray,
        PreserveSessionIndex = PreserveSessionIndex,
        CollectAnonymousDiagnostics = CollectAnonymousDiagnostics,
        CheckUpdatesAutomatically = CheckUpdatesAutomatically,
    };

    private static string SanitizeSettingMessage(string message) =>
        message.Replace("\r", " ", StringComparison.Ordinal).Replace("\n", " ", StringComparison.Ordinal).Trim();
}

internal static class WorkspaceDisplay
{
    public static string CliName(CliKind cli) => cli switch
    {
        CliKind.Codex => "Codex",
        CliKind.ClaudeCode => "Claude Code",
        CliKind.GeminiCli => "Gemini CLI",
        _ => cli.ToString(),
    };

    public static CliKind ParseCli(string displayName) => displayName switch
    {
        "Claude Code" => CliKind.ClaudeCode,
        "Gemini CLI" => CliKind.GeminiCli,
        _ => CliKind.Codex,
    };

    public static string RelativeTime(DateTimeOffset? timestamp)
    {
        if (timestamp is null || timestamp == default)
        {
            return "尚无活动";
        }

        DateTimeOffset local = timestamp.Value.ToLocalTime();
        TimeSpan age = DateTimeOffset.Now - local;
        if (age < TimeSpan.FromMinutes(1))
        {
            return "刚刚";
        }

        if (age < TimeSpan.FromHours(1))
        {
            return $"{Math.Max(1, (int)age.TotalMinutes)} 分钟前";
        }

        if (age < TimeSpan.FromDays(1) && local.Date == DateTime.Today)
        {
            return $"今天 {local:HH:mm}";
        }

        if (local.Date == DateTime.Today.AddDays(-1))
        {
            return $"昨天 {local:HH:mm}";
        }

        return local.Year == DateTime.Today.Year
            ? local.ToString("M 月 d 日 HH:mm", System.Globalization.CultureInfo.CurrentCulture)
            : local.ToString("yyyy 年 M 月 d 日", System.Globalization.CultureInfo.CurrentCulture);
    }

    public static string Monogram(string name)
    {
        if (string.IsNullOrWhiteSpace(name))
        {
            return "AI";
        }

        string[] parts = name.Split(
            [' ', '-', '_', '.'],
            StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);
        if (parts.Length > 1)
        {
            return string.Concat(parts.Take(2).Select(part => char.ToUpperInvariant(part[0])));
        }

        string compact = parts.FirstOrDefault() ?? name.Trim();
        return compact.Length <= 2 ? compact.ToUpperInvariant() : compact[..2].ToUpperInvariant();
    }

    public static string PathName(string path)
    {
        try
        {
            string trimmed = Path.TrimEndingDirectorySeparator(path);
            string name = Path.GetFileName(trimmed);
            return string.IsNullOrWhiteSpace(name) ? trimmed : name;
        }
        catch (Exception exception) when (exception is ArgumentException or NotSupportedException)
        {
            return "未归类项目";
        }
    }

    public static string ErrorSummary(IReadOnlyList<WorkspaceLoadError> errors)
        => errors.Count == 0
            ? string.Empty
            : string.Join("；", errors.Select(error => $"{error.Source}：{error.Message}"));
}
