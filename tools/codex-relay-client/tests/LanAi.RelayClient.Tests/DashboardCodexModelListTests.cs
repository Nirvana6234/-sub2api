using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The Codex page's model picker: which models the relay is told about, what a running Codex
/// is asked about after a switch, and what the start passes on.
/// </summary>
public sealed class DashboardCodexModelListTests
{
    private static RelayGroup Group(long id, string name, string platform, params string[] models) =>
        new()
        {
            Id = id,
            Name = name,
            RateMultiplier = 1,
            SubscriptionType = "standard",
            Platform = platform,
            ModelAllowlist = new GroupModelAllowlist(models.Length > 0, models),
        };

    private static readonly RelayGroup ClaudeGroup = Group(1, "Claude", "anthropic", "claude-sonnet-5", "claude-opus-5", "claude-haiku-4");
    private static readonly RelayGroup OpenAiGroup = Group(2, "OpenAI", "openai", "gpt-5.5", "gpt-5.4");
    private static readonly RelayGroup PlainGroup = Group(3, "Plain", "openai");

    private static async Task<(DashboardViewModel Dashboard, FakeCodexStartup Codex)> BuildAsync(long? startOn = null)
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var preferences = new FakeGroupPreferenceStore();
        if (startOn is { } id)
        {
            preferences.Save(id);
        }

        var dashboard = new DashboardViewModel(
            relay, session, preferences, new ManagedKeyNaming(new FixedInstallId("testinst")), codex);
        await session.SignInAsync("a@b.com", "pw");
        relay.OnAvailableGroups = () => [ClaudeGroup, OpenAiGroup, PlainGroup];
        relay.OnListKeys = () => [];
        await dashboard.RefreshAsync();
        return (dashboard, codex);
    }

    private static GroupItemViewModel Item(DashboardViewModel dashboard, long id) => dashboard.Groups.Single(g => g.Id == id);

    // ---- What the relay is told -----------------------------------------------

    [Fact]
    public async Task SwitchingToAWhitelistedGroupHandsTheRelayItsModels()
    {
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildAsync(startOn: 2);

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));

        CodexGroupModels? pushed = codex.ActiveGroupModels[^1];
        Assert.Equal(1, codex.ActiveGroups[^1]);
        Assert.Equal(["claude-haiku-4", "claude-opus-5", "claude-sonnet-5"], pushed!.Models.OrderBy(m => m, StringComparer.Ordinal));
    }

    [Fact]
    public async Task ADefaultTheUserChoseIsFirstWhenTheGroupServesIt()
    {
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildAsync(startOn: 2);
        dashboard.ClaudePreference.SelectedClaudeModel = "claude-opus-5";

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));

        Assert.Equal("claude-opus-5", codex.ActiveGroupModels[^1]!.DefaultModel);
    }

    [Fact]
    public async Task ChangingTheDefaultModelReachesTheRelayWithoutASwitch()
    {
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildAsync(startOn: 1);
        int before = codex.ActiveGroupModels.Count;

        dashboard.ClaudePreference.SelectedClaudeModel = "claude-opus-5";

        Assert.True(codex.ActiveGroupModels.Count > before);
        Assert.Equal("claude-opus-5", codex.ActiveGroupModels[^1]!.DefaultModel);
    }

    [Fact]
    public async Task AGroupWithoutAWhitelistHandsTheRelayNoModels()
    {
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildAsync(startOn: 1);

        await dashboard.SwitchGroupAsync(Item(dashboard, 3));

        Assert.Equal(3, codex.ActiveGroups[^1]);
        Assert.Null(codex.ActiveGroupModels[^1]);
    }

    // ---- What starting Codex passes on ------------------------------------------

    [Fact]
    public async Task AClaudeGroupStartReplacesTheModelWithTheDefault()
    {
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildAsync(startOn: 1);

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.Equal("claude-sonnet-5", codex.LastPreferredModel);
        Assert.False(codex.LastKeepUserModel);
        Assert.Equal(3, codex.LastGroupModels!.Models.Count);
    }

    [Fact]
    public async Task AWhitelistedNonClaudeGroupKeepsTheUsersModelWhenItIsServed()
    {
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildAsync(startOn: 2);

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.Equal("gpt-5.4", codex.LastPreferredModel); // the group's default, used only if the user's is not served
        Assert.True(codex.LastKeepUserModel);
    }

    [Fact]
    public async Task AGroupWithoutAWhitelistLeavesTheModelAlone()
    {
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildAsync(startOn: 3);

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.Null(codex.LastPreferredModel);
        Assert.Null(codex.LastGroupModels);
        Assert.False(codex.LastKeepUserModel);
    }

    // ---- The Codex page's dropdown ------------------------------------------------

    [Fact]
    public async Task TheDropdownOffersTheWhitelistForAClaudeGroupAndTheTwoKnownModelsWithout()
    {
        (DashboardViewModel dashboard, _) = await BuildAsync(startOn: 1);

        // The whitelist names three; the ones the preference can hold come first-class.
        Assert.Equal(["claude-sonnet-5", "claude-opus-5"], dashboard.CodexModelChoices.OrderByDescending(m => m));

        var noList = Group(9, "NoList", "anthropic");
        Assert.Equal(ClaudePreferenceViewModel.ClaudeModels, (await BuildWith(noList)).CodexModelChoices);
    }

    private static async Task<DashboardViewModel> BuildWith(RelayGroup group)
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var preferences = new FakeGroupPreferenceStore();
        preferences.Save(group.Id);
        var dashboard = new DashboardViewModel(
            relay, session, preferences, new ManagedKeyNaming(new FixedInstallId("testinst")), new FakeCodexStartup { UsesLocalTransport = true });
        await session.SignInAsync("a@b.com", "pw");
        relay.OnAvailableGroups = () => [group];
        relay.OnListKeys = () => [];
        await dashboard.RefreshAsync();
        return dashboard;
    }

    [Fact]
    public async Task ChoosingInTheDropdownSetsThePreferenceAndANullFromTheControlIsIgnored()
    {
        (DashboardViewModel dashboard, _) = await BuildAsync(startOn: 1);

        dashboard.CodexModelChoice = "claude-opus-5";
        Assert.Equal("claude-opus-5", dashboard.ClaudePreference.SelectedClaudeModel);

        dashboard.CodexModelChoice = null!;
        dashboard.CodexModelChoice = "not-in-the-list";
        Assert.Equal("claude-opus-5", dashboard.ClaudePreference.SelectedClaudeModel);
    }

    [Fact]
    public async Task ThePageShowsTheFirstChoiceWhenThePreferenceIsNotOneOfThem()
    {
        var onlyHaiku = Group(9, "Haiku", "anthropic", "claude-haiku-4");
        DashboardViewModel dashboard = await BuildWith(onlyHaiku);

        Assert.Equal(["claude-haiku-4"], dashboard.CodexModelChoices);
        Assert.Equal("claude-haiku-4", dashboard.CodexModelChoice);
    }

    // ---- Restarting to see the new list ---------------------------------------------

    private static async Task<(DashboardViewModel Dashboard, FakeCodexStartup Codex, List<string> Asked)> RunningOnAsync(long groupId, bool answer)
    {
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildAsync(startOn: groupId);
        await dashboard.StartCodexAsync(_ => Task.FromResult(false));
        dashboard.IsCodexRunning = true;
        var asked = new List<string>();
        dashboard.ConfirmModelListRestart = message => { asked.Add(message); return Task.FromResult(answer); };
        return (dashboard, codex, asked);
    }

    [Fact]
    public async Task SwitchingToAGroupWithADifferentListWhileCodexRunsAsksWhetherToRestart()
    {
        (DashboardViewModel dashboard, FakeCodexStartup codex, List<string> asked) = await RunningOnAsync(2, answer: false);

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));

        string question = Assert.Single(asked);
        Assert.Contains("Claude", question, StringComparison.Ordinal);
        Assert.Contains("稍后", question, StringComparison.Ordinal);
        Assert.Contains(codex.ActiveGroupModels[^1]!.DefaultModel, question, StringComparison.Ordinal);
    }

    [Fact]
    public async Task DecliningLeavesANoticeOnThePageUntilCodexIsRestarted()
    {
        (DashboardViewModel dashboard, _, _) = await RunningOnAsync(2, answer: false);

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));

        Assert.True(dashboard.HasCodexModelListNotice);

        await dashboard.StartCodexAsync(_ => Task.FromResult(true), forceRestart: true);
        Assert.False(dashboard.HasCodexModelListNotice);
    }

    [Fact]
    public async Task AgreeingRestartsCodexWithTheNewGroupsModels()
    {
        (DashboardViewModel dashboard, FakeCodexStartup codex, _) = await RunningOnAsync(2, answer: true);
        int runsBefore = codex.RunCount;

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));

        Assert.Equal(runsBefore + 1, codex.RunCount);
        Assert.True(codex.LastAllowRestart);
        Assert.Equal(3, codex.LastGroupModels!.Models.Count);
        Assert.False(dashboard.HasCodexModelListNotice);
    }

    [Fact]
    public async Task SwitchingBetweenGroupsWithoutWhitelistsNeverAsks()
    {
        (DashboardViewModel dashboard, _, List<string> asked) = await RunningOnAsync(3, answer: false);
        var another = Group(4, "Another", "openai");

        // Both have no list: Codex's own list is right for either.
        await dashboard.SwitchGroupAsync(Item(dashboard, 3));
        Assert.Empty(asked);
        Assert.False(dashboard.HasCodexModelListNotice);
        _ = another;
    }

    [Fact]
    public async Task DoesNotAskWhenCodexIsNotRunning()
    {
        (DashboardViewModel dashboard, _) = await BuildAsync(startOn: 2);
        var asked = new List<string>();
        dashboard.ConfirmModelListRestart = message => { asked.Add(message); return Task.FromResult(true); };

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));

        Assert.Empty(asked);
    }

    [Fact]
    public async Task SwitchingBackToTheGroupCodexWasStartedWithNeedsNoRestart()
    {
        (DashboardViewModel dashboard, _, List<string> asked) = await RunningOnAsync(2, answer: false);

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));
        asked.Clear();
        await dashboard.SwitchGroupAsync(Item(dashboard, 2));

        Assert.Empty(asked);
        Assert.False(dashboard.HasCodexModelListNotice);
    }

    [Fact]
    public async Task ASwitchToAGroupWithoutAWhitelistSaysTheListGoesBackToCodexsOwn()
    {
        (DashboardViewModel dashboard, _, List<string> asked) = await RunningOnAsync(1, answer: false);

        await dashboard.SwitchGroupAsync(Item(dashboard, 3));

        Assert.Contains("自带", Assert.Single(asked), StringComparison.Ordinal);
    }

    [Fact]
    public async Task SwitchingToANonClaudeWhitelistedGroupHandsTheRelayItsModelsToo()
    {
        // Claude groups get a second push when their preference loads; this one has only the switch.
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildAsync(startOn: 1);

        await dashboard.SwitchGroupAsync(Item(dashboard, 2));

        Assert.Equal(2, codex.ActiveGroups[^1]);
        Assert.Equal(["gpt-5.4", "gpt-5.5"], codex.ActiveGroupModels[^1]!.Models.OrderBy(m => m, StringComparer.Ordinal));
    }

    [Fact]
    public async Task DoesNotAskOnceCodexHasBeenClosed()
    {
        (DashboardViewModel dashboard, _, List<string> asked) = await RunningOnAsync(2, answer: true);
        dashboard.IsCodexRunning = false;

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));

        Assert.Empty(asked);
        Assert.False(dashboard.HasCodexModelListNotice);
    }
}
