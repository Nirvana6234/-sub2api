using System.Net;
using System.Net.NetworkInformation;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Wpf.Services;

/// <summary>
/// Resolves the API and browser endpoints of the fixed <c>local-machine</c>
/// connection.  The resolver deliberately accepts only loopback addresses or
/// addresses assigned to this computer, so a stale profile cannot redirect
/// local statistics credentials to another machine on the LAN or Internet.
/// </summary>
internal interface ILocalGatewayEndpointResolver
{
    Task<LocalGatewayEndpointResolution> ResolveAsync(CancellationToken cancellationToken);
}

internal enum LocalGatewayEndpointResolutionStatus
{
    /// <summary>
    /// Used by design-time/legacy construction where Connection Center is not
    /// available.  The statistics page may still use its explicit remote form.
    /// The production shell always supplies a profile reader.
    /// </summary>
    ManualCloudOnly,

    ProfileReadFailed,
    ProfileMissing,
    ProfileInvalid,
    ApiAddressMissing,
    ApiAddressNotLocal,
    Ready,
}

/// <summary>
/// Keeps resolved addresses private to the page/service boundary.  They are
/// never copied into a bindable view-model property, telemetry, or log entry.
/// </summary>
internal sealed record LocalGatewayEndpointResolution(
    LocalGatewayEndpointResolutionStatus Status,
    Uri? ApiBaseUri,
    Uri? DashboardUri)
{
    public bool IsReady => Status == LocalGatewayEndpointResolutionStatus.Ready && ApiBaseUri is not null;

    public bool RequiresConfigurationFix => Status is not (
        LocalGatewayEndpointResolutionStatus.Ready or
        LocalGatewayEndpointResolutionStatus.ManualCloudOnly);

    public static LocalGatewayEndpointResolution ManualCloudOnly { get; } = new(
        LocalGatewayEndpointResolutionStatus.ManualCloudOnly,
        ApiBaseUri: null,
        DashboardUri: null);

    public static LocalGatewayEndpointResolution ProfileMissing { get; } = new(
        LocalGatewayEndpointResolutionStatus.ProfileMissing,
        ApiBaseUri: null,
        DashboardUri: null);

    public static LocalGatewayEndpointResolution ProfileReadFailed { get; } = new(
        LocalGatewayEndpointResolutionStatus.ProfileReadFailed,
        ApiBaseUri: null,
        DashboardUri: null);

    public static LocalGatewayEndpointResolution ProfileInvalid { get; } = new(
        LocalGatewayEndpointResolutionStatus.ProfileInvalid,
        ApiBaseUri: null,
        DashboardUri: null);

    public static LocalGatewayEndpointResolution ApiAddressMissing { get; } = new(
        LocalGatewayEndpointResolutionStatus.ApiAddressMissing,
        ApiBaseUri: null,
        DashboardUri: null);

    public static LocalGatewayEndpointResolution ApiAddressNotLocal { get; } = new(
        LocalGatewayEndpointResolutionStatus.ApiAddressNotLocal,
        ApiBaseUri: null,
        DashboardUri: null);
}

internal sealed class ManualCloudOnlyLocalGatewayEndpointResolver : ILocalGatewayEndpointResolver
{
    public Task<LocalGatewayEndpointResolution> ResolveAsync(CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        return Task.FromResult(LocalGatewayEndpointResolution.ManualCloudOnly);
    }
}

internal sealed class ConnectionProfileLocalGatewayEndpointResolver : ILocalGatewayEndpointResolver
{
    private readonly IConnectionProfileReader _profileReader;
    private readonly ILocalGatewayAddressValidator _addressValidator;

    public ConnectionProfileLocalGatewayEndpointResolver(IConnectionProfileReader profileReader)
        : this(profileReader, LocalGatewayAddressValidator.Instance)
    {
    }

    internal ConnectionProfileLocalGatewayEndpointResolver(
        IConnectionProfileReader profileReader,
        ILocalGatewayAddressValidator addressValidator)
    {
        _profileReader = profileReader ?? throw new ArgumentNullException(nameof(profileReader));
        _addressValidator = addressValidator ?? throw new ArgumentNullException(nameof(addressValidator));
    }

    public async Task<LocalGatewayEndpointResolution> ResolveAsync(CancellationToken cancellationToken)
    {
        ConnectionProfile? profile = await _profileReader
            .GetByIdAsync(ConnectionProfileIds.LocalMachine, cancellationToken)
            .ConfigureAwait(false);
        if (profile is null)
        {
            return LocalGatewayEndpointResolution.ProfileMissing;
        }

        if (profile.Kind != ConnectionProfileKind.Local)
        {
            return LocalGatewayEndpointResolution.ProfileInvalid;
        }

        string? candidate = SelectApiAddress(profile);
        if (string.IsNullOrWhiteSpace(candidate))
        {
            return LocalGatewayEndpointResolution.ApiAddressMissing;
        }

        if (!LocalGatewayEndpointNormalizer.TryNormalizeLocalApiBaseUri(
                candidate,
                _addressValidator,
                out Uri? apiBaseUri))
        {
            return LocalGatewayEndpointResolution.ApiAddressNotLocal;
        }

        Uri? dashboardUri = ResolveDashboardUri(profile.DashboardUrl, apiBaseUri!);
        return new LocalGatewayEndpointResolution(
            LocalGatewayEndpointResolutionStatus.Ready,
            apiBaseUri,
            dashboardUri);
    }

    private static string? SelectApiAddress(ConnectionProfile profile)
    {
        ArgumentNullException.ThrowIfNull(profile);

        if (profile.ClientBaseUrls.TryGetValue(CliKind.Codex, out string? codexAddress) &&
            !string.IsNullOrWhiteSpace(codexAddress))
        {
            return codexAddress.Trim();
        }

        if (!string.IsNullOrWhiteSpace(profile.BaseUrl))
        {
            return profile.BaseUrl.Trim();
        }

        foreach (CliKind client in new[] { CliKind.ClaudeCode, CliKind.GeminiCli })
        {
            if (profile.ClientBaseUrls.TryGetValue(client, out string? address) &&
                !string.IsNullOrWhiteSpace(address))
            {
                return address.Trim();
            }
        }

        return null;
    }

    private Uri? ResolveDashboardUri(string? configuredDashboardUrl, Uri apiBaseUri)
    {
        if (LocalGatewayEndpointNormalizer.TryNormalizeLocalDashboardUri(
                configuredDashboardUrl,
                _addressValidator,
                out Uri? configuredDashboardUri))
        {
            return configuredDashboardUri;
        }

        // The production gateway serves its API and embedded browser UI from
        // the same endpoint. Custom ports still require an explicit
        // DashboardUrl in the local connection profile.
        return apiBaseUri.Port == 8080
            ? LocalGatewayEndpointNormalizer.CreateNativeDashboardUri(apiBaseUri)
            : null;
    }
}

internal interface ILocalGatewayAddressValidator
{
    bool IsAddressOnThisComputer(Uri uri);
}

internal sealed class LocalGatewayAddressValidator : ILocalGatewayAddressValidator
{
    public static LocalGatewayAddressValidator Instance { get; } = new();

    private LocalGatewayAddressValidator()
    {
    }

    public bool IsAddressOnThisComputer(Uri uri)
    {
        ArgumentNullException.ThrowIfNull(uri);
        if (uri.IsLoopback || string.Equals(uri.Host, "localhost", StringComparison.OrdinalIgnoreCase))
        {
            return true;
        }

        string host = uri.Host.Trim('[', ']');
        if (!IPAddress.TryParse(host, out IPAddress? candidate))
        {
            return false;
        }

        if (IPAddress.IsLoopback(candidate))
        {
            return true;
        }

        try
        {
            foreach (NetworkInterface networkInterface in NetworkInterface.GetAllNetworkInterfaces())
            {
                foreach (UnicastIPAddressInformation address in networkInterface.GetIPProperties().UnicastAddresses)
                {
                    if (candidate.Equals(address.Address))
                    {
                        return true;
                    }
                }
            }
        }
        catch (NetworkInformationException)
        {
            return false;
        }

        return false;
    }
}

/// <summary>
/// Strictly normalizes local Sub2API endpoints before requests or browser
/// launches.  No redirect/proxy behavior is introduced here; callers still
/// configure their own HTTP handlers accordingly.
/// </summary>
internal static class LocalGatewayEndpointNormalizer
{
    public static bool TryNormalizeLocalApiBaseUri(
        string? value,
        ILocalGatewayAddressValidator addressValidator,
        out Uri? baseUri)
    {
        baseUri = null;
        if (!TryParseHttpUri(value, out Uri? parsed) ||
            !IsApiRootPath(parsed!) ||
            !addressValidator.IsAddressOnThisComputer(parsed!))
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

    public static bool TryNormalizeLocalDashboardUri(
        string? value,
        ILocalGatewayAddressValidator addressValidator,
        out Uri? dashboardUri)
    {
        dashboardUri = null;
        if (!TryParseHttpUri(value, out Uri? parsed) ||
            !addressValidator.IsAddressOnThisComputer(parsed!))
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

    public static Uri CreateNativeDashboardUri(Uri apiBaseUri)
    {
        ArgumentNullException.ThrowIfNull(apiBaseUri);
        return new UriBuilder(apiBaseUri.Scheme, apiBaseUri.Host, apiBaseUri.Port)
        {
            Path = "/dashboard",
        }.Uri;
    }

    private static bool TryParseHttpUri(string? value, out Uri? uri)
    {
        uri = null;
        if (string.IsNullOrWhiteSpace(value) ||
            !Uri.TryCreate(value.Trim(), UriKind.Absolute, out Uri? parsed) ||
            parsed.Scheme is not ("http" or "https") ||
            string.IsNullOrWhiteSpace(parsed.Host) ||
            !string.IsNullOrWhiteSpace(parsed.UserInfo) ||
            !string.IsNullOrWhiteSpace(parsed.Query) ||
            !string.IsNullOrWhiteSpace(parsed.Fragment))
        {
            return false;
        }

        uri = parsed;
        return true;
    }

    private static bool IsApiRootPath(Uri uri)
    {
        string path = uri.AbsolutePath.Trim('/');
        return path is "" or "v1" or "api/v1";
    }
}
