using System.IO;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The Anthropic Messages half of the relay: what Claude Code and the editor
/// extensions built on it send, what is allowed to reach the server, and what comes
/// back. The shapes here were captured from a real Claude Code 2.1.258 CLI.
/// </summary>
public sealed class LocalPawRelayMessagesTests
{
    private const string EditorUserAgent = "claude-cli/2.1.258 (external, claude-vscode)";

    /// <summary>
    /// Codex and Claude Code each name their own group; setting one never moves the other.
    /// </summary>
    [Fact]
    public async Task CodexAndClaudeGroupsAreIndependentBindings()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt-1"));
        await relay.StartAsync();
        relay.SetGroup(11);
        relay.SetClaudeGroup(22);

        await PostAsync(relay, "/v1/responses", "{}");
        await PostAsync(relay, "/v1/messages", "{}");
        relay.SetGroup(33);
        await PostAsync(relay, "/v1/messages", "{}");
        await PostAsync(relay, "/v1/responses", "{}");

        Assert.Equal(["11", "22", "22", "33"], upstream.Requests.Select(r => r.Header(LocalPawRelay.GroupHeader)));
    }

    [Fact]
    public async Task AClaudeTurnWithNoClaudeGroupIsRefusedEvenWhenCodexHasOne()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt-1"));
        await relay.StartAsync();
        relay.SetGroup(11);

        using HttpResponseMessage response = await PostAsync(relay, "/v1/messages", "{}");

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        Assert.Empty(upstream.Requests);
    }

    // ---- Which paths are served ------------------------------------------------------

    [Theory]
    [InlineData("/v1/messages")]
    [InlineData("/v1/messages/count_tokens")]
    [InlineData("/v1/responses")]
    public void ThePathsTheRelayServesAreAllowed(string path)
    {
        Assert.Null(LocalPawRelay.Reject("POST", path, "127.0.0.1:1234", null, "Bearer abc123", "abc123", 1234));
    }

    [Theory]
    [InlineData("/v1/models")]
    [InlineData("/v1/chat/completions")]
    [InlineData("/v1/messages/batches")]
    [InlineData("/v1/messages/")]
    [InlineData("/messages")]
    public void EverythingElseIsStillRefused(string path)
    {
        Assert.Equal(404, LocalPawRelay.Reject("POST", path, "127.0.0.1:1234", null, "Bearer abc123", "abc123", 1234)?.Status);
    }

    [Fact]
    public void AMessagesPathIsHeldToTheSameGatesAsTheCodexOne()
    {
        (int, string)? Reject(string? origin = null, string? host = "127.0.0.1:1234", string? auth = "Bearer abc123") =>
            LocalPawRelay.Reject("POST", "/v1/messages", host, origin, auth, "abc123", 1234);

        Assert.Equal(403, Reject(origin: "https://example.com")?.Item1);
        Assert.Equal(403, Reject(host: "evil.example.com")?.Item1);
        Assert.Equal(401, Reject(auth: "Bearer nope")?.Item1);
        Assert.Equal(401, Reject(auth: null)?.Item1);
    }

    // ---- What is forwarded -----------------------------------------------------------

    [Fact]
    public async Task ATurnGoesToThePawMessagesRouteWithItsQueryAndTheGroup()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt-1"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage response = await PostAsync(relay, "/v1/messages?beta=true", "{\"model\":\"claude-opus-5\"}");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Received sent = Assert.Single(upstream.Requests);
        Assert.Equal("/api/v1/paw/messages", sent.Path);

        // Claude Code calls /v1/messages?beta=true. Dropping the query changes which API
        // surface answers, and nothing would report it.
        Assert.Equal("?beta=true", sent.Query);
        Assert.Equal("5", sent.Header("X-Paw-Group-Id"));
        Assert.Equal("Bearer jwt-1", sent.Header("Authorization"));
        Assert.Equal("{\"model\":\"claude-opus-5\"}", sent.Body);
    }

    [Fact]
    public async Task CountTokensGoesToItsOwnPawRoute()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage _ = await PostAsync(relay, "/v1/messages/count_tokens", "{}");

        Assert.Equal("/api/v1/paw/messages/count_tokens", Assert.Single(upstream.Requests).Path);
    }

    [Fact]
    public async Task TheHeadersTheServerReadsAreForwardedAndNothingElseIs()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage _ = await PostAsync(relay, "/v1/messages?beta=true", "{}", headers:
        [
            ("anthropic-version", "2023-06-01"),
            ("anthropic-beta", "claude-code-20250219,interleaved-thinking-2025-05-14"),
            ("x-app", "cli"),
            ("X-Claude-Code-Session-Id", "961a5144-0fb6-45ab-968e-0cbe160e135c"),
            ("Accept", "application/json"),

            // What the SDK and the caller also send, none of which may travel.
            ("X-Stainless-Lang", "js"),
            ("X-Stainless-Runtime-Version", "v26.3.0"),
            ("anthropic-dangerous-direct-browser-access", "true"),
            ("x-api-key", "sk-should-not-travel"),
            ("Cookie", "session=secret"),
        ]);

        Received sent = Assert.Single(upstream.Requests);
        Assert.Equal("2023-06-01", sent.Header("anthropic-version"));
        Assert.Equal("claude-code-20250219,interleaved-thinking-2025-05-14", sent.Header("anthropic-beta"));
        Assert.Equal("cli", sent.Header("x-app"));
        Assert.Equal("961a5144-0fb6-45ab-968e-0cbe160e135c", sent.Header("X-Claude-Code-Session-Id"));

        foreach (string blocked in new[]
                 {
                     "X-Stainless-Lang", "X-Stainless-Runtime-Version",
                     "anthropic-dangerous-direct-browser-access", "x-api-key", "Cookie",
                 })
        {
            Assert.Null(sent.Header(blocked));
        }
    }

    /// <summary>
    /// The account session is bound to the User-Agent it was issued under, and the server
    /// revokes the whole session family on a mismatch. The editor's must therefore never be
    /// the request's own.
    /// </summary>
    [Fact]
    public async Task TheEditorUserAgentTravelsInItsOwnHeaderAndNeverAsTheRequestsOwn()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage _ = await PostAsync(relay, "/v1/messages", "{}", userAgent: EditorUserAgent);

        Received sent = Assert.Single(upstream.Requests);
        Assert.Equal(EditorUserAgent, sent.Header(LocalPawRelay.ClientUserAgentHeader));
        Assert.NotEqual(EditorUserAgent, sent.Header("User-Agent"));
        Assert.DoesNotContain("claude-cli", sent.Header("User-Agent") ?? string.Empty, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task TheLocalTokenNeverReachesTheServer()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage _ = await PostAsync(relay, "/v1/messages", "{}");

        Assert.DoesNotContain(relay.Token, Assert.Single(upstream.Requests).AllHeaders, StringComparison.Ordinal);
    }

    /// <summary>
    /// Claude Code asks for application/json even with stream:true in the body, and a
    /// count_tokens reply is plain JSON. Forcing an event stream would answer both wrongly.
    /// </summary>
    [Fact]
    public async Task TheClientsOwnAcceptIsKeptRatherThanForcedToAnEventStream()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage _ = await PostAsync(relay, "/v1/messages", "{}", headers: [("Accept", "application/json")]);

        Assert.Equal("application/json", Assert.Single(upstream.Requests).Header("Accept"));
    }

    // ---- No group ---------------------------------------------------------------------

    [Fact]
    public async Task ACallWithNoGroupIsRefusedInWordsAndNeverSentUpstream()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();

        // Automatic routing is defined over OpenAI candidates: no group is set.
        using HttpResponseMessage response = await PostAsync(relay, "/v1/messages", "{}");

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        Assert.Empty(upstream.Requests);
        JsonElement error = (await ReadJsonAsync(response)).GetProperty("error");
        Assert.Equal("invalid_request_error", error.GetProperty("type").GetString());
        Assert.Contains("group", error.GetProperty("message").GetString(), StringComparison.OrdinalIgnoreCase);
    }

    // ---- Error shapes -----------------------------------------------------------------

    /// <summary>
    /// Claude Code parses {"type":"error","error":{"type","message"}}. Any other envelope
    /// it prints at the user as raw JSON.
    /// </summary>
    [Fact]
    public async Task ARefusalOnAMessagesPathIsShapedTheWayAnthropicClientsParse()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage response = await PostAsync(relay, "/v1/messages", "{}", token: "not-the-token");

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
        JsonElement body = await ReadJsonAsync(response);
        Assert.Equal("error", body.GetProperty("type").GetString());
        Assert.Equal("authentication_error", body.GetProperty("error").GetProperty("type").GetString());
    }

    [Fact]
    public async Task ARefusalOnTheCodexPathKeepsItsOwnShape()
    {
        await using var upstream = await MessagesUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage response = await PostAsync(relay, "/v1/responses", "{}", token: "not-the-token");

        JsonElement body = await ReadJsonAsync(response);
        Assert.False(body.TryGetProperty("type", out _), "the Codex envelope has no top-level type");
        Assert.Equal("cofly_local_relay", body.GetProperty("error").GetProperty("type").GetString());
    }

    [Theory]
    [InlineData(400, "invalid_request_error")]
    [InlineData(401, "authentication_error")]
    [InlineData(403, "permission_error")]
    [InlineData(404, "not_found_error")]
    [InlineData(413, "request_too_large")]
    [InlineData(429, "rate_limit_error")]
    [InlineData(500, "api_error")]
    [InlineData(502, "api_error")]
    [InlineData(529, "api_error")]
    public void EveryStatusMapsToTheTypeAnthropicUses(int status, string expected)
    {
        Assert.Equal(expected, LocalPawRelay.AnthropicErrorType(status));
    }

    // ---- What comes back --------------------------------------------------------------

    /// <summary>
    /// The SDK decides whether and when to retry from these. Without them a 429 that said
    /// "wait ten seconds" becomes an immediate retry.
    /// </summary>
    [Fact]
    public async Task RetryAndRateLimitHeadersReachTheClientOnAnUpstreamRefusal()
    {
        await using var upstream = await MessagesUpstream.StartAsync(
            status: 429,
            body: "{\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":\"slow down\"}}",
            responseHeaders:
            [
                ("retry-after", "10"),
                ("retry-after-ms", "10000"),
                ("x-should-retry", "true"),
                ("anthropic-ratelimit-requests-remaining", "0"),
                ("request-id", "req_abc"),
                ("Set-Cookie", "tracking=1"),
                ("X-Internal-Debug", "leaks"),
            ]);
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage response = await PostAsync(relay, "/v1/messages", "{}");

        Assert.Equal((HttpStatusCode)429, response.StatusCode);
        Assert.Equal("10", response.Headers.GetValues("retry-after").Single());
        Assert.Equal("10000", response.Headers.GetValues("retry-after-ms").Single());
        Assert.Equal("true", response.Headers.GetValues("x-should-retry").Single());
        Assert.Equal("0", response.Headers.GetValues("anthropic-ratelimit-requests-remaining").Single());
        Assert.Equal("req_abc", response.Headers.GetValues("request-id").Single());
        Assert.False(response.Headers.Contains("Set-Cookie"), "only what the client acts on is passed through");
        Assert.False(response.Headers.Contains("X-Internal-Debug"));

        // The server's own body is passed through untouched: it is already Anthropic-shaped.
        Assert.Equal("slow down", (await ReadJsonAsync(response)).GetProperty("error").GetProperty("message").GetString());
    }

    [Fact]
    public async Task ASuccessfulJsonReplyIsPassedThroughByteForByte()
    {
        const string reply = "{\"id\":\"msg_1\",\"type\":\"message\",\"content\":[{\"type\":\"text\",\"text\":\"hi\"}]}";
        await using var upstream = await MessagesUpstream.StartAsync(body: reply, contentType: "application/json");
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage response = await PostAsync(relay, "/v1/messages", "{}");

        Assert.Equal(reply, await response.Content.ReadAsStringAsync());
        Assert.Equal("application/json", response.Content.Headers.ContentType?.MediaType);
    }

    [Fact]
    public async Task AStreamedReplyKeepsItsEventStreamType()
    {
        const string sse = "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n";
        await using var upstream = await MessagesUpstream.StartAsync(body: sse, contentType: "text/event-stream");
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetClaudeGroup(5);

        using HttpResponseMessage response = await PostAsync(relay, "/v1/messages", "{\"stream\":true}");

        Assert.Equal(sse, await response.Content.ReadAsStringAsync());
        Assert.Equal("text/event-stream", response.Content.Headers.ContentType?.MediaType);
    }

    // ---- Where it listens -------------------------------------------------------------

    [Fact]
    public async Task TheOriginHasNoV1SoAnAnthropicClientDoesNotAppendItTwice()
    {
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"));
        await relay.StartAsync();

        Assert.NotNull(relay.Origin);
        Assert.DoesNotContain("/v1", relay.Origin, StringComparison.Ordinal);
        Assert.StartsWith("http://127.0.0.1:", relay.Origin, StringComparison.Ordinal);
    }

    // ---- A port and token that survive a restart ---------------------------------------

    /// <summary>
    /// An editor is not launched by this client, and reads a configuration that outlives it.
    /// If either value changed on every start, that configuration would point at a dead port
    /// with a dead token after any restart.
    /// </summary>
    [Fact]
    public async Task TheSamePortAndTokenComeBackAfterARestart()
    {
        var store = new InMemoryEndpointStore();

        int firstPort;
        string firstToken;
        await using (var first = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), endpointStore: store))
        {
            await first.StartAsync();
            firstPort = new Uri(first.Origin!).Port;
            firstToken = first.Token;
        }

        await using var second = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), endpointStore: store);
        await second.StartAsync();

        Assert.Equal(firstPort, new Uri(second.Origin!).Port);
        Assert.Equal(firstToken, second.Token);
    }

    [Fact]
    public async Task WhatIsRememberedIsWhatWasActuallyBound()
    {
        var store = new InMemoryEndpointStore();
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), endpointStore: store);
        Assert.Null(store.SavedPort);

        await relay.StartAsync();

        Assert.Equal(new Uri(relay.Origin!).Port, store.SavedPort);
        Assert.Equal(relay.Token, store.SavedToken);
    }

    [Fact]
    public async Task ATakenPortFallsBackToAFreeOneAndIsRememberedInstead()
    {
        int taken;
        using var occupier = new System.Net.Sockets.TcpListener(IPAddress.Loopback, 0);
        occupier.Start();
        taken = ((IPEndPoint)occupier.LocalEndpoint).Port;

        var store = new InMemoryEndpointStore { Port = taken, Token = new string('a', 64) };
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), endpointStore: store);

        await relay.StartAsync();

        int bound = new Uri(relay.Origin!).Port;
        Assert.NotEqual(taken, bound);
        Assert.Equal(bound, store.SavedPort);
        Assert.Equal(new string('a', 64), relay.Token);
    }

    /// <summary>
    /// The token is compared against every request. One edited down to something short
    /// would quietly weaken that, so anything not the shape this class generates is replaced.
    /// </summary>
    [Theory]
    [InlineData("")]
    [InlineData("short")]
    [InlineData("ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ")]
    [InlineData("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")]
    public void ARememberedTokenOfTheWrongShapeIsNotTrusted(string remembered)
    {
        var store = new InMemoryEndpointStore { Token = remembered };

        var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), endpointStore: store);

        Assert.NotEqual(remembered, relay.Token);
        Assert.Equal(64, relay.Token.Length);
    }

    [Theory]
    [InlineData(0)]
    [InlineData(80)]
    [InlineData(1023)]
    [InlineData(70000)]
    [InlineData(-5)]
    public async Task APrivilegedOrImpossibleRememberedPortIsIgnored(int remembered)
    {
        var store = new InMemoryEndpointStore { Port = remembered };
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), endpointStore: store);

        await relay.StartAsync();

        Assert.True(new Uri(relay.Origin!).Port > 1023);
    }

    [Fact]
    public void WithNoStoreTheTokenIsStillFreshEachTime()
    {
        var a = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"));
        var b = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"));

        Assert.NotEqual(a.Token, b.Token);
    }

    // ---- Harness ----------------------------------------------------------------------

    private static async Task<HttpResponseMessage> PostAsync(
        LocalPawRelay relay,
        string pathAndQuery,
        string body,
        IEnumerable<(string Name, string Value)>? headers = null,
        string? token = null,
        string? userAgent = null)
    {
        using var client = new HttpClient();
        using var request = new HttpRequestMessage(HttpMethod.Post, relay.Origin + pathAndQuery)
        {
            Content = new StringContent(body, Encoding.UTF8, "application/json"),
        };
        request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + (token ?? relay.Token));
        if (userAgent is not null)
        {
            request.Headers.TryAddWithoutValidation("User-Agent", userAgent);
        }

        if (headers is not null)
        {
            foreach ((string name, string value) in headers)
            {
                request.Headers.TryAddWithoutValidation(name, value);
            }
        }

        return await client.SendAsync(request);
    }

    private static async Task<JsonElement> ReadJsonAsync(HttpResponseMessage response)
    {
        using JsonDocument document = JsonDocument.Parse(await response.Content.ReadAsStringAsync());
        return document.RootElement.Clone();
    }

    private sealed class InMemoryEndpointStore : IRelayEndpointStore
    {
        public int? Port { get; set; }

        public string? Token { get; set; }

        public int? SavedPort { get; private set; }

        public string? SavedToken { get; private set; }

        public RelayEndpoint Load() => new(Port, Token);

        public void Save(int port, string token)
        {
            SavedPort = port;
            SavedToken = token;
            Port = port;
            Token = token;
        }
    }

    private sealed record Received(string Path, string Query, string Body, string AllHeaders, IReadOnlyDictionary<string, string> Headers)
    {
        public string? Header(string name) =>
            Headers.TryGetValue(name.ToLowerInvariant(), out string? value) ? value : null;
    }

    /// <summary>A stand-in for the server that records exactly what arrived and answers as told.</summary>
    private sealed class MessagesUpstream : IAsyncDisposable
    {
        private readonly HttpListener _listener;
        private readonly CancellationTokenSource _stop = new();
        private readonly Task _serve;
        private readonly int _status;
        private readonly string _body;
        private readonly string _contentType;
        private readonly (string Name, string Value)[] _responseHeaders;

        public List<Received> Requests { get; } = [];

        public string BaseAddress { get; }

        private MessagesUpstream(int status, string body, string contentType, (string, string)[] responseHeaders)
        {
            _listener = LoopbackHttpListener.Start(null, out int port);
            BaseAddress = $"http://127.0.0.1:{port}";
            _status = status;
            _body = body;
            _contentType = contentType;
            _responseHeaders = responseHeaders;
            _serve = ServeAsync(_stop.Token);
        }

        public static Task<MessagesUpstream> StartAsync(
            int status = 200,
            string body = "{\"type\":\"message\"}",
            string contentType = "application/json",
            (string, string)[]? responseHeaders = null)
        {
            return Task.FromResult(new MessagesUpstream(status, body, contentType, responseHeaders ?? []));
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
                var headers = new Dictionary<string, string>();
                var all = new StringBuilder();
                foreach (string? name in context.Request.Headers.AllKeys)
                {
                    if (name is null)
                    {
                        continue;
                    }

                    headers[name.ToLowerInvariant()] = context.Request.Headers[name] ?? string.Empty;
                    all.Append(name).Append(": ").AppendLine(context.Request.Headers[name]);
                }

                lock (Requests)
                {
                    Requests.Add(new Received(
                        context.Request.Url?.AbsolutePath ?? string.Empty,
                        context.Request.Url?.Query ?? string.Empty,
                        body,
                        all.ToString(),
                        headers));
                }

                byte[] payload = Encoding.UTF8.GetBytes(_body);
                context.Response.StatusCode = _status;
                context.Response.ContentType = _contentType;
                foreach ((string name, string value) in _responseHeaders)
                {
                    context.Response.AddHeader(name, value);
                }

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
