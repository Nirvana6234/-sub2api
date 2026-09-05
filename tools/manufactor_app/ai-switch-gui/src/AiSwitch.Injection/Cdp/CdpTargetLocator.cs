using System.Net.Http.Json;
using System.Text.Json.Serialization;

namespace LanAi.Workspace.Injection.Cdp;

/// <summary>A DevTools target. <see cref="WebSocketDebuggerUrl"/> may be absent.</summary>
public sealed record CdpTarget(
    [property: JsonPropertyName("id")] string? Id,
    [property: JsonPropertyName("type")] string? Type,
    [property: JsonPropertyName("title")] string? Title,
    [property: JsonPropertyName("url")] string? Url,
    [property: JsonPropertyName("webSocketDebuggerUrl")] string? WebSocketDebuggerUrl);

/// <summary>Browser build information reported by <c>/json/version</c>.</summary>
public sealed record CdpBrowserInfo(
    [property: JsonPropertyName("Browser")] string? Browser,
    [property: JsonPropertyName("Protocol-Version")] string? ProtocolVersion);

/// <summary>
/// Discovers DevTools targets over the loopback HTTP endpoint. Also serves as the
/// liveness probe that lets the launcher attach to an already-running instance
/// instead of terminating the user's session.
/// </summary>
public sealed class CdpTargetLocator : IDisposable
{
    private readonly HttpClient _httpClient;
    private readonly bool _ownsHttpClient;

    public CdpTargetLocator(HttpClient? httpClient = null)
    {
        _ownsHttpClient = httpClient is null;
        _httpClient = httpClient ?? new HttpClient { Timeout = TimeSpan.FromSeconds(5) };
    }

    /// <summary>
    /// Returns build information when a debug port is listening, otherwise
    /// <c>null</c>. Never throws for an absent or unresponsive endpoint.
    /// </summary>
    public async Task<CdpBrowserInfo?> TryGetBrowserAsync(
        int port,
        CancellationToken cancellationToken)
    {
        try
        {
            return await _httpClient
                .GetFromJsonAsync(
                    BuildUri(port, "json/version"),
                    CdpJsonContext.Default.CdpBrowserInfo,
                    cancellationToken)
                .ConfigureAwait(false);
        }
        catch (Exception exception) when (
            exception is HttpRequestException
                or TaskCanceledException
                or System.Text.Json.JsonException)
        {
            return null;
        }
    }

    public async Task<IReadOnlyList<CdpTarget>> ListTargetsAsync(
        int port,
        CancellationToken cancellationToken)
    {
        var targets = await _httpClient
            .GetFromJsonAsync(
                BuildUri(port, "json/list"),
                CdpJsonContext.Default.ListCdpTarget,
                cancellationToken)
            .ConfigureAwait(false);
        return targets ?? [];
    }

    /// <summary>
    /// Returns the attachable page target, preferring the app shell document over
    /// auxiliary targets such as service workers.
    /// </summary>
    public async Task<CdpTarget?> FindPageTargetAsync(
        int port,
        CancellationToken cancellationToken)
    {
        var targets = await ListTargetsAsync(port, cancellationToken).ConfigureAwait(false);
        return targets.FirstOrDefault(target =>
            string.Equals(target.Type, "page", StringComparison.OrdinalIgnoreCase)
            && !string.IsNullOrWhiteSpace(target.WebSocketDebuggerUrl));
    }

    private static Uri BuildUri(int port, string path)
    {
        if (port is < 1 or > 65535)
        {
            throw new ArgumentOutOfRangeException(nameof(port));
        }

        return new Uri($"http://127.0.0.1:{port}/{path}");
    }

    public void Dispose()
    {
        if (_ownsHttpClient)
        {
            _httpClient.Dispose();
        }
    }
}
