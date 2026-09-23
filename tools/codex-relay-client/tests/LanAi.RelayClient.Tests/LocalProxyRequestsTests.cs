using System.Collections.Specialized;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>What the local proxy adds to, keeps from and strips out of a tool's request.</summary>
public sealed class LocalProxyRequestsTests
{
    private static readonly LocalProxyCredential OpenAICredential =
        new(accountId: 1, platform: "openai", accessToken: "at-official", chatgptAccountId: "acct-9");

    private static NameValueCollection CodexHeaders(params (string Name, string Value)[] extra)
    {
        var headers = new NameValueCollection
        {
            ["Authorization"] = "Bearer local-relay-token",
            ["User-Agent"] = "codex_cli_rs/0.160.0 (Windows 10.0.26200; x86_64)",
            ["originator"] = "codex_cli_rs",
            ["session_id"] = "sess-1",
            ["version"] = "0.160.0",
            ["X-Context-Filter-Enabled"] = "true",
            ["X-Context-Filter-Bytes-Before"] = "1000",
            ["Accept-Encoding"] = "gzip",
            ["X-Custom-Junk"] = "nope",
        };
        foreach ((string name, string value) in extra)
        {
            headers[name] = value;
        }
        return headers;
    }

    /// <summary>The value exactly as it will go on the wire (TryGetValues would split a User-Agent into tokens).</summary>
    private static string? Header(HttpRequestMessage request, string name) =>
        request.Headers.NonValidated.TryGetValues(name, out HeaderStringValues values) ? values.ToString() : null;

    // ---- Codex --------------------------------------------------------------------------

    [Fact]
    public void CodexKeepsItsOwnIdentityAndGetsTheAccountsToken()
    {
        using HttpRequestMessage request = LocalProxyRequests.BuildCodex(
            "https://official/responses", CodexHeaders(), "{}"u8.ToArray(), "application/json", OpenAICredential, compact: false);

        Assert.Equal("Bearer at-official", Header(request, "Authorization"));
        Assert.Equal("acct-9", Header(request, "chatgpt-account-id"));
        Assert.Equal("codex_cli_rs/0.160.0 (Windows 10.0.26200; x86_64)", Header(request, "User-Agent"));
        Assert.Equal("codex_cli_rs", Header(request, "originator"));
        Assert.Equal("sess-1", Header(request, "session_id"));
        Assert.Equal("0.160.0", Header(request, "version"));
        Assert.Null(Header(request, "x-openai-fedramp"));
    }

    [Fact]
    public void CodexNeverForwardsTheLocalTokenTheFilterStatsOrUnknownHeaders()
    {
        using HttpRequestMessage request = LocalProxyRequests.BuildCodex(
            "https://official/responses", CodexHeaders(), "{}"u8.ToArray(), null, OpenAICredential, compact: false);

        string all = request.Headers.ToString();
        Assert.DoesNotContain("local-relay-token", all);
        Assert.DoesNotContain("X-Context-Filter", all, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("X-Custom-Junk", all, StringComparison.OrdinalIgnoreCase);
        Assert.Null(Header(request, "Accept-Encoding"));
    }

    [Fact]
    public void CodexGetsTheDefaultBetaFeaturesAndStreamAcceptOnlyWhenItSentNone()
    {
        using HttpRequestMessage bare = LocalProxyRequests.BuildCodex(
            "u", CodexHeaders(), "{}"u8.ToArray(), null, OpenAICredential, compact: false);
        Assert.Equal(LocalProxyRequests.RemoteCompactionV2, Header(bare, "x-codex-beta-features"));
        Assert.Equal("text/event-stream", Header(bare, "Accept"));

        using HttpRequestMessage declared = LocalProxyRequests.BuildCodex(
            "u", CodexHeaders(("x-codex-beta-features", "other"), ("Accept", "application/json")),
            "{}"u8.ToArray(), null, OpenAICredential, compact: false);
        Assert.Equal("other", Header(declared, "x-codex-beta-features"));
        Assert.Equal("application/json", Header(declared, "Accept"));
    }

    [Fact]
    public void AFedRampAccountSaysSo()
    {
        var credential = OpenAICredential with { FedRamp = true };
        using HttpRequestMessage request = LocalProxyRequests.BuildCodex(
            "u", CodexHeaders(), "{}"u8.ToArray(), null, credential, compact: false);

        Assert.Equal("true", Header(request, "x-openai-fedramp"));
    }

    [Fact]
    public void OnlyTheLegacyResponsesBetaIsStripped()
    {
        Assert.Equal("other=1", LocalProxyRequests.StripLegacyResponsesBeta("responses=experimental, other=1"));
        Assert.Equal(string.Empty, LocalProxyRequests.StripLegacyResponsesBeta("responses=experimental"));
        Assert.Equal("assistants=v2", LocalProxyRequests.StripLegacyResponsesBeta("assistants=v2"));

        using HttpRequestMessage request = LocalProxyRequests.BuildCodex(
            "u", CodexHeaders(("OpenAI-Beta", "responses=experimental")), "{}"u8.ToArray(), null, OpenAICredential, compact: false);
        Assert.Null(Header(request, "OpenAI-Beta"));
    }

    [Fact]
    public void CompactAsksForJsonAndDropsStoreAndStream()
    {
        byte[] body = """{"model":"gpt-5.5","store":false,"stream":true,"input":[]}"""u8.ToArray();
        using HttpRequestMessage request = LocalProxyRequests.BuildCodex(
            "u", CodexHeaders(("Accept", "text/event-stream")), body, null, OpenAICredential, compact: true);

        Assert.Equal("application/json", Header(request, "Accept"));
        string sent = request.Content!.ReadAsStringAsync().Result;
        Assert.DoesNotContain("store", sent);
        Assert.DoesNotContain("stream", sent);
    }

    [Fact]
    public void ANormalCodexTurnReachesTheOfficialApiByteForByte()
    {
        byte[] body = Encoding.UTF8.GetBytes(
            """{ "model": "gpt-5.5", "instructions": "你好", "input": [{"type":"message","role":"user","content":"hi"}], "store": false, "stream": true, "include": ["reasoning.encrypted_content"] }""");

        Assert.Same(body, LocalProxyRequests.NormalizeCodexBody(body, compact: false));
    }

    [Fact]
    public void BodyFieldsTheChatGptBackendRefusesAreFixed()
    {
        byte[] body = Encoding.UTF8.GetBytes(
            """{"model":"gpt-5.5","prompt":"hello","commands":[1],"metadata":{"a":1},"user":"u","truncation":"auto","reasoning":{"mode":"pro"},"store":true}""");

        using JsonDocument fixedUp = JsonDocument.Parse(LocalProxyRequests.NormalizeCodexBody(body, compact: false));
        JsonElement root = fixedUp.RootElement;

        Assert.False(root.TryGetProperty("prompt", out _));
        Assert.False(root.TryGetProperty("commands", out _));
        Assert.False(root.TryGetProperty("metadata", out _));
        Assert.False(root.TryGetProperty("user", out _));
        Assert.False(root.TryGetProperty("truncation", out _));
        Assert.Equal("max", root.GetProperty("reasoning").GetProperty("effort").GetString());
        Assert.False(root.GetProperty("reasoning").TryGetProperty("mode", out _));
        Assert.False(root.GetProperty("store").GetBoolean());
        Assert.True(root.GetProperty("stream").GetBoolean());
        JsonElement message = Assert.Single(root.GetProperty("input").EnumerateArray());
        Assert.Equal("hello", message.GetProperty("content").GetString());
    }

    [Fact]
    public void TheAstraFamilyKeepsItsReasoningMode()
    {
        byte[] body = """{"model":"gpt-6-astra-1","reasoning":{"mode":"pro"},"store":false,"stream":true}"""u8.ToArray();

        Assert.Same(body, LocalProxyRequests.NormalizeCodexBody(body, compact: false));
    }

    [Fact]
    public void ABodyThatIsNotAJsonObjectIsLeftForTheOfficialApiToJudge()
    {
        byte[] body = "not json"u8.ToArray();

        Assert.Same(body, LocalProxyRequests.NormalizeCodexBody(body, compact: false));
    }
}
