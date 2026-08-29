using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Controls;

namespace LanAi.RelayClient.ViewModels;

/// <summary>
/// The signed-in surface: balance, today's usage, and the group selector (F4, F5).
/// </summary>
/// <remarks>
/// <para>
/// Each card loads on its own and fails on its own. F4.2 forbids one card's
/// failure from taking down the page or the session, and the shape of the code
/// enforces it: there is no combined await and no shared try/catch, so a failure
/// has nowhere to propagate to.
/// </para>
/// <para>
/// In particular a 401 from a card endpoint greys that card only. Ending the
/// session stays the exclusive right of token renewal — a stale card response
/// must never log the user out.
/// </para>
/// </remarks>
public sealed partial class DashboardViewModel : ObservableObject
{
    private readonly IRelayServerClient _client;
    private readonly RelaySessionManager _session;
    private readonly IGroupPreferenceStore _preferences;
    private readonly ManagedKeyNaming _keyNaming;
    private readonly ICodexStartup _codex;
    private readonly ICodexInstaller _codexInstaller;
    private readonly ICodexAccountStore _codexAccountStore;
    private readonly IStartupRegistration _startupRegistration;
    private readonly PollingBackoff _pollingBackoff;
    private readonly SafeAsyncRunner _safeAsync;
    private readonly SemaphoreSlim _pollGate = new(1, 1);

    private PublicSettings _settings = PublicSettings.Conservative;
    private RelayApiKey? _managedKey;
    private bool _refreshHadFailure;
    private bool _refreshWasRateLimited;

    /// <summary>Cancels the refresh in flight when the session ends under it.</summary>
    private CancellationTokenSource? _refreshCancellation;

    private CancellationTokenSource? _installationCancellation;

    internal DashboardViewModel(
        IRelayServerClient client,
        RelaySessionManager session,
        IGroupPreferenceStore preferences,
        ManagedKeyNaming keyNaming,
        ICodexStartup codex,
        PollingBackoff? pollingBackoff = null,
        SafeAsyncRunner? safeAsync = null,
        ICodexInstaller? codexInstaller = null,
        ICodexAccountStore? codexAccountStore = null,
        IStartupRegistration? startupRegistration = null)
    {
        _client = client ?? throw new ArgumentNullException(nameof(client));
        _session = session ?? throw new ArgumentNullException(nameof(session));
        _preferences = preferences ?? throw new ArgumentNullException(nameof(preferences));
        _keyNaming = keyNaming ?? throw new ArgumentNullException(nameof(keyNaming));
        _codex = codex ?? throw new ArgumentNullException(nameof(codex));
        _codexInstaller = codexInstaller ?? new CodexInstaller();
        _codexAccountStore = codexAccountStore ?? new CodexAccountStore();
        _startupRegistration = startupRegistration ?? new UnsupportedStartupRegistration();
        _pollingBackoff = pollingBackoff ?? new PollingBackoff();
        _safeAsync = safeAsync ?? new SafeAsyncRunner();
    }

    public ObservableCollection<GroupItemViewModel> Groups { get; } = [];

    [ObservableProperty]
    private string userDisplayName = string.Empty;

    // ---- Account card -------------------------------------------------------

    [ObservableProperty]
    private string balanceText = "—";

    [ObservableProperty]
    private string frozenBalanceText = "—";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(AccountUnavailable))]
    private bool accountReady;

    /// <summary>True when this card could not be loaded and should be greyed out.</summary>
    public bool AccountUnavailable => !AccountReady;

    [ObservableProperty]
    private bool balanceIsLow;

    /// <summary>Where the top-up button should send the user; supplied by the server.</summary>
    [ObservableProperty]
    private string rechargeUrl = string.Empty;

    public bool CanRecharge => _settings.PaymentEnabled || !string.IsNullOrWhiteSpace(RechargeUrl);

    [ObservableProperty]
    private bool isRateLimited;

    [ObservableProperty]
    private string refreshMessage = string.Empty;

    // ---- Usage card ---------------------------------------------------------

    [ObservableProperty]
    private string todayRequestsText = "—";

    [ObservableProperty]
    private string todayTokensText = "—";

    [ObservableProperty]
    private string todayCostText = "—";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(UsageUnavailable))]
    private bool usageReady;

    public bool UsageUnavailable => !UsageReady;

    // ---- Subscription card --------------------------------------------------

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(SubscriptionUnavailable))]
    private bool subscriptionReady;

    public bool SubscriptionUnavailable => !SubscriptionReady;

    [ObservableProperty]
    private string subscriptionName = string.Empty;

    [ObservableProperty]
    private string subscriptionProgressText = string.Empty;

    public bool HasSubscription => SubscriptionReady && !string.IsNullOrWhiteSpace(SubscriptionName);

    // ---- Group card ---------------------------------------------------------

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(GroupsUnavailable))]
    private bool groupsReady;

    public bool GroupsUnavailable => !GroupsReady;

    [ObservableProperty]
    private string currentGroupName = "未选择";

    /// <summary>The rate of the group in force, spelled out next to its name.</summary>
    [ObservableProperty]
    private string currentGroupRate = string.Empty;

    [ObservableProperty]
    private string currentGroupRateDescription = string.Empty;

    public bool HasGroupRateDescription => !string.IsNullOrWhiteSpace(CurrentGroupRateDescription);

    partial void OnCurrentGroupRateDescriptionChanged(string value) =>
        OnPropertyChanged(nameof(HasGroupRateDescription));

    /// <summary>
    /// The dropdown's selection.
    /// </summary>
    /// <remarks>
    /// Changing this is what performs a switch, so every programmatic assignment —
    /// loading the current group, rolling a rejected switch back — is made under
    /// <see cref="_applyingKnownState"/>. Without that guard, populating the list
    /// would look exactly like the user picking something and would fire a switch
    /// against the server on every refresh.
    /// </remarks>
    [ObservableProperty]
    private GroupItemViewModel? selectedGroup;

    private bool _applyingKnownState;

    /// <summary>
    /// True when the dropdown has nothing selected and would otherwise read as blank.
    /// </summary>
    /// <remarks>
    /// A collapsed WPF ComboBox with no selection renders empty, which a novice
    /// user reads as "broken" rather than "nothing chosen yet" — so the blank state
    /// gets words of its own.
    /// </remarks>
    public bool HasNoGroupSelected => SelectedGroup is null;

    partial void OnSelectedGroupChanged(GroupItemViewModel? value)
    {
        OnPropertyChanged(nameof(HasNoGroupSelected));
        OnPropertyChanged(nameof(IsClaudeGroup));

        if (_applyingKnownState || value is null || value.IsCurrent)
        {
            return;
        }
        _ = _safeAsync.RunAsync(() => SwitchGroupAsync(value));
    }

    /// <summary>Assigns the selection without treating it as a user action.</summary>
    private void SelectWithoutSwitching(GroupItemViewModel? group)
    {
        _applyingKnownState = true;
        try
        {
            SelectedGroup = group;
        }
        finally
        {
            _applyingKnownState = false;
        }
    }

    [ObservableProperty]
    private string groupMessage = string.Empty;

    public bool HasGroupMessage => !string.IsNullOrWhiteSpace(GroupMessage);

    partial void OnGroupMessageChanged(string value) => OnPropertyChanged(nameof(HasGroupMessage));

    // ---- Claude preference (visible only when a Claude-platform group is selected) -----

    /// <summary>True when the selected group uses the Anthropic/Claude platform.</summary>
    public bool IsClaudeGroup => SelectedGroup?.Platform?.ToLowerInvariant().Contains("claude") == true
                               || SelectedGroup?.Platform?.ToLowerInvariant().Contains("anthropic") == true;

    public static IReadOnlyList<string> ClaudeModels { get; } =
        ["claude-sonnet-5", "claude-opus-5"];

    public static IReadOnlyList<string> ClaudeThinkingLevels { get; } =
        ["关闭", "低", "中", "高", "极高"];

    private static readonly string[] _thinkingLevelKeys = ["off", "low", "medium", "high", "max"];

    private const int DefaultClaudeThinkingLevelIndex = 2;

    private bool _loadingClaudePreference;

    [ObservableProperty]
    private string selectedClaudeModel = "claude-sonnet-5";

    partial void OnSelectedClaudeModelChanged(string value) =>
        _ = _loadingClaudePreference ? Task.CompletedTask : _safeAsync.RunAsync(SaveClaudePreferenceAsync);

    [ObservableProperty]
    private string selectedClaudeThinkingLevel = ClaudeThinkingLevels[DefaultClaudeThinkingLevelIndex];

    partial void OnSelectedClaudeThinkingLevelChanged(string value) =>
        _ = _loadingClaudePreference ? Task.CompletedTask : _safeAsync.RunAsync(SaveClaudePreferenceAsync);

    internal async Task LoadClaudePreferenceAsync()
    {
        _loadingClaudePreference = true;
        try
        {
            var token = await _session.GetAccessTokenAsync().ConfigureAwait(true);
            var pref = await _client.GetClaudePreferenceAsync(token);
            SelectedClaudeModel = pref.Model;
            var idx = System.Array.IndexOf(_thinkingLevelKeys, pref.ThinkingLevel);
            SelectedClaudeThinkingLevel = idx >= 0
                ? ClaudeThinkingLevels[idx]
                : ClaudeThinkingLevels[DefaultClaudeThinkingLevelIndex];
        }
        catch { /* best-effort */ }
        finally
        {
            _loadingClaudePreference = false;
        }
    }

    private async Task SaveClaudePreferenceAsync()
    {
        try
        {
            var token = await _session.GetAccessTokenAsync().ConfigureAwait(true);
            var modelIdx = System.Array.IndexOf(ClaudeModels.ToArray(), SelectedClaudeModel);
            if (modelIdx < 0) modelIdx = 0;
            var levelIdx = System.Array.IndexOf(
                ClaudeThinkingLevels.ToArray(),
                SelectedClaudeThinkingLevel);
            if (levelIdx < 0) levelIdx = 0;
            await _client.SetClaudePreferenceAsync(token, ClaudeModels[modelIdx], _thinkingLevelKeys[levelIdx]);
        }
        catch { /* best-effort */ }
    }

    // ---- Usage trend and models (F4) -----------------------------------------

    /// <summary>Days covered by the trend chart and the model breakdown.</summary>
    private const int TrendDays = 7;

    /// <summary>How many models the breakdown lists.</summary>
    /// <remarks>
    /// Five. The point of this card is "where is my money going", and a list long
    /// enough to need scrolling stops answering that at a glance.
    /// </remarks>
    private const int TopModels = 5;

    public ObservableCollection<UsageLineChartPoint> CostTrend { get; } = [];

    public ObservableCollection<ModelUsageRowViewModel> TopModelUsage { get; } = [];

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(TrendUnavailable))]
    private bool trendReady;

    public bool TrendUnavailable => !TrendReady;

    public bool HasTrend => CostTrend.Count > 0;

    public bool HasModelUsage => TopModelUsage.Count > 0;

    /// <summary>
    /// True when the card loaded fine and there is simply nothing to show yet.
    /// </summary>
    /// <remarks>
    /// Distinct from the failure state, and it needs words of its own: an account
    /// that has not sent any traffic gets an empty chart, and to a novice an empty
    /// card is indistinguishable from a broken one. The same omission already
    /// caught us once with the group dropdown.
    /// </remarks>
    public bool HasNoUsageYet => TrendReady && CostTrend.Count == 0;

    /// <remarks>
    /// Its own card, loaded on its own, for the same reason as the others (F4.2) —
    /// and the chart is the most likely of them to fail, since it asks for the
    /// widest date range.
    /// </remarks>
    private async Task LoadTrendCardAsync(string accessToken, CancellationToken cancellationToken)
    {
        try
        {
            IReadOnlyList<UsageTrendPoint> trend =
                await _client.GetUsageTrendAsync(accessToken, TrendDays, cancellationToken).ConfigureAwait(true);

            CostTrend.Clear();
            foreach (UsageTrendPoint point in trend)
            {
                // Labelled by day-of-month alone: seven full dates will not fit
                // under a chart this narrow, and the year is never in question.
                string label = point.Date.Length >= 10 ? point.Date[8..10] : point.Date;
                CostTrend.Add(new UsageLineChartPoint(
                    label,
                    point.ActualCost,
                    $"{point.Date}  ${point.ActualCost:0.####}  {point.Requests} 次"));
            }

            // Fails on its own inside the same card: the chart is still worth
            // showing when the per-model split is unavailable.
            try
            {
                IReadOnlyList<ModelUsage> models =
                    await _client.GetModelUsageAsync(accessToken, TrendDays, cancellationToken).ConfigureAwait(true);

                TopModelUsage.Clear();
                foreach (ModelUsage model in models
                    .OrderByDescending(m => m.ActualCost)
                    .ThenByDescending(m => m.Requests)
                    .Take(TopModels))
                {
                    TopModelUsage.Add(new ModelUsageRowViewModel(model));
                }
            }
            catch (Exception ex) when (ObserveRefreshFailure(ex))
            {
                TopModelUsage.Clear();
                ClientLog.Warning("按模型用量取数失败", ex);
            }

            TrendReady = true;
            OnPropertyChanged(nameof(HasTrend));
            OnPropertyChanged(nameof(HasModelUsage));
            OnPropertyChanged(nameof(HasNoUsageYet));
        }
        catch (Exception ex) when (ObserveRefreshFailure(ex))
        {
            TrendReady = false;
            OnPropertyChanged(nameof(HasNoUsageYet));
            ClientLog.Warning("用量趋势取数失败", ex);
        }
    }

    // ---- Codex ---------------------------------------------------------------

    [ObservableProperty]
    private bool isStartingCodex;

    [ObservableProperty]
    private string codexMessage = string.Empty;

    public bool HasCodexMessage => !string.IsNullOrWhiteSpace(CodexMessage);

    partial void OnCodexMessageChanged(string value) => OnPropertyChanged(nameof(HasCodexMessage));

    /// <summary>True once Codex is up, so the button stops inviting a second launch.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanStartCodex))]
    [NotifyPropertyChangedFor(nameof(StartCodexLabel))]
    private bool isCodexRunning;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanStartCodex))]
    [NotifyPropertyChangedFor(nameof(StartCodexLabel))]
    private bool requiresCodexAccountRestart;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanStartCodex))]
    [NotifyPropertyChangedFor(nameof(StartCodexLabel))]
    private bool isInstallingCodex;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(StartCodexLabel))]
    private string codexDownloadProgressText = string.Empty;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(StartCodexLabel))]
    private bool codexNotInstalled;

    [ObservableProperty]
    private bool codexInstallerAvailable;

    [ObservableProperty]
    private bool startWithWindows = true;

    private bool _loadingStartupPreference;

    partial void OnStartWithWindowsChanged(bool value)
    {
        if (!_loadingStartupPreference)
        {
            _startupRegistration.SetEnabled(value);
        }
    }

    /// <summary>Loads the preference and enables startup by default on first use.</summary>
    public void InitializeStartupPreference()
    {
        _loadingStartupPreference = true;
        try
        {
            StartWithWindows = _startupRegistration.EnsureDefaultEnabled();
        }
        finally
        {
            _loadingStartupPreference = false;
        }
    }

    /// <remarks>
    /// Disabled while starting, and once running: pressing it again would rewrite
    /// the config and re-probe for no benefit, and to a novice a button that stays
    /// live reads as "it did not work, press again".
    /// </remarks>
    public bool CanStartCodex => !IsStartingCodex && !IsInstallingCodex &&
        (!IsCodexRunning || RequiresCodexAccountRestart);

    public string StartCodexLabel => IsInstallingCodex && !string.IsNullOrWhiteSpace(CodexDownloadProgressText)
        ? CodexDownloadProgressText
        : IsInstallingCodex
        ? "正在安装 ChatGPT…"
        : RequiresCodexAccountRestart
        ? "重启 ChatGPT 激活账户"
        : IsCodexRunning
        ? "ChatGPT 已启动"
        : CodexNotInstalled ? "安装 ChatGPT" : "启动 ChatGPT";

    partial void OnIsStartingCodexChanged(bool value) => OnPropertyChanged(nameof(CanStartCodex));

    /// <summary>
    /// Refreshes the Codex state and rolls the lease forward when it is due.
    /// </summary>
    /// <remarks>
    /// Runs on the same poll as the cards. The lease is the reason the client stays
    /// resident at all — a tray icon that sat there without renewing would keep the
    /// process alive and still let Codex stop working overnight.
    /// </remarks>
    public async Task MonitorCodexAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            CodexHealth health = await _codex.CheckAsync(cancellationToken).ConfigureAwait(true);
            IsCodexRunning = health.IsRunning;
            UpdateCodexAccountActivationState();
            CodexNotInstalled = !health.IsInstalled;
            CodexInstallerAvailable = _codexInstaller.Inspect().PackageAvailable;

            DateTimeOffset? renewed = await _codex.RenewLeaseIfDueAsync(cancellationToken).ConfigureAwait(true);
            DateTimeOffset? expiry = renewed ?? health.LeaseExpiresAt;

            LeaseStatus = expiry is { } at
                ? $"授权有效至 {at.ToLocalTime():MM-dd HH:mm}"
                : string.Empty;
        }
        catch (RelayApiException ex) when (ex.Failure == RelayFailure.RateLimited)
        {
            ApplyBackoffMessage(_pollingBackoff.RecordRateLimited());
            ClientLog.Warning("Codex 状态监控触发限流，已暂停轮询", ex);
        }
        catch (Exception ex) when (IsCardFailure(ex))
        {
            // Monitoring is background work; it must never be able to interrupt the
            // user or take the panel down.
            ClientLog.Warning("Codex 状态监控失败", ex);
        }
    }

    /// <summary>让卡片刷新和 Codex 监控共用同一轮询与退避预算。</summary>
    public async Task RefreshAndMonitorAsync(CancellationToken cancellationToken = default)
    {
        if (!await _pollGate.WaitAsync(0, cancellationToken).ConfigureAwait(true))
        {
            return;
        }

        try
        {
            await RefreshAsync(cancellationToken).ConfigureAwait(true);
            if (_pollingBackoff.CanAttempt)
            {
                await MonitorCodexAsync(cancellationToken).ConfigureAwait(true);
            }
        }
        finally
        {
            _pollGate.Release();
        }
    }

    [ObservableProperty]
    private string leaseStatus = string.Empty;

    public bool HasLeaseStatus => !string.IsNullOrWhiteSpace(LeaseStatus);

    partial void OnLeaseStatusChanged(string value) => OnPropertyChanged(nameof(HasLeaseStatus));

    /// <summary>
    /// Issues or renews the lease, points Codex at the relay, and launches it (F3).
    /// </summary>
    /// <param name="confirmRestart">
    /// Asked only when Codex is already running without a debug port. Restarting
    /// discards whatever turn the user has in flight, so it is never assumed.
    /// </param>
    public async Task StartCodexAsync(
        Func<string, Task<bool>> confirmRestart,
        CancellationToken cancellationToken = default,
        bool forceRestart = false)
    {
        ArgumentNullException.ThrowIfNull(confirmRestart);

        if (IsStartingCodex || (IsInstallingCodex && !forceRestart))
        {
            return;
        }

        IsStartingCodex = true;
        CodexMessage = "正在准备 ChatGPT…";
        try
        {
            long? groupId = SelectedGroup?.Id;
            string? preferredModel = IsClaudeGroup ? SelectedClaudeModel : null;
            CodexStartupResult result;
            if (forceRestart)
            {
                result = await _codex
                    .RunAsync(
                        groupId,
                        _settings.ApiBaseUrl ?? string.Empty,
                        allowRestart: true,
                        cancellationToken: cancellationToken,
                        preferredModel: preferredModel)
                    .ConfigureAwait(true);
            }
            else if (RequiresCodexAccountRestart)
            {
                const string activationMessage = "当前登录账户与 ChatGPT 已激活账户不同，需要重启 ChatGPT 才能激活当前账户。";
                if (!await confirmRestart(activationMessage).ConfigureAwait(true))
                {
                    CodexMessage = "已取消重启，ChatGPT 仍使用原账户。";
                    return;
                }

                result = await _codex
                    .RunAsync(
                        groupId,
                        _settings.ApiBaseUrl ?? string.Empty,
                        allowRestart: true,
                        cancellationToken: cancellationToken,
                        preferredModel: preferredModel)
                    .ConfigureAwait(true);
            }
            else
            {
                result = await _codex
                    .RunAsync(
                        groupId,
                        _settings.ApiBaseUrl ?? string.Empty,
                        allowRestart: false,
                        cancellationToken: cancellationToken,
                        preferredModel: preferredModel)
                    .ConfigureAwait(true);
            }

            if (result.Status == CodexStartupStatus.NeedsRestartConfirmation &&
                await confirmRestart(result.Message).ConfigureAwait(true))
            {
                result = await _codex
                    .RunAsync(
                        groupId,
                        _settings.ApiBaseUrl ?? string.Empty,
                        allowRestart: true,
                        cancellationToken: cancellationToken,
                        preferredModel: preferredModel)
                    .ConfigureAwait(true);
            }

            CodexMessage = result.Message;

            if (result.Status == CodexStartupStatus.NotInstalled)
            {
                CodexNotInstalled = true;
                CodexInstallerAvailable = _codexInstaller.Inspect().PackageAvailable;
            }

            if (result.Status == CodexStartupStatus.Ready)
            {
                IsCodexRunning = true;
                CodexNotInstalled = false;
                string currentEmail = _session.UserEmail.Trim();
                if (!string.IsNullOrWhiteSpace(currentEmail))
                {
                    _codexAccountStore.Save(currentEmail);
                    RequiresCodexAccountRestart = false;
                }
            }
        }
        catch (Exception ex) when (IsCardFailure(ex))
        {
            // Same rule as the cards: pressing this button must not be able to end
            // the session or take the window down.
            ClientLog.Error("启动 Codex 失败", ex);
            CodexMessage = "启动 ChatGPT 时出错，详情见日志。";
        }
        finally
        {
            IsStartingCodex = false;
        }
    }

    public async Task InstallCodexAsync(CancellationToken cancellationToken = default)
    {
        if (IsInstallingCodex)
        {
            return;
        }

        cancellationToken.ThrowIfCancellationRequested();
        _installationCancellation?.Cancel();
        _installationCancellation?.Dispose();
        _installationCancellation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        IsInstallingCodex = true;
        CodexDownloadProgressText = _codexInstaller.Inspect().PackageAvailable
            ? string.Empty
            : "正在下载 ChatGPT…";
        try
        {
            var progress = new Progress<CodexDownloadProgress>(UpdateCodexDownloadProgress);
            CodexInstallerResult result = await _codexInstaller
                .EnsureAndLaunchAsync(progress, _installationCancellation.Token)
                .ConfigureAwait(true);
            CodexDownloadProgressText = string.Empty;
            CodexMessage = result.Message;
            CodexInstallerAvailable = _codexInstaller.Inspect().PackageAvailable;
            if (!result.Started)
            {
                CodexNotInstalled = true;
                return;
            }

            bool installed = await WaitForCodexInstallationAsync(
                result.InstallerProcess,
                _installationCancellation.Token).ConfigureAwait(true);

            if (!installed)
            {
                CodexNotInstalled = true;
                CodexMessage = "ChatGPT 安装未完成，可以重新点击安装。";
                return;
            }

            CodexNotInstalled = false;
            await StartCodexAsync(
                _ => Task.FromResult(true),
                _installationCancellation.Token,
                forceRestart: true).ConfigureAwait(true);
        }
        catch (OperationCanceledException)
        {
            CodexNotInstalled = true;
            CodexMessage = "ChatGPT 安装已取消，可以重新点击安装。";
        }
        catch (Exception ex) when (IsCardFailure(ex))
        {
            CodexNotInstalled = true;
            CodexMessage = "ChatGPT 安装状态检查失败，可以重新点击安装。";
            ClientLog.Error("监控 Codex 安装失败", ex);
        }
        finally
        {
            IsInstallingCodex = false;
            CodexDownloadProgressText = string.Empty;
            _installationCancellation?.Dispose();
            _installationCancellation = null;
        }
    }

    private void UpdateCodexDownloadProgress(CodexDownloadProgress progress) =>
        CodexDownloadProgressText = progress.Percent is int percent
            ? $"正在下载 ChatGPT… {percent}%"
            : "正在下载 ChatGPT…";

    private async Task<bool> WaitForCodexInstallationAsync(
        System.Diagnostics.Process? installerProcess,
        CancellationToken cancellationToken)
    {
        DateTimeOffset deadline = DateTimeOffset.UtcNow.AddMinutes(10);
        while (DateTimeOffset.UtcNow < deadline)
        {
            if (await _codex.CheckInstalledAsync(cancellationToken).ConfigureAwait(true))
            {
                return true;
            }

            if (installerProcess?.HasExited == true)
            {
                return false;
            }

            await Task.Delay(TimeSpan.FromMilliseconds(500), cancellationToken).ConfigureAwait(true);
        }

        return false;
    }

    // ---- Whole-panel state --------------------------------------------------

    [ObservableProperty]
    private bool isRefreshing;

    /// <summary>Reads the server-driven settings the cards depend on.</summary>
    public void ApplySettings(PublicSettings settings)
    {
        _settings = settings ?? PublicSettings.Conservative;
        RechargeUrl = _settings.BalanceLowNotifyRechargeUrl ?? string.Empty;
        OnPropertyChanged(nameof(CanRecharge));
    }

    /// <summary>
    /// Refreshes every card.
    /// </summary>
    /// <remarks>
    /// The access token is obtained once, up front. That single call is the only
    /// place allowed to end the session, and it does so only when renewal itself
    /// fails. If it throws, no card is loaded — but the user is not signed out for
    /// a mere network failure either, because renewal keeps the session on
    /// <see cref="RelayFailure.NetworkUnreachable"/>.
    /// </remarks>
    public async Task RefreshAsync(CancellationToken cancellationToken = default)
    {
        if (IsRefreshing)
        {
            return;
        }

        if (!_pollingBackoff.CanAttempt)
        {
            ApplyBackoffMessage(_pollingBackoff.Remaining);
            return;
        }

        _refreshHadFailure = false;
        _refreshWasRateLimited = false;

        // Linked so a sign-out can abandon this refresh; without it the guard above
        // would still be set when the next user signs in, and their load would be
        // silently dropped.
        _refreshCancellation?.Dispose();
        _refreshCancellation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        CancellationToken cancellation = _refreshCancellation.Token;

        IsRefreshing = true;
        try
        {
            UserDisplayName = _session.UserDisplayName;

            string accessToken;
            try
            {
                accessToken = await _session.GetAccessTokenAsync(cancellation).ConfigureAwait(true);
            }
            catch (Exception ex) when (ex is RelayApiException or OperationCanceledException)
            {
                // Either the session ended (the manager has already signed out and
                // raised its event), the network is down, or a sign-out cancelled
                // us. Cards stay greyed; nothing here decides to sign anyone out.
                MarkAllUnavailable();
                ObserveRefreshFailure(ex);
                return;
            }

            // Sequential rather than concurrent: the panel endpoints share a
            // per-user rate limiter, and four simultaneous calls every 60 seconds
            // is the pattern most likely to trip it. Each still fails alone.
            await LoadAccountCardAsync(accessToken, cancellation).ConfigureAwait(true);
            if (StopForRateLimit())
            {
                return;
            }

            await LoadUsageCardAsync(accessToken, cancellation).ConfigureAwait(true);
            if (StopForRateLimit())
            {
                return;
            }

            await LoadSubscriptionCardAsync(accessToken, cancellation).ConfigureAwait(true);
            if (StopForRateLimit())
            {
                return;
            }

            await LoadGroupCardAsync(accessToken, cancellation).ConfigureAwait(true);
            if (StopForRateLimit())
            {
                return;
            }

            await LoadTrendCardAsync(accessToken, cancellation).ConfigureAwait(true);
            if (StopForRateLimit())
            {
                return;
            }

            if (!_refreshHadFailure)
            {
                _pollingBackoff.RecordSuccess();
                IsRateLimited = false;
                RefreshMessage = string.Empty;
            }
        }
        finally
        {
            IsRefreshing = false;
        }
    }

    /// <summary>
    /// Whether a card's failure is one the panel absorbs by greying that card.
    /// </summary>
    /// <remarks>
    /// F4.2 forbids one card taking down the page, and a <c>catch</c> narrowed to
    /// <see cref="RelayApiException"/> does not deliver that: any other escape —
    /// a cancellation, a serialization fault, a bug in a mapper — would propagate
    /// out of the loader and abandon the cards queued behind it. The filter is
    /// deliberately broad, and deliberately still excludes the exceptions that
    /// indicate the process itself is unsound.
    /// </remarks>
    private static bool IsCardFailure(Exception ex) =>
        ex is not (OutOfMemoryException or StackOverflowException or ThreadAbortException);

    private bool ObserveRefreshFailure(Exception ex)
    {
        bool isCardFailure = IsCardFailure(ex);
        if (!isCardFailure)
        {
            return false;
        }

        _refreshHadFailure = true;
        if (ex is RelayApiException { Failure: RelayFailure.RateLimited })
        {
            _refreshWasRateLimited = true;
        }

        return true;
    }

    private bool StopForRateLimit()
    {
        if (!_refreshWasRateLimited)
        {
            return false;
        }

        ApplyBackoffMessage(_pollingBackoff.RecordRateLimited());
        return true;
    }

    private void ApplyBackoffMessage(TimeSpan remaining)
    {
        int minutes = Math.Max(1, (int)Math.Ceiling(remaining.TotalMinutes));
        IsRateLimited = true;
        RefreshMessage = $"请求频繁，请在约 {minutes} 分钟后重试。";
    }

    private async Task LoadAccountCardAsync(string token, CancellationToken cancellationToken)
    {
        try
        {
            RelayUser user = await _client.GetCurrentUserAsync(token, cancellationToken).ConfigureAwait(true);

            UserDisplayName = user.DisplayName;
            BalanceText = FormatBalance(user.Balance);
            FrozenBalanceText = FormatBalance(user.FrozenBalance);

            // The threshold is the server's to decide (F4.3); the client must not
            // invent one, or operators changing it would need a client release.
            BalanceIsLow = _settings.BalanceLowNotifyEnabled &&
                           user.Balance < _settings.BalanceLowNotifyThreshold;

            AccountReady = true;
        }
        catch (Exception ex) when (ObserveRefreshFailure(ex))
        {
            BalanceText = "—";
            FrozenBalanceText = "—";
            BalanceIsLow = false;
            AccountReady = false;
            ClientLog.Warning("账户卡取数失败", ex);
        }
    }

    private async Task LoadUsageCardAsync(string token, CancellationToken cancellationToken)
    {
        try
        {
            DashboardStats stats = await _client.GetDashboardStatsAsync(token, cancellationToken).ConfigureAwait(true);

            TodayRequestsText = stats.TodayRequests.ToString("N0");
            TodayTokensText = stats.TodayTokens.ToString("N0");
            TodayCostText = FormatMoney(stats.TodayActualCost);
            UsageReady = true;
        }
        catch (Exception ex) when (ObserveRefreshFailure(ex))
        {
            TodayRequestsText = "—";
            TodayTokensText = "—";
            TodayCostText = "—";
            UsageReady = false;
            ClientLog.Warning("用量卡取数失败", ex);
        }
    }

    private async Task LoadSubscriptionCardAsync(string token, CancellationToken cancellationToken)
    {
        try
        {
            IReadOnlyList<SubscriptionSummaryItem> subscriptions = await _client
                .GetSubscriptionSummaryAsync(token, cancellationToken)
                .ConfigureAwait(true);
            SubscriptionSummaryItem? subscription = subscriptions.FirstOrDefault();

            if (subscription is null)
            {
                SubscriptionName = string.Empty;
                SubscriptionProgressText = string.Empty;
            }
            else
            {
                SubscriptionName = string.IsNullOrWhiteSpace(subscription.GroupName)
                    ? "订阅"
                    : subscription.GroupName;
                SubscriptionProgressText = FormatSubscriptionProgress(subscription);
            }

            SubscriptionReady = true;
            OnPropertyChanged(nameof(HasSubscription));
        }
        catch (Exception ex) when (ObserveRefreshFailure(ex))
        {
            SubscriptionName = string.Empty;
            SubscriptionProgressText = string.Empty;
            SubscriptionReady = false;
            OnPropertyChanged(nameof(HasSubscription));
            ClientLog.Warning("订阅卡取数失败", ex);
        }
    }

    private static string FormatSubscriptionProgress(SubscriptionSummaryItem subscription)
    {
        if (subscription.MonthlyLimitUsd > 0)
        {
            return $"{FormatMoney(subscription.MonthlyUsedUsd)} / {FormatMoney(subscription.MonthlyLimitUsd)} 本月";
        }

        if (subscription.WeeklyLimitUsd > 0)
        {
            return $"{FormatMoney(subscription.WeeklyUsedUsd)} / {FormatMoney(subscription.WeeklyLimitUsd)} 本周";
        }

        if (subscription.DailyLimitUsd > 0)
        {
            return $"{FormatMoney(subscription.DailyUsedUsd)} / {FormatMoney(subscription.DailyLimitUsd)} 今日";
        }

        return "使用中";
    }

    private async Task LoadGroupCardAsync(string token, CancellationToken cancellationToken)
    {
        try
        {
            IReadOnlyList<RelayGroup> groups =
                await _client.GetAvailableGroupsAsync(token, cancellationToken).ConfigureAwait(true);

            // The rates call is allowed to fail on its own: without it every group
            // simply shows its default multiplier, which is still true for anyone
            // without a personal deal. Losing the whole group list instead would
            // also cost the user the ability to switch.
            IReadOnlyDictionary<long, double> rates;
            try
            {
                rates = await _client.GetUserGroupRatesAsync(token, cancellationToken).ConfigureAwait(true);
            }
            catch (Exception ex) when (ObserveRefreshFailure(ex))
            {
                rates = new Dictionary<long, double>();
                ClientLog.Warning("专属倍率取数失败，按分组默认倍率显示", ex);
            }

            if (_refreshWasRateLimited)
            {
                GroupsReady = false;
                return;
            }

            await IdentifyManagedKeyAsync(token, cancellationToken).ConfigureAwait(true);

            long? current = _managedKey?.GroupId ?? _preferences.Load();

            Groups.Clear();
            foreach (RelayGroup group in groups.Where(g => IsSelectable(g, current)))
            {
                var item = new GroupItemViewModel(group, GroupRate.Resolve(group, rates), _settings.ServerUtcOffset)
                {
                    IsCurrent = current == group.Id,
                };
                Groups.Add(item);
            }

            GroupItemViewModel? inForce = Groups.FirstOrDefault(g => g.IsCurrent);

            // A first-time account has neither a managed key nor a local choice.
            // Select the first server-approved group so Codex has a usable billing
            // target immediately after registration, then persist that choice for
            // the next refresh.
            if (inForce is null && Groups.Count > 0)
            {
                await SwitchGroupAsync(Groups[0], cancellationToken).ConfigureAwait(true);
            }
            else
            {
                // The dropdown opens on the group actually in force rather than on
                // nothing, so the answer to "which one am I on" needs no interaction.
                SelectWithoutSwitching(inForce);
                ApplyCurrentLabels(inForce);
                if (IsClaudeGroup) _ = _safeAsync.RunAsync(LoadClaudePreferenceAsync);
            }

            GroupsReady = true;
        }
        catch (Exception ex) when (ObserveRefreshFailure(ex))
        {
            GroupsReady = false;
            ClientLog.Warning("分组卡取数失败", ex);
        }
    }

    /// <summary>
    /// Platforms whose groups can actually serve Codex traffic (F5.3).
    /// </summary>
    /// <remarks>
    /// <para>
    /// The relay supports OpenAI-compatible groups and the Claude-over-Codex
    /// bridge. Gemini and other unrelated platforms would fail every request.
    /// </para>
    /// <para>
    /// <c>composite</c> is deliberately excluded. Such a group aggregates several
    /// upstreams, so whether it can serve Codex depends on its routing
    /// configuration — which the user-facing group payload does not carry (it is
    /// an admin-only field). Including it on the strength of the platform label
    /// would put back exactly the kind of entry F5.3 exists to remove: one that
    /// looks selectable and then fails every request. It can be allowed once
    /// there is something in the response a client can actually evaluate.
    /// </para>
    /// </remarks>
    private static readonly string[] CodexPlatforms = ["openai", "anthropic", "claude"];

    /// <summary>
    /// Whether a group belongs in the switcher.
    /// </summary>
    /// <remarks>
    /// Narrows the server's list by platform, which F5.3 permits — it never widens
    /// it, which F5.3 forbids. The group currently in force is kept regardless:
    /// hiding it would leave the user unable to see what they are actually billed
    /// on, and this client cannot assume every account was set up for Codex.
    /// </remarks>
    private static bool IsSelectable(RelayGroup group, long? currentGroupId) =>
        group.Id == currentGroupId ||
        CodexPlatforms.Contains(group.Platform, StringComparer.OrdinalIgnoreCase);

    /// <remarks>
    /// Read-only (F3.2.1). A failure here leaves <c>_managedKey</c> null, which
    /// only means switching falls back to recording the choice locally — the group
    /// list itself must still render.
    /// </remarks>
    private async Task IdentifyManagedKeyAsync(string token, CancellationToken cancellationToken)
    {
        try
        {
            IReadOnlyList<RelayApiKey> keys = await _client.ListApiKeysAsync(token, cancellationToken).ConfigureAwait(true);
            _managedKey = _keyNaming.FindCurrent(keys);
        }
        catch (Exception ex) when (ObserveRefreshFailure(ex))
        {
            _managedKey = null;
            ClientLog.Warning("托管 key 识别失败，分组切换将只记录在本地", ex);
        }
    }

    /// <summary>
    /// Switches to <paramref name="group"/> (F5.4), rolling the selection back on failure (F5.5).
    /// </summary>
    /// <remarks>
    /// With no managed key yet the choice is recorded locally instead. That is a
    /// real branch, not a stub: the key is created with its group already set
    /// (F3.2.2), so the selection has to exist before the key does.
    /// </remarks>
    public async Task SwitchGroupAsync(GroupItemViewModel group, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(group);

        GroupItemViewModel? previous = Groups.FirstOrDefault(g => g.IsCurrent);
        if (previous == group)
        {
            return;
        }

        SetCurrent(group);

        // Keeps the dropdown in step when the switch was started from elsewhere
        // (a tray menu later, or a test) rather than from the dropdown itself.
        SelectWithoutSwitching(group);
        GroupMessage = string.Empty;

        if (_managedKey is null)
        {
            _preferences.Save(group.Id);
            GroupMessage = $"已选择 {group.Name}，将在授权生效时套用。";
            if (IsClaudeGroup) _ = _safeAsync.RunAsync(LoadClaudePreferenceAsync);
            return;
        }

        try
        {
            string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);
            RelayApiKey updated = await _client
                .UpdateApiKeyGroupAsync(token, _managedKey.Id, group.Id, cancellationToken)
                .ConfigureAwait(true);

            _managedKey = updated;
            _preferences.Save(group.Id);
            GroupMessage = $"已切换到 {group.Name}。";
            if (IsClaudeGroup) _ = _safeAsync.RunAsync(LoadClaudePreferenceAsync);
        }
        catch (RelayApiException ex)
        {
            // The server refused — the group may have been retired or the
            // subscription lapsed. Leaving the dropdown on the new group would tell
            // the user their traffic is being billed somewhere it is not.
            if (previous is not null)
            {
                SetCurrent(previous);
            }
            else
            {
                group.IsCurrent = false;
                ApplyCurrentLabels(null);
            }

            SelectWithoutSwitching(previous);
            GroupMessage = ex.UserMessage;
        }
    }

    private void SetCurrent(GroupItemViewModel group)
    {
        foreach (GroupItemViewModel item in Groups)
        {
            item.IsCurrent = item == group;
        }

        ApplyCurrentLabels(group);
    }

    private void ApplyCurrentLabels(GroupItemViewModel? group)
    {
        CurrentGroupName = group?.Name ?? "未选择";
        CurrentGroupRate = group?.RateLabel ?? string.Empty;
        CurrentGroupRateDescription = group?.RateDescription ?? string.Empty;
    }

    private void MarkAllUnavailable()
    {
        AccountReady = false;
        UsageReady = false;
        GroupsReady = false;
    }

    private void UpdateCodexAccountActivationState()
    {
        string currentEmail = _session.UserEmail.Trim();
        string? activatedEmail = _codexAccountStore.Load();

        RequiresCodexAccountRestart = IsCodexRunning &&
            !string.IsNullOrWhiteSpace(currentEmail) &&
            !string.IsNullOrWhiteSpace(activatedEmail) &&
            !string.Equals(currentEmail, activatedEmail.Trim(), StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>
    /// Drops everything belonging to the account that just signed out.
    /// </summary>
    /// <remarks>
    /// Without this, signing out and back in as someone else leaves the previous
    /// user's balance, usage and group on screen until the next poll — one account
    /// showing another's figures, which is a disclosure, not just a stale view.
    /// The in-flight refresh is cancelled for the same reason: its continuation
    /// would otherwise write the old user's values into the cards after the new
    /// one has signed in.
    /// </remarks>
    public void Reset()
    {
        _installationCancellation?.Cancel();
        _installationCancellation?.Dispose();
        _installationCancellation = null;
        IsInstallingCodex = false;

        _refreshCancellation?.Cancel();
        _refreshCancellation?.Dispose();
        _refreshCancellation = null;

        IsRefreshing = false;
        _managedKey = null;
        _pollingBackoff.RecordSuccess();
        IsRateLimited = false;
        RefreshMessage = string.Empty;

        UserDisplayName = string.Empty;
        BalanceText = "—";
        FrozenBalanceText = "—";
        BalanceIsLow = false;
        TodayRequestsText = "—";
        TodayTokensText = "—";
        TodayCostText = "—";
        SubscriptionName = string.Empty;
        SubscriptionProgressText = string.Empty;
        SubscriptionReady = false;
        OnPropertyChanged(nameof(HasSubscription));
        GroupMessage = string.Empty;
        RequiresCodexAccountRestart = false;

        Groups.Clear();
        CostTrend.Clear();
        TopModelUsage.Clear();
        TrendReady = false;
        SelectWithoutSwitching(null);
        ApplyCurrentLabels(null);
        MarkAllUnavailable();
    }

    private static string FormatBalance(double value) =>
        "￥" + value.ToString("0.####", System.Globalization.CultureInfo.InvariantCulture);

    private static string FormatMoney(double value) =>
        "$" + value.ToString("0.####", System.Globalization.CultureInfo.InvariantCulture);
}
