using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;

namespace LanAi.RelayClient.ViewModels;

/// <summary>One account the local proxy can use, as the page lists it.</summary>
public sealed partial class LocalProxyAccountItem : ObservableObject
{
    /// <summary>This machine's own official sign-in (<see cref="LocalMachineAccounts"/>).</summary>
    internal LocalProxyAccountItem(LocalProxyKind kind, LocalMachineAccountStatus status)
    {
        Id = LocalMachineAccounts.IdFor(kind);
        Name = status.DisplayName;
        Kind = kind;
        IsLocal = true;
        IsUsable = status.IsUsable;
        PlatformLabel = kind == LocalProxyKind.ClaudeCode ? "Claude" : "ChatGPT";
        TypeLabel = "本机登录";
        StatusText = status.State switch
        {
            LocalMachineAccountState.SignedIn when status.Detail.Length > 0 => "已登录。" + status.Detail,
            LocalMachineAccountState.SignedIn => "已登录，登录信息只在这台电脑上使用，不会上传到共飞服务器",
            LocalMachineAccountState.Unsupported => "暂不支持：" + status.Detail,
            _ => "未登录：" + status.Detail,
        };
        Detail = status.Detail;
    }

    /// <summary>An official account signed in within the client (<see cref="OfficialAccountIds"/>).</summary>
    internal LocalProxyAccountItem(OfficialAccount account)
    {
        Id = account.Id;
        Name = account.DisplayName;
        Kind = account.Kind;
        IsOfficial = true;
        IsUsable = account.IsValid;
        PlatformLabel = account.Kind == LocalProxyKind.ClaudeCode ? "Claude" : "ChatGPT";
        TypeLabel = "已授权";
        string expiry = DateTimeOffset.TryParse(account.SubscriptionExpiresAt, out DateTimeOffset until)
            ? $"，订阅到 {until.ToLocalTime():yyyy-MM-dd}"
            : string.Empty;
        StatusText = account.IsValid
            ? $"可用{expiry}，授权信息只保存在这台电脑上，不会上传到共飞服务器"
            : $"{(account.InvalidReason.Length > 0 ? account.InvalidReason : "授权已失效")}，请点「重新授权」";
        Detail = account.IsValid ? string.Empty : "这个账号的授权已失效，请点「重新授权」。";
    }

    public long Id { get; }

    public string Name { get; }

    internal LocalProxyKind Kind { get; }

    /// <summary>This machine's own sign-in.</summary>
    public bool IsLocal { get; }

    /// <summary>Signed in within the client: can be signed in again and removed.</summary>
    public bool IsOfficial { get; }

    /// <summary>Can be switched on.</summary>
    public bool IsUsable { get; }

    /// <summary>What to say before it is switched on (a coming system prompt), or why it cannot be.</summary>
    internal string Detail { get; }

    public string PlatformLabel { get; }

    public string TypeLabel { get; }

    public string StatusText { get; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(ActionLabel))]
    [NotifyPropertyChangedFor(nameof(CanToggle))]
    private bool isActive;

    /// <summary>
    /// The one to use (D9): this machine's sign-in, usable, while the tool is still on the relay.
    /// A label only — nothing is ever switched on without a click.
    /// </summary>
    [ObservableProperty]
    private bool isRecommended;

    public string ActionLabel => IsActive ? "关闭" : "开启";

    /// <summary>An active one can always be switched off, even after its sign-in went away.</summary>
    public bool CanToggle => IsUsable || IsActive;

    internal bool SameAs(LocalProxyAccountItem other) =>
        Id == other.Id && Name == other.Name && IsUsable == other.IsUsable && StatusText == other.StatusText;
}

/// <summary>
/// The 本地代理 page: which account, if any, each tool goes straight to the official API with —
/// this machine's own official sign-in first (D9), or an official account signed in within the
/// client (任务计划：小白端客户端内登录官方账号).
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
/// <para>
/// The user's accounts on the relay server are no longer offered (D7): a choice saved for one
/// is dropped once, with a notice, and the tool goes back to the relay server.
/// </para>
/// </remarks>
public sealed partial class LocalProxyViewModel : ObservableObject
{
    private readonly ICodexStartup _codex;
    private readonly ILocalProxyCredentialSource _credentials;
    private readonly ILocalProxyPreferenceStore _preferences;
    private readonly ILocalProxyUsageStore _usage;
    private readonly ClaudeCodeViewModel _claudeCode;
    private readonly ClaudePreferenceViewModel _claudePreference;
    private readonly Func<bool> _codexOnClaudeGroup;
    private readonly Func<DateTimeOffset> _clock;
    private readonly ILocalMachineAccount? _localCodex;
    private readonly ILocalMachineAccount? _localClaude;
    private readonly OfficialAccountStore? _official;
    private readonly Func<string?> _officialScope;
    private readonly Func<LocalProxyKind, OfficialSignInSession>? _startSignIn;
    private readonly Func<Uri, bool> _openUrl;
    private readonly IOfficialReachability _reachability;

    private bool _restored;
    private OfficialSignInSession? _signIn;

    internal LocalProxyViewModel(
        ICodexStartup codex,
        ILocalProxyCredentialSource credentials,
        ILocalProxyPreferenceStore preferences,
        ILocalProxyUsageStore usage,
        ClaudeCodeViewModel claudeCode,
        ClaudePreferenceViewModel claudePreference,
        Func<bool> codexOnClaudeGroup,
        Func<DateTimeOffset>? clock = null,
        IOfficialReachability? reachability = null,
        ILocalMachineAccount? localCodex = null,
        ILocalMachineAccount? localClaude = null,
        OfficialAccountStore? officialAccounts = null,
        Func<string?>? officialScope = null,
        Func<LocalProxyKind, OfficialSignInSession>? startSignIn = null,
        Func<Uri, bool>? openUrl = null)
    {
        _reachability = reachability ?? new OfficialReachability();
        _codex = codex ?? throw new ArgumentNullException(nameof(codex));
        _credentials = credentials ?? throw new ArgumentNullException(nameof(credentials));
        _preferences = preferences ?? throw new ArgumentNullException(nameof(preferences));
        _usage = usage ?? throw new ArgumentNullException(nameof(usage));
        _claudeCode = claudeCode ?? throw new ArgumentNullException(nameof(claudeCode));
        _claudePreference = claudePreference ?? throw new ArgumentNullException(nameof(claudePreference));
        _codexOnClaudeGroup = codexOnClaudeGroup ?? throw new ArgumentNullException(nameof(codexOnClaudeGroup));
        _clock = clock ?? (() => DateTimeOffset.Now);
        _localCodex = localCodex;
        _localClaude = localClaude;
        _official = officialAccounts;
        _officialScope = officialScope ?? (() => null);
        _startSignIn = startSignIn ?? (officialAccounts is null ? null : kind => OfficialSignInSession.Start(kind, new OfficialTokenExchanger()));
        _openUrl = openUrl ?? BrowserLauncher.TryOpen;
        if (_official is not null)
        {
            // A refresh on the relay's thread can mark an account gone; the page hears of it here.
            _official.Changed += () => Post(ReloadOfficialAccounts);
        }

        RefreshUsage();
        ProbeLocalAccounts();
    }

    /// <summary>How work from other threads reaches the page's thread. The host sets the UI dispatcher.</summary>
    internal Action<Action> Post { get; set; } = action => action();

    /// <summary>Raised with a message the first time a new problem appears; the host shows a notification.</summary>
    public event Action<string>? FailureRaised;

    /// <summary>Something the user should know that is not a failure (a saved choice that no longer applies); the host shows a plain notification.</summary>
    public event Action<string>? NoticeRaised;

    /// <summary>
    /// Asks the user to confirm: (message, confirm-button label) → whether to go ahead. Supplied by
    /// the host, which owns a window. Without one, switching on goes ahead when the official host
    /// is reachable, and removing an account goes ahead.
    /// </summary>
    public Func<string, string, Task<bool>>? ConfirmEnable { get; set; }

    /// <summary>
    /// Whether ChatGPT is installed but not running, so switching Codex on should start it.
    /// Supplied by the dashboard; without one nothing is started.
    /// </summary>
    internal Func<bool>? CodexNeedsLaunch { get; set; }

    /// <summary>Starts ChatGPT; returns why it did not start, or null when it did.</summary>
    internal Func<Task<string?>>? LaunchCodex { get; set; }

    /// <summary>The official host a tool's local proxy connects to.</summary>
    internal static Uri OfficialEndpoint(LocalProxyKind kind) => new(kind == LocalProxyKind.ClaudeCode
        ? LocalProxyEndpoints.Official.ClaudeBaseUrl
        : LocalProxyEndpoints.Official.CodexResponsesUrl);

    private static string ToolName(LocalProxyKind kind) => kind == LocalProxyKind.ClaudeCode ? "Claude Code" : "Codex";

    private static string ProductName(LocalProxyKind kind) => kind == LocalProxyKind.ClaudeCode ? "Claude" : "ChatGPT";

    /// <summary>
    /// What the user is told before switching on. The official hosts are often reachable
    /// only through a proxy/VPN, and a local proxy that cannot reach them simply stops the
    /// tool working — with no way back to the relay server unless the user switches it off.
    /// </summary>
    internal static string DescribeSwitchOn(LocalProxyKind kind, Reachability check, bool launchesCodex = false)
    {
        string tool = ToolName(kind);
        string host = OfficialEndpoint(kind).Host;
        string launch = launchesCodex ? "ChatGPT 还没有启动，确定后会自动启动它。\n\n" : string.Empty;
        return check.Reachable
            ? $"开启后，{tool} 将不再经过中转站，而是由本机直接连接官方服务器（{host}）。\n\n" +
              $"当前网络：{check.ProxyDescription}，已能连上官方。\n\n" +
              "请注意：使用期间请保持代理/VPN 一直开启且节点可用。代理断开或节点失效时，" +
              $"{tool} 会无法使用（客户端会提醒），但不会自动切回中转站。\n\n{launch}确定开启吗？"
            : $"现在连不上官方服务器（{host}）：{check.Problem}\n\n" +
              $"当前网络：{check.ProxyDescription}。\n\n" +
              "本地代理需要本机能直接访问官方，国内通常要先打开代理/VPN（系统代理或 TUN 模式）。" +
              $"建议先开好代理再开启；现在开启的话，{tool} 在连上官方之前都无法使用。\n\n{launch}仍要开启吗？";
    }

    /// <summary>This machine's Codex ChatGPT sign-in; null when the host supplied none.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasLocalCodexAccount))]
    [NotifyPropertyChangedFor(nameof(CodexSignInIsPrimary))]
    private LocalProxyAccountItem? localCodexAccount;

    /// <summary>This machine's Claude Code sign-in; null when the host supplied none.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasLocalClaudeAccount))]
    [NotifyPropertyChangedFor(nameof(ClaudeSignInIsPrimary))]
    private LocalProxyAccountItem? localClaudeAccount;

    public bool HasLocalCodexAccount => LocalCodexAccount is not null;

    public bool HasLocalClaudeAccount => LocalClaudeAccount is not null;

    /// <summary>ChatGPT accounts signed in within the client.</summary>
    public ObservableCollection<LocalProxyAccountItem> CodexSignIns { get; } = [];

    /// <summary>Claude accounts signed in within the client.</summary>
    public ObservableCollection<LocalProxyAccountItem> ClaudeSignIns { get; } = [];

    public bool HasCodexSignIns => CodexSignIns.Count > 0;

    public bool HasClaudeSignIns => ClaudeSignIns.Count > 0;

    /// <summary>Accounts can be signed in within the client at all (the host supplied a store).</summary>
    public bool CanSignIn => _official is not null && _startSignIn is not null;

    /// <summary>
    /// 「在共飞里登录」 is the thing to do (D9): nothing on this machine or in the client can serve
    /// Codex yet. Otherwise it stays a secondary link.
    /// </summary>
    public bool CodexSignInIsPrimary => CanSignIn && LocalCodexAccount?.IsUsable != true && !CodexSignIns.Any(a => a.IsUsable);

    /// <inheritdoc cref="CodexSignInIsPrimary"/>
    public bool ClaudeSignInIsPrimary => CanSignIn && LocalClaudeAccount?.IsUsable != true && !ClaudeSignIns.Any(a => a.IsUsable);

    // What the page shows for signing in, per tool: the button, the secondary link, or neither while one is under way.
    public bool ShowCodexSignInButton => CodexSignInIsPrimary && !IsCodexSigningIn;

    public bool ShowCodexSignInLink => CanSignIn && !CodexSignInIsPrimary && !IsCodexSigningIn;

    public bool ShowClaudeSignInButton => ClaudeSignInIsPrimary && !IsClaudeSigningIn;

    public bool ShowClaudeSignInLink => CanSignIn && !ClaudeSignInIsPrimary && !IsClaudeSigningIn;

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

    // ---- Signing in within the client (A2 / A3). One sign-in at a time. ----

    /// <summary>The tool a sign-in is under way for; null when none is.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsCodexSigningIn))]
    [NotifyPropertyChangedFor(nameof(IsClaudeSigningIn))]
    [NotifyPropertyChangedFor(nameof(ShowCodexSignInButton))]
    [NotifyPropertyChangedFor(nameof(ShowCodexSignInLink))]
    [NotifyPropertyChangedFor(nameof(ShowClaudeSignInButton))]
    [NotifyPropertyChangedFor(nameof(ShowClaudeSignInLink))]
    private LocalProxyKind? signingInKind;

    public bool IsCodexSigningIn => SigningInKind == LocalProxyKind.Codex;

    public bool IsClaudeSigningIn => SigningInKind == LocalProxyKind.ClaudeCode;

    /// <summary>The authorization link, to copy when the browser did not open.</summary>
    [ObservableProperty]
    private string signInUrl = string.Empty;

    /// <summary>What to do now, in words.</summary>
    [ObservableProperty]
    private string signInPrompt = string.Empty;

    /// <summary>What the user pasted back: the browser's address (ChatGPT) or the code (Claude).</summary>
    [ObservableProperty]
    private string pastedSignIn = string.Empty;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasSignInError))]
    private string signInError = string.Empty;

    public bool HasSignInError => SignInError.Length > 0;

    /// <summary>The sign-in ended without an account; it can be started again or closed.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSubmitSignIn))]
    private bool signInFailed;

    /// <summary>What was pasted is being traded for the account: nothing more to paste meanwhile.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSubmitSignIn))]
    private bool signInSubmitted;

    public bool CanSubmitSignIn => !SignInFailed && !SignInSubmitted;

    /// <summary>A card loader for the refresh cycle: looks at this machine's sign-ins and the client's own, and restores the choice once.</summary>
    internal Task LoadAccountsAsync(string token, CancellationToken cancellationToken)
    {
        // Cheap and local: a sign-in made or lost since the last cycle shows up within one.
        ProbeLocalAccounts();
        if (_official is not null && _official.Scope != _officialScope())
        {
            _official.SetScope(_officialScope());
        }

        ReloadOfficialAccounts();
        if (_restored)
        {
            return Task.CompletedTask;
        }

        _restored = true;
        RestoreChoice();
        return WarnIfRestoredToolsCannotReachOfficialAsync();
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
        if (!item.IsUsable)
        {
            ActionMessage = item.Detail;
            return;
        }

        if (item.Kind == LocalProxyKind.Codex && _codexOnClaudeGroup())
        {
            ActionMessage = "Codex 现在用的是 Claude 分组，ChatGPT 账号跑不了 Claude 模型。请先在 Codex 页切到 GPT 分组，再开启本地代理。";
            return;
        }

        ActionMessage = "正在检测能否连上官方服务器…";
        Reachability check = await _reachability.CheckAsync(OfficialEndpoint(item.Kind)).ConfigureAwait(true);
        bool launchesCodex = item.Kind == LocalProxyKind.Codex && LaunchCodex is not null && CodexNeedsLaunch?.Invoke() == true;

        // A usable local sign-in with something to say first — the macOS keychain prompt that is coming.
        string notice = item.IsLocal && item.Detail.Length > 0 ? "\n\n" + item.Detail : string.Empty;
        bool confirmed = ConfirmEnable is { } confirm
            ? await confirm(DescribeSwitchOn(item.Kind, check, launchesCodex) + notice, check.Reachable ? "开启" : "仍然开启").ConfigureAwait(true)
            : check.Reachable;
        if (!confirmed)
        {
            ActionMessage = check.Reachable
                ? "已取消，仍经中转站。"
                : $"连不上官方服务器（{check.Problem}），未开启。请先打开代理/VPN 再试。";
            return;
        }

        // Asked for once now, so a refusal is shown on this click rather than on the tool's next
        // turn. Never a forced refresh: each refresh spends the refresh token for good, so it is
        // only done when the token is expiring.
        try
        {
            await _credentials.GetAsync(item.Id, forceRefresh: false, CancellationToken.None).ConfigureAwait(true);
        }
        catch (LocalProxyCredentialException ex)
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

        // Checked again: ChatGPT may have been started while the dialog was open.
        if (launchesCodex && CodexNeedsLaunch?.Invoke() == true && LaunchCodex is { } launch)
        {
            string switched = ActionMessage;
            ActionMessage = switched + " 正在启动 ChatGPT…";
            string? problem = await launch().ConfigureAwait(true);
            ActionMessage = problem is null
                ? switched + " ChatGPT 已启动。"
                : switched + $" ChatGPT 没有启动：{problem}";
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

    /// <summary>
    /// Starts signing in to an official account for <paramref name="kind"/>'s tool within the
    /// client: the browser opens the official sign-in page, and the account is kept when the
    /// browser comes back (ChatGPT) or when the user pastes what it showed (§3.3 / §3.4).
    /// </summary>
    /// <param name="reauthorize">The account being signed in again, if any.</param>
    public async Task StartSignInAsync(LocalProxyKind kind, LocalProxyAccountItem? reauthorize = null)
    {
        if (_official is null || _startSignIn is null)
        {
            return;
        }

        if (_official.Scope is null)
        {
            ActionMessage = "请先登录共飞AI助手，再授权本地代理。";
            return;
        }

        EndSignIn();
        OfficialSignInSession session;
        try
        {
            session = _startSignIn(kind);
        }
        catch (Exception ex) when (ex is InvalidOperationException or System.Net.HttpListenerException or IOException)
        {
            ClientLog.Warning("开始在共飞里登录官方账号失败", ex);
            ActionMessage = "没能开始登录，请稍后再试。";
            return;
        }

        _signIn = session;
        SigningInKind = kind;
        SignInUrl = session.AuthorizeUrl.ToString();
        SignInError = string.Empty;
        SignInFailed = false;
        PastedSignIn = string.Empty;
        SignInPrompt = kind == LocalProxyKind.ClaudeCode
            ? "已在浏览器打开 Claude 授权页。登录并点「授权」后，页面会显示一串授权码：复制下来，粘贴到下面，点「完成登录」。"
            : session.IsAutomatic
                ? "已在浏览器打开 ChatGPT 登录页。登录完成后会自动回到这里；如果浏览器没有跳回，把地址栏里的完整网址粘贴到下面，点「完成登录」。"
                : "已在浏览器打开 ChatGPT 登录页。登录完成后浏览器会显示「无法访问此网站」，这是正常的：把地址栏里的完整网址复制下来，粘贴到下面，点「完成登录」。";
        if (!_openUrl(session.AuthorizeUrl))
        {
            SignInPrompt = $"没能自动打开浏览器：请点「复制链接」，粘到浏览器里打开 {ProductName(kind)} 登录页。" +
                           SignInPrompt[(SignInPrompt.IndexOf('。') + 1)..];
        }

        OfficialAccount account;
        try
        {
            account = await session.Completion.ConfigureAwait(true);
        }
        catch (OfficialSignInException ex)
        {
            if (ReferenceEquals(_signIn, session))
            {
                SignInError = ex.UserMessage;
                SignInFailed = true;
                SignInSubmitted = false;
                _signIn = null;
                session.Dispose();
            }

            return;
        }

        if (!ReferenceEquals(_signIn, session))
        {
            return;
        }

        EndSignIn();
        OfficialAccount stored;
        try
        {
            stored = _official.Add(account);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            ClientLog.Warning("保存在共飞里登录的官方账号失败", ex);
            ActionMessage = "登录成功，但没能保存到这台电脑，请稍后重新登录。";
            return;
        }

        ReloadOfficialAccounts();
        if ((kind == LocalProxyKind.Codex ? CodexTarget : ClaudeTarget)?.AccountId == stored.Id)
        {
            SetError(kind, string.Empty);
        }

        ActionMessage = reauthorize is not null && reauthorize.Id != stored.Id
            ? $"这次登录的是另一个账号「{stored.DisplayName}」，已作为新账号添加；原来的账号仍需重新登录。"
            : reauthorize is not null
                ? $"「{stored.DisplayName}」已重新登录，可以继续使用。"
                : $"已添加「{stored.DisplayName}」。点它右边的「开启」，{ToolName(kind)} 就会用它直连官方。";
    }

    /// <summary>Hands what the user pasted to the sign-in under way.</summary>
    public void SubmitSignIn()
    {
        if (_signIn is not { } session)
        {
            return;
        }

        try
        {
            session.SubmitPasted(PastedSignIn);
            SignInError = string.Empty;
            SignInSubmitted = true;
            SignInPrompt = "正在换取授权…";
        }
        catch (OfficialSignInException ex)
        {
            SignInError = ex.UserMessage;
        }
    }

    /// <summary>Opens the authorization page again (it may have been closed).</summary>
    public void OpenSignInPage()
    {
        if (_signIn is { } session && !_openUrl(session.AuthorizeUrl))
        {
            SignInError = "没能打开浏览器，请点「复制链接」，粘到浏览器里打开。";
        }
    }

    public void CancelSignIn() => EndSignIn();

    private void EndSignIn()
    {
        OfficialSignInSession? session = _signIn;
        _signIn = null;
        session?.Dispose();
        SigningInKind = null;
        SignInUrl = string.Empty;
        SignInPrompt = string.Empty;
        SignInError = string.Empty;
        SignInFailed = false;
        SignInSubmitted = false;
        PastedSignIn = string.Empty;
    }

    /// <summary>Forgets an account signed in within the client, after asking; a tool using it goes back to the relay server.</summary>
    public async Task RemoveAsync(LocalProxyAccountItem item)
    {
        ArgumentNullException.ThrowIfNull(item);
        if (!item.IsOfficial || _official is null)
        {
            return;
        }

        LocalProxyTarget? current = item.Kind == LocalProxyKind.Codex ? CodexTarget : ClaudeTarget;
        bool active = current?.AccountId == item.Id;
        string message = active
            ? $"「{item.Name}」正在被 {ToolName(item.Kind)} 使用。删除后 {ToolName(item.Kind)} 会改回经中转站。\n\n确定删除吗？"
            : $"确定删除「{item.Name}」吗？\n\n只会删除这台电脑上保存的登录，不影响官方账号本身。";
        if (ConfirmEnable is { } confirm && !await confirm(message, "删除").ConfigureAwait(true))
        {
            return;
        }

        if (active)
        {
            Disable(item.Kind);
        }

        try
        {
            _official.Remove(item.Id);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("删除在共飞里登录的官方账号失败", ex);
            ActionMessage = "删除失败，请稍后再试。";
            return;
        }

        ReloadOfficialAccounts();
        ActionMessage = active
            ? $"已删除「{item.Name}」，{ToolName(item.Kind)} 已改回经中转站。"
            : $"已删除「{item.Name}」。";
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
        SetError(outcome.Kind, message);

        // Once per new problem, not once per failed request.
        if (message.Length > 0 && message != previous)
        {
            FailureRaised?.Invoke($"{ToolName(outcome.Kind)} 本地代理「{current.Name}」出错：{message}\n未切回中转站，可在「本地代理」页关闭。");
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

    /// <summary>
    /// Drops everything belonging to the 共飞 account that just signed out: the saved choice, and
    /// the view of its signed-in official accounts (kept on disk, D5).
    /// </summary>
    internal void Reset()
    {
        EndSignIn();
        CodexTarget = null;
        ClaudeTarget = null;
        _claudeCode.SetLocalProxy(null);
        CodexError = string.Empty;
        ClaudeError = string.Empty;
        ActionMessage = string.Empty;
        _restored = false;
        _official?.SetScope(null);
        ReloadOfficialAccounts();

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
        var retired = new List<string>();
        if (saved.CodexAccountId is long codexId)
        {
            if (codexId > 0)
            {
                retired.Add($"Codex 原来用的「{saved.CodexAccountName}」");
            }
            else
            {
                Apply(LocalProxyKind.Codex, new LocalProxyTarget(codexId, NameOf(CodexItems(), codexId, saved.CodexAccountName)));
                CodexError = MissingChoice(CodexItems(), codexId);
            }
        }

        if (saved.ClaudeAccountId is long claudeId)
        {
            if (claudeId > 0)
            {
                retired.Add($"Claude Code 原来用的「{saved.ClaudeAccountName}」");
            }
            else
            {
                Apply(LocalProxyKind.ClaudeCode, new LocalProxyTarget(claudeId, NameOf(ClaudeItems(), claudeId, saved.ClaudeAccountName)));
                ClaudeError = MissingChoice(ClaudeItems(), claudeId);
            }
        }

        if (retired.Count > 0)
        {
            // D7: the user's accounts on the relay server are no longer offered. Said once,
            // and the tool is left on the relay server rather than on something that cannot work.
            Save();
            string message = $"{string.Join("、", retired)}是中转站上的账号，本地代理不再使用中转站上的账号，已改回经中转站。" +
                             "可以改用这台电脑上已登录的账号，或在本地代理页授权共飞AI助手使用你的官方账号。";
            ActionMessage = message;
            NoticeRaised?.Invoke(message);
        }
    }

    /// <summary>Why a restored choice cannot work, or empty. Checked as the account is now.</summary>
    private static string MissingChoice(IReadOnlyList<LocalProxyAccountItem> items, long id)
    {
        LocalProxyAccountItem? item = items.FirstOrDefault(a => a.Id == id);
        if (!OfficialAccountIds.IsOfficial(id))
        {
            return item is { IsUsable: false } local ? "本机官方账号现在用不了：" + local.Detail : string.Empty;
        }

        return item switch
        {
            null => "上次选择的账号已不在，本地代理无法使用。请另选一个账号，或改回经中转站。",
            { IsUsable: false } => "上次选择的账号授权已失效，请在下面点「重新授权」。",
            _ => string.Empty,
        };
    }

    private IReadOnlyList<LocalProxyAccountItem> CodexItems() =>
        LocalCodexAccount is { } local ? [local, .. CodexSignIns] : [.. CodexSignIns];

    private IReadOnlyList<LocalProxyAccountItem> ClaudeItems() =>
        LocalClaudeAccount is { } local ? [local, .. ClaudeSignIns] : [.. ClaudeSignIns];

    private static string NameOf(IEnumerable<LocalProxyAccountItem> items, long id, string? fallback) =>
        items.FirstOrDefault(a => a.Id == id)?.Name ?? fallback ?? $"账号 {id}";

    /// <summary>
    /// Looks for this machine's own sign-ins again. An item is replaced only when something the
    /// page shows changed, so an unchanged row is not rebuilt under the user's pointer.
    /// </summary>
    internal void ProbeLocalAccounts()
    {
        LocalCodexAccount = Probed(_localCodex, LocalCodexAccount);
        LocalClaudeAccount = Probed(_localClaude, LocalClaudeAccount);
        MarkActive();
    }

    private static LocalProxyAccountItem? Probed(ILocalMachineAccount? account, LocalProxyAccountItem? current)
    {
        if (account is null)
        {
            return null;
        }

        var fresh = new LocalProxyAccountItem(account.Kind, account.Probe());
        return current is not null && current.SameAs(fresh) ? current : fresh;
    }

    /// <summary>The client's own signed-in accounts again, per tool; unchanged rows are kept.</summary>
    internal void ReloadOfficialAccounts()
    {
        IReadOnlyList<OfficialAccount> accounts = _official?.List() ?? [];
        Sync(CodexSignIns, accounts.Where(a => a.Kind == LocalProxyKind.Codex));
        Sync(ClaudeSignIns, accounts.Where(a => a.Kind == LocalProxyKind.ClaudeCode));
        OnPropertyChanged(nameof(HasCodexSignIns));
        OnPropertyChanged(nameof(HasClaudeSignIns));
        MarkActive();
    }

    private static void Sync(ObservableCollection<LocalProxyAccountItem> items, IEnumerable<OfficialAccount> accounts)
    {
        List<LocalProxyAccountItem> fresh = [.. accounts.OrderByDescending(a => a.AddedAt).Select(a => new LocalProxyAccountItem(a))];
        if (fresh.Count == items.Count && fresh.Zip(items).All(pair => pair.First.SameAs(pair.Second)))
        {
            return;
        }

        items.Clear();
        foreach (LocalProxyAccountItem item in fresh)
        {
            items.Add(item);
        }
    }

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

    private void MarkActive()
    {
        foreach (LocalProxyAccountItem item in CodexItems())
        {
            item.IsActive = CodexTarget?.AccountId == item.Id;
            item.IsRecommended = item.IsLocal && item.IsUsable && CodexTarget is null;
        }
        foreach (LocalProxyAccountItem item in ClaudeItems())
        {
            item.IsActive = ClaudeTarget?.AccountId == item.Id;
            item.IsRecommended = item.IsLocal && item.IsUsable && ClaudeTarget is null;
        }

        OnPropertyChanged(nameof(CodexSignInIsPrimary));
        OnPropertyChanged(nameof(ClaudeSignInIsPrimary));
        OnPropertyChanged(nameof(ShowCodexSignInButton));
        OnPropertyChanged(nameof(ShowCodexSignInLink));
        OnPropertyChanged(nameof(ShowClaudeSignInButton));
        OnPropertyChanged(nameof(ShowClaudeSignInLink));
    }
}
