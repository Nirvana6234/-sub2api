using System.Text.Json;
using LanAi.RelayClient.CodexBinding.DesktopSync;
using LanAi.RelayClient.DesktopSync;
using Xunit;

namespace LanAi.RelayClient.Tests.DesktopSync;

/// <summary>The item fields the phone reads (protocol.ts readItem), as this side writes them.</summary>
public sealed class SyncJsonTests
{
    private static JsonElement Written(SyncItem item)
    {
        byte[] json = SyncJson.Write(w => SyncJson.WriteItems(w, "items", [item]));
        return JsonDocument.Parse(json).RootElement.GetProperty("items")[0];
    }

    [Fact]
    public void AnUntaggedMessageCarriesTheFlag()
    {
        JsonElement item = Written(new SyncItem(12, "t", "m1", SyncItemKind.Progress) { Text = "改好了。", PhaseMissing = true });

        Assert.Equal("progress", item.GetProperty("kind").GetString());
        Assert.True(item.GetProperty("phase_missing").GetBoolean());
    }

    [Fact]
    public void ATaggedMessageLeavesItOut()
    {
        JsonElement item = Written(new SyncItem(12, "t", "m1", SyncItemKind.Progress) { Text = "我先看一下。" });

        Assert.False(item.TryGetProperty("phase_missing", out _));
    }
}
