using System.Net;
using System.Net.Http;
using System.Text;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The relay assignment: which address and credential each forwarded request carries when the server may have
/// assigned this user to a relay node. Driven through real sockets for the relay itself and a scripted handler for
/// the assignment client, so what is asserted is what actually goes out on the wire.
/// </summary>
public sealed class RelayAssignmentTests
{
    private const string Server = "http://server.test";

    // ---- The assignment client -------------------------------------------------------------

    private static string RelayAnswer(string baseUrl, string ticket, long node = 12, int refresh = 60, string? expires = null) =>
        "{\"role\":\"relay\",\"node_id\":" + node + ",\"base_url\":\"" + baseUrl + "\",\"ticket\":\"" + ticket + "\",\"ticket_expires_at\":\"" +
        (expires ?? DateTimeOffset.UtcNow.AddMinutes(10).ToString("o")) + "\",\"refresh_after\":" + refresh + "}";

    private sealed class ScriptedServer : HttpMessageHandler
    {
        public Queue<(int Status, string Body)> Answers { get; } = new();
        public List<(string Method, string Path, string? Authorization, string Body)> Requests { get; } = [];
        public Exception? Failure { get; set; }

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            string body = request.Content is null ? string.Empty : await request.Content.ReadAsStringAsync(cancellationToken);
            Requests.Add((request.Method.Method, request.RequestUri!.AbsolutePath, request.Headers.Authorization?.ToString(), body));
            if (Failure is not null)
            {
                throw Failure;
            }

            (int status, string answer) = Answers.Count > 1 ? Answers.Dequeue() : Answers.Peek();
            return new HttpResponseMessage((HttpStatusCode)status) { Content = new StringContent(answer, Encoding.UTF8, "application/json") };
        }
    }

    private sealed class Clock : TimeProvider
    {
        public DateTimeOffset Now { get; set; } = new(2026, 10, 6, 12, 0, 0, TimeSpan.Zero);

        public override DateTimeOffset GetUtcNow() => Now;
    }

    private static RelayAssignmentClient NewClient(ScriptedServer server, Clock clock, Func<CancellationToken, Task<string>>? jwt = null) =>
        new(new HttpClient(server), Server, jwt ?? (_ => Task.FromResult("jwt-1")), clock);

    [Fact]
    public async Task ARelayAssignmentSendsTheTicketToTheNodeAndNeverTheAccountSession()
    {
        var server = new ScriptedServer();
        server.Answers.Enqueue((200, RelayAnswer("https://r1.example.com/", "srt1.abc")));
        RelayAssignmentClient client = NewClient(server, new Clock());

        RelayTarget target = await client.GetTargetAsync(CancellationToken.None);

        Assert.True(target.IsRelay);
        Assert.Equal("https://r1.example.com", target.BaseUrl);
        Assert.Equal("srt1.abc", target.Bearer);
        Assert.Equal(12, target.NodeId);
        // The assignment call itself carries the account session, to the server only.
        Assert.Equal(("GET", "/api/v1/paw/relay/assignment", "Bearer jwt-1", string.Empty), server.Requests.Single());
    }

    [Fact]
    public async Task VmRelayTestUsesTheLocalNodeAddressWithTheAssignedTicket()
    {
        var server = new ScriptedServer();
        server.Answers.Enqueue((200, RelayAnswer("https://relay1.localhost", "srt1.local")));
        var client = new RelayAssignmentClient(new HttpClient(server), Server,
            _ => Task.FromResult("jwt-1"), new Clock(), "http://192.168.202.1:18081");

        RelayTarget target = await client.GetTargetAsync(CancellationToken.None);

        Assert.Equal("http://192.168.202.1:18081", target.BaseUrl);
        Assert.Equal("srt1.local", target.Bearer);
        Assert.Equal(12, target.NodeId);
        Assert.Equal(("GET", "/api/v1/paw/relay/assignment", "Bearer jwt-1", string.Empty), server.Requests.Single());
    }

    [Fact]
    public async Task AMasterAssignmentKeepsTheServersOwnAddressAndTheSessionWhateverTheAnswerSays()
    {
        // The session may only ever travel to the address compiled into the client.
        var server = new ScriptedServer();
        server.Answers.Enqueue((200, "{\"role\":\"master\",\"node_id\":0,\"base_url\":\"https://evil.example.com\",\"refresh_after\":60}"));
        RelayAssignmentClient client = NewClient(server, new Clock());

        RelayTarget target = await client.GetTargetAsync(CancellationToken.None);

        Assert.False(target.IsRelay);
        Assert.Equal(Server, target.BaseUrl);
        Assert.Equal("jwt-1", target.Bearer);
    }

    [Fact]
    public async Task AServerWithoutRelayAnswers404AndIsUsedAsBefore()
    {
        var server = new ScriptedServer();
        server.Answers.Enqueue((404, "{\"error\":{\"code\":\"RELAY_NOT_ENABLED\"}}"));
        var clock = new Clock();
        RelayAssignmentClient client = NewClient(server, clock);

        RelayTarget target = await client.GetTargetAsync(CancellationToken.None);
        Assert.False(target.IsRelay);
        Assert.Equal(Server, target.BaseUrl);

        // Not asked again on every request: the answer is trusted for a few minutes.
        await client.GetTargetAsync(CancellationToken.None);
        Assert.Single(server.Requests);
        clock.Now += TimeSpan.FromMinutes(6);
        await client.GetTargetAsync(CancellationToken.None);
        Assert.Equal(2, server.Requests.Count);
    }

    [Fact]
    public async Task TheAssignmentIsKeptUntilRefreshAfterAndTheTicketIsRenewedBeforeItExpires()
    {
        var server = new ScriptedServer();
        var clock = new Clock();
        server.Answers.Enqueue((200, RelayAnswer("https://r1.example.com", "ticket-1", refresh: 60, expires: clock.Now.AddMinutes(10).ToString("o"))));
        RelayAssignmentClient client = NewClient(server, clock);

        await client.GetTargetAsync(CancellationToken.None);
        clock.Now += TimeSpan.FromSeconds(30);
        Assert.Equal("ticket-1", (await client.GetTargetAsync(CancellationToken.None)).Bearer);
        Assert.Single(server.Requests);

        // refresh_after elapsed: asked again, and the new ticket is used.
        server.Answers.Clear();
        server.Answers.Enqueue((200, RelayAnswer("https://r1.example.com", "ticket-2", expires: clock.Now.AddMinutes(10).ToString("o"))));
        clock.Now += TimeSpan.FromSeconds(40);
        Assert.Equal("ticket-2", (await client.GetTargetAsync(CancellationToken.None)).Bearer);
        Assert.Equal(2, server.Requests.Count);

        // A ticket close to expiry is renewed even before refresh_after.
        server.Answers.Clear();
        server.Answers.Enqueue((200, RelayAnswer("https://r1.example.com", "ticket-3", refresh: 3600, expires: clock.Now.AddMinutes(10).ToString("o"))));
        clock.Now += TimeSpan.FromMinutes(9).Add(TimeSpan.FromSeconds(30));
        Assert.Equal("ticket-3", (await client.GetTargetAsync(CancellationToken.None)).Bearer);
    }

    [Fact]
    public async Task AnotherSessionNeverInheritsTheCachedTicket()
    {
        // Another user signed in (or the token rotated): the cached ticket may belong to someone else.
        var server = new ScriptedServer();
        server.Answers.Enqueue((200, RelayAnswer("https://r1.example.com", "ticket-of-user-a")));
        string jwt = "jwt-a";
        RelayAssignmentClient client = NewClient(server, new Clock(), _ => Task.FromResult(jwt));
        await client.GetTargetAsync(CancellationToken.None);

        server.Answers.Clear();
        server.Answers.Enqueue((200, RelayAnswer("https://r2.example.com", "ticket-of-user-b", node: 13)));
        jwt = "jwt-b";
        RelayTarget target = await client.GetTargetAsync(CancellationToken.None);

        Assert.Equal("ticket-of-user-b", target.Bearer);
        Assert.Equal(("GET", "/api/v1/paw/relay/assignment", "Bearer jwt-b", string.Empty), server.Requests.Last());
    }

    [Fact]
    public async Task NoRelayAvailableIsReportedAndNeverFallsBackToTheServerSilently()
    {
        var server = new ScriptedServer();
        server.Answers.Enqueue((503, "{\"error\":{\"code\":\"RELAY_UNAVAILABLE\",\"message\":\"no relay\"}}"));
        RelayAssignmentClient client = NewClient(server, new Clock());

        await Assert.ThrowsAsync<RelayUnavailableException>(() => client.GetTargetAsync(CancellationToken.None));
    }

    [Fact]
    public async Task AnUnreachableAssignmentEndpointKeepsAValidTicketOtherwiseUsesTheServer()
    {
        var server = new ScriptedServer();
        var clock = new Clock();
        server.Answers.Enqueue((200, RelayAnswer("https://r1.example.com", "ticket-1", refresh: 30, expires: clock.Now.AddMinutes(10).ToString("o"))));
        RelayAssignmentClient client = NewClient(server, clock);
        await client.GetTargetAsync(CancellationToken.None);

        // refresh due, endpoint down: the still-valid ticket keeps working.
        server.Failure = new HttpRequestException("connection refused");
        clock.Now += TimeSpan.FromSeconds(31);
        RelayTarget target = await client.GetTargetAsync(CancellationToken.None);
        Assert.True(target.IsRelay);
        Assert.Equal("ticket-1", target.Bearer);

        // ticket nearly expired and endpoint still down: the server forwards itself, as before.
        clock.Now += TimeSpan.FromMinutes(9).Add(TimeSpan.FromSeconds(45));
        target = await client.GetTargetAsync(CancellationToken.None);
        Assert.False(target.IsRelay);
        Assert.Equal(Server, target.BaseUrl);
    }

    [Fact]
    public async Task MalformedAnswersAreIgnored()
    {
        var server = new ScriptedServer();
        server.Answers.Enqueue((200, "{\"role\":\"relay\",\"base_url\":\"ftp://x\",\"ticket\":\"t\"}"));
        RelayAssignmentClient client = NewClient(server, new Clock());

        RelayTarget target = await client.GetTargetAsync(CancellationToken.None);

        Assert.False(target.IsRelay);
        Assert.Equal(Server, target.BaseUrl);
    }

    [Fact]
    public async Task ReportingAnUnreachableNodePostsItsIdAndMovesToTheNewAssignment()
    {
        var server = new ScriptedServer();
        server.Answers.Enqueue((200, RelayAnswer("https://r1.example.com", "ticket-1", node: 12)));
        RelayAssignmentClient client = NewClient(server, new Clock());
        RelayTarget first = await client.GetTargetAsync(CancellationToken.None);

        server.Answers.Clear();
        server.Answers.Enqueue((200, RelayAnswer("https://r2.example.com", "ticket-2", node: 13)));
        RelayTarget? next = await client.ReportUnreachableAsync(first, CancellationToken.None);

        Assert.NotNull(next);
        Assert.Equal(13, next.NodeId);
        Assert.Equal("ticket-2", next.Bearer);
        Assert.Equal(("POST", "/api/v1/paw/relay/assignment", "Bearer jwt-1", "{\"unreachable_node_id\":12}"), server.Requests.Last());

        // Nothing else to move to: no target.
        server.Answers.Clear();
        server.Answers.Enqueue((503, "{\"error\":{\"code\":\"RELAY_UNAVAILABLE\"}}"));
        Assert.Null(await client.ReportUnreachableAsync(next, CancellationToken.None));
    }

    [Fact]
    public async Task ARefusedTicketIsRefreshedOnceAndAnUnchangedAnswerIsNotRetried()
    {
        var server = new ScriptedServer();
        server.Answers.Enqueue((200, RelayAnswer("https://r1.example.com", "ticket-1")));
        RelayAssignmentClient client = NewClient(server, new Clock());
        RelayTarget first = await client.GetTargetAsync(CancellationToken.None);

        server.Answers.Clear();
        server.Answers.Enqueue((200, RelayAnswer("https://r1.example.com", "ticket-2")));
        RelayTarget? renewed = await client.RefreshAsync(first, CancellationToken.None);
        Assert.Equal("ticket-2", renewed?.Bearer);

        // Same ticket again: nothing changed, so the caller gives up instead of resending the same thing.
        server.Answers.Clear();
        server.Answers.Enqueue((200, RelayAnswer("https://r1.example.com", "ticket-2")));
        int before = server.Requests.Count;
        Assert.Null(await client.RefreshAsync(renewed!, CancellationToken.None));
        Assert.Equal(before + 1, server.Requests.Count);
    }

    // ---- The relay, end to end through real sockets ------------------------------------------

    private sealed class FakeNode : IAsyncDisposable
    {
        private readonly HttpListener _listener;
        private readonly CancellationTokenSource _stop = new();
        private readonly Task _serve;

        public List<(string Path, string? Authorization)> Requests { get; } = [];

        public int Status { get; set; } = 200;

        public string BaseAddress { get; }

        public FakeNode()
        {
            _listener = LoopbackHttpListener.Start(null, out int port);
            BaseAddress = $"http://127.0.0.1:{port}";
            _serve = ServeAsync();
        }

        private async Task ServeAsync()
        {
            while (!_stop.IsCancellationRequested)
            {
                HttpListenerContext context;
                try { context = await _listener.GetContextAsync().WaitAsync(_stop.Token); }
                catch (Exception) { break; }

                using (var reader = new StreamReader(context.Request.InputStream, Encoding.UTF8))
                {
                    _ = await reader.ReadToEndAsync();
                }

                lock (Requests)
                {
                    Requests.Add((context.Request.Url?.AbsolutePath ?? string.Empty, context.Request.Headers["Authorization"]));
                }

                context.Response.StatusCode = Status;
                context.Response.ContentType = "text/event-stream";
                byte[] payload = Encoding.UTF8.GetBytes(Status == 200 ? "data: {\"type\":\"response.completed\"}\n\n" : "{\"error\":{\"code\":\"TICKET_EXPIRED\"}}");
                await context.Response.OutputStream.WriteAsync(payload);
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

    private sealed class ScriptedTargets : IRelayTargetProvider
    {
        public RelayTarget Current { get; set; } = new("http://unused", "t", true, 1);

        public Queue<RelayTarget?> OnUnreachable { get; } = new();

        public Queue<RelayTarget?> OnRefresh { get; } = new();

        public bool Unavailable { get; set; }

        public int Reported { get; private set; }

        public int Refreshed { get; private set; }

        public Task<RelayTarget> GetTargetAsync(CancellationToken cancellationToken) =>
            Unavailable ? throw new RelayUnavailableException("none") : Task.FromResult(Current);

        public Task<RelayTarget?> ReportUnreachableAsync(RelayTarget failed, CancellationToken cancellationToken)
        {
            Reported++;
            return Task.FromResult(OnUnreachable.Count > 0 ? OnUnreachable.Dequeue() : null);
        }

        public Task<RelayTarget?> RefreshAsync(RelayTarget rejected, CancellationToken cancellationToken)
        {
            Refreshed++;
            return Task.FromResult(OnRefresh.Count > 0 ? OnRefresh.Dequeue() : null);
        }
    }

    private static async Task<HttpResponseMessage> PostAsync(LocalPawRelay relay)
    {
        using var client = new HttpClient();
        using var request = new HttpRequestMessage(HttpMethod.Post, new Uri(relay.BaseAddress!, "responses"))
        {
            Content = new StringContent("{}", Encoding.UTF8, "application/json"),
        };
        request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + relay.Token);
        return await client.SendAsync(request);
    }

    private static int UnusedPort()
    {
        using var probe = new System.Net.Sockets.TcpListener(IPAddress.Loopback, 0);
        probe.Start();
        return ((IPEndPoint)probe.LocalEndpoint).Port;
    }

    [Fact]
    public async Task ARequestToAnAssignedNodeCarriesTheTicketAndTheNodesAddress()
    {
        await using var node = new FakeNode();
        var targets = new ScriptedTargets { Current = new RelayTarget(node.BaseAddress, "srt1.ticket", true, 12) };
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt-should-not-leak"), relayTargets: targets);
        await relay.StartAsync();
        relay.SetGroup(7);

        HttpResponseMessage response = await PostAsync(relay);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var sent = Assert.Single(node.Requests);
        Assert.Equal("/api/v1/paw/responses", sent.Path);
        Assert.Equal("Bearer srt1.ticket", sent.Authorization);
    }

    [Fact]
    public async Task AnUnreachableNodeIsReportedAndTheRequestGoesToTheNewOneOnce()
    {
        await using var good = new FakeNode();
        var targets = new ScriptedTargets { Current = new RelayTarget($"http://127.0.0.1:{UnusedPort()}", "ticket-dead", true, 12) };
        targets.OnUnreachable.Enqueue(new RelayTarget(good.BaseAddress, "ticket-good", true, 13));
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), relayTargets: targets);
        await relay.StartAsync();
        relay.SetGroup(7);

        HttpResponseMessage response = await PostAsync(relay);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal(1, targets.Reported);
        Assert.Equal("Bearer ticket-good", Assert.Single(good.Requests).Authorization);
    }

    [Fact]
    public async Task WhenNoOtherNodeIsAvailableTheToolIsToldTheServiceIsUnavailable()
    {
        var targets = new ScriptedTargets { Current = new RelayTarget($"http://127.0.0.1:{UnusedPort()}", "ticket-dead", true, 12) };
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), relayTargets: targets);
        await relay.StartAsync();
        relay.SetGroup(7);

        HttpResponseMessage response = await PostAsync(relay);

        Assert.Equal(HttpStatusCode.ServiceUnavailable, response.StatusCode);
        Assert.Equal(1, targets.Reported);
    }

    [Fact]
    public async Task NoRelayAtAllIsAnAnswerNotAHang()
    {
        var targets = new ScriptedTargets { Unavailable = true };
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), relayTargets: targets);
        await relay.StartAsync();
        relay.SetGroup(7);

        HttpResponseMessage response = await PostAsync(relay);

        Assert.Equal(HttpStatusCode.ServiceUnavailable, response.StatusCode);
    }

    [Fact]
    public async Task ARefusedTicketIsRenewedAndTheRequestSentOnceMore()
    {
        await using var node = new FakeNode { Status = 401 };
        var targets = new ScriptedTargets { Current = new RelayTarget(node.BaseAddress, "ticket-old", true, 12) };
        targets.OnRefresh.Enqueue(new RelayTarget(node.BaseAddress, "ticket-new", true, 12));
        await using var relay = new LocalPawRelay("http://127.0.0.1:1", _ => Task.FromResult("jwt"), relayTargets: targets);
        await relay.StartAsync();
        relay.SetGroup(7);

        HttpResponseMessage response = await PostAsync(relay);

        // The node keeps answering 401 (it is a stub), so the tool sees it - but only after exactly one retry.
        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
        Assert.Equal(1, targets.Refreshed);
        Assert.Equal(["Bearer ticket-old", "Bearer ticket-new"], node.Requests.Select(r => r.Authorization));
    }

    [Fact]
    public async Task WithoutAnAssignmentProviderTheRelayBehavesAsBefore()
    {
        await using var node = new FakeNode();
        await using var relay = new LocalPawRelay(node.BaseAddress, _ => Task.FromResult("jwt-1"));
        await relay.StartAsync();
        relay.SetGroup(7);

        HttpResponseMessage response = await PostAsync(relay);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal("Bearer jwt-1", Assert.Single(node.Requests).Authorization);
    }
}
