using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The 支持插件 checkbox and the Claude group picked for it: a selection of its own, independent
/// of the group Codex is on.
/// </summary>
public sealed class DashboardPluginSupportTests
{
    private sealed record Rig(
        DashboardViewModel Dashboard,
        FakeRelayClient Relay,
        FakeCodexStartup Codex,
        FakePluginSupportPreferenceStore Preferences,
        FakeGroupPreferenceStore Groups);

    private static async Task<Rig> BuildAsync(
        bool localTransport = true,
        bool? saved = null,
        long? savedClaudeGroup = null,
        params RelayGroup[] groups)
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = localTransport };
        var preferences = new FakePluginSupportPreferenceStore(saved);
        var groupPreferences = new FakeGroupPreferenceStore();
        if (savedClaudeGroup is { } id)
        {
            groupPreferences.SaveClaudeGroup(id);
        }

        var dashboard = new DashboardViewModel(
            relay,
            session,
            groupPreferences,
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex,
            pluginSupportPreferences: preferences);
        await session.SignInAsync("a@b.com", "pw");
        relay.OnAvailableGroups = () => groups;
        relay.OnListKeys = () => [];
        relay.ClaudePreference = new ClaudePreferenceDto { Model = "claude-opus-5", ThinkingLevel = "medium" };
        return new Rig(dashboard, relay, codex, preferences, groupPreferences);
    }

    private static RelayGroup Group(long id, string name, string platform) =>
        new() { Id = id, Name = name, RateMultiplier = 1, SubscriptionType = "standard", Platform = platform };

    private static async Task WaitForAsync(Func<bool> condition)
    {
        DateTime deadline = DateTime.UtcNow.AddSeconds(5);
        while (!condition())
        {
            Assert.True(DateTime.UtcNow < deadline, "timed out waiting for the plug-in sync");
            await Task.Delay(10);
        }
    }

    [Fact]
    public async Task TheBoxIsOffByDefaultAndTheSavedChoiceWins()
    {
        Assert.False((await BuildAsync()).Dashboard.PluginSupportEnabled);
        Assert.True((await BuildAsync(saved: true)).Dashboard.PluginSupportEnabled);
    }

    [Fact]
    public async Task RestoringTheSavedChoiceIsNotTheUserChangingIt()
    {
        Rig rig = await BuildAsync(saved: true);

        Assert.Equal(0, rig.Preferences.SaveCount);
        Assert.Empty(rig.Codex.PluginRequests);
    }

    [Fact]
    public async Task OnlyClaudeGroupsAreOfferedAndAnOnlyOneIsPreselected()
    {
        Rig rig = await BuildAsync(true, null, null, Group(11, "OpenAI", "openai"), Group(21, "Claude", "anthropic"));

        await rig.Dashboard.RefreshAsync();

        GroupItemViewModel offered = Assert.Single(rig.Dashboard.ClaudePluginGroups);
        Assert.Equal(21, offered.Id);
        Assert.Same(offered, rig.Dashboard.SelectedClaudePluginGroup);
        Assert.True(rig.Dashboard.HasClaudePluginGroups);
    }

    [Fact]
    public async Task ARememberedClaudeGroupBeatsTheDefault()
    {
        Rig rig = await BuildAsync(true, true, 22, Group(21, "Claude 甲", "anthropic"), Group(22, "Claude 乙", "anthropic"));

        await rig.Dashboard.RefreshAsync();

        Assert.Equal(22, rig.Dashboard.SelectedClaudePluginGroup?.Id);
    }

    [Fact]
    public async Task WithSeveralClaudeGroupsAndNoChoiceNothingIsPreselected()
    {
        Rig rig = await BuildAsync(true, null, null, Group(21, "Claude 甲", "anthropic"), Group(22, "Claude 乙", "anthropic"));

        await rig.Dashboard.RefreshAsync();

        Assert.Null(rig.Dashboard.SelectedClaudePluginGroup);
    }

    /// <summary>
    /// The point of the split: Codex on an OpenAI group and the plug-ins on a Claude group at the
    /// same time, each with its own group.
    /// </summary>
    [Fact]
    public async Task ThePluginsUseTheirOwnClaudeGroupWhileCodexStaysOnItsOwn()
    {
        Rig rig = await BuildAsync(true, true, null, Group(11, "OpenAI", "openai"), Group(21, "Claude", "anthropic"));

        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Any(r => r.Enabled && r.GroupId == 21));

        PluginSupportRequest request = rig.Codex.PluginRequests.First(r => r.GroupId == 21);
        Assert.Equal("claude-opus-5", request.Model);
        Assert.Equal(11, rig.Dashboard.SelectedGroup?.Id);
        Assert.DoesNotContain(21L, rig.Codex.ActiveGroups.Select(g => g ?? 0));
    }

    [Fact]
    public async Task PickingAnotherClaudeGroupSavesItAndAppliesIt()
    {
        Rig rig = await BuildAsync(true, true, null, Group(21, "Claude 甲", "anthropic"), Group(22, "Claude 乙", "anthropic"));
        await rig.Dashboard.RefreshAsync();

        rig.Dashboard.SelectedClaudePluginGroup = rig.Dashboard.ClaudePluginGroups.Single(g => g.Id == 22);
        await WaitForAsync(() => rig.Codex.PluginRequests.Any(r => r.GroupId == 22));

        Assert.Equal(22, rig.Groups.SavedClaudeGroup);
    }

    [Fact]
    public async Task NothingIsSyncedWhenThereIsNoLocalRelay()
    {
        Rig rig = await BuildAsync(false, null, null, Group(21, "Claude", "anthropic"));

        await rig.Dashboard.RefreshAsync();
        rig.Dashboard.PluginSupportEnabled = true;
        await rig.Dashboard.SyncPluginSupportAsync();

        Assert.Empty(rig.Codex.PluginRequests);
        Assert.False(rig.Dashboard.CanConfigurePluginSupport);
    }

    [Fact]
    public async Task TickingTheBoxSavesItAndAppliesIt()
    {
        Rig rig = await BuildAsync(true, null, null, Group(21, "Claude", "anthropic"));
        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Count >= 1);

        rig.Dashboard.PluginSupportEnabled = true;
        await WaitForAsync(() => rig.Codex.PluginRequests.Any(r => r.Enabled));

        Assert.Equal(true, rig.Preferences.Saved);
    }

    [Fact]
    public async Task AnUnchangedRequestDoesNoSecondRound()
    {
        Rig rig = await BuildAsync(true, true, null, Group(21, "Claude", "anthropic"));
        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Count >= 1);
        await Task.Delay(100);
        int before = rig.Codex.PluginRequests.Count;

        await rig.Dashboard.SyncPluginSupportAsync();
        await rig.Dashboard.SyncPluginSupportAsync();

        Assert.Equal(before, rig.Codex.PluginRequests.Count);
    }

    [Fact]
    public async Task AProblemIsRetriedRatherThanRemembered()
    {
        Rig rig = await BuildAsync(true, true, null, Group(21, "Claude", "anthropic"));
        rig.Codex.PluginResult = new PluginSupportResult(PluginSupportState.Problem, "写入失败");
        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Count >= 1);
        await Task.Delay(100);
        int before = rig.Codex.PluginRequests.Count;

        await rig.Dashboard.SyncPluginSupportAsync();

        Assert.Equal(before + 1, rig.Codex.PluginRequests.Count);
        Assert.Equal("写入失败", rig.Dashboard.PluginSupportStatus);
        Assert.True(rig.Dashboard.HasPluginSupportStatus);
    }

    [Fact]
    public async Task ChangingTheClaudeModelIsPassedOn()
    {
        Rig rig = await BuildAsync(true, true, null, Group(21, "Claude", "anthropic"));
        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Any(r => r.Model == "claude-opus-5"));

        rig.Dashboard.SelectedClaudeModel = "claude-sonnet-5";
        await WaitForAsync(() => rig.Codex.PluginRequests.Any(r => r.Model == "claude-sonnet-5"));
    }

    /// <summary>
    /// The Claude 模型设置 picker follows only Codex's own group. Picking a Claude group for the
    /// plug-ins, or ticking 支持插件, must not turn it on — it stays where F5.4 put it.
    /// </summary>
    [Fact]
    public async Task TheClaudePreferencePickerIgnoresThePluginPath()
    {
        Rig rig = await BuildAsync(true, false, null, Group(11, "OpenAI", "openai"), Group(21, "Claude", "anthropic"));
        await rig.Dashboard.RefreshAsync();

        Assert.False(rig.Dashboard.IsClaudeGroup);

        rig.Dashboard.PluginSupportEnabled = true;

        Assert.False(rig.Dashboard.IsClaudeGroup);
    }

    [Fact]
    public async Task SigningOutForgetsTheClaudeGroupList()
    {
        Rig rig = await BuildAsync(true, null, null, Group(21, "Claude", "anthropic"));
        await rig.Dashboard.RefreshAsync();

        rig.Dashboard.Reset();

        Assert.Empty(rig.Dashboard.ClaudePluginGroups);
        Assert.Null(rig.Dashboard.SelectedClaudePluginGroup);
        Assert.False(rig.Dashboard.HasClaudePluginGroups);
    }

    [Theory]
    [InlineData((int)PluginSupportState.Active, true, "Claude Code 已接入")]
    [InlineData((int)PluginSupportState.NoGroupChosen, true, "请选择一个 Claude 分组")]
    [InlineData((int)PluginSupportState.NoGroupChosen, false, "没有可用的 Claude 分组")]
    [InlineData((int)PluginSupportState.Off, true, "")]
    [InlineData((int)PluginSupportState.NotApplicable, true, "")]
    public void TheStatusLineSaysOnlyWhatIsUseful(int state, bool hasClaudeGroup, string expected)
    {
        string text = DashboardViewModel.DescribePluginSupport(new PluginSupportResult((PluginSupportState)state), hasClaudeGroup);

        if (expected.Length == 0)
        {
            Assert.Empty(text);
        }
        else
        {
            Assert.Contains(expected, text);
        }
    }
}
