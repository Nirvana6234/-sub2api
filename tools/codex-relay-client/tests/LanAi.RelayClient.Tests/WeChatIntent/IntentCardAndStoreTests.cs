using System.Text.Json;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.WeChatIntent;
using Xunit;

namespace LanAi.RelayClient.Tests.WeChatIntent;

public sealed class IntentCardAndStoreTests : IDisposable
{
    private readonly string _dir = Path.Combine(Path.GetTempPath(), "wechat-intent-tests-" + Guid.NewGuid().ToString("N"));

    public void Dispose()
    {
        if (Directory.Exists(_dir))
        {
            Directory.Delete(_dir, recursive: true);
        }
    }

    [Fact]
    public void TheCardFollowsTheDisplayRules()
    {
        JevResponse response = JsonSerializer.Deserialize(JevClientTests.OkBody, JevJsonContext.Default.JevResponse)!;

        IntentCard card = IntentCard.From(response, "小明", [new ChatItem(ChatSpeaker.Them, "那你说")], stale: false, DateTimeOffset.Now);

        Assert.Equal("在考验你 93% · 想被在乎 5%", card.Intent);
        Assert.False(card.IntentUncertain);
        Assert.Equal("不耐烦", card.Emotion);

        // The most probable level (3), not the score (2.8) rounded or interpolated.
        Assert.Equal(3, card.RiskLevel);
        Assert.Equal("回错一句就会吵起来或冷战", card.RiskText);
        Assert.False(card.RiskHigh);            // score 2.8 is under the red-border threshold of 3
        Assert.True(card.ReplySoon);            // 0.79 > 0.6
        Assert.Equal("解释清楚 80% · 明确表态 10%", card.Action);
        Assert.Equal("那你说", card.About);
    }

    [Fact]
    public void AFlatIntentIsMarkedUncertain()
    {
        var response = new JevResponse
        {
            Answers = new()
            {
                ["intent"] = new JevAnswer { Type = "choice", Choice = "test", Probabilities = new() { ["test"] = 0.4, ["request"] = 0.35, ["other"] = 0.25 }, Confidence = 0.1 },
            },
        };

        Assert.True(IntentCard.From(response, "小明", [], false, DateTimeOffset.Now).IntentUncertain);
    }

    [Fact]
    public void TheRequestBodyIsValidJsonWithTheQuestionsSplicedIn()
    {
        byte[] body = JevRequestBody.Build("jev-1.13.0", new JevState { LatestFromThem = ["引号\"和\\反斜杠"] });

        using JsonDocument doc = JsonDocument.Parse(body);
        Assert.Equal("引号\"和\\反斜杠", doc.RootElement.GetProperty("state").GetProperty("latest_from_them")[0].GetString());
        Assert.Equal(WeChatIntentQuestions.RiskLevels, doc.RootElement.GetProperty("questions").GetProperty("risk").GetProperty("criteria").EnumerateArray().Select(e => e.GetString()));
    }

    private sealed class ReversingProtector : ISnapshotProtector
    {
        public byte[] Protect(byte[] plaintext) => plaintext.Reverse().Select(b => (byte)(b ^ 0x5a)).ToArray();

        public byte[] Unprotect(byte[] protectedData) => protectedData.Select(b => (byte)(b ^ 0x5a)).Reverse().ToArray();
    }

    [Fact]
    public void TheKeyIsStoredEncryptedPerAccountAndCleared()
    {
        var store = new JevApiKeyStore(new ReversingProtector(), _dir);
        string ann = JevApiKeyStore.ScopeFor("https://gongfeiai.com", "Ann@Example.com");
        string bob = JevApiKeyStore.ScopeFor("https://gongfeiai.com", "bob@example.com");

        store.Save(ann, "  apikey-secret-1234  ");

        Assert.Equal("apikey-secret-1234", store.Load(ann));
        Assert.Null(store.Load(bob));
        Assert.Equal(ann, JevApiKeyStore.ScopeFor("https://GONGFEIAI.com ", "ann@example.com"));
        Assert.DoesNotContain("apikey-secret", File.ReadAllText(Directory.GetFiles(_dir).Single()), StringComparison.Ordinal);

        store.Clear(ann);
        Assert.Null(store.Load(ann));
    }

    [Fact]
    public void TheKeyIsShownOnlyMasked() =>
        Assert.Equal("apik…1234", JevApiKeyStore.Mask(" apikey-secret-1234 "));

    [Fact]
    public void UsageCountsTodayAndStartsOverTomorrow()
    {
        DateTime day = new(2026, 9, 25);
        var usage = new WeChatIntentUsageStore(Path.Combine(_dir, "usage.json"), () => day);

        usage.Add(1100);
        usage.Add(1200);
        Assert.Equal((2, 2300L), (usage.Today().Count, usage.Today().InputTokens));

        day = day.AddDays(1);
        Assert.Equal((0, 0L), (usage.Today().Count, usage.Today().InputTokens));
    }

    [Fact]
    public void PreferencesRoundTripAndHoldNoConversation()
    {
        var store = new WeChatIntentPreferenceStore(Path.Combine(_dir, "preferences.json"));
        store.Save(new WeChatIntentPreferenceStore.Preferences { Enabled = true, ConsentVersion = 1, Automatic = false, Muted = ["工作群"] });

        WeChatIntentPreferenceStore.Preferences loaded = store.Load();

        Assert.True(loaded.Enabled);
        Assert.Equal(1, loaded.ConsentVersion);
        Assert.False(loaded.Automatic);
        Assert.Equal(["工作群"], loaded.Muted);
    }
}
