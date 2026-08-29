using System.Net;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Wpf.Services;

internal sealed record Sub2ApiEndpointTarget(
    string ProfileId,
    string DisplayName,
    ConnectionProfileKind Kind,
    Uri ApiBaseUri,
    Uri? DashboardUri,
    bool RequiresInsecureLoginConfirmation = false)
{
    public bool IsLocalMachine =>
        string.Equals(ProfileId, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase);
}

internal static class ConnectionSourceResolver
{
    internal static string? ResolveRequestedProfileId(
        ConnectionProfileSelection? selection,
        ConnectionProfileRouting? routing = null)
    {
        string? unifiedRoutingId = ResolveUnifiedRoutingProfileId(routing);
        if (!string.IsNullOrWhiteSpace(unifiedRoutingId))
        {
            return unifiedRoutingId;
        }

        return !string.IsNullOrWhiteSpace(selection?.ActiveProfileId)
            ? selection.ActiveProfileId
            : selection?.LocalProfileId;
    }

    internal static string? ResolveActiveProfileId(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileSelection? selection,
        ConnectionProfileRouting? routing = null)
    {
        ArgumentNullException.ThrowIfNull(connections);
        string? requestedId = ResolveRequestedProfileId(selection, routing);
        return !string.IsNullOrWhiteSpace(requestedId) && connections.Any(connection =>
            string.Equals(connection.Id, requestedId, StringComparison.OrdinalIgnoreCase))
            ? requestedId
            : null;
    }

    internal static ConnectionProfile? FindActiveProfile(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileSelection? selection,
        ConnectionProfileRouting? routing = null)
    {
        string? id = ResolveActiveProfileId(connections, selection, routing);
        return string.IsNullOrWhiteSpace(id)
            ? null
            : connections.FirstOrDefault(connection =>
                string.Equals(connection.Id, id, StringComparison.OrdinalIgnoreCase));
    }

    internal static string? ResolveUnifiedRoutingProfileId(ConnectionProfileRouting? routing)
    {
        if (routing is null ||
            string.IsNullOrWhiteSpace(routing.CodexProfileId) ||
            !string.Equals(routing.CodexProfileId, routing.ClaudeCodeProfileId, StringComparison.OrdinalIgnoreCase) ||
            !string.Equals(routing.CodexProfileId, routing.GeminiCliProfileId, StringComparison.OrdinalIgnoreCase) ||
            !string.Equals(routing.CodexProfileId, ResolveOptionalGrokRoutingId(routing), StringComparison.OrdinalIgnoreCase))
        {
            return null;
        }

        return routing.CodexProfileId;
    }

    private static string ResolveOptionalGrokRoutingId(ConnectionProfileRouting routing) =>
        string.IsNullOrWhiteSpace(routing.GrokCliProfileId)
            ? routing.GeminiCliProfileId
            : routing.GrokCliProfileId;
}

internal static class Sub2ApiEndpointSelector
{
    internal static IReadOnlyList<Sub2ApiEndpointTarget> GetCandidates(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileSelection? selection,
        ConnectionProfileRouting? routing = null)
    {
        ArgumentNullException.ThrowIfNull(connections);

        string? requestedProfileId = ConnectionSourceResolver.ResolveRequestedProfileId(selection, routing);
        if (!string.IsNullOrWhiteSpace(requestedProfileId))
        {
            ConnectionProfile? selectedProfile = connections.FirstOrDefault(connection =>
                string.Equals(connection.Id, requestedProfileId, StringComparison.OrdinalIgnoreCase));
            return selectedProfile is not null && TryCreate(selectedProfile, out Sub2ApiEndpointTarget? selectedTarget)
                ? [selectedTarget!]
                : [];
        }

        var orderedIds = new List<string?>
        {
            ConnectionProfileIds.LocalMachine,
            ConnectionProfileIds.LanDefault,
        };
        orderedIds.AddRange(connections.Select(connection => connection.Id));

        var targets = new List<Sub2ApiEndpointTarget>();
        var visited = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (string? id in orderedIds)
        {
            if (string.IsNullOrWhiteSpace(id) || !visited.Add(id))
            {
                continue;
            }

            ConnectionProfile? profile = connections.FirstOrDefault(connection =>
                string.Equals(connection.Id, id, StringComparison.OrdinalIgnoreCase));
            if (profile is not null && TryCreate(profile, out Sub2ApiEndpointTarget? target))
            {
                targets.Add(target!);
            }
        }

        return targets;
    }

    internal static bool TryCreate(ConnectionProfile profile, out Sub2ApiEndpointTarget? target)
    {
        ArgumentNullException.ThrowIfNull(profile);
        target = null;
        if (!TrySelectApiBaseUri(profile, out Uri? apiBaseUri))
        {
            return false;
        }

        Uri? dashboardUri = Sub2ApiEndpointNormalizer.TryNormalizeDashboardUri(
            profile.DashboardUrl,
            out Uri? configuredDashboard)
            ? configuredDashboard
            : profile.Kind == ConnectionProfileKind.Cloud
                ? apiBaseUri
            : profile.Kind is ConnectionProfileKind.Local or ConnectionProfileKind.Lan && apiBaseUri!.Port == 8080
                ? LocalGatewayEndpointNormalizer.CreateNativeDashboardUri(apiBaseUri)
                : null;
        target = new Sub2ApiEndpointTarget(
            profile.Id,
            string.IsNullOrWhiteSpace(profile.Name) ? profile.Id : profile.Name.Trim(),
            profile.Kind,
            apiBaseUri!,
            dashboardUri,
            apiBaseUri!.Scheme == Uri.UriSchemeHttp &&
            !Sub2ApiEndpointNormalizer.IsPrivateNetworkHost(apiBaseUri));
        return true;
    }

    private static bool TrySelectApiBaseUri(ConnectionProfile profile, out Uri? apiBaseUri)
    {
        apiBaseUri = null;
        string? candidate = SelectPrimaryAddress(profile);
        return Sub2ApiEndpointNormalizer.TryNormalizeApiBaseUri(
            candidate,
            allowPublicHttp: profile.Kind == ConnectionProfileKind.Cloud,
            out apiBaseUri);
    }

    internal static string? SelectPrimaryAddress(ConnectionProfile profile)
    {
        ArgumentNullException.ThrowIfNull(profile);
        return !string.IsNullOrWhiteSpace(profile.BaseUrl)
            ? profile.BaseUrl
            : profile.ClientBaseUrls.GetValueOrDefault(CliKind.Codex)
              ?? profile.ClientBaseUrls.Values.FirstOrDefault(value => !string.IsNullOrWhiteSpace(value));
    }

    internal static string DescribeUnavailableSelectedSource(ConnectionProfile profile)
    {
        ArgumentNullException.ThrowIfNull(profile);
        string? address = SelectPrimaryAddress(profile);
        if (profile.Kind == ConnectionProfileKind.Cloud &&
            Uri.TryCreate(address, UriKind.Absolute, out Uri? uri) &&
            uri.Scheme == Uri.UriSchemeHttp)
        {
            return $"当前来源“{profile.Name}”使用公网 HTTP。模型连接可以继续使用，但为保护账号密码，云端后台登录要求同一来源启用 HTTPS。";
        }

        return $"当前来源“{profile.Name}”没有可用于账户登录的后台地址，请在连接中心检查该来源。";
    }
}

internal static class Sub2ApiEndpointNormalizer
{
    internal static bool TryNormalizeApiBaseUri(string? value, out Uri? baseUri)
        => TryNormalizeApiBaseUri(value, allowPublicHttp: false, out baseUri);

    internal static bool TryNormalizeApiBaseUri(
        string? value,
        bool allowPublicHttp,
        out Uri? baseUri)
    {
        baseUri = null;
        if (!TryParseSafeHttpUri(value, allowPublicHttp, out Uri? parsed) || !IsApiRootPath(parsed!))
        {
            return false;
        }

        baseUri = new UriBuilder(parsed!)
        {
            Path = "/",
            Query = string.Empty,
            Fragment = string.Empty,
        }.Uri;
        return true;
    }

    internal static bool TryNormalizeDashboardUri(string? value, out Uri? dashboardUri)
    {
        dashboardUri = null;
        if (!TryParseSafeHttpUri(value, allowPublicHttp: false, out Uri? parsed))
        {
            return false;
        }

        dashboardUri = new UriBuilder(parsed!)
        {
            Query = string.Empty,
            Fragment = string.Empty,
        }.Uri;
        return true;
    }

    private static bool TryParseSafeHttpUri(string? value, bool allowPublicHttp, out Uri? uri)
    {
        uri = null;
        if (string.IsNullOrWhiteSpace(value) ||
            !Uri.TryCreate(value.Trim(), UriKind.Absolute, out Uri? parsed) ||
            parsed.Scheme is not ("http" or "https") ||
            string.IsNullOrWhiteSpace(parsed.Host) ||
            !string.IsNullOrWhiteSpace(parsed.UserInfo) ||
            !string.IsNullOrWhiteSpace(parsed.Query) ||
            !string.IsNullOrWhiteSpace(parsed.Fragment) ||
            parsed.Scheme == Uri.UriSchemeHttp && !allowPublicHttp && !IsPrivateNetworkHost(parsed))
        {
            return false;
        }

        uri = parsed;
        return true;
    }

    internal static bool IsPrivateNetworkHost(Uri uri)
    {
        if (uri.IsLoopback || string.Equals(uri.Host, "localhost", StringComparison.OrdinalIgnoreCase))
        {
            return true;
        }

        string host = uri.Host.Trim('[', ']');
        if (!host.Contains('.') && !host.Contains(':'))
        {
            return true;
        }

        return IPAddress.TryParse(host, out IPAddress? address) && IsPrivateAddress(address);
    }

    private static bool IsPrivateAddress(IPAddress address)
    {
        if (IPAddress.IsLoopback(address) || address.IsIPv6LinkLocal || address.IsIPv6SiteLocal)
        {
            return true;
        }

        byte[] bytes = address.GetAddressBytes();
        if (address.AddressFamily == System.Net.Sockets.AddressFamily.InterNetwork)
        {
            return bytes[0] == 10 ||
                   bytes[0] == 127 ||
                   bytes[0] == 169 && bytes[1] == 254 ||
                   bytes[0] == 172 && bytes[1] is >= 16 and <= 31 ||
                   bytes[0] == 192 && bytes[1] == 168;
        }

        return bytes.Length == 16 && (bytes[0] & 0xFE) == 0xFC;
    }

    private static bool IsApiRootPath(Uri uri)
    {
        string path = uri.AbsolutePath.Trim('/');
        return path is "" or "v1" or "api/v1";
    }
}








