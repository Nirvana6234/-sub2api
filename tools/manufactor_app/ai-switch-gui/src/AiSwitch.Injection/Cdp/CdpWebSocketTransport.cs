using System.Net.WebSockets;
using System.Text;

namespace LanAi.Workspace.Injection.Cdp;

/// <summary>
/// WebSocket transport over the loopback DevTools endpoint. Uses only
/// <see cref="ClientWebSocket"/> so the injection layer carries no third-party
/// dependency.
/// </summary>
public sealed class CdpWebSocketTransport : ICdpTransport
{
    private const int ReceiveChunkSize = 64 * 1024;

    private readonly ClientWebSocket _socket = new();
    private bool _disposed;

    public Task ConnectAsync(Uri endpoint, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(endpoint);
        if (!endpoint.IsLoopback)
        {
            throw new ArgumentException(
                "The injection layer only connects to loopback DevTools endpoints.",
                nameof(endpoint));
        }

        return _socket.ConnectAsync(endpoint, cancellationToken);
    }

    public Task SendAsync(string payload, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(payload);
        var buffer = Encoding.UTF8.GetBytes(payload);
        return _socket.SendAsync(
            new ArraySegment<byte>(buffer),
            WebSocketMessageType.Text,
            endOfMessage: true,
            cancellationToken);
    }

    public async Task<string?> ReceiveAsync(CancellationToken cancellationToken)
    {
        var buffer = new byte[ReceiveChunkSize];
        var builder = new StringBuilder();

        while (true)
        {
            WebSocketReceiveResult result;
            try
            {
                result = await _socket
                    .ReceiveAsync(new ArraySegment<byte>(buffer), cancellationToken)
                    .ConfigureAwait(false);
            }
            catch (WebSocketException)
            {
                return null;
            }

            if (result.MessageType == WebSocketMessageType.Close)
            {
                return null;
            }

            builder.Append(Encoding.UTF8.GetString(buffer, 0, result.Count));
            if (result.EndOfMessage)
            {
                return builder.ToString();
            }
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _socket.Dispose();
    }
}
