using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>The 本地代理 page: listing, switching Codex, remembering, and never falling back quietly.</summary>
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
        public IReadOnlyList<LocalProxyUsageDay> Load() => [];

        public void Add(LocalProxyUsage usage) { }
    }

    private sealed class Rig
    {
        public required DashboardViewModel Dashboard { get; init; }

        public required FakeRelayClient Relay { get; init; }

        public required FakeCodexStartup Codex { get; init; }

        public required MemoryChoiceStore Choice { get; init; }

        public LocalProxyViewModel LocalProxy => Dashboard.LocalProxy;
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
        Func<IReadOnlyList<ContributionAccount>>? accounts = null)
    {
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
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("t")),
            codex,
            pluginSupportPreferences: new FakePluginSupportPreferenceStore(),
            localProxyCredentials: new LocalProxyCredentialCache(relay, _ => Task.FromResult("jwt")),
            localProxyPreferences: choice,
            localProxyUsage: new MemoryUsageStore());
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();
        return new Rig { Dashboard = dashboard, Relay = relay, Codex = codex, Choice = choice };
    }

    [Fact]
    public async Task OnlyChatGptSubscriptionAccountsCanBeChosenAndTheRestAreStillListed()
    {
        Rig rig = await SignedInAsync();

        Assert.Equal([7L], rig.LocalProxy.Accounts.Select(a => a.Id));
        Assert.Equal([8L, 9L], rig.LocalProxy.OtherAccounts.Select(a => a.Id));
        Assert.Equal("plus", rig.LocalProxy.Accounts[0].PlanText);
        Assert.False(rig.LocalProxy.HasAccountsMessage);
    }

    [Fact]
    public async Task AUserWithoutTheFeatureIsToldSoInWords()
    {
        Rig rig = await SignedInAsync(accounts: () => throw new RelayApiException(RelayFailure.Forbidden, "nope"));

        Assert.Contains("未开通", rig.LocalProxy.AccountsMessage);
        Assert.Empty(rig.LocalProxy.Accounts);
        Assert.False(rig.Dashboard.RefreshState.IsRateLimited);
    }

    [Fact]
    public async Task SwitchingOnReachesTheRelayIsRememberedAndFreesCodexFromTheGroup()
    {
        Rig rig = await SignedInAsync();

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.Accounts[0]);

        Assert.Equal(new LocalProxyTarget(7, "我的 Plus"), rig.Codex.LocalProxies[^1]);
        Assert.True(rig.LocalProxy.Accounts[0].IsActive);
        Assert.Equal(7, rig.Choice.Saved.CodexAccountId);
        Assert.False(rig.Dashboard.CanChooseGroup);
        Assert.Contains(7L, rig.Relay.LocalProxyCredentialRequests);
    }

    [Fact]
    public async Task SwitchingOffGoesBackToTheRelayServer()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.Accounts[0]);

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.Accounts[0]);

        Assert.Null(rig.Codex.LocalProxies[^1]);
        Assert.Null(rig.Choice.Saved.CodexAccountId);
        Assert.True(rig.Dashboard.CanChooseGroup);
    }

    [Fact]
    public async Task ACodexOnAClaudeGroupIsNotSwitched()
    {
        Rig rig = await SignedInAsync();
        await rig.Dashboard.SwitchGroupAsync(rig.Dashboard.Groups.Single(g => g.Id == 12));

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.Accounts[0]);

        Assert.Empty(rig.Codex.LocalProxies);
        Assert.Contains("Claude 分组", rig.LocalProxy.ActionMessage);
    }

    [Fact]
    public async Task ARefusedTokenLeavesCodexWhereItWas()
    {
        Rig rig = await SignedInAsync();
        rig.Relay.OnLocalProxyCredential = _ => throw new RelayApiException(RelayFailure.Forbidden, "账号已停用");

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.Accounts[0]);

        Assert.Empty(rig.Codex.LocalProxies);
        Assert.Contains("开启失败", rig.LocalProxy.ActionMessage);
    }

    [Fact]
    public async Task AFailureIsShownAndNotifiedOncePerNewProblemAndCodexStaysPut()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.Accounts[0]);
        var notified = new List<string>();
        rig.LocalProxy.FailureRaised += notified.Add;

        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(7, false, "额度用完"));
        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(7, false, "额度用完"));

        Assert.Single(notified);
        Assert.Contains("未切回中转站", notified[0]);
        Assert.True(rig.LocalProxy.HasError);
        Assert.True(rig.LocalProxy.IsActive);

        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(7, true, null));
        Assert.False(rig.LocalProxy.HasError);
    }

    [Fact]
    public async Task AReportAboutAnotherAccountIsIgnored()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.Accounts[0]);

        rig.LocalProxy.ApplyOutcome(new LocalProxyOutcome(999, false, "x"));

        Assert.False(rig.LocalProxy.HasError);
    }

    [Fact]
    public async Task ASavedChoiceIsRestoredAfterARestart()
    {
        Rig rig = await SignedInAsync(new LocalProxyChoice { CodexAccountId = 7, CodexAccountName = "我的 Plus" });

        Assert.Equal(new LocalProxyTarget(7, "我的 Plus"), rig.LocalProxy.Target);
        Assert.Contains(new LocalProxyTarget(7, "我的 Plus"), rig.Codex.LocalProxies);
        Assert.False(rig.LocalProxy.HasError);
    }

    [Fact]
    public async Task ASavedChoiceWhoseAccountIsGoneStaysOnAndSaysSo()
    {
        Rig rig = await SignedInAsync(new LocalProxyChoice { CodexAccountId = 70, CodexAccountName = "旧账号" });

        Assert.Equal(new LocalProxyTarget(70, "旧账号"), rig.LocalProxy.Target);
        Assert.True(rig.LocalProxy.HasError);
    }

    [Fact]
    public async Task SigningOutForgetsTheChoice()
    {
        Rig rig = await SignedInAsync();
        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.Accounts[0]);

        rig.Dashboard.Reset();

        Assert.Null(rig.LocalProxy.Target);
        Assert.Equal(LocalProxyChoice.None, rig.Choice.Saved);
        Assert.Empty(rig.LocalProxy.Accounts);
    }

    [Fact]
    public async Task ClaudeCodeIsNotTouchedByTheLocalProxy()
    {
        Rig rig = await SignedInAsync();
        int before = rig.Codex.PluginRequests.Count;

        await rig.LocalProxy.ToggleAsync(rig.LocalProxy.Accounts[0]);

        Assert.False(rig.Dashboard.ClaudeCode.PluginSupportEnabled);
        Assert.Equal(before, rig.Codex.PluginRequests.Count);
    }
}
