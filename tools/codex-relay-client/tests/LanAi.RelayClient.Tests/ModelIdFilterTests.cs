using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class ModelIdFilterTests
{
    private static readonly ModelIdFilter Default = new();

    // The names below are the ones present in production groups' whitelists and accounts' model
    // mappings (read-only query, 2026-09-29).

    [Theory]
    [InlineData("gpt-6")]
    [InlineData("gpt-6-astra")]
    [InlineData("gpt-5.6")]
    [InlineData("gpt-5.6-sol")]
    [InlineData("gpt-5.6-terra")]
    [InlineData("gpt-5.5")]
    [InlineData("gpt-5.4-mini")]
    [InlineData("gpt-5.3-codex")]
    [InlineData("gpt-5.3-codex-spark")]
    [InlineData("gpt-5.2-pro")]
    [InlineData("gpt-5.2-chat-latest")]
    [InlineData("claude-opus-5")]
    [InlineData("claude-sonnet-4-5-20250929")]
    [InlineData("claude-opus-4-8-thinking")]
    public void KeepsTheModelsCodexCanUse(string model) => Assert.True(Default.IsSelectable(model));

    [Theory]
    [InlineData("codex-auto-review")]
    [InlineData("gpt-5.6-sol-openai-compact")]
    [InlineData("gpt-5.5-openai-compact")]
    [InlineData("gpt-reserve")]
    [InlineData("gpt-image-1")]
    [InlineData("gpt-image-2.5-flare")]
    [InlineData("gemini-3-pro-image-4k")]
    [InlineData("seedream-5-0")]
    [InlineData("gpt-4o-audio-preview")]
    [InlineData("gpt-4o-realtime-preview")]
    [InlineData("stepaudio-2.5-tts")]
    [InlineData("stepaudio-2.5-asr")]
    [InlineData("Qwen3-Embedding-8B")]
    [InlineData("测试模型")]
    [InlineData("gpt-5（限时）")]
    [InlineData("")]
    [InlineData("  ")]
    public void DropsTheNamesThatAreNotChatModels(string model) => Assert.False(Default.IsSelectable(model));

    [Fact]
    public void MatchingIgnoresCase() => Assert.False(Default.IsSelectable("Codex-Auto-Review"));

    [Fact]
    public void AllowPatternsKeepOnlyWhatTheyMatch()
    {
        var filter = new ModelIdFilter(allow: ["gpt-*", "claude-*"]);

        Assert.True(filter.IsSelectable("gpt-5.5"));
        Assert.True(filter.IsSelectable("CLAUDE-opus-5"));
        Assert.False(filter.IsSelectable("kimi-k3"));
        Assert.False(filter.IsSelectable("codex-auto-review")); // still denied, and not allowed anyway
    }

    [Fact]
    public void DenyPatternsAddToTheBuiltInOnes()
    {
        var filter = new ModelIdFilter(deny: ["gpt-5.3-*", "*-thinking"]);

        Assert.False(filter.IsSelectable("gpt-5.3-codex"));
        Assert.False(filter.IsSelectable("claude-opus-4-8-thinking"));
        Assert.False(filter.IsSelectable("codex-auto-review"));
        Assert.True(filter.IsSelectable("gpt-5.5"));
    }

    [Fact]
    public void TheBuiltInDenyListCanBeSwitchedOff()
    {
        var filter = new ModelIdFilter(useDefaultDeny: false);

        Assert.True(filter.IsSelectable("codex-auto-review"));
        Assert.False(filter.IsSelectable("测试")); // Chinese is dropped whatever the file says
    }

    [Fact]
    public void QuestionMarkMatchesOneCharacterAndRegexCharactersAreLiteral()
    {
        var filter = new ModelIdFilter(useDefaultDeny: false, deny: ["gpt-5.?", "a.b+c"]);

        Assert.False(filter.IsSelectable("gpt-5.5"));
        Assert.True(filter.IsSelectable("gpt-5.55"));
        Assert.False(filter.IsSelectable("a.b+c"));
        Assert.True(filter.IsSelectable("aXb+c"));
    }

    [Fact]
    public void ReadsTheUsersFile_WithCommentsAndTrailingCommas()
    {
        ModelIdFilter filter = ModelIdFilter.Parse("""
            {
              // only these families
              "allow": ["gpt-*",],
              "deny": ["gpt-5.3-*"],
            }
            """);

        Assert.True(filter.IsSelectable("gpt-5.5"));
        Assert.False(filter.IsSelectable("gpt-5.3-codex"));
        Assert.False(filter.IsSelectable("claude-opus-5"));
        Assert.False(filter.IsSelectable("codex-auto-review"));
    }

    [Fact]
    public void AFileThatCannotBeUsedFallsBackToTheBuiltInFilter_AndSaysSo()
    {
        var warnings = new List<string>();

        ModelIdFilter broken = ModelIdFilter.Parse("{ not json", warnings.Add);
        ModelIdFilter notAnObject = ModelIdFilter.Parse("[1,2]", warnings.Add);

        Assert.Equal(2, warnings.Count);
        Assert.True(broken.IsSelectable("gpt-5.5"));
        Assert.False(broken.IsSelectable("codex-auto-review"));
        Assert.False(notAnObject.IsSelectable("codex-auto-review"));
    }

    [Fact]
    public void AMissingFileIsTheBuiltInFilter()
    {
        ModelIdFilter filter = ModelIdFilter.Load(Path.Combine(Path.GetTempPath(), "no-such-" + Guid.NewGuid().ToString("N") + ".json"));

        Assert.False(filter.IsSelectable("codex-auto-review"));
        Assert.True(filter.IsSelectable("gpt-5.5"));
    }

    [Fact]
    public void UseDefaultDenyFalseInTheFileTurnsTheBuiltInsOff()
    {
        ModelIdFilter filter = ModelIdFilter.Parse("{ \"useDefaultDeny\": false, \"deny\": [\"gpt-image-*\"] }");

        Assert.True(filter.IsSelectable("codex-auto-review"));
        Assert.False(filter.IsSelectable("gpt-image-1"));
    }
}
