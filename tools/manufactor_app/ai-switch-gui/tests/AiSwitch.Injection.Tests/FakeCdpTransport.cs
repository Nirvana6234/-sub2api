using System.Collections.Concurrent;
using System.Text.Json;
using System.Threading.Channels;
using LanAi.Workspace.Injection.Cdp;

namespace AiSwitch.Injection.Tests;

/// <summary>
/// In-memory transport that records outgoing commands and lets a test push replies
/// and events back, so the protocol layer runs without a browser.
/// </summary>
internal sealed class FakeCdpTransport : ICdpTransport
{
    private readonly Channel<string> _inbound = Channel.CreateUnbounded<string>();

    public ConcurrentQueue<string> Sent { get; } = new();

    public Uri? ConnectedTo { get; private set; }

    public bool Disposed { get; private set; }

    /// <summary>Replies automatically to each command with an empty result.</summary>
    public bool AutoAcknowledge { get; set; }

    public Task ConnectAsync(Uri endpoint, CancellationToken cancellationToken)
    {
        ConnectedTo = endpoint;
        return Task.CompletedTask;
    }

    public Task SendAsync(string payload, CancellationToken cancellationToken)
    {
        Sent.Enqueue(payload);

        if (AutoAcknowledge)
        {
            using var document = JsonDocument.Parse(payload);
            var id = document.RootElement.GetProperty("id").GetInt32();
            PushResult(id, "{}");
        }

        return Task.CompletedTask;
    }

    public async Task<string?> ReceiveAsync(CancellationToken cancellationToken)
    {
        try
        {
            return await _inbound.Reader.ReadAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (ChannelClosedException)
        {
            return null;
        }
    }

    public void PushRaw(string payload) => _inbound.Writer.TryWrite(payload);

    public void PushResult(int id, string resultJson)
        => PushRaw($"{{\"id\":{id},\"result\":{resultJson}}}");

    public void PushError(int id, int code, string message)
        => PushRaw($"{{\"id\":{id},\"error\":{{\"code\":{code},\"message\":\"{message}\"}}}}");

    public void PushEvent(string method, string paramsJson)
        => PushRaw($"{{\"method\":\"{method}\",\"params\":{paramsJson}}}");

    /// <summary>Signals that the peer closed the channel.</summary>
    public void Close() => _inbound.Writer.TryComplete();

    public string LastSent => Sent.LastOrDefault() ?? string.Empty;

    public void Dispose() => Disposed = true;
}
