using System.Collections.Concurrent;
using System.Collections.ObjectModel;
using System.Diagnostics;
using System.Globalization;
using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;
using Microsoft.Win32;

namespace LanAi.Workspace.Wpf.ViewModels;

public partial class AccountCenterViewModel : PageViewModel, IDisposable
{
    internal const int PageSize = 10;
    private const int BackendPageSize = 100;
    internal static readonly TimeSpan CacheLifetime = TimeSpan.FromMinutes(10);
    private const int UsageConcurrency = 4;
    private const int BatchConcurrency = 4;
    private const int MaxOAuthAccountsPerRequest = 10;

    private readonly ISub2ApiSessionManager _sessionManager;
    private readonly ISub2ApiAccountCenterClient _client;
    private readonly bool _ownsClient;
    private readonly Func<AccountCenterAccountViewModel, bool> _confirmDelete;
    private readonly Func<IReadOnlyList<AccountCenterAccountViewModel>, bool> _confirmBatchDelete;
    private readonly Func<AccountCenterOAuthDocuments?> _selectOAuthDocuments;
    private readonly Action<string> _openExternalUrl;
    private readonly Func<string?> _localControlTokenProvider;
    private readonly SemaphoreSlim _loadGate = new(1, 1);
    private readonly ConcurrentDictionary<string, CachedUsage> _usageCache = new(StringComparer.OrdinalIgnoreCase);
    private readonly List<AccountCenterAccountViewModel> _allAccounts = [];
    private Sub2ApiEndpointTarget? _activeTarget;
    private Sub2ApiSessionAccess? _activeAccess;
    private AccountCenterEditOptions? _editOptions;
    private DateTimeOffset _editOptionsLoadedAt;
    private DateTimeOffset _lastLoadedAt;
    private bool _hasLoaded;
    private bool _syncingSelection;
    private bool _disposed;
    private CancellationTokenSource? _testCancellation;

    internal AccountCenterViewModel(
        ISub2ApiSessionManager sessionManager,
        ISub2ApiAccountCenterClient? client = null,
        Func<AccountCenterAccountViewModel, bool>? confirmDelete = null,
        Func<IReadOnlyList<AccountCenterAccountViewModel>, bool>? confirmBatchDelete = null,
        Func<AccountCenterOAuthDocuments?>? selectOAuthDocuments = null,
        Action<string>? openExternalUrl = null,
        Func<string?>? localControlTokenProvider = null)
        : base("账号中心", "管理和添加本机后台中的个人账号。")
    {
        _sessionManager = sessionManager ?? throw new ArgumentNullException(nameof(sessionManager));
        _client = client ?? new Sub2ApiAccountCenterClient();
        _ownsClient = client is null;
        _confirmDelete = confirmDelete ?? ShowDeleteConfirmation;
        _confirmBatchDelete = confirmBatchDelete ?? ShowBatchDeleteConfirmation;
        _selectOAuthDocuments = selectOAuthDocuments ?? SelectOAuthDocuments;
        _openExternalUrl = openExternalUrl ?? OpenExternalUrl;
        _localControlTokenProvider = localControlTokenProvider ?? (() => null);
        SelectedStatusFilter = StatusFilterOptions[0];
        SelectedPlatformFilter = PlatformFilterOptions[0];
        SelectedAddPlatform = AddPlatformOptions[0];
        SelectedAddMode = AddModeOptions[0];
        SelectedOpenAiMethod = OpenAiMethodOptions[0];
        SelectedTestMode = TestModeOptions[0];
        _sessionManager.SessionChanged += OnSessionChanged;
    }

    public ObservableCollection<AccountCenterAccountViewModel> Accounts { get; } = [];
    public ObservableCollection<AccountCenterProxyOptionViewModel> ProxyOptions { get; } = [];
    public ObservableCollection<AccountCenterProxyOptionViewModel> AddProxyOptions { get; } = [];
    public ObservableCollection<AccountCenterFilterOption> PlatformFilterOptions { get; } = [new("all", "全部平台")];
    public ObservableCollection<AccountCenterTestModel> TestModels { get; } = [];
    public ObservableCollection<AccountCenterTestLogLine> TestLogLines { get; } = [];
    public ObservableCollection<AccountCenterTestImageViewModel> TestImages { get; } = [];
    public IReadOnlyList<AccountCenterFilterOption> StatusFilterOptions { get; } =
    [
        new("all", "全部状态"),
        new("available", "可用"),
        new("attention", "需处理"),
        new("disabled", "已停用"),
    ];
    public IReadOnlyList<AccountCenterFilterOption> AddPlatformOptions { get; } =
    [
        new("openai", "OpenAI / Codex"),
        new("anthropic", "Anthropic / Claude"),
        new("gemini", "Gemini"),
        new("grok", "Grok"),
    ];
    public IReadOnlyList<AccountCenterFilterOption> AddModeOptions { get; } =
    [new("oauth", "OAuth / 凭据导入"), new("api_key", "API Key")];
    public IReadOnlyList<AccountCenterFilterOption> OpenAiMethodOptions { get; } =
    [
        new("codex_session", "Codex 会话文件或文本"),
        new("manual", "浏览器授权回调"),
        new("refresh_token", "Refresh Token"),
        new("mobile_refresh_token", "移动端 Refresh Token"),
        new("codex_pat", "Codex PAT"),
    ];
    public IReadOnlyList<AccountCenterFilterOption> TestModeOptions { get; } =
    [
        new("default", "标准测试"),
        new("compact", "轻量测试"),
    ];
    public IReadOnlyList<AccountCenterFilterOption> AddProxyProtocolOptions { get; } =
    [
        new("http", "HTTP"),
        new("https", "HTTPS"),
        new("socks5", "SOCKS5"),
        new("socks5h", "SOCKS5H"),
    ];

    [ObservableProperty] private bool isBusy;
    [ObservableProperty] private string sourceName = "尚未选择来源";
    [ObservableProperty] private string statusMessage = "进入页面后读取当前来源中的账号。";
    [ObservableProperty] private string updatedLabel = "尚未同步";
    [ObservableProperty] private int totalAccounts;
    [ObservableProperty] private int activeAccounts;
    [ObservableProperty] private int attentionAccounts;
    [ObservableProperty] private int currentPageNumber = 1;
    [ObservableProperty] private int totalPages = 1;
    [ObservableProperty] private int filteredAccountCount;
    [ObservableProperty] private string accountSearchText = string.Empty;
    [ObservableProperty] private AccountCenterFilterOption? selectedStatusFilter;
    [ObservableProperty] private AccountCenterFilterOption? selectedPlatformFilter;
    [ObservableProperty] private int selectedAccountCount;
    [ObservableProperty] private bool isAllVisibleSelected;
    [ObservableProperty] private bool hasAccounts;
    [ObservableProperty] private bool showEmptyState = true;
    [ObservableProperty] private AccountCenterAccountViewModel? editingAccount;
    [ObservableProperty] private bool isEditing;
    [ObservableProperty] private bool isBatchEditing;
    [ObservableProperty] private string editName = string.Empty;
    [ObservableProperty] private int editConcurrency = 1;
    [ObservableProperty] private int editLoadFactor = 1;
    [ObservableProperty] private int editPriority = 20;
    [ObservableProperty] private AccountCenterProxyOptionViewModel? selectedProxy;
    [ObservableProperty] private string editValidationMessage = string.Empty;
    [ObservableProperty] private bool isAdding;
    [ObservableProperty] private bool isAddBusy;
    [ObservableProperty] private AccountCenterFilterOption? selectedAddPlatform;
    [ObservableProperty] private AccountCenterFilterOption? selectedAddMode;
    [ObservableProperty] private AccountCenterFilterOption? selectedOpenAiMethod;
    [ObservableProperty] private AccountCenterProxyOptionViewModel? selectedAddProxy;
    [ObservableProperty] private string addName = string.Empty;
    [ObservableProperty] private string addApiKey = string.Empty;
    [ObservableProperty] private string addBaseUrl = string.Empty;
    [ObservableProperty] private string addOAuthText = string.Empty;
    [ObservableProperty] private string addOAuthFilesLabel = "尚未选择文件";
    [ObservableProperty] private int addConcurrency = 30;
    [ObservableProperty] private int addLoadFactor;
    [ObservableProperty] private int addPriority;
    [ObservableProperty] private string addTestModelId = string.Empty;
    [ObservableProperty] private string addValidationMessage = string.Empty;
    [ObservableProperty] private string addOpenAiAuthUrl = string.Empty;
    [ObservableProperty] private string addOpenAiAuthSessionId = string.Empty;
    [ObservableProperty] private bool isAddProxyEditorOpen;
    [ObservableProperty] private bool isAddProxyBusy;
    [ObservableProperty] private string addProxyName = string.Empty;
    [ObservableProperty] private AccountCenterFilterOption? selectedAddProxyProtocol;
    [ObservableProperty] private string addProxyHost = string.Empty;
    [ObservableProperty] private int addProxyPort = 10808;
    [ObservableProperty] private string addProxyUsername = string.Empty;
    [ObservableProperty] private string addProxyPassword = string.Empty;
    [ObservableProperty] private string addProxyValidationMessage = string.Empty;
    [ObservableProperty] private bool isTestDialogOpen;
    [ObservableProperty] private bool isTestLoadingModels;
    [ObservableProperty] private bool isTestRunning;
    [ObservableProperty] private bool testSucceeded;
    [ObservableProperty] private bool testFailed;
    [ObservableProperty] private AccountCenterAccountViewModel? testingAccount;
    [ObservableProperty] private AccountCenterTestModel? selectedTestModel;
    [ObservableProperty] private AccountCenterFilterOption? selectedTestMode;
    [ObservableProperty] private string testPrompt = string.Empty;
    [ObservableProperty] private string testStatusMessage = "请选择模型后开始测试。";
    private IReadOnlyList<string> _addOAuthDocuments = [];

    public string PageLabel => $"第 {CurrentPageNumber} / {TotalPages} 页";
    public string AddProxyEditorToggleLabel => IsAddProxyEditorOpen ? "收起" : "添加代理";
    public bool CanGoPrevious => !IsBusy && CurrentPageNumber > 1;
    public bool CanGoNext => !IsBusy && CurrentPageNumber < TotalPages;
    public string FilteredSummaryLabel => $"{FilteredAccountCount:N0} 个结果";
    public bool HasSelection => SelectedAccountCount > 0;
    public string SelectionLabel => $"已选择 {SelectedAccountCount:N0} 个账号";
    public string EditTitle => IsBatchEditing ? "批量修改账号" : "编辑账号";
    public string EditSubtitle => IsBatchEditing
        ? $"将设置应用到 {SelectedAccountCount:N0} 个已选账号"
        : EditingAccount?.IdentityLabel ?? string.Empty;
    public bool ShowSingleAccountName => !IsBatchEditing;
    public bool IsAddOpenAi => string.Equals(SelectedAddPlatform?.Id, "openai", StringComparison.Ordinal);
    public bool IsAddApiKeyMode => string.Equals(SelectedAddMode?.Id, "api_key", StringComparison.Ordinal);
    public bool IsAddOpenAiOAuth => IsAddOpenAi && !IsAddApiKeyMode;
    public bool UsesAddOAuthDocuments => IsAddOpenAiOAuth && string.Equals(SelectedOpenAiMethod?.Id, "codex_session", StringComparison.Ordinal);
    public bool UsesAddManualCallback => IsAddOpenAiOAuth && string.Equals(SelectedOpenAiMethod?.Id, "manual", StringComparison.Ordinal);
    public bool UsesAddSecret => IsAddApiKeyMode || IsAddOpenAiOAuth && !UsesAddOAuthDocuments && !UsesAddManualCallback;
    public string AddSecretLabel => IsAddApiKeyMode
        ? "API Key"
        : string.Equals(SelectedOpenAiMethod?.Id, "codex_pat", StringComparison.Ordinal)
            ? "Codex PAT"
            : "Refresh Token";
    public bool IsTestingOpenAi => string.Equals(TestingAccount?.Platform, "openai", StringComparison.OrdinalIgnoreCase);
    public bool CanStartDetailedTest => !IsTestLoadingModels && !IsTestRunning && SelectedTestModel is not null;
    public string DetailedTestButtonLabel => IsTestRunning ? "正在测试" : TestSucceeded || TestFailed ? "重新测试" : "开始测试";
    public bool HasTestImages => TestImages.Count > 0;

    internal void ApplyConnections(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileSelection? selection,
        ConnectionProfileRouting? routing)
    {
        ConnectionProfile? localProfile = connections.FirstOrDefault(connection =>
            string.Equals(connection.Id, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase));
        Sub2ApiEndpointTarget? next = localProfile is not null && Sub2ApiEndpointSelector.TryCreate(localProfile, out Sub2ApiEndpointTarget? localTarget)
            ? localTarget
            : null;
        bool changed = !SameTarget(_activeTarget, next);
        _activeTarget = next;
        SourceName = next is null ? "本机中转未配置" : "本机后台";
        if (!changed) return;

        _activeAccess = null;
        _editOptions = null;
        _hasLoaded = false;
        CurrentPageNumber = 1;
        _allAccounts.Clear();
        Accounts.Clear();
        ApplyCollectionState();
        StatusMessage = next is null
            ? "请先在设置中配置本机后台。"
            : "个人账号由本机后台统一调度。";
        UpdatedLabel = "等待同步";
    }

    internal async Task ActivateAsync()
    {
        if (_disposed || IsBusy) return;
        if (_hasLoaded && DateTimeOffset.UtcNow - _lastLoadedAt < CacheLifetime)
        {
            UpdatedLabel = $"使用缓存 · {_lastLoadedAt.ToLocalTime():HH:mm}";
            return;
        }
        await LoadPageAsync(CurrentPageNumber, false).ConfigureAwait(true);
    }

    [RelayCommand]
    private async Task RefreshAsync()
    {
        _hasLoaded = false;
        await LoadPageAsync(CurrentPageNumber, true).ConfigureAwait(true);
    }

    [RelayCommand]
    private async Task BeginAddAsync()
    {
        if (IsBusy || IsAddBusy)
        {
            StatusMessage = IsBusy ? "账号列表正在同步，请稍后再添加。" : "正在准备添加账号，请稍候。";
            return;
        }
        IsAddBusy = true;
        AddValidationMessage = string.Empty;
        try
        {
            _activeAccess = await RequireAccessAsync().ConfigureAwait(true);
            if (_editOptions is null || DateTimeOffset.UtcNow - _editOptionsLoadedAt >= CacheLifetime)
            {
                _editOptions = await _client.GetEditOptionsAsync(_activeAccess, CancellationToken.None).ConfigureAwait(true);
                _editOptionsLoadedAt = DateTimeOffset.UtcNow;
            }
            ResetAddForm();
            // Start new imports on the first active proxy; direct connection remains an explicit choice.
            PopulateAddOptions(-1);
            IsAdding = true;
        }
        catch (Sub2ApiSessionException exception)
        {
            ApplySessionFailure(exception.Failure);
            AddValidationMessage = DescribeSessionFailure(exception.Failure);
        }
        catch (AccountCenterClientException exception)
        {
            StatusMessage = DescribeClientFailure(exception);
        }
        finally
        {
            IsAddBusy = false;
        }
    }

    [RelayCommand]
    private void CancelAdd()
    {
        IsAdding = false;
        ResetAddSecrets();
        ResetAddProxyForm();
        AddValidationMessage = string.Empty;
    }

    [RelayCommand]
    private void ToggleAddProxyEditor()
    {
        IsAddProxyEditorOpen = !IsAddProxyEditorOpen;
        AddProxyValidationMessage = string.Empty;
    }

    [RelayCommand]
    private async Task CreateAddProxyAsync()
    {
        if (IsAddProxyBusy || !ValidateAddProxyForm()) return;
        IsAddProxyBusy = true;
        AddProxyValidationMessage = string.Empty;
        try
        {
            Sub2ApiSessionAccess access = await RequireAccessAsync().ConfigureAwait(true);
            _activeAccess = access;
            AccountCenterProxy created = await _client.CreateProxyAsync(
                access,
                new AccountCenterProxyCreateRequest(
                    AddProxyName,
                    SelectedAddProxyProtocol?.Id ?? "http",
                    AddProxyHost,
                    AddProxyPort,
                    AddProxyUsername,
                    AddProxyPassword),
                CancellationToken.None).ConfigureAwait(true);
            _editOptions = await _client.GetEditOptionsAsync(access, CancellationToken.None).ConfigureAwait(true);
            _editOptionsLoadedAt = DateTimeOffset.UtcNow;
            PopulateAddOptions(created.Id);
            ResetAddProxyForm();
            AddValidationMessage = $"代理“{created.Name}”已添加并选中。";
        }
        catch (Sub2ApiSessionException exception)
        {
            AddProxyValidationMessage = DescribeSessionFailure(exception.Failure);
        }
        catch (AccountCenterClientException exception)
        {
            AddProxyValidationMessage = DescribeClientFailure(exception);
        }
        finally
        {
            IsAddProxyBusy = false;
        }
    }

    [RelayCommand]
    private void SelectAddOAuthFiles()
    {
        try
        {
            AccountCenterOAuthDocuments? documents = _selectOAuthDocuments();
            if (documents is null) return;
            _addOAuthDocuments = documents.Contents;
            int candidateCount = CountOAuthDocumentCandidates(documents.Contents);
            AddOAuthFilesLabel = candidateCount > 0
                ? $"{documents.Label} · 识别到 {candidateCount:N0} 个账号"
                : documents.Label;
            AddValidationMessage = string.Empty;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            AddValidationMessage = $"读取凭据文件失败：{exception.Message}";
        }
    }

    [RelayCommand]
    private async Task GenerateOpenAiAuthorizationAsync()
    {
        if (!IsAddOpenAiOAuth || _activeAccess is null || IsAddBusy) return;
        IsAddBusy = true;
        AddValidationMessage = string.Empty;
        try
        {
            AccountCenterOpenAiAuthSession session = await _client.GenerateOpenAiAuthAsync(
                _activeAccess,
                SelectedAddProxy?.Id ?? 0,
                CancellationToken.None).ConfigureAwait(true);
            AddOpenAiAuthUrl = session.AuthUrl;
            AddOpenAiAuthSessionId = session.SessionId;
            _openExternalUrl(session.AuthUrl);
            AddValidationMessage = "授权页面已打开。完成授权后，把回调地址粘贴到下方。";
        }
        catch (AccountCenterClientException exception)
        {
            AddValidationMessage = DescribeClientFailure(exception);
        }
        catch (Exception exception) when (exception is InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            AddValidationMessage = $"授权地址已生成，但浏览器打开失败：{exception.Message}";
        }
        finally
        {
            IsAddBusy = false;
        }
    }

    [RelayCommand]
    private void OpenGeneratedAuthorization()
    {
        if (string.IsNullOrWhiteSpace(AddOpenAiAuthUrl)) return;
        try
        {
            _openExternalUrl(AddOpenAiAuthUrl);
        }
        catch (Exception exception) when (exception is InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            AddValidationMessage = $"无法打开授权页面：{exception.Message}";
        }
    }

    [RelayCommand]
    private async Task SaveAddAsync()
    {
        if (IsAddBusy || !ValidateAddForm()) return;
        IsAddBusy = true;
        AddValidationMessage = string.Empty;
        try
        {
            Sub2ApiSessionAccess access = await RequireAccessAsync().ConfigureAwait(true);
            _activeAccess = access;
            AccountCenterCreateResult result;
            int skippedExistingAccounts = 0;
            long[] groupIds = [];
            long proxyId = SelectedAddProxy?.Id ?? 0;
            if (IsAddApiKeyMode)
            {
                result = await _client.CreateAsync(
                    access,
                    new AccountCenterCreateRequest(
                        "api_key", AddName, SelectedAddPlatform?.Id ?? string.Empty, AddApiKey, AddBaseUrl, [],
                        AddConcurrency, AddLoadFactor, 0, groupIds, proxyId, AddTestModelId),
                    CancellationToken.None).ConfigureAwait(true);
            }
            else if (UsesAddOAuthDocuments)
            {
                List<string> contents = [.. _addOAuthDocuments];
                if (!string.IsNullOrWhiteSpace(AddOAuthText)) contents.Add(AddOAuthText.Trim());
                AccountCenterPage existingPage = await LoadAllAccountsAsync(access).ConfigureAwait(true);
                PreparedOAuthDocuments prepared = PrepareOAuthDocuments(
                    contents,
                    existingPage.Items
                        .Where(account => string.Equals(account.Platform, "openai", StringComparison.OrdinalIgnoreCase))
                        .Select(account => account.Name));
                if (prepared.Contents.Count == 0)
                {
                    AddValidationMessage = prepared.SkippedExisting > 0
                        ? $"文件中的 {prepared.SkippedExisting:N0} 个账号已存在，无需重复导入。"
                        : "没有读取到可导入的账号凭据。";
                    return;
                }
                skippedExistingAccounts = prepared.SkippedExisting;
                result = await CreateOAuthDocumentsAsync(
                    access,
                    prepared.Contents,
                    groupIds,
                    proxyId).ConfigureAwait(true);
            }
            else
            {
                var request = new AccountCenterOpenAiCreateRequest(
                    AddName, AddConcurrency, AddLoadFactor, 0, groupIds, proxyId, AddTestModelId);
                result = await CreateOpenAiCredentialAsync(request).ConfigureAwait(true);
            }

            if (result.Created <= 0)
            {
                if (result.Skipped > 0)
                {
                    AddValidationMessage = $"未添加新账号，{result.Skipped + skippedExistingAccounts:N0} 个同名账号已存在并跳过。";
                    return;
                }
                AddValidationMessage = FormatCreateFailure(result);
                return;
            }
            string successMessage = FormatCreateSuccess(result, skippedExistingAccounts);
            IsAdding = false;
            ResetAddSecrets();
            await ReloadAfterMutationAsync().ConfigureAwait(true);
            StatusMessage = successMessage;
        }
        catch (Sub2ApiSessionException exception)
        {
            ApplySessionFailure(exception.Failure);
            AddValidationMessage = DescribeSessionFailure(exception.Failure);
        }
        catch (AccountCenterClientException exception)
        {
            AddValidationMessage = DescribeCreateClientFailure(exception);
        }
        finally
        {
            IsAddBusy = false;
        }
    }

    [RelayCommand]
    private async Task PreviousPageAsync()
    {
        if (CanGoPrevious) await ShowPageAsync(CurrentPageNumber - 1).ConfigureAwait(true);
    }

    [RelayCommand]
    private async Task NextPageAsync()
    {
        if (CanGoNext) await ShowPageAsync(CurrentPageNumber + 1).ConfigureAwait(true);
    }
    private async Task LoadPageAsync(int page, bool forceUsage)
    {
        if (!await _loadGate.WaitAsync(0).ConfigureAwait(true)) return;
        IsBusy = true;
        NotifyPagingState();
        StatusMessage = "正在读取自己的账号…";
        try
        {
            Sub2ApiSessionAccess access = await RequireAccessAsync().ConfigureAwait(true);
            AccountCenterPage result = await LoadAllAccountsAsync(access).ConfigureAwait(true);
            _activeAccess = access;
            _allAccounts.Clear();
            foreach (AccountCenterAccount account in result.Items.OrderBy(account => account.Priority))
            {
                _allAccounts.Add(new AccountCenterAccountViewModel(account, UpdateSelectionState));
            }
            for (int index = 0; index < _allAccounts.Count; index++)
            {
                _allAccounts[index].ApplySchedulingPosition(_allAccounts[index].Priority, index + 1);
            }

            TotalAccounts = result.Total;
            ActiveAccounts = _allAccounts.Count(account => account.IsActive);
            AttentionAccounts = _allAccounts.Count(account => account.NeedsAttention);
            RebuildFilterOptions();
            ApplyFilteredPage(page);

            await LoadPageUsageAsync(access, Accounts.ToArray(), forceUsage).ConfigureAwait(true);
            _lastLoadedAt = DateTimeOffset.UtcNow;
            _hasLoaded = true;
            UpdatedLabel = $"本机同步 · {_lastLoadedAt.ToLocalTime():HH:mm}";
            StatusMessage = TotalAccounts == 0
                ? "当前登录用户还没有账号。"
                : $"已读取 {TotalAccounts:N0} 个账号；当前页用量窗口已同步。";
        }
        catch (Sub2ApiSessionException exception)
        {
            ApplySessionFailure(exception.Failure);
        }
        catch (AccountCenterClientException exception)
        {
            StatusMessage = DescribeClientFailure(exception);
        }
        catch (Exception)
        {
            StatusMessage = "账号读取失败，请稍后重试。";
        }
        finally
        {
            IsBusy = false;
            NotifyPagingState();
            _loadGate.Release();
        }
    }

    private async Task<AccountCenterPage> LoadAllAccountsAsync(Sub2ApiSessionAccess access)
    {
        AccountCenterPage first = await _client
            .ListAsync(access, 1, BackendPageSize, CancellationToken.None)
            .ConfigureAwait(true);
        var items = new List<AccountCenterAccount>(first.Items);
        int pageCount = Math.Max(1, (int)Math.Ceiling(first.Total / (double)BackendPageSize));
        for (int start = 2; start <= pageCount; start += BatchConcurrency)
        {
            int count = Math.Min(BatchConcurrency, pageCount - start + 1);
            Task<AccountCenterPage>[] tasks = Enumerable.Range(start, count)
                .Select(page => _client.ListAsync(access, page, BackendPageSize, CancellationToken.None))
                .ToArray();
            AccountCenterPage[] pages = await Task.WhenAll(tasks).ConfigureAwait(true);
            foreach (AccountCenterPage page in pages.OrderBy(result => result.Page))
            {
                items.AddRange(page.Items);
            }
        }

        return first with
        {
            Items = items.GroupBy(account => account.Id).Select(group => group.First()).ToArray(),
            Page = 1,
            Limit = BackendPageSize,
        };
    }

    private async Task ShowPageAsync(int page)
    {
        ApplyFilteredPage(page);
        if (_activeAccess is not null)
        {
            await LoadPageUsageAsync(_activeAccess, Accounts.ToArray(), false).ConfigureAwait(true);
        }
    }

    internal async Task ReorderAccountAsync(
        AccountCenterAccountViewModel source,
        AccountCenterAccountViewModel target,
        bool insertAfter)
    {
        if (IsBusy || ReferenceEquals(source, target)) return;
        int sourceIndex = _allAccounts.IndexOf(source);
        int targetIndex = _allAccounts.IndexOf(target);
        if (sourceIndex < 0 || targetIndex < 0) return;

        var reordered = new List<AccountCenterAccountViewModel>(_allAccounts);
        reordered.RemoveAt(sourceIndex);
        targetIndex = reordered.IndexOf(target);
        int insertionIndex = targetIndex + (insertAfter ? 1 : 0);
        reordered.Insert(insertionIndex, source);
        if (reordered.SequenceEqual(_allAccounts)) return;

        IsBusy = true;
        NotifyPagingState();
        StatusMessage = $"正在保存“{source.Name}”的新调度顺序…";
        string? failureMessage = null;
        try
        {
            Sub2ApiSessionAccess access = _activeAccess ?? await RequireAccessAsync().ConfigureAwait(true);
            _activeAccess = access;
            (AccountCenterAccountViewModel Account, int Priority)[] changes = reordered
                .Select((account, priority) => (Account: account, Priority: priority))
                .Where(change => change.Account.Priority != change.Priority)
                .ToArray();
            await Parallel.ForEachAsync(
                changes,
                new ParallelOptions { MaxDegreeOfParallelism = BatchConcurrency },
                async (change, cancellationToken) =>
                {
                    await _client.UpdateAsync(
                        access,
                        change.Account.Id,
                        BuildExistingUpdate(change.Account, change.Account.Status, change.Priority),
                        cancellationToken).ConfigureAwait(false);
                }).ConfigureAwait(true);

            _allAccounts.Clear();
            _allAccounts.AddRange(reordered);
            for (int index = 0; index < _allAccounts.Count; index++)
            {
                _allAccounts[index].ApplySchedulingPosition(index, index + 1);
            }
            ApplyFilteredPage(CurrentPageNumber);
            _lastLoadedAt = DateTimeOffset.UtcNow;
            UpdatedLabel = $"顺序已保存 · {_lastLoadedAt.ToLocalTime():HH:mm}";
            StatusMessage = $"已调整“{source.Name}”的调度顺序；排在前面的同平台账号优先使用。";
        }
        catch (Sub2ApiSessionException exception)
        {
            failureMessage = DescribeSessionFailure(exception.Failure);
        }
        catch (AccountCenterClientException exception)
        {
            failureMessage = $"顺序保存失败：{DescribeClientFailure(exception)}";
        }
        catch (Exception exception) when (exception is IOException or InvalidOperationException)
        {
            failureMessage = $"顺序保存失败：{exception.Message}";
        }
        finally
        {
            IsBusy = false;
            NotifyPagingState();
        }

        if (failureMessage is not null)
        {
            _hasLoaded = false;
            await LoadPageAsync(CurrentPageNumber, false).ConfigureAwait(true);
            StatusMessage = failureMessage;
        }
    }

    private void ApplyFilteredPage(int requestedPage)
    {
        AccountCenterAccountViewModel[] filtered = _allAccounts.Where(MatchesFilters).ToArray();
        FilteredAccountCount = filtered.Length;
        TotalPages = Math.Max(1, (int)Math.Ceiling(filtered.Length / (double)PageSize));
        CurrentPageNumber = Math.Clamp(requestedPage, 1, TotalPages);
        Accounts.Clear();
        foreach (AccountCenterAccountViewModel account in filtered
                     .Skip((CurrentPageNumber - 1) * PageSize)
                     .Take(PageSize))
        {
            Accounts.Add(account);
        }
        ApplyCollectionState();
        UpdateSelectionState();
        NotifyPagingState();
        OnPropertyChanged(nameof(FilteredSummaryLabel));
    }

    private bool MatchesFilters(AccountCenterAccountViewModel account)
    {
        string query = AccountSearchText.Trim();
        if (query.Length > 0 &&
            !account.Name.Contains(query, StringComparison.OrdinalIgnoreCase) &&
            !account.IdentityLabel.Contains(query, StringComparison.OrdinalIgnoreCase) &&
            !account.ProxyLabel.Contains(query, StringComparison.OrdinalIgnoreCase))
        {
            return false;
        }

        string status = SelectedStatusFilter?.Id ?? "all";
        if (status == "available" && !string.Equals(account.StatusLabel, "启用", StringComparison.Ordinal)) return false;
        if (status == "disabled" && account.IsActive) return false;
        if (status == "attention" && !account.NeedsAttention) return false;

        string platform = SelectedPlatformFilter?.Id ?? "all";
        return platform == "all" || string.Equals(platform, account.Platform, StringComparison.OrdinalIgnoreCase);
    }

    private void RebuildFilterOptions()
    {
        string platformId = SelectedPlatformFilter?.Id ?? "all";
        ReplaceFilterOptions(
            PlatformFilterOptions,
            "全部平台",
            _allAccounts.Select(account => account.Platform));
        SelectedPlatformFilter = PlatformFilterOptions.FirstOrDefault(option => option.Id == platformId) ?? PlatformFilterOptions[0];
    }

    private static void ReplaceFilterOptions(
        ObservableCollection<AccountCenterFilterOption> target,
        string allLabel,
        IEnumerable<string> values)
    {
        target.Clear();
        target.Add(new AccountCenterFilterOption("all", allLabel));
        foreach (string value in values
                     .Where(value => !string.IsNullOrWhiteSpace(value))
                     .Distinct(StringComparer.OrdinalIgnoreCase)
                     .OrderBy(value => value, StringComparer.CurrentCultureIgnoreCase))
        {
            target.Add(new AccountCenterFilterOption(value, value));
        }
    }

    private void ApplyFilters()
    {
        if (!_hasLoaded) return;
        ApplyFilteredPage(1);
        if (_activeAccess is not null)
        {
            _ = LoadVisibleUsageAfterFilterAsync(_activeAccess);
        }
    }

    private async Task LoadVisibleUsageAfterFilterAsync(Sub2ApiSessionAccess access)
    {
        try
        {
            await LoadPageUsageAsync(access, Accounts.ToArray(), false).ConfigureAwait(true);
        }
        catch
        {
            // Individual usage failures are rendered on the corresponding account.
        }
    }

    private void UpdateSelectionState()
    {
        SelectedAccountCount = _allAccounts.Count(account => account.IsSelected);
        _syncingSelection = true;
        IsAllVisibleSelected = Accounts.Count > 0 && Accounts.All(account => account.IsSelected);
        _syncingSelection = false;
        OnPropertyChanged(nameof(HasSelection));
        OnPropertyChanged(nameof(SelectionLabel));
        OnPropertyChanged(nameof(EditSubtitle));
    }

    partial void OnAccountSearchTextChanged(string value) => ApplyFilters();
    partial void OnSelectedStatusFilterChanged(AccountCenterFilterOption? value) => ApplyFilters();
    partial void OnSelectedPlatformFilterChanged(AccountCenterFilterOption? value) => ApplyFilters();
    partial void OnIsAllVisibleSelectedChanged(bool value)
    {
        if (_syncingSelection) return;
        foreach (AccountCenterAccountViewModel account in Accounts) account.IsSelected = value;
        UpdateSelectionState();
    }
    partial void OnIsBatchEditingChanged(bool value)
    {
        OnPropertyChanged(nameof(EditTitle));
        OnPropertyChanged(nameof(EditSubtitle));
        OnPropertyChanged(nameof(ShowSingleAccountName));
    }
    partial void OnEditingAccountChanged(AccountCenterAccountViewModel? value) => OnPropertyChanged(nameof(EditSubtitle));

    private async Task LoadPageUsageAsync(
        Sub2ApiSessionAccess access,
        IReadOnlyList<AccountCenterAccountViewModel> accounts,
        bool force)
    {
        if (accounts.Count == 0) return;
        foreach (AccountCenterAccountViewModel account in accounts) account.IsUsageLoading = true;
        var results = new ConcurrentDictionary<long, UsageLoadResult>();
        using var concurrency = new SemaphoreSlim(UsageConcurrency, UsageConcurrency);
        Task[] tasks = accounts.Select(async account =>
        {
            await concurrency.WaitAsync().ConfigureAwait(false);
            try
            {
                string key = BuildUsageCacheKey(access, account.Id);
                if (!force && _usageCache.TryGetValue(key, out CachedUsage? cached) &&
                    cached is not null &&
                    DateTimeOffset.UtcNow - cached.LoadedAt < CacheLifetime)
                {
                    results[account.Id] = new UsageLoadResult(cached.Summary, null);
                    return;
                }
                try
                {
                    AccountCenterUsageSummary? summary = await _client
                        .GetUsageAsync(access, account.Id, force, CancellationToken.None)
                        .ConfigureAwait(false);
                    if (summary is not null) _usageCache[key] = new CachedUsage(summary, DateTimeOffset.UtcNow);
                    results[account.Id] = new UsageLoadResult(summary, null);
                }
                catch (Exception exception)
                {
                    results[account.Id] = new UsageLoadResult(null, exception);
                }
            }
            finally
            {
                concurrency.Release();
            }
        }).ToArray();
        await Task.WhenAll(tasks).ConfigureAwait(true);
        foreach (AccountCenterAccountViewModel account in accounts)
        {
            account.IsUsageLoading = false;
            if (!results.TryGetValue(account.Id, out UsageLoadResult? result) || result is null) continue;
            if (result.Summary is not null) account.ApplyUsage(result.Summary);
            else account.UsageError = "用量暂不可用";
        }
    }

    [RelayCommand]
    private async Task RefreshAccountUsageAsync(AccountCenterAccountViewModel? account)
    {
        if (account is null || account.IsBusy || _activeAccess is null) return;
        account.IsUsageLoading = true;
        account.UsageError = string.Empty;
        try
        {
            AccountCenterUsageSummary? summary = await _client
                .GetUsageAsync(_activeAccess, account.Id, true, CancellationToken.None)
                .ConfigureAwait(true);
            if (summary is not null)
            {
                _usageCache[BuildUsageCacheKey(_activeAccess, account.Id)] = new CachedUsage(summary, DateTimeOffset.UtcNow);
                account.ApplyUsage(summary);
            }
        }
        catch (Exception)
        {
            account.UsageError = "用量查询失败";
        }
        finally
        {
            account.IsUsageLoading = false;
        }
    }

    private async Task<Sub2ApiSessionAccess> RequireAccessAsync()
    {
        if (_activeTarget is null)
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.GatewayUnavailable);

        string? localControlToken = _localControlTokenProvider();
        if (!string.IsNullOrWhiteSpace(localControlToken))
        {
            return await _sessionManager.LoginLocalControlAsync(
                _activeTarget.ApiBaseUri,
                localControlToken,
                CancellationToken.None).ConfigureAwait(true);
        }

        if (_sessionManager.Current is not { IsAuthenticated: true, ApiBaseUri: not null } current ||
            !SameEndpoint(current.ApiBaseUri, _activeTarget.ApiBaseUri))
        {
            await _sessionManager.RestoreAsync(_activeTarget.ApiBaseUri, CancellationToken.None).ConfigureAwait(true);
        }
        if (_sessionManager.Current is not { IsAuthenticated: true, ApiBaseUri: not null } restored ||
            !SameEndpoint(restored.ApiBaseUri, _activeTarget.ApiBaseUri))
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.AuthorizationUnavailable);
        }
        return await _sessionManager.GetAccessAsync(_activeTarget.ApiBaseUri, CancellationToken.None).ConfigureAwait(true);
    }

    private static bool SameTarget(Sub2ApiEndpointTarget? left, Sub2ApiEndpointTarget? right)
        => left is null && right is null ||
           left is not null && right is not null && SameEndpoint(left.ApiBaseUri, right.ApiBaseUri);

    private static bool SameEndpoint(Uri left, Uri right)
        => Uri.Compare(
            left,
            right,
            UriComponents.SchemeAndServer | UriComponents.Path,
            UriFormat.SafeUnescaped,
            StringComparison.OrdinalIgnoreCase) == 0;

    private static string BuildUsageCacheKey(Sub2ApiSessionAccess access, long accountId)
        => $"{access.ApiBaseUri.AbsoluteUri}|{access.UserId}|{accountId}";
    [RelayCommand]
    private async Task TestAccountAsync(AccountCenterAccountViewModel? account)
    {
        if (account is null || account.IsBusy || _activeAccess is null) return;
        account.IsBusy = true;
        TestingAccount = account;
        IsTestDialogOpen = true;
        IsTestLoadingModels = true;
        TestSucceeded = false;
        TestFailed = false;
        TestPrompt = string.Empty;
        TestStatusMessage = "正在读取该账号可用的测试模型…";
        TestModels.Clear();
        TestLogLines.Clear();
        TestImages.Clear();
        OnPropertyChanged(nameof(IsTestingOpenAi));
        OnPropertyChanged(nameof(HasTestImages));
        try
        {
            IReadOnlyList<AccountCenterTestModel> models = await _client
                .GetAvailableModelsAsync(_activeAccess, account.Id, CancellationToken.None)
                .ConfigureAwait(true);
            foreach (AccountCenterTestModel model in SortTestModels(account.Platform, models))
            {
                TestModels.Add(model);
            }
            SelectedTestModel = TestModels.FirstOrDefault();
            TestStatusMessage = SelectedTestModel is null
                ? "后台没有返回可用于该账号的模型，请先在“更多”中同步上游模型。"
                : $"已读取 {TestModels.Count:N0} 个模型，可以开始测试。";
            TestFailed = SelectedTestModel is null;
        }
        catch (AccountCenterClientException exception)
        {
            TestFailed = true;
            TestStatusMessage = DescribeClientFailure(exception);
            TestLogLines.Add(new AccountCenterTestLogLine(TestStatusMessage, "error"));
        }
        finally
        {
            IsTestLoadingModels = false;
            account.IsBusy = false;
        }
    }

    [RelayCommand]
    private async Task StartDetailedAccountTestAsync()
    {
        if (TestingAccount is null || SelectedTestModel is null || _activeAccess is null || !CanStartDetailedTest) return;

        _testCancellation?.Cancel();
        _testCancellation?.Dispose();
        var cancellation = new CancellationTokenSource();
        _testCancellation = cancellation;
        IsTestRunning = true;
        TestSucceeded = false;
        TestFailed = false;
        TestStatusMessage = $"正在连接 {SelectedTestModel.DisplayName}…";
        TestLogLines.Clear();
        TestImages.Clear();
        OnPropertyChanged(nameof(HasTestImages));
        TestingAccount.IsBusy = true;
        TestLogLines.Add(new AccountCenterTestLogLine($"> 开始测试 {TestingAccount.Name}", "muted"));
        TestLogLines.Add(new AccountCenterTestLogLine($"> 模型：{SelectedTestModel.Id}", "model"));
        string routeLabel = string.IsNullOrWhiteSpace(TestingAccount.ProxyName)
            ? "直连（未配置代理）"
            : TestingAccount.ProxyName;
        TestLogLines.Add(new AccountCenterTestLogLine($"> 出口：{routeLabel}", "status"));
        try
        {
            var progress = new Progress<AccountCenterTestEvent>(AppendDetailedTestEvent);
            AccountCenterDetailedTestResult result = await _client.RunDetailedTestAsync(
                _activeAccess,
                TestingAccount.Id,
                new AccountCenterDetailedTestRequest(
                    SelectedTestModel.Id,
                    TestPrompt.Trim(),
                    IsTestingOpenAi ? SelectedTestMode?.Id ?? "default" : string.Empty),
                progress,
                cancellation.Token).ConfigureAwait(true);
            TestSucceeded = result.Success;
            TestFailed = !result.Success;
            TestStatusMessage = result.Success
                ? "账号测试通过，后台已恢复该账号的可调度状态。"
                : $"账号测试失败：{NormalizeActionMessage(result.ErrorMessage)}";
            if (!result.Success)
            {
                TestLogLines.Add(new AccountCenterTestLogLine(TestStatusMessage, "error"));
            }
            StatusMessage = result.Success
                ? $"“{TestingAccount.Name}”测试通过。"
                : TestStatusMessage;
            await ReloadAfterMutationAsync().ConfigureAwait(true);
        }
        catch (OperationCanceledException) when (cancellation.IsCancellationRequested)
        {
            TestStatusMessage = "测试已取消。";
            TestLogLines.Add(new AccountCenterTestLogLine(TestStatusMessage, "muted"));
        }
        catch (AccountCenterClientException exception)
        {
            TestFailed = true;
            TestStatusMessage = DescribeClientFailure(exception);
            TestLogLines.Add(new AccountCenterTestLogLine(TestStatusMessage, "error"));
        }
        finally
        {
            TestingAccount.IsBusy = false;
            IsTestRunning = false;
            if (ReferenceEquals(_testCancellation, cancellation))
            {
                _testCancellation = null;
            }
            cancellation.Dispose();
        }
    }

    [RelayCommand]
    private void CloseAccountTest()
    {
        _testCancellation?.Cancel();
        IsTestDialogOpen = false;
    }

    [RelayCommand]
    private void CopyTestOutput()
    {
        string output = string.Join(Environment.NewLine, TestLogLines.Select(line => line.Text));
        if (string.IsNullOrWhiteSpace(output)) return;
        Clipboard.SetText(output);
        TestStatusMessage = "测试日志已复制。";
    }

    [RelayCommand]
    private async Task ManageAccountAsync(AccountCenterAccountActionRequest? request)
    {
        if (request?.Account is not { } account || account.IsBusy || _activeAccess is null) return;
        if (request.Action == AccountCenterAdminAction.ResetQuota &&
            MessageBox.Show(
                $"确定重置“{account.Name}”的本机额度计数吗？",
                "重置额度状态",
                MessageBoxButton.YesNo,
                MessageBoxImage.Question,
                MessageBoxResult.No) != MessageBoxResult.Yes)
        {
            return;
        }

        account.IsBusy = true;
        string actionLabel = DescribeAdminAction(request.Action);
        StatusMessage = $"正在为“{account.Name}”{actionLabel}…";
        try
        {
            await _client.RunAdminActionAsync(
                _activeAccess,
                account.Id,
                request.Action,
                CancellationToken.None).ConfigureAwait(true);
            StatusMessage = $"已为“{account.Name}”{actionLabel}。";
            RemoveUsageCache(_activeAccess, account.Id);
            await ReloadAfterMutationAsync().ConfigureAwait(true);
        }
        catch (AccountCenterClientException exception)
        {
            StatusMessage = $"{actionLabel}失败：{DescribeClientFailure(exception)}";
        }
        finally
        {
            account.IsBusy = false;
        }
    }

    [RelayCommand]
    private void ClearSelection()
    {
        foreach (AccountCenterAccountViewModel account in _allAccounts) account.IsSelected = false;
        UpdateSelectionState();
    }

    [RelayCommand]
    private void ResetFilters()
    {
        AccountSearchText = string.Empty;
        SelectedStatusFilter = StatusFilterOptions[0];
        SelectedPlatformFilter = PlatformFilterOptions[0];
        ApplyFilters();
    }

    [RelayCommand]
    private async Task TestSelectedAccountsAsync()
    {
        AccountCenterAccountViewModel[] targets = GetSelectedAccounts();
        if (targets.Length == 0 || IsBusy || _activeAccess is null) return;
        IsBusy = true;
        StatusMessage = $"正在检测 {targets.Length:N0} 个账号…";
        Sub2ApiSessionAccess access = _activeAccess;
        try
        {
            BatchOperationResult result = await RunBatchOperationAsync(
                targets,
                async account =>
                {
                    AccountCenterTestResult test = await _client
                        .TestAsync(access, account.Id, CancellationToken.None)
                        .ConfigureAwait(false);
                    if (!string.Equals(test.Status, "success", StringComparison.OrdinalIgnoreCase))
                    {
                        throw new InvalidOperationException(NormalizeActionMessage(test.ErrorMessage));
                    }
                }).ConfigureAwait(true);
            StatusMessage = FormatBatchResult("检测", result);
            await ReloadAfterMutationAsync().ConfigureAwait(true);
        }
        finally
        {
            IsBusy = false;
            foreach (AccountCenterAccountViewModel account in targets) account.IsBusy = false;
            NotifyPagingState();
        }
    }

    [RelayCommand]
    private async Task DeleteSelectedAccountsAsync()
    {
        AccountCenterAccountViewModel[] targets = GetSelectedAccounts();
        if (targets.Length == 0 || IsBusy || _activeAccess is null || !_confirmBatchDelete(targets)) return;
        IsBusy = true;
        StatusMessage = $"正在删除 {targets.Length:N0} 个账号…";
        Sub2ApiSessionAccess access = _activeAccess;
        try
        {
            BatchOperationResult result = await RunBatchOperationAsync(
                targets,
                account => _client.DeleteAsync(access, account.Id, CancellationToken.None)).ConfigureAwait(true);
            foreach (AccountCenterAccountViewModel account in targets) RemoveUsageCache(access, account.Id);
            StatusMessage = FormatBatchResult("删除", result);
            await ReloadAfterMutationAsync().ConfigureAwait(true);
        }
        finally
        {
            IsBusy = false;
            foreach (AccountCenterAccountViewModel account in targets) account.IsBusy = false;
            NotifyPagingState();
        }
    }

    [RelayCommand]
    private async Task ToggleAccountAsync(AccountCenterAccountViewModel? account)
    {
        if (account is null || account.IsBusy || _activeAccess is null) return;
        account.IsBusy = true;
        try
        {
            AccountCenterUpdateRequest update = BuildExistingUpdate(
                account,
                status: account.IsActive ? "disabled" : "active");
            await _client.UpdateAsync(_activeAccess, account.Id, update, CancellationToken.None).ConfigureAwait(true);
            StatusMessage = account.IsActive ? $"已停用“{account.Name}”。" : $"已启用“{account.Name}”。";
            await ReloadAfterMutationAsync().ConfigureAwait(true);
        }
        catch (AccountCenterClientException exception)
        {
            StatusMessage = DescribeClientFailure(exception);
        }
        finally
        {
            account.IsBusy = false;
        }
    }

    [RelayCommand]
    private async Task BeginEditAsync(AccountCenterAccountViewModel? account)
    {
        if (account is null || IsBusy || _activeAccess is null) return;
        IsBusy = true;
        EditValidationMessage = string.Empty;
        try
        {
            if (_editOptions is null || DateTimeOffset.UtcNow - _editOptionsLoadedAt >= CacheLifetime)
            {
                _editOptions = await _client.GetEditOptionsAsync(_activeAccess, CancellationToken.None).ConfigureAwait(true);
                _editOptionsLoadedAt = DateTimeOffset.UtcNow;
            }
            PopulateEditOptions(account, _editOptions);
            IsBatchEditing = false;
            EditingAccount = account;
            EditName = account.Name;
            EditConcurrency = Math.Max(account.Concurrency, 1);
            EditLoadFactor = Math.Max(account.LoadFactor, 0);
            EditPriority = Math.Max(account.Priority, 0);
            SelectedProxy = ProxyOptions.FirstOrDefault(proxy => proxy.Id == (account.ProxyId ?? 0)) ?? ProxyOptions.FirstOrDefault();
            IsEditing = true;
        }
        catch (AccountCenterClientException exception)
        {
            StatusMessage = DescribeClientFailure(exception);
        }
        finally
        {
            IsBusy = false;
            NotifyPagingState();
        }
    }

    [RelayCommand]
    private async Task BeginBatchEditAsync()
    {
        AccountCenterAccountViewModel[] targets = GetSelectedAccounts();
        if (targets.Length == 0 || IsBusy || _activeAccess is null) return;
        if (targets.Select(account => account.Platform).Distinct(StringComparer.OrdinalIgnoreCase).Count() > 1)
        {
            StatusMessage = "批量修改需要选择同一平台的账号；请先使用平台筛选。";
            return;
        }

        IsBusy = true;
        EditValidationMessage = string.Empty;
        try
        {
            if (_editOptions is null || DateTimeOffset.UtcNow - _editOptionsLoadedAt >= CacheLifetime)
            {
                _editOptions = await _client.GetEditOptionsAsync(_activeAccess, CancellationToken.None).ConfigureAwait(true);
                _editOptionsLoadedAt = DateTimeOffset.UtcNow;
            }

            AccountCenterAccountViewModel first = targets[0];
            PopulateEditOptions(first, _editOptions);
            EditingAccount = null;
            EditName = string.Empty;
            EditConcurrency = Math.Max(first.Concurrency, 1);
            EditLoadFactor = Math.Max(first.LoadFactor, 0);
            EditPriority = Math.Max(first.Priority, 0);
            SelectedProxy = ProxyOptions.FirstOrDefault(proxy => proxy.Id == (first.ProxyId ?? 0)) ?? ProxyOptions.FirstOrDefault();
            IsBatchEditing = true;
            IsEditing = true;
        }
        catch (AccountCenterClientException exception)
        {
            StatusMessage = DescribeClientFailure(exception);
        }
        finally
        {
            IsBusy = false;
            NotifyPagingState();
        }
    }

    [RelayCommand]
    private void CancelEdit()
    {
        IsEditing = false;
        IsBatchEditing = false;
        EditingAccount = null;
        EditValidationMessage = string.Empty;
    }

    [RelayCommand]
    private async Task SaveEditAsync()
    {
        AccountCenterAccountViewModel[] targets = IsBatchEditing
            ? GetSelectedAccounts()
            : EditingAccount is null ? [] : [EditingAccount];
        if (targets.Length == 0 || _activeAccess is null) return;
        if (IsBatchEditing) EditName = targets[0].Name;
        if (!ValidateEdit()) return;
        IsBusy = true;
        Sub2ApiSessionAccess access = _activeAccess;
        try
        {
            IReadOnlyList<long> groups = [];
            bool batch = IsBatchEditing;
            BatchOperationResult result = await RunBatchOperationAsync(
                targets,
                account => _client.UpdateAsync(
                    access,
                    account.Id,
                    new AccountCenterUpdateRequest(
                        batch ? account.Name : EditName.Trim(),
                        EditConcurrency,
                        EditLoadFactor,
                        groups,
                        SelectedProxy?.Id ?? 0,
                        null),
                    CancellationToken.None)).ConfigureAwait(true);
            string singleName = EditName.Trim();
            CancelEdit();
            StatusMessage = batch
                ? FormatBatchResult("修改", result)
                : result.Failed == 0
                    ? $"已保存“{singleName}”的调度设置。"
                    : FormatBatchResult("修改", result);
            _editOptions = null;
            await ReloadAfterMutationAsync().ConfigureAwait(true);
        }
        finally
        {
            IsBusy = false;
            foreach (AccountCenterAccountViewModel account in targets) account.IsBusy = false;
            NotifyPagingState();
        }
    }

    [RelayCommand]
    private async Task DeleteAccountAsync(AccountCenterAccountViewModel? account)
    {
        if (account is null || account.IsBusy || _activeAccess is null || !_confirmDelete(account)) return;
        account.IsBusy = true;
        try
        {
            await _client.DeleteAsync(_activeAccess, account.Id, CancellationToken.None).ConfigureAwait(true);
            RemoveUsageCache(_activeAccess, account.Id);
            if (Accounts.Count == 1 && CurrentPageNumber > 1) CurrentPageNumber--;
            StatusMessage = $"已删除“{account.Name}”。";
            await ReloadAfterMutationAsync().ConfigureAwait(true);
        }
        catch (AccountCenterClientException exception)
        {
            StatusMessage = DescribeClientFailure(exception);
        }
        finally
        {
            account.IsBusy = false;
        }
    }

    private AccountCenterAccountViewModel[] GetSelectedAccounts()
        => _allAccounts.Where(account => account.IsSelected).ToArray();

    private async Task<BatchOperationResult> RunBatchOperationAsync(
        IReadOnlyList<AccountCenterAccountViewModel> accounts,
        Func<AccountCenterAccountViewModel, Task> operation)
    {
        var errors = new ConcurrentBag<string>();
        int succeeded = 0;
        using var concurrency = new SemaphoreSlim(BatchConcurrency, BatchConcurrency);
        foreach (AccountCenterAccountViewModel account in accounts) account.IsBusy = true;
        Task[] tasks = accounts.Select(async account =>
        {
            await concurrency.WaitAsync().ConfigureAwait(false);
            try
            {
                await operation(account).ConfigureAwait(false);
                Interlocked.Increment(ref succeeded);
            }
            catch (Exception exception)
            {
                string message = exception is AccountCenterClientException clientException
                    ? DescribeClientFailure(clientException)
                    : NormalizeActionMessage(exception.Message);
                errors.Add($"{account.Name}：{message}");
            }
            finally
            {
                concurrency.Release();
            }
        }).ToArray();
        await Task.WhenAll(tasks).ConfigureAwait(true);
        return new BatchOperationResult(succeeded, errors.OrderBy(error => error, StringComparer.CurrentCulture).ToArray());
    }

    private static string FormatBatchResult(string action, BatchOperationResult result)
    {
        if (result.Failed == 0)
        {
            return $"批量{action}完成：{result.Succeeded:N0} 个账号成功。";
        }

        string firstError = result.Errors.Count == 0 ? string.Empty : $" 首个失败：{result.Errors[0]}";
        return $"批量{action}完成：成功 {result.Succeeded:N0} 个，失败 {result.Failed:N0} 个。{firstError}";
    }

    private void PopulateEditOptions(AccountCenterAccountViewModel account, AccountCenterEditOptions options)
    {
        ProxyOptions.Clear();
        ProxyOptions.Add(new AccountCenterProxyOptionViewModel(0, "直连", "不使用代理"));
        foreach (AccountCenterProxy proxy in options.Proxies.Where(proxy =>
                     string.Equals(proxy.Status, "active", StringComparison.OrdinalIgnoreCase) || proxy.Id == account.ProxyId))
        {
            ProxyOptions.Add(new AccountCenterProxyOptionViewModel(
                proxy.Id,
                proxy.Name,
                $"{proxy.Protocol}://{proxy.Host}:{proxy.Port}"));
        }
    }

    private bool ValidateEdit()
    {
        if (!IsBatchEditing && string.IsNullOrWhiteSpace(EditName)) return SetEditError("请输入账号名称。");
        if (EditConcurrency is < 1 or > 1000) return SetEditError("并发数应在 1 到 1000 之间。");
        if (EditLoadFactor is < 0 or > 10000) return SetEditError("调度负载应在 0 到 10000 之间。");
        EditValidationMessage = string.Empty;
        return true;
    }

    private bool SetEditError(string message)
    {
        EditValidationMessage = message;
        return false;
    }

    private AccountCenterUpdateRequest BuildExistingUpdate(
        AccountCenterAccountViewModel account,
        string status,
        int? priority = null)
        => new(
            account.Name,
            Math.Max(account.Concurrency, 1),
            Math.Max(account.LoadFactor, 0),
            [],
            account.ProxyId ?? 0,
            Math.Max(priority ?? account.Priority, 0),
            status);

    private async Task ReloadAfterMutationAsync()
    {
        _hasLoaded = false;
        await LoadPageAsync(CurrentPageNumber, false).ConfigureAwait(true);
    }

    private async Task<AccountCenterCreateResult> CreateOpenAiCredentialAsync(AccountCenterOpenAiCreateRequest request)
    {
        string method = SelectedOpenAiMethod?.Id ?? string.Empty;
        if (string.Equals(method, "manual", StringComparison.Ordinal))
        {
            (string code, string state) = ParseOpenAiCallback(AddOAuthText);
            return await _client.CreateOpenAiFromCodeAsync(
                _activeAccess!, request, AddOpenAiAuthSessionId, code, state, CancellationToken.None).ConfigureAwait(true);
        }
        if (string.Equals(method, "refresh_token", StringComparison.Ordinal) ||
            string.Equals(method, "mobile_refresh_token", StringComparison.Ordinal))
        {
            return await _client.CreateOpenAiFromRefreshTokenAsync(
                _activeAccess!, request, AddApiKey,
                string.Equals(method, "mobile_refresh_token", StringComparison.Ordinal),
                CancellationToken.None).ConfigureAwait(true);
        }
        return await _client.CreateOpenAiFromCodexPatAsync(
            _activeAccess!, request, AddApiKey, CancellationToken.None).ConfigureAwait(true);
    }

    private bool ValidateAddForm()
    {
        if (SelectedAddPlatform is null) return SetAddError("请选择账号平台。");
        if (AddConcurrency is < 1 or > 1000) return SetAddError("并发数应在 1 到 1000 之间。");
        if (AddLoadFactor is < 0 or > 10000) return SetAddError("调度负载应在 0 到 10000 之间。");
        if (IsAddApiKeyMode && string.IsNullOrWhiteSpace(AddApiKey)) return SetAddError("请输入 API Key。");
        if (UsesAddOAuthDocuments && _addOAuthDocuments.Count == 0 && string.IsNullOrWhiteSpace(AddOAuthText))
            return SetAddError("请选择 auth.json、Codex 会话文件，或粘贴凭据 JSON。");
        if (UsesAddManualCallback)
        {
            if (string.IsNullOrWhiteSpace(AddOpenAiAuthSessionId)) return SetAddError("请先生成并打开授权页面。");
            (string code, string state) = ParseOpenAiCallback(AddOAuthText);
            if (string.IsNullOrWhiteSpace(code) || string.IsNullOrWhiteSpace(state))
                return SetAddError("请粘贴包含 code 和 state 的完整回调地址。");
        }
        if (UsesAddSecret && string.IsNullOrWhiteSpace(AddApiKey)) return SetAddError($"请输入 {AddSecretLabel}。");
        AddValidationMessage = string.Empty;
        return true;
    }

    private bool SetAddError(string message)
    {
        AddValidationMessage = message;
        return false;
    }

    private void PopulateAddOptions(long? preferredProxyId = null)
    {
        long selectionId = preferredProxyId ?? SelectedAddProxy?.Id ?? -1;
        AddProxyOptions.Clear();
        AddProxyOptions.Add(new AccountCenterProxyOptionViewModel(0, "直连", "不使用代理"));
        foreach (AccountCenterProxy proxy in (_editOptions?.Proxies ?? []).Where(proxy =>
                     string.Equals(proxy.Status, "active", StringComparison.OrdinalIgnoreCase)))
        {
            AddProxyOptions.Add(new AccountCenterProxyOptionViewModel(
                proxy.Id, proxy.Name, $"{proxy.Protocol}://{proxy.Host}:{proxy.Port}"));
        }
        // Prefer the first active outbound proxy for newly imported accounts.
        // Direct connection remains available as an explicit fallback.
        SelectedAddProxy = selectionId >= 0
            ? AddProxyOptions.FirstOrDefault(proxy => proxy.Id == selectionId)
            : null;
        SelectedAddProxy ??= AddProxyOptions.FirstOrDefault(proxy => proxy.Id > 0)
            ?? AddProxyOptions.FirstOrDefault();
    }

    private void ResetAddForm()
    {
        SelectedAddPlatform = AddPlatformOptions[0];
        SelectedAddMode = AddModeOptions[0];
        SelectedOpenAiMethod = OpenAiMethodOptions[0];
        AddName = string.Empty;
        AddBaseUrl = string.Empty;
        AddConcurrency = 30;
        AddLoadFactor = 0;
        AddPriority = 0;
        AddTestModelId = string.Empty;
        AddValidationMessage = string.Empty;
        ResetAddProxyForm();
        ResetAddSecrets();
    }

    private bool ValidateAddProxyForm()
    {
        if (string.IsNullOrWhiteSpace(AddProxyName)) return SetAddProxyError("请输入代理名称。");
        if (SelectedAddProxyProtocol is null) return SetAddProxyError("请选择代理协议。");
        if (string.IsNullOrWhiteSpace(AddProxyHost)) return SetAddProxyError("请输入代理主机或 IP 地址。");
        if (AddProxyHost.Contains("://", StringComparison.Ordinal) || AddProxyHost.IndexOfAny(['/', '?', '#', '@']) >= 0)
            return SetAddProxyError("代理主机只填写域名或 IP，不要包含协议和路径。");
        if (AddProxyPort is < 1 or > 65535) return SetAddProxyError("代理端口应在 1 到 65535 之间。");
        if (AddProxyName.Trim().Length > 100 || AddProxyUsername.Trim().Length > 100 || AddProxyPassword.Trim().Length > 100)
            return SetAddProxyError("代理名称、用户名和密码不能超过 100 个字符。");
        return true;
    }

    private bool SetAddProxyError(string message)
    {
        AddProxyValidationMessage = message;
        return false;
    }

    private void ResetAddProxyForm()
    {
        IsAddProxyEditorOpen = false;
        AddProxyName = string.Empty;
        SelectedAddProxyProtocol = AddProxyProtocolOptions[0];
        AddProxyHost = string.Empty;
        AddProxyPort = 10808;
        AddProxyUsername = string.Empty;
        AddProxyPassword = string.Empty;
        AddProxyValidationMessage = string.Empty;
    }

    private void ResetAddSecrets()
    {
        AddApiKey = string.Empty;
        AddOAuthText = string.Empty;
        _addOAuthDocuments = [];
        AddOAuthFilesLabel = "尚未选择文件";
        AddOpenAiAuthUrl = string.Empty;
        AddOpenAiAuthSessionId = string.Empty;
    }

    private static string FormatCreateFailure(AccountCenterCreateResult result)
    {
        string[] messages = result.Items
            .Select(item => item.Message)
            .Where(message => !string.IsNullOrWhiteSpace(message))
            .Distinct(StringComparer.CurrentCulture)
            .Take(3)
            .ToArray();
        return messages.Length == 0 ? "账号添加失败，后台未返回具体原因。" : string.Join(" ", messages);
    }

    private static string FormatCreateSuccess(AccountCenterCreateResult result, int skippedExistingAccounts)
    {
        string message = result.Failed == 0
            ? $"已添加 {result.Created:N0} 个个人账号。"
            : $"已添加 {result.Created:N0} 个账号，另有 {result.Failed:N0} 个失败。";
        int totalSkipped = result.Skipped + skippedExistingAccounts;
        return totalSkipped > 0
            ? $"{message} 已存在的 {totalSkipped:N0} 个同名账号未重复导入。"
            : message;
    }

    private static (string Code, string State) ParseOpenAiCallback(string value)
    {
        string raw = value.Trim();
        if (string.IsNullOrWhiteSpace(raw)) return (string.Empty, string.Empty);
        string candidate = raw.Contains("?", StringComparison.Ordinal)
            ? raw
            : $"http://localhost/callback?{raw.TrimStart('?')}";
        if (!Uri.TryCreate(candidate, UriKind.Absolute, out Uri? uri)) return (string.Empty, string.Empty);
        Dictionary<string, string> query = uri.Query.TrimStart('?')
            .Split('&', StringSplitOptions.RemoveEmptyEntries)
            .Select(part => part.Split('=', 2))
            .Where(parts => parts.Length == 2)
            .ToDictionary(
                parts => Uri.UnescapeDataString(parts[0]),
                parts => Uri.UnescapeDataString(parts[1]),
                StringComparer.OrdinalIgnoreCase);
        return (query.GetValueOrDefault("code", string.Empty), query.GetValueOrDefault("state", string.Empty));
    }

    private static AccountCenterOAuthDocuments? SelectOAuthDocuments()
    {
        var dialog = new OpenFileDialog
        {
            Title = "选择 Codex / OpenAI 凭据文件",
            Filter = "凭据文件 (*.json;*.txt)|*.json;*.txt|所有文件 (*.*)|*.*",
            Multiselect = true,
            CheckFileExists = true,
        };
        if (dialog.ShowDialog() != true) return null;
        string[] contents = dialog.FileNames.Select(File.ReadAllText).ToArray();
        string label = dialog.FileNames.Length == 1
            ? Path.GetFileName(dialog.FileNames[0])
            : $"已选择 {dialog.FileNames.Length:N0} 个文件";
        return new AccountCenterOAuthDocuments(contents, label);
    }

    private static int CountOAuthDocumentCandidates(IReadOnlyList<string> contents)
    {
        int count = 0;
        foreach (string content in contents)
        {
            string trimmed = content.Trim();
            if (string.IsNullOrWhiteSpace(trimmed)) continue;
            try
            {
                using JsonDocument document = JsonDocument.Parse(trimmed);
                JsonElement root = document.RootElement;
                if (root.ValueKind == JsonValueKind.Array)
                {
                    count += root.GetArrayLength();
                }
                else if (root.ValueKind == JsonValueKind.Object &&
                         root.TryGetProperty("accounts", out JsonElement accounts) &&
                         accounts.ValueKind == JsonValueKind.Array)
                {
                    count += accounts.GetArrayLength();
                }
                else
                {
                    count++;
                }
            }
            catch (JsonException)
            {
                count += trimmed.Split('\n', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries).Length;
            }
        }
        return count;
    }

    private async Task<AccountCenterCreateResult> CreateOAuthDocumentsAsync(
        Sub2ApiSessionAccess access,
        IReadOnlyList<string> contents,
        IReadOnlyList<long> groupIds,
        long proxyId)
    {
        IReadOnlyList<IReadOnlyList<string>> batches = CreateOAuthDocumentBatches(contents);
        var results = new List<AccountCenterCreateResult>(batches.Count);
        for (int index = 0; index < batches.Count; index++)
        {
            if (batches.Count > 1)
            {
                AddValidationMessage = $"正在导入第 {index + 1:N0} / {batches.Count:N0} 批，每批最多 {MaxOAuthAccountsPerRequest:N0} 个账号…";
            }

            results.Add(await _client.CreateAsync(
                access,
                new AccountCenterCreateRequest(
                    "oauth", AddName, "openai", string.Empty, string.Empty, batches[index],
                    AddConcurrency, AddLoadFactor, 0, groupIds, proxyId, AddTestModelId),
                CancellationToken.None).ConfigureAwait(true));
        }

        return CombineCreateResults(results);
    }

    private static IReadOnlyList<IReadOnlyList<string>> CreateOAuthDocumentBatches(
        IReadOnlyList<string> contents)
    {
        var segments = new List<(string Content, int CandidateCount)>();
        foreach (string content in contents)
        {
            ExpandOAuthDocumentSegments(content, segments);
        }

        var batches = new List<IReadOnlyList<string>>();
        var current = new List<string>();
        int currentCount = 0;
        foreach ((string content, int candidateCount) in segments)
        {
            if (currentCount > 0 && currentCount + candidateCount > MaxOAuthAccountsPerRequest)
            {
                batches.Add(current.ToArray());
                current.Clear();
                currentCount = 0;
            }

            current.Add(content);
            currentCount += candidateCount;
        }
        if (current.Count > 0)
        {
            batches.Add(current.ToArray());
        }

        return batches;
    }

    private static void ExpandOAuthDocumentSegments(
        string content,
        ICollection<(string Content, int CandidateCount)> segments)
    {
        string trimmed = content.Trim();
        if (string.IsNullOrWhiteSpace(trimmed)) return;

        try
        {
            JsonNode? root = JsonNode.Parse(trimmed);
            if (root is JsonObject wrapper && wrapper["accounts"] is JsonArray wrappedAccounts)
            {
                foreach (JsonNode?[] accountChunk in wrappedAccounts.Chunk(MaxOAuthAccountsPerRequest))
                {
                    var chunkAccounts = new JsonArray();
                    foreach (JsonNode? account in accountChunk)
                    {
                        chunkAccounts.Add(account?.DeepClone());
                    }

                    var chunkWrapper = (JsonObject)wrapper.DeepClone();
                    chunkWrapper["accounts"] = chunkAccounts;
                    segments.Add((chunkWrapper.ToJsonString(), accountChunk.Length));
                }
                return;
            }

            if (root is JsonArray accounts)
            {
                foreach (JsonNode?[] accountChunk in accounts.Chunk(MaxOAuthAccountsPerRequest))
                {
                    var chunkAccounts = new JsonArray();
                    foreach (JsonNode? account in accountChunk)
                    {
                        chunkAccounts.Add(account?.DeepClone());
                    }
                    segments.Add((chunkAccounts.ToJsonString(), accountChunk.Length));
                }
                return;
            }

            segments.Add((trimmed, 1));
        }
        catch (JsonException)
        {
            foreach (string line in trimmed.Split(
                         '\n',
                         StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
            {
                segments.Add((line, 1));
            }
        }
    }

    private static AccountCenterCreateResult CombineCreateResults(
        IReadOnlyList<AccountCenterCreateResult> results)
    {
        int itemOffset = 0;
        var items = new List<AccountCenterCreateResultItem>();
        foreach (AccountCenterCreateResult result in results)
        {
            items.AddRange(result.Items.Select(item => item with { Index = item.Index + itemOffset }));
            itemOffset += result.Total;
        }

        return new AccountCenterCreateResult(
            results.Sum(result => result.Total),
            results.Sum(result => result.Created),
            results.Sum(result => result.Failed),
            items,
            results.Sum(result => result.Skipped));
    }

    private static PreparedOAuthDocuments PrepareOAuthDocuments(
        IReadOnlyList<string> contents,
        IEnumerable<string> existingAccountNames)
    {
        var existingNames = new HashSet<string>(
            existingAccountNames.Where(name => !string.IsNullOrWhiteSpace(name)),
            StringComparer.OrdinalIgnoreCase);
        var prepared = new List<string>(contents.Count);
        int skipped = 0;
        foreach (string content in contents)
        {
            string trimmed = content.Trim();
            if (string.IsNullOrWhiteSpace(trimmed)) continue;
            try
            {
                JsonNode? root = JsonNode.Parse(trimmed);
                if (root is JsonObject wrapper && wrapper["accounts"] is JsonArray wrappedAccounts)
                {
                    JsonArray filtered = FilterExistingAccounts(wrappedAccounts, existingNames, ref skipped);
                    if (filtered.Count == 0) continue;
                    wrapper["accounts"] = filtered;
                    prepared.Add(wrapper.ToJsonString());
                }
                else if (root is JsonArray accounts)
                {
                    JsonArray filtered = FilterExistingAccounts(accounts, existingNames, ref skipped);
                    if (filtered.Count > 0) prepared.Add(filtered.ToJsonString());
                }
                else
                {
                    prepared.Add(trimmed);
                }
            }
            catch (JsonException)
            {
                prepared.Add(trimmed);
            }
        }
        return new PreparedOAuthDocuments(prepared, skipped);
    }

    private static JsonArray FilterExistingAccounts(
        JsonArray accounts,
        IReadOnlySet<string> existingNames,
        ref int skipped)
    {
        var filtered = new JsonArray();
        foreach (JsonNode? account in accounts)
        {
            string? name = account is JsonObject accountObject &&
                           accountObject["name"] is JsonValue nameValue &&
                           nameValue.TryGetValue(out string? parsedName)
                ? parsedName
                : null;
            if (!string.IsNullOrWhiteSpace(name) && existingNames.Contains(name))
            {
                skipped++;
                continue;
            }
            filtered.Add(account?.DeepClone());
        }
        return filtered;
    }

    private static void OpenExternalUrl(string url)
        => Process.Start(new ProcessStartInfo(url) { UseShellExecute = true });

    partial void OnSelectedAddPlatformChanged(AccountCenterFilterOption? value)
    {
        SelectedAddMode = string.Equals(value?.Id, "openai", StringComparison.Ordinal)
            ? AddModeOptions[0]
            : AddModeOptions[1];
        PopulateAddOptions();
        NotifyAddModeState();
    }

    partial void OnIsAddProxyEditorOpenChanged(bool value)
        => OnPropertyChanged(nameof(AddProxyEditorToggleLabel));

    partial void OnSelectedAddModeChanged(AccountCenterFilterOption? value) => NotifyAddModeState();
    partial void OnSelectedOpenAiMethodChanged(AccountCenterFilterOption? value) => NotifyAddModeState();
    partial void OnTestingAccountChanged(AccountCenterAccountViewModel? value) => OnPropertyChanged(nameof(IsTestingOpenAi));
    partial void OnSelectedTestModelChanged(AccountCenterTestModel? value) => OnPropertyChanged(nameof(CanStartDetailedTest));
    partial void OnIsTestLoadingModelsChanged(bool value) => OnPropertyChanged(nameof(CanStartDetailedTest));
    partial void OnIsTestRunningChanged(bool value)
    {
        OnPropertyChanged(nameof(CanStartDetailedTest));
        OnPropertyChanged(nameof(DetailedTestButtonLabel));
    }
    partial void OnTestSucceededChanged(bool value) => OnPropertyChanged(nameof(DetailedTestButtonLabel));
    partial void OnTestFailedChanged(bool value) => OnPropertyChanged(nameof(DetailedTestButtonLabel));

    private void NotifyAddModeState()
    {
        OnPropertyChanged(nameof(IsAddOpenAi));
        OnPropertyChanged(nameof(IsAddApiKeyMode));
        OnPropertyChanged(nameof(IsAddOpenAiOAuth));
        OnPropertyChanged(nameof(UsesAddOAuthDocuments));
        OnPropertyChanged(nameof(UsesAddManualCallback));
        OnPropertyChanged(nameof(UsesAddSecret));
        OnPropertyChanged(nameof(AddSecretLabel));
        AddValidationMessage = string.Empty;
        ResetAddSecrets();
    }

    private void ApplyCollectionState()
    {
        HasAccounts = Accounts.Count > 0;
        ShowEmptyState = !HasAccounts;
    }

    private void NotifyPagingState()
    {
        OnPropertyChanged(nameof(PageLabel));
        OnPropertyChanged(nameof(CanGoPrevious));
        OnPropertyChanged(nameof(CanGoNext));
    }

    partial void OnCurrentPageNumberChanged(int value) => NotifyPagingState();
    partial void OnTotalPagesChanged(int value) => NotifyPagingState();
    partial void OnIsBusyChanged(bool value) => NotifyPagingState();

    private void ApplySessionFailure(Sub2ApiSessionFailure failure)
    {
        StatusMessage = failure switch
        {
            Sub2ApiSessionFailure.AuthorizationUnavailable => "本机管理权限尚未就绪，请重启软件后重试。",
            Sub2ApiSessionFailure.InvalidCredentials => "本机管理会话已经失效，请重启软件后重试。",
            Sub2ApiSessionFailure.Forbidden => "当前用户没有读取自己账号的权限。",
            Sub2ApiSessionFailure.GatewayUnavailable => "无法连接本机后台，请检查本机目录和服务。",
            _ => "当前登录无法用于账号中心，请重新登录。",
        };
        UpdatedLabel = "未同步";
    }

    private static string DescribeSessionFailure(Sub2ApiSessionFailure failure)
        => failure switch
        {
            Sub2ApiSessionFailure.AuthorizationUnavailable => "本机管理会话尚未就绪。请确认本机中转已经启动，然后重试。",
            Sub2ApiSessionFailure.InvalidCredentials => "本机管理会话已经失效，软件未能自动恢复，请重启本机中转后重试。",
            Sub2ApiSessionFailure.Forbidden => "当前本机会话没有添加个人账号的权限。",
            Sub2ApiSessionFailure.GatewayUnavailable => "无法连接本机后台，请确认本机中转服务正在运行。",
            _ => "本机管理会话恢复失败，请重启本机中转后重试。",
        };

    private static string DescribeClientFailure(AccountCenterClientException exception)
    {
        string fallback = exception.Failure switch
        {
            AccountCenterClientFailure.Unauthorized => "登录已过期，请重新登录。",
            AccountCenterClientFailure.Forbidden => "当前用户没有执行此操作的权限。",
            AccountCenterClientFailure.NotFound => "账号已经不存在，请刷新列表。",
            AccountCenterClientFailure.InvalidRequest => "提交的账号设置不符合后台规则。",
            AccountCenterClientFailure.RequestTimedOut => "本机后台处理超时，请稍后重试。",
            AccountCenterClientFailure.GatewayUnavailable => "无法连接本机中转后台。",
            _ => "后台返回了无法识别的账号数据。",
        };
        return string.IsNullOrWhiteSpace(exception.ServerMessage)
            ? fallback
            : $"{fallback} {NormalizeActionMessage(exception.ServerMessage)}";
    }

    private static string DescribeCreateClientFailure(AccountCenterClientException exception)
    {
        if (exception.Failure != AccountCenterClientFailure.RequestTimedOut)
        {
            return DescribeClientFailure(exception);
        }

        const string guidance =
            "账号导入或导入后的联网验证超时。账号可能已经写入本机后台，请关闭窗口并刷新账号列表确认；确认未导入后再重试，同名账号会自动跳过。";
        return string.IsNullOrWhiteSpace(exception.ServerMessage)
            ? guidance
            : $"{guidance} 技术信息：{NormalizeActionMessage(exception.ServerMessage)}";
    }

    private static string NormalizeActionMessage(string? value)
    {
        if (string.IsNullOrWhiteSpace(value)) return "未返回具体原因。";
        string normalized = value.Replace('\r', ' ').Replace('\n', ' ').Trim();
        return normalized.Length <= 160 ? normalized : normalized[..160] + "…";
    }

    private static IReadOnlyList<AccountCenterTestModel> SortTestModels(
        string platform,
        IReadOnlyList<AccountCenterTestModel> models)
    {
        if (!string.Equals(platform, "gemini", StringComparison.OrdinalIgnoreCase)) return models;
        string[] preferred =
        [
            "gemini-3.1-flash-image",
            "gemini-2.5-flash-image",
            "gemini-3.5-flash",
            "gemini-2.5-flash",
            "gemini-2.5-pro",
        ];
        return models
            .Select((model, index) => new
            {
                Model = model,
                Index = index,
                Rank = Array.FindIndex(preferred, id => string.Equals(id, model.Id, StringComparison.OrdinalIgnoreCase)),
            })
            .OrderBy(item => item.Rank < 0 ? int.MaxValue : item.Rank)
            .ThenBy(item => item.Index)
            .Select(item => item.Model)
            .ToArray();
    }

    private void AppendDetailedTestEvent(AccountCenterTestEvent item)
    {
        string type = item.Type.Trim().ToLowerInvariant();
        if (type == "image" &&
            TryCreateTestImage(item.ImageUrl, item.MimeType, out AccountCenterTestImageViewModel? image) &&
            image is not null)
        {
            TestImages.Add(image);
            OnPropertyChanged(nameof(HasTestImages));
            TestLogLines.Add(new AccountCenterTestLogLine("> 已收到测试图片", "success"));
            return;
        }

        string message = type switch
        {
            "test_start" when !string.IsNullOrWhiteSpace(item.Model) => $"> 后台使用模型：{item.Model}",
            "test_complete" when item.Success => "> 测试完成",
            "error" => NormalizeActionMessage(item.Error),
            _ when !string.IsNullOrWhiteSpace(item.Text) => item.Text.Trim(),
            _ => string.Empty,
        };
        if (string.IsNullOrWhiteSpace(message)) return;
        string level = type switch
        {
            "error" => "error",
            "test_complete" => item.Success ? "success" : "error",
            "test_start" => "model",
            "status" => "status",
            "content" => "content",
            _ => "muted",
        };
        TestLogLines.Add(new AccountCenterTestLogLine(message, level));
    }

    private static bool TryCreateTestImage(
        string dataUrl,
        string mimeType,
        out AccountCenterTestImageViewModel? image)
    {
        image = null;
        int comma = dataUrl.IndexOf(',');
        if (comma <= 0 || !dataUrl.StartsWith("data:image/", StringComparison.OrdinalIgnoreCase)) return false;
        try
        {
            byte[] bytes = Convert.FromBase64String(dataUrl[(comma + 1)..]);
            using var stream = new MemoryStream(bytes, writable: false);
            var bitmap = new BitmapImage();
            bitmap.BeginInit();
            bitmap.CacheOption = BitmapCacheOption.OnLoad;
            bitmap.StreamSource = stream;
            bitmap.EndInit();
            bitmap.Freeze();
            image = new AccountCenterTestImageViewModel(bitmap, string.IsNullOrWhiteSpace(mimeType) ? "测试图片" : mimeType);
            CryptographicOperations.ZeroMemory(bytes);
            return true;
        }
        catch (Exception exception) when (exception is FormatException or NotSupportedException or IOException)
        {
            return false;
        }
    }

    private static string DescribeAdminAction(AccountCenterAdminAction action) => action switch
    {
        AccountCenterAdminAction.RefreshCredentials => "刷新凭据",
        AccountCenterAdminAction.RecoverState => "恢复运行状态",
        AccountCenterAdminAction.ClearError => "清除错误状态",
        AccountCenterAdminAction.SetPrivacy => "重新设置隐私保护",
        AccountCenterAdminAction.ResetQuota => "重置额度状态",
        AccountCenterAdminAction.SyncUpstreamModels => "同步上游模型",
        _ => "执行账号操作",
    };

    private static bool ShowDeleteConfirmation(AccountCenterAccountViewModel account)
        => MessageBox.Show(
               $"确定删除账号“{account.Name}”吗？\n\n删除会同步到当前本机后台，且无法撤销。",
               "删除账号",
               MessageBoxButton.YesNo,
               MessageBoxImage.Warning,
               MessageBoxResult.No) == MessageBoxResult.Yes;

    private static bool ShowBatchDeleteConfirmation(IReadOnlyList<AccountCenterAccountViewModel> accounts)
        => MessageBox.Show(
               $"确定删除已选择的 {accounts.Count:N0} 个账号吗？\n\n删除会同步到当前本机后台，且无法撤销。",
               "批量删除账号",
               MessageBoxButton.YesNo,
               MessageBoxImage.Warning,
               MessageBoxResult.No) == MessageBoxResult.Yes;

    private void RemoveUsageCache(Sub2ApiSessionAccess access, long accountId)
        => _usageCache.TryRemove(BuildUsageCacheKey(access, accountId), out _);

    private void OnSessionChanged(object? sender, EventArgs args)
    {
        if (_disposed || _activeTarget is null) return;
        void Apply()
        {
            Sub2ApiSessionState current = _sessionManager.Current;
            if (current.ApiBaseUri is not null && !SameEndpoint(current.ApiBaseUri, _activeTarget.ApiBaseUri)) return;
            _activeAccess = null;
            _hasLoaded = false;
            if (!current.IsAuthenticated)
            {
                Accounts.Clear();
                ApplyCollectionState();
                StatusMessage = "本机管理会话尚未就绪，请重启软件后重试。";
                UpdatedLabel = "未同步";
            }
        }

        if (Application.Current?.Dispatcher is { } dispatcher && !dispatcher.CheckAccess())
            _ = dispatcher.BeginInvoke(Apply);
        else
            Apply();
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _testCancellation?.Cancel();
        _testCancellation?.Dispose();
        _sessionManager.SessionChanged -= OnSessionChanged;
        _loadGate.Dispose();
        if (_ownsClient && _client is IDisposable disposable) disposable.Dispose();
    }

    private sealed record CachedUsage(AccountCenterUsageSummary Summary, DateTimeOffset LoadedAt);
    private sealed record UsageLoadResult(AccountCenterUsageSummary? Summary, Exception? Error);
    private sealed record BatchOperationResult(int Succeeded, IReadOnlyList<string> Errors) { public int Failed => Errors.Count; }
    private sealed record PreparedOAuthDocuments(IReadOnlyList<string> Contents, int SkippedExisting);
}

public partial class AccountCenterAccountViewModel : ObservableObject
{
    private readonly Action? _selectionChanged;

    internal AccountCenterAccountViewModel(AccountCenterAccount model, Action? selectionChanged = null)
    {
        _selectionChanged = selectionChanged;
        Id = model.Id;
        Name = model.Name;
        Platform = model.Platform;
        Type = model.Type;
        Concurrency = model.Concurrency;
        LoadFactor = model.LoadFactor;
        Priority = model.Priority;
        Status = model.Status;
        ErrorMessage = model.ErrorMessage;
        LastUsedAt = model.LastUsedAt;
        CreatedAt = model.CreatedAt;
        Schedulable = model.Schedulable;
        RateLimitResetAt = model.RateLimitResetAt;
        TempUnschedulableUntil = model.TempUnschedulableUntil;
        ProxyId = model.ProxyId;
        ProxyName = model.ProxyName;
        GroupIds = model.GroupIds;
        GroupNames = model.GroupNames;
        TempUnschedulableReason = model.TempUnschedulableReason;
        RateLimitedAt = model.RateLimitedAt;
        OverloadUntil = model.OverloadUntil;
    }

    public long Id { get; }
    public string Name { get; }
    public string Platform { get; }
    public string Type { get; }
    public int Concurrency { get; }
    public int LoadFactor { get; }
    [ObservableProperty] private int priority;
    [ObservableProperty] private int schedulingOrder;
    [ObservableProperty] private bool isDropTarget;
    [ObservableProperty] private bool dropInsertAfter;
    [ObservableProperty] private bool isDetailsExpanded;
    public string Status { get; }
    public string ErrorMessage { get; }
    public DateTimeOffset? LastUsedAt { get; }
    public DateTimeOffset CreatedAt { get; }
    public bool Schedulable { get; }
    public DateTimeOffset? RateLimitResetAt { get; }
    public DateTimeOffset? TempUnschedulableUntil { get; }
    public long? ProxyId { get; }
    public string? ProxyName { get; }
    public IReadOnlyList<long> GroupIds { get; }
    public IReadOnlyList<string> GroupNames { get; }
    public string TempUnschedulableReason { get; }
    public DateTimeOffset? RateLimitedAt { get; }
    public DateTimeOffset? OverloadUntil { get; }

    [ObservableProperty] private bool isSelected;
    [ObservableProperty] private bool isBusy;
    [ObservableProperty] private bool isUsageLoading;
    [ObservableProperty] private string usageError = string.Empty;
    [ObservableProperty] private bool hasRollingUsage;
    [ObservableProperty] private bool hasLocalUsage;
    [ObservableProperty] private double fiveHourPercent;
    [ObservableProperty] private double sevenDayPercent;
    [ObservableProperty] private string fiveHourLabel = "—";
    [ObservableProperty] private string sevenDayLabel = "—";
    [ObservableProperty] private string fiveHourDetail = string.Empty;
    [ObservableProperty] private string sevenDayDetail = string.Empty;
    [ObservableProperty] private string localUsageLabel = string.Empty;

    partial void OnIsSelectedChanged(bool value) => _selectionChanged?.Invoke();

    public bool IsActive => string.Equals(Status, "active", StringComparison.OrdinalIgnoreCase);
    public bool HasError => !string.IsNullOrWhiteSpace(ErrorMessage);
    public bool NeedsAttention =>
        HasError || !IsActive || !Schedulable ||
        RateLimitResetAt > DateTimeOffset.UtcNow || TempUnschedulableUntil > DateTimeOffset.UtcNow || OverloadUntil > DateTimeOffset.UtcNow;
    public string IdentityLabel => $"{Platform} / {Type} · ID {Id}";
    public string SchedulingLabel => $"并发 {Concurrency} · 调度负载 {LoadFactor}";
    public string SchedulingOrderLabel => $"优先第 {SchedulingOrder} 顺位";
    public string ProxyLabel => string.IsNullOrWhiteSpace(ProxyName) ? "代理：直连" : $"代理：{ProxyName}";
    public string LastUsedLabel => LastUsedAt is null ? "从未使用" : FormatDateTime(LastUsedAt.Value);
    public string SubmittedLabel => FormatDateTime(CreatedAt);
    public string ToggleLabel => IsActive ? "停用" : "启用";
    public string HealthStatusLabel => HasError ? "需关注" : "正常";
    public string HealthMessageLabel => HasError ? ErrorMessage : "暂无错误";
    public string SchedulableLabel => Schedulable ? "可调度" : "不参与调度";
    public bool SupportsCredentialRefresh => Type is "oauth" or "setup-token";
    public bool SupportsPrivacy =>
        Type == "oauth" &&
        (string.Equals(Platform, "openai", StringComparison.OrdinalIgnoreCase) ||
         string.Equals(Platform, "antigravity", StringComparison.OrdinalIgnoreCase));
    public bool SupportsQuotaReset => Type is "apikey" or "bedrock";
    public bool SupportsModelSync => Type is "apikey" or "upstream";

    internal void ApplySchedulingPosition(int nextPriority, int nextOrder)
    {
        Priority = nextPriority;
        SchedulingOrder = nextOrder;
        OnPropertyChanged(nameof(SchedulingOrderLabel));
    }
    public string HealthSummaryLabel
    {
        get
        {
            if (HasError) return ErrorMessage;
            if (!IsActive) return "账号当前已停用。";
            if (OverloadUntil > DateTimeOffset.UtcNow) return $"账号过载，预计 {FormatDateTime(OverloadUntil.Value)} 恢复。";
            if (RateLimitResetAt > DateTimeOffset.UtcNow) return $"账号正在限流，预计 {FormatDateTime(RateLimitResetAt.Value)} 恢复。";
            if (TempUnschedulableUntil > DateTimeOffset.UtcNow)
                return string.IsNullOrWhiteSpace(TempUnschedulableReason)
                    ? $"账号暂不可用，预计 {FormatDateTime(TempUnschedulableUntil.Value)} 恢复。"
                    : TempUnschedulableReason;
            return Schedulable ? "账号运行正常，可以参与当前调度。" : "账号当前不参与调度。";
        }
    }

    public string StatusLabel
    {
        get
        {
            DateTimeOffset now = DateTimeOffset.UtcNow;
            if (LastUsedAt > now.AddMinutes(-2)) return "正在使用";
            if (!IsActive) return "已停用";
            if (RateLimitResetAt > now) return "限流中";
            if (OverloadUntil > now) return "过载中";
            if (TempUnschedulableUntil > now) return "暂不可用";
            if (HasError) return "异常";
            return Schedulable ? "启用" : "待调度";
        }
    }

    public string StatusDetail
    {
        get
        {
            if (HasError) return ErrorMessage.Length <= 120 ? ErrorMessage : ErrorMessage[..120] + "…";
            if (RateLimitResetAt > DateTimeOffset.UtcNow) return $"恢复于 {FormatDateTime(RateLimitResetAt.Value)}";
            if (OverloadUntil > DateTimeOffset.UtcNow) return $"恢复于 {FormatDateTime(OverloadUntil.Value)}";
            if (TempUnschedulableUntil > DateTimeOffset.UtcNow && !string.IsNullOrWhiteSpace(TempUnschedulableReason))
                return TempUnschedulableReason;
            if (TempUnschedulableUntil > DateTimeOffset.UtcNow) return $"恢复于 {FormatDateTime(TempUnschedulableUntil.Value)}";
            return SchedulableLabel;
        }
    }

    internal void ApplyUsage(AccountCenterUsageSummary summary)
    {
        UsageError = string.Empty;
        HasLocalUsage = summary.IsLocalRollup;
        HasRollingUsage = !summary.IsLocalRollup && (summary.FiveHour is not null || summary.SevenDay is not null);
        if (summary.FiveHour is not null)
        {
            FiveHourPercent = Math.Clamp(summary.FiveHour.Utilization, 0d, 100d);
            FiveHourLabel = $"{summary.FiveHour.Utilization:0.#}%";
            FiveHourDetail = FormatWindow(summary.FiveHour);
        }
        if (summary.SevenDay is not null)
        {
            SevenDayPercent = Math.Clamp(summary.SevenDay.Utilization, 0d, 100d);
            SevenDayLabel = $"{summary.SevenDay.Utilization:0.#}%";
            SevenDayDetail = FormatWindow(summary.SevenDay);
        }
        if (summary.IsLocalRollup)
        {
            LocalUsageLabel = $"近30天 {FormatCount(summary.ThirtyDayTokens)} Token · {summary.ThirtyDayRequests:N0} 次 · ${summary.ThirtyDayCost:N2}";
        }
    }

    private static string FormatWindow(AccountCenterUsageWindow window)
    {
        string reset = window.ResetsAt is null ? string.Empty : $" · {FormatRemaining(window.ResetsAt.Value)}后重置";
        return $"{window.Requests:N0} 次 · {FormatCount(window.Tokens)} · ${window.Cost:N2}{reset}";
    }

    private static string FormatRemaining(DateTimeOffset resetAt)
    {
        TimeSpan remaining = resetAt - DateTimeOffset.UtcNow;
        if (remaining <= TimeSpan.Zero) return "即将";
        if (remaining.TotalDays >= 1) return $"{(int)remaining.TotalDays}天{remaining.Hours}小时";
        if (remaining.TotalHours >= 1) return $"{(int)remaining.TotalHours}小时{remaining.Minutes}分";
        return $"{Math.Max(1, remaining.Minutes)}分钟";
    }

    private static string FormatDateTime(DateTimeOffset value)
        => value.ToLocalTime().ToString("yyyy/M/d HH:mm", CultureInfo.CurrentCulture);

    private static string FormatCount(long value)
        => value switch
        {
            >= 1_000_000_000 => $"{value / 1_000_000_000d:0.#}B",
            >= 1_000_000 => $"{value / 1_000_000d:0.#}M",
            >= 1_000 => $"{value / 1_000d:0.#}K",
            _ => value.ToString("N0", CultureInfo.CurrentCulture),
        };
}

public sealed record AccountCenterFilterOption(string Id, string Label);
public sealed record AccountCenterProxyOptionViewModel(long Id, string Name, string Detail);
public sealed record AccountCenterAccountActionRequest(AccountCenterAccountViewModel Account, AccountCenterAdminAction Action);
public sealed record AccountCenterTestLogLine(string Text, string Level);
public sealed record AccountCenterTestImageViewModel(ImageSource Source, string Label);
internal sealed record AccountCenterOAuthDocuments(IReadOnlyList<string> Contents, string Label);
