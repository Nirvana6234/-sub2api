using System.Net;
using AiSwitchGui;
using LanAi.Workspace.Injection.Sentinel;

namespace LanAi.Workspace.Wpf.Services;

/// <summary>
/// Implements the injection layer's switch gateway by delegating to the legacy switch
/// coordinator.
/// </summary>
/// <remarks>
/// The coordinator and its <c>LiveStatus</c> / <c>OperationResult</c> models are
/// internal to this assembly, so they cannot cross into
/// <c>AiSwitch.Injection</c>; this adapter maps them onto the records that layer owns.
///
/// The snapshot the gateway contract demands is taken by
/// <c>SwitchService.SwitchAsync</c>, which copies the whole Codex configuration file
/// before writing. Keep that call in the path — preserving only the routing fields
/// would lose the user's other settings when the official client rewrites the file.
/// </remarks>
internal sealed class RelaySwitchGatewayAdapter : IRelaySwitchGateway
{
    private readonly ILegacySwitchCoordinator _coordinator;

    public RelaySwitchGatewayAdapter(ILegacySwitchCoordinator coordinator)
    {
        _coordinator = coordinator ?? throw new ArgumentNullException(nameof(coordinator));
    }

    public Task<RelayRoutingState> ReadRoutingAsync(CancellationToken cancellationToken)
    {
        LiveStatus status = _coordinator.ReadLiveStatus();
        var baseUrl = status.CodexBaseUrl;
        var sourceId = status.MixedCodexSourceId;

        return Task.FromResult(new RelayRoutingState(
            PointsAtRelay(baseUrl, sourceId),
            baseUrl,
            sourceId));
    }

    public async Task<RelaySwitchOutcome> SwitchToRelayAsync(CancellationToken cancellationToken)
    {
        OperationResult result = await _coordinator
            .ApplySourceAsync(ProfileSourceIds.LocalMachine, cancellationToken)
            .ConfigureAwait(false);
        return Map(result);
    }

    public async Task<RelaySwitchOutcome> SwitchToOfficialAsync(CancellationToken cancellationToken)
    {
        OperationResult result = await _coordinator
            .ApplySourceAsync(ProfileSourceIds.Cloud, cancellationToken)
            .ConfigureAwait(false);
        return Map(result);
    }

    private static RelaySwitchOutcome Map(OperationResult result)
        => new(result.Success, result.Summary);

    /// <summary>
    /// Decides whether Codex currently routes through a 共飞 relay.
    /// </summary>
    /// <remarks>
    /// The base URL is the ground truth because it is what actually carries requests;
    /// the source id only corroborates it. A relay is either this machine (loopback) or
    /// another machine on the LAN, so a private address counts too.
    /// </remarks>
    internal static bool PointsAtRelay(string? baseUrl, string? sourceId)
    {
        if (!string.IsNullOrWhiteSpace(baseUrl)
            && Uri.TryCreate(baseUrl, UriKind.Absolute, out var uri)
            && (uri.Scheme == Uri.UriSchemeHttp || uri.Scheme == Uri.UriSchemeHttps))
        {
            if (uri.IsLoopback)
            {
                return true;
            }

            if (IPAddress.TryParse(uri.Host, out var address) && IsPrivate(address))
            {
                return true;
            }

            // A resolvable public host means the official endpoint, whatever the
            // recorded source id claims.
            return false;
        }

        return string.Equals(sourceId, ProfileSourceIds.LocalMachine, StringComparison.OrdinalIgnoreCase)
            || string.Equals(sourceId, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase);
    }

    private static bool IsPrivate(IPAddress address)
    {
        if (IPAddress.IsLoopback(address))
        {
            return true;
        }

        if (address.AddressFamily != System.Net.Sockets.AddressFamily.InterNetwork)
        {
            // Unique local addresses fc00::/7.
            var v6 = address.GetAddressBytes();
            return v6.Length == 16 && (v6[0] & 0xFE) == 0xFC;
        }

        var octets = address.GetAddressBytes();
        return octets[0] switch
        {
            10 => true,
            172 => octets[1] >= 16 && octets[1] <= 31,
            192 => octets[1] == 168,
            169 => octets[1] == 254,
            _ => false,
        };
    }
}
