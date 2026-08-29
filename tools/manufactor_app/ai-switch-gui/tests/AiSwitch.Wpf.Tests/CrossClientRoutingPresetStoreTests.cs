using AiSwitchGui;
using LanAi.Workspace.Wpf.Services;

namespace AiSwitch.Wpf.Tests;

public sealed class CrossClientRoutingPresetStoreTests
{
    [Fact]
    public void Presets_RoundTripIndependentlyForEverySourceAndDirection()
    {
        string root = Path.Combine(Path.GetTempPath(), "LanAiRoutingPresetTests", Guid.NewGuid().ToString("N"));
        string path = Path.Combine(root, "cross-client-routing-presets.json");
        try
        {
            var store = new CrossClientRoutingPresetStore(path);
            Assert.True(store.SaveClaudeGpt("source-a", new ClaudeGptModelMapping
            {
                OpusModel = "gpt-a-opus",
                SonnetModel = "gpt-a-sonnet",
                HaikuModel = "gpt-a-haiku",
            }));
            Assert.True(store.SaveClaudeGpt("source-b", new ClaudeGptModelMapping
            {
                OpusModel = "gpt-b-opus",
                SonnetModel = "gpt-b-sonnet",
                HaikuModel = "gpt-b-haiku",
            }));
            Assert.True(store.SaveCodexClaude("source-a", new CodexClaudeModelMapping
            {
                TargetPlatform = "Claude",
                DefaultModel = "claude-a-default",
                ReviewModel = "claude-a-review",
                ReasoningEffort = "xhigh",
            }));
            Assert.True(store.SaveCodexClaude("source-a", new CodexClaudeModelMapping
            {
                TargetPlatform = "Grok",
                DefaultModel = "grok-a-default",
                ReviewModel = "grok-a-review",
                ReasoningEffort = "high",
            }));

            var restarted = new CrossClientRoutingPresetStore(path);

            Assert.Equal("gpt-a-opus", restarted.ReadClaudeGpt("source-a")?.OpusModel);
            Assert.Equal("gpt-b-opus", restarted.ReadClaudeGpt("source-b")?.OpusModel);
            Assert.Equal("claude-a-default", restarted.ReadCodexClaude("source-a", "Claude")?.DefaultModel);
            Assert.Equal("xhigh", restarted.ReadCodexClaude("source-a", "Claude")?.ReasoningEffort);
            Assert.Equal("grok-a-default", restarted.ReadCodexClaude("source-a", "Grok")?.DefaultModel);
            Assert.Equal("high", restarted.ReadCodexClaude("source-a", "Grok")?.ReasoningEffort);
            Assert.Null(restarted.ReadCodexClaude("source-b", "Claude"));
        }
        finally
        {
            if (Directory.Exists(root))
            {
                Directory.Delete(root, recursive: true);
            }
        }
    }
}
