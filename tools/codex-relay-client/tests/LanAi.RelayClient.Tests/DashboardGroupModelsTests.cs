using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>The 模型 tip beside each fixed group: shown only for a whitelisted group, listing the whitelist.</summary>
public sealed class DashboardGroupModelsTests
{
    private static async Task<DashboardViewModel> BuildAsync(params RelayGroup[] groups)
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var dashboard = new DashboardViewModel(
            relay, session, new FakeGroupPreferenceStore(), new ManagedKeyNaming(new FixedInstallId("testinst")), new FakeCodexStartup());
        await session.SignInAsync("a@b.com", "pw");
        relay.OnAvailableGroups = () => groups;
        relay.OnListKeys = () => [];
        await dashboard.RefreshAsync();
        return dashboard;
    }

    private static RelayGroup Group(long id, string name, bool allowlistEnabled, params string[] models) =>
        new()
        {
            Id = id,
            Name = name,
            RateMultiplier = 1,
            SubscriptionType = "standard",
            Platform = "openai",
            ModelAllowlist = new GroupModelAllowlist(allowlistEnabled, models),
        };

    [Fact]
    public async Task ShowsTheWhitelistedModels()
    {
        DashboardViewModel dashboard = await BuildAsync(Group(11, "OpenAI 甲", true, "gpt-5", "gpt-5-mini"));
        var messages = new List<string>();
        dashboard.ShowGroupModels = message => { messages.Add(message); return Task.CompletedTask; };

        await dashboard.ShowGroupModelsAsync(dashboard.Groups.Single(g => g.Id == 11));

        Assert.Equal(["OpenAI 甲 支持的模型：\n\ngpt-5\ngpt-5-mini"], messages);
    }

    [Fact]
    public async Task DeduplicatesAndSortsTheModelList()
    {
        DashboardViewModel dashboard = await BuildAsync(Group(11, "OpenAI 甲", true, "gpt-5", "gpt-4", "gpt-5", ""));
        var messages = new List<string>();
        dashboard.ShowGroupModels = message => { messages.Add(message); return Task.CompletedTask; };

        await dashboard.ShowGroupModelsAsync(dashboard.Groups.Single(g => g.Id == 11));

        Assert.Equal(["OpenAI 甲 支持的模型：\n\ngpt-4\ngpt-5"], messages);
    }

    /// <summary>Wildcard entries are shown as written — the client has no source list to expand them against.</summary>
    [Fact]
    public async Task WildcardEntriesAreShownAsWritten()
    {
        DashboardViewModel dashboard = await BuildAsync(Group(11, "OpenAI 甲", true, "gpt-5*"));
        var messages = new List<string>();
        dashboard.ShowGroupModels = message => { messages.Add(message); return Task.CompletedTask; };

        await dashboard.ShowGroupModelsAsync(dashboard.Groups.Single(g => g.Id == 11));

        Assert.Equal(["OpenAI 甲 支持的模型：\n\ngpt-5*"], messages);
    }

    [Fact]
    public async Task TheButtonIsOfferedOnlyWhenTheWhitelistIsOn()
    {
        DashboardViewModel dashboard = await BuildAsync(
            Group(11, "开", true, "gpt-5"),
            Group(12, "关", false, "gpt-5"),
            Group(13, "无", false));

        Assert.True(dashboard.Groups.Single(g => g.Id == 11).HasModelAllowlist);
        Assert.False(dashboard.Groups.Single(g => g.Id == 12).HasModelAllowlist);
        Assert.False(dashboard.Groups.Single(g => g.Id == 13).HasModelAllowlist);
        Assert.False(GroupItemViewModel.CreateAutomatic().HasModelAllowlist);
    }

    [Fact]
    public async Task DoesNothingWhenTheWhitelistIsOff()
    {
        DashboardViewModel dashboard = await BuildAsync(Group(12, "关", false, "gpt-5"));
        int calls = 0;
        dashboard.ShowGroupModels = _ => { calls++; return Task.CompletedTask; };

        await dashboard.ShowGroupModelsAsync(dashboard.Groups.Single(g => g.Id == 12));
        await dashboard.ShowGroupModelsAsync(GroupItemViewModel.CreateAutomatic());
        await dashboard.ShowGroupModelsAsync(null);

        Assert.Equal(0, calls);
    }

    // ---- Entries that are not model ids ----------------------------------------------

    [Theory]
    [InlineData("gpt-5", true)]
    [InlineData("claude-sonnet-5", true)]
    [InlineData("gpt-5*", true)]
    [InlineData("gpt-5 折扣", false)]
    [InlineData("测试模型", false)]
    [InlineData("gpt-5（限时）", false)]
    [InlineData("gpt-5，gpt-4", false)]
    [InlineData("ｇｐｔ", false)]
    [InlineData("", false)]
    [InlineData("  ", false)]
    public void OnlyEntriesWithoutChineseCharactersCanBeModels(string entry, bool expected) =>
        Assert.Equal(expected, GroupItemViewModel.IsSelectableModelId(entry));

    [Fact]
    public async Task ChineseEntriesAreLeftOutOfTheTip()
    {
        DashboardViewModel dashboard = await BuildAsync(Group(11, "OpenAI 甲", true, "gpt-5", "测试模型", "gpt-4", "限时 gpt-3"));
        var messages = new List<string>();
        dashboard.ShowGroupModels = message => { messages.Add(message); return Task.CompletedTask; };

        await dashboard.ShowGroupModelsAsync(dashboard.Groups.Single(g => g.Id == 11));

        Assert.Equal(["OpenAI 甲 支持的模型：\n\ngpt-4\ngpt-5"], messages);
    }

    [Fact]
    public async Task AWhitelistOfNothingButChineseEntriesIsNoWhitelistAtAll()
    {
        DashboardViewModel dashboard = await BuildAsync(Group(11, "OpenAI 甲", true, "测试", "另一个"));

        GroupItemViewModel group = dashboard.Groups.Single(g => g.Id == 11);

        Assert.Empty(group.AllowedModels);
        Assert.False(group.HasModelAllowlist);
    }
}
