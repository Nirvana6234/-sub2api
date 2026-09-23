using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;

namespace LanAi.RelayClient.ViewModels;

/// <summary>One of the user's own accounts, as the local proxy page lists it.</summary>
public sealed partial class LocalProxyAccountItem : ObservableObject
{
    internal LocalProxyAccountItem(ContributionAccount account)
    {
        Id = account.Id;
        Name = account.Name;
        Kind = account.LocalProxyKind;
        PlatformLabel = account.Platform.ToLowerInvariant() switch
        {
            "openai" => "ChatGPT",
            "anthropic" => "Claude",
            "" => "未知",
            string other => other,
        };
        TypeLabel = account.Type.ToLowerInvariant() switch
        {
            "oauth" => "订阅登录",
            "setup-token" => "长期令牌",
            "apikey" => "API Key",
            string other => other,
        };
        PlanText = account.Credentials?.PlanType is { Length: > 0 } plan ? plan : string.Empty;
        IsHealthy = account.IsActive;
        StatusText = account.IsActive
            ? "正常"
            : string.IsNullOrWhiteSpace(account.ErrorMessage) ? $"不可用（{account.Status}）" : $"不可用：{account.ErrorMessage}";
    }

    public long Id { get; }

    public string Name { get; }

    internal LocalProxyKind Kind { get; }

    public string PlatformLabel { get; }

    public string TypeLabel { get; }

    public string PlanText { get; }

    public bool HasPlan => PlanText.Length > 0;

    public bool IsHealthy { get; }

    public string StatusText { get; }

    public string UnsupportedReason => Kind == LocalProxyKind.Unsupported
        ? "暂不支持：只支持订阅登录的 ChatGPT / Claude 账号"
        : string.Empty;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(ActionLabel))]
    private bool isActive;

    public string ActionLabel => IsActive ? "关闭" : "开启";
}

/// <summary>
/// The 本地代理 page: the user's own accounts on the relay, and which of them, if any, each
/// tool goes straight to the official API with.
/// </summary>
/// <remarks>
/// <para>
/// Each tool is its own either/or with the relay server: Codex on a local proxy leaves
/// Claude Code where it was, and the reverse.
/// </para>
/// <para>
/// <b>Never a silent way back.</b> A local proxy is the user's own choice. When it fails the
/// failure is shown — an error on the page, a badge, one notification per new problem — and
/// the tool stays on the local proxy until the user switches it off. Falling back to the relay
/// server on its own would start spending the user's balance without their knowing.
/// </para>
/// </remarks>
public sealed partial class LocalProxyViewModel : ObservableObject
{
    /// <summary>The account list changes rarely; the poll re-reads it at most this often.</summary>
    private static readonly TimeSpan AccountsMaxAge = TimeSpan.FromMinutes(10);

    private readonly IRelayServerClient _client;
    private readonly RefreshState _refresh;
    private readonly ICodexStartup _codex;
    private readonly ILocalProxyCredentialSource _credentials;
    private readonly ILocalProxyPreferenceStore _preferences;
    private readonly ILocalProxyUsageStore _usage;
    private readonly ClaudeCodeViewModel _claudeCode;
    private readonly ClaudePreferenceViewModel _claudePreference;
    private readonly Func<bool> _codexOnClaudeGroup;
    private readonly Func<DateTimeOffset> _clock;

    private DateTimeOffset? _accountsLoadedAt;
    private bool _restored;

    internal LocalProxyViewModel(
        IRelayServerClient client,
        RefreshState refresh,
        ICodexStartup codex,
        ILocalProxyCredentialSource credentials,
        ILocalProxyPreferenceStore preferences,
        ILocalProxyUsageStore usage,
        ClaudeCodeViewModel claudeCode,
        ClaudePreferenceViewModel claudePreference,
        Func<bool> codexOnClaudeGroup,
        Func<DateTimeOffset>? clock = null,
        IOfficialReachability? reachability = null)
    {
        _reachability = reachability ?? new OfficialReachability();
        _client = client ?? throw new ArgumentNullException(nameof(client));
        _refresh = refresh ?? throw new ArgumentNullException(nameof(refresh));
        _codex = codex ?? throw new ArgumentNullException(nameof(codex));
        _credentials = credentials ?? throw new ArgumentNullException(nameof(credentials));
        _preferences = preferences ?? throw new ArgumentNullException(nameof(preferences));
        _usage = usage ?? throw new ArgumentNullException(nameof(usage));
        _claudeCode = claudeCode ?? throw new ArgumentNullException(nameof(claudeCode));
        _claudePreference = claudePreference ?? throw new ArgumentNullException(nameof(claudePreference));
        _codexOnClaudeGroup = codexOnClaudeGroup ?? throw new ArgumentNullException(nameof(codexOnClaudeGroup));
        _clock = clock ?? (() => DateTimeOffset.Now);
        RefreshUsage();
    }

    private readonly IOfficialReachability _reachability;

    /// <summary>Raised with a message the first time a new problem appears; the host shows a notification.</summary>
    public event Action<string>? FailureRaised;

    /// <summary>
    /// Asks the user to confirm switching on, with the network check's result: (message,
    /// confirm-button label) → whether to go ahead. Supplied by the host, which owns a window.
    /// Without one, a reachable host goes ahead and an unreachable one is refused.
    /// </summary>
    public Func<string, string, Task<bool>>? ConfirmEnable { get; set; }

    /// <summary>The official host a tool's local proxy connects to.</summary>
    internal static Uri OfficialEndpoint(LocalProxyKind kind) => new(kind == LocalProxyKind.ClaudeCode
        ? LocalProxyEndpoints.Official.ClaudeBaseUrl
        : LocalProxyEndpoints.Official.CodexResponsesUrl);

    private static string ToolName(LocalProxyKind kind) => kind == LocalProxyKind.ClaudeCode ? "Claude Code" : "Codex";

    /// <summary>
    /// What the user is told before switching on. The official hosts are often reachable
    /// only through a proxy/VPN, and a local proxy that cannot reach them simply stops the
    /// tool working — with no way back to the relay server unless the user switches it off.
    /// </summary>
    internal static string DescribeSwitchOn(LocalProxyKind kind, Reachability check)
    {
        string tool = ToolName(kind);
        string host = OfficialEndpoint(kind).Host;
        return check.Reachable
            ? $"开启后，{tool} 将不再经过中转站，而是由本机直接连接官方服务器（{host}）。\n\n" +
              $"当前网络：{check.ProxyDescription}，已能连上官方。\n\n" +
              "请注意：使用期间请保持代理/VPN 一直开启且节点可用。代理断开或节点失效时，" +
              $"{tool} 会无法使用（客户端会提醒），但不会自动切回中转站。\n\n确定开启吗？"
            : $"现在连不上官方服务器（{host}）：{check.Problem}\n\n" +
              $"当前网络：{check.ProxyDescription}。\n\n" +
              "本地代理需要本机能直接访问官方，国内通常要先打开代理/VPN（系统代理或 TUN 模式）。" +
              $"建议先开好代理再开启；现在开启的话，{tool} 在连上官方之前都无法使用。\n\n仍要开启吗？";
    }

    public ObservableCollection<LocalProxyAccountItem> CodexAccounts { get; } = [];

    public ObservableCollection<LocalProxyAccountItem> ClaudeAccounts { get; } = [];

    public ObservableCollection<LocalProxyAccountItem> OtherAccounts { get; } = [];

    public bool HasCodexAccounts => CodexAccounts.Count > 0;

    public bool HasClaudeAccounts => ClaudeAccounts.Count > 0;

    public bool HasOtherAccounts => OtherAccounts.Count > 0;

    /// <summary>Why the list is empty or stale, in words; empty when there is nothing to say.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasAccountsMessage))]
    private string accountsMessage = "正在读取你的账号…";

    public bool HasAccountsMessage => AccountsMessage.Length > 0;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsCodexActive))]
    [NotifyPropertyChangedFor(nameof(CodexStatusText))]
    private LocalProxyTarget? codexTarget;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsClaudeActive))]
    [NotifyPropertyChangedFor(nameof(ClaudeStatusText))]
    private LocalProxyTarget? claudeTarget;

    public bool IsCodexActive => CodexTarget is not null;

    public bool IsClaudeActive => ClaudeTarget is not null;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasCodexError))]
    [NotifyPropertyChangedFor(nameof(HasError))]
    private string codexError = string.Empty;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasClaudeError))]
    [NotifyPropertyChangedFor(nameof(HasError))]
    private string claudeError = string.Empty;

    public bool HasCodexError => CodexError.Length > 0;

    public bool HasClaudeError => ClaudeError.Length > 0;

    /// <summary>Anything the user should look at — drives the page's badge.</summary>
    public bool HasError => HasCodexError || HasClaudeError;

    public string CodexStatusText => CodexTarget is { } t ? $"正在使用本地代理：{t.Name}" : "经中转站（未开启本地代理）";

    public string ClaudeStatusText => ClaudeTarget is { } t ? $"正在使用本地代理：{t.Name}" : "经中转站（未开启本地代理）";

    [ObservableProperty]
    private string codexUsageText = string.Empty;

    [ObservableProperty]
    private string claudeUsageText = string.Empty;

    /// <summary>A one-line message about the last action, e.g. why a switch was refused.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasActionMessage))]
    private string actionMessage = string.Empty;

    public bool HasActionMessage => ActionMessage.Length > 0;

    /// <summary>A card loader for the refresh cycle: reads the user's own accounts, at most every ten minutes.</summary>
    internal async Task LoadAccountsAsync(string token, CancellationToken cancellationToken)
    {
        if (_accountsLoadedAt is { } loaded && _clock() - loaded < AccountsMaxAge)
        {
            return;
        }

        try
        {
            IReadOnlyList<ContributionAccount> accounts =
                await _client.ListContributionAccountsAsync(token, cancellationToken).ConfigureAwait(true);
            _accountsLoadedAt = _clock();
            Populate(accounts);
            AccountsMessage = accounts.Count == 0 ? "你在中转站还没有自己的账号。" : string.Empty;
        }
        catch (RelayApiException ex) when (ex.Failure == RelayFailure.Forbidden)
        {
            // An answer, not a failure of the cycle: this user simply does not have the feature.
            _accountsLoadedAt = _clock();
            Populate([]);
            AccountsMessage = "你的账号未开通「我的账号」功能，暂时无法使用本地代理。";
        }
        catch (Exception ex) when (_refresh.Observe(ex))
        {
            if (_accountsLoadedAt is null)
            {
                AccountsMessage = "暂时取不到你的账号，稍后会自动重试。";
            }
            ClientLog.Warning("读取本地代理账号失败", ex);
        }

        if (!_restored)
        {
            _restored = true;
            RestoreChoice();
            await WarnIfRestoredToolsCannotReachOfficialAsync().ConfigureAwait(true);
        }
    }

    /// <summary>
    /// After a restart the choice comes back without the user clicking anything, so nobody
    /// has just been reminded about the proxy. Checked once; an unreachable host is said on
    /// the page and in one notification.
    /// </summary>
    private async Task WarnIfRestoredToolsCannotReachOfficialAsync()
    {
        foreach ((LocalProxyKind kind, LocalProxyTarget? target) in
                 new[] { (LocalProxyKind.Codex, CodexTarget), (LocalProxyKind.ClaudeCode, ClaudeTarget) })
        {
            if (target is null)
            {
                continue;
            }

            Reachability check = await _reachability.CheckAsync(OfficialEndpoint(kind)).ConfigureAwait(true);
            if (!check.Reachable)
            {
                string message = $"连不上官方服务器（{check.Problem}；当前网络：{check.ProxyDescription}），请打开代理/VPN。开启后下一轮对话会自动使用。";
                SetError(kind, message);
                FailureRaised?.Invoke($"{ToolName(kind)} 正在使用本地代理「{target.Name}」，但{message}\n未切回中转站，可在「本地代理」页关闭。");
            }
        }
    }

    private void SetError(LocalProxyKind kind, string message)
    {
        if (kind == LocalProxyKind.Codex)
        {
            CodexError = message;
        }
        else
        {
            ClaudeError = message;
        }
    }

    /// <summary>Re-reads the account list now, whatever its age.</summary>
    internal void InvalidateAccounts() => _accountsLoadedAt = null;

    /// <summary>Switches <paramref name="item"/>'s tool to it, or — when it is the active one — back to the relay server.</summary>
    public async Task ToggleAsync(LocalProxyAccountItem item)
    {
        ArgumentNullException.ThrowIfNull(item);
        if (item.IsActive)
        {
            Disable(item.Kind);
            return;
        }

        await EnableAsync(item).ConfigureAwait(true);
    }

    internal async Task EnableAsync(LocalProxyAccountItem item)
    {
        ActionMessage = string.Empty;
        if (item.Kind == LocalProxyKind.Unsupported)
        {
            ActionMessage = item.UnsupportedReason;
            return;
        }

        if (item.Kind == LocalProxyKind.Codex && _codexOnClaudeGroup())
        {
            ActionMessage = "Codex 现在用的是 Claude 分组，ChatGPT 账号跑不了 Claude 模型。请先在 Codex 页切到 GPT 分组，再开启本地代理。";
            return;
        }

        ActionMessage = "正在检测能否连上官方服务器…";
        Reachability check = await _reachability.CheckAsync(OfficialEndpoint(item.Kind)).ConfigureAwait(true);
        bool confirmed = ConfirmEnable is { } confirm
            ? await confirm(DescribeSwitchOn(item.Kind, check), check.Reachable ? "开启" : "仍然开启").ConfigureAwait(true)
            : check.Reachable;
        if (!confirmed)
        {
            ActionMessage = check.Reachable
                ? "已取消，仍经中转站。"
                : $"连不上官方服务器（{check.Problem}），未开启。请先打开代理/VPN 再试。";
            return;
        }

        // Asked for once now, so a refusal is shown on this click rather than on the tool's next turn.
        try
        {
            await _credentials.GetAsync(item.Id, forceRefresh: true, CancellationToken.None).ConfigureAwait(true);
        }
        catch (RelayApiException ex)
        {
            ActionMessage = $"开启失败：{ex.UserMessage}";
            return;
        }

        Apply(item.Kind, new LocalProxyTarget(item.Id, item.Name));
        Save();
        ActionMessage = $"{ToolName(item.Kind)} 已改为本地代理「{item.Name}」，下一轮对话起生效，不再经过中转站、不扣余额。请保持代理/VPN 开启。";
        if (!check.Reachable)
        {
            SetError(item.Kind, $"连不上官方服务器（{check.Problem}），请打开代理/VPN。开启后下一轮对话会自动使用。");
        }
    }

    internal void Disable(LocalProxyKind kind)
    {
        Apply(kind, null);
        Save();
        ActionMessage = kind == LocalProxyKind.Codex
            ? "Codex 已改回经中转站。"
            : "Claude Code 已改回经中转站。";
    }

    /// <summary>Takes a report from the relay. Call on the UI thread.</summary>
    internal void ApplyOutcome(LocalProxyOutcome outcome)
    {
        LocalProxyTarget? current = outcome.Kind == LocalProxyKind.Codex ? CodexTarget : ClaudeTarget;
        if (current is null || current.AccountId != outcome.AccountId)
        {
            return;
        }

        string message = outcome.Succeeded ? string.Empty : outcome.Message ?? "本地代理出错。";
        string previous = outcome.Kind == LocalProxyKind.Codex ? CodexError : ClaudeError;
        if (outcome.Kind == LocalProxyKind.Codex)
        {
            CodexError = message;
        }
        else
        {
            ClaudeError = message;
        }

        // Once per new problem, not once per failed request.
        if (message.Length > 0 && message != previous)
        {
            string tool = outcome.Kind == LocalProxyKind.Codex ? "Codex" : "Claude Code";
            FailureRaised?.Invoke($"{tool} 本地代理「{current.Name}」出错：{message}\n未切回中转站，可在「本地代理」页关闭。");
        }
    }

    /// <summary>Re-reads today's local-proxy usage from this machine's store.</summary>
    public void RefreshUsage()
    {
        IReadOnlyList<LocalProxyUsageDay> days = _usage.Load();
        string today = LocalProxyUsageStore.DateKey(_clock());
        CodexUsageText = LocalProxyUsageStore.Describe(LocalProxyUsageStore.Sum(days, LocalProxyKind.Codex, today));
        ClaudeUsageText = LocalProxyUsageStore.Describe(LocalProxyUsageStore.Sum(days, LocalProxyKind.ClaudeCode, today));
    }

    /// <summary>Drops everything belonging to the account that just signed out, including the saved choice.</summary>
    internal void Reset()
    {
        CodexTarget = null;
        ClaudeTarget = null;
        _claudeCode.SetLocalProxy(null);
        CodexError = string.Empty;
        ClaudeError = string.Empty;
        ActionMessage = string.Empty;
        _accountsLoadedAt = null;
        _restored = false;
        Populate([]);
        AccountsMessage = "正在读取你的账号…";

        // Written only when there is a choice to forget: a sign-out with nothing chosen
        // leaves the disk alone.
        if (_preferences.Load() != LocalProxyChoice.None)
        {
            _preferences.Save(LocalProxyChoice.None);
        }
    }

    private void RestoreChoice()
    {
        LocalProxyChoice saved = _preferences.Load();
        if (saved.CodexAccountId is long codexId)
        {
            Apply(LocalProxyKind.Codex, new LocalProxyTarget(codexId, NameOf(CodexAccounts, codexId, saved.CodexAccountName)));
            if (CodexAccounts.All(a => a.Id != codexId) && AccountsMessage.Length == 0)
            {
                CodexError = "上次选择的账号已不在你的账号列表中，本地代理无法使用。";
            }
        }
        if (saved.ClaudeAccountId is long claudeId)
        {
            Apply(LocalProxyKind.ClaudeCode, new LocalProxyTarget(claudeId, NameOf(ClaudeAccounts, claudeId, saved.ClaudeAccountName)));
            if (ClaudeAccounts.All(a => a.Id != claudeId) && AccountsMessage.Length == 0)
            {
                ClaudeError = "上次选择的账号已不在你的账号列表中，本地代理无法使用。";
            }
        }
    }

    private static string NameOf(IEnumerable<LocalProxyAccountItem> items, long id, string? fallback) =>
        items.FirstOrDefault(a => a.Id == id)?.Name ?? fallback ?? $"账号 {id}";

    private void Apply(LocalProxyKind kind, LocalProxyTarget? target)
    {
        if (kind == LocalProxyKind.Codex)
        {
            CodexTarget = target;
            CodexError = string.Empty;
        }
        else
        {
            ClaudeTarget = target;
            ClaudeError = string.Empty;
        }

        _codex.SetLocalProxy(kind, target);
        MarkActive();

        if (kind == LocalProxyKind.ClaudeCode)
        {
            _claudeCode.SetLocalProxy(target);
            if (target is not null)
            {
                // Claude Code has to be pointed at this relay for the local proxy to reach it,
                // and its model comes from the account preference.
                if (!_claudeCode.PluginSupportEnabled)
                {
                    _claudeCode.PluginSupportEnabled = true;
                }
                if (!_claudePreference.IsLoaded)
                {
                    _ = _claudePreference.LoadAsync();
                }
            }
        }
    }

    private void Save() => _preferences.Save(new LocalProxyChoice
    {
        CodexAccountId = CodexTarget?.AccountId,
        CodexAccountName = CodexTarget?.Name,
        ClaudeAccountId = ClaudeTarget?.AccountId,
        ClaudeAccountName = ClaudeTarget?.Name,
    });

    private void Populate(IReadOnlyList<ContributionAccount> accounts)
    {
        CodexAccounts.Clear();
        ClaudeAccounts.Clear();
        OtherAccounts.Clear();
        foreach (ContributionAccount account in accounts)
        {
            var item = new LocalProxyAccountItem(account);
            (item.Kind switch
            {
                LocalProxyKind.Codex => CodexAccounts,
                LocalProxyKind.ClaudeCode => ClaudeAccounts,
                _ => OtherAccounts,
            }).Add(item);
        }

        OnPropertyChanged(nameof(HasCodexAccounts));
        OnPropertyChanged(nameof(HasClaudeAccounts));
        OnPropertyChanged(nameof(HasOtherAccounts));
        MarkActive();
    }

    private void MarkActive()
    {
        foreach (LocalProxyAccountItem item in CodexAccounts)
        {
            item.IsActive = CodexTarget?.AccountId == item.Id;
        }
        foreach (LocalProxyAccountItem item in ClaudeAccounts)
        {
            item.IsActive = ClaudeTarget?.AccountId == item.Id;
        }
    }
}
