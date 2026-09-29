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
    private static readonly RelayGroup OtherPlainGroup = Group(4, "OtherPlain", "openai");

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
        relay.OnAvailableGroups = () => [ClaudeGroup, OpenAiGroup, PlainGroup, OtherPlainGroup];
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
    public async Task AModelOnlyTheWhitelistNamesStaysOnTheCodexSide()
    {
        // The Claude page and Claude Code's settings read the shared preference; a model chosen
        // for Codex that they cannot show or hold must not end up there.
        var wide = Group(9, "Wide", "anthropic", "claude-haiku-4", "kimi-k2.5");
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildWithCodexAsync(wide);
        string before = dashboard.ClaudePreference.SelectedClaudeModel;

        dashboard.CodexModelChoice = "kimi-k2.5";

        Assert.Equal(before, dashboard.ClaudePreference.SelectedClaudeModel);
        Assert.Equal("kimi-k2.5", dashboard.CodexModelChoice);
        Assert.Equal("kimi-k2.5", codex.ActiveGroupModels[^1]!.DefaultModel);
    }

    [Fact]
    public async Task ACodexOnlyPickIsDroppedWhenTheNextGroupDoesNotNameIt()
    {
        var wide = Group(9, "Wide", "anthropic", "claude-haiku-4", "kimi-k2.5");
        (DashboardViewModel dashboard, FakeCodexStartup codex) = await BuildWithCodexAsync(wide, ClaudeGroup);
        dashboard.CodexModelChoice = "kimi-k2.5";

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));

        Assert.NotEqual("kimi-k2.5", codex.ActiveGroupModels[^1]!.DefaultModel);
    }

    private static async Task<(DashboardViewModel Dashboard, FakeCodexStartup Codex)> BuildWithCodexAsync(params RelayGroup[] groups)
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var preferences = new FakeGroupPreferenceStore();
        preferences.Save(groups[0].Id);
        var dashboard = new DashboardViewModel(
            relay, session, preferences, new ManagedKeyNaming(new FixedInstallId("testinst")), codex);
        await session.SignInAsync("a@b.com", "pw");
        relay.OnAvailableGroups = () => groups;
        relay.OnListKeys = () => [];
        await dashboard.RefreshAsync();
        return (dashboard, codex);
    }

    [Fact]
    public async Task ThePageShowsTheFirstChoiceWhenThePreferenceIsNotOneOfThem()
    {
        var onlyHaiku = Group(9, "Haiku", "anthropic", "claude-haiku-4");
        DashboardViewModel dashboard = await BuildWith(onlyHaiku);

        Assert.Equal(["claude-haiku-4"], dashboard.CodexModelChoices);
        Assert.Equal("claude-haiku-4", dashboard.CodexModelChoice);
    }

    // ---- Telling the user what a switch does to Codex's picker -------------------------

    private static async Task<DashboardViewModel> WithCodexRunningOnAsync(long groupId, bool running = true)
    {
        (DashboardViewModel dashboard, _) = await BuildAsync(startOn: groupId);
        await dashboard.StartCodexAsync(_ => Task.FromResult(false));
        dashboard.IsCodexRunning = running;
        return dashboard;
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
    public async Task SwitchingToAGroupWithADifferentListSaysWhenTheRunningCodexUpdates()
    {
        // No restart is needed: the relay and the client see to it that Codex fetches the new
        // list. The message only says when the picker changes.
        DashboardViewModel dashboard = await WithCodexRunningOnAsync(2);

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));

        Assert.Contains("已切换到 Claude", dashboard.GroupMessage, StringComparison.Ordinal);
        Assert.Contains("模型下拉", dashboard.GroupMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task SaysNothingAboutThePickerWhenCodexIsNotRunning()
    {
        DashboardViewModel dashboard = await WithCodexRunningOnAsync(2, running: false);

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));

        Assert.DoesNotContain("模型下拉", dashboard.GroupMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task SaysNothingAboutThePickerWhenBothGroupsShowCodexsOwnList()
    {
        DashboardViewModel dashboard = await WithCodexRunningOnAsync(3);

        await dashboard.SwitchGroupAsync(Item(dashboard, 4));

        Assert.DoesNotContain("模型下拉", dashboard.GroupMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task SwitchingBackStillTellsTheRunningCodexItHasToCatchUp()
    {
        DashboardViewModel dashboard = await WithCodexRunningOnAsync(2);

        await dashboard.SwitchGroupAsync(Item(dashboard, 1));
        await dashboard.SwitchGroupAsync(Item(dashboard, 2));

        // The list changed back, so it changed again: the running Codex still has to catch up.
        Assert.Contains("模型下拉", dashboard.GroupMessage, StringComparison.Ordinal);
    }
}
