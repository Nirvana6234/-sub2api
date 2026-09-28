using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>The 本地代理 page: listing, switching, remembering, and never falling back quietly.</summary>
public sealed class LocalProxyViewModelTests
{
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

    private sealed class Rig
    {
        public required FakeReachability Network { get; init; }

        public required DashboardViewModel Dashboard { get; init; }

        public required FakeRelayClient Relay { get; init; }

        public required FakeCodexStartup Codex { get; init; }

        public required MemoryChoiceStore Choice { get; init; }

        public LocalProxyViewModel LocalProxy => Dashboard.LocalProxy;

        public FakeLocalAccount? LocalCodex { get; init; }

        public FakeLocalAccount? LocalClaude { get; init; }
    }

    private static readonly ContributionAccount Plus =
        new(id: 7, name: "我的 Plus", platform: "openai", type: "oauth", status: "active",
            credentials: new ContributionAccountCredentials("plus"));

    private static readonly ContributionAccount Max =
        new(id: 8, name: "Max", platform: "anthropic", type: "oauth", status: "active");

    private static readonly ContributionAccount Key =
        new(id: 9, name: "key", platform: "openai", type: "apikey", status: "active");

    private static async Task<Rig> SignedInAsync(
        LocalProxyChoice? saved = null,
        Func<IReadOnlyList<ContributionAccount>>? accounts = null,
        bool reachable = true,
        bool withLocalAccounts = false,
        Action<FakeLocalAccount, FakeLocalAccount>? arrangeLocal = null)
    {
        var network = new FakeReachability { Reachable = reachable };
        var relay = new FakeRelayClient
        {
            OnListContributionAccounts = accounts ?? (() => [Plus, Max, Key]),
            OnLocalProxyCredential = id => new LocalProxyCredential(accountId: id, accessToken: $"at-{id}"),
            OnAvailableGroups = () =>
            [
                new RelayGroup { Id = 11, Name = "GPT", RateMultiplier = 1, SubscriptionType = "standard", Platform = "openai" },
                new RelayGroup { Id = 12, Name = "Claude", RateMultiplier = 1, SubscriptionType = "standard", Platform = "anthropic" },
            ],
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var choice = new MemoryChoiceStore { Saved = saved ?? LocalProxyChoice.None };
        var relayCredentials = new LocalProxyCredentialCache(relay, _ => Task.FromResult("jwt"));
        FakeLocalAccount? localCodex = null;
        FakeLocalAccount? localClaude = null;
        ILocalProxyCredentialSource credentials = relayCredentials;
        if (withLocalAccounts)
        {
            localCodex = new FakeLocalAccount(LocalProxyKind.Codex);
            localClaude = new FakeLocalAccount(LocalProxyKind.ClaudeCode);
            arrangeLocal?.Invoke(localCodex, localClaude);
            credentials = new LocalProxyCredentialRouter(relayCredentials, localCodex, localClaude);
        }
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("t")),
            codex,
            pluginSupportPreferences: new FakePluginSupportPreferenceStore(),
            localProxyCredentials: credentials,
            localProxyPreferences: choice,
            localProxyUsage: new MemoryUsageStore(),
            localProxyReachability: network);
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();
        return new Rig
        {
            Dashboard = dashboard, Relay = relay, Codex = codex, Choice = choice, Network = network,
            LocalCodex = localCodex, LocalClaude = localClaude,
        };
    }

    [Fact]
    public async Task AccountsAreSortedByWhichToolTheyCanServe()
    {
        Rig rig = await SignedInAsync();

        Assert.Equal([7L], rig.LocalProxy.CodexAccounts.Select(a => a.Id));
        Assert.Equal([8L], rig.LocalProxy.ClaudeAccounts.Select(a => a.Id));
        Assert.Equal([9L], rig.LocalProxy.OtherAccounts.Select(a => a.Id));
        Assert.Equal("plus", rig.LocalProxy.CodexAccounts[0].PlanText);
        Assert.False(rig.LocalProxy.HasAccountsMessage);
    }

    [Fact]
    public async Task AUserWithoutTheFeatureIsToldSoInWords()
    {
        Rig rig = await SignedInAsync(accounts: () => throw new RelayApiException(RelayFailure.Forbidden, "nope"));

        Assert.Contains("未开通", rig.LocalProxy.AccountsMessage);
        Assert.Empty(rig.LocalProxy.CodexAccounts);
        Assert.False(rig.Dashboard.RefreshState.IsRateLimited);
    }

    [Fact]
    public async Task SwitchingCodexOnReachesTheRelayIsRememberedAndFreesItFromTheGroup()
    {
        Rig rig = await SignedInAsync();

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.CodexAccounts[0]);

        Assert.Equal((LocalProxyKind.Codex, new LocalProxyTarget(7, "我的 Plus")), rig.Codex.LocalProxies[^1]);
        Assert.True(rig.LocalProxy.CodexAccounts[0].IsActive);
        Assert.Equal(7, rig.Choice.Saved.CodexAccountId);
        Assert.False(rig.Dashboard.CanChooseGroup);
        Assert.Contains(7L, rig.Relay.LocalProxyCredentialRequests);
    }

    [Fact]
    public async Task SwitchingOffGoesBackToTheRelayServer()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.CodexAccounts[0]);

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.CodexAccounts[0]);

        Assert.Equal((LocalProxyKind.Codex, (LocalProxyTarget?)null), rig.Codex.LocalProxies[^1]);
        Assert.Null(rig.Choice.Saved.CodexAccountId);
        Assert.True(rig.Dashboard.CanChooseGroup);
    }

    [Fact]
    public async Task ACodexOnAClaudeGroupIsNotSwitched()
    {
        Rig rig = await SignedInAsync();
        await rig.Dashboard.SwitchGroupAsync(rig.Dashboard.Groups.Single(g => g.Id == 12));

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.CodexAccounts[0]);

        Assert.DoesNotContain(rig.Codex.LocalProxies, p => p.Kind == LocalProxyKind.Codex);
        Assert.Contains("Claude 分组", rig.LocalProxy.ActionMessage);
    }

    [Fact]
    public async Task ARefusedTokenLeavesTheToolWhereItWas()
    {
        Rig rig = await SignedInAsync();
        rig.Relay.OnLocalProxyCredential = _ => throw new RelayApiException(RelayFailure.Forbidden, "账号已停用");

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.CodexAccounts[0]);

        Assert.Empty(rig.Codex.LocalProxies);
        Assert.Contains("开启失败", rig.LocalProxy.ActionMessage);
    }

    [Fact]
    public async Task ClaudeCodeOnALocalProxyIsSwitchedOnAndNeedsNoGroup()
    {
        Rig rig = await SignedInAsync();
        Assert.False(rig.Dashboard.ClaudeCode.PluginSupportEnabled);

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.ClaudeAccounts[0]);
        await rig.Dashboard.ClaudePreference.LoadAsync();
        await rig.Dashboard.ClaudeCode.SyncPluginSupportAsync();

        Assert.True(rig.Dashboard.ClaudeCode.PluginSupportEnabled);
        Assert.False(rig.Dashboard.ClaudeCode.CanChooseGroup);
        PluginSupportRequest request = rig.Codex.PluginRequests[^1];
        Assert.Equal(8, request.LocalProxyAccountId);
        Assert.True(request.Enabled);
        Assert.Contains(rig.Codex.LocalProxies, p => p.Kind == LocalProxyKind.ClaudeCode && p.Target?.AccountId == 8);
    }

    [Fact]
    public async Task AFailureIsShownAndNotifiedOncePerNewProblemAndTheToolStaysPut()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.CodexAccounts[0]);
        var notified = new List<string>();
        rig.LocalProxy.FailureRaised += notified.Add;

        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(LocalProxyKind.Codex, 7, false, "额度用完"));
        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(LocalProxyKind.Codex, 7, false, "额度用完"));

        Assert.Single(notified);
        Assert.Contains("未切回中转站", notified[0]);
        Assert.True(rig.LocalProxy.HasError);
        Assert.True(rig.LocalProxy.IsCodexActive);

        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(LocalProxyKind.Codex, 7, true, null));
        Assert.False(rig.LocalProxy.HasError);
    }

    [Fact]
    public async Task AReportAboutAnotherAccountIsIgnored()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.CodexAccounts[0]);

        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(LocalProxyKind.Codex, 999, false, "x"));

        Assert.False(rig.LocalProxy.HasError);
    }

    [Fact]
    public async Task ASavedChoiceIsRestoredAfterARestart()
    {
        Rig rig = await SignedInAsync(new LocalProxyChoice { CodexAccountId = 7, CodexAccountName = "我的 Plus" });

        Assert.Equal(new LocalProxyTarget(7, "我的 Plus"), rig.LocalProxy.CodexTarget);
        Assert.Contains((LocalProxyKind.Codex, new LocalProxyTarget(7, "我的 Plus")), rig.Codex.LocalProxies);
        Assert.False(rig.LocalProxy.HasError);
    }

    [Fact]
    public async Task ASavedChoiceWhoseAccountIsGoneStaysOnAndSaysSo()
    {
        Rig rig = await SignedInAsync(new LocalProxyChoice { CodexAccountId = 70, CodexAccountName = "旧账号" });

        Assert.Equal(new LocalProxyTarget(70, "旧账号"), rig.LocalProxy.CodexTarget);
        Assert.True(rig.LocalProxy.HasCodexError);
    }

    [Fact]
    public async Task SigningOutForgetsTheChoice()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.CodexAccounts[0]);

        rig.Dashboard.Reset();

        Assert.Null(rig.LocalProxy.CodexTarget);
        Assert.Equal(LocalProxyChoice.None, rig.Choice.Saved);
        Assert.Empty(rig.LocalProxy.CodexAccounts);
    }

    private static readonly LocalMachineAccountStatus NotSignedIn =
        new(LocalMachineAccountState.NotSignedIn, "本机 Claude 登录", "请在终端运行 claude 登录");

    [Fact]
    public async Task WithoutLocalAccountsThePageListsOnlyTheRelaysAccounts()
    {
        Rig rig = await SignedInAsync();

        Assert.False(rig.LocalProxy.HasLocalCodexAccount);
        Assert.False(rig.LocalProxy.HasLocalClaudeAccount);
    }

    [Fact]
    public async Task ThisMachinesSignInsAreListedPerToolWithWhetherTheyCanBeUsed()
    {
        Rig rig = await SignedInAsync(withLocalAccounts: true, arrangeLocal: (_, claude) => claude.Status = NotSignedIn);

        LocalProxyAccountItem codex = rig.LocalProxy.LocalCodexAccount!;
        Assert.Equal(LocalMachineAccounts.CodexId, codex.Id);
        Assert.True(codex.IsLocal);
        Assert.True(codex.CanToggle);
        Assert.Equal("本机 ChatGPT 登录（me · Plus）", codex.Name);
        Assert.Contains("不上传中转站", codex.StatusText);

        LocalProxyAccountItem claude = rig.LocalProxy.LocalClaudeAccount!;
        Assert.False(claude.CanToggle);
        Assert.Contains("未登录", claude.StatusText);
        Assert.Contains("claude", claude.StatusText);

        Assert.Equal([7L], rig.LocalProxy.CodexAccounts.Select(a => a.Id));
    }

    [Fact]
    public async Task SwitchingCodexOnToThisMachinesSignInSpendsNoRefreshAndIsRemembered()
    {
        Rig rig = await SignedInAsync(withLocalAccounts: true);

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalCodexAccount!);

        LocalProxyTarget expected = new(LocalMachineAccounts.CodexId, "本机 ChatGPT 登录（me · Plus）");
        Assert.Equal((LocalProxyKind.Codex, expected), rig.Codex.LocalProxies[^1]);
        Assert.Equal([false], rig.LocalCodex!.Requests);
        Assert.Empty(rig.Relay.LocalProxyCredentialRequests);
        Assert.Equal(LocalMachineAccounts.CodexId, rig.Choice.Saved.CodexAccountId);
        Assert.True(rig.LocalProxy.LocalCodexAccount!.IsActive);
        Assert.False(rig.LocalProxy.CodexAccounts[0].IsActive);
        Assert.Equal("正在使用本地代理：本机 ChatGPT 登录（me · Plus）", rig.LocalProxy.CodexStatusText);
    }

    [Fact]
    public async Task ASignInThatCannotBeUsedIsNotSwitchedOnAndSaysWhy()
    {
        Rig rig = await SignedInAsync(withLocalAccounts: true, arrangeLocal: (_, claude) => claude.Status = NotSignedIn);

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalClaudeAccount!);

        Assert.DoesNotContain(rig.Codex.LocalProxies, p => p.Kind == LocalProxyKind.ClaudeCode);
        Assert.Equal("请在终端运行 claude 登录", rig.LocalProxy.ActionMessage);
        Assert.Empty(rig.LocalClaude!.Requests);
    }

    [Fact]
    public async Task ASignInLostBetweenTheProbeAndTheClickIsRefusedInWords()
    {
        Rig rig = await SignedInAsync(withLocalAccounts: true);
        rig.LocalCodex!.Status = new(LocalMachineAccountState.NotSignedIn, "x", "本机 ChatGPT 登录已失效");
        LocalProxyAccountItem stale = rig.LocalProxy.LocalCodexAccount!;

        await rig.LocalProxy.ToggleAsync(stale);

        Assert.Empty(rig.Codex.LocalProxies);
        Assert.Contains("开启失败：本机 ChatGPT 登录已失效", rig.LocalProxy.ActionMessage);
    }

    [Fact]
    public async Task ASignInMadeLaterShowsUpOnTheNextRefresh()
    {
        Rig rig = await SignedInAsync(withLocalAccounts: true, arrangeLocal: (_, claude) => claude.Status = NotSignedIn);
        Assert.False(rig.LocalProxy.LocalClaudeAccount!.CanToggle);

        rig.LocalClaude!.Status = new(LocalMachineAccountState.SignedIn, "本机 Claude 登录（me · Max）", string.Empty);
        await rig.Dashboard.RefreshAsync();

        Assert.True(rig.LocalProxy.LocalClaudeAccount!.CanToggle);
    }

    [Fact]
    public async Task ARestoredLocalChoiceComesBackUnderItsCurrentName()
    {
        Rig rig = await SignedInAsync(
            new LocalProxyChoice { CodexAccountId = LocalMachineAccounts.CodexId, CodexAccountName = "旧名字" },
            withLocalAccounts: true);

        Assert.Equal(new LocalProxyTarget(LocalMachineAccounts.CodexId, "本机 ChatGPT 登录（me · Plus）"), rig.LocalProxy.CodexTarget);
        Assert.False(rig.LocalProxy.HasCodexError);
        Assert.True(rig.LocalProxy.LocalCodexAccount!.IsActive);
    }

    [Fact]
    public async Task ARestoredLocalChoiceWhoseSignInIsGoneStaysOnAndSaysSo()
    {
        Rig rig = await SignedInAsync(
            new LocalProxyChoice { ClaudeAccountId = LocalMachineAccounts.ClaudeId, ClaudeAccountName = "本机 Claude 登录" },
            withLocalAccounts: true,
            arrangeLocal: (_, claude) => claude.Status = NotSignedIn);

        Assert.Equal(LocalMachineAccounts.ClaudeId, rig.LocalProxy.ClaudeTarget?.AccountId);
        Assert.Contains("本机官方账号现在用不了", rig.LocalProxy.ClaudeError);
        Assert.True(rig.LocalProxy.LocalClaudeAccount!.CanToggle);
    }

    [Fact]
    public async Task WithoutTheRelayFeatureThisMachinesSignInsStillWork()
    {
        Rig rig = await SignedInAsync(
            accounts: () => throw new RelayApiException(RelayFailure.Forbidden, "nope"),
            withLocalAccounts: true);

        Assert.Contains("本机官方账号不受影响", rig.LocalProxy.AccountsMessage);
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalCodexAccount!);
        Assert.Equal(LocalMachineAccounts.CodexId, rig.LocalProxy.CodexTarget?.AccountId);
    }

    [Fact]
    public async Task SigningOutKeepsListingThisMachinesSignIns()
    {
        Rig rig = await SignedInAsync(withLocalAccounts: true);
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.LocalCodexAccount!);

        rig.Dashboard.Reset();

        Assert.Null(rig.LocalProxy.CodexTarget);
        Assert.True(rig.LocalProxy.HasLocalCodexAccount);
        Assert.False(rig.LocalProxy.LocalCodexAccount!.IsActive);
    }
}
