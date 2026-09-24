using System.Net;
using System.Net.WebSockets;
using System.Text;
using System.Text.Json;
using System.Threading.Channels;
using LanAi.RelayClient.CodexBinding.DesktopSync;
using LanAi.RelayClient.DesktopSync;
using Xunit;

namespace LanAi.RelayClient.Tests.DesktopSync;

/// <summary>
/// The assistant's socket against a real WebSocket server on this machine, speaking the
/// frames of the backend's remote_hub.go.
/// </summary>
public sealed class DesktopSyncLinkTests : IAsyncDisposable
{
    private readonly FakeRemoteServer _server = new();
    private readonly SyncAuditLog _audit = new(persist: false);
    private readonly DesktopSyncAgent _agent;
    private readonly DesktopSyncLink _link;
    private int _tokensIssued;

    public DesktopSyncLinkTests()
    {
        _agent = new DesktopSyncAgent(new IdleTools(), new SessionContentSync(_ => null), _ => null, new MemoryStore(), _audit);
        _link = new DesktopSyncLink(
            new Uri(_server.BaseAddress),
            "pc-abc12345",
            "办公室电脑",
            _ => Task.FromResult($"token-{Interlocked.Increment(ref _tokensIssued)}"),
            _agent,
            _ => Task.FromResult("""{"desktop_running":true}"""u8.ToArray()));
    }

    public async ValueTask DisposeAsync()
    {
        await _link.DisposeAsync();
        _agent.Dispose();
        await _server.DisposeAsync();
    }

    [Fact]
    public async Task ItConnectsWithTheAccountTokenAndSaysHello()
    {
        _link.Start();
        FakeRemoteServer.Connection connection = await _server.NextConnection();

        Assert.Equal("Bearer token-1", connection.Authorization);
        Assert.Equal("pc-abc12345", connection.DeviceId);
        Assert.Null(connection.UserAgent);
        JsonElement hello = await connection.Receive();
        Assert.Equal("hello", hello.GetProperty("kind").GetString());
        Assert.True(hello.GetProperty("body").GetProperty("desktop_running").GetBoolean());
    }

    /// <summary>The answer carries the command's id, and the agent's refusal verbatim.</summary>
    [Fact]
    public async Task ACommandIsAnsweredUnderItsId()
    {
        _link.Start();
        FakeRemoteServer.Connection connection = await _server.NextConnection();
        await connection.Receive();

        await connection.Send("""{"kind":"cmd","id":"r7","pairing_id":5,"body":{"type":"sessions.list"}}""");
        JsonElement result = await connection.Receive();

        Assert.Equal("result", result.GetProperty("kind").GetString());
        Assert.Equal("r7", result.GetProperty("id").GetString());
        Assert.Equal("disabled", result.GetProperty("body").GetProperty("error").GetString());
    }

    [Fact]
    public async Task APairingRequestIsRaisedWithAFingerprint()
    {
        var raised = new TaskCompletionSource<PairingRequest>(TaskCreationOptions.RunContinuationsAsynchronously);
        _link.PairingRequested += request => raised.TrySetResult(request);
        _link.Start();
        FakeRemoteServer.Connection connection = await _server.NextConnection();
        await connection.Receive();

        await connection.Send("""{"kind":"event","type":"pair.request","body":{"pairing_id":9,"phone_label":"iPhone","public_key":"AAAA"}}""");
        PairingRequest request = await raised.Task.WaitAsync(TimeSpan.FromSeconds(5));

        Assert.Equal(9, request.PairingId);
        Assert.Equal(DesktopSyncLink.Fingerprint("AAAA"), request.Fingerprint);
        Assert.Matches("^[0-9A-F]{3} [0-9A-F]{3}$", request.Fingerprint);
    }

    /// <summary>Approving keeps the key here before the server hears about it.</summary>
    [Fact]
    public async Task ApprovingTrustsTheKeyLocallyAndConfirms()
    {
        _link.Start();
        FakeRemoteServer.Connection connection = await _server.NextConnection();
        await connection.Receive();

        await _link.ApproveAsync(new PairingRequest(9, "iPhone", "AAAA", "000 000"));
        JsonElement confirm = await connection.Receive();

        Assert.Equal("pair.confirm", confirm.GetProperty("kind").GetString());
        Assert.Equal(9, confirm.GetProperty("pairing_id").GetInt64());
        Assert.Equal("AAAA", Assert.Single(_agent.State.Phones).PublicKey);
    }

    [Fact]
    public async Task ARevokedPairingIsForgottenHere()
    {
        _agent.ApprovePhone(9, "iPhone", "AAAA");
        _link.Start();
        FakeRemoteServer.Connection connection = await _server.NextConnection();
        await connection.Receive();

        await connection.Send("""{"kind":"event","type":"pair.revoked","body":{"pairing_id":9}}""");

        await WaitUntil(() => _agent.State.Phones.Count == 0);
    }

    /// <summary>The server closes with 1008 when the token runs out; reconnect at once with a new one.</summary>
    [Fact]
    public async Task AnExpiredTokenReconnectsImmediatelyWithAFreshOne()
    {
        _link.Start();
        FakeRemoteServer.Connection first = await _server.NextConnection();
        await first.Receive();

        var started = DateTime.UtcNow;
        await first.Close(WebSocketCloseStatus.PolicyViolation, "token expired");
        FakeRemoteServer.Connection second = await _server.NextConnection();

        Assert.Equal("Bearer token-2", second.Authorization);
        Assert.True(DateTime.UtcNow - started < TimeSpan.FromSeconds(1), "no backoff after an expired token");
    }

    [Fact]
    public async Task StoppingClosesTheConnection()
    {
        _link.Start();
        FakeRemoteServer.Connection connection = await _server.NextConnection();
        await connection.Receive();

        await _link.StopAsync();

        Assert.Equal(DesktopSyncLinkState.Off, _link.State);
    }

    private static async Task WaitUntil(Func<bool> condition)
    {
        DateTime deadline = DateTime.UtcNow.AddSeconds(5);
        while (!condition())
        {
            Assert.True(DateTime.UtcNow < deadline, "condition not reached");
            await Task.Delay(20);
        }
    }

    private sealed class MemoryStore : IDesktopSyncStateStore
    {
        private DesktopSyncState _state = DesktopSyncState.Empty;

        public DesktopSyncState Load() => _state;

        public void Save(DesktopSyncState state) => _state = state;
    }

    private sealed class IdleTools : IDesktopAppTools
    {
        public AppToolsCapabilities Capabilities => AppToolsCapabilities.None;

        public Task<AppToolsCapabilities> ConnectAsync(CancellationToken cancellationToken) => Task.FromResult(Capabilities);

        public Task<IReadOnlyList<DesktopThread>> ListThreadsAsync(int limit, CancellationToken cancellationToken) =>
            Task.FromResult<IReadOnlyList<DesktopThread>>([]);

        public Task<DesktopThreadStatus> GetThreadStatusAsync(string threadId, CancellationToken cancellationToken) =>
            Task.FromResult(new DesktopThreadStatus("idle", []));

        public Task SendMessageAsync(string threadId, string prompt, CancellationToken cancellationToken) => Task.CompletedTask;

        public Task NavigateToAsync(string threadId, CancellationToken cancellationToken) => Task.CompletedTask;
    }

    /// <summary>A WebSocket endpoint at /api/v1/remote/agent, like the backend's.</summary>
    private sealed class FakeRemoteServer : IAsyncDisposable
    {
        private readonly HttpListener _listener = new();
        private readonly Channel<Connection> _connections = Channel.CreateUnbounded<Connection>();
        private readonly CancellationTokenSource _stop = new();
        private readonly Task _serve;

        public FakeRemoteServer()
        {
            using var probe = new System.Net.Sockets.TcpListener(IPAddress.Loopback, 0);
            probe.Start();
            int port = ((IPEndPoint)probe.LocalEndpoint).Port;
            probe.Stop();
            BaseAddress = $"http://127.0.0.1:{port}/";
            _listener.Prefixes.Add(BaseAddress);
            _listener.Start();
            _serve = ServeAsync();
        }

        public string BaseAddress { get; }

        public async Task<Connection> NextConnection() =>
            await _connections.Reader.ReadAsync().AsTask().WaitAsync(TimeSpan.FromSeconds(5));

        private async Task ServeAsync()
        {
            while (!_stop.IsCancellationRequested)
            {
                HttpListenerContext context;
                try
                {
                    context = await _listener.GetContextAsync().WaitAsync(_stop.Token);
                }
                catch (Exception)
                {
                    break;
                }

                if (!context.Request.IsWebSocketRequest || context.Request.Url?.AbsolutePath != "/api/v1/remote/agent")
                {
                    context.Response.StatusCode = 404;
                    context.Response.Close();
                    continue;
                }

                HttpListenerWebSocketContext ws = await context.AcceptWebSocketAsync(null);
                await _connections.Writer.WriteAsync(new Connection(
                    ws.WebSocket,
                    context.Request.Headers["Authorization"],
                    context.Request.QueryString["device_id"],
                    context.Request.Headers["User-Agent"]));
            }
        }

        public async ValueTask DisposeAsync()
        {
            await _stop.CancelAsync();
            _listener.Stop();
            _listener.Close();
            try
            {
                await _serve;
            }
            catch (Exception)
            {
            }
        }

        public sealed class Connection(WebSocket socket, string? authorization, string? deviceId, string? userAgent)
        {
            public string? Authorization { get; } = authorization;

            public string? DeviceId { get; } = deviceId;

            public string? UserAgent { get; } = userAgent;

            public Task Send(string json) =>
                socket.SendAsync(Encoding.UTF8.GetBytes(json), WebSocketMessageType.Text, true, CancellationToken.None);

            public async Task<JsonElement> Receive()
            {
                var buffer = new byte[64 * 1024];
                using var message = new MemoryStream();
                using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
                WebSocketReceiveResult result;
                do
                {
                    result = await socket.ReceiveAsync(buffer, timeout.Token);
                    message.Write(buffer, 0, result.Count);
                }
                while (!result.EndOfMessage);

                using JsonDocument document = JsonDocument.Parse(message.ToArray());
                return document.RootElement.Clone();
            }

            public Task Close(WebSocketCloseStatus status, string reason) =>
                socket.CloseOutputAsync(status, reason, CancellationToken.None);
        }
    }
}
