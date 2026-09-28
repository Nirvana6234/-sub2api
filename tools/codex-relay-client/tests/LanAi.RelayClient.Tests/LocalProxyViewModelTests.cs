using System.Net;
using System.Net.Http;
using System.Text;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The 本地代理 page: this machine's sign-ins first, official accounts signed in within the client,
/// switching, remembering, and never falling back quietly.
/// </summary>
public sealed class LocalProxyViewModelTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), "local-proxy-vm-" + Guid.NewGuid().ToString("N"));

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    private sealed class MemoryChoiceStore : ILocalProxyPreferenceStore
    {
        public LocalProxyChoice Saved { get; set; } = LocalProxyChoice.None;

        public LocalProxyChoice Load() => Saved;

        public void Save(LocalProxyChoice choice) => Saved = choice;
    }

    private sealed class MemoryUsageStore : ILocalProxyUsageStore
    {
        public List<LocalProxyUsageDay> Days { get; } = [];

        public IReadOnlyList<LocalProxyUsageDay> Load() => Days;

        public void Add(LocalProxyUsage usage) { }
    }

    internal sealed class FakeReachability : IOfficialReachability
    {
        public bool Reachable { get; set; } = true;

        public List<Uri> Checked { get; } = [];

        public Task<Reachability> CheckAsync(Uri officialEndpoint, CancellationToken cancellationToken = default)
        {
            lock (Checked)
            {
                Checked.Add(officialEndpoint);
            }
            return Task.FromResult(Reachable
                ? new Reachability(true, "系统代理 127.0.0.1:7897", null)
                : new Reachability(false, "直连（未检测到系统代理）", "10 秒内没有响应"));
        }
    }

    /// <summary>This machine's own sign-in, as the page and the relay see it.</summary>
    internal sealed class FakeLocalAccount(LocalProxyKind kind) : ILocalMachineAccount
    {
        public LocalMachineAccountStatus Status { get; set; } =
            new(LocalMachineAccountState.SignedIn, kind == LocalProxyKind.ClaudeCode ? "本机 Claude 登录（me · Max）" : "本机 ChatGPT 登录（me · Plus）", string.Empty);

        public List<bool> Requests { get; } = [];

        public LocalProxyKind Kind => kind;

        public LocalMachineAccountStatus Probe() => Status;

        public Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken)
        {
            Requests.Add(forceRefresh);
            return Status.IsUsable
                ? Task.FromResult(new LocalProxyCredential(accountId: accountId, accessToken: "local"))
                : Task.FromException<LocalProxyCredential>(new LocalProxyCredentialException(Status.Detail));
        }

        public void Clear()
        {
        }
    }

    private sealed class ClaudeTokenServer : HttpMessageHandler
    {
        public List<string> Bodies { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            Bodies.Add(await request.Content!.ReadAsStringAsync(cancellationToken));
            return new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent("""
                    {"access_token":"cat","refresh_token":"crt","expires_in":28800,
                     "account":{"uuid":"uuid-7","email_address":"me@claude.test"},"organization":{"uuid":"org-7"}}
                    """, Encoding.UTF8, "application/json"),
            };
        }
    }

    private sealed class Rig
    {
        public required FakeReachability Network { get; init; }

        public required DashboardViewModel Dashboard { get; init; }

        public required RelaySessionManager Session { get; init; }

        public required FakeCodexStartup Codex { get; init; }

        public required MemoryChoiceStore Choice { get; init; }

        public required FakeLocalAccount LocalCodex { get; init; }

        public required FakeLocalAccount LocalClaude { get; init; }

        public required OfficialAccountStore Official { get; init; }

        public required ClaudeTokenServer ClaudeServer { get; init; }

        public required List<Uri> Opened { get; init; }

        public List<string> Notified { get; } = [];

        public LocalProxyViewModel LocalProxy => Dashboard.LocalProxy;
    }

    private async Task<Rig> SignedInAsync(
        LocalProxyChoice? saved = null,
        bool reachable = true,
        Action<FakeLocalAccount, FakeLocalAccount>? arrangeLocal = null,
        Action<OfficialAccountStore>? arrangeOfficial = null)
    {
        var network = new FakeReachability { Reachable = reachable };
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () =>
            [
                new RelayGroup { Id = 11, Name = "GPT", RateMultiplier = 1, SubscriptionType = "standard", Platform = "openai" },
                new RelayGroup { Id = 12, Name = "Claude", RateMultiplier = 1, SubscriptionType = "standard", Platform = "anthropic" },
            ],
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var choice = new MemoryChoiceStore { Saved = saved ?? LocalProxyChoice.None };
        var localCodex = new FakeLocalAccount(LocalProxyKind.Codex);
        var localClaude = new FakeLocalAccount(LocalProxyKind.ClaudeCode);
        arrangeLocal?.Invoke(localCodex, localClaude);
        var official = new OfficialAccountStore(new TestSnapshotProtector(), _root);
        var claudeServer = new ClaudeTokenServer();
        var opened = new List<Uri>();
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("t")),
            codex,
            pluginSupportPreferences: new FakePluginSupportPreferenceStore(),
            localProxyCredentials: new LocalProxyCredentialRouter(localCodex, localClaude, new OfficialAccountSource(official, new FakeTokenRefresher())),
            localProxyPreferences: choice,
            localProxyUsage: new MemoryUsageStore(),
            localProxyReachability: network,
            // Claude signs in by pasting: no port is listened on in these tests.
            localOfficialSignIn: kind => OfficialSignInSession.Start(kind, new OfficialTokenExchanger(handler: _ => claudeServer)),
            // Never the real browser from a test.
            openUrl: url =>
            {
                opened.Add(url);
                return true;
            });
        var rig = new Rig
        {
            Dashboard = dashboard, Session = session, Codex = codex, Choice = choice, Network = network,
            LocalCodex = localCodex, LocalClaude = localClaude, Official = official, ClaudeServer = claudeServer,
            Opened = opened,
        };
        dashboard.LocalProxy.FailureRaised += rig.Notified.Add;
        await session.SignInAsync("a@b.com", "pw");
        if (arrangeOfficial is not null)
        {
            official.SetScope(LanAi.RelayClient.WeChatIntent.JevApiKeyStore.ScopeFor(ClientOptions.ServerAddress, "a@b.com"));
            arrangeOfficial(official);
        }

        await dashboard.RefreshAsync();
        return rig;
    }

    private static readonly LocalMachineAccountStatus NotSignedIn =
        new(LocalMachineAccountState.NotSignedIn, "本机 Claude 登录", "请在终端运行 claude 登录");

    private static OfficialAccount ClaudeAccount(string refresh = "crt-1") =>
        OfficialAccountStoreTests.Claude(refresh: refresh) with { ExpiresAt = DateTimeOffset.UtcNow.AddHours(8) };

    // ---- This machine's own sign-ins (D9: first, recommended, no sign-in needed). ----

    [Fact]
    public async Task ThisMachinesSignInsAreListedPerToolWithWhetherTheyCanBeUsed()
    {
        Rig rig = await SignedInAsync(arrangeLocal: (_, claude) => claude.Status = NotSignedIn);

        LocalProxyAccountItem codex = rig.LocalProxy.LocalCodexAccount!;
        Assert.Equal(LocalMachineAccounts.CodexId, codex.Id);
        Assert.True(codex.IsLocal);
        Assert.True(codex.CanToggle);
        Assert.True(codex.IsRecommended);
        Assert.Equal("本机 ChatGPT 登录（me · Plus）", codex.Name);
        Assert.Contains("不上传中转站", codex.StatusText);

        LocalProxyAccountItem claude = rig.LocalProxy.LocalClaudeAccount!;
        Assert.False(claude.CanToggle);
        Assert.False(claude.IsRecommended);
        Assert.Contains("未登录", claude.StatusText);
    }

    [Fact]
    public async Task SigningInWithinTheClientIsOfferedPlainlyOnlyWhenThisMachineHasNothing()
    {
        Rig rig = await SignedInAsync(arrangeLocal: (_, claude) => claude.Status = NotSignedIn);

        Assert.False(rig.LocalProxy.ShowCodexSignInButton);
        Assert.True(rig.LocalProxy.ShowCodexSignInLink);
        Assert.True(rig.LocalProxy.ShowClaudeSignInButton);
        Assert.False(rig.LocalProxy.ShowClaudeSignInLink);
    }

    [Fact]
    public async Task SwitchingCodexOnToThisMachinesSignInSpendsNoRefreshAndIsRemembered()
    {
        Rig rig = await SignedInAsync();

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalCodexAccount!);

        LocalProxyTarget expected = new(LocalMachineAccounts.CodexId, "本机 ChatGPT 登录（me · Plus）");
        Assert.Equal((LocalProxyKind.Codex, expected), rig.Codex.LocalProxies[^1]);
        Assert.Equal([false], rig.LocalCodex.Requests);
        Assert.Equal(LocalMachineAccounts.CodexId, rig.Choice.Saved.CodexAccountId);
        Assert.True(rig.LocalProxy.LocalCodexAccount!.IsActive);
        Assert.False(rig.LocalProxy.LocalCodexAccount!.IsRecommended);
        Assert.False(rig.Dashboard.CanChooseGroup);
        Assert.Equal("正在使用本地代理：本机 ChatGPT 登录（me · Plus）", rig.LocalProxy.CodexStatusText);
    }

    [Fact]
    public async Task SwitchingOffGoesBackToTheRelayServer()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalCodexAccount!);

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalCodexAccount!);

        Assert.Equal((LocalProxyKind.Codex, (LocalProxyTarget?)null), rig.Codex.LocalProxies[^1]);
        Assert.Null(rig.Choice.Saved.CodexAccountId);
        Assert.True(rig.Dashboard.CanChooseGroup);
    }

    [Fact]
    public async Task ACodexOnAClaudeGroupIsNotSwitched()
    {
        Rig rig = await SignedInAsync();
        await rig.Dashboard.SwitchGroupAsync(rig.Dashboard.Groups.Single(g => g.Id == 12));

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalCodexAccount!);

        Assert.DoesNotContain(rig.Codex.LocalProxies, p => p.Kind == LocalProxyKind.Codex);
        Assert.Contains("Claude 分组", rig.LocalProxy.ActionMessage);
    }

    [Fact]
    public async Task ClaudeCodeOnThisMachinesSignInIsSetUpWithoutAGroup()
    {
        Rig rig = await SignedInAsync();
        Assert.False(rig.Dashboard.ClaudeCode.PluginSupportEnabled);

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalClaudeAccount!);
        await rig.Dashboard.ClaudePreference.LoadAsync();
        await rig.Dashboard.ClaudeCode.SyncPluginSupportAsync();

        Assert.True(rig.Dashboard.ClaudeCode.PluginSupportEnabled);
        Assert.False(rig.Dashboard.ClaudeCode.CanChooseGroup);
        PluginSupportRequest request = rig.Codex.PluginRequests[^1];
        Assert.Equal(LocalMachineAccounts.ClaudeId, request.LocalProxyAccountId);
        Assert.True(request.Enabled);
    }

    [Fact]
    public async Task AFailureIsShownAndNotifiedOncePerNewProblemAndTheToolStaysPut()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalCodexAccount!);
        long id = LocalMachineAccounts.CodexId;

        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(LocalProxyKind.Codex, id, false, "额度用完"));
        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(LocalProxyKind.Codex, id, false, "额度用完"));

        Assert.Single(rig.Notified);
        Assert.Contains("未切回中转站", rig.Notified[0]);
        Assert.True(rig.LocalProxy.HasError);
        Assert.True(rig.LocalProxy.IsCodexActive);

        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(LocalProxyKind.Codex, id, true, null));
        Assert.False(rig.LocalProxy.HasError);

        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(LocalProxyKind.Codex, 999, false, "x"));
        Assert.False(rig.LocalProxy.HasError);
    }

    [Fact]
    public async Task ASignInThatCannotBeUsedIsNotSwitchedOnAndSaysWhy()
    {
        Rig rig = await SignedInAsync(arrangeLocal: (_, claude) => claude.Status = NotSignedIn);

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalClaudeAccount!);

        Assert.DoesNotContain(rig.Codex.LocalProxies, p => p.Kind == LocalProxyKind.ClaudeCode);
        Assert.Equal("请在终端运行 claude 登录", rig.LocalProxy.ActionMessage);
        Assert.Empty(rig.LocalClaude.Requests);
    }

    [Fact]
    public async Task ASignInLostBetweenTheProbeAndTheClickIsRefusedInWords()
    {
        Rig rig = await SignedInAsync();
        rig.LocalCodex.Status = new(LocalMachineAccountState.NotSignedIn, "x", "本机 ChatGPT 登录已失效");
        LocalProxyAccountItem stale = rig.LocalProxy.LocalCodexAccount!;

        await rig.LocalProxy.ToggleAsync(stale);

        Assert.Empty(rig.Codex.LocalProxies);
        Assert.Contains("开启失败：本机 ChatGPT 登录已失效", rig.LocalProxy.ActionMessage);
    }

    [Fact]
    public async Task ASignInMadeLaterShowsUpOnTheNextRefresh()
    {
        Rig rig = await SignedInAsync(arrangeLocal: (_, claude) => claude.Status = NotSignedIn);
        Assert.False(rig.LocalProxy.LocalClaudeAccount!.CanToggle);

        rig.LocalClaude.Status = new(LocalMachineAccountState.SignedIn, "本机 Claude 登录（me · Max）", string.Empty);
        await rig.Dashboard.RefreshAsync();

        Assert.True(rig.LocalProxy.LocalClaudeAccount!.CanToggle);
        Assert.False(rig.LocalProxy.ShowClaudeSignInButton);
    }

    [Fact]
    public async Task ASignInBehindAPromptIsSaidBeforeItIsSwitchedOn()
    {
        Rig rig = await SignedInAsync(arrangeLocal: (_, claude) =>
            claude.Status = new(LocalMachineAccountState.SignedIn, "本机 Claude 登录", LocalClaudeAccount.ConsentDetail));
        var asked = new List<string>();
        rig.LocalProxy.ConfirmEnable = (message, _) =>
        {
            asked.Add(message);
            return Task.FromResult(true);
        };

        Assert.Contains("始终允许", rig.LocalProxy.LocalClaudeAccount!.StatusText);
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalClaudeAccount!);

        Assert.Contains("始终允许", Assert.Single(asked));
        Assert.True(rig.LocalProxy.IsClaudeActive);
    }

    [Fact]
    public async Task ARestoredLocalChoiceComesBackUnderItsCurrentName()
    {
        Rig rig = await SignedInAsync(new LocalProxyChoice { CodexAccountId = LocalMachineAccounts.CodexId, CodexAccountName = "旧名字" });

        Assert.Equal(new LocalProxyTarget(LocalMachineAccounts.CodexId, "本机 ChatGPT 登录（me · Plus）"), rig.LocalProxy.CodexTarget);
        Assert.False(rig.LocalProxy.HasCodexError);
        Assert.True(rig.LocalProxy.LocalCodexAccount!.IsActive);
    }

    [Fact]
    public async Task ARestoredLocalChoiceWhoseSignInIsGoneStaysOnAndSaysSo()
    {
        Rig rig = await SignedInAsync(
            new LocalProxyChoice { ClaudeAccountId = LocalMachineAccounts.ClaudeId, ClaudeAccountName = "本机 Claude 登录" },
            arrangeLocal: (_, claude) => claude.Status = NotSignedIn);

        Assert.Equal(LocalMachineAccounts.ClaudeId, rig.LocalProxy.ClaudeTarget?.AccountId);
        Assert.Contains("本机官方账号现在用不了", rig.LocalProxy.ClaudeError);
        Assert.True(rig.LocalProxy.LocalClaudeAccount!.CanToggle);
    }

    [Fact]
    public async Task SigningOutForgetsTheChoiceButKeepsListingThisMachinesSignIns()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalCodexAccount!);

        rig.Dashboard.Reset();

        Assert.Null(rig.LocalProxy.CodexTarget);
        Assert.Equal(LocalProxyChoice.None, rig.Choice.Saved);
        Assert.True(rig.LocalProxy.HasLocalCodexAccount);
        Assert.False(rig.LocalProxy.LocalCodexAccount!.IsActive);
    }

    // ---- The relay server's accounts are gone (D7). ----

    [Fact]
    public async Task ARelayAccountChosenInAnOlderClientIsDroppedOnceWithANotice()
    {
        Rig rig = await SignedInAsync(new LocalProxyChoice
        {
            CodexAccountId = 7, CodexAccountName = "我的 Plus",
            ClaudeAccountId = LocalMachineAccounts.ClaudeId, ClaudeAccountName = "本机 Claude 登录",
        });

        Assert.Null(rig.LocalProxy.CodexTarget);
        Assert.DoesNotContain(rig.Codex.LocalProxies, p => p.Target?.AccountId == 7);
        Assert.Null(rig.Choice.Saved.CodexAccountId);
        Assert.Equal(LocalMachineAccounts.ClaudeId, rig.Choice.Saved.ClaudeAccountId);
        Assert.Contains("中转站上的账号", Assert.Single(rig.Notified));
        Assert.Contains("我的 Plus", rig.LocalProxy.ActionMessage);

        await rig.Dashboard.RefreshAsync();
        Assert.Single(rig.Notified);
    }

    // ---- Official accounts signed in within the client. ----

    [Fact]
    public async Task SigningInToClaudeByPasteAddsAnAccountThatCanBeSwitchedOn()
    {
        Rig rig = await SignedInAsync(arrangeLocal: (_, claude) => claude.Status = NotSignedIn);

        Task signingIn = rig.LocalProxy.StartSignInAsync(LocalProxyKind.ClaudeCode);
        Assert.True(rig.LocalProxy.IsClaudeSigningIn);
        Assert.False(rig.LocalProxy.ShowClaudeSignInButton);
        Assert.StartsWith("https://claude.com/cai/oauth/authorize?", rig.LocalProxy.SignInUrl);
        Assert.Equal(rig.LocalProxy.SignInUrl, Assert.Single(rig.Opened).ToString());
        string state = OfficialAuthorization.ParseQuery(new Uri(rig.LocalProxy.SignInUrl).Query.TrimStart('?'))["state"];

        rig.LocalProxy.PastedSignIn = "not a code";
        rig.LocalProxy.SubmitSignIn();
        Assert.True(rig.LocalProxy.HasSignInError);

        rig.LocalProxy.PastedSignIn = $"the-code#{state}";
        rig.LocalProxy.SubmitSignIn();
        await signingIn.WaitAsync(TimeSpan.FromSeconds(10));

        Assert.False(rig.LocalProxy.IsClaudeSigningIn);
        LocalProxyAccountItem added = Assert.Single(rig.LocalProxy.ClaudeSignIns);
        Assert.Equal(OfficialAccountIds.First, added.Id);
        Assert.True(added.IsOfficial);
        Assert.Equal("me@claude.test", added.Name);
        Assert.Contains("已添加", rig.LocalProxy.ActionMessage);
        Assert.False(rig.LocalProxy.ShowClaudeSignInButton);

        await rig.LocalProxy.ToggleAsync(added);

        Assert.Equal(OfficialAccountIds.First, rig.LocalProxy.ClaudeTarget?.AccountId);
        Assert.Equal(OfficialAccountIds.First, rig.Choice.Saved.ClaudeAccountId);
    }

    [Fact]
    public async Task CancellingASignInLeavesNothingBehind()
    {
        Rig rig = await SignedInAsync();

        Task signingIn = rig.LocalProxy.StartSignInAsync(LocalProxyKind.ClaudeCode);
        rig.LocalProxy.CancelSignIn();
        await signingIn.WaitAsync(TimeSpan.FromSeconds(10));

        Assert.Null(rig.LocalProxy.SigningInKind);
        Assert.Empty(rig.LocalProxy.ClaudeSignIns);
        Assert.Empty(rig.ClaudeServer.Bodies);
    }

    [Fact]
    public async Task TheClientsOwnAccountsBelongToThe共飞UserSignedIn()
    {
        Rig rig = await SignedInAsync(arrangeOfficial: store => store.Add(ClaudeAccount()));
        Assert.Single(rig.LocalProxy.ClaudeSignIns);

        rig.Dashboard.Reset();
        Assert.Empty(rig.LocalProxy.ClaudeSignIns);

        await rig.Session.SignInAsync("a@b.com", "pw");
        await rig.Dashboard.RefreshAsync();
        Assert.Single(rig.LocalProxy.ClaudeSignIns);
    }

    [Fact]
    public async Task RemovingTheAccountInUseAsksAndGoesBackToTheRelayServer()
    {
        Rig rig = await SignedInAsync(arrangeOfficial: store => store.Add(ClaudeAccount()));
        LocalProxyAccountItem account = rig.LocalProxy.ClaudeSignIns[0];
        await rig.LocalProxy.ToggleAsync(account);
        var asked = new List<string>();
        rig.LocalProxy.ConfirmEnable = (message, _) =>
        {
            asked.Add(message);
            return Task.FromResult(true);
        };

        await rig.LocalProxy.RemoveAsync(rig.LocalProxy.ClaudeSignIns[0]);

        Assert.Contains("正在被 Claude Code 使用", Assert.Single(asked));
        Assert.Null(rig.LocalProxy.ClaudeTarget);
        Assert.Empty(rig.LocalProxy.ClaudeSignIns);
        Assert.Empty(rig.Official.List());
    }

    [Fact]
    public async Task AGoneAccountSaysSoAndCannotBeSwitchedOn()
    {
        Rig rig = await SignedInAsync(arrangeOfficial: store => store.Add(ClaudeAccount()));
        OfficialAccount stored = rig.Official.List().Single();

        rig.Official.Update(stored.Id, stored.RefreshToken, a => a with { InvalidReason = "登录已失效" });

        LocalProxyAccountItem item = rig.LocalProxy.ClaudeSignIns.Single();
        Assert.False(item.CanToggle);
        Assert.Contains("重新登录", item.StatusText);
        await rig.LocalProxy.ToggleAsync(item);
        Assert.Contains("重新登录", rig.LocalProxy.ActionMessage);
        Assert.Null(rig.LocalProxy.ClaudeTarget);
    }

    [Fact]
    public async Task ARestoredChoiceOfARemovedAccountStaysOnAndSaysSo()
    {
        Rig rig = await SignedInAsync(new LocalProxyChoice { ClaudeAccountId = -1005, ClaudeAccountName = "旧账号" });

        Assert.Equal(-1005, rig.LocalProxy.ClaudeTarget?.AccountId);
        Assert.Contains("已不在", rig.LocalProxy.ClaudeError);
    }
}
