using System.IO;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Covers the loopback relay that is now the client's default transport.
/// </summary>
/// <remarks>
/// Driven through real sockets rather than a seam. The things most worth protecting
/// here — that the account session is re-read per request, that a group switch lands,
/// that the gates turn away what they are meant to — are all properties of what
/// actually goes out on the wire, and a fake transport would assert none of them.
/// </remarks>
public sealed class LocalPawRelayTests
{
    // ---- Gates --------------------------------------------------------------

    [Fact]
    public void RejectsAnythingButThePostCodexPath()
    {
        Assert.Equal(404, StatusOf(Reject(method: "GET")));
        Assert.Equal(404, StatusOf(Reject(path: "/v1/chat/completions")));
        // The one GET the relay answers is Codex's model list; see OnlyTheCatalogPathAnswersGet.
        Assert.Equal(404, StatusOf(Reject(path: "/v1/models")));
        Assert.Null(Reject());
    }

    [Fact]
    public void RejectsBrowserOriginatedRequests()
    {
        // A page cannot read the answer, but the request alone already spends money.
        Assert.Equal(403, StatusOf(Reject(origin: "https://example.com")));
        Assert.Equal(403, StatusOf(Reject(origin: "null")));
        // Presence is the test, not content: an empty Origin is still a browser.
        Assert.Equal(403, StatusOf(Reject(origin: "")));
    }

    [Fact]
    public void RejectsAForeignHostHeader()
    {
        // DNS rebinding: a name that resolves to 127.0.0.1 walks past same-origin.
        Assert.Equal(403, StatusOf(Reject(host: "evil.example.com")));
        Assert.Equal(403, StatusOf(Reject(host: "127.0.0.1:9999")));
        Assert.Equal(403, StatusOf(Reject(host: null)));
    }

    [Fact]
    public void RejectsAWrongOrMissingToken()
    {
        Assert.Equal(401, StatusOf(Reject(authorization: null)));
        Assert.Equal(401, StatusOf(Reject(authorization: "Bearer nope")));
        Assert.Equal(401, StatusOf(Reject(authorization: "token abc123")));
        Assert.Null(Reject(authorization: "bearer abc123"));
    }

    private static int StatusOf((int Status, string Message)? rejection)
    {
        Assert.NotNull(rejection);
        return rejection.Value.Status;
    }

    private static (int Status, string Message)? Reject(
        string method = "POST",
        string? path = "/v1/responses",
        string? host = "127.0.0.1:1234",
        string? origin = null,
        string? authorization = "Bearer abc123") =>
        LocalPawRelay.Reject(method, path, host, origin, authorization, "abc123", 1234);

    // ---- What the log can be asked afterwards ---------------------------------

    [Fact]
    public void ARequestWithNoFilterHeadersIsReportedAsUncompressed()
    {
        // Measured: with compression off the filter stamps no headers at all, so their
        // absence is the signal. It cannot tell "filter off" from "no filter present" —
        // the startup line is what separates those, and this wording claims neither.
        Assert.Equal("本轮未经过上下文压缩", Describe(null, null, null, null, null));
    }

    [Fact]
    public void AComprssedRequestReportsWhatWasSaved()
    {
        string line = Describe("true", "true", "46284", "12045", "34239");

        Assert.Contains("45.2 KB", line, StringComparison.Ordinal);
        Assert.Contains("11.8 KB", line, StringComparison.Ordinal);
        Assert.Contains("生效", line, StringComparison.Ordinal);
        Assert.Contains("74.0%", line, StringComparison.Ordinal);
    }

    [Fact]
    public void EnabledButUnchangedIsReportedAsNormal_NotAsAFailure()
    {
        // A short turn with nothing worth compressing is the common case; wording it
        // as a fault would train the reader to ignore the line.
        string line = Describe("true", "false", "812", "812", "0");

        Assert.Contains("已启用", line, StringComparison.Ordinal);
        Assert.Contains("未压缩", line, StringComparison.Ordinal);
        Assert.DoesNotContain("生效", line, StringComparison.Ordinal);
    }

    [Fact]
    public void MalformedOrPartialHeadersNeverThrow()
    {
        Assert.Contains("未报告大小", Describe("true", "true", "not-a-number", null, null), StringComparison.Ordinal);
        Assert.Contains("压缩未启用", Describe("false", "false", "10", "10", "0"), StringComparison.Ordinal);
        // Saved missing: derived from before/after rather than giving up.
        Assert.Contains("省 ", Describe("true", "true", "2048", "1024", null), StringComparison.Ordinal);
    }

    private static string Describe(string? enabled, string? changed, string? before, string? after, string? saved) =>
        LocalPawRelay.DescribeContextFilter(enabled, changed, before, after, saved);

    [Fact]
    public void AnUpstreamRefusalIsSummarisedForTheLogWithoutRunningAway()
    {
        Assert.Equal("(无响应内容)", LocalPawRelay.Summarize(null));
        Assert.Equal("(无响应内容)", LocalPawRelay.Summarize("   "));

        // Flattened, so one refusal stays one log line rather than smearing a JSON
        // envelope across the file the user is asked to send in.
        Assert.Equal(
            "group forbidden for this account",
            LocalPawRelay.Summarize("group forbidden" + Environment.NewLine + "for this account"));

        // Capped: an unbounded upstream body would be both unreadable and a place for
        // content to end up in a support log.
        string huge = LocalPawRelay.Summarize(new string('x', 5000));
        Assert.True(huge.Length <= 301, $"截断后仍有 {huge.Length} 字符");
        Assert.EndsWith("…", huge, StringComparison.Ordinal);
    }

    [Fact]
    public void AClientHangingUpMidAnswerIsNotTreatedAsAFailure()
    {
        // Measured in a real session: pressing stop in ChatGPT produced exactly these
        // two, and they were logged as warnings with a stack trace each time.
        Assert.True(LocalPawRelay.ClientWentAway(new HttpListenerException(1229)));
        Assert.True(LocalPawRelay.ClientWentAway(new HttpListenerException(64)));
        Assert.True(LocalPawRelay.ClientWentAway(new ObjectDisposedException("stream")));
        Assert.True(LocalPawRelay.ClientWentAway(new IOException("wrapped", new HttpListenerException(64))));

        // A genuine fault must keep its warning and its stack trace.
        Assert.False(LocalPawRelay.ClientWentAway(new HttpListenerException(5)));
        Assert.False(LocalPawRelay.ClientWentAway(new InvalidOperationException("boom")));
        Assert.False(LocalPawRelay.ClientWentAway(new IOException("plain")));
    }

    // ---- The running usage total ----------------------------------------------

    [Fact]
    public async Task InvokesTheUsageCallbackWithWhatTheFilterReported()
    {
        // This is the only channel ContextFilterUsageStore has into a live request —
        // if this stops firing, the number next to 启用上下文压缩 stops moving and
        // nothing else would notice.
        await using var upstream = await FakeUpstream.StartAsync();
        var measured = new List<(long Before, long Saved)>();
        await using var relay = new LocalPawRelay(
            upstream.BaseAddress,
            _ => Task.FromResult("jwt"),
            (before, saved) => measured.Add((before, saved)));
        await relay.StartAsync();
        relay.SetGroup(7);

        await PostAsync(relay, headers:
        [
            ("X-Context-Filter-Enabled", "true"),
            ("X-Context-Filter-Changed", "true"),
            ("X-Context-Filter-Bytes-Before", "1000"),
            ("X-Context-Filter-Bytes-After", "400"),
            ("X-Context-Filter-Bytes-Saved", "600"),
        ]);

        Assert.Equal([(1000L, 600L)], measured);
    }

    [Fact]
    public async Task StillCountsAnUnchangedTurnAsBytesProcessed()
    {
        // "Total processed" has to include turns where nothing was compressible —
        // otherwise the total would only ever grow on the turns already reported by
        // the per-request log line as 生效, which is most turns, not all of them.
        await using var upstream = await FakeUpstream.StartAsync();
        var measured = new List<(long Before, long Saved)>();
        await using var relay = new LocalPawRelay(
            upstream.BaseAddress,
            _ => Task.FromResult("jwt"),
            (before, saved) => measured.Add((before, saved)));
        await relay.StartAsync();
        relay.SetGroup(7);

        await PostAsync(relay, headers:
        [
            ("X-Context-Filter-Enabled", "true"),
            ("X-Context-Filter-Changed", "false"),
            ("X-Context-Filter-Bytes-Before", "812"),
            ("X-Context-Filter-Bytes-After", "812"),
            ("X-Context-Filter-Bytes-Saved", "0"),
        ]);

        Assert.Equal([(812L, 0L)], measured);
    }

    [Fact]
    public async Task NeverInvokesTheUsageCallbackWithNothingToMeasure()
    {
        await using var upstream = await FakeUpstream.StartAsync();
        var measured = new List<(long, long)>();
        await using var relay = new LocalPawRelay(
            upstream.BaseAddress,
            _ => Task.FromResult("jwt"),
            (before, saved) => measured.Add((before, saved)));
        await relay.StartAsync();
        relay.SetGroup(7);

        // No filter headers at all — the filter is off or absent.
        await PostAsync(relay);
        // Filter present but disabled: no bytes reported either, same as measured live.
        await PostAsync(relay, headers: [("X-Context-Filter-Enabled", "false")]);

        Assert.Empty(measured);
    }

    // ---- The account session -------------------------------------------------

    [Fact]
    public async Task ReadsTheAccountSessionAgainOnEveryRequest()
    {
        // The defect this locks down: the session used to be snapshotted when Codex
        // launched. Access tokens rotate, so within the hour every Codex request 401s
        // while the dashboard — which re-fetches each poll — still looks healthy.
        await using var upstream = await FakeUpstream.StartAsync();
        int issued = 0;
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult($"jwt-{++issued}"));
        await relay.StartAsync();
        relay.SetGroup(7);

        await PostAsync(relay);
        await PostAsync(relay);

        Assert.Equal(["Bearer jwt-1", "Bearer jwt-2"], upstream.Requests.Select(r => r.Authorization));
    }

    [Fact]
    public async Task AnsweringWithoutASessionIsA401AndNeverReachesTheServer()
    {
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(
            upstream.BaseAddress,
            _ => throw new RelayApiException(RelayFailure.Unauthenticated, "尚未登录。"));
        await relay.StartAsync();
        relay.SetGroup(7);

        HttpResponseMessage response = await PostAsync(relay);

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
        Assert.Empty(upstream.Requests);
    }

    // ---- The billing group ---------------------------------------------------

    [Fact]
    public async Task StampsTheSelectedGroupAndHonoursASwitchOnTheNextRequest()
    {
        // The other half of the same defect: switching groups in the dashboard used to
        // leave the relay on the previous one, so traffic kept being billed somewhere
        // the user had been told it would not go.
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();

        relay.SetGroup(11);
        await PostAsync(relay);
        relay.SetGroup(22);
        await PostAsync(relay);

        Assert.Equal(["11", "22"], upstream.Requests.Select(r => r.Group));
    }

    [Fact]
    public async Task UsesAutomaticRoutingWhenNoFixedGroupIsBound()
    {
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();

        await PostAsync(relay);

        FakeUpstream.Captured sent = Assert.Single(upstream.Requests);
        Assert.Equal("auto", sent.Group);
    }

    // ---- What actually goes upstream ----------------------------------------

    [Fact]
    public async Task ForwardsOnTheWireContractTheServerListensOn()
    {
        // These literals also exist in Go (routes/paw.go) and Rust (codex-host). Renaming
        // one side leaves every test green and the product 404ing, so they are pinned
        // as literals here rather than referenced from a shared constant.
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetGroup(7);

        await PostAsync(relay, body: """{"model":"gpt-5","input":[]}""");

        FakeUpstream.Captured sent = Assert.Single(upstream.Requests);
        Assert.Equal("/api/v1/paw/responses", sent.Path);
        Assert.Equal("7", sent.Group);
        Assert.Equal("""{"model":"gpt-5","input":[]}""", sent.Body);
    }

    [Fact]
    public async Task NeverLetsTheLocalTokenReachTheServer()
    {
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetGroup(7);

        await PostAsync(relay);

        FakeUpstream.Captured sent = Assert.Single(upstream.Requests);
        Assert.Equal("Bearer jwt", sent.Authorization);
        Assert.DoesNotContain(relay.Token, sent.AllHeaders, StringComparison.Ordinal);
    }

    [Fact]
    public async Task RefusalsCarryAnErrorBodyCodexCanClassify()
    {
        // A bare status with an empty body reaches the user as an unexplained protocol
        // error; this shape Codex files as an upstream failure.
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();

        HttpResponseMessage response = await PostAsync(
            relay,
            headers: [("Origin", "https://example.com")]);
        using JsonDocument payload = JsonDocument.Parse(await response.Content.ReadAsStringAsync());

        Assert.Equal("application/json", response.Content.Headers.ContentType?.MediaType);
        Assert.Equal("cofly_local_relay", payload.RootElement.GetProperty("error").GetProperty("type").GetString());
        Assert.False(string.IsNullOrWhiteSpace(
            payload.RootElement.GetProperty("error").GetProperty("message").GetString()));
    }

    [Fact]
    public async Task StartingTwiceKeepsTheSamePortInsteadOfLosingIt()
    {
        // Pressing 启动 twice is an explicitly supported flow, and the second RunAsync
        // calls StartAsync again. The address Codex was just configured with must
        // survive that, rather than being nulled out from under it.
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));

        await relay.StartAsync();
        Uri? first = relay.BaseAddress;
        await relay.StartAsync();

        Assert.NotNull(first);
        Assert.Equal(first, relay.BaseAddress);

        relay.SetGroup(7);
        await PostAsync(relay);
        Assert.Single(upstream.Requests);
    }

    [Fact]
    public async Task StopLeavesTheRelayReusableForALaterSignIn()
    {
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));

        await relay.StartAsync();
        relay.SetGroup(7);
        await PostAsync(relay);
        await relay.StopAsync();

        await relay.StartAsync();
        relay.SetGroup(8);
        await PostAsync(relay);

        Assert.Equal(["7", "8"], upstream.Requests.Select(r => r.Group));
    }

    // ---- Harness -------------------------------------------------------------

    // ---- The model list Codex's picker shows ---------------------------------

    private const string BundledCatalog = """
        {"models":[
          {"slug":"gpt-6-astra","display_name":"GPT-6-Astra","description":"d","priority":1,"visibility":"list","tool_mode":"code_mode_only","use_responses_lite":true,"multi_agent_version":"v2","shell_type":"unified_exec","base_instructions":"b","supported_reasoning_levels":[{"effort":"low","description":"x"}]},
          {"slug":"gpt-5.5","display_name":"GPT-5.5","description":"d","priority":9,"visibility":"list","tool_mode":null,"use_responses_lite":false,"multi_agent_version":null,"shell_type":"unified_exec","base_instructions":"b","upgrade":{"model":"x"},"supported_reasoning_levels":[{"effort":"low","description":"x"}]}
        ]}
        """;

    private sealed class FixedCatalogSource(string? json) : LanAi.RelayClient.Services.ICodexCatalogSource
    {
        public int Calls { get; private set; }

        public Task<string?> GetBundledCatalogAsync(CancellationToken cancellationToken = default)
        {
            Calls++;
            return Task.FromResult(json);
        }
    }

    private static async Task<HttpResponseMessage> GetModelsAsync(LocalPawRelay relay, string? token = null)
    {
        using var client = new HttpClient();
        using var request = new HttpRequestMessage(HttpMethod.Get, new Uri(relay.BaseAddress!, "models?client_version=0.158.0"));
        request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + (token ?? relay.Token));
        return await client.SendAsync(request);
    }

    private static string[] SlugsOf(string catalogJson) =>
        [.. JsonDocument.Parse(catalogJson).RootElement.GetProperty("models").EnumerateArray()
            .Select(m => m.GetProperty("slug").GetString()!)];

    [Fact]
    public void OnlyTheCatalogPathAnswersGet_AndOnlyResponsesAnswersPost()
    {
        Assert.Null(Reject(method: "GET", path: "/v1/models"));
        Assert.Equal(404, StatusOf(Reject(method: "POST", path: "/v1/models")));
        Assert.Equal(404, StatusOf(Reject(method: "GET", path: "/v1/responses")));
    }

    [Fact]
    public async Task TheCatalogGateStillDemandsTheLocalToken()
    {
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), codexCatalogSource: new FixedCatalogSource(BundledCatalog));
        await relay.StartAsync();
        relay.SetGroup(7, "g", LanAi.RelayClient.Services.CodexGroupModels.From(["claude-sonnet-5"]));

        HttpResponseMessage response = await GetModelsAsync(relay, token: "not-the-token");

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }

    [Fact]
    public async Task ServesTheGroupsModelsAsCodexsCatalogWithTheDefaultFirst()
    {
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), codexCatalogSource: new FixedCatalogSource(BundledCatalog));
        await relay.StartAsync();
        relay.SetGroup(7, "Claude", LanAi.RelayClient.Services.CodexGroupModels.From(["claude-sonnet-5", "claude-opus-5"], preferred: "claude-opus-5"));

        HttpResponseMessage response = await GetModelsAsync(relay);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal(["claude-opus-5", "claude-sonnet-5"], SlugsOf(await response.Content.ReadAsStringAsync()));
    }

    [Theory]
    [InlineData("no whitelist")]
    [InlineData("automatic group")]
    public async Task GivesCodexItsOwnListBackWhenThereIsNothingOfOursToShow(string why)
    {
        // Its own list rather than a 404: a 404 makes Codex fall back to the copy of the last
        // catalog it fetched, which is still the previous group's for up to five minutes.
        var models = LanAi.RelayClient.Services.CodexGroupModels.From(["claude-sonnet-5"]);
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), codexCatalogSource: new FixedCatalogSource(BundledCatalog));
        await relay.StartAsync();
        if (why == "no whitelist")
        {
            relay.SetGroup(7, "g", null);
        }
        else
        {
            relay.SetGroup(null, "auto", models);
        }

        HttpResponseMessage response = await GetModelsAsync(relay);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal(["gpt-6-astra", "gpt-5.5"], SlugsOf(await response.Content.ReadAsStringAsync()));
    }

    [Fact]
    public async Task IsA404OnlyWhenCodexsOwnCatalogCannotBeRead()
    {
        var models = LanAi.RelayClient.Services.CodexGroupModels.From(["claude-sonnet-5"]);
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), codexCatalogSource: new FixedCatalogSource(null));
        await relay.StartAsync();

        relay.SetGroup(7, "g", models);
        Assert.Equal(HttpStatusCode.NotFound, (await GetModelsAsync(relay)).StatusCode);

        relay.SetGroup(7, "g", null);
        Assert.Equal(HttpStatusCode.NotFound, (await GetModelsAsync(relay)).StatusCode);
    }

    [Fact]
    public async Task SwitchingGroupsChangesWhatTheNextCatalogRequestSees()
    {
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), codexCatalogSource: new FixedCatalogSource(BundledCatalog));
        await relay.StartAsync();

        relay.SetGroup(1, "a", LanAi.RelayClient.Services.CodexGroupModels.From(["claude-sonnet-5"]));
        Assert.Equal(["claude-sonnet-5"], SlugsOf(await (await GetModelsAsync(relay)).Content.ReadAsStringAsync()));

        relay.SetGroup(2, "b", null);
        Assert.Equal(["gpt-6-astra", "gpt-5.5"], SlugsOf(await (await GetModelsAsync(relay)).Content.ReadAsStringAsync()));
    }

    // ---- A model the group does not serve ------------------------------------

    [Fact]
    public async Task MovesARequestForAnUnservedModelOntoTheGroupsDefault_AndTouchesNothingElse()
    {
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetGroup(7, "Claude", LanAi.RelayClient.Services.CodexGroupModels.From(["claude-sonnet-5", "claude-opus-5"], "claude-opus-5"));

        await PostAsync(relay, body: """{"input":[{"model":"nested-untouched"}],  "model" : "gpt-5.5","stream":true}""");

        FakeUpstream.Captured sent = Assert.Single(upstream.Requests);
        Assert.Equal("""{"input":[{"model":"nested-untouched"}],  "model" : "claude-opus-5","stream":true}""", sent.Body);
    }

    [Theory]
    [InlineData("""{"model":"claude-sonnet-5","input":[]}""")]
    [InlineData("""{"model":"CLAUDE-SONNET-5","input":[]}""")]
    [InlineData("""{"input":[],"nothing":"here"}""")]
    [InlineData("""{"model":12}""")]
    public async Task LeavesARequestAloneWhenTheModelIsServedOrThereIsNoneToJudge(string body)
    {
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetGroup(7, "Claude", LanAi.RelayClient.Services.CodexGroupModels.From(["claude-sonnet-5"]));

        await PostAsync(relay, body: body);

        Assert.Equal(body, Assert.Single(upstream.Requests).Body);
    }

    [Fact]
    public async Task DoesNotRewriteWithoutAWhitelistOrOnTheAutomaticGroup()
    {
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        const string body = """{"model":"gpt-5.5"}""";

        relay.SetGroup(7, "g", null);
        await PostAsync(relay, body: body);
        relay.SetGroup(null, "auto", LanAi.RelayClient.Services.CodexGroupModels.From(["claude-sonnet-5"]));
        await PostAsync(relay, body: body);

        Assert.All(upstream.Requests, r => Assert.Equal(body, r.Body));
    }

    [Fact]
    public async Task ASwitchToAGroupWithoutAWhitelistDropsTheOldGroupsModels()
    {
        // Models live and die with the group they came from: left behind, they would
        // rewrite the next group's traffic onto a model it does not offer.
        await using var upstream = await FakeUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetGroup(7, "Claude", LanAi.RelayClient.Services.CodexGroupModels.From(["claude-sonnet-5"]));
        relay.SetGroup(8, "OpenAI");

        await PostAsync(relay, body: """{"model":"gpt-5.5"}""");

        Assert.Equal("""{"model":"gpt-5.5"}""", Assert.Single(upstream.Requests).Body);
    }

    private static async Task<HttpResponseMessage> PostAsync(
        LocalPawRelay relay,
        string body = "{}",
        IEnumerable<(string Name, string Value)>? headers = null)
    {
        using var client = new HttpClient();
        using var request = new HttpRequestMessage(
            HttpMethod.Post,
            new Uri(relay.BaseAddress!, "responses"))
        {
            Content = new StringContent(body, Encoding.UTF8, "application/json"),
        };
        request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + relay.Token);
        if (headers is not null)
        {
            foreach ((string name, string value) in headers)
            {
                request.Headers.TryAddWithoutValidation(name, value);
            }
        }

        return await client.SendAsync(request);
    }

    /// <summary>A stand-in for the server, recording exactly what arrived.</summary>
    private sealed class FakeUpstream : IAsyncDisposable
    {
        private readonly HttpListener _listener;
        private readonly CancellationTokenSource _stop = new();
        private readonly Task _serve;

        public sealed record Captured(string Path, string? Authorization, string? Group, string Body, string AllHeaders);

        public List<Captured> Requests { get; } = [];

        public string BaseAddress { get; }

        private FakeUpstream()
        {
            _listener = LoopbackHttpListener.Start(null, out int port);
            BaseAddress = $"http://127.0.0.1:{port}";
            _serve = ServeAsync(_stop.Token);
        }

        public static Task<FakeUpstream> StartAsync()
        {
            return Task.FromResult(new FakeUpstream());
        }

        private async Task ServeAsync(CancellationToken cancellationToken)
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                HttpListenerContext context;
                try { context = await _listener.GetContextAsync().WaitAsync(cancellationToken); }
                catch (Exception) { break; }

                using var reader = new StreamReader(context.Request.InputStream, Encoding.UTF8);
                string body = await reader.ReadToEndAsync(cancellationToken);
                var headers = new StringBuilder();
                foreach (string? name in context.Request.Headers.AllKeys)
                {
                    headers.Append(name).Append(": ").AppendLine(context.Request.Headers[name]);
                }

                lock (Requests)
                {
                    Requests.Add(new Captured(
                        context.Request.Url?.AbsolutePath ?? string.Empty,
                        context.Request.Headers["Authorization"],
                        context.Request.Headers["X-Paw-Group-Id"],
                        body,
                        headers.ToString()));
                }

                byte[] payload = Encoding.UTF8.GetBytes("data: {\"type\":\"response.completed\"}\n\n");
                context.Response.StatusCode = 200;
                context.Response.ContentType = "text/event-stream";
                await context.Response.OutputStream.WriteAsync(payload, cancellationToken);
                context.Response.Close();
            }
        }

        public async ValueTask DisposeAsync()
        {
            await _stop.CancelAsync();
            _listener.Close();
            try { await _serve; } catch (Exception) { }
            _stop.Dispose();
        }
    }
}
