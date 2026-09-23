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
        IsSupported = account.LocalProxyKind == LocalProxyKind.Codex;
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

    /// <summary>A ChatGPT subscription account: the only kind the local proxy serves.</summary>
    public bool IsSupported { get; }

    public string PlatformLabel { get; }

    public string TypeLabel { get; }

    public string PlanText { get; }

    public bool HasPlan => PlanText.Length > 0;

    public bool IsHealthy { get; }

    public string StatusText { get; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(ActionLabel))]
    private bool isActive;

    public string ActionLabel => IsActive ? "关闭" : "开启";
}

/// <summary>
/// The 本地代理 page: the user's own accounts on the relay, and which ChatGPT account, if any,
/// Codex goes straight to the official API with.
/// </summary>
/// <remarks>
/// <para>
/// Codex only. Its configuration keeps the local placeholder key; the account's real token
/// lives inside the relay and is fetched per request, so token expiry never touches a Codex
/// session. Claude Code is not part of this and always goes through the relay server.
/// </para>
/// <para>
/// <b>Never a silent way back.</b> A local proxy is the user's own choice. When it fails the
/// failure is shown — an error on the page, a badge, one notification per new problem — and
/// Codex stays on the local proxy until the user switches it off. Falling back to the relay
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
        Func<bool> codexOnClaudeGroup,
        Func<DateTimeOffset>? clock = null)
    {
        _client = client ?? throw new ArgumentNullException(nameof(client));
        _refresh = refresh ?? throw new ArgumentNullException(nameof(refresh));
        _codex = codex ?? throw new ArgumentNullException(nameof(codex));
        _credentials = credentials ?? throw new ArgumentNullException(nameof(credentials));
        _preferences = preferences ?? throw new ArgumentNullException(nameof(preferences));
        _usage = usage ?? throw new ArgumentNullException(nameof(usage));
        _codexOnClaudeGroup = codexOnClaudeGroup ?? throw new ArgumentNullException(nameof(codexOnClaudeGroup));
        _clock = clock ?? (() => DateTimeOffset.Now);
        RefreshUsage();
    }

    /// <summary>Raised with a message the first time a new problem appears; the host shows a notification.</summary>
    public event Action<string>? FailureRaised;

    /// <summary>ChatGPT subscription accounts: the ones Codex can use.</summary>
    public ObservableCollection<LocalProxyAccountItem> Accounts { get; } = [];

    /// <summary>Everything else the user owns, listed so nothing looks lost.</summary>
    public ObservableCollection<LocalProxyAccountItem> OtherAccounts { get; } = [];

    public bool HasAccounts => Accounts.Count > 0;

    public bool HasOtherAccounts => OtherAccounts.Count > 0;

    /// <summary>Why the list is empty or stale, in words; empty when there is nothing to say.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasAccountsMessage))]
    private string accountsMessage = "正在读取你的账号…";

    public bool HasAccountsMessage => AccountsMessage.Length > 0;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsActive))]
    [NotifyPropertyChangedFor(nameof(StatusText))]
    private LocalProxyTarget? target;

    public bool IsActive => Target is not null;

    public string StatusText => Target is { } t ? $"正在使用本地代理：{t.Name}" : "经中转站（未开启本地代理）";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasError))]
    private string error = string.Empty;

    /// <summary>Anything the user should look at — drives the page's badge.</summary>
    public bool HasError => Error.Length > 0;

    [ObservableProperty]
    private string usageText = string.Empty;

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
        }
    }

    /// <summary>Switches Codex to <paramref name="item"/>, or — when it is the active one — back to the relay server.</summary>
    public async Task ToggleAsync(LocalProxyAccountItem item)
    {
        ArgumentNullException.ThrowIfNull(item);
        if (item.IsActive)
        {
            Disable();
            return;
        }

        await EnableAsync(item).ConfigureAwait(true);
    }

    internal async Task EnableAsync(LocalProxyAccountItem item)
    {
        ActionMessage = string.Empty;
        if (!item.IsSupported)
        {
            ActionMessage = "本地代理只支持订阅登录的 ChatGPT 账号。";
            return;
        }

        if (_codexOnClaudeGroup())
        {
            ActionMessage = "Codex 现在用的是 Claude 分组，ChatGPT 账号跑不了 Claude 模型。请先在 Codex 页切到 GPT 分组，再开启本地代理。";
            return;
        }

        // Asked for once now, so a refusal is shown on this click rather than on Codex's next turn.
        try
        {
            await _credentials.GetAsync(item.Id, forceRefresh: true, CancellationToken.None).ConfigureAwait(true);
        }
        catch (RelayApiException ex)
        {
            ActionMessage = $"开启失败：{ex.UserMessage}";
            return;
        }

        Apply(new LocalProxyTarget(item.Id, item.Name));
        Save();
        ActionMessage = $"Codex 已改为本地代理「{item.Name}」，下一轮对话起生效，不再经过中转站、不扣余额。";
    }

    internal void Disable()
    {
        Apply(null);
        Save();
        ActionMessage = "Codex 已改回经中转站。";
    }

    /// <summary>Takes a report from the relay. Call on the UI thread.</summary>
    internal void ApplyOutcome(LocalProxyOutcome outcome)
    {
        LocalProxyTarget? current = Target;
        if (current is null || current.AccountId != outcome.AccountId)
        {
            return;
        }

        string message = outcome.Succeeded ? string.Empty : outcome.Message ?? "本地代理出错。";
        string previous = Error;
        Error = message;

        // Once per new problem, not once per failed request.
        if (message.Length > 0 && message != previous)
        {
            FailureRaised?.Invoke($"Codex 本地代理「{current.Name}」出错：{message}\n未切回中转站，可在「本地代理」页关闭。");
        }
    }

    /// <summary>Re-reads today's local-proxy usage from this machine's store.</summary>
    public void RefreshUsage() =>
        UsageText = LocalProxyUsageStore.Describe(
            LocalProxyUsageStore.Sum(_usage.Load(), LocalProxyUsageStore.DateKey(_clock())));

    /// <summary>Drops everything belonging to the account that just signed out, including the saved choice.</summary>
    internal void Reset()
    {
        Target = null;
        Error = string.Empty;
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
        if (saved.CodexAccountId is not long id)
        {
            return;
        }

        LocalProxyAccountItem? item = Accounts.FirstOrDefault(a => a.Id == id);
        Apply(new LocalProxyTarget(id, item?.Name ?? saved.CodexAccountName ?? $"账号 {id}"));
        if (item is null && AccountsMessage.Length == 0)
        {
            Error = "上次选择的账号已不在你的账号列表中，本地代理无法使用。";
        }
    }

    private void Apply(LocalProxyTarget? target)
    {
        Target = target;
        Error = string.Empty;
        _codex.SetLocalProxy(target);
        MarkActive();
    }

    private void Save() => _preferences.Save(new LocalProxyChoice
    {
        CodexAccountId = Target?.AccountId,
        CodexAccountName = Target?.Name,
    });

    private void Populate(IReadOnlyList<ContributionAccount> accounts)
    {
        Accounts.Clear();
        OtherAccounts.Clear();
        foreach (ContributionAccount account in accounts)
        {
            var item = new LocalProxyAccountItem(account);
            (item.IsSupported ? Accounts : OtherAccounts).Add(item);
        }

        OnPropertyChanged(nameof(HasAccounts));
        OnPropertyChanged(nameof(HasOtherAccounts));
        MarkActive();
    }

    private void MarkActive()
    {
        foreach (LocalProxyAccountItem item in Accounts)
        {
            item.IsActive = Target?.AccountId == item.Id;
        }
    }
}
