using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Which group this installation is on: decided once, at start-up — its own last choice, or the
/// server's default when it has none — and after that changed only by its own switches.
/// </summary>
/// <remarks>
/// Found with two copies of the client on one machine. They share the preference file, and every
/// poll used to read the group back from it (or from the server's record), so one copy dragged the
/// other to its group each minute — and in automatic mode the fixed group the preference still
/// named stayed marked beside 自动分组, two groups showing as in use.
/// </remarks>
public sealed class DashboardGroupSelectionTests
{
    private static RelayGroup Group(long id, string name) =>
        new() { Id = id, Name = name, RateMultiplier = 1, SubscriptionType = "standard", Platform = "openai" };

    private static async Task<(DashboardViewModel Dashboard, FakeCodexStartup Codex, FakeGroupPreferenceStore Preferences, FakeRelayClient Relay)> BuildAsync(
        long? localChoice = null,
        long? serverDefault = null,
        bool automatic = false)
    {
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙"), Group(13, "丙")],
            OnListKeys = () => serverDefault is { } id
                ? [new RelayApiKey { Id = 5, Name = ManagedKeyNaming.MachinePrefix() + "abc", GroupId = id }]
                : [],
            OnPawAutoGroup = () => new PawAutoGroupSettings(true, [11, 12], "price"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var preferences = new FakeGroupPreferenceStore { SavedAutomatic = automatic };
        if (localChoice is { } choice)
        {
            preferences.Save(choice);
        }

        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var dashboard = new DashboardViewModel(
            relay, session, preferences, new ManagedKeyNaming(new FixedInstallId("testinst")), codex);
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();
        return (dashboard, codex, preferences, relay);
    }

    [Fact]
    public async Task StartsOnItsOwnLastChoiceWhenItHasOne_EvenIfTheServerSaysOtherwise()
    {
        var (dashboard, codex, _, _) = await BuildAsync(localChoice: 12, serverDefault: 11);

        Assert.Equal(12, dashboard.SelectedGroup!.Id);
        Assert.Equal(12, codex.ActiveGroups[^1]);
    }

    [Fact]
    public async Task StartsOnTheServersDefaultWhenItHasNoChoice_AndRemembersIt()
    {
        var (dashboard, codex, preferences, _) = await BuildAsync(localChoice: null, serverDefault: 11);

        Assert.Equal(11, dashboard.SelectedGroup!.Id);
        Assert.Equal(11, codex.ActiveGroups[^1]);
        Assert.Equal(11, preferences.Saved);
    }

    [Fact]
    public async Task APollNeverReadsThePreferenceFileAgain()
    {
        var (dashboard, codex, preferences, _) = await BuildAsync(localChoice: 11);

        // Another copy of the client on this machine switches, and writes the shared file.
        preferences.Save(12);
        preferences.SaveAutomatic(true);
        await dashboard.RefreshAsync();
        await dashboard.RefreshAsync();

        Assert.Equal(11, dashboard.SelectedGroup!.Id);
        Assert.False(dashboard.SelectedGroup.IsAutomatic);
        Assert.Equal(11, codex.ActiveGroups[^1]);
        Assert.Single(dashboard.Groups, g => g.IsCurrent);
    }

    [Fact]
    public async Task APollNeverTakesTheServersGroupEither()
    {
        var (dashboard, codex, _, relay) = await BuildAsync(localChoice: 11, serverDefault: 11);

        // Another installation of the account records its own choice on the shared key.
        relay.OnListKeys = () => [new RelayApiKey { Id = 5, Name = ManagedKeyNaming.MachinePrefix() + "abc", GroupId = 12 }];
        await dashboard.RefreshAsync();

        Assert.Equal(11, dashboard.SelectedGroup!.Id);
        Assert.Equal(11, codex.ActiveGroups[^1]);
    }

    [Fact]
    public async Task ThisClientsOwnSwitchIsWhatLaterPollsKeep()
    {
        var (dashboard, codex, preferences, _) = await BuildAsync(localChoice: 11);

        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(g => g.Id == 13));
        preferences.Save(12); // the other copy again
        await dashboard.RefreshAsync();

        Assert.Equal(13, dashboard.SelectedGroup!.Id);
        Assert.Equal(13, codex.ActiveGroups[^1]);
        Assert.Single(dashboard.Groups, g => g.IsCurrent);
    }

    [Fact]
    public async Task AutomaticModeShowsExactlyOneGroupInUse()
    {
        // The preference still names the fixed group from before automatic mode was chosen.
        var (dashboard, _, _, _) = await BuildAsync(localChoice: 11, automatic: true);

        GroupItemViewModel inUse = Assert.Single(dashboard.Groups, g => g.IsCurrent);
        Assert.True(inUse.IsAutomatic);

        await dashboard.RefreshAsync();
        Assert.True(Assert.Single(dashboard.Groups, g => g.IsCurrent).IsAutomatic);
    }

    [Fact]
    public async Task ASwitchOutOfAutomaticModeSurvivesThePollToo()
    {
        var (dashboard, codex, _, _) = await BuildAsync(localChoice: 11, automatic: true);

        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(g => g.Id == 12));
        await dashboard.RefreshAsync();

        Assert.Equal(12, dashboard.SelectedGroup!.Id);
        Assert.Equal(12, codex.ActiveGroups[^1]);
        Assert.Single(dashboard.Groups, g => g.IsCurrent);
    }

    [Fact]
    public async Task AfterSigningOutTheChoiceIsDecidedAgainFromTheStore()
    {
        var (dashboard, _, preferences, _) = await BuildAsync(localChoice: 11);

        dashboard.Reset();
        preferences.Save(12);
        await dashboard.RefreshAsync();

        Assert.Equal(12, dashboard.SelectedGroup!.Id);
    }
}
