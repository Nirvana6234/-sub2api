using System.Buffers;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Net.WebSockets;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.DesktopSync;

public enum DesktopSyncLinkState
{
    /// <summary>Not running: switched off, or signed out.</summary>
    Off,
    Connecting,
    Connected,

    /// <summary>Lost or refused; trying again after a pause.</summary>
    Retrying,
}

/// <summary>A phone asking to be paired, as shown to the user for confirmation.</summary>
/// <param name="Fingerprint">Also shown on the phone; the user compares the two before approving.</param>
internal sealed record PairingRequest(long PairingId, string PhoneLabel, string PublicKey, string Fingerprint);

/// <summary>
/// The assistant's one connection to the server, outbound only.
/// </summary>
/// <remarks>
/// <para>
/// It carries commands from the server to <see cref="DesktopSyncAgent"/> and the
/// answers back; the agent decides what is allowed. The connection authenticates with
/// the account's access token, fetched per connection so a renewed token is used on
/// the next reconnect. The server closes it when that token expires (1008); it is then
/// reopened at once with a fresh one.
/// </para>
/// <para>
/// No User-Agent is set, deliberately: the account session is bound to IP and
/// User-Agent, and this client's HTTP calls send none either.
/// </para>
/// </remarks>
internal sealed class DesktopSyncLink : IAsyncDisposable
{
    private static readonly TimeSpan[] Backoff =
        [TimeSpan.FromSeconds(1), TimeSpan.FromSeconds(2), TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(15), TimeSpan.FromSeconds(30)];

    private const int MaxFrameBytes = 4 * 1024 * 1024;

    private readonly Uri _server;
    private readonly string _deviceId;
    private readonly string _deviceName;
    private readonly Func<CancellationToken, Task<string>> _accessToken;
    private readonly DesktopSyncAgent _agent;
    private readonly Func<CancellationToken, Task<byte[]>> _hello;
    private readonly HttpClient _http;
    private readonly SemaphoreSlim _sendGate = new(1, 1);
    private readonly object _gate = new();
    private CancellationTokenSource? _run;
    private Task? _loop;
    private ClientWebSocket? _socket;

    /// <param name="server">The relay root, e.g. <c>https://example.com/</c>.</param>
    /// <param name="hello">The status reported to the server on connect (desktop app present, etc.).</param>
    public DesktopSyncLink(
        Uri server,
        string deviceId,
        string deviceName,
        Func<CancellationToken, Task<string>> accessToken,
        DesktopSyncAgent agent,
        Func<CancellationToken, Task<byte[]>> hello,
        HttpClient? http = null)
    {
        _server = server;
        _deviceId = deviceId;
        _deviceName = deviceName;
        _accessToken = accessToken;
        _agent = agent;
        _hello = hello;
        _http = http ?? new HttpClient { Timeout = TimeSpan.FromSeconds(30) };
    }

    public DesktopSyncLinkState State { get; private set; } = DesktopSyncLinkState.Off;

    public string? LastError { get; private set; }

    /// <summary>Raised on any thread.</summary>
    public event Action? StateChanged;

    /// <summary>A phone claimed a code; the user must approve or reject it here. Raised on any thread.</summary>
    public event Action<PairingRequest>? PairingRequested;

    public string DeviceId => _deviceId;

    public void Start()
    {
        lock (_gate)
        {
            if (_run is not null)
            {
                return;
            }

            _run = new CancellationTokenSource();
            _loop = Task.Run(() => RunAsync(_run.Token));
        }
    }

    public async Task StopAsync()
    {
        CancellationTokenSource? run;
        Task? loop;
        lock (_gate)
        {
            run = _run;
            loop = _loop;
            _run = null;
            _loop = null;
        }

        if (run is null)
        {
            return;
        }

        await run.CancelAsync().ConfigureAwait(false);
        try
        {
            if (loop is not null)
            {
                await loop.ConfigureAwait(false);
            }
        }
        catch (OperationCanceledException)
        {
        }

        run.Dispose();
        SetState(DesktopSyncLinkState.Off, null);
    }

    // ---- Pairing, over plain HTTP ----------------------------------------------------

    /// <summary>Asks the server for a code to show; earlier codes for this computer stop working.</summary>
    public async Task<(string Code, DateTimeOffset ExpiresAt)> StartPairingAsync(CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, new Uri(_server, "api/v1/remote/pair/start"))
        {
            Content = new ByteArrayContent(SyncJson.Write(w =>
            {
                w.WriteString("device_id", _deviceId);
                w.WriteString("device_name", _deviceName);
            }))
            {
                Headers = { ContentType = new MediaTypeHeaderValue("application/json") },
            },
        };
        using JsonDocument data = await SendAsync(request, cancellationToken).ConfigureAwait(false);
        JsonElement root = data.RootElement;
        return (root.GetProperty("code").GetString()!, root.GetProperty("expires_at").GetDateTimeOffset());
    }

    /// <summary>Approves a phone: its key is kept here, then the server is told.</summary>
    public async Task ApproveAsync(PairingRequest request)
    {
        _agent.ApprovePhone(request.PairingId, request.PhoneLabel, request.PublicKey);
        await SendFrameAsync(SyncJson.Write(w =>
        {
            w.WriteString("kind", "pair.confirm");
            w.WriteNumber("pairing_id", request.PairingId);
        }), CancellationToken.None).ConfigureAwait(false);
    }

    public Task RejectAsync(PairingRequest request) =>
        SendFrameAsync(SyncJson.Write(w =>
        {
            w.WriteString("kind", "pair.reject");
            w.WriteNumber("pairing_id", request.PairingId);
        }), CancellationToken.None);

    /// <summary>Forgets a phone here first, so it is cut off even if the server cannot be reached.</summary>
    public async Task RevokeAsync(long pairingId, CancellationToken cancellationToken)
    {
        _agent.ForgetPhone(pairingId);
        using var request = new HttpRequestMessage(HttpMethod.Delete, new Uri(_server, $"api/v1/remote/pairings/{pairingId}"));
        using JsonDocument _ = await SendAsync(request, cancellationToken).ConfigureAwait(false);
    }

    private async Task<JsonDocument> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", await _accessToken(cancellationToken).ConfigureAwait(false));
        using HttpResponseMessage response = await _http.SendAsync(request, cancellationToken).ConfigureAwait(false);
        byte[] body = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
        JsonDocument document = JsonDocument.Parse(body.Length == 0 ? "{}"u8.ToArray() : body);
        if (!response.IsSuccessStatusCode)
        {
            string message = document.RootElement.TryGetProperty("message", out JsonElement m) ? m.GetString() ?? "" : "";
            document.Dispose();
            throw new HttpRequestException($"{(int)response.StatusCode} {message}".Trim(), null, response.StatusCode);
        }

        if (document.RootElement.TryGetProperty("data", out JsonElement data))
        {
            JsonDocument inner = JsonDocument.Parse(data.GetRawText());
            document.Dispose();
            return inner;
        }

        return document;
    }

    // ---- The connection ------------------------------------------------------------

    private async Task RunAsync(CancellationToken cancellationToken)
    {
        int failures = 0;
        while (!cancellationToken.IsCancellationRequested)
        {
            SetState(DesktopSyncLinkState.Connecting, null);
            bool tokenExpired = false;
            try
            {
                using var socket = new ClientWebSocket();
                socket.Options.SetRequestHeader("Authorization", "Bearer " + await _accessToken(cancellationToken).ConfigureAwait(false));
                socket.Options.KeepAliveInterval = TimeSpan.FromSeconds(20);
                await socket.ConnectAsync(AgentUri(), cancellationToken).ConfigureAwait(false);
                _socket = socket;
                failures = 0;
                SetState(DesktopSyncLinkState.Connected, null);

                byte[] hello = await _hello(cancellationToken).ConfigureAwait(false);
                await SendFrameAsync(SyncJson.Write(w =>
                {
                    w.WriteString("kind", "hello");
                    w.WritePropertyName("body");
                    w.WriteRawValue(hello);
                }), cancellationToken).ConfigureAwait(false);

                await ReceiveLoopAsync(socket, cancellationToken).ConfigureAwait(false);
                tokenExpired = socket.CloseStatus == WebSocketCloseStatus.PolicyViolation;
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                break;
            }
            catch (Exception ex) when (ex is WebSocketException or HttpRequestException or IOException or JsonException or RelayApiException)
            {
                LastError = ex.Message;
            }
            finally
            {
                _socket = null;
                _agent.UnsubscribeAll();
            }

            if (cancellationToken.IsCancellationRequested)
            {
                break;
            }

            // A token that ran out is expected: reconnect at once with a renewed one.
            TimeSpan pause = tokenExpired ? TimeSpan.Zero : Backoff[Math.Min(failures++, Backoff.Length - 1)];
            SetState(DesktopSyncLinkState.Retrying, LastError);
            await Task.Delay(pause, cancellationToken).ConfigureAwait(false);
        }
    }

    private Uri AgentUri()
    {
        var builder = new UriBuilder(new Uri(_server, "api/v1/remote/agent"))
        {
            Scheme = _server.Scheme == Uri.UriSchemeHttps ? "wss" : "ws",
            Query = "device_id=" + Uri.EscapeDataString(_deviceId),
        };
        return builder.Uri;
    }

    private async Task ReceiveLoopAsync(ClientWebSocket socket, CancellationToken cancellationToken)
    {
        byte[] buffer = ArrayPool<byte>.Shared.Rent(64 * 1024);
        try
        {
            var message = new MemoryStream();
            while (socket.State == WebSocketState.Open)
            {
                WebSocketReceiveResult result = await socket.ReceiveAsync(buffer, cancellationToken).ConfigureAwait(false);
                if (result.MessageType == WebSocketMessageType.Close)
                {
                    LastError = result.CloseStatusDescription;
                    return;
                }

                message.Write(buffer, 0, result.Count);
                if (message.Length > MaxFrameBytes)
                {
                    throw new WebSocketException("服务端消息过大");
                }

                if (!result.EndOfMessage)
                {
                    continue;
                }

                byte[] frame = message.ToArray();
                message.SetLength(0);
                Dispatch(frame, cancellationToken);
            }
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(buffer);
        }
    }

    private void Dispatch(byte[] frame, CancellationToken cancellationToken)
    {
        using JsonDocument document = JsonDocument.Parse(frame);
        JsonElement root = document.RootElement;
        string? kind = Str(root, "kind");
        string? id = Str(root, "id");
        long pairingId = root.TryGetProperty("pairing_id", out JsonElement p) && p.TryGetInt64(out long value) ? value : 0;
        byte[] body = root.TryGetProperty("body", out JsonElement b) ? Encoding.UTF8.GetBytes(b.GetRawText()) : "{}"u8.ToArray();

        switch (kind)
        {
            case "cmd" when id is not null:
                // Commands may take a while (a send waits on the desktop app); answer each on its own.
                _ = Task.Run(async () =>
                {
                    byte[] answer = await _agent.HandleAsync(pairingId, body, cancellationToken).ConfigureAwait(false);
                    await SendFrameAsync(SyncJson.Write(w =>
                    {
                        w.WriteString("kind", "result");
                        w.WriteString("id", id);
                        w.WritePropertyName("body");
                        w.WriteRawValue(answer);
                    }), cancellationToken).ConfigureAwait(false);
                }, cancellationToken);
                break;

            case "subscribe" when Str(root, "subscription") is string subscription:
                byte[]? refusal = _agent.Subscribe(pairingId, subscription, body, e => SendEventAsync(subscription, e, cancellationToken));
                if (refusal is not null)
                {
                    _ = SendEventAsync(subscription, refusal, cancellationToken);
                }

                break;

            case "unsubscribe" when Str(root, "subscription") is string subscription:
                _agent.Unsubscribe(subscription);
                break;

            case "event":
                HandleServerEvent(Str(root, "type"), root.TryGetProperty("body", out JsonElement eventBody) ? eventBody : default);
                break;
        }
    }

    private void HandleServerEvent(string? type, JsonElement body)
    {
        long pairingId = body.ValueKind == JsonValueKind.Object && body.TryGetProperty("pairing_id", out JsonElement p) && p.TryGetInt64(out long v) ? v : 0;
        switch (type)
        {
            case "pair.request" when pairingId > 0:
                string key = Str(body, "public_key") ?? string.Empty;
                PairingRequested?.Invoke(new PairingRequest(pairingId, Str(body, "phone_label") ?? "手机", key, Fingerprint(key)));
                break;

            case "pair.revoked" when pairingId > 0:
                _agent.ForgetPhone(pairingId);
                break;
        }
    }

    /// <summary>
    /// Six hex digits of the key's SHA-256, grouped for reading aloud. The phone shows the
    /// same; matching them is what makes approving the right phone possible.
    /// </summary>
    public static string Fingerprint(string publicKey)
    {
        byte[] hash = System.Security.Cryptography.SHA256.HashData(Encoding.UTF8.GetBytes(publicKey));
        string hex = Convert.ToHexString(hash, 0, 3);
        return $"{hex[..3]} {hex[3..]}";
    }

    private Task SendEventAsync(string subscription, byte[] body, CancellationToken cancellationToken) =>
        SendFrameAsync(SyncJson.Write(w =>
        {
            w.WriteString("kind", "event");
            w.WriteString("subscription", subscription);
            w.WritePropertyName("body");
            w.WriteRawValue(body);
        }), cancellationToken);

    private async Task SendFrameAsync(byte[] frame, CancellationToken cancellationToken)
    {
        ClientWebSocket? socket = _socket;
        if (socket is null || socket.State != WebSocketState.Open)
        {
            return;
        }

        await _sendGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await socket.SendAsync(frame, WebSocketMessageType.Text, endOfMessage: true, cancellationToken).ConfigureAwait(false);
        }
        catch (Exception ex) when (ex is WebSocketException or ObjectDisposedException or OperationCanceledException)
        {
            // The receive loop notices the broken socket and reconnects.
        }
        finally
        {
            _sendGate.Release();
        }
    }

    private void SetState(DesktopSyncLinkState state, string? error)
    {
        State = state;
        if (error is not null)
        {
            LastError = error;
        }

        StateChanged?.Invoke();
    }

    public async ValueTask DisposeAsync()
    {
        await StopAsync().ConfigureAwait(false);
        _sendGate.Dispose();
    }

    private static string? Str(JsonElement element, string property) =>
        element.ValueKind == JsonValueKind.Object && element.TryGetProperty(property, out JsonElement value) && value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;
}
