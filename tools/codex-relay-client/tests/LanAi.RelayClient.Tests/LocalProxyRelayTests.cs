using System.Collections.Concurrent;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Text;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The relay in local-proxy mode: a tool's traffic goes straight to the "official" API
/// (a loopback stand-in) with the account's token, never to the relay server, and a
/// failure is reported rather than rerouted.
/// </summary>
public sealed class LocalProxyRelayTests
{
    private const string CodexCompleted =
        "event: response.completed\n" +
        "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":7}}}\n\n";

    private static readonly LocalProxyTarget Mine = new(7, "我的 Plus");

    private sealed class Rig : IAsyncDisposable
    {
        private Rig(Upstream server, Upstream official)
        {
            Server = server;
            Official = official;
            Relay = new LocalPawRelay(
                server.BaseAddress,
                _ => Task.FromResult("jwt"),
                localProxyCredentials: Credentials,
                localProxyEndpoints: new LocalProxyEndpoints(official.BaseAddress + "/backend-api/codex/responses", official.BaseAddress),
                onLocalProxyOutcome: Outcomes.Enqueue,
                onLocalProxyUsage: Usage.Enqueue);
        }

        public Upstream Server { get; }

        public Upstream Official { get; }

        public StubCredentials Credentials { get; } = new();

        public LocalPawRelay Relay { get; }

        public ConcurrentQueue<LocalProxyOutcome> Outcomes { get; } = new();

        public ConcurrentQueue<LocalProxyUsage> Usage { get; } = new();

        public static async Task<Rig> StartAsync(params (int Status, string Body)[] officialAnswers)
        {
            var rig = new Rig(
                Upstream.Start([(200, "{}")]),
                Upstream.Start(officialAnswers.Length > 0 ? officialAnswers : [(200, CodexCompleted)]));
            await rig.Relay.StartAsync();
            return rig;
        }

        public async Task<HttpResponseMessage> PostAsync(string pathAndQuery, string body = "{}", params (string, string)[] headers)
        {
            using var client = new HttpClient();
            using var request = new HttpRequestMessage(HttpMethod.Post, Relay.Origin + pathAndQuery)
            {
                Content = new StringContent(body, Encoding.UTF8, "application/json"),
            };
            request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + Relay.Token);
            foreach ((string name, string value) in headers)
            {
                request.Headers.TryAddWithoutValidation(name, value);
            }
            HttpResponseMessage response = await client.SendAsync(request);
            await response.Content.LoadIntoBufferAsync();
            return response;
        }

        public async ValueTask DisposeAsync()
        {
            await Relay.DisposeAsync();
            await Server.DisposeAsync();
            await Official.DisposeAsync();
        }
    }

    [Fact]
    public async Task ACodexTurnGoesToTheOfficialApiWithTheAccountsTokenAndNeverToTheServer()
    {
        await using Rig rig = await Rig.StartAsync();
        rig.Relay.SetGroup(11);
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);

        using HttpResponseMessage response = await rig.PostAsync(
            "/v1/responses", """{"model":"gpt-5.5","store":false,"stream":true}""", ("originator", "codex_cli_rs"));

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Contains("response.completed", await response.Content.ReadAsStringAsync());
        Assert.Empty(rig.Server.Requests);
        Received sent = Assert.Single(rig.Official.Requests);
        Assert.Equal("/backend-api/codex/responses", sent.Path);
        Assert.Equal("Bearer at-7", sent.Header("Authorization"));
        Assert.Equal("acct-7", sent.Header("chatgpt-account-id"));
        Assert.Equal("codex_cli_rs", sent.Header("originator"));
        Assert.Null(sent.Header(LocalPawRelay.GroupHeader));
        Assert.Equal("""{"model":"gpt-5.5","store":false,"stream":true}""", sent.Body);
    }

    [Fact]
    public async Task UsageAndSuccessAreReported()
    {
        await using Rig rig = await Rig.StartAsync();
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);

        (await rig.PostAsync("/v1/responses")).Dispose();

        LocalProxyUsage usage = Assert.Single(rig.Usage);
        Assert.Equal(new LocalProxyUsage(LocalProxyKind.Codex, 7, 100, 7, 0), usage);
        Assert.True(Assert.Single(rig.Outcomes).Succeeded);
    }

    [Fact]
    public async Task SwitchingBackSendsTheNextTurnToTheServerAgain()
    {
        await using Rig rig = await Rig.StartAsync();
        rig.Relay.SetGroup(11);
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);
        (await rig.PostAsync("/v1/responses")).Dispose();

        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, null);
        (await rig.PostAsync("/v1/responses")).Dispose();

        Assert.Single(rig.Official.Requests);
        Received toServer = Assert.Single(rig.Server.Requests);
        Assert.Equal("11", toServer.Header(LocalPawRelay.GroupHeader));
    }

    [Fact]
    public async Task CodexAndClaudeCodeSwitchIndependently()
    {
        await using Rig rig = await Rig.StartAsync((200, "{}"), (200, "{}"));
        rig.Relay.SetClaudeGroup(22);
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);

        (await rig.PostAsync("/v1/messages?beta=true")).Dispose();
        (await rig.PostAsync("/v1/responses")).Dispose();

        Assert.Equal("/api/v1/paw/messages", Assert.Single(rig.Server.Requests).Path);
        Assert.Equal("/backend-api/codex/responses", Assert.Single(rig.Official.Requests).Path);
    }

    [Fact]
    public async Task ClaudeCodeGoesStraightToAnthropicWithoutNeedingAGroup()
    {
        await using Rig rig = await Rig.StartAsync((200, "{\"type\":\"message\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}"));
        rig.Relay.SetLocalProxy(LocalProxyKind.ClaudeCode, new LocalProxyTarget(8, "Max"));

        using HttpResponseMessage response = await rig.PostAsync(
            "/v1/messages?beta=true", "{}", ("anthropic-beta", "claude-code-20250219"), ("User-Agent", "claude-cli/2.1.258 (external, claude-vscode)"));

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Received sent = Assert.Single(rig.Official.Requests);
        Assert.Equal("/v1/messages", sent.Path);
        Assert.Equal("?beta=true", sent.Query);
        Assert.Equal("Bearer at-8", sent.Header("Authorization"));
        Assert.Equal("claude-code-20250219,oauth-2025-04-20", sent.Header("anthropic-beta"));
        Assert.Equal("claude-cli/2.1.258 (external, claude-vscode)", sent.Header("User-Agent"));
        Assert.Null(sent.Header("chatgpt-account-id"));
        Assert.Equal(new LocalProxyUsage(LocalProxyKind.ClaudeCode, 8, 3, 4, 0), Assert.Single(rig.Usage));
    }

    [Fact]
    public async Task AnOfficial401IsRetriedOnceWithAFreshToken()
    {
        await using Rig rig = await Rig.StartAsync((401, "{\"error\":\"expired\"}"), (200, CodexCompleted));
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);

        using HttpResponseMessage response = await rig.PostAsync("/v1/responses");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal([false, true], rig.Credentials.ForceRefreshes);
        Assert.Equal(["Bearer at-7", "Bearer at-7-fresh"], rig.Official.Requests.Select(r => r.Header("Authorization")));
    }

    [Fact]
    public async Task AFailureIsPassedThroughAndReportedButNeverReroutedToTheServer()
    {
        await using Rig rig = await Rig.StartAsync((429, "{\"error\":{\"type\":\"usage_limit_reached\"}}"));
        rig.Relay.SetGroup(11);
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);

        using HttpResponseMessage response = await rig.PostAsync("/v1/responses");

        Assert.Equal((HttpStatusCode)429, response.StatusCode);
        Assert.Contains("usage_limit_reached", await response.Content.ReadAsStringAsync());
        Assert.Empty(rig.Server.Requests);
        LocalProxyOutcome outcome = Assert.Single(rig.Outcomes);
        Assert.False(outcome.Succeeded);
        Assert.Contains("429", outcome.Message);
    }

    [Fact]
    public async Task ACredentialTheServerWillNotIssueIsReported()
    {
        await using Rig rig = await Rig.StartAsync();
        rig.Credentials.Fail = new RelayApiException(RelayFailure.Forbidden, "not yours");
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);

        using HttpResponseMessage response = await rig.PostAsync("/v1/responses");

        Assert.Equal(HttpStatusCode.BadGateway, response.StatusCode);
        Assert.Empty(rig.Official.Requests);
        Assert.Empty(rig.Server.Requests);
        Assert.False(Assert.Single(rig.Outcomes).Succeeded);
    }

    [Fact]
    public async Task CompactIsServedOnlyByTheLocalProxy()
    {
        await using Rig rig = await Rig.StartAsync((200, "{\"output\":[]}"));
        rig.Relay.SetGroup(11);

        using HttpResponseMessage refused = await rig.PostAsync("/v1/responses/compact");
        Assert.Equal(HttpStatusCode.NotFound, refused.StatusCode);
        Assert.Empty(rig.Server.Requests);

        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);
        using HttpResponseMessage served = await rig.PostAsync("/v1/responses/compact", """{"model":"m","store":false,"stream":true}""");

        Assert.Equal(HttpStatusCode.OK, served.StatusCode);
        Received sent = Assert.Single(rig.Official.Requests);
        Assert.Equal("/backend-api/codex/responses/compact", sent.Path);
        Assert.Equal("application/json", sent.Header("Accept"));
        Assert.Equal("""{"model":"m"}""", sent.Body);
    }

    [Fact]
    public async Task StoppingForgetsTheLocalProxyAndItsTokens()
    {
        await using Rig rig = await Rig.StartAsync();
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);

        await rig.Relay.StopAsync();
        await rig.Relay.StartAsync();
        rig.Relay.SetGroup(11);
        (await rig.PostAsync("/v1/responses")).Dispose();

        Assert.True(rig.Credentials.Cleared);
        Assert.Empty(rig.Official.Requests);
        Assert.Single(rig.Server.Requests);
    }

    // ---- Harness ----------------------------------------------------------------------

    internal sealed class StubCredentials : ILocalProxyCredentialSource
    {
        public List<bool> ForceRefreshes { get; } = [];

        public RelayApiException? Fail { get; set; }

        public bool Cleared { get; private set; }

        public Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken)
        {
            lock (ForceRefreshes)
            {
                ForceRefreshes.Add(forceRefresh);
            }
            if (Fail is not null)
            {
                return Task.FromException<LocalProxyCredential>(Fail);
            }
            return Task.FromResult(new LocalProxyCredential(
                accountId: accountId,
                accessToken: $"at-{accountId}" + (forceRefresh ? "-fresh" : string.Empty),
                chatgptAccountId: $"acct-{accountId}"));
        }

        public void Clear() => Cleared = true;
    }

    private const string InvalidEncrypted =
        """{"error":{"message":"The encrypted content gAAA could not be verified.","type":"invalid_request_error","code":"invalid_encrypted_content"}}""";

    private const string TurnFromTheServer =
        """{"model":"gpt-5.5","store":false,"stream":true,"input":[{"type":"message","role":"user","content":"hi"},{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"FROM-SERVER"},{"type":"message","role":"user","content":"again"}]}""";

    [Fact]
    public async Task HistoryFromAnotherAccountIsStrippedAndTheTurnRetriedOnce()
    {
        await using Rig rig = await Rig.StartAsync((400, InvalidEncrypted), (200, CodexCompleted));
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);

        using HttpResponseMessage response = await rig.PostAsync("/v1/responses", TurnFromTheServer);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal(2, rig.Official.Requests.Count);
        string retried = rig.Official.Requests.ToArray()[1].Body;
        Assert.DoesNotContain("FROM-SERVER", retried);
        Assert.Contains("\"rs_1\"", retried);
        Assert.Contains("again", retried);
        Assert.True(Assert.Single(rig.Outcomes).Succeeded);
    }

    [Fact]
    public async Task ARefusedItemIsStrippedUpFrontOnLaterTurnsButNewOnesAreKept()
    {
        await using Rig rig = await Rig.StartAsync((400, InvalidEncrypted), (200, CodexCompleted));
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);
        (await rig.PostAsync("/v1/responses", TurnFromTheServer)).Dispose();

        string nextTurn = TurnFromTheServer.Replace(
            "]}", "," + """{"type":"reasoning","id":"rs_2","summary":[],"encrypted_content":"OWN"}]}""");
        (await rig.PostAsync("/v1/responses", nextTurn)).Dispose();

        Assert.Equal(3, rig.Official.Requests.Count);
        string third = rig.Official.Requests.ToArray()[2].Body;
        Assert.DoesNotContain("FROM-SERVER", third);
        Assert.Contains("OWN", third);
    }

    [Fact]
    public async Task AnyOther400IsPassedThroughWithoutRetrying()
    {
        const string Other = """{"error":{"message":"Unsupported parameter: foo","code":"unsupported_parameter"}}""";
        await using Rig rig = await Rig.StartAsync((400, Other), (200, CodexCompleted));
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);

        using HttpResponseMessage response = await rig.PostAsync("/v1/responses", TurnFromTheServer);

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        Assert.Contains("unsupported_parameter", await response.Content.ReadAsStringAsync());
        Assert.Single(rig.Official.Requests);
        Assert.False(Assert.Single(rig.Outcomes).Succeeded);
    }

    [Fact]
    public async Task StillRefusedAfterStrippingIsPassedThroughAfterOneRetry()
    {
        await using Rig rig = await Rig.StartAsync((400, InvalidEncrypted));
        rig.Relay.SetLocalProxy(LocalProxyKind.Codex, Mine);

        using HttpResponseMessage response = await rig.PostAsync("/v1/responses", TurnFromTheServer);

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        Assert.Contains("invalid_encrypted_content", await response.Content.ReadAsStringAsync());
        Assert.Equal(2, rig.Official.Requests.Count);
    }

    internal sealed record Received(string Path, string Query, string Body, IReadOnlyDictionary<string, string> Headers)
    {
        public string? Header(string name) =>
            Headers.TryGetValue(name.ToLowerInvariant(), out string? value) ? value : null;
    }

    /// <summary>Records what arrived and answers each request with the next scripted reply (the last repeats).</summary>
    internal sealed class Upstream : IAsyncDisposable
    {
        private readonly HttpListener _listener = new();
        private readonly CancellationTokenSource _stop = new();
        private readonly Task _serve;
        private readonly (int Status, string Body)[] _answers;
        private int _next;

        private Upstream(int port, (int, string)[] answers)
        {
            BaseAddress = $"http://127.0.0.1:{port}";
            _answers = answers;
            _listener.Prefixes.Add($"http://127.0.0.1:{port}/");
            _listener.Start();
            _serve = ServeAsync(_stop.Token);
        }

        public List<Received> Requests { get; } = [];

        public string BaseAddress { get; }

        public static Upstream Start((int, string)[] answers)
        {
            using var probe = new System.Net.Sockets.TcpListener(IPAddress.Loopback, 0);
            probe.Start();
            int port = ((IPEndPoint)probe.LocalEndpoint).Port;
            probe.Stop();
            return new Upstream(port, answers);
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
                foreach (string? name in context.Request.Headers.AllKeys)
                {
                    if (name is not null)
                    {
                        headers[name.ToLowerInvariant()] = context.Request.Headers[name] ?? string.Empty;
                    }
                }

                (int status, string answer) = _answers[Math.Min(_next++, _answers.Length - 1)];
                lock (Requests)
                {
                    Requests.Add(new Received(
                        context.Request.Url?.AbsolutePath ?? string.Empty,
                        context.Request.Url?.Query ?? string.Empty,
                        body,
                        headers));
                }

                byte[] payload = Encoding.UTF8.GetBytes(answer);
                context.Response.StatusCode = status;
                context.Response.ContentType = answer.StartsWith("event:", StringComparison.Ordinal)
                    ? "text/event-stream"
                    : "application/json";
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
