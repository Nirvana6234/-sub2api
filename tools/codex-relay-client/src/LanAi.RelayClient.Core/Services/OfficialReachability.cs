using System.Net;
using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>Whether this machine can reach an official API host right now, and through what.</summary>
/// <param name="Problem">Why not, in words, when it cannot.</param>
internal sealed record Reachability(bool Reachable, string ProxyDescription, string? Problem);

internal interface IOfficialReachability
{
    Task<Reachability> CheckAsync(Uri officialEndpoint, CancellationToken cancellationToken = default);
}

/// <summary>
/// One request to the official host, through the proxy as it is set now, before the user
/// commits to the local proxy.
/// </summary>
/// <remarks>
/// Any HTTP answer counts as reachable — the host's root answers 403 or 404 to an anonymous
/// GET, and that is still proof the network path works. Only a failure to get any answer
/// within the time limit counts as unreachable, which in practice means the proxy/VPN is off,
/// or pointed at a node that cannot reach the host.
/// </remarks>
internal sealed class OfficialReachability : IOfficialReachability
{
    private static readonly TimeSpan Limit = TimeSpan.FromSeconds(10);

    public async Task<Reachability> CheckAsync(Uri officialEndpoint, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(officialEndpoint);
        SystemProxy proxy = SystemProxyReader.Current(officialEndpoint);
        var root = new Uri(officialEndpoint.GetLeftPart(UriPartial.Authority) + "/");

        using var handler = new HttpClientHandler
        {
            AllowAutoRedirect = false,
            UseProxy = proxy.Proxy is not null,
            Proxy = proxy.Proxy,
        };
        using var client = new HttpClient(handler) { Timeout = Limit };
        try
        {
            using HttpResponseMessage response = await client
                .GetAsync(root, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                .ConfigureAwait(false);
            return new Reachability(true, proxy.Description, null);
        }
        catch (HttpRequestException ex)
        {
            return new Reachability(false, proxy.Description, ex.InnerException?.Message ?? ex.Message);
        }
        catch (TaskCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            return new Reachability(false, proxy.Description, $"{Limit.TotalSeconds:0} 秒内没有响应");
        }
    }
}
