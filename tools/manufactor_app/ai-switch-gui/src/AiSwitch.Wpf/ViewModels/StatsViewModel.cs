using System.Collections.ObjectModel;
using System.Diagnostics;
using System.Globalization;
using System.Net;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.RegularExpressions;
using AiSwitchGui;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Controls;
using LanAi.Workspace.Wpf.Services;

namespace LanAi.Workspace.Wpf.ViewModels;

/// <summary>
/// Reads the authenticated Sub2API usage dashboard while keeping the password
/// outside WPF data binding. Passwords enter only through RefreshAsync and are
/// immediately passed to the compatibility controller.
/// </summary>
public partial class StatsViewModel : PageViewModel
{
    private static readonly Uri LocalBackendApiBaseUri = new("http://127.0.0.1:8080/");
    private static readonly Uri LocalBackendDashboardUri = new("http://127.0.0.1:8080/dashboard");
    private static readonly Sub2ApiEndpointTarget LocalBackendTarget = new(
        ConnectionProfileIds.LocalMachine,
        "本机中转",
        ConnectionProfileKind.Local,
        LocalBackendApiBaseUri,
        LocalBackendDashboardUri);
    private static readonly LocalGatewayEndpointResolution LocalBackendEndpoint = new(
        LocalGatewayEndpointResolutionStatus.Ready,
        LocalBackendApiBaseUri,
        LocalBackendDashboardUri);

    private readonly IStatsController _controller;
    private readonly ILocalTelemetryRepository _localTelemetryRepository;
    private readonly ILocalCloudStatisticsClient _localCloudStatisticsClient;
    private readonly ILocalGatewayAuthorizationStore _localGatewayAuthorizationStore;
    private readonly ILocalUserStatsAuthorizationStore _localUserStatsAuthorizationStore;
    private readonly ILocalGatewayEndpointResolver _localGatewayEndpointResolver;
    private readonly ISub2ApiSessionManager? _sub2ApiSessionManager;
    private readonly ICloudUsageSnapshotCache _cloudUsageSnapshotCache;
    private readonly SemaphoreSlim _operationGate = new(1, 1);
    private readonly SemaphoreSlim _localOperationGate = new(1, 1);
    private LocalGatewayAuthorization _administratorAuthorization = LocalGatewayAuthorization.Unavailable;
    private LocalUserStatsAuthorization _userAuthorization = LocalUserStatsAuthorization.Unavailable;
    private LocalGatewayEndpointResolution _localGatewayEndpoint = LocalBackendEndpoint;
    private StatsSettings _storedSettings = new();
    private int _initialized;
    private bool _updatingLocalFilters;
    private IReadOnlyList<LocalTelemetrySourceFilterOption> _configuredLocalSourceFilters =
        Array.Empty<LocalTelemetrySourceFilterOption>();
    private IReadOnlyList<LocalTelemetrySourceFilterOption> _observedLocalSourceFilters =
        Array.Empty<LocalTelemetrySourceFilterOption>();
    private string? _activeConnectionProfileId;
    private Sub2ApiEndpointTarget _activeBackendTarget = LocalBackendTarget;

    [ObservableProperty]
    private string gatewayBaseUrl = string.Empty;

    [ObservableProperty]
    private string email = string.Empty;

    [ObservableProperty]
    private string localAuthorizationEmail = string.Empty;

    [ObservableProperty]
    private int selectedTrendDays = 7;

    [ObservableProperty]
    private string dashboardRangeLabel = "近 7 天";

    [ObservableProperty]
    private string dashboardRangeCaption = "今天至过去 6 天";

    [ObservableProperty]
    private bool hasSavedPassword;

    [ObservableProperty]
    private bool isLocalGatewayAvailable;

    [ObservableProperty]
    private bool isCheckingLocalGateway;

    [ObservableProperty]
    private bool isLocalAuthorizationEditorOpen;

    [ObservableProperty]
    private bool hasLocalAdministratorAuthorization;

    [ObservableProperty]
    private bool hasLocalUserAuthorization;

    [ObservableProperty]
    private string cloudConnectionNotice = "正在检查本机中转状态…";

    [ObservableProperty]
    private string cloudAuthorizationNotice = "本机统计会在获得授权后自动读取。";

    [ObservableProperty]
    private string cloudDataScope = "尚未读取后台统计";

    [ObservableProperty]
    private string backendSourceLabel = "当前后台：本机中转";

    [ObservableProperty]
    private bool isBusy;

    [ObservableProperty]
    private bool hasFailure;

    [ObservableProperty]
    private bool hasData;

    [ObservableProperty]
    private string statusNotice = "正在读取已保存的统计设置…";

    [ObservableProperty]
    private string lastUpdated = "尚未刷新";

    [ObservableProperty]
    private string totalRequests = "—";

    [ObservableProperty]
    private string todayRequests = "—";

    [ObservableProperty]
    private string totalTokens = "—";

    [ObservableProperty]
    private string todayTokens = "—";

    [ObservableProperty]
    private string totalCost = "—";

    [ObservableProperty]
    private string todayCost = "—";

    [ObservableProperty]
    private string actualCost = "—";

    [ObservableProperty]
    private string cacheReadTokens = "—";

    [ObservableProperty]
    private string cacheCreationTokens = "—";

    [ObservableProperty]
    private string averageDuration = "—";

    [ObservableProperty]
    private string apiKeySummary = "—";

    [ObservableProperty]
    private string throughputSummary = "—";

    [ObservableProperty]
    private string trendSummary = "等待统计数据";

    [ObservableProperty]
    private string recentTrendText = "刷新后显示近期每日请求、Token 与费用。";

    [ObservableProperty]
    private string cloudRangeRequests = "—";

    [ObservableProperty]
    private string cloudRangeTokens = "—";

    [ObservableProperty]
    private string cloudRangeActualCost = "—";

    [ObservableProperty]
    private string cloudRangeAverageLatency = "—";

    [ObservableProperty]
    private string cloudRangeInputTokens = "—";

    [ObservableProperty]
    private string cloudRangeOutputTokens = "—";

    [ObservableProperty]
    private string cloudRangeCacheReadTokens = "—";

    [ObservableProperty]
    private string cloudRangeCacheCreationTokens = "—";

    [ObservableProperty]
    private string cloudRangeCacheHitRate = "—";

    [ObservableProperty]
    private string cloudLifetimeSummary = "历史累计尚未读取";

    [ObservableProperty]
    private StatisticsScope selectedScope = StatisticsScope.Cloud;

    [ObservableProperty]
    private bool isLocalBusy;

    [ObservableProperty]
    private bool hasLocalTelemetry;

    [ObservableProperty]
    private bool hasLocalTelemetryFailure;

    [ObservableProperty]
    private string localStatusNotice = "正在读取这台电脑上的聚合用量…";

    [ObservableProperty]
    private string localLastUpdated = "尚未读取";

    [ObservableProperty]
    private string localTodayTokens = "—";

    [ObservableProperty]
    private string localTodayRequests = "—";

    [ObservableProperty]
    private string localSevenDayTokens = "—";

    [ObservableProperty]
    private string localSevenDayRequests = "—";

    [ObservableProperty]
    private string localSuccessRate = "—";

    [ObservableProperty]
    private string localAverageLatency = "—";

    [ObservableProperty]
    private string localInputTokens = "—";

    [ObservableProperty]
    private string localOutputTokens = "—";

    [ObservableProperty]
    private string localCachedInputTokens = "—";

    [ObservableProperty]
    private string localCacheCreationTokens = "—";

    [ObservableProperty]
    private string localCacheHitRate = "—";

    [ObservableProperty]
    private string localSuccessfulRequests = "—";

    [ObservableProperty]
    private string localFailedRequests = "—";

    [ObservableProperty]
    private string localNetworkStatus = "尚未检测";

    [ObservableProperty]
    private string localNetworkDetail = "开始一次工作台对话或刷新工作区后，会记录最近一次连接探测。";

    [ObservableProperty]
    private string localTrendSummary = "近 7 天尚无本工作台记录";

    [ObservableProperty]
    private string localHourlyTrendSummary = "近 24 小时尚无本工作台记录";

    [ObservableProperty]
    private string localRangeRequests = "—";

    [ObservableProperty]
    private string localRangeTokens = "—";

    [ObservableProperty]
    private string localRangeSuccessRate = "—";

    [ObservableProperty]
    private string localRangeAverageLatency = "—";

    [ObservableProperty]
    private string localRangeCacheReadTokens = "—";

    [ObservableProperty]
    private string localRangeCacheCreationTokens = "—";

    [ObservableProperty]
    private string localRangeCacheHitRate = "—";

    [ObservableProperty]
    private LocalTelemetrySourceFilterOption? selectedLocalSourceFilter;

    [ObservableProperty]
    private LocalTelemetryCliFilterOption? selectedLocalCliFilter;

    [ObservableProperty]
    private LocalTelemetryModelFilterOption? selectedLocalModelFilter;

    public StatsViewModel()
        : this(new StatsController(), EmptyLocalTelemetryRepository.Instance)
    {
    }

    internal StatsViewModel(
        IStatsController controller,
        ILocalTelemetryRepository? localTelemetryRepository = null,
        ILocalCloudStatisticsClient? localCloudStatisticsClient = null,
        ILocalGatewayAuthorizationStore? localGatewayAuthorizationStore = null,
        ILocalUserStatsAuthorizationStore? localUserStatsAuthorizationStore = null,
        IConnectionProfileReader? connectionProfileReader = null,
        ILocalGatewayEndpointResolver? localGatewayEndpointResolver = null,
        ISub2ApiSessionManager? sub2ApiSessionManager = null,
        ICloudUsageSnapshotCache? cloudUsageSnapshotCache = null)
        : base("用量仪表盘", "本地记录工作台会话；本机后台可安全复用授权，云端仍按账户权限读取真实账本。")
    {
        _controller = controller ?? throw new ArgumentNullException(nameof(controller));
        _localTelemetryRepository = localTelemetryRepository ?? EmptyLocalTelemetryRepository.Instance;
        _ = connectionProfileReader;
        _localGatewayEndpointResolver = localGatewayEndpointResolver ?? new ManualCloudOnlyLocalGatewayEndpointResolver();
        _localCloudStatisticsClient = localCloudStatisticsClient ?? new LocalCloudStatisticsClient();
        _localGatewayAuthorizationStore = localGatewayAuthorizationStore ?? new LocalGatewayAuthorizationStore();
        _localUserStatsAuthorizationStore = localUserStatsAuthorizationStore ?? new DpapiLocalUserStatsAuthorizationStore();
        _sub2ApiSessionManager = sub2ApiSessionManager;
        _cloudUsageSnapshotCache = cloudUsageSnapshotCache ?? new CloudUsageSnapshotCache();
        if (_sub2ApiSessionManager is not null)
        {
            _sub2ApiSessionManager.SessionChanged += OnSharedSessionChanged;
        }
        Models = new ObservableCollection<ModelStatsRowViewModel>();
        Trend = new ObservableCollection<TrendStatsRowViewModel>();
        CloudTrend = new ObservableCollection<UsageLineChartPoint>();
        LocalTrend = new ObservableCollection<UsageLineChartPoint>();
        LocalRequestTrend = new ObservableCollection<UsageLineChartPoint>();
        LocalInputTokenTrend = new ObservableCollection<UsageLineChartPoint>();
        LocalOutputTokenTrend = new ObservableCollection<UsageLineChartPoint>();
        LocalCacheReadTrend = new ObservableCollection<UsageLineChartPoint>();
        LocalCacheWriteTrend = new ObservableCollection<UsageLineChartPoint>();
        LocalSuccessRateTrend = new ObservableCollection<UsageLineChartPoint>();
        LocalLatencyTrend = new ObservableCollection<UsageLineChartPoint>();
        LocalCacheHitRateTrend = new ObservableCollection<UsageLineChartPoint>();
        LocalHourlyTrend = new ObservableCollection<LocalTelemetryHourlyTrendRowViewModel>();
        LocalSources = new ObservableCollection<LocalTelemetryBreakdownRowViewModel>();
        LocalCliBreakdowns = new ObservableCollection<LocalTelemetryBreakdownRowViewModel>();
        LocalModels = new ObservableCollection<LocalTelemetryBreakdownRowViewModel>();
        LocalRecentActivity = new ObservableCollection<LocalTelemetryRecentActivityRowViewModel>();
        LocalSourceFilters = [];
        LocalCliFilters = [];
        LocalModelFilters = [];
        TrendDayOptions = [1, 7, 30];
    }

    internal void ApplyConnections(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileSelection? selection = null,
        ConnectionProfileRouting? routing = null)
    {
        ArgumentNullException.ThrowIfNull(connections);

        string? previousActiveProfileId = _activeConnectionProfileId;
        _activeConnectionProfileId = ConnectionSourceResolver.ResolveActiveProfileId(
            connections,
            selection,
            routing);
        BackendSourceLabel = "当前后台：本机中转";
        var usedLabels = new HashSet<string>(StringComparer.CurrentCultureIgnoreCase);
        _configuredLocalSourceFilters = connections
            .Where(connection => !string.IsNullOrWhiteSpace(connection.Id))
            .Select(connection =>
            {
                string label = string.IsNullOrWhiteSpace(connection.Name)
                    ? connection.Id
                    : connection.Name.Trim();
                if (!usedLabels.Add(label))
                {
                    label = $"{label} · {connection.Id}";
                    usedLabels.Add(label);
                }

                return new LocalTelemetrySourceFilterOption(
                    label,
                    connection.Id,
                    IsConfigured: true,
                    IsActive: string.Equals(
                        connection.Id,
                        _activeConnectionProfileId,
                        StringComparison.OrdinalIgnoreCase));
            })
            .ToArray();

        RebuildLocalSourceFilters(_activeConnectionProfileId);
        bool activeSourceChanged = !string.Equals(
            previousActiveProfileId,
            _activeConnectionProfileId,
            StringComparison.OrdinalIgnoreCase);
        _ = activeSourceChanged;
    }

    public ObservableCollection<ModelStatsRowViewModel> Models { get; }

    public ObservableCollection<TrendStatsRowViewModel> Trend { get; }

    public ObservableCollection<UsageLineChartPoint> CloudTrend { get; }

    public ObservableCollection<UsageLineChartPoint> LocalTrend { get; }

    public ObservableCollection<UsageLineChartPoint> LocalRequestTrend { get; }

    public ObservableCollection<UsageLineChartPoint> LocalInputTokenTrend { get; }

    public ObservableCollection<UsageLineChartPoint> LocalOutputTokenTrend { get; }

    public ObservableCollection<UsageLineChartPoint> LocalCacheReadTrend { get; }

    public ObservableCollection<UsageLineChartPoint> LocalCacheWriteTrend { get; }

    public ObservableCollection<UsageLineChartPoint> LocalSuccessRateTrend { get; }

    public ObservableCollection<UsageLineChartPoint> LocalLatencyTrend { get; }

    public ObservableCollection<UsageLineChartPoint> LocalCacheHitRateTrend { get; }

    public ObservableCollection<LocalTelemetryHourlyTrendRowViewModel> LocalHourlyTrend { get; }

    public ObservableCollection<LocalTelemetryBreakdownRowViewModel> LocalSources { get; }

    public ObservableCollection<LocalTelemetryBreakdownRowViewModel> LocalCliBreakdowns { get; }

    public ObservableCollection<LocalTelemetryBreakdownRowViewModel> LocalModels { get; }

    public ObservableCollection<LocalTelemetryRecentActivityRowViewModel> LocalRecentActivity { get; }

    public ObservableCollection<LocalTelemetrySourceFilterOption> LocalSourceFilters { get; }

    public ObservableCollection<LocalTelemetryCliFilterOption> LocalCliFilters { get; }

    public ObservableCollection<LocalTelemetryModelFilterOption> LocalModelFilters { get; }

    public IReadOnlyList<int> TrendDayOptions { get; }

    public bool IsTodayRangeSelected => SelectedTrendDays == 1;

    public bool IsSevenDayRangeSelected => SelectedTrendDays == 7;

    public bool IsThirtyDayRangeSelected => SelectedTrendDays == 30;

    public bool CanRefresh => !IsBusy && (IsLocalGatewayAvailable
        ? HasLocalGatewayAuthorization
        : ShowManualCloudCredentialForm);

    public bool CanRefreshLocal => !IsLocalBusy;

    public bool IsLocalStatisticsSelected => false;

    public bool IsCloudStatisticsSelected => true;

    public string PreferredDataSourceLabel => "本机后台";

    public string PreferredDataSourceDetail => CloudDataScope;

    public bool IsLocalCloudConnection => IsLocalGatewayAvailable;

    public bool IsManualCloudConnection => ShowManualCloudCredentialForm;

    public bool HasLocalGatewayAuthorization =>
        HasLocalAdministratorAuthorization || HasLocalUserAuthorization;

    private bool IsSharedSessionForActiveBackend
    {
        get
        {
            return _sub2ApiSessionManager?.Current is { IsAuthenticated: true, ApiBaseUri: not null } state &&
                   SameEndpoint(state.ApiBaseUri, LocalBackendApiBaseUri);
        }
    }

    private bool IsUsingLocalMachineBackend => true;

    public bool RequiresLocalAuthorization =>
        IsLocalGatewayAvailable && !HasLocalGatewayAuthorization;

    public bool ShowLocalAuthorizationEditor =>
        IsLocalGatewayAvailable && IsLocalAuthorizationEditorOpen && !HasLocalGatewayAuthorization;

    public bool ShowAdministratorApiKeyEditor => IsUsingLocalMachineBackend;

    /// <summary>
    /// A broken fixed local profile must never silently turn into an arbitrary
    /// remote endpoint form.  Manual cloud statistics remains available when
    /// the local profile is valid but its backend is offline, or when this
    /// view is hosted without Connection Center (legacy/design-time mode).
    /// </summary>
    public bool ShowManualCloudCredentialForm => false;

    public bool HasLocalGatewayConfigurationIssue => _localGatewayEndpoint.RequiresConfigurationFix;

    public string LocalGatewayConfigurationNotice => _localGatewayEndpoint.Status switch
    {
        LocalGatewayEndpointResolutionStatus.ProfileReadFailed =>
            "暂时无法读取连接中心的“本机中转”配置。请确认连接中心可正常使用后重新检查。",
        LocalGatewayEndpointResolutionStatus.ProfileMissing =>
            "没有找到固定“本机中转”配置。请前往连接中心恢复该固定来源后重新检查。",
        LocalGatewayEndpointResolutionStatus.ProfileInvalid =>
            "“本机中转”配置类型不正确。请在连接中心恢复固定本机来源后重新检查。",
        LocalGatewayEndpointResolutionStatus.ApiAddressMissing =>
            "“本机中转”没有可用 API 地址。请在连接中心填写这台电脑上的后台地址。",
        LocalGatewayEndpointResolutionStatus.ApiAddressNotLocal =>
            "“本机中转”的 API 地址并不属于这台电脑。请在连接中心改为回环地址或本机网卡 IP。",
        _ => string.Empty,
    };

    public bool HasNoModels => HasData && Models.Count == 0;

    public bool HasNoTrend => HasData && Trend.Count == 0;

    public bool HasNoCloudTrend => HasData && CloudTrend.Count == 0;

    public bool HasNoLocalTrend => HasLocalTelemetry && LocalTrend.Count == 0;

    public bool HasNoLocalTelemetry => !HasLocalTelemetry;

    public string PasswordHint => "远程统计密码只用于本次读取，不会写入配置文件。";

    public string RefreshButtonLabel => IsBusy
        ? "正在读取真实用量…"
        : IsLocalGatewayAvailable
            ? HasLocalAdministratorAuthorization
                ? "刷新全站统计"
                : HasLocalUserAuthorization
                    ? "刷新账户统计"
                    : "需要一次性授权"
            : "读取云端统计";

    public async Task InitializeAsync()
    {
        if (Interlocked.Exchange(ref _initialized, 1) != 0)
        {
            return;
        }

        try
        {
            StatsSettings settings = await _controller
                .LoadSettingsAsync(CancellationToken.None)
                .ConfigureAwait(true);
            _storedSettings = CloneWithoutPassword(settings);
            GatewayBaseUrl = _storedSettings.GatewayBaseUrl;
            Email = settings.Email;
            SelectedTrendDays = TrendDayOptions.Contains(settings.TrendDays)
                ? settings.TrendDays
                : 7;
            HasSavedPassword = false;
            HasFailure = false;

            // Older versions placed the password in the legacy settings file.
            // Do not reuse that plaintext value.  Keep the address/email for
            // convenience, clear only the secret, and use safe local grants
            // for future automatic refreshes.
            if (!string.IsNullOrWhiteSpace(settings.Password))
            {
                await _controller
                    .SaveSettingsAsync(_storedSettings, CancellationToken.None)
                    .ConfigureAwait(true);
            }

            await RefreshCloudConnectionModeCoreAsync(autoRefresh: true).ConfigureAwait(true);
        }
        catch (Exception exception)
        {
            SelectedScope = StatisticsScope.Cloud;
            HasFailure = true;
            StatusNotice = $"读取统计设置失败：{Sanitize(exception.Message, string.Empty)}";
        }
    }

    public async Task ActivateAsync()
    {
        await InitializeAsync().ConfigureAwait(true);
        if (IsLocalGatewayAvailable && HasLocalGatewayAuthorization && !IsBusy)
        {
            await RefreshLocalGatewayStatisticsAsync(forceRefresh: false).ConfigureAwait(true);
        }
    }

    [RelayCommand]
    private Task SelectTodayRangeAsync() => SelectDashboardRangeAsync(1);

    [RelayCommand]
    private Task SelectSevenDayRangeAsync() => SelectDashboardRangeAsync(7);

    [RelayCommand]
    private Task SelectThirtyDayRangeAsync() => SelectDashboardRangeAsync(30);

    private async Task SelectDashboardRangeAsync(int days)
    {
        if (!TrendDayOptions.Contains(days) || (IsBusy || IsLocalBusy))
        {
            return;
        }

        bool changed = SelectedTrendDays != days;
        SelectedTrendDays = days;
        StatsSettings updatedSettings = CloneWithoutPassword(_storedSettings);
        updatedSettings.TrendDays = days;
        _storedSettings = updatedSettings;
        try
        {
            await _controller.SaveSettingsAsync(_storedSettings, CancellationToken.None).ConfigureAwait(true);
        }
        catch
        {
            // The selected range remains usable for this session.  A failure to
            // persist a non-sensitive UI preference must not block the dashboard.
        }

        if (!changed)
        {
            return;
        }

        if (IsLocalGatewayAvailable && HasLocalGatewayAuthorization)
        {
            await RefreshLocalGatewayStatisticsAsync(forceRefresh: false).ConfigureAwait(true);
            return;
        }

        SelectedScope = StatisticsScope.Cloud;
    }

    [RelayCommand]
    private async Task RefreshCloudConnectionModeAsync()
    {
        await InitializeAsync().ConfigureAwait(true);
        await RefreshCloudConnectionModeCoreAsync(autoRefresh: true).ConfigureAwait(true);
    }

    [RelayCommand]
    private void BeginLocalAuthorization()
    {
        if (IsLocalGatewayAvailable && !HasLocalGatewayAuthorization)
        {
            IsLocalAuthorizationEditorOpen = true;
        }
    }

    [RelayCommand]
    private void CancelLocalAuthorization()
        => IsLocalAuthorizationEditorOpen = false;

    [RelayCommand]
    private void OpenLocalDashboard()
    {
        Uri? dashboardUri = _localGatewayEndpoint.DashboardUri;
        if (dashboardUri is null)
        {
            StatusNotice = "本机后台地址不可用，请确认 127.0.0.1:8080 已启动。";
            return;
        }

        try
        {
            Process.Start(new ProcessStartInfo(dashboardUri.AbsoluteUri) { UseShellExecute = true });
        }
        catch
        {
            StatusNotice = "无法打开本机后台，请确认 127.0.0.1:8080 已启动。";
        }
    }

    [RelayCommand]
    internal async Task RefreshLocalStatisticsAsync()
    {
        if (!await _localOperationGate.WaitAsync(0).ConfigureAwait(true))
        {
            return;
        }

        IsLocalBusy = true;
        HasLocalTelemetryFailure = false;
        try
        {
            LocalTelemetryRangeSnapshot unfiltered = await _localTelemetryRepository
                .GetRangeSnapshotAsync(SelectedTrendDays, TimeZoneInfo.Local, CancellationToken.None)
                .ConfigureAwait(true);
            UpdateLocalFilterOptions(unfiltered);
            LocalTelemetryQueryFilter filter = CreateLocalFilter();
            LocalTelemetryRangeSnapshot snapshot = filter.IsEmpty
                ? unfiltered
                : await _localTelemetryRepository
                    .GetFilteredRangeSnapshotAsync(SelectedTrendDays, filter, TimeZoneInfo.Local, CancellationToken.None)
                    .ConfigureAwait(true);
            ApplyLocalTelemetryRangeSnapshot(snapshot);
            LocalLastUpdated = $"更新于 {snapshot.GeneratedAt.ToLocalTime():HH:mm:ss}";
        }
        catch (OperationCanceledException)
        {
            LocalStatusNotice = "本地统计读取已取消。";
        }
        catch
        {
            HasLocalTelemetryFailure = true;
            LocalStatusNotice = "本地统计暂时无法读取，请稍后重新刷新。";
        }
        finally
        {
            IsLocalBusy = false;
            _localOperationGate.Release();
        }
    }

    partial void OnSelectedLocalSourceFilterChanged(LocalTelemetrySourceFilterOption? value) => RefreshForFilterChange();

    partial void OnSelectedLocalCliFilterChanged(LocalTelemetryCliFilterOption? value) => RefreshForFilterChange();

    partial void OnSelectedLocalModelFilterChanged(LocalTelemetryModelFilterOption? value) => RefreshForFilterChange();

    private async void RefreshForFilterChange()
    {
        if (_updatingLocalFilters || Volatile.Read(ref _initialized) == 0) return;
        await RefreshLocalStatisticsAsync().ConfigureAwait(true);
    }

    private LocalTelemetryQueryFilter CreateLocalFilter() => new(
        SelectedLocalSourceFilter?.SourceId,
        SelectedLocalCliFilter?.CliKind,
        SelectedLocalModelFilter?.Model);

    private void UpdateLocalFilterOptions(LocalTelemetryRangeSnapshot snapshot)
    {
        _updatingLocalFilters = true;
        try
        {
            string? source = SelectedLocalSourceFilter?.SourceId;
            CliKind? cli = SelectedLocalCliFilter?.CliKind;
            string? model = SelectedLocalModelFilter?.Model;
            _observedLocalSourceFilters = snapshot.BySource
                .Where(item => !string.IsNullOrWhiteSpace(item.SourceId))
                .Select(item => new LocalTelemetrySourceFilterOption(
                    item.SourceLabel ?? item.SourceId!,
                    item.SourceId))
                .DistinctBy(item => item.SourceId, StringComparer.OrdinalIgnoreCase)
                .ToArray();
            ReplaceRows(LocalSourceFilters, BuildLocalSourceFilterOptions());
            ReplaceRows(LocalCliFilters,
                new[] { new LocalTelemetryCliFilterOption("全部客户端", null) }.Concat(
                    snapshot.ByCli.Where(item => item.CliKind is not null)
                        .Select(item => new LocalTelemetryCliFilterOption(WorkspaceDisplay.CliName(item.CliKind!.Value), item.CliKind))));
            ReplaceRows(LocalModelFilters,
                new[] { new LocalTelemetryModelFilterOption("全部模型", null) }.Concat(
                    snapshot.ByModel.Where(item => !string.IsNullOrWhiteSpace(item.Model))
                        .Select(item => new LocalTelemetryModelFilterOption(item.Model!, item.Model))
                        .DistinctBy(item => item.Model, StringComparer.OrdinalIgnoreCase)));

            SelectedLocalSourceFilter = LocalSourceFilters.FirstOrDefault(item =>
                string.Equals(item.SourceId, source, StringComparison.OrdinalIgnoreCase)) ?? LocalSourceFilters[0];
            SelectedLocalCliFilter = LocalCliFilters.FirstOrDefault(item => item.CliKind == cli) ?? LocalCliFilters[0];
            SelectedLocalModelFilter = LocalModelFilters.FirstOrDefault(item =>
                string.Equals(item.Model, model, StringComparison.OrdinalIgnoreCase)) ?? LocalModelFilters[0];
        }
        finally
        {
            _updatingLocalFilters = false;
        }
    }

    private void RebuildLocalSourceFilters(string? preferredSourceId)
    {
        bool wasUpdating = _updatingLocalFilters;
        _updatingLocalFilters = true;
        try
        {
            ReplaceRows(LocalSourceFilters, BuildLocalSourceFilterOptions());
            SelectedLocalSourceFilter = LocalSourceFilters.FirstOrDefault(item =>
                string.Equals(item.SourceId, preferredSourceId, StringComparison.OrdinalIgnoreCase))
                ?? LocalSourceFilters[0];
        }
        finally
        {
            _updatingLocalFilters = wasUpdating;
        }
    }

    private IEnumerable<LocalTelemetrySourceFilterOption> BuildLocalSourceFilterOptions()
        => new[] { new LocalTelemetrySourceFilterOption("全部来源", null) }.Concat(
            _configuredLocalSourceFilters
                .Concat(_observedLocalSourceFilters)
                .DistinctBy(item => item.SourceId, StringComparer.OrdinalIgnoreCase));

    private static string? ResolveActiveConnectionId(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileSelection? selection)
    {
        if (selection is null)
        {
            return null;
        }

        string? candidateId = !string.IsNullOrWhiteSpace(selection.ActiveProfileId)
            ? selection.ActiveProfileId
            : selection.LocalProfileId;
        return connections.Any(connection => string.Equals(
            connection.Id,
            candidateId,
            StringComparison.OrdinalIgnoreCase))
            ? candidateId
            : null;
    }

    /// <summary>
    /// The view passes PasswordBox.Password directly here. The value is never
    /// assigned to an observable property or included in status text.
    /// </summary>
    public async Task<bool> RefreshAsync(string? submittedPassword)
    {
        await InitializeAsync().ConfigureAwait(true);
        if (IsLocalGatewayAvailable)
        {
            return await RefreshLocalGatewayStatisticsAsync(forceRefresh: true).ConfigureAwait(true);
        }

        if (HasLocalGatewayConfigurationIssue)
        {
            StatusNotice = LocalGatewayConfigurationNotice;
            return false;
        }

        if (!await _operationGate.WaitAsync(0).ConfigureAwait(true))
        {
            return false;
        }

        IsBusy = true;
        HasFailure = false;
        StatusNotice = "正在使用本次授权并行读取总览、模型和近期趋势…";
        string effectivePassword = submittedPassword ?? string.Empty;

        try
        {
            string normalizedGateway = NormalizeGatewayBaseUrl(
                string.IsNullOrWhiteSpace(GatewayBaseUrl)
                    ? _storedSettings.GatewayBaseUrl
                    : GatewayBaseUrl);
            string normalizedEmail = Email.Trim();
            if (string.IsNullOrWhiteSpace(normalizedEmail))
            {
                throw new InvalidOperationException("请填写账号邮箱。 ");
            }

            if (string.IsNullOrWhiteSpace(effectivePassword))
            {
                throw new InvalidOperationException("请填写账号密码。 ");
            }

            var settings = new StatsSettings
            {
                GatewayBaseUrl = normalizedGateway,
                Email = normalizedEmail,
                Password = effectivePassword,
                TrendDays = TrendDayOptions.Contains(SelectedTrendDays) ? SelectedTrendDays : 7,
            };

            _storedSettings = CloneWithoutPassword(settings);
            await _controller.SaveSettingsAsync(_storedSettings, CancellationToken.None).ConfigureAwait(true);
            HasSavedPassword = false;
            GatewayBaseUrl = settings.GatewayBaseUrl;
            Email = settings.Email;
            SelectedTrendDays = settings.TrendDays;

            CloudUsageSnapshotCacheResult cached = await _cloudUsageSnapshotCache
                .GetOrLoadAsync(
                    new Uri(normalizedGateway, UriKind.Absolute),
                    $"manual:{normalizedEmail.ToLowerInvariant()}",
                    settings.TrendDays,
                    forceRefresh: true,
                    cancellationToken => _controller.RefreshAsync(settings, cancellationToken),
                    CancellationToken.None)
                .ConfigureAwait(true);
            StatsSnapshot snapshot = cached.Snapshot;
            ApplySnapshot(snapshot);
            HasFailure = false;
            HasData = true;
            LastUpdated = FormatCalibrationLabel(cached);
            StatusNotice = $"本机账户统计已更新：{snapshot.Overview.TotalApiKeys} 个 API Key，累计官方费用 {FormatMoney(snapshot.Overview.TotalCost)}。";
            return true;
        }
        catch (Exception exception)
        {
            SelectedScope = StatisticsScope.Cloud;
            HasFailure = true;
            StatusNotice = $"读取统计失败：{Sanitize(exception.Message, effectivePassword)}";
            return false;
        }
        finally
        {
            effectivePassword = string.Empty;
            IsBusy = false;
            _operationGate.Release();
        }
    }

    /// <summary>
    /// Called by the view with a PasswordBox value.  The administrator API key
    /// never enters a bindable property, settings file, or telemetry record.
    /// </summary>
    public async Task<bool> SaveLocalAdministratorAuthorizationAsync(string? submittedAdministratorApiKey)
    {
        if (!IsLocalGatewayAvailable)
        {
            StatusNotice = "当前没有可用的数据后台，无法保存管理员授权。";
            return false;
        }

        if (!IsUsingLocalMachineBackend)
        {
            StatusNotice = "管理员 API Key 只用于本机后台；局域网和云端后台请使用管理员账号登录。";
            return false;
        }

        LocalGatewayAuthorizationSaveResult saved = _localGatewayAuthorizationStore
            .SaveAdministratorApiKey(submittedAdministratorApiKey ?? string.Empty);
        submittedAdministratorApiKey = string.Empty;
        if (saved != LocalGatewayAuthorizationSaveResult.Saved)
        {
            StatusNotice = saved switch
            {
                LocalGatewayAuthorizationSaveResult.Invalid => "管理员 API Key 格式无效，请从本机后台复制完整访问密钥。",
                _ => "Windows 凭据管理器不可用，无法安全保存本机统计授权。",
            };
            return false;
        }

        RefreshLocalAuthorizationState();
        IsLocalAuthorizationEditorOpen = false;
        StatusNotice = "已安全保存本机管理员授权，正在读取后台完整统计。";
        return await RefreshLocalGatewayStatisticsAsync(forceRefresh: true).ConfigureAwait(true);
    }

    /// <summary>
    /// Performs one interactive local user login, saves only the rotating
    /// refresh token with DPAPI, and immediately reads user-scoped statistics.
    /// The password remains process-local for this one request.
    /// </summary>
    public async Task<bool> AuthorizeLocalUserAsync(string? submittedPassword)
    {
        if (!IsLocalGatewayAvailable)
        {
            StatusNotice = "当前没有可用的数据后台，请先在连接中心配置并选择后台。";
            return false;
        }

        if (!TryGetResolvedLocalGatewayBaseUrl(out string gatewayBaseUrl))
        {
            StatusNotice = LocalGatewayConfigurationNotice;
            return false;
        }

        string email = LocalAuthorizationEmail.Trim();
        string password = submittedPassword ?? string.Empty;
        if (string.IsNullOrWhiteSpace(email) || string.IsNullOrWhiteSpace(password))
        {
            StatusNotice = "请输入当前后台的账户邮箱和密码，授权完成后密码不会保存。";
            return false;
        }

        try
        {
            IsBusy = true;
            HasFailure = false;
            StatusNotice = "正在登录当前后台并建立安全会话…";
            if (_sub2ApiSessionManager is not null)
            {
                var apiBaseUri = new Uri(gatewayBaseUrl, UriKind.Absolute);
                Sub2ApiSessionAccess access = await _sub2ApiSessionManager!
                    .LoginAsync(
                        apiBaseUri,
                        email,
                        password,
                        allowInsecurePublicHttp: false,
                        CancellationToken.None)
                    .ConfigureAwait(true);
                CloudUsageSnapshotCacheResult cached = await LoadSharedCloudSnapshotAsync(
                        gatewayBaseUrl,
                        access,
                        forceRefresh: true,
                        CancellationToken.None)
                    .ConfigureAwait(true);
                RefreshLocalAuthorizationState();
                IsLocalAuthorizationEditorOpen = false;
                ApplySnapshot(cached.Snapshot);
                HasData = true;
                LastUpdated = FormatCalibrationLabel(cached);
                StatusNotice = $"已以{_sub2ApiSessionManager.Current.RoleLabel}身份登录；用量仪表盘将使用此安全会话。";
                return true;
            }

            LocalUserAuthorizationResult result = await _localCloudStatisticsClient
                .AuthorizeUserAsync(gatewayBaseUrl, email, password, SelectedTrendDays, CancellationToken.None)
                .ConfigureAwait(true);
            LocalUserStatsAuthorizationSaveResult saveResult = _localUserStatsAuthorizationStore
                .Save(result.RefreshToken);
            if (saveResult != LocalUserStatsAuthorizationSaveResult.Saved)
            {
                throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.SecureStorageUnavailable);
            }

            _cloudUsageSnapshotCache.Store(
                new Uri(gatewayBaseUrl, UriKind.Absolute),
                "local-user",
                SelectedTrendDays,
                result.Snapshot);

            RefreshLocalAuthorizationState();
            IsLocalAuthorizationEditorOpen = false;
            ApplySnapshot(result.Snapshot);
            HasData = true;
            LastUpdated = $"更新于 {DateTime.Now:HH:mm:ss}";
            StatusNotice = "账户授权已保存到 Windows 当前用户保护区；后续会自动刷新当前后台统计。";
            return true;
        }
        catch (Exception exception)
        {
            SelectedScope = StatisticsScope.Cloud;
            HasFailure = true;
            StatusNotice = DescribeLocalCloudFailure(exception);
            return false;
        }
        finally
        {
            password = string.Empty;
            IsBusy = false;
        }
    }

    private async Task RefreshCloudConnectionModeCoreAsync(bool autoRefresh)
    {
        if (IsCheckingLocalGateway)
        {
            return;
        }

        IsCheckingLocalGateway = true;
        try
        {
            _activeBackendTarget = LocalBackendTarget;
            _localGatewayEndpoint = LocalBackendEndpoint;
            BackendSourceLabel = "当前后台：本机中转";
            NotifyLocalGatewayEndpointChanged();
            LocalGatewayEndpointResolution configuredEndpoint = await _localGatewayEndpointResolver
                .ResolveAsync(CancellationToken.None)
                .ConfigureAwait(true);
            bool hasConfiguredLocalBackend = configuredEndpoint.IsReady ||
                _configuredLocalSourceFilters.Any(option => string.Equals(
                    option.SourceId,
                    ConnectionProfileIds.LocalMachine,
                    StringComparison.OrdinalIgnoreCase));

            GatewayBaseUrl = string.Empty;

            if (_sub2ApiSessionManager is not null)
            {
                bool alreadySignedIn = _sub2ApiSessionManager.Current is
                {
                    IsAuthenticated: true,
                    ApiBaseUri: not null,
                } current && SameEndpoint(current.ApiBaseUri, LocalBackendApiBaseUri);
                if (!alreadySignedIn)
                {
                    await _sub2ApiSessionManager
                        .RestoreAsync(LocalBackendApiBaseUri, CancellationToken.None)
                        .ConfigureAwait(true);
                }
            }

            RefreshLocalAuthorizationState();
            IsLocalGatewayAvailable = hasConfiguredLocalBackend ||
                _sub2ApiSessionManager is not null ||
                HasLocalGatewayAuthorization;
            if (!IsLocalGatewayAvailable)
            {
                SelectedScope = StatisticsScope.Cloud;
                CloudConnectionNotice = "本机后台会话尚未就绪；用量仪表盘不会改用远程来源。";
                CloudAuthorizationNotice = "请完成本机后台授权后读取真实用量。";
                IsLocalAuthorizationEditorOpen = false;
                return;
            }

            CloudConnectionNotice = "用量仪表盘固定读取本机后台。";
            CloudAuthorizationNotice = HasLocalAdministratorAuthorization
                ? "已发现 Windows 凭据管理器/环境变量中的管理员访问密钥，可读取本机后台完整统计。"
                : HasLocalUserAuthorization
                    ? "已发现 Windows 当前用户保护的本机账户授权，可读取该账户自己的真实账本。"
                    : "本机统计接口仍要求授权。程序不会读取 .env、浏览器或明文密码。";
            IsLocalAuthorizationEditorOpen = false;

            if (autoRefresh && HasLocalGatewayAuthorization && !IsBusy)
            {
                // A dashboard authorization/read failure is reported by the
                // refresh method itself.  It must not be mislabeled as a
                // broken Connection Center profile by this discovery pass.
                try
                {
                    await RefreshLocalGatewayStatisticsAsync(forceRefresh: false).ConfigureAwait(true);
                }
                catch (OperationCanceledException)
                {
                    throw;
                }
                catch
                {
                    // RefreshLocalGatewayStatisticsAsync already keeps an
                    // actionable, sanitized status.  Preserve the verified
                    // local endpoint state for the user to retry.
                }
            }
            else if (!HasLocalGatewayAuthorization)
            {
                SelectedScope = StatisticsScope.Cloud;
            }
        }
        catch (OperationCanceledException)
        {
            throw;
        }
        catch
        {
            SelectedScope = StatisticsScope.Cloud;
            _activeBackendTarget = LocalBackendTarget;
            _localGatewayEndpoint = LocalBackendEndpoint;
            BackendSourceLabel = "当前后台：本机中转";
            NotifyLocalGatewayEndpointChanged();
            IsLocalGatewayAvailable = false;
            GatewayBaseUrl = string.Empty;
            CloudConnectionNotice = "本机后台暂时无法读取；用量仪表盘不会改用远程来源。";
            CloudAuthorizationNotice = "请确认本机后台已启动并监听 127.0.0.1:8080。";
            IsLocalAuthorizationEditorOpen = false;
        }
        finally
        {
            IsCheckingLocalGateway = false;
        }
    }

    private async Task<bool> RefreshLocalGatewayStatisticsAsync(bool forceRefresh)
    {
        if (!TryGetResolvedLocalGatewayBaseUrl(out string gatewayBaseUrl))
        {
            SelectedScope = StatisticsScope.Cloud;
            StatusNotice = LocalGatewayConfigurationNotice;
            return false;
        }

        if (!HasLocalGatewayAuthorization)
        {
            SelectedScope = StatisticsScope.Cloud;
            IsLocalAuthorizationEditorOpen = true;
            StatusNotice = "本机后台已识别，但尚未完成统计授权。请进行一次性授权后再读取。";
            return false;
        }

        if (!await _operationGate.WaitAsync(0).ConfigureAwait(true))
        {
            return false;
        }

        IsBusy = true;
        HasFailure = false;
        StatusNotice = "正在读取本机后台的真实统计…";
        try
        {
            var apiBaseUri = new Uri(gatewayBaseUrl, UriKind.Absolute);
            CloudUsageSnapshotCacheResult cached;
            if (IsUsingLocalMachineBackend &&
                HasLocalAdministratorAuthorization &&
                _administratorAuthorization.AdministratorApiKey is { Length: > 0 } administratorApiKey)
            {
                cached = await _cloudUsageSnapshotCache
                    .GetOrLoadAsync(
                        apiBaseUri,
                        "local-admin",
                        SelectedTrendDays,
                        forceRefresh,
                        cancellationToken => _localCloudStatisticsClient.RefreshAdministratorAsync(
                            gatewayBaseUrl,
                            administratorApiKey,
                            SelectedTrendDays,
                            cancellationToken),
                        CancellationToken.None)
                    .ConfigureAwait(true);
            }
            else if (IsSharedSessionForActiveBackend)
            {
                Sub2ApiSessionAccess access = await _sub2ApiSessionManager!
                    .GetAccessAsync(apiBaseUri, CancellationToken.None)
                    .ConfigureAwait(true);
                cached = await LoadSharedCloudSnapshotAsync(
                        gatewayBaseUrl,
                        access,
                        forceRefresh,
                        CancellationToken.None)
                    .ConfigureAwait(true);
                RefreshLocalAuthorizationState();
            }
            else if (IsUsingLocalMachineBackend &&
                     HasLocalUserAuthorization &&
                     _userAuthorization.RefreshToken is { Length: > 0 } refreshToken)
            {
                cached = await _cloudUsageSnapshotCache
                    .GetOrLoadAsync(
                        apiBaseUri,
                        "local-user",
                        SelectedTrendDays,
                        forceRefresh,
                        async cancellationToken =>
                        {
                            LocalUserRefreshResult refreshResult = await _localCloudStatisticsClient
                                .RefreshUserAsync(gatewayBaseUrl, refreshToken, SelectedTrendDays, cancellationToken)
                                .ConfigureAwait(false);
                            if (_localUserStatsAuthorizationStore.Save(refreshResult.RefreshToken) !=
                                LocalUserStatsAuthorizationSaveResult.Saved)
                            {
                                throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.SecureStorageUnavailable);
                            }

                            return refreshResult.Snapshot;
                        },
                        CancellationToken.None)
                    .ConfigureAwait(true);
                RefreshLocalAuthorizationState();
            }
            else
            {
                throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.AuthorizationUnavailable);
            }

            StatsSnapshot snapshot = cached.Snapshot;
            ApplySnapshot(snapshot);
            HasData = true;
            LastUpdated = FormatCalibrationLabel(cached);
            StatusNotice = cached.WasCached
                ? $"正在使用 {cached.CalibratedAtUtc.ToLocalTime():HH:mm:ss} 的云端校准快照；本地账本已更新，10 分钟后再向后台校准。"
                : snapshot.Scope == CloudStatisticsScope.LocalAdministrator
                    ? "后台完整统计已重新校准；范围为具有管理员权限的全站聚合。"
                    : "本机后台账户统计已重新校准。";
            return true;
        }
        catch (Exception exception)
        {
            SelectedScope = StatisticsScope.Cloud;
            HasFailure = true;
            StatusNotice = DescribeLocalCloudFailure(exception);
            return false;
        }
        finally
        {
            IsBusy = false;
            _operationGate.Release();
        }
    }

    private Task<CloudUsageSnapshotCacheResult> LoadSharedCloudSnapshotAsync(
        string gatewayBaseUrl,
        Sub2ApiSessionAccess access,
        bool forceRefresh,
        CancellationToken cancellationToken)
        => _cloudUsageSnapshotCache.GetOrLoadAsync(
            access.ApiBaseUri,
            $"user:{access.UserId}:{(access.IsAdministrator ? "admin" : "user")}",
            SelectedTrendDays,
            forceRefresh,
            token => _localCloudStatisticsClient.RefreshWithAccessTokenAsync(
                gatewayBaseUrl,
                access.AccessToken,
                SelectedTrendDays,
                access.IsAdministrator,
                token),
            cancellationToken);

    private static string FormatCalibrationLabel(CloudUsageSnapshotCacheResult cached)
        => cached.WasCached
            ? $"云端校准于 {cached.CalibratedAtUtc.ToLocalTime():HH:mm:ss}"
            : $"刚刚校准于 {cached.CalibratedAtUtc.ToLocalTime():HH:mm:ss}";

    private void RefreshLocalAuthorizationState()
    {
        _administratorAuthorization = _localGatewayAuthorizationStore.GetCurrentAuthorization();
        _userAuthorization = _localUserStatsAuthorizationStore.GetCurrent();
        HasLocalAdministratorAuthorization = IsUsingLocalMachineBackend && _administratorAuthorization.IsAvailable;
        HasLocalUserAuthorization = IsSharedSessionForActiveBackend ||
                                    IsUsingLocalMachineBackend && _userAuthorization.IsAvailable;
    }

    private void OnSharedSessionChanged(object? sender, EventArgs args)
    {
        void Apply()
        {
            _cloudUsageSnapshotCache.Clear();
            RefreshLocalAuthorizationState();
            if (_sub2ApiSessionManager?.Current.IsAuthenticated == true)
            {
                CloudAuthorizationNotice = $"已以{_sub2ApiSessionManager.Current.RoleLabel}身份登录；服务控制和统计页共享此会话。";
                IsLocalAuthorizationEditorOpen = false;
                if (Volatile.Read(ref _initialized) != 0 && !IsBusy)
                {
                    _ = RefreshCloudConnectionModeCoreAsync(autoRefresh: true);
                }
            }
            else
            {
                SelectedScope = StatisticsScope.Cloud;
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

    private bool TryGetResolvedLocalGatewayBaseUrl(out string gatewayBaseUrl)
    {
        gatewayBaseUrl = LocalBackendApiBaseUri.AbsoluteUri;
        return true;
    }

    private static bool SameEndpoint(Uri left, Uri right)
        => Uri.Compare(
            left,
            right,
            UriComponents.SchemeAndServer | UriComponents.Path,
            UriFormat.SafeUnescaped,
            StringComparison.OrdinalIgnoreCase) == 0;

    private void NotifyLocalGatewayEndpointChanged()
    {
        OnPropertyChanged(nameof(ShowManualCloudCredentialForm));
        OnPropertyChanged(nameof(IsManualCloudConnection));
        OnPropertyChanged(nameof(HasLocalGatewayConfigurationIssue));
        OnPropertyChanged(nameof(LocalGatewayConfigurationNotice));
        OnPropertyChanged(nameof(ShowAdministratorApiKeyEditor));
        OnPropertyChanged(nameof(CanRefresh));
    }

    private static StatsSettings CloneWithoutPassword(StatsSettings settings)
        => new()
        {
            GatewayBaseUrl = settings.GatewayBaseUrl,
            Email = settings.Email,
            Password = string.Empty,
            TrendDays = settings.TrendDays,
        };

    private static string DescribeLocalCloudFailure(Exception exception)
        => exception switch
        {
            LocalCloudStatisticsException { Failure: LocalCloudStatisticsFailure.AuthorizationUnavailable } =>
                "当前后台没有可用的安全授权，请登录后重试。",
            LocalCloudStatisticsException { Failure: LocalCloudStatisticsFailure.Unauthorized } =>
                "当前后台拒绝了授权，请清除登录后重新设置。",
            LocalCloudStatisticsException { Failure: LocalCloudStatisticsFailure.Forbidden } =>
                "当前授权没有读取此统计范围的权限。管理员密钥可读取全站统计；普通账户仅能读取自己的账本。",
            LocalCloudStatisticsException { Failure: LocalCloudStatisticsFailure.ComplianceRequired } =>
                "当前后台要求管理员先完成合规确认，请打开后台处理后重试。",
            LocalCloudStatisticsException { Failure: LocalCloudStatisticsFailure.RequiresTwoFactor } =>
                "该账户开启了两步验证，请在后台完成登录验证后重试。",
            LocalCloudStatisticsException { Failure: LocalCloudStatisticsFailure.SecureStorageUnavailable } =>
                "Windows 当前用户保护区不可用，无法安全保存授权；密码和令牌不会写入设置文件。",
            LocalCloudStatisticsException { Failure: LocalCloudStatisticsFailure.GatewayUnavailable } =>
                "当前后台暂时不可访问，请检查网络和后台状态。",
            Sub2ApiSessionException { Failure: Sub2ApiSessionFailure.InvalidCredentials } =>
                "账户或密码不正确，请检查后重试。",
            Sub2ApiSessionException { Failure: Sub2ApiSessionFailure.RequiresTwoFactor } =>
                "该账户开启了两步验证，请先在后台完成登录验证。",
            Sub2ApiSessionException { Failure: Sub2ApiSessionFailure.Forbidden } =>
                "当前账户没有访问此服务的权限。",
            Sub2ApiSessionException { Failure: Sub2ApiSessionFailure.ComplianceRequired } =>
                "后台要求先完成合规确认，请打开后台处理后重试。",
            Sub2ApiSessionException { Failure: Sub2ApiSessionFailure.SecureStorageUnavailable } =>
                "Windows 当前用户保护区不可用，无法安全保存登录。",
            Sub2ApiSessionException { Failure: Sub2ApiSessionFailure.GatewayUnavailable } =>
                "当前后台暂时不可访问，请检查网络和后台状态。",
            _ => "后台统计暂时无法读取，请检查授权、地址和网络状态。",
        };

    private void ApplySnapshot(StatsSnapshot snapshot)
    {
        SelectedScope = StatisticsScope.Cloud;
        StatsOverview overview = snapshot.Overview;
        const string backendLabel = "本机后台";
        CloudDataScope = snapshot.Scope == CloudStatisticsScope.LocalAdministrator
            ? $"{backendLabel} · 管理员全站聚合"
            : $"{backendLabel} · 当前账户真实账本";
        TotalRequests = FormatCount(overview.TotalRequests);
        TodayRequests = FormatCount(overview.TodayRequests);
        TotalTokens = FormatCount(overview.TotalTokens);
        TodayTokens = FormatCount(overview.TodayTokens);
        TotalCost = FormatMoney(overview.TotalCost);
        TodayCost = FormatMoney(overview.TodayCost);
        ActualCost = FormatMoney(overview.TotalActualCost);
        CacheReadTokens = FormatCount(overview.TotalCacheReadTokens);
        CacheCreationTokens = FormatCount(overview.TotalCacheCreationTokens);
        AverageDuration = overview.AverageDurationMs <= 0
            ? "—"
            : $"{overview.AverageDurationMs / 1000d:N1}s";
        ApiKeySummary = $"{overview.ActiveApiKeys}/{overview.TotalApiKeys} 活跃";
        ThroughputSummary = $"{overview.Rpm:N1} RPM  ·  {FormatCount((long)overview.Tpm)} TPM";

        // The first row of the cloud dashboard always represents the selected
        // range.  Lifetime values remain available only as a clearly labelled
        // reference, never as a substitute for the current period.
        UsageRangeOverview range = snapshot.RangeOverview ?? UsageRangeOverview.FromTrend(snapshot.Trend);
        CloudRangeRequests = FormatCount(range.TotalRequests);
        CloudRangeTokens = FormatCount(range.TotalTokens);
        CloudRangeActualCost = FormatMoney(range.TotalCost);
        CloudRangeAverageLatency = range.AverageDurationMs > 0
            ? $"{range.AverageDurationMs / 1000d:N1}s"
            : "—";
        CloudRangeInputTokens = FormatCount(range.TotalInputTokens);
        CloudRangeOutputTokens = FormatCount(range.TotalOutputTokens);
        CloudRangeCacheReadTokens = FormatCount(range.TotalCacheReadTokens);
        CloudRangeCacheCreationTokens = FormatCount(range.TotalCacheCreationTokens);
        double cacheDenominator = range.TotalInputTokens + range.TotalCacheReadTokens + range.TotalCacheCreationTokens;
        CloudRangeCacheHitRate = cacheDenominator > 0
            ? FormatPercent(range.TotalCacheReadTokens * 100d / cacheDenominator)
            : "—";
        CloudLifetimeSummary = $"历史累计 · {FormatCount(overview.TotalRequests)} 次请求 · {FormatCount(overview.TotalTokens)} Token · {FormatMoney(overview.TotalCost)} 官方费用";

        Models.Clear();
        foreach (ModelStat model in snapshot.Models
                     .OrderByDescending(model => model.Cost)
                     .ThenByDescending(model => model.Requests)
                     .ThenBy(model => model.Model, StringComparer.OrdinalIgnoreCase))
        {
            Models.Add(new ModelStatsRowViewModel(model));
        }

        TrendPoint[] orderedTrend = snapshot.Trend
            .OrderBy(point => point.Date, StringComparer.OrdinalIgnoreCase)
            .ToArray();
        IReadOnlyList<TrendPoint> chartTrend = BuildCloudChartTrend(
            orderedTrend,
            snapshot.Range,
            SelectedTrendDays);
        long maximumTokens = Math.Max(1, orderedTrend.Select(point => point.TotalTokens).DefaultIfEmpty(0).Max());
        Trend.Clear();
        foreach (TrendPoint point in orderedTrend)
        {
            Trend.Add(new TrendStatsRowViewModel(point, maximumTokens));
        }

        CloudTrend.Clear();
        foreach (TrendPoint point in chartTrend)
        {
            string requests = FormatCount(point.Requests);
            string tokens = FormatCount(point.TotalTokens);
            CloudTrend.Add(new UsageLineChartPoint(
                FormatChartDate(point.Date),
                point.TotalTokens,
                $"{requests} 次请求 · {tokens} Token · {FormatMoney(point.Cost)} 官方费用"));
        }

        long trendRequests = orderedTrend.Sum(point => point.Requests);
        long trendTokens = orderedTrend.Sum(point => point.TotalTokens);
        double trendCost = orderedTrend.Sum(point => point.Cost);
        TrendSummary = orderedTrend.Length == 0
            ? $"{DashboardRangeLabel}暂无用量"
            : $"{DashboardRangeLabel} · {FormatCount(trendRequests)} 次请求 · {FormatCount(trendTokens)} Token · {FormatMoney(trendCost)} 官方费用";
        RecentTrendText = orderedTrend.Length == 0
            ? "近期没有返回可展示的趋势数据。"
            : string.Join(
                Environment.NewLine,
                orderedTrend.TakeLast(10).Reverse().Select(point =>
                    $"{point.Date,-12}  {FormatCount(point.Requests),8} 请求  {FormatCount(point.TotalTokens),10} Token  {FormatMoney(point.Cost),9}"));

        OnPropertyChanged(nameof(HasNoModels));
        OnPropertyChanged(nameof(HasNoTrend));
        OnPropertyChanged(nameof(HasNoCloudTrend));
    }

    /// <summary>
    /// Compatibility entry point used by older callers and focused tests.  The
    /// dashboard itself always uses the range-aware overload below, so every
    /// visible local panel shares the same calendar window.
    /// </summary>
    internal void ApplyLocalTelemetrySnapshot(LocalTelemetrySnapshot snapshot)
    {
        ArgumentNullException.ThrowIfNull(snapshot);
        ApplyLocalTelemetryRangeSnapshot(LocalTelemetryRangeSnapshot.FromLegacy(snapshot, 7));

        // The legacy snapshot carries a dedicated today aggregate even when a
        // lightweight caller supplies an abbreviated daily series.  Preserve
        // that contract for older hosts; the current dashboard binds to the
        // explicit LocalRange* values above.
        LocalTodayTokens = FormatCount(RecordedTotalTokens(snapshot.Today));
        LocalTodayRequests = FormatCount(snapshot.Today.RequestCount);
    }

    /// <summary>
    /// Applies one inclusive local-calendar range to the complete local
    /// dashboard.  No panel intentionally falls back to all-time or a fixed
    /// seven-day aggregate after the user has selected another period.
    /// </summary>
    internal void ApplyLocalTelemetryRangeSnapshot(LocalTelemetryRangeSnapshot snapshot)
    {
        ArgumentNullException.ThrowIfNull(snapshot);

        LocalTelemetryUsageSummary usage = snapshot.Usage;
        LocalTelemetryDailyUsage[] dailyUsage = snapshot.DailyUsage
            .OrderBy(item => item.Date)
            .ToArray();
        LocalTelemetryUsageSummary today = dailyUsage.Length > 0
            ? dailyUsage[^1].Usage
            : LocalTelemetryUsageSummary.Empty;
        HasLocalTelemetry = usage.RequestCount > 0 || RecordedTotalTokens(usage) > 0;

        // Keep the legacy bindings meaningful for older hosts, while the new
        // dashboard cards bind to the LocalRange* fields below.
        LocalTodayTokens = FormatCount(RecordedTotalTokens(today));
        LocalTodayRequests = FormatCount(today.RequestCount);
        LocalSevenDayTokens = FormatCount(RecordedTotalTokens(usage));
        LocalSevenDayRequests = FormatCount(usage.RequestCount);
        LocalSuccessRate = FormatPercent(usage.SuccessRatePercent);
        LocalAverageLatency = FormatLatency(usage.AverageLatencyMilliseconds);
        LocalInputTokens = FormatCount(usage.InputTokens);
        LocalOutputTokens = FormatCount(usage.OutputTokens);
        LocalCachedInputTokens = FormatCount(usage.CachedInputTokens);
        LocalCacheCreationTokens = FormatCount(usage.CacheCreationTokens);
        LocalCacheHitRate = FormatPercent(usage.CacheHitRatePercent);
        LocalSuccessfulRequests = FormatCount(usage.SuccessfulRequestCount);
        LocalFailedRequests = FormatCount(usage.FailedRequestCount);

        LocalRangeRequests = FormatCount(usage.RequestCount);
        LocalRangeTokens = FormatCount(RecordedTotalTokens(usage));
        LocalRangeSuccessRate = FormatPercent(usage.SuccessRatePercent);
        LocalRangeAverageLatency = FormatLatency(usage.AverageLatencyMilliseconds);
        LocalRangeCacheReadTokens = FormatCount(usage.CachedInputTokens);
        LocalRangeCacheCreationTokens = FormatCount(usage.CacheCreationTokens);
        LocalRangeCacheHitRate = FormatPercent(usage.CacheHitRatePercent);

        IReadOnlyList<(string Label, LocalTelemetryUsageSummary Usage)> chartBuckets =
            snapshot.Days == 1 && snapshot.RecentHourlyUsage.Count > 0
                ? snapshot.RecentHourlyUsage
                    .OrderBy(item => item.HourStart)
                    .Select(item => (
                        item.HourStart.ToLocalTime().ToString("HH:mm", CultureInfo.CurrentCulture),
                        item.Usage))
                    .ToArray()
                : dailyUsage
                    .Select(day => (
                        day.Date.ToString("M/d", CultureInfo.CurrentCulture),
                        day.Usage))
                    .ToArray();
        ReplaceLocalUsageChartSeries(chartBuckets);

        LocalTelemetryHourlyUsage[] activeHours = snapshot.RecentHourlyUsage
            .Where(item => item.Usage.RequestCount > 0 || RecordedTotalTokens(item.Usage) > 0)
            .OrderBy(item => item.HourStart)
            .ToArray();
        long maximumHourlyTokens = Math.Max(
            1,
            activeHours.Select(item => RecordedTotalTokens(item.Usage)).DefaultIfEmpty(0).Max());
        LocalHourlyTrend.Clear();
        foreach (LocalTelemetryHourlyUsage hour in activeHours)
        {
            LocalHourlyTrend.Add(new LocalTelemetryHourlyTrendRowViewModel(hour, maximumHourlyTokens));
        }

        ReplaceRows(
            LocalSources,
            snapshot.BySource.Select(row => new LocalTelemetryBreakdownRowViewModel(row, LocalTelemetryBreakdownKind.Source)));
        ReplaceRows(
            LocalCliBreakdowns,
            snapshot.ByCli.Select(row => new LocalTelemetryBreakdownRowViewModel(row, LocalTelemetryBreakdownKind.Cli)));
        ReplaceRows(
            LocalModels,
            snapshot.ByModel.Select(row => new LocalTelemetryBreakdownRowViewModel(row, LocalTelemetryBreakdownKind.Model)));
        ReplaceRows(
            LocalRecentActivity,
            snapshot.RecentActivity.Select(row => new LocalTelemetryRecentActivityRowViewModel(row)));

        LocalTrendSummary = HasLocalTelemetry
            ? $"{DashboardRangeLabel} · {FormatCount(usage.RequestCount)} 次请求 · {FormatCount(RecordedTotalTokens(usage))} 已记录 Token（含缓存）"
            : $"{DashboardRangeLabel}尚无本工作台发起的对话记录";
        LocalHourlyTrendSummary = LocalHourlyTrend.Count > 0
            ? $"近 24 小时 · {FormatCount(activeHours.Sum(item => item.Usage.RequestCount))} 次已记录请求"
            : "近 24 小时尚无本工作台发起的会话记录";
        LocalStatusNotice = HasLocalTelemetry
            ? $"本地仪表盘已按“{DashboardRangeLabel}”汇总。只统计工作台发起且可记录的会话；不会保存提示词、回复、密钥、完整地址、项目路径或会话 ID。"
            : $"{DashboardRangeLabel}还没有本地记录。开始一次工作台对话后，这里会显示请求、Token、缓存、成功率与耗时曲线。";

        OnPropertyChanged(nameof(HasNoLocalTrend));
    }

    private void ReplaceLocalUsageChartSeries(
        IReadOnlyList<(string Label, LocalTelemetryUsageSummary Usage)> buckets)
    {
        LocalTrend.Clear();
        LocalRequestTrend.Clear();
        LocalInputTokenTrend.Clear();
        LocalOutputTokenTrend.Clear();
        LocalCacheReadTrend.Clear();
        LocalCacheWriteTrend.Clear();
        LocalSuccessRateTrend.Clear();
        LocalLatencyTrend.Clear();
        LocalCacheHitRateTrend.Clear();

        foreach ((string label, LocalTelemetryUsageSummary usage) in buckets)
        {
            long recordedTokens = RecordedTotalTokens(usage);
            LocalTrend.Add(new UsageLineChartPoint(
                label,
                recordedTokens,
                $"{FormatCount(usage.RequestCount)} 次请求 · {FormatCount(recordedTokens)} 已记录 Token"));
            LocalRequestTrend.Add(new UsageLineChartPoint(
                label,
                usage.RequestCount,
                $"成功 {FormatCount(usage.SuccessfulRequestCount)} · 失败 {FormatCount(usage.FailedRequestCount)}"));
            LocalInputTokenTrend.Add(new UsageLineChartPoint(
                label,
                usage.InputTokens,
                $"{FormatCount(usage.InputTokens)} 输入 Token"));
            LocalOutputTokenTrend.Add(new UsageLineChartPoint(
                label,
                usage.OutputTokens,
                $"{FormatCount(usage.OutputTokens)} 输出 Token"));
            LocalCacheReadTrend.Add(new UsageLineChartPoint(
                label,
                usage.CachedInputTokens,
                $"{FormatCount(usage.CachedInputTokens)} 缓存读取"));
            LocalCacheWriteTrend.Add(new UsageLineChartPoint(
                label,
                usage.CacheCreationTokens,
                $"{FormatCount(usage.CacheCreationTokens)} 缓存写入"));
            LocalSuccessRateTrend.Add(new UsageLineChartPoint(
                label,
                usage.SuccessRatePercent ?? 0d,
                $"请求成功率 {FormatPercent(usage.SuccessRatePercent)}"));
            LocalLatencyTrend.Add(new UsageLineChartPoint(
                label,
                usage.AverageLatencyMilliseconds ?? 0d,
                $"平均耗时 {FormatLatency(usage.AverageLatencyMilliseconds)}"));
            LocalCacheHitRateTrend.Add(new UsageLineChartPoint(
                label,
                usage.CacheHitRatePercent ?? 0d,
                $"缓存命中率 {FormatPercent(usage.CacheHitRatePercent)}"));
        }
    }

    internal static long RecordedTotalTokens(LocalTelemetryUsageSummary usage)
    {
        ArgumentNullException.ThrowIfNull(usage);
        return usage.InputTokens + usage.OutputTokens + usage.CachedInputTokens + usage.CacheCreationTokens;
    }

    private static void ReplaceRows<T>(ObservableCollection<T> target, IEnumerable<T> rows)
    {
        target.Clear();
        foreach (T row in rows)
        {
            target.Add(row);
        }
    }

    private static string NormalizeGatewayBaseUrl(string value)
    {
        string candidate = value.Trim();
        if (string.IsNullOrWhiteSpace(candidate))
        {
            throw new InvalidOperationException("请填写中转地址。 ");
        }

        if (!candidate.Contains("://", StringComparison.Ordinal))
        {
            candidate = "http://" + candidate;
        }

        if (!Uri.TryCreate(candidate, UriKind.Absolute, out Uri? uri) ||
            uri.Scheme is not ("http" or "https") ||
            string.IsNullOrWhiteSpace(uri.Host))
        {
            throw new InvalidOperationException("中转地址格式无效，请填写 http:// 或 https:// 地址。 ");
        }

        string port = uri.IsDefaultPort ? string.Empty : $":{uri.Port}";
        return $"{uri.Scheme}://{uri.Host}{port}";
    }

    private static string FormatChartDate(string? value)
    {
        if (DateTime.TryParseExact(
                value,
                "yyyy-MM-dd HH:mm",
                CultureInfo.InvariantCulture,
                DateTimeStyles.None,
                out DateTime hour))
        {
            return hour.ToString("HH:mm", CultureInfo.InvariantCulture);
        }

        return DateOnly.TryParseExact(
                value,
                "yyyy-MM-dd",
                CultureInfo.InvariantCulture,
                DateTimeStyles.None,
                out DateOnly date)
            ? date.ToString("M/d", CultureInfo.CurrentCulture)
            : value ?? string.Empty;
    }

    private static IReadOnlyList<TrendPoint> BuildCloudChartTrend(
        IReadOnlyList<TrendPoint> orderedTrend,
        CloudUsageDateRange? range,
        int selectedDays)
    {
        if ((range?.Days ?? selectedDays) != 1)
        {
            return orderedTrend;
        }

        DateOnly day = range?.EndDate ?? DateOnly.FromDateTime(DateTime.Now);
        var pointsByHour = new Dictionary<int, TrendPoint>();
        foreach (TrendPoint point in orderedTrend)
        {
            if (DateTime.TryParseExact(
                    point.Date,
                    "yyyy-MM-dd HH:mm",
                    CultureInfo.InvariantCulture,
                    DateTimeStyles.None,
                    out DateTime timestamp) &&
                DateOnly.FromDateTime(timestamp) == day)
            {
                pointsByHour[timestamp.Hour] = point;
            }
        }

        return Enumerable.Range(0, 24)
            .Select(hour => pointsByHour.GetValueOrDefault(hour) ?? new TrendPoint
            {
                Date = $"{day:yyyy-MM-dd} {hour:00}:00",
            })
            .ToArray();
    }

    internal static string FormatCount(long value)
    {
        if (Math.Abs(value) >= 100_000_000)
        {
            return $"{value / 100_000_000d:N2}亿";
        }

        if (Math.Abs(value) >= 10_000)
        {
            return $"{value / 10_000d:N2}万";
        }

        return value.ToString("N0", CultureInfo.CurrentCulture);
    }

    internal static string FormatMoney(double value)
        => $"${value:N2}";

    internal static string FormatPercent(double? value)
        => value is null ? "—" : $"{value.Value:N1}%";

    internal static string FormatLatency(double? value)
        => value is null ? "—" : $"{value.Value:N0} ms";

    private static string Sanitize(string? message, string password)
    {
        string safe = string.IsNullOrWhiteSpace(message) ? "未知错误" : message.Trim();
        if (!string.IsNullOrEmpty(password))
        {
            safe = safe.Replace(password, "<已隐藏>", StringComparison.Ordinal);
        }

        return Regex.Replace(
            safe,
            @"(?i)(password|passwd|pwd|authorization|access[_ -]?token)\s*[:=]\s*[^\s,;]+",
            "$1=<已隐藏>",
            RegexOptions.CultureInvariant,
            TimeSpan.FromMilliseconds(100));
    }

    partial void OnIsBusyChanged(bool value)
    {
        OnPropertyChanged(nameof(CanRefresh));
        OnPropertyChanged(nameof(RefreshButtonLabel));
    }

    partial void OnHasSavedPasswordChanged(bool value)
        => OnPropertyChanged(nameof(PasswordHint));

    partial void OnHasDataChanged(bool value)
    {
        OnPropertyChanged(nameof(HasNoModels));
        OnPropertyChanged(nameof(HasNoTrend));
        OnPropertyChanged(nameof(HasNoCloudTrend));
    }

    partial void OnIsLocalBusyChanged(bool value)
        => OnPropertyChanged(nameof(CanRefreshLocal));

    partial void OnSelectedTrendDaysChanged(int value)
    {
        (DashboardRangeLabel, DashboardRangeCaption) = value switch
        {
            1 => ("今天", "仅统计今天"),
            30 => ("近 30 天", "今天至过去 29 天"),
            _ => ("近 7 天", "今天至过去 6 天"),
        };
        OnPropertyChanged(nameof(IsTodayRangeSelected));
        OnPropertyChanged(nameof(IsSevenDayRangeSelected));
        OnPropertyChanged(nameof(IsThirtyDayRangeSelected));
    }

    partial void OnSelectedScopeChanged(StatisticsScope value)
    {
        OnPropertyChanged(nameof(IsLocalStatisticsSelected));
        OnPropertyChanged(nameof(IsCloudStatisticsSelected));
        OnPropertyChanged(nameof(PreferredDataSourceLabel));
        OnPropertyChanged(nameof(PreferredDataSourceDetail));
    }

    partial void OnCloudDataScopeChanged(string value)
        => OnPropertyChanged(nameof(PreferredDataSourceDetail));

    partial void OnIsLocalGatewayAvailableChanged(bool value)
    {
        OnPropertyChanged(nameof(IsLocalCloudConnection));
        OnPropertyChanged(nameof(IsManualCloudConnection));
        OnPropertyChanged(nameof(RequiresLocalAuthorization));
        OnPropertyChanged(nameof(ShowLocalAuthorizationEditor));
        OnPropertyChanged(nameof(ShowManualCloudCredentialForm));
        OnPropertyChanged(nameof(CanRefresh));
        OnPropertyChanged(nameof(RefreshButtonLabel));
    }

    partial void OnIsLocalAuthorizationEditorOpenChanged(bool value)
        => OnPropertyChanged(nameof(ShowLocalAuthorizationEditor));

    partial void OnHasLocalAdministratorAuthorizationChanged(bool value)
        => NotifyLocalAuthorizationStateChanged();

    partial void OnHasLocalUserAuthorizationChanged(bool value)
        => NotifyLocalAuthorizationStateChanged();

    private void NotifyLocalAuthorizationStateChanged()
    {
        OnPropertyChanged(nameof(HasLocalGatewayAuthorization));
        OnPropertyChanged(nameof(RequiresLocalAuthorization));
        OnPropertyChanged(nameof(ShowLocalAuthorizationEditor));
        OnPropertyChanged(nameof(CanRefresh));
        OnPropertyChanged(nameof(RefreshButtonLabel));
    }

    partial void OnHasLocalTelemetryChanged(bool value)
    {
        OnPropertyChanged(nameof(HasNoLocalTrend));
        OnPropertyChanged(nameof(HasNoLocalTelemetry));
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

public enum StatisticsScope
{
    Local,
    Cloud,
}

public sealed class LocalTelemetryHourlyTrendRowViewModel
{
    internal LocalTelemetryHourlyTrendRowViewModel(LocalTelemetryHourlyUsage hourlyUsage, long maximumTokens)
    {
        ArgumentNullException.ThrowIfNull(hourlyUsage);
        Hour = hourlyUsage.HourStart.ToLocalTime().ToString("HH:mm", CultureInfo.CurrentCulture);
        Requests = StatsViewModel.FormatCount(hourlyUsage.Usage.RequestCount);
        long recordedTokens = StatsViewModel.RecordedTotalTokens(hourlyUsage.Usage);
        Tokens = StatsViewModel.FormatCount(recordedTokens);
        Detail = $"{Requests} 次 · {Tokens} Token";
        BarWidth = Math.Clamp(
            recordedTokens / (double)Math.Max(1, maximumTokens) * 190d,
            4d,
            190d);
    }

    public string Hour { get; }

    public string Requests { get; }

    public string Tokens { get; }

    public string Detail { get; }

    public double BarWidth { get; }
}

public enum LocalTelemetryBreakdownKind
{
    Source,
    Cli,
    Model,
}

public sealed class LocalTelemetryBreakdownRowViewModel
{
    internal LocalTelemetryBreakdownRowViewModel(
        LocalTelemetryUsageBreakdown breakdown,
        LocalTelemetryBreakdownKind kind)
    {
        ArgumentNullException.ThrowIfNull(breakdown);
        LocalTelemetryUsageSummary usage = breakdown.Usage;
        Label = kind switch
        {
            LocalTelemetryBreakdownKind.Source => string.IsNullOrWhiteSpace(breakdown.SourceLabel)
                ? "未标记来源"
                : breakdown.SourceLabel,
            LocalTelemetryBreakdownKind.Cli => breakdown.CliKind is { } cli
                ? WorkspaceDisplay.CliName(cli)
                : "未知 CLI",
            LocalTelemetryBreakdownKind.Model => string.IsNullOrWhiteSpace(breakdown.Model)
                ? "客户端未报告模型"
                : breakdown.Model,
            _ => "未标记",
        };
        Requests = StatsViewModel.FormatCount(usage.RequestCount);
        Success = StatsViewModel.FormatCount(usage.SuccessfulRequestCount);
        Failure = StatsViewModel.FormatCount(usage.FailedRequestCount);
        Tokens = StatsViewModel.FormatCount(StatsViewModel.RecordedTotalTokens(usage));
        InputTokens = StatsViewModel.FormatCount(usage.InputTokens);
        OutputTokens = StatsViewModel.FormatCount(usage.OutputTokens);
        CacheReadTokens = StatsViewModel.FormatCount(usage.CachedInputTokens);
        CacheCreationTokens = StatsViewModel.FormatCount(usage.CacheCreationTokens);
        SuccessRate = StatsViewModel.FormatPercent(usage.SuccessRatePercent);
        AverageLatency = StatsViewModel.FormatLatency(usage.AverageLatencyMilliseconds);
    }

    public string Label { get; }

    public string Requests { get; }

    public string Success { get; }

    public string Failure { get; }

    public string Tokens { get; }

    public string InputTokens { get; }

    public string OutputTokens { get; }

    public string CacheReadTokens { get; }

    public string CacheCreationTokens { get; }

    public string SuccessRate { get; }

    public string AverageLatency { get; }

}

public sealed class LocalTelemetryRecentActivityRowViewModel
{
    internal LocalTelemetryRecentActivityRowViewModel(LocalTelemetryRecentActivity activity)
    {
        ArgumentNullException.ThrowIfNull(activity);
        Time = activity.OccurredAt.ToLocalTime().ToString("M/d HH:mm", CultureInfo.CurrentCulture);
        Source = string.IsNullOrWhiteSpace(activity.SourceLabel) ? "未标记来源" : activity.SourceLabel;
        Cli = WorkspaceDisplay.CliName(activity.CliKind);
        Model = string.IsNullOrWhiteSpace(activity.Model) ? "客户端未报告模型" : activity.Model;
        Result = activity.Succeeded ? "成功" : "失败";
        Tokens = StatsViewModel.FormatCount(activity.TotalTokens + activity.CachedInputTokens + activity.CacheCreationTokens);
        InputTokens = StatsViewModel.FormatCount(activity.InputTokens);
        OutputTokens = StatsViewModel.FormatCount(activity.OutputTokens);
        CacheReadTokens = StatsViewModel.FormatCount(activity.CachedInputTokens);
        CacheCreationTokens = StatsViewModel.FormatCount(activity.CacheCreationTokens);
        Duration = StatsViewModel.FormatLatency(activity.ElapsedMilliseconds);
    }

    public string Time { get; }

    public string Source { get; }

    public string Cli { get; }

    public string Model { get; }

    public string Result { get; }

    public string Tokens { get; }

    public string InputTokens { get; }

    public string OutputTokens { get; }

    public string CacheReadTokens { get; }

    public string CacheCreationTokens { get; }

    public string Duration { get; }

}

public sealed record LocalTelemetrySourceFilterOption(
    string Label,
    string? SourceId,
    bool IsConfigured = false,
    bool IsActive = false)
{
    public string DisplayLabel => IsActive ? $"{Label}（当前）" : Label;
}

public sealed record LocalTelemetryCliFilterOption(string Label, CliKind? CliKind);

public sealed record LocalTelemetryModelFilterOption(string Label, string? Model);

public sealed class ModelStatsRowViewModel
{
    internal ModelStatsRowViewModel(ModelStat model)
    {
        Model = string.IsNullOrWhiteSpace(model.Model) ? "未知模型" : model.Model;
        Requests = StatsViewModel.FormatCount(model.Requests);
        InputTokens = StatsViewModel.FormatCount(model.InputTokens);
        OutputTokens = StatsViewModel.FormatCount(model.OutputTokens);
        CacheReadTokens = StatsViewModel.FormatCount(model.CacheReadTokens);
        CacheCreationTokens = StatsViewModel.FormatCount(model.CacheCreationTokens);
        TotalTokens = StatsViewModel.FormatCount(model.TotalTokens);
        Cost = StatsViewModel.FormatMoney(model.Cost);
        ActualCost = StatsViewModel.FormatMoney(model.ActualCost);
    }

    public string Model { get; }

    public string Requests { get; }

    public string InputTokens { get; }

    public string OutputTokens { get; }

    public string CacheReadTokens { get; }

    public string CacheCreationTokens { get; }

    public string TotalTokens { get; }

    public string Cost { get; }

    public string ActualCost { get; }
}

public sealed class TrendStatsRowViewModel
{
    internal TrendStatsRowViewModel(TrendPoint point, long maximumTokens)
    {
        Date = point.Date;
        Requests = StatsViewModel.FormatCount(point.Requests);
        Tokens = StatsViewModel.FormatCount(point.TotalTokens);
        Cost = StatsViewModel.FormatMoney(point.Cost);
        ActualCost = StatsViewModel.FormatMoney(point.ActualCost);
        BarWidth = Math.Clamp(point.TotalTokens / (double)Math.Max(1, maximumTokens) * 190d, 4d, 190d);
    }

    public string Date { get; }

    public string Requests { get; }

    public string Tokens { get; }

    public string Cost { get; }

    public string ActualCost { get; }

    public double BarWidth { get; }
}

internal sealed record StatsSnapshot(
    StatsOverview Overview,
    IReadOnlyList<ModelStat> Models,
    IReadOnlyList<TrendPoint> Trend,
    CloudStatisticsScope Scope = CloudStatisticsScope.RemoteUser,
    UsageRangeOverview? RangeOverview = null,
    CloudUsageDateRange? Range = null);

internal enum CloudStatisticsScope
{
    RemoteUser,
    LocalUser,
    LocalAdministrator,
}

internal interface IStatsController
{
    Task<StatsSettings> LoadSettingsAsync(CancellationToken cancellationToken);

    Task SaveSettingsAsync(StatsSettings settings, CancellationToken cancellationToken);

    Task<StatsSnapshot> RefreshAsync(StatsSettings settings, CancellationToken cancellationToken);
}

internal sealed class StatsController : IStatsController
{
    private readonly ProfileRepository _repository;
    private readonly StatsService _service;

    public StatsController()
    {
        string root = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            "ai-switch-gui");
        var paths = new ConfigPaths(root);
        _repository = new ProfileRepository(paths);
        _service = new StatsService(new StatsSettings());
    }

    public Task<StatsSettings> LoadSettingsAsync(CancellationToken cancellationToken)
        => Task.Run(() =>
        {
            cancellationToken.ThrowIfCancellationRequested();
            _repository.EnsureInitialized();
            return Clone(_repository.LoadSettings().Stats);
        }, cancellationToken);

    public Task SaveSettingsAsync(StatsSettings settings, CancellationToken cancellationToken)
        => Task.Run(() =>
        {
            cancellationToken.ThrowIfCancellationRequested();
            _repository.EnsureInitialized();
            AppSettings appSettings = _repository.LoadSettings();
            appSettings.Stats = Clone(settings);
            _repository.SaveSettings(appSettings);
        }, cancellationToken);

    public async Task<StatsSnapshot> RefreshAsync(
        StatsSettings settings,
        CancellationToken cancellationToken)
    {
        StatsSettings copy = Clone(settings);
        _service.UpdateSettings(copy);
        await _service.LoginAsync(cancellationToken).ConfigureAwait(false);

        // Keep the legacy overview strictly labelled as all-time while the
        // dashboard body is built from one range-consistent snapshot.  This
        // prevents a period switch from mixing a 7/30-day chart with an
        // unrelated cumulative model ledger or activity list.
        Task<StatsOverview> overviewTask = _service.GetOverviewAsync(cancellationToken);
        Task<CloudDashboardSnapshot> dashboardTask = _service.GetDashboardSnapshotAsync(cancellationToken);
        await Task.WhenAll(overviewTask, dashboardTask).ConfigureAwait(false);
        CloudDashboardSnapshot dashboard = await dashboardTask.ConfigureAwait(false);

        return new StatsSnapshot(
            await overviewTask.ConfigureAwait(false),
            dashboard.Models,
            dashboard.Trend,
            RangeOverview: dashboard.Metrics,
            Range: dashboard.Range);
    }

    private static StatsSettings Clone(StatsSettings settings)
        => new()
        {
            GatewayBaseUrl = settings.GatewayBaseUrl,
            Email = settings.Email,
            Password = settings.Password,
            TrendDays = settings.TrendDays,
        };
}

/// <summary>
/// A narrow, unauthenticated health probe for the local Sub2API backend
/// selected by Connection Center.  A successful probe only establishes local
/// reachability; it is never treated as a user or administrator login.
/// </summary>
internal interface ILocalGatewayStatsProbe
{
    Task<LocalGatewayStatsProbeResult> ProbeAsync(Uri apiBaseUri, CancellationToken cancellationToken);
}

internal sealed record LocalGatewayStatsProbeResult(bool IsAvailable)
{
    public static LocalGatewayStatsProbeResult Available { get; } = new(true);

    public static LocalGatewayStatsProbeResult Unavailable { get; } = new(false);
}

internal sealed class LocalGatewayStatsProbe : ILocalGatewayStatsProbe
{
    private readonly HttpClient _httpClient;

    public LocalGatewayStatsProbe()
        : this(new HttpClient(new HttpClientHandler
        {
            UseProxy = false,
            AllowAutoRedirect = false,
        })
        {
            Timeout = TimeSpan.FromSeconds(3),
        })
    {
    }

    internal LocalGatewayStatsProbe(HttpClient httpClient)
        => _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));

    public async Task<LocalGatewayStatsProbeResult> ProbeAsync(Uri apiBaseUri, CancellationToken cancellationToken)
    {
        if (!Sub2ApiEndpointNormalizer.TryNormalizeApiBaseUri(
                apiBaseUri?.AbsoluteUri,
                allowPublicHttp: true,
                out Uri? normalizedBaseUri))
        {
            return LocalGatewayStatsProbeResult.Unavailable;
        }

        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Get, new Uri(normalizedBaseUri!, "health"));
            using HttpResponseMessage response = await _httpClient
                .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                .ConfigureAwait(false);
            return response.IsSuccessStatusCode
                ? LocalGatewayStatsProbeResult.Available
                : LocalGatewayStatsProbeResult.Unavailable;
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (HttpRequestException)
        {
            return LocalGatewayStatsProbeResult.Unavailable;
        }
        catch (TaskCanceledException)
        {
            return LocalGatewayStatsProbeResult.Unavailable;
        }
    }
}

/// <summary>
/// Stores a rotating normal-user refresh token with DPAPI for the current
/// Windows user.  The token is never put in profiles.json, telemetry, logs, or
/// a bindable property.
/// </summary>
internal interface ILocalUserStatsAuthorizationStore
{
    LocalUserStatsAuthorization GetCurrent();

    LocalUserStatsAuthorizationSaveResult Save(string refreshToken);

    bool Clear();
}

internal enum LocalUserStatsAuthorizationSaveResult
{
    Saved,
    Invalid,
    Unavailable,
}

internal sealed class LocalUserStatsAuthorization
{
    private LocalUserStatsAuthorization(string? refreshToken)
        => RefreshToken = refreshToken;

    internal string? RefreshToken { get; }

    public bool IsAvailable => !string.IsNullOrWhiteSpace(RefreshToken);

    public static LocalUserStatsAuthorization Unavailable { get; } = new(null);

    internal static LocalUserStatsAuthorization Available(string refreshToken) => new(refreshToken);

    public override string ToString() => "Local user statistics authorization";
}

internal sealed class DpapiLocalUserStatsAuthorizationStore : ILocalUserStatsAuthorizationStore
{
    private static readonly byte[] Entropy = SHA256.HashData(
        Encoding.UTF8.GetBytes("LanAi.Workspace/Sub2ApiLocalUserStatsRefresh/v1"));
    private readonly string _path;

    public DpapiLocalUserStatsAuthorizationStore()
        : this(Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "LanAi.Workspace",
            "Auth",
            "sub2api-local-user-refresh.bin"))
    {
    }

    internal DpapiLocalUserStatsAuthorizationStore(string path)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(path);
        _path = Path.GetFullPath(path);
    }

    public LocalUserStatsAuthorization GetCurrent()
    {
        if (!OperatingSystem.IsWindows() || !File.Exists(_path))
        {
            return LocalUserStatsAuthorization.Unavailable;
        }

        byte[]? protectedBytes = null;
        byte[]? plainBytes = null;
        try
        {
            protectedBytes = File.ReadAllBytes(_path);
            if (protectedBytes.Length is 0 or > 4096)
            {
                return LocalUserStatsAuthorization.Unavailable;
            }

            plainBytes = ProtectedData.Unprotect(protectedBytes, Entropy, DataProtectionScope.CurrentUser);
            string candidate = Encoding.UTF8.GetString(plainBytes);
            return TryNormalizeRefreshToken(candidate, out string? normalized)
                ? LocalUserStatsAuthorization.Available(normalized!)
                : LocalUserStatsAuthorization.Unavailable;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or CryptographicException)
        {
            return LocalUserStatsAuthorization.Unavailable;
        }
        finally
        {
            if (protectedBytes is not null)
            {
                CryptographicOperations.ZeroMemory(protectedBytes);
            }

            if (plainBytes is not null)
            {
                CryptographicOperations.ZeroMemory(plainBytes);
            }
        }
    }

    public LocalUserStatsAuthorizationSaveResult Save(string refreshToken)
    {
        if (!TryNormalizeRefreshToken(refreshToken, out string? normalized))
        {
            return LocalUserStatsAuthorizationSaveResult.Invalid;
        }

        if (!OperatingSystem.IsWindows())
        {
            return LocalUserStatsAuthorizationSaveResult.Unavailable;
        }

        byte[] plainBytes = Encoding.UTF8.GetBytes(normalized!);
        byte[]? protectedBytes = null;
        string? temporaryPath = null;
        try
        {
            protectedBytes = ProtectedData.Protect(plainBytes, Entropy, DataProtectionScope.CurrentUser);
            string? directory = Path.GetDirectoryName(_path);
            if (string.IsNullOrWhiteSpace(directory))
            {
                return LocalUserStatsAuthorizationSaveResult.Unavailable;
            }

            Directory.CreateDirectory(directory);
            temporaryPath = _path + "." + Guid.NewGuid().ToString("N") + ".tmp";
            File.WriteAllBytes(temporaryPath, protectedBytes);
            File.Move(temporaryPath, _path, overwrite: true);
            return LocalUserStatsAuthorizationSaveResult.Saved;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or CryptographicException)
        {
            return LocalUserStatsAuthorizationSaveResult.Unavailable;
        }
        finally
        {
            CryptographicOperations.ZeroMemory(plainBytes);
            if (protectedBytes is not null)
            {
                CryptographicOperations.ZeroMemory(protectedBytes);
            }

            if (!string.IsNullOrWhiteSpace(temporaryPath))
            {
                try
                {
                    if (File.Exists(temporaryPath))
                    {
                        File.Delete(temporaryPath);
                    }
                }
                catch (IOException)
                {
                    // A failed cleanup does not expose plaintext because the
                    // temporary file already contains DPAPI-protected bytes.
                }
                catch (UnauthorizedAccessException)
                {
                    // Same confidentiality boundary as above.
                }
            }
        }
    }

    public bool Clear()
    {
        try
        {
            if (!File.Exists(_path))
            {
                return false;
            }

            File.Delete(_path);
            return true;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            return false;
        }
    }

    private static bool TryNormalizeRefreshToken(string? value, out string? normalized)
    {
        normalized = null;
        if (string.IsNullOrWhiteSpace(value))
        {
            return false;
        }

        string candidate = value.Trim();
        if (candidate.Length is 0 or > 2048 || candidate.Any(char.IsControl))
        {
            return false;
        }

        normalized = candidate;
        return true;
    }
}

internal enum LocalCloudStatisticsFailure
{
    AuthorizationUnavailable,
    Unauthorized,
    Forbidden,
    ComplianceRequired,
    RequiresTwoFactor,
    SecureStorageUnavailable,
    GatewayUnavailable,
    ProtocolMismatch,
}

internal sealed class LocalCloudStatisticsException : Exception
{
    public LocalCloudStatisticsException(LocalCloudStatisticsFailure failure)
        : base(failure.ToString())
        => Failure = failure;

    public LocalCloudStatisticsFailure Failure { get; }
}

internal sealed record LocalUserAuthorizationResult(StatsSnapshot Snapshot, string RefreshToken);

internal sealed record LocalUserRefreshResult(StatsSnapshot Snapshot, string RefreshToken);

internal interface ILocalCloudStatisticsClient
{
    Task<StatsSnapshot> RefreshAdministratorAsync(
        string gatewayBaseUrl,
        string administratorApiKey,
        int trendDays,
        CancellationToken cancellationToken);

    Task<LocalUserAuthorizationResult> AuthorizeUserAsync(
        string gatewayBaseUrl,
        string email,
        string password,
        int trendDays,
        CancellationToken cancellationToken);

    Task<LocalUserRefreshResult> RefreshUserAsync(
        string gatewayBaseUrl,
        string refreshToken,
        int trendDays,
        CancellationToken cancellationToken);

    Task<StatsSnapshot> RefreshUserWithAccessTokenAsync(
        string gatewayBaseUrl,
        string accessToken,
        int trendDays,
        CancellationToken cancellationToken)
        => Task.FromException<StatsSnapshot>(new NotSupportedException("Access-token refresh is not implemented."));

    Task<StatsSnapshot> RefreshWithAccessTokenAsync(
        string gatewayBaseUrl,
        string accessToken,
        int trendDays,
        bool administrator,
        CancellationToken cancellationToken)
        => administrator
            ? Task.FromException<StatsSnapshot>(new NotSupportedException("Administrator access-token refresh is not implemented."))
            : RefreshUserWithAccessTokenAsync(gatewayBaseUrl, accessToken, trendDays, cancellationToken);
}

/// <summary>
/// Uses only documented Sub2API authentication and dashboard endpoints.  It
/// first prefers the administrator dashboard when an explicit administrator
/// key exists; user flow uses a rotating JWT refresh token.  No endpoint is
/// called without the matching authorization header.
/// </summary>
internal sealed class LocalCloudStatisticsClient : ILocalCloudStatisticsClient, IDisposable
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
        NumberHandling = JsonNumberHandling.AllowReadingFromString,
    };

    private readonly HttpClient _httpClient;

    public LocalCloudStatisticsClient()
        : this(new HttpClient(new HttpClientHandler
        {
            UseProxy = false,
            AllowAutoRedirect = false,
        })
        {
            Timeout = TimeSpan.FromSeconds(15),
        })
    {
    }

    internal LocalCloudStatisticsClient(HttpClient httpClient)
        => _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));

    public void Dispose() => _httpClient.Dispose();

    public Task<StatsSnapshot> RefreshAdministratorAsync(
        string gatewayBaseUrl,
        string administratorApiKey,
        int trendDays,
        CancellationToken cancellationToken)
    {
        Uri baseUri = RequireGateway(gatewayBaseUrl);
        if (string.IsNullOrWhiteSpace(administratorApiKey))
        {
            throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.AuthorizationUnavailable);
        }

        return LoadSnapshotAsync(
            baseUri,
            request => request.Headers.TryAddWithoutValidation("x-api-key", administratorApiKey),
            trendDays,
            CloudStatisticsScope.LocalAdministrator,
            administrator: true,
            cancellationToken);
    }

    public async Task<LocalUserAuthorizationResult> AuthorizeUserAsync(
        string gatewayBaseUrl,
        string email,
        string password,
        int trendDays,
        CancellationToken cancellationToken)
    {
        Uri baseUri = RequireGateway(gatewayBaseUrl);
        AuthenticationData authentication = await LoginAsync(baseUri, email, password, cancellationToken).ConfigureAwait(false);
        if (authentication.RequiresTwoFactor)
        {
            throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.RequiresTwoFactor);
        }

        if (string.IsNullOrWhiteSpace(authentication.AccessToken) || string.IsNullOrWhiteSpace(authentication.RefreshToken))
        {
            throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.ProtocolMismatch);
        }

        StatsSnapshot snapshot = await LoadSnapshotAsync(
                baseUri,
                request => request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", authentication.AccessToken),
                trendDays,
                CloudStatisticsScope.LocalUser,
                administrator: false,
                cancellationToken)
            .ConfigureAwait(false);
        return new LocalUserAuthorizationResult(snapshot, authentication.RefreshToken);
    }

    public async Task<LocalUserRefreshResult> RefreshUserAsync(
        string gatewayBaseUrl,
        string refreshToken,
        int trendDays,
        CancellationToken cancellationToken)
    {
        Uri baseUri = RequireGateway(gatewayBaseUrl);
        AuthenticationData authentication = await RefreshTokenAsync(baseUri, refreshToken, cancellationToken).ConfigureAwait(false);
        if (string.IsNullOrWhiteSpace(authentication.AccessToken) || string.IsNullOrWhiteSpace(authentication.RefreshToken))
        {
            throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.ProtocolMismatch);
        }

        StatsSnapshot snapshot = await LoadSnapshotAsync(
                baseUri,
                request => request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", authentication.AccessToken),
                trendDays,
                CloudStatisticsScope.LocalUser,
                administrator: false,
                cancellationToken)
            .ConfigureAwait(false);
        return new LocalUserRefreshResult(snapshot, authentication.RefreshToken);
    }

    public Task<StatsSnapshot> RefreshUserWithAccessTokenAsync(
        string gatewayBaseUrl,
        string accessToken,
        int trendDays,
        CancellationToken cancellationToken)
    {
        Uri baseUri = RequireGateway(gatewayBaseUrl);
        if (string.IsNullOrWhiteSpace(accessToken))
        {
            throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.AuthorizationUnavailable);
        }

        return LoadSnapshotAsync(
            baseUri,
            request => request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", accessToken),
            trendDays,
            CloudStatisticsScope.LocalUser,
            administrator: false,
            cancellationToken);
    }

    public Task<StatsSnapshot> RefreshWithAccessTokenAsync(
        string gatewayBaseUrl,
        string accessToken,
        int trendDays,
        bool administrator,
        CancellationToken cancellationToken)
    {
        Uri baseUri = RequireGateway(gatewayBaseUrl);
        if (string.IsNullOrWhiteSpace(accessToken))
        {
            throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.AuthorizationUnavailable);
        }

        return LoadSnapshotAsync(
            baseUri,
            request => request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", accessToken),
            trendDays,
            administrator ? CloudStatisticsScope.LocalAdministrator : CloudStatisticsScope.LocalUser,
            administrator,
            cancellationToken);
    }

    private async Task<StatsSnapshot> LoadSnapshotAsync(
        Uri baseUri,
        Action<HttpRequestMessage> applyAuthorization,
        int trendDays,
        CloudStatisticsScope scope,
        bool administrator,
        CancellationToken cancellationToken)
    {
        string prefix = administrator ? "api/v1/admin/dashboard" : "api/v1/usage/dashboard";
        int days = trendDays is 1 or 7 or 30 ? trendDays : 7;
        // Calculate this once before issuing parallel requests.  All panels
        // then share the same inclusive local-calendar window even if a
        // refresh happens near midnight.
        CloudUsageDateRange dashboardRange = StatsService.CreateDashboardDateRange(
            days,
            DateOnly.FromDateTime(DateTime.Now),
            StatsService.ResolveIanaTimeZone());
        string range = dashboardRange.ToQueryString();
        string trendRange = dashboardRange.ToQueryString(granularity: days == 1 ? "hour" : "day");
        Task<StatsOverview> overviewTask = GetAsync<StatsOverview>(baseUri, $"{prefix}/stats", applyAuthorization, cancellationToken);
        string usageStatsPath = administrator ? "api/v1/admin/usage/stats" : "api/v1/usage/stats";
        Task<UsageRangeOverview> rangeOverviewTask = GetAsync<UsageRangeOverview>(
            baseUri,
            $"{usageStatsPath}?{range}",
            applyAuthorization,
            cancellationToken);
        Task<ModelsEnvelopeData> modelsTask = GetAsync<ModelsEnvelopeData>(
            baseUri,
            $"{prefix}/models?{range}",
            applyAuthorization,
            cancellationToken);
        Task<TrendEnvelopeData> trendTask = GetAsync<TrendEnvelopeData>(
            baseUri,
            $"{prefix}/trend?{trendRange}",
            applyAuthorization,
            cancellationToken);
        await Task.WhenAll(overviewTask, rangeOverviewTask, modelsTask, trendTask).ConfigureAwait(false);
        return new StatsSnapshot(
            await overviewTask.ConfigureAwait(false),
            (await modelsTask.ConfigureAwait(false)).Models ?? Array.Empty<ModelStat>(),
            (await trendTask.ConfigureAwait(false)).Trend ?? Array.Empty<TrendPoint>(),
            scope,
            await rangeOverviewTask.ConfigureAwait(false),
            Range: dashboardRange);
    }

    private async Task<AuthenticationData> LoginAsync(Uri baseUri, string email, string password, CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new { email = email.Trim(), password });
        try
        {
            return await PostAsync<AuthenticationData>(baseUri, "api/v1/auth/login", body, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(body);
        }
    }

    private async Task<AuthenticationData> RefreshTokenAsync(Uri baseUri, string refreshToken, CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new { refresh_token = refreshToken });
        try
        {
            return await PostAsync<AuthenticationData>(baseUri, "api/v1/auth/refresh", body, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(body);
        }
    }

    private async Task<T> GetAsync<T>(
        Uri baseUri,
        string relativePath,
        Action<HttpRequestMessage> applyAuthorization,
        CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, new Uri(baseUri, relativePath));
        applyAuthorization(request);
        return await SendAndReadAsync<T>(request, cancellationToken).ConfigureAwait(false);
    }

    private async Task<T> PostAsync<T>(Uri baseUri, string relativePath, byte[] body, CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, new Uri(baseUri, relativePath))
        {
            Content = new ByteArrayContent(body),
        };
        request.Content.Headers.ContentType = new MediaTypeHeaderValue("application/json") { CharSet = "utf-8" };
        return await SendAndReadAsync<T>(request, cancellationToken).ConfigureAwait(false);
    }

    private async Task<T> SendAndReadAsync<T>(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        try
        {
            using HttpResponseMessage response = await _httpClient
                .SendAsync(request, HttpCompletionOption.ResponseContentRead, cancellationToken)
                .ConfigureAwait(false);
            if (!response.IsSuccessStatusCode)
            {
                throw new LocalCloudStatisticsException(MapFailure(response.StatusCode));
            }

            byte[] bytes = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
            try
            {
                ApiEnvelope<T>? envelope = JsonSerializer.Deserialize<ApiEnvelope<T>>(bytes, JsonOptions);
                if (envelope?.Code != 0 || envelope.Data is null)
                {
                    throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.ProtocolMismatch);
                }

                return envelope.Data;
            }
            finally
            {
                CryptographicOperations.ZeroMemory(bytes);
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (TaskCanceledException)
        {
            throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.GatewayUnavailable);
        }
        catch (HttpRequestException)
        {
            throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.GatewayUnavailable);
        }
    }

    private static LocalCloudStatisticsFailure MapFailure(HttpStatusCode statusCode)
        => statusCode switch
        {
            HttpStatusCode.Unauthorized => LocalCloudStatisticsFailure.Unauthorized,
            HttpStatusCode.Forbidden => LocalCloudStatisticsFailure.Forbidden,
            HttpStatusCode.Locked => LocalCloudStatisticsFailure.ComplianceRequired,
            _ => LocalCloudStatisticsFailure.GatewayUnavailable,
        };

    private static Uri RequireGateway(string value)
    {
        if (!Sub2ApiEndpointNormalizer.TryNormalizeApiBaseUri(
                value,
                allowPublicHttp: true,
                out Uri? baseUri))
        {
            throw new LocalCloudStatisticsException(LocalCloudStatisticsFailure.GatewayUnavailable);
        }

        return baseUri!;
    }

    private static string BuildDashboardRangeQuery(int days)
    {
        int normalizedDays = days is 1 or 7 or 30 ? days : 7;
        return StatsService.BuildDashboardRangeQuery(normalizedDays);
    }

    private sealed class ApiEnvelope<T>
    {
        [JsonPropertyName("code")] public int Code { get; set; }

        [JsonPropertyName("data")] public T? Data { get; set; }
    }

    private sealed class AuthenticationData
    {
        [JsonPropertyName("access_token")] public string? AccessToken { get; set; }

        [JsonPropertyName("refresh_token")] public string? RefreshToken { get; set; }

        [JsonPropertyName("requires_2fa")] public bool RequiresTwoFactor { get; set; }
    }

    private sealed class ModelsEnvelopeData
    {
        [JsonPropertyName("models")] public IReadOnlyList<ModelStat>? Models { get; set; }
    }

    private sealed class TrendEnvelopeData
    {
        [JsonPropertyName("trend")] public IReadOnlyList<TrendPoint>? Trend { get; set; }
    }
}
