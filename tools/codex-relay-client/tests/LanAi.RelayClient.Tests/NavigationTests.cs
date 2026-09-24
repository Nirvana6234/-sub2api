using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>The left rail, and the page view model's badges and status lines.</summary>
public sealed class NavigationTests
{
    [Fact]
    public void TheRailListsThePagesInOrderAndStartsOnTheOverview()
    {
        var navigation = new NavigationViewModel();

        Assert.Equal(
            [ClientPage.Overview, ClientPage.Codex, ClientPage.Claude, ClientPage.LocalProxy, ClientPage.DesktopSync, ClientPage.Account],
            navigation.Items.Select(i => i.Page));
        Assert.Equal(ClientPage.Overview, navigation.CurrentPage);
        Assert.Equal("仪表盘", navigation.Title);
        Assert.False(navigation.IsSettingsSelected);
    }

    [Fact]
    public void SettingsIsAPageOfItsOwnOutsideTheList()
    {
        var navigation = new NavigationViewModel();

        navigation.Navigate(ClientPage.Settings);

        Assert.Equal(ClientPage.Settings, navigation.CurrentPage);
        Assert.True(navigation.IsSettingsSelected);
        Assert.DoesNotContain(navigation.Selected, navigation.Items);

        navigation.Navigate(ClientPage.Claude);

        Assert.Equal("Claude", navigation.Title);
        Assert.False(navigation.IsSettingsSelected);
    }

    [Fact]
    public async Task ALowBalanceMarksTheAccountPage()
    {
        (DashboardPageViewModel page, DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInPageAsync();
        dashboard.ApplySettings(new PublicSettings
        {
            BalanceLowNotifyEnabled = true,
            BalanceLowNotifyThreshold = 5.0,
            BalanceLowNotifyRechargeUrl = "https://relay.test/recharge",
        });
        relay.OnCurrentUser = () => new RelayUser { Username = "ann", Balance = 3.0 };

        await dashboard.RefreshAsync();

        Assert.True(page.Navigation.Item(ClientPage.Account).HasBadge);
        Assert.False(page.Navigation.Item(ClientPage.Codex).HasBadge);
    }

    [Fact]
    public async Task SigningOutReturnsTheRailToTheOverview()
    {
        (DashboardPageViewModel page, _, _, RelaySessionManager session) = await SignedInPageAsync();
        page.Navigation.Navigate(ClientPage.Account);

        await session.SignOutAsync();
        page.Refresh();

        Assert.Equal(ClientPage.Overview, page.Navigation.CurrentPage);
        Assert.Equal(string.Empty, page.WelcomeText);
    }

    [Fact]
    public async Task TheOverviewStatusLinesFollowTheDashboard()
    {
        (DashboardPageViewModel page, DashboardViewModel dashboard, _, _) = await SignedInPageAsync();
        var changed = new List<string?>();
        page.PropertyChanged += (_, e) => changed.Add(e.PropertyName);

        Assert.Equal("未启动", page.CodexStatusText);
        Assert.Equal("未开启", page.ClaudeStatusText);

        dashboard.IsCodexRunning = true;
        dashboard.ClaudeCode.PluginSupportEnabled = true;

        Assert.Equal("运行中", page.CodexStatusText);
        Assert.NotEqual("未开启", page.ClaudeStatusText);
        Assert.Contains(nameof(DashboardPageViewModel.CodexStatusText), changed);
        Assert.Contains(nameof(DashboardPageViewModel.ClaudeStatusText), changed);
    }

    [Fact]
    public async Task AClaudeCodeSetupThatDidNotTakeMarksTheClaudePage()
    {
        var codex = new FakeCodexStartup
        {
            UsesLocalTransport = true,
            PluginResult = new PluginSupportResult(PluginSupportState.Problem, "写入失败"),
        };
        (DashboardPageViewModel page, DashboardViewModel dashboard, _, _) = await SignedInPageAsync(codex);

        dashboard.ClaudeCode.PluginSupportEnabled = true;
        await dashboard.ClaudeCode.SyncPluginSupportAsync();

        Assert.True(page.Navigation.Item(ClientPage.Claude).HasBadge);

        codex.PluginResult = new PluginSupportResult(PluginSupportState.Off);
        dashboard.ClaudeCode.PluginSupportEnabled = false;
        await dashboard.ClaudeCode.SyncPluginSupportAsync();

        Assert.False(page.Navigation.Item(ClientPage.Claude).HasBadge);
    }

    private static async Task<(DashboardPageViewModel Page, DashboardViewModel Dashboard, FakeRelayClient Relay, RelaySessionManager Session)> SignedInPageAsync(FakeCodexStartup? codex = null)
    {
        var relay = new FakeRelayClient();
        var clock = new TestClock();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", clock.Read);
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex ?? new FakeCodexStartup(),
            pluginSupportPreferences: new FakePluginSupportPreferenceStore());
        var clientUpdate = new ClientUpdateViewModel(_ => Task.FromResult(new ClientCheckResult(ClientCheckStatus.UpToDate)));
        var announcements = new AnnouncementsViewModel(
            new AnnouncementMonitor(relay, session, new FakeAnnouncementNotifyStateStore()),
            relay,
            session);
        var page = new DashboardPageViewModel(dashboard, clientUpdate, announcements, session);

        await session.SignInAsync("a@b.com", "pw");
        page.Refresh();
        return (page, dashboard, relay, session);
    }
}
