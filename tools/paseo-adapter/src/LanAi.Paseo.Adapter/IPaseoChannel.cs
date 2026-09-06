namespace LanAi.Paseo.Adapter;

/// <summary>
/// A line-oriented duplex channel to the bridge.
/// </summary>
/// <remarks>
/// Abstracted so the client's framing, correlation and error mapping can be
/// tested without a process, a pipe, or a daemon. The real implementation is
/// <see cref="NamedPipeChannel"/>; tests supply an in-memory double.
/// </remarks>
public interface IPaseoChannel : IAsyncDisposable
{
    /// <summary>Raised for each complete line received. Never raised with a partial line.</summary>
    event EventHandler<string>? LineReceived;

    /// <summary>
    /// Raised once when the channel can no longer carry traffic. The client turns
    /// this into <see cref="PaseoErrorCode.TransportDown"/> for every in-flight
    /// request, because no response can arrive after it.
    /// </summary>
    event EventHandler? Closed;

    /// <summary>Waits for the peer to attach, then starts dispatching <see cref="LineReceived"/>.</summary>
    Task OpenAsync(CancellationToken cancellationToken);

    /// <summary>Writes one line. The implementation appends the newline.</summary>
    Task SendLineAsync(string line, CancellationToken cancellationToken);
}
