using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>The 支持插件 checkbox: when it asks the startup to set the editors up, and with what.</summary>
public sealed class DashboardPluginSupportTests
{
    private sealed record Rig(
        DashboardViewModel Dashboard,
        FakeRelayClient Relay,
        FakeCodexStartup Codex,
        FakePluginSupportPreferenceStore Preferences);

    private static async Task<Rig> BuildAsync(
        bool localTransport = true,
        bool? saved = null,
        params RelayGroup[] groups)
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = localTransport };
        var preferences = new FakePluginSupportPreferenceStore(saved);
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex,
            pluginSupportPreferences: preferences);
        await session.SignInAsync("a@b.com", "pw");
        relay.OnAvailableGroups = () => groups;
        relay.OnListKeys = () => [];
        relay.ClaudePreference = new ClaudePreferenceDto { Model = "claude-opus-5", ThinkingLevel = "medium" };
        return new Rig(dashboard, relay, codex, preferences);
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
    public async Task TheBoxIsOnByDefaultAndTheSavedChoiceWins()
    {
        Assert.True((await BuildAsync()).Dashboard.PluginSupportEnabled);
        Assert.False((await BuildAsync(saved: false)).Dashboard.PluginSupportEnabled);
    }

    [Fact]
    public async Task RestoringTheSavedChoiceIsNotTheUserChangingIt()
    {
        Rig rig = await BuildAsync(saved: false);

        Assert.Equal(0, rig.Preferences.SaveCount);
        Assert.Empty(rig.Codex.PluginRequests);
    }

    [Fact]
    public async Task AClaudeGroupIsSyncedWithTheModelTheServerHolds()
    {
        Rig rig = await BuildAsync(true, null, Group(21, "Claude", "anthropic"));

        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Count > 0);

        PluginSupportRequest request = Assert.Single(rig.Codex.PluginRequests);
        Assert.True(request.Enabled);
        Assert.True(request.GroupIsClaude);
        Assert.Equal(21, request.GroupId);
        Assert.Equal("claude-opus-5", request.Model);
    }

    [Fact]
    public async Task AnOpenAiGroupIsSyncedAsNotClaude()
    {
        Rig rig = await BuildAsync(true, null, Group(11, "OpenAI", "openai"));

        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Count > 0);

        PluginSupportRequest request = Assert.Single(rig.Codex.PluginRequests);
        Assert.False(request.GroupIsClaude);
        Assert.Null(request.Model);
    }

    [Fact]
    public async Task NothingIsSyncedWhenThereIsNoLocalRelay()
    {
        Rig rig = await BuildAsync(false, null, Group(21, "Claude", "anthropic"));

        await rig.Dashboard.RefreshAsync();
        rig.Dashboard.PluginSupportEnabled = false;
        await rig.Dashboard.SyncPluginSupportAsync();

        Assert.Empty(rig.Codex.PluginRequests);
        Assert.False(rig.Dashboard.CanConfigurePluginSupport);
    }

    [Fact]
    public async Task TickingTheBoxSavesItAndAppliesIt()
    {
        Rig rig = await BuildAsync(true, null, Group(21, "Claude", "anthropic"));
        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Count == 1);

        rig.Dashboard.PluginSupportEnabled = false;
        await WaitForAsync(() => rig.Codex.PluginRequests.Count == 2);

        Assert.Equal(false, rig.Preferences.Saved);
        Assert.False(rig.Codex.PluginRequests[1].Enabled);
    }

    [Fact]
    public async Task AnUnchangedRequestDoesNoSecondRound()
    {
        Rig rig = await BuildAsync(true, null, Group(21, "Claude", "anthropic"));
        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Count == 1);

        await rig.Dashboard.SyncPluginSupportAsync();
        await rig.Dashboard.SyncPluginSupportAsync();

        Assert.Single(rig.Codex.PluginRequests);
    }

    [Fact]
    public async Task AProblemIsRetriedRatherThanRemembered()
    {
        Rig rig = await BuildAsync(true, null, Group(21, "Claude", "anthropic"));
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
        Rig rig = await BuildAsync(true, null, Group(21, "Claude", "anthropic"));
        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Count == 1);

        rig.Dashboard.SelectedClaudeModel = "claude-sonnet-5";
        await WaitForAsync(() => rig.Codex.PluginRequests.Count == 2);

        Assert.Equal("claude-sonnet-5", rig.Codex.PluginRequests[1].Model);
    }

    [Fact]
    public async Task SwitchingToAClaudeGroupWaitsForTheServersModelAndNeverWritesTheDefault()
    {
        Rig rig = await BuildAsync(true, null, Group(11, "OpenAI", "openai"), Group(21, "Claude", "anthropic"));
        await rig.Dashboard.RefreshAsync();
        await WaitForAsync(() => rig.Codex.PluginRequests.Count == 1);

        await rig.Dashboard.SwitchGroupAsync(rig.Dashboard.Groups.Single(g => g.Id == 21));
        await WaitForAsync(() => rig.Codex.PluginRequests.Any(r => r.GroupIsClaude));

        Assert.All(rig.Codex.PluginRequests.Where(r => r.GroupIsClaude), r => Assert.Equal("claude-opus-5", r.Model));
    }

    [Theory]
    [InlineData((int)PluginSupportState.Active, true, "Claude Code 已接入")]
    [InlineData((int)PluginSupportState.WrongGroup, true, "不是 Claude 分组")]
    [InlineData((int)PluginSupportState.WrongGroup, false, "")]
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
