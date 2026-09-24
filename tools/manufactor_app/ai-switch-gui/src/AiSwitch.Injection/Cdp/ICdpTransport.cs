namespace LanAi.Workspace.Injection.Cdp;

/// <summary>
/// Message transport for a Chrome DevTools Protocol endpoint. Kept separate from
/// <see cref="CdpConnection"/> so the wire protocol (id correlation, error
/// surfacing, event dispatch) can be unit tested without a live browser, the same
/// way the probe services take an injectable HttpClient.
/// </summary>
public interface ICdpTransport : IDisposable
{
    Task ConnectAsync(Uri endpoint, CancellationToken cancellationToken);

    Task SendAsync(string payload, CancellationToken cancellationToken);

    /// <summary>
    /// Returns the next complete text message, or <c>null</c> once the peer closed
    /// the channel.
    /// </summary>
    Task<string?> ReceiveAsync(CancellationToken cancellationToken);
}
