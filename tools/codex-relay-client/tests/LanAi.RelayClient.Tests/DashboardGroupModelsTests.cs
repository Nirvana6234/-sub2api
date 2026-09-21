using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>The 模型 tip beside each fixed group: what it shows, and when it asks the server.</summary>
public sealed class DashboardGroupModelsTests
{
    private sealed record Rig(DashboardViewModel Dashboard, FakeRelayClient Relay);

    private static async Task<Rig> BuildAsync(params RelayGroup[] groups)
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var dashboard = new DashboardViewModel(
            relay, session, new FakeGroupPreferenceStore(), new ManagedKeyNaming(new FixedInstallId("testinst")), new FakeCodexStartup());
        await session.SignInAsync("a@b.com", "pw");
        relay.OnAvailableGroups = () => groups;
        relay.OnListKeys = () => [];
        await dashboard.RefreshAsync();
        return new Rig(dashboard, relay);
    }

    private static RelayGroup Group(long id, string name, string platform = "openai") =>
        new() { Id = id, Name = name, RateMultiplier = 1, SubscriptionType = "standard", Platform = platform };

    private static ModelPlazaGroup PlazaGroup(long id, params string[] models) =>
        new(id, models.Select(m => new PlazaModel(m)).ToArray());

    [Fact]
    public async Task ShowsTheModelsTheGroupSupports()
    {
        Rig rig = await BuildAsync(Group(11, "OpenAI 甲"));
        rig.Relay.OnModelPlaza = () => new ModelPlazaResponse([PlazaGroup(11, "gpt-5", "gpt-5-mini")]);
        var messages = new List<string>();
        rig.Dashboard.ShowGroupModels = message => { messages.Add(message); return Task.CompletedTask; };

        await rig.Dashboard.ShowGroupModelsAsync(rig.Dashboard.Groups.Single(g => g.Id == 11));

        Assert.Equal(["OpenAI 甲 支持的模型：\n\ngpt-5\ngpt-5-mini"], messages);
    }

    [Fact]
    public async Task DeduplicatesAndSortsTheModelList()
    {
        Rig rig = await BuildAsync(Group(11, "OpenAI 甲"));
        rig.Relay.OnModelPlaza = () => new ModelPlazaResponse([PlazaGroup(11, "gpt-5", "gpt-4", "gpt-5", "")]);
        var messages = new List<string>();
        rig.Dashboard.ShowGroupModels = message => { messages.Add(message); return Task.CompletedTask; };

        await rig.Dashboard.ShowGroupModelsAsync(rig.Dashboard.Groups.Single(g => g.Id == 11));

        Assert.Equal(["OpenAI 甲 支持的模型：\n\ngpt-4\ngpt-5"], messages);
    }

    /// <summary>自动分组 has no single account pool of its own to list — see the type's own remarks.</summary>
    [Fact]
    public async Task DoesNothingForTheAutomaticEntry()
    {
        Rig rig = await BuildAsync(Group(11, "OpenAI 甲"), Group(12, "OpenAI 乙"));
        var automatic = GroupItemViewModel.CreateAutomatic();
        int calls = 0;
        rig.Dashboard.ShowGroupModels = _ => { calls++; return Task.CompletedTask; };

        await rig.Dashboard.ShowGroupModelsAsync(automatic);

        Assert.Equal(0, calls);
        Assert.Equal(0, rig.Relay.ModelPlazaCallCount);
    }

    [Fact]
    public async Task ANullGroupIsIgnored()
    {
        Rig rig = await BuildAsync(Group(11, "OpenAI 甲"));
        int calls = 0;
        rig.Dashboard.ShowGroupModels = _ => { calls++; return Task.CompletedTask; };

        await rig.Dashboard.ShowGroupModelsAsync(null);

        Assert.Equal(0, calls);
    }

    [Theory]
    [InlineData(RelayFailure.NotFound)]
    [InlineData(RelayFailure.NetworkUnreachable)]
    public async Task AnUnreadablePlazaIsWordedTheSameAsGenuinelyNothingToShow(RelayFailure failure)
    {
        Rig rig = await BuildAsync(Group(11, "OpenAI 甲"));
        rig.Relay.OnModelPlazaThrow = new RelayApiException(failure, "unavailable");
        var messages = new List<string>();
        rig.Dashboard.ShowGroupModels = message => { messages.Add(message); return Task.CompletedTask; };

        await rig.Dashboard.ShowGroupModelsAsync(rig.Dashboard.Groups.Single(g => g.Id == 11));

        Assert.Equal(["OpenAI 甲 暂时没有可显示的模型信息。"], messages);
    }

    /// <summary>
    /// One request answers for every group — GET /model-plaza returns the whole list at once —
    /// so a second button, for a different group, must not ask again.
    /// </summary>
    [Fact]
    public async Task TheSecondGroupsTipReusesTheFirstsFetch()
    {
        Rig rig = await BuildAsync(Group(11, "OpenAI 甲"), Group(12, "OpenAI 乙"));
        rig.Relay.OnModelPlaza = () => new ModelPlazaResponse([PlazaGroup(11, "gpt-5"), PlazaGroup(12, "gpt-5-mini")]);
        var messages = new List<string>();
        rig.Dashboard.ShowGroupModels = message => { messages.Add(message); return Task.CompletedTask; };

        await rig.Dashboard.ShowGroupModelsAsync(rig.Dashboard.Groups.Single(g => g.Id == 11));
        await rig.Dashboard.ShowGroupModelsAsync(rig.Dashboard.Groups.Single(g => g.Id == 12));

        Assert.Equal(1, rig.Relay.ModelPlazaCallCount);
        Assert.Equal(
            ["OpenAI 甲 支持的模型：\n\ngpt-5", "OpenAI 乙 支持的模型：\n\ngpt-5-mini"],
            messages);
    }

    /// <summary>
    /// The cache is account-scoped, not process-scoped: signing out and back in as someone else
    /// must not hand the new account the previous one's model list.
    /// </summary>
    [Fact]
    public async Task SigningOutForgetsTheCachedPlazaResponse()
    {
        Rig rig = await BuildAsync(Group(11, "OpenAI 甲"));
        rig.Relay.OnModelPlaza = () => new ModelPlazaResponse([PlazaGroup(11, "gpt-5")]);
        rig.Dashboard.ShowGroupModels = _ => Task.CompletedTask;
        await rig.Dashboard.ShowGroupModelsAsync(rig.Dashboard.Groups.Single(g => g.Id == 11));
        Assert.Equal(1, rig.Relay.ModelPlazaCallCount);

        rig.Dashboard.Reset();
        await rig.Dashboard.RefreshAsync();
        await rig.Dashboard.ShowGroupModelsAsync(rig.Dashboard.Groups.Single(g => g.Id == 11));

        Assert.Equal(2, rig.Relay.ModelPlazaCallCount);
    }
}
