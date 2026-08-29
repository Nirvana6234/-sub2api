global using System.IO;
global using System.Net.Http;

using System.Collections.ObjectModel;
using System.Diagnostics;
using System.Text.RegularExpressions;
using AiSwitchGui;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;
using Microsoft.Win32;

namespace LanAi.Workspace.Wpf.ViewModels;

/// <summary>
/// Presents the existing LocalGatewayService as a non-concurrent, observable
/// control surface. The view model never changes Sub2API configuration; it only
/// invokes the control operations already owned by the legacy service.
/// </summary>
public partial class GatewayViewModel : PageViewModel
{
    private const int MaximumLogLines = 240;
    private const int MaximumOutputLength = 12_000;
    private static readonly TimeSpan AutomaticRefreshLifetime = TimeSpan.FromMinutes(10);

    private readonly ILocalGatewayController _controller;
    private readonly ISub2ApiSessionManager? _sessionManager;
    private readonly ILocalGatewayEndpointResolver? _localGatewayEndpointResolver;
    private readonly IEndpointProbeService? _endpointProbeService;
    private readonly ISub2ApiServiceSummaryClient? _serviceSummaryClient;
    private readonly ILocalGatewayStatsProbe? _backendProbe;
    private readonly TimeProvider _timeProvider;
    private readonly bool _localControlCenterMode;
    private readonly SemaphoreSlim _operationGate = new(1, 1);
    private readonly SemaphoreSlim _summaryGate = new(1, 1);
    private readonly List<string> _logLines = [];
    // The browser destination comes only from the explicit connection-center
    // dashboard address. Runtime probes never become a LAN browser target.
    private string? _currentLanDashboardUrl;
    private bool _connectionConfigurationKnown;
    private bool _lanProfileFound;
    private IReadOnlyList<ConnectionProfile> _connections = Array.Empty<ConnectionProfile>();
    private ConnectionProfileRouting? _connectionRouting;
    private ConnectionProfileSelection? _connectionSelection;
    private bool _hasExplicitBackendSelection;
    private string? _selectedBackendDisplayName;
    private string? _backendSelectionIssue;
    private IReadOnlyList<Sub2ApiEndpointTarget> _backendCandidates = Array.Empty<Sub2ApiEndpointTarget>();
    private Sub2ApiEndpointTarget? _activeBackendTarget;
    private DateTimeOffset? _lastStatusRefreshAt;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(PrimaryGatewayActionLabel))]
    private bool isBusy;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(PrimaryGatewayActionLabel))]
    private string currentOperation = "空闲";

    [ObservableProperty]
    private string modeLabel = "正在识别";

    [ObservableProperty]
    private string modeDescription = "等待读取本地中转控制方式";

    [ObservableProperty]
    private string gatewaySummary = "等待刷新";

    [ObservableProperty]
    private string webUrl = LocalGatewayService.DockerWebUrl;

    [ObservableProperty]
    private string lanDashboardUrl = "未发现局域网后台地址";

    [ObservableProperty]
    private string lanDashboardStatusLabel = "等待网络状态";

    [ObservableProperty]
    private string webStatusLabel = "尚未检测";

    [ObservableProperty]
    private string runtimeStatusLabel = "等待服务状态";

    [ObservableProperty]
    private string lastRefreshed = "尚未刷新";

    [ObservableProperty]
    private string cacheStatusLabel = "尚未读取";

    [ObservableProperty]
    private string statusNotice = "页面打开后会自动读取本机中转状态。";

    [ObservableProperty]
    private string automaticRecoveryLabel = "后台监测准备中";

    [ObservableProperty]
    private bool hasAutomaticRecoveryFailure;

    [ObservableProperty]
    private string operationLog = "等待操作…";

    [ObservableProperty]
    private string nativeRootPath = "尚未设置";

    public string NativeRootDisplayPath => HideBackendBrand(NativeRootPath);

    partial void OnNativeRootPathChanged(string value) =>
        OnPropertyChanged(nameof(NativeRootDisplayPath));

    [ObservableProperty]
    private bool controlAvailable;

    [ObservableProperty]
    private bool isHealthy;

    [ObservableProperty]
    private bool hasFailure;

    [ObservableProperty]
    private bool nativeMode;

    [ObservableProperty]
    private bool dockerInstalled;

    [ObservableProperty]
    private bool dockerAvailable;

    [ObservableProperty]
    private bool hasServices;

    [ObservableProperty]
    private string loginEmail = string.Empty;

    [ObservableProperty]
    private bool isLoginEditorOpen;

    [ObservableProperty]
    private bool isAuthenticating;

    [ObservableProperty]
    private string loginRoleLabel = "未登录";

    [ObservableProperty]
    private string loginStatus = "登录后可查看个人余额、额度和服务摘要。";

    [ObservableProperty]
    private string backendSourceLabel = "等待连接中心同步数据后台";

    [ObservableProperty]
    private string accountBalanceLabel = "—";

    [ObservableProperty]
    private bool isProbingConnections;

    [ObservableProperty]
    private string connectionHealthStatus = "尚未检测当前三条客户端连接。";

    [ObservableProperty]
    private bool isLoadingServiceSummary;

    [ObservableProperty]
    private string todayUsageLabel = "—";

    [ObservableProperty]
    private string todayRequestLabel = "—";

    [ObservableProperty]
    private string todayTokenLabel = "—";

    [ObservableProperty]
    private string todayActualCostLabel = "—";

    [ObservableProperty]
    private string apiKeyStatusLabel = "—";

    [ObservableProperty]
    private string platformQuotaLabel = "—";

    [ObservableProperty]
    private string recentFailureLabel = "—";

    [ObservableProperty]
    private string userServiceStatusLabel = "登录后自动读取今天的使用状态。";

    [ObservableProperty]
    private bool hasTodayFailures;

    [ObservableProperty]
    private bool hasQuotaWarning;

    [ObservableProperty]
    private string adminTrafficLabel = "—";

    [ObservableProperty]
    private string adminLatencyLabel = "—";

    [ObservableProperty]
    private string adminConcurrencyLabel = "—";

    [ObservableProperty]
    private string adminAccountHealthLabel = "—";

    [ObservableProperty]
    private string serviceVersionLabel = "—";

    [ObservableProperty]
    private string serviceLogHealthLabel = "—";

    [ObservableProperty]
    private string administratorHealthHeadline = "全站状态尚未读取";

    [ObservableProperty]
    private string administratorHealthDetail = "—";

    [ObservableProperty]
    private bool hasAdministratorAttention;

    [ObservableProperty]
    private bool hasServiceUpdate;

    [ObservableProperty]
    private string serviceUpdateLabel = string.Empty;

    public GatewayViewModel()
        : this(new LocalGatewayController())
    {
    }

    internal GatewayViewModel(ILocalGatewayController controller)
        : this(
            controller,
            sessionManager: null,
            localGatewayEndpointResolver: null,
            endpointProbeService: null,
            serviceSummaryClient: null)
    {
    }

    internal GatewayViewModel(
        ILocalGatewayController controller,
        ISub2ApiSessionManager? sessionManager,
        ILocalGatewayEndpointResolver? localGatewayEndpointResolver,
        IEndpointProbeService? endpointProbeService = null,
        ISub2ApiServiceSummaryClient? serviceSummaryClient = null,
        ILocalGatewayStatsProbe? backendProbe = null,
        TimeProvider? timeProvider = null,
        bool localControlCenterMode = false)
        : base("中转服务", "在这台电脑上启动、检查并维护本机中转服务。")
    {
        _controller = controller ?? throw new ArgumentNullException(nameof(controller));
        _sessionManager = sessionManager;
        _localGatewayEndpointResolver = localGatewayEndpointResolver;
        _endpointProbeService = endpointProbeService;
        _serviceSummaryClient = serviceSummaryClient;
        _backendProbe = backendProbe;
        _timeProvider = timeProvider ?? TimeProvider.System;
        _localControlCenterMode = localControlCenterMode;
        if (_sessionManager is not null)
        {
            _sessionManager.SessionChanged += OnSessionChanged;
            ApplySessionState(_sessionManager.Current);
        }

        Services = new ObservableCollection<GatewayServiceRowViewModel>();
        ConnectionHealth = new ObservableCollection<EndpointHealthRowViewModel>();
        CliReadiness = new ObservableCollection<CliReadinessRowViewModel>();
        ApplyStatus(_controller.GetStartupStatus(), updateNotice: false);
    }

    public ObservableCollection<GatewayServiceRowViewModel> Services { get; }

    public ObservableCollection<EndpointHealthRowViewModel> ConnectionHealth { get; }

    public ObservableCollection<CliReadinessRowViewModel> CliReadiness { get; }

    public bool HasNoServices => !HasServices;

    public bool IsSignedIn => _sessionManager?.Current.IsAuthenticated == true;

    public bool ShowLoginEditor => _sessionManager is not null && IsLoginEditorOpen && !IsSignedIn;

    public bool ShowSignedInSummary => _sessionManager is not null && IsSignedIn;

    public bool ShowSignedOutSummary => _sessionManager is not null && !IsSignedIn;

    public bool ShowLoginPrompt => ShowSignedOutSummary && !IsLoginEditorOpen;

    public bool ShowAdministratorSummary => _sessionManager?.Current.IsAdministrator == true;

    public bool ShowLocalServiceControls => _activeBackendTarget is { IsLocalMachine: true } ||
                                            _activeBackendTarget is null && !_hasExplicitBackendSelection;

    public int ServiceSummaryColumnSpan => ShowLocalServiceControls ? 1 : 3;

    public bool IsWarning => !IsHealthy && !HasFailure;

    public string ModeBadge => NativeMode ? "WINDOWS NATIVE" : "DOCKER COMPOSE";

    public string DockerStatus => NativeMode
        ? "原生服务不依赖 Docker"
        : DockerAvailable
            ? "Docker 已连接"
            : DockerInstalled
                ? "Docker 已安装但未运行"
                : "未检测到 Docker";

    public string GatewayUsageHint => _hasExplicitBackendSelection && _activeBackendTarget is null
        ? _backendSelectionIssue ?? "连接中心当前来源暂时不能用于账户登录。"
        : _activeBackendTarget is { IsLocalMachine: false } selected
            ? selected.DashboardUri is not null
                ? $"当前按照连接中心选择使用“{selected.DisplayName}”，可以打开该来源后台。"
                : $"当前按照连接中心选择使用“{selected.DisplayName}”；该来源未配置管理后台入口。"
        : IsHealthy
            ? "本机服务正在运行，本机和已配置的局域网设备都可以使用。"
            : _activeBackendTarget is not null
                ? $"当前使用“{_activeBackendTarget.DisplayName}”读取后台数据。"
                : ControlAvailable
                    ? "可以启动本机服务，也可以在连接中心选择局域网或云端后台。"
                    : "请在连接中心配置可访问的局域网或云端后台。";

    /// <summary>
    /// One primary action with a stable place in the UI.  It starts the gateway
    /// when needed, but never pretends a healthy gateway needs starting again.
    /// </summary>
    public string PrimaryGatewayActionLabel => IsBusy
        ? $"{CurrentOperation}中…"
        : _hasExplicitBackendSelection && _activeBackendTarget is null
            ? "当前来源不可用"
        : _activeBackendTarget is { IsLocalMachine: false } selected
            ? selected.DashboardUri is not null
                ? $"打开{selected.DisplayName}后台"
                : "当前来源无后台入口"
            : IsHealthy
                ? "打开本机后台"
                : "启动并打开本机后台";

    /// <summary>
    /// Accepts the same public connection snapshot shown by Connection Center.
    /// The selected admin URL is intentionally derived from the fixed
    /// lan-default profile only; secrets are not read or retained here.
    /// </summary>
    internal void ApplyConnections(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileSelection? selection = null,
        ConnectionProfileRouting? routing = null)
    {
        ArgumentNullException.ThrowIfNull(connections);

        _connectionConfigurationKnown = true;
        _connections = connections.ToArray();
        _connectionSelection = selection;
        _connectionRouting = routing;
        ConnectionProfile? selectedProfile = _localControlCenterMode
            ? connections.FirstOrDefault(profile =>
                string.Equals(profile.Id, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase))
            : ConnectionSourceResolver.FindActiveProfile(connections, selection, routing);
        _selectedBackendDisplayName = selectedProfile?.Name;
        _backendSelectionIssue = selectedProfile is null
            ? null
            : Sub2ApiEndpointSelector.DescribeUnavailableSelectedSource(selectedProfile);
        _hasExplicitBackendSelection = _localControlCenterMode
            ? selectedProfile is not null
            : !string.IsNullOrWhiteSpace(ConnectionSourceResolver.ResolveRequestedProfileId(selection, routing));
        _backendCandidates = _localControlCenterMode
            ? selectedProfile is not null && Sub2ApiEndpointSelector.TryCreate(selectedProfile, out Sub2ApiEndpointTarget? localTarget)
                ? [localTarget!]
                : []
            : Sub2ApiEndpointSelector.GetCandidates(connections, selection, routing);
        _activeBackendTarget = null;
        BackendSourceLabel = _backendCandidates.Count > 0
            ? FormatBackendSourceLabel("候选后台", _backendCandidates[0])
            : _hasExplicitBackendSelection
                ? _backendSelectionIssue ?? "连接中心当前来源没有可用的后台地址"
                : "连接中心尚未配置可用的数据后台";
        ConnectionProfile? lanProfile = connections.FirstOrDefault(profile =>
            string.Equals(profile.Id, ConnectionProfileIds.LanDefault, StringComparison.OrdinalIgnoreCase));
        _lanProfileFound = lanProfile is not null;

        _currentLanDashboardUrl = lanProfile is not null &&
                                  TryCreateDashboardUrlFromConnection(lanProfile, out string dashboardUrl)
            ? dashboardUrl
            : null;

        UpdateLanDashboardPresentation();
        _ = RefreshBackendSessionAfterConnectionChangeAsync();
    }

    internal void ApplyCliInstallations(IReadOnlyList<CliInstallation> installations)
    {
        ArgumentNullException.ThrowIfNull(installations);
        CliReadiness.Clear();
        foreach (CliInstallation installation in installations.OrderBy(item => item.Kind))
        {
            CliReadiness.Add(new CliReadinessRowViewModel(installation));
        }
    }

    /// <summary>
    /// Page activation never performs network I/O. It only reports whether the
    /// in-memory snapshot is still fresh; the user explicitly decides when to refresh.
    /// </summary>
    public Task InitializeAsync()
    {
        DateTimeOffset now = _timeProvider.GetUtcNow();
        CacheStatusLabel = _lastStatusRefreshAt switch
        {
            null => "尚未读取 · 点击刷新",
            { } lastRefresh when now - lastRefresh < AutomaticRefreshLifetime =>
                $"使用 10 分钟缓存 · {lastRefresh.ToLocalTime():HH:mm:ss}",
            _ => "缓存已超过 10 分钟 · 点击刷新",
        };
        return Task.CompletedTask;
    }

    [RelayCommand(CanExecute = nameof(CanRefresh))]
    private async Task RefreshStatusAsync()
    {
        if (!await _operationGate.WaitAsync(0).ConfigureAwait(true))
        {
            return;
        }

        BeginOperation("刷新状态");
        try
        {
            Sub2ApiEndpointTarget? selectedBackend = _backendCandidates.FirstOrDefault();
            Sub2ApiEndpointTarget? remoteBackend = _hasExplicitBackendSelection &&
                                                   selectedBackend is { IsLocalMachine: false }
                ? selectedBackend
                : null;
            if (remoteBackend is not null)
            {
                AppendLog($"开始检查当前来源“{remoteBackend.DisplayName}”。", "检查");
            }
            else
            {
                AppendLog("开始读取本机服务、端口与网页状态。", "检查");
                LocalGatewayStatus status = await _controller
                    .GetStatusAsync(CancellationToken.None)
                    .ConfigureAwait(true);
                ApplyStatus(status, updateNotice: true);
                AppendLog($"状态刷新完成：{status.Summary}", status.WebReachable ? "正常" : "状态");
            }

            await ResolveAvailableBackendAsync().ConfigureAwait(true);
            if (remoteBackend is not null)
            {
                AppendLog($"当前来源检查完成：{remoteBackend.DisplayName}", "正常");
            }
            if (_activeBackendTarget is not null)
            {
                await RestoreSharedSessionAsync().ConfigureAwait(true);
            }

            if (IsSignedIn)
            {
                await RefreshServiceSummaryAsync().ConfigureAwait(true);
            }

            MarkStatusRefreshed();
        }
        catch (Exception exception)
        {
            SetFailure($"刷新状态失败：{exception.Message}");
            AppendLog(exception.Message, "失败");
        }
        finally
        {
            EndOperation();
            _operationGate.Release();
        }
    }

    [RelayCommand(CanExecute = nameof(CanControl))]
    private Task StartGatewayAsync() => RunCommandOperationAsync(
        "启动中转",
        _controller.StartAsync,
        waitForWeb: true,
        waitTimeout: TimeSpan.FromSeconds(90));

    [RelayCommand(CanExecute = nameof(CanStartAndOpenDashboard))]
    private Task StartAndOpenDashboardAsync() => RunCommandOperationAsync(
        "启动并打开本机后台",
        _controller.StartAsync,
        waitForWeb: true,
        waitTimeout: TimeSpan.FromSeconds(90),
        openDashboardWhenReady: true);

    [RelayCommand]
    private async Task SelectNativeRootAsync()
    {
        if (IsBusy)
        {
            return;
        }

        var dialog = new OpenFolderDialog
        {
            Title = "选择本机中转工作区",
            Multiselect = false,
        };
        if (Directory.Exists(NativeRootPath))
        {
            dialog.InitialDirectory = NativeRootPath;
        }
        if (dialog.ShowDialog() != true)
        {
            return;
        }

        await _operationGate.WaitAsync().ConfigureAwait(true);
        try
        {
            BeginOperation("设置本机中转目录");
            CommandResult result = await _controller
                .ConfigureNativeRootAsync(dialog.FolderName, CancellationToken.None)
                .ConfigureAwait(true);
            AppendCommandOutput(result);
            if (!result.Success)
            {
                SetFailure($"设置本机中转目录失败：{RedactSensitiveValues(result.CombinedOutput)}");
                return;
            }

            LocalGatewayStatus status = await _controller
                .GetStatusAsync(CancellationToken.None)
                .ConfigureAwait(true);
            ApplyStatus(status, updateNotice: false);
            StatusNotice = $"本机中转目录已设置为 {HideBackendBrand(status.NativeRoot)}。现在可以直接启动服务。";
            HasFailure = false;
            MarkStatusRefreshed();
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            SetFailure($"设置本机中转目录失败：{exception.Message}");
            AppendLog(exception.Message, "失败");
        }
        finally
        {
            EndOperation();
            _operationGate.Release();
        }
    }

    [RelayCommand(CanExecute = nameof(CanPrimaryGatewayAction))]
    private Task PrimaryGatewayActionAsync()
        => _activeBackendTarget is { IsLocalMachine: false, DashboardUri: not null }
            ? OpenActiveBackendDashboardAsync()
            : IsHealthy
                ? OpenDashboardAsync()
                : StartAndOpenDashboardAsync();

    private Task OpenActiveBackendDashboardAsync()
    {
        if (_activeBackendTarget?.DashboardUri is not { } dashboardUri)
        {
            return Task.CompletedTask;
        }

        return OpenDashboardUrlAsync(
            dashboardUri.AbsoluteUri,
            $"{_activeBackendTarget.DisplayName}后台");
    }

    [RelayCommand(CanExecute = nameof(CanControl))]
    private Task StopGatewayAsync() => RunCommandOperationAsync(
        "停止中转",
        _controller.StopAsync,
        waitForWeb: false,
        waitTimeout: TimeSpan.Zero);

    [RelayCommand(CanExecute = nameof(CanControl))]
    private Task RestartGatewayAsync() => RunCommandOperationAsync(
        "重启中转",
        _controller.RestartAsync,
        waitForWeb: true,
        waitTimeout: TimeSpan.FromSeconds(90));

    [RelayCommand(CanExecute = nameof(CanOpenDashboard))]
    private async Task OpenDashboardAsync()
    {
        if (!TryNormalizeHttpUrl(WebUrl, out string dashboardUrl))
        {
            SetFailure("本机后台地址无效，无法交给系统浏览器打开。", preserveHealth: true);
            return;
        }

        await OpenDashboardUrlAsync(dashboardUrl, "本机后台").ConfigureAwait(true);
    }

    [RelayCommand(CanExecute = nameof(CanOpenLanDashboard))]
    private async Task OpenLanDashboardAsync()
    {
        // The private value is refreshed only from the lan-default connection
        // snapshot.  The visible runtime-status address is deliberately not a
        // browser input, so a Docker/WSL 172.x probe cannot become the target.
        if (!TryNormalizeHttpUrl(_currentLanDashboardUrl, out string dashboardUrl))
        {
            SetFailure("未检测到有效的局域网后台地址，请先在连接中心配置“局域网中转”。", preserveHealth: true);
            return;
        }

        await OpenDashboardUrlAsync(dashboardUrl, "局域网后台").ConfigureAwait(true);
    }

    [RelayCommand]
    private void ClearLog()
    {
        _logLines.Clear();
        OperationLog = "操作日志已清空。";
    }

    [RelayCommand(CanExecute = nameof(CanBeginLogin))]
    private void BeginLogin()
    {
        IsLoginEditorOpen = true;
        LoginStatus = "请输入后台管理账户。密码仅用于本次登录，不会保存。";
    }

    [RelayCommand]
    private void CancelLogin()
    {
        IsLoginEditorOpen = false;
        LoginStatus = "登录后可查看个人余额、额度和服务摘要。";
    }

    [RelayCommand(CanExecute = nameof(CanLogout))]
    private async Task LogoutAsync()
    {
        if (_sessionManager is null)
        {
            return;
        }

        IsAuthenticating = true;
        try
        {
            await _sessionManager.LogoutAsync(CancellationToken.None).ConfigureAwait(true);
            LoginEmail = string.Empty;
            IsLoginEditorOpen = false;
        }
        finally
        {
            IsAuthenticating = false;
        }
    }

    [RelayCommand(CanExecute = nameof(CanProbeConnections))]
    private async Task ProbeConnectionsAsync()
    {
        if (_endpointProbeService is null)
        {
            ConnectionHealthStatus = "当前页面没有可用的连接检测服务。";
            return;
        }

        IsProbingConnections = true;
        ConnectionHealthStatus = "正在并行检测 Codex、Claude、Gemini 和 Grok…";
        try
        {
            IReadOnlyList<EndpointHealthResult> results = await _endpointProbeService
                .ProbeAllAsync(_connections, _connectionRouting, _connectionSelection, CancellationToken.None)
                .ConfigureAwait(true);
            ConnectionHealth.Clear();
            foreach (EndpointHealthResult result in results)
            {
                ConnectionHealth.Add(new EndpointHealthRowViewModel(result));
            }

            int healthy = results.Count(result => result.Succeeded);
            ConnectionHealthStatus = $"{healthy}/{results.Count} 条连接正常 · 更新于 {DateTime.Now:HH:mm:ss}";
        }
        catch (OperationCanceledException)
        {
            ConnectionHealthStatus = "连接检测已取消。";
        }
        catch
        {
            ConnectionHealthStatus = "连接检测暂时失败；未保存地址、密钥或响应正文。";
        }
        finally
        {
            IsProbingConnections = false;
        }
    }

    [RelayCommand(CanExecute = nameof(CanRefreshServiceSummary))]
    private async Task RefreshServiceSummaryAsync()
    {
        if (_sessionManager is null || _serviceSummaryClient is null ||
            !await _summaryGate.WaitAsync(0).ConfigureAwait(true))
        {
            return;
        }

        IsLoadingServiceSummary = true;
        UserServiceStatusLabel = "正在读取当前账户的服务摘要…";
        try
        {
            await ResolveAvailableBackendAsync().ConfigureAwait(true);
            if (_activeBackendTarget is null)
            {
                LoginStatus = "没有可访问的数据后台，服务摘要暂不可用。";
                return;
            }

            Sub2ApiSessionAccess access = await _sessionManager
                .GetAccessAsync(_activeBackendTarget.ApiBaseUri, CancellationToken.None)
                .ConfigureAwait(true);
            Sub2ApiServiceSummary summary = await _serviceSummaryClient
                .LoadAsync(access, CancellationToken.None)
                .ConfigureAwait(true);
            ApplyServiceSummary(summary);
            LoginStatus = "服务摘要已更新。";
        }
        catch (Sub2ApiSessionException exception)
        {
            LoginStatus = DescribeSessionFailure(exception.Failure);
            UserServiceStatusLabel = LoginStatus;
        }
        catch
        {
            LoginStatus = "服务摘要暂时无法读取；本机服务控制不受影响。";
            UserServiceStatusLabel = LoginStatus;
        }
        finally
        {
            IsLoadingServiceSummary = false;
            _summaryGate.Release();
        }
    }

    public async Task<bool> LoginLocalAccountAsync(string? submittedPassword)
    {
        if (IsAuthenticating)
        {
            return false;
        }

        if (_sessionManager is null)
        {
            LoginStatus = "当前页面没有可用的账户登录服务。";
            return false;
        }

        string email = LoginEmail.Trim();
        string password = submittedPassword ?? string.Empty;
        if (string.IsNullOrWhiteSpace(email) || string.IsNullOrWhiteSpace(password))
        {
            LoginStatus = "请输入账户邮箱和密码。";
            return false;
        }

        IsAuthenticating = true;
        LoginStatus = "正在登录并建立安全会话…";
        try
        {
            await ResolveAvailableBackendAsync().ConfigureAwait(true);
            if (_activeBackendTarget is null)
            {
                LoginStatus = _backendSelectionIssue ?? "连接中心没有可访问的后台，请检查当前局域网或云端地址。";
                return false;
            }

            await _sessionManager
                .LoginAsync(
                    _activeBackendTarget.ApiBaseUri,
                    email,
                    password,
                    _activeBackendTarget.RequiresInsecureLoginConfirmation,
                    CancellationToken.None)
                .ConfigureAwait(true);
            IsLoginEditorOpen = false;
            LoginStatus = $"已以{_sessionManager.Current.RoleLabel}身份登录。";
            await RefreshServiceSummaryAsync().ConfigureAwait(true);
            return true;
        }
        catch (Sub2ApiSessionException exception)
        {
            LoginStatus = DescribeSessionFailure(exception.Failure);
            return false;
        }
        catch
        {
            LoginStatus = "登录暂时失败，请确认当前后台地址和网络连接正常后重试。";
            return false;
        }
        finally
        {
            password = string.Empty;
            IsAuthenticating = false;
        }
    }

    private async Task RunCommandOperationAsync(
        string operation,
        Func<CancellationToken, Task<CommandResult>> action,
        bool waitForWeb,
        TimeSpan waitTimeout,
        bool openDashboardWhenReady = false)
    {
        if (!await _operationGate.WaitAsync(0).ConfigureAwait(true))
        {
            return;
        }

        BeginOperation(operation);
        try
        {
            AppendLog($"开始执行“{operation}”。", "操作");
            CommandResult result = await action(CancellationToken.None).ConfigureAwait(true);
            AppendCommandOutput(result);

            bool webReady = false;
            if (result.Success && waitForWeb)
            {
                AppendLog($"命令已完成，等待后台就绪（最长 {waitTimeout.TotalSeconds:0} 秒）。", "等待");
                webReady = await _controller
                    .WaitForWebAsync(waitTimeout, CancellationToken.None)
                    .ConfigureAwait(true);
                AppendLog(webReady ? "后台健康检查已通过。" : "等待结束，后台仍未通过健康检查。", webReady ? "正常" : "提示");
            }

            LocalGatewayStatus status = await _controller
                .GetStatusAsync(CancellationToken.None)
                .ConfigureAwait(true);
            ApplyStatus(status, updateNotice: false);
            MarkStatusRefreshed();

            if (status.WebReachable)
            {
                await RestoreSharedSessionAsync().ConfigureAwait(true);
            }

            if (!result.Success)
            {
                string detail = FirstUsefulLine(result.CombinedOutput);
                SetFailure(string.IsNullOrWhiteSpace(detail)
                    ? $"{operation}失败，退出码 {result.ExitCode}。"
                    : $"{operation}失败：{detail}", preserveHealth: true);
                return;
            }

            if (waitForWeb && !webReady && !status.WebReachable)
            {
                StatusNotice = $"{operation}命令执行成功，但本机后台尚未就绪；请刷新状态并查看操作日志。";
                HasFailure = false;
                IsHealthy = false;
                return;
            }

            if (!waitForWeb && operation.Contains("停止", StringComparison.Ordinal))
            {
                StatusNotice = status.WebReachable
                    ? "停止命令已执行，但仍检测到后台可访问，请稍后刷新。"
                    : "本地中转已停止。";
                HasFailure = false;
                IsHealthy = status.WebReachable;
                return;
            }

            if (openDashboardWhenReady)
            {
                if (!TryNormalizeHttpUrl(WebUrl, out string dashboardUrl))
                {
                    SetFailure("本机后台地址无效，服务已启动但无法自动打开后台。", preserveHealth: true);
                    AppendLog("本机后台地址无效，未执行浏览器打开操作。", "失败");
                    return;
                }

                await OpenDashboardUrlAsync(
                        dashboardUrl,
                        "本机后台",
                        $"{operation}完成，已交给系统浏览器打开 {dashboardUrl}")
                    .ConfigureAwait(true);
                return;
            }

            SetHealthyNotice($"{operation}完成，本机后台已就绪。", status);
        }
        catch (Exception exception)
        {
            SetFailure($"{operation}失败：{exception.Message}");
            AppendLog(exception.Message, "失败");
        }
        finally
        {
            EndOperation();
            _operationGate.Release();
        }
    }

    private async Task OpenDashboardUrlAsync(string url, string destinationLabel, string? successNotice = null)
    {
        try
        {
            await _controller.OpenDashboardAsync(url, CancellationToken.None).ConfigureAwait(true);
            StatusNotice = successNotice ?? $"已交给系统浏览器打开 {url}";
            HasFailure = false;
            AppendLog($"打开{destinationLabel}：{url}", "浏览器");
        }
        catch (Exception exception)
        {
            SetFailure($"无法打开{destinationLabel}：{exception.Message}", preserveHealth: true);
            AppendLog(exception.Message, "失败");
        }
    }

    private void ApplyStatus(LocalGatewayStatus status, bool updateNotice)
    {
        ArgumentNullException.ThrowIfNull(status);

        NativeMode = status.NativeMode;
        NativeRootPath = string.IsNullOrWhiteSpace(status.NativeRoot) ? "尚未设置" : status.NativeRoot;
        ControlAvailable = status.ControlAvailable;
        DockerInstalled = status.DockerInstalled;
        DockerAvailable = status.DockerAvailable;
        ModeLabel = status.WebReachable ? "当前设备已可使用" : "等待启动中转服务";
        ModeDescription = status.WebReachable
            ? "本机可直接使用；已配置局域网中转的其他电脑也可以连接。"
            : "启动服务后，本机和局域网中的已配置电脑即可通过中转使用 AI。";
        GatewaySummary = status.WebReachable
            ? "中转服务已可用"
            : status.ControlAvailable
                ? "中转服务尚未准备好"
                : "当前设备无法管理中转服务";
        WebUrl = string.IsNullOrWhiteSpace(status.WebUrl)
            ? status.NativeMode ? LocalGatewayService.NativeWebUrl : LocalGatewayService.DockerWebUrl
            : status.WebUrl;
        UpdateLanDashboardPresentation();
        WebStatusLabel = status.WebReachable ? "本机后台可访问" : "本机后台不可访问";
        RuntimeStatusLabel = status.WebReachable ? "可以开始使用" : status.ControlAvailable ? "等待启动" : "需要处理";
        Services.Clear();
        foreach (LocalGatewayServiceStatus service in status.Services
                     .OrderBy(service => ServiceOrder(service.Service))
                     .ThenBy(service => service.Service, StringComparer.OrdinalIgnoreCase))
        {
            Services.Add(new GatewayServiceRowViewModel(service));
        }

        HasServices = Services.Count > 0;
        IsHealthy = status.WebReachable;
        HasFailure = !status.WebReachable && !status.ControlAvailable;

        if (updateNotice)
        {
            string visibleSummary = HideBackendBrand(status.Summary);
            StatusNotice = status.WebReachable
                ? $"{visibleSummary}，可以直接打开本机后台。"
                : status.ControlAvailable
                    ? $"{visibleSummary}。如果服务长时间未就绪，请查看操作日志或手动重启。"
                    : $"{visibleSummary}。";
        }

        ApplySelectedBackendPresentation();
        ApplyUnavailableSelectedBackendPresentation();

        OnPropertyChanged(nameof(ModeBadge));
        OnPropertyChanged(nameof(DockerStatus));
        OnPropertyChanged(nameof(GatewayUsageHint));
        OnPropertyChanged(nameof(IsWarning));
        OnPropertyChanged(nameof(HasNoServices));
        NotifyCommandStates();
    }

    private void BeginOperation(string operation)
    {
        CurrentOperation = operation;
        IsBusy = true;
        HasFailure = false;
        StatusNotice = $"正在{operation}，请不要重复点击其他控制按钮…";
    }

    private void MarkStatusRefreshed()
    {
        _lastStatusRefreshAt = _timeProvider.GetUtcNow();
        LastRefreshed = $"更新于 {_lastStatusRefreshAt.Value.ToLocalTime():HH:mm:ss}";
        CacheStatusLabel = "已更新 · 10 分钟内继续使用当前结果";
    }

    private void EndOperation()
    {
        CurrentOperation = "空闲";
        IsBusy = false;
    }

    private void SetHealthyNotice(string message, LocalGatewayStatus status)
    {
        StatusNotice = RedactSensitiveValues(message);
        IsHealthy = status.WebReachable;
        HasFailure = false;
        OnPropertyChanged(nameof(IsWarning));
    }

    private void SetFailure(string message, bool preserveHealth = false)
    {
        // Command and process exceptions can contain values copied from a
        // profile, so the visible status must use the same redaction path as
        // the operation log.
        StatusNotice = RedactSensitiveValues(message);
        HasFailure = true;
        if (!preserveHealth)
        {
            IsHealthy = false;
        }

        OnPropertyChanged(nameof(IsWarning));
    }

    private void AppendCommandOutput(CommandResult result)
    {
        AppendLog(result.Success
            ? $"命令完成，退出码 {result.ExitCode}。"
            : $"命令失败，退出码 {result.ExitCode}。", result.Success ? "完成" : "失败");
        AppendMultiline(result.CombinedOutput, "输出");
    }

    private void AppendMultiline(string? text, string category)
    {
        if (string.IsNullOrWhiteSpace(text))
        {
            return;
        }

        string bounded = text.Length <= MaximumOutputLength
            ? text
            : text[..MaximumOutputLength] + Environment.NewLine + "…输出已截断…";
        foreach (string line in bounded.Split(['\r', '\n'], StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
        {
            AppendLog(line, category);
        }
    }

    private void AppendLog(string message, string category)
    {
        if (string.IsNullOrWhiteSpace(message))
        {
            return;
        }

        string safe = HideBackendBrand(RedactSensitiveValues(message.Trim()));
        _logLines.Add($"[{DateTime.Now:HH:mm:ss}] {category,-4}  {safe}");
        if (_logLines.Count > MaximumLogLines)
        {
            _logLines.RemoveRange(0, _logLines.Count - MaximumLogLines);
        }

        OperationLog = string.Join(Environment.NewLine, _logLines);
    }

    private static string BuildRuntimeStatus(LocalGatewayStatus status)
    {
        if (status.Services.Count == 0)
        {
            return status.ControlAvailable ? "尚未发现运行中的服务" : "本机服务管理不可用";
        }

        int healthy = status.Services.Count(service => service.IsHealthyEnough);
        return healthy == status.Services.Count
            ? $"{healthy} 个服务运行正常"
            : $"{healthy}/{status.Services.Count} 个服务正常";
    }

    private void UpdateLanDashboardPresentation()
    {
        if (!_connectionConfigurationKnown)
        {
            _currentLanDashboardUrl = null;
            LanDashboardUrl = "等待连接中心同步";
            LanDashboardStatusLabel = "请在连接中心配置“局域网中转”后打开后台";
        }
        else if (!_lanProfileFound)
        {
            _currentLanDashboardUrl = null;
            LanDashboardUrl = "未找到“局域网中转”配置";
            LanDashboardStatusLabel = "连接中心未提供 lan-default，无法确定跨电脑后台地址";
        }
        else if (!TryNormalizeHttpUrl(_currentLanDashboardUrl, out string dashboardUrl))
        {
            _currentLanDashboardUrl = null;
            LanDashboardUrl = "局域网中转地址尚未正确配置";
            LanDashboardStatusLabel = "请在连接中心编辑“局域网中转”的“局域网后台地址”。";
        }
        else
        {
            _currentLanDashboardUrl = dashboardUrl;
            LanDashboardUrl = dashboardUrl;
            LanDashboardStatusLabel = "来自连接中心“局域网中转”的局域网后台地址配置";
        }

        OpenLanDashboardCommand.NotifyCanExecuteChanged();
    }

    /// <summary>
    /// Uses only the explicit browser address configured for lan-default. API
    /// endpoints and runtime probes are deliberately never transformed into a
    /// dashboard address, because another computer may expose a different UI.
    /// </summary>
    private static bool TryCreateDashboardUrlFromConnection(ConnectionProfile profile, out string dashboardUrl)
    {
        ArgumentNullException.ThrowIfNull(profile);
        return TryNormalizeDashboardUrl(profile.DashboardUrl, out dashboardUrl);
    }

    private static bool TryNormalizeDashboardUrl(string? value, out string dashboardUrl)
    {
        dashboardUrl = string.Empty;
        if (!TryNormalizeHttpUrl(value, out string normalizedUrl) ||
            !Uri.TryCreate(normalizedUrl, UriKind.Absolute, out Uri? uri) ||
            IsPlaceholderLanHost(uri.Host))
        {
            return false;
        }

        dashboardUrl = normalizedUrl;
        return true;
    }

    private static bool IsPlaceholderLanHost(string host)
        => string.Equals(host, "192.168.x.x", StringComparison.OrdinalIgnoreCase) ||
           host.Contains(".x.", StringComparison.OrdinalIgnoreCase) ||
           host.StartsWith("x.", StringComparison.OrdinalIgnoreCase) ||
           host.EndsWith(".x", StringComparison.OrdinalIgnoreCase);

    private static bool TryNormalizeHttpUrl(string? value, out string normalizedUrl)
    {
        normalizedUrl = string.Empty;
        if (string.IsNullOrWhiteSpace(value) ||
            !Uri.TryCreate(value, UriKind.Absolute, out Uri? uri) ||
            uri.Scheme is not ("http" or "https") ||
            string.IsNullOrWhiteSpace(uri.Host) ||
            !string.IsNullOrWhiteSpace(uri.UserInfo) ||
            !string.IsNullOrWhiteSpace(uri.Query) ||
            !string.IsNullOrWhiteSpace(uri.Fragment))
        {
            return false;
        }

        normalizedUrl = uri.AbsoluteUri;
        return true;
    }

    private static int ServiceOrder(string service) => service.ToLowerInvariant() switch
    {
        "frontend" => 0,
        "sub2api" => 1,
        "postgres" => 2,
        "redis" => 3,
        _ => 10,
    };

    private static string EmptyFallback(string value, string fallback)
        => string.IsNullOrWhiteSpace(value) ? fallback : value;

    private static string FirstUsefulLine(string? text)
        => string.IsNullOrWhiteSpace(text)
            ? string.Empty
            : text.Split(['\r', '\n'], StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
                .FirstOrDefault() ?? string.Empty;

    private static string RedactSensitiveValues(string value)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return string.Empty;
        }

        try
        {
            string redacted = Regex.Replace(
                value,
                @"(?i)\b(authorization|api[-_ ]?key|auth[-_ ]?token|access[-_ ]?token|refresh[-_ ]?token|temp[-_ ]?token|password|secret)\s*[:=]\s*(?:""[^""]*""|'[^']*'|[^\s,;]+)",
                "$1=<已隐藏>",
                RegexOptions.CultureInvariant,
                TimeSpan.FromMilliseconds(100));
            redacted = Regex.Replace(
                redacted,
                @"(?i)\bbearer\s+[A-Za-z0-9._~+\-/=]+",
                "Bearer <已隐藏>",
                RegexOptions.CultureInvariant,
                TimeSpan.FromMilliseconds(100));
            return Regex.Replace(
                redacted,
                @"(?<=://)[^/\s:@]+:[^@\s/]+@",
                "<已隐藏>@",
                RegexOptions.CultureInvariant,
                TimeSpan.FromMilliseconds(100));
        }
        catch (RegexMatchTimeoutException)
        {
            return "敏感运行文本已隐藏。";
        }
    }

    internal void ApplyRecoveryUpdate(LocalGatewayRecoveryUpdate update)
    {
        ArgumentNullException.ThrowIfNull(update);
        AutomaticRecoveryLabel = update.Message;
        HasAutomaticRecoveryFailure = update.State is
            LocalGatewayRecoveryState.Failed or LocalGatewayRecoveryState.Suspended;
        if (update.State is LocalGatewayRecoveryState.Recovering or LocalGatewayRecoveryState.Recovered)
        {
            StatusNotice = update.Message;
        }
    }

    private static string HideBackendBrand(string value) => Regex.Replace(
        value,
        "sub2api",
        "本机中转",
        RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);

    private async Task RestoreSharedSessionAsync()
    {
        if (_sessionManager is null ||
            _sessionManager.Current.IsRestoring)
        {
            return;
        }

        try
        {
            await ResolveAvailableBackendAsync().ConfigureAwait(true);
            if (_activeBackendTarget is not null)
            {
                if (_sessionManager.Current is { IsAuthenticated: true, ApiBaseUri: not null } current &&
                    SameEndpoint(current.ApiBaseUri, _activeBackendTarget.ApiBaseUri))
                {
                    return;
                }

                await _sessionManager
                    .RestoreAsync(_activeBackendTarget.ApiBaseUri, CancellationToken.None)
                    .ConfigureAwait(true);
            }
        }
        catch (Sub2ApiSessionException exception)
        {
            LoginStatus = DescribeSessionFailure(exception.Failure);
        }
    }

    private async Task RefreshBackendSessionAfterConnectionChangeAsync()
    {
        try
        {
            await ResolveAvailableBackendAsync().ConfigureAwait(true);
            await RestoreSharedSessionAsync().ConfigureAwait(true);
        }
        catch (Sub2ApiSessionException exception)
        {
            LoginStatus = DescribeSessionFailure(exception.Failure);
        }
        catch
        {
            LoginStatus = "当前后台暂时无法连接，请检查连接中心地址。";
        }
    }

    private async Task ResolveAvailableBackendAsync()
    {
        if (_backendCandidates.Count > 0)
        {
            Sub2ApiEndpointTarget candidate = _backendCandidates[0];
            if (candidate.Kind == ConnectionProfileKind.Cloud && candidate.DashboardUri is null)
            {
                candidate = candidate with { DashboardUri = new Uri(candidate.ApiBaseUri, "/") };
            }

            if (_backendProbe is not null)
            {
                _ = await _backendProbe
                    .ProbeAsync(candidate.ApiBaseUri, CancellationToken.None)
                    .ConfigureAwait(true);
            }

            _activeBackendTarget = candidate;
            BackendSourceLabel = FormatBackendSourceLabel("当前后台", _activeBackendTarget);
            ApplySelectedBackendPresentation();
            NotifyBackendTargetChanged();
            return;
        }

        if (_hasExplicitBackendSelection)
        {
            _activeBackendTarget = null;
            BackendSourceLabel = _backendSelectionIssue ?? "连接中心当前来源没有可用的后台地址";
            IsLoginEditorOpen = false;
            ApplyUnavailableSelectedBackendPresentation();
            NotifyBackendTargetChanged();
            return;
        }

        if (_localGatewayEndpointResolver is not null)
        {
            LocalGatewayEndpointResolution local = await _localGatewayEndpointResolver
                .ResolveAsync(CancellationToken.None)
                .ConfigureAwait(true);
            if (local.IsReady && local.ApiBaseUri is not null)
            {
                _activeBackendTarget = new Sub2ApiEndpointTarget(
                    ConnectionProfileIds.LocalMachine,
                    "本机中转",
                    ConnectionProfileKind.Local,
                    local.ApiBaseUri,
                    local.DashboardUri);
                BackendSourceLabel = FormatBackendSourceLabel("当前后台", _activeBackendTarget);
                NotifyBackendTargetChanged();
                return;
            }
        }

        _activeBackendTarget = null;
        BackendSourceLabel = "没有检测到可访问的数据后台";
        NotifyBackendTargetChanged();
    }

    private static string FormatBackendSourceLabel(string prefix, Sub2ApiEndpointTarget target)
    {
        Uri homepage = target.DashboardUri ?? target.ApiBaseUri;
        string address = homepage.AbsolutePath is "" or "/"
            ? homepage.GetLeftPart(UriPartial.Authority)
            : homepage.GetLeftPart(UriPartial.Path).TrimEnd('/');
        return $"{prefix}：{target.DisplayName} · 主页：{address}";
    }
    private void NotifyBackendTargetChanged()
    {
        OnPropertyChanged(nameof(GatewayUsageHint));
        OnPropertyChanged(nameof(PrimaryGatewayActionLabel));
        OnPropertyChanged(nameof(ShowLocalServiceControls));
        OnPropertyChanged(nameof(ServiceSummaryColumnSpan));
        PrimaryGatewayActionCommand.NotifyCanExecuteChanged();
        BeginLoginCommand.NotifyCanExecuteChanged();
    }

    private void ApplySelectedBackendPresentation()
    {
        if (_activeBackendTarget is not { IsLocalMachine: false } selected)
        {
            return;
        }

        GatewaySummary = $"当前来源：{selected.DisplayName}";
        ModeLabel = "连接中心当前来源";
        ModeDescription = selected.DashboardUri is not null
            ? "中转服务页已跟随连接中心，可以直接打开该来源后台。"
            : "API 分流已跟随连接中心；该来源暂未配置管理后台地址。";
        RuntimeStatusLabel = selected.DashboardUri is not null ? "后台入口已配置" : "仅 API 连接";
        StatusNotice = selected.DashboardUri is not null
            ? "当前页面已按照连接中心实际应用的来源显示。"
            : "当前来源未提供管理后台入口；不会自动切回本机中转。";
    }

    private void ApplyUnavailableSelectedBackendPresentation()
    {
        if (!_hasExplicitBackendSelection || _activeBackendTarget is not null)
        {
            return;
        }

        string displayName = string.IsNullOrWhiteSpace(_selectedBackendDisplayName)
            ? "当前来源"
            : _selectedBackendDisplayName;
        string issue = _backendSelectionIssue ?? "当前来源没有可用的后台地址。";
        GatewaySummary = $"当前来源：{displayName}";
        ModeLabel = "连接中心当前来源";
        ModeDescription = issue;
        RuntimeStatusLabel = "账户登录不可用";
        StatusNotice = issue;
    }

    private static bool SameEndpoint(Uri left, Uri right)
        => Uri.Compare(
            left,
            right,
            UriComponents.SchemeAndServer | UriComponents.Path,
            UriFormat.SafeUnescaped,
            StringComparison.OrdinalIgnoreCase) == 0;

    private void OnSessionChanged(object? sender, EventArgs args)
    {
        void Apply()
        {
            Sub2ApiSessionState state = _sessionManager?.Current ?? Sub2ApiSessionState.SignedOut;
            ApplySessionState(state);
            if (state.IsAuthenticated && !IsAuthenticating)
            {
                _ = RefreshServiceSummaryAsync();
            }
        }

        if (System.Windows.Application.Current?.Dispatcher is { } dispatcher && !dispatcher.CheckAccess())
        {
            _ = dispatcher.BeginInvoke(Apply);
        }
        else
        {
            Apply();
        }
    }

    private void ApplySessionState(Sub2ApiSessionState state)
    {
        LoginRoleLabel = state.RoleLabel;
        LoginStatus = state.Status;
        AccountBalanceLabel = state.IsAuthenticated ? $"${state.Balance:N2}" : "—";
        OnPropertyChanged(nameof(IsSignedIn));
        OnPropertyChanged(nameof(ShowLoginEditor));
        OnPropertyChanged(nameof(ShowSignedInSummary));
        OnPropertyChanged(nameof(ShowSignedOutSummary));
        OnPropertyChanged(nameof(ShowLoginPrompt));
        OnPropertyChanged(nameof(ShowAdministratorSummary));
        BeginLoginCommand.NotifyCanExecuteChanged();
        LogoutCommand.NotifyCanExecuteChanged();
        RefreshServiceSummaryCommand.NotifyCanExecuteChanged();
        if (!state.IsAuthenticated)
        {
            ClearServiceSummary();
        }
    }

    private void ApplyServiceSummary(Sub2ApiServiceSummary summary)
    {
        AccountBalanceLabel = $"${summary.Balance:N2}";
        TodayUsageLabel = summary.UsageAvailable
            ? $"{summary.TodayRequests:N0} 次 · {summary.TodayTokens:N0} Token"
            : "暂不可读";
        TodayRequestLabel = summary.UsageAvailable ? $"{summary.TodayRequests:N0} 次" : "暂不可读";
        TodayTokenLabel = summary.UsageAvailable ? $"{summary.TodayTokens:N0}" : "暂不可读";
        TodayActualCostLabel = summary.UsageAvailable ? $"${summary.TodayActualCost:N4}" : "暂不可读";
        ApiKeyStatusLabel = summary.UsageAvailable
            ? $"{summary.ActiveApiKeyCount:N0}/{summary.ApiKeyCount:N0} 可用"
            : "暂不可读";
        (PlatformQuotaLabel, HasQuotaWarning) = DescribeQuotaState(summary.PlatformQuotas);
        HasTodayFailures = summary.RecentFailuresAvailable && summary.RecentFailureCount > 0;
        RecentFailureLabel = !summary.RecentFailuresAvailable
            ? "错误记录未开放"
            : summary.RecentFailureCount == 0
                ? "今日没有失败记录"
                : $"今日有 {summary.RecentFailureCount:N0} 条失败记录";
        UserServiceStatusLabel = !summary.UsageAvailable
            ? "今日请求和扣费暂时无法读取，请重试或查看详细用量。"
            : HasTodayFailures
            ? "今天有调用失败，建议查看用量仪表盘中的错误记录。"
            : HasQuotaWarning
                ? "有平台额度接近上限，请留意剩余额度。"
                : "今天使用正常，暂未发现需要处理的问题。";

        if (summary.Administrator is { } admin)
        {
            AdminTrafficLabel = $"QPS {admin.CurrentQps:N2} · TPS {admin.CurrentTps:N2}";
            AdminLatencyLabel = admin.P95LatencyMilliseconds is { } p95
                ? $"错误率 {admin.ErrorRatePercent:N2}% · P95 {p95:N0} ms"
                : $"错误率 {admin.ErrorRatePercent:N2}% · P95 暂无";
            AdminConcurrencyLabel = $"并发 {admin.CurrentConcurrency:N0} · 排队 {admin.WaitingInQueue:N0}";
            AdminAccountHealthLabel = $"可用 {admin.AvailableAccounts:N0}/{admin.TotalAccounts:N0} · 限流 {admin.RateLimitedAccounts:N0} · 异常 {admin.ErrorAccounts:N0}";
            ServiceVersionLabel = $"v{admin.Version} · {admin.UpdateStatus}";
            ServiceLogHealthLabel = admin.LogHealth;
            HasAdministratorAttention = admin.ErrorRatePercent >= 2d ||
                                        admin.WaitingInQueue > 0 ||
                                        admin.ErrorAccounts > 0 ||
                                        admin.RateLimitedAccounts > 0 ||
                                        admin.LogHealth.Contains("异常", StringComparison.Ordinal);
            AdministratorHealthHeadline = HasAdministratorAttention
                ? "全站有需要关注的项目"
                : "全站运行平稳";
            string responseDescription = admin.P95LatencyMilliseconds is { } responseMilliseconds
                ? $"大多数请求在 {responseMilliseconds:N0} ms 内响应"
                : "响应速度暂无数据";
            AdministratorHealthDetail = $"可用账号 {admin.AvailableAccounts:N0}/{admin.TotalAccounts:N0} · " +
                                        $"错误率 {admin.ErrorRatePercent:N2}% · {responseDescription}" +
                                        (admin.WaitingInQueue > 0 ? $" · 当前排队 {admin.WaitingInQueue:N0}" : string.Empty);
            HasServiceUpdate = admin.UpdateStatus.Contains("可用更新", StringComparison.Ordinal) ||
                               admin.UpdateStatus.Contains("可更新至", StringComparison.Ordinal);
            ServiceUpdateLabel = HasServiceUpdate ? admin.UpdateStatus : string.Empty;
        }
    }

    private static (string Label, bool IsWarning) DescribeQuotaState(
        IReadOnlyList<PlatformQuotaSummary> quotas)
    {
        var windows = quotas
            .SelectMany(quota => new[]
            {
                CreateQuotaWindow(quota.Platform, quota.DailyLimit, quota.DailyUsage),
                CreateQuotaWindow(quota.Platform, quota.WeeklyLimit, quota.WeeklyUsage),
                CreateQuotaWindow(quota.Platform, quota.MonthlyLimit, quota.MonthlyUsage),
            })
            .Where(window => window is not null)
            .Select(window => window!.Value)
            .ToArray();
        if (windows.Length == 0)
        {
            return ("未设置平台限额", false);
        }

        (string Platform, decimal RemainingRatio) closest = windows.MinBy(window => window.RemainingRatio);
        int remainingPercent = (int)Math.Clamp(Math.Round(closest.RemainingRatio * 100m), 0m, 100m);
        if (remainingPercent <= 0)
        {
            return ($"{DescribePlatform(closest.Platform)} 额度已用尽", true);
        }

        if (remainingPercent <= 15)
        {
            return ($"{DescribePlatform(closest.Platform)} 额度剩余 {remainingPercent}%", true);
        }

        return ("各平台额度正常", false);
    }

    private static (string Platform, decimal RemainingRatio)? CreateQuotaWindow(
        string platform,
        decimal? limit,
        decimal usage)
        => limit is > 0m
            ? (platform, Math.Max(0m, (limit.Value - usage) / limit.Value))
            : null;

    private static string DescribePlatform(string platform)
        => platform.Trim().ToLowerInvariant() switch
        {
            "openai" or "codex" => "OpenAI",
            "anthropic" or "claude" => "Claude",
            "gemini" or "google" => "Gemini",
            "grok" or "xai" => "Grok",
            _ => platform,
        };

    private void ClearServiceSummary()
    {
        TodayUsageLabel = "—";
        TodayRequestLabel = "—";
        TodayTokenLabel = "—";
        TodayActualCostLabel = "—";
        ApiKeyStatusLabel = "—";
        PlatformQuotaLabel = "—";
        RecentFailureLabel = "—";
        UserServiceStatusLabel = "登录后自动读取今天的使用状态。";
        HasTodayFailures = false;
        HasQuotaWarning = false;
        AdminTrafficLabel = "—";
        AdminLatencyLabel = "—";
        AdminConcurrencyLabel = "—";
        AdminAccountHealthLabel = "—";
        ServiceVersionLabel = "—";
        ServiceLogHealthLabel = "—";
        AdministratorHealthHeadline = "全站状态尚未读取";
        AdministratorHealthDetail = "—";
        HasAdministratorAttention = false;
        HasServiceUpdate = false;
        ServiceUpdateLabel = string.Empty;
    }

    private static string DescribeSessionFailure(Sub2ApiSessionFailure failure)
        => failure switch
        {
            Sub2ApiSessionFailure.InvalidCredentials => "账户或密码不正确，请检查后重试。",
            Sub2ApiSessionFailure.RequiresTwoFactor => "该账户开启了两步验证，请先在后台完成验证。",
            Sub2ApiSessionFailure.Forbidden => "当前账户没有登录此服务的权限。",
            Sub2ApiSessionFailure.ComplianceRequired => "后台要求先完成合规确认，请打开后台处理。",
            Sub2ApiSessionFailure.SecureStorageUnavailable => "Windows 安全存储不可用，无法保存登录。",
            Sub2ApiSessionFailure.GatewayUnavailable => "当前后台暂时不可访问，请检查地址和网络状态。",
            _ => "登录响应无法识别，请确认当前后台版本。",
        };

    private bool CanRefresh() => !IsBusy;

    private bool CanControl() => !IsBusy && ControlAvailable;

    private bool CanStartAndOpenDashboard()
        => CanControl() && TryNormalizeHttpUrl(WebUrl, out _);

    private bool CanPrimaryGatewayAction()
        => _hasExplicitBackendSelection && _activeBackendTarget is null
            ? false
            : _activeBackendTarget is { IsLocalMachine: false } selected
            ? selected.DashboardUri is not null && !IsBusy
            : IsHealthy
                ? CanOpenDashboard()
                : CanStartAndOpenDashboard();

    private bool CanOpenDashboard()
        => !IsBusy && TryNormalizeHttpUrl(WebUrl, out _);

    private bool CanOpenLanDashboard()
        => !IsBusy && TryNormalizeHttpUrl(_currentLanDashboardUrl, out _);

    private bool CanBeginLogin() =>
        _sessionManager is not null && _activeBackendTarget is not null && !IsSignedIn && !IsAuthenticating;

    private bool CanLogout() => _sessionManager is not null && IsSignedIn && !IsAuthenticating;

    private bool CanProbeConnections() => _endpointProbeService is not null && !IsProbingConnections;

    private bool CanRefreshServiceSummary()
        => _serviceSummaryClient is not null && IsSignedIn && !IsLoadingServiceSummary;

    partial void OnIsBusyChanged(bool value)
    {
        NotifyCommandStates();
        OnPropertyChanged(nameof(IsWarning));
    }

    partial void OnControlAvailableChanged(bool value)
    {
        NotifyCommandStates();
        OnPropertyChanged(nameof(GatewayUsageHint));
    }

    partial void OnWebUrlChanged(string value)
    {
        OpenDashboardCommand.NotifyCanExecuteChanged();
        StartAndOpenDashboardCommand.NotifyCanExecuteChanged();
        PrimaryGatewayActionCommand.NotifyCanExecuteChanged();
    }

    partial void OnHasServicesChanged(bool value)
    {
        OnPropertyChanged(nameof(HasNoServices));
    }

    partial void OnIsHealthyChanged(bool value)
    {
        OnPropertyChanged(nameof(IsWarning));
        OnPropertyChanged(nameof(GatewayUsageHint));
        OnPropertyChanged(nameof(PrimaryGatewayActionLabel));
        PrimaryGatewayActionCommand.NotifyCanExecuteChanged();
    }

    partial void OnHasFailureChanged(bool value) => OnPropertyChanged(nameof(IsWarning));

    partial void OnIsLoginEditorOpenChanged(bool value)
    {
        OnPropertyChanged(nameof(ShowLoginEditor));
        OnPropertyChanged(nameof(ShowLoginPrompt));
    }

    partial void OnIsAuthenticatingChanged(bool value)
    {
        BeginLoginCommand.NotifyCanExecuteChanged();
        LogoutCommand.NotifyCanExecuteChanged();
    }

    partial void OnIsProbingConnectionsChanged(bool value)
        => ProbeConnectionsCommand.NotifyCanExecuteChanged();

    partial void OnIsLoadingServiceSummaryChanged(bool value)
        => RefreshServiceSummaryCommand.NotifyCanExecuteChanged();

    partial void OnNativeModeChanged(bool value)
    {
        OnPropertyChanged(nameof(ModeBadge));
        OnPropertyChanged(nameof(DockerStatus));
    }

    partial void OnDockerInstalledChanged(bool value) => OnPropertyChanged(nameof(DockerStatus));

    partial void OnDockerAvailableChanged(bool value) => OnPropertyChanged(nameof(DockerStatus));

    private void NotifyCommandStates()
    {
        RefreshStatusCommand.NotifyCanExecuteChanged();
        StartGatewayCommand.NotifyCanExecuteChanged();
        StartAndOpenDashboardCommand.NotifyCanExecuteChanged();
        StopGatewayCommand.NotifyCanExecuteChanged();
        RestartGatewayCommand.NotifyCanExecuteChanged();
        OpenDashboardCommand.NotifyCanExecuteChanged();
        OpenLanDashboardCommand.NotifyCanExecuteChanged();
        BeginLoginCommand.NotifyCanExecuteChanged();
        LogoutCommand.NotifyCanExecuteChanged();
        ProbeConnectionsCommand.NotifyCanExecuteChanged();
        RefreshServiceSummaryCommand.NotifyCanExecuteChanged();
    }
}

public sealed class EndpointHealthRowViewModel
{
    internal EndpointHealthRowViewModel(EndpointHealthResult result)
    {
        Client = result.ClientLabel;
        Destination = result.DestinationLabel;
        IsHealthy = result.Succeeded;
        Status = result.StatusLabel;
        LatestLatency = result.LatestLatencyMilliseconds is { } latest ? $"{latest:N0} ms" : "—";
        SuccessRate = result.SuccessRate24Hours is { } rate ? $"{rate:N1}%" : "—";
        Percentiles = result.P50LatencyMilliseconds is { } p50 && result.P95LatencyMilliseconds is { } p95
            ? $"P50 {p50:N0} ms · P95 {p95:N0} ms"
            : "样本积累中";
        LastSuccess = result.LastSuccessAt is { } last
            ? last.ToLocalTime().ToString("MM-dd HH:mm")
            : "暂无成功记录";
    }

    public string Client { get; }

    public string Destination { get; }

    public bool IsHealthy { get; }

    public string Status { get; }

    public string LatestLatency { get; }

    public string SuccessRate { get; }

    public string Percentiles { get; }

    public string LastSuccess { get; }
}

public sealed class CliReadinessRowViewModel
{
    internal CliReadinessRowViewModel(CliInstallation installation)
    {
        Client = installation.Kind switch
        {
            CliKind.Codex => "Codex",
            CliKind.ClaudeCode => "Claude Code",
            CliKind.GeminiCli => "Gemini CLI",
            _ => installation.Kind.ToString(),
        };
        IsReady = installation.CanRun;
        Version = string.IsNullOrWhiteSpace(installation.Version) ? "版本未知" : installation.Version;
        Path = string.IsNullOrWhiteSpace(installation.ExecutablePath) ? "未找到安装路径" : installation.ExecutablePath;
        Conflict = installation.HasPathConflict
            ? $"发现 {installation.AlternativeExecutablePaths.Count + 1} 个可运行版本，当前优先使用以上路径"
            : installation.IsInstalled
                ? "路径唯一"
                : "尚未安装或无法启动";
    }

    public string Client { get; }

    public bool IsReady { get; }

    public string Version { get; }

    public string Path { get; }

    public string Conflict { get; }
}

public sealed class GatewayServiceRowViewModel
{
    internal GatewayServiceRowViewModel(LocalGatewayServiceStatus service)
    {
        Name = HideBackendBrand(string.IsNullOrWhiteSpace(service.Service)
            ? EmptyFallback(service.Name, "未命名服务")
            : service.Service);
        ContainerName = HideBackendBrand(EmptyFallback(service.Name, Name));
        State = HumanizeState(service.State);
        Health = string.IsNullOrWhiteSpace(service.Health)
            ? service.IsHealthyEnough ? "正常" : "无健康报告"
            : HumanizeHealth(service.Health);
        Detail = HideBackendBrand(string.Join(
            "  ·  ",
            new[] { service.Ports, service.Status }
                .Where(value => !string.IsNullOrWhiteSpace(value))));
        if (string.IsNullOrWhiteSpace(Detail))
        {
            Detail = "暂无额外运行信息";
        }

        IsHealthy = service.IsHealthyEnough;
    }

    public string Name { get; }

    public string ContainerName { get; }

    public string State { get; }

    public string Health { get; }

    public string Detail { get; }

    public bool IsHealthy { get; }

    private static string HumanizeState(string value) => value.ToLowerInvariant() switch
    {
        "running" => "运行中",
        "restarting" => "重启中",
        "created" => "已创建",
        "exited" => "已退出",
        "stopped" => "已停止",
        "paused" => "已暂停",
        "dead" => "异常退出",
        _ => string.IsNullOrWhiteSpace(value) ? "未知" : value,
    };

    private static string HumanizeHealth(string value) => value.ToLowerInvariant() switch
    {
        "healthy" => "健康",
        "starting" => "检查中",
        "unhealthy" => "不健康",
        _ => value,
    };

    private static string EmptyFallback(string value, string fallback)
        => string.IsNullOrWhiteSpace(value) ? fallback : value;

    private static string HideBackendBrand(string value) => Regex.Replace(
        value,
        "sub2api",
        "本机中转",
        RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);
}

internal interface ILocalGatewayController
{
    LocalGatewayStatus GetStartupStatus();

    Task<LocalGatewayStatus> GetStatusAsync(CancellationToken cancellationToken);

    Task<CommandResult> StartAsync(CancellationToken cancellationToken);

    Task<CommandResult> ConfigureNativeRootAsync(string selectedPath, CancellationToken cancellationToken);

    Task<CommandResult> StopAsync(CancellationToken cancellationToken);

    Task<CommandResult> RestartAsync(CancellationToken cancellationToken);

    Task<bool> WaitForWebAsync(TimeSpan timeout, CancellationToken cancellationToken);

    Task OpenDashboardAsync(string url, CancellationToken cancellationToken);
}

internal sealed class LocalGatewayController : ILocalGatewayController
{
    private readonly LocalGatewayService _service;

    public LocalGatewayController()
        : this(new LocalGatewayService())
    {
    }

    internal LocalGatewayController(LocalGatewayService service)
    {
        _service = service ?? throw new ArgumentNullException(nameof(service));
    }

    public LocalGatewayStatus GetStartupStatus() => _service.GetStartupStatus();

    public Task<LocalGatewayStatus> GetStatusAsync(CancellationToken cancellationToken)
        => _service.GetStatusAsync(cancellationToken);

    public Task<CommandResult> StartAsync(CancellationToken cancellationToken)
        => _service.StartAsync(cancellationToken);

    public Task<CommandResult> ConfigureNativeRootAsync(string selectedPath, CancellationToken cancellationToken)
        => _service.ConfigureNativeRootAsync(selectedPath, cancellationToken);

    public Task<CommandResult> StopAsync(CancellationToken cancellationToken)
        => _service.StopAsync(cancellationToken);

    public Task<CommandResult> RestartAsync(CancellationToken cancellationToken)
        => _service.RestartAsync(cancellationToken);

    public Task<bool> WaitForWebAsync(TimeSpan timeout, CancellationToken cancellationToken)
        => _service.WaitForWebAsync(timeout, cancellationToken);

    public Task OpenDashboardAsync(string url, CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        if (!Uri.TryCreate(url, UriKind.Absolute, out Uri? uri) || uri.Scheme is not ("http" or "https"))
        {
            throw new InvalidOperationException("本机后台地址无效。");
        }

        Process.Start(new ProcessStartInfo(uri.AbsoluteUri)
        {
            UseShellExecute = true,
        });
        return Task.CompletedTask;
    }
}
