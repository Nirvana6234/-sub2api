using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;

namespace AiSwitch.Wpf.Tests;

public sealed class LocalGatewayEndpointResolverTests
{
    [Fact]
    public async Task ResolveAsync_LoopbackCodexEndpoint_NormalizesCustomPortAndDerivesNativeDashboard()
    {
        var profile = CreateLocalProfile(
            baseUrl: "http://127.0.0.1:8080",
            codexUrl: "http://127.0.0.1:18080/v1");
        var resolver = new ConnectionProfileLocalGatewayEndpointResolver(
            new StubConnectionProfileReader(profile),
            new StubLocalGatewayAddressValidator("127.0.0.1"));

        LocalGatewayEndpointResolution result = await resolver.ResolveAsync(CancellationToken.None);

        Assert.True(result.IsReady);
        Assert.Equal("http://127.0.0.1:18080/", result.ApiBaseUri!.AbsoluteUri);
        Assert.Null(result.DashboardUri);
    }

    [Fact]
    public async Task ResolveAsync_LocalNetworkIpAndConfiguredDashboard_UsesBothCustomPorts()
    {
        const string localIp = "192.168.31.247";
        var profile = CreateLocalProfile(
            baseUrl: $"http://{localIp}:8080",
            codexUrl: $"http://{localIp}:18080/api/v1",
            dashboardUrl: $"http://{localIp}:3300/control");
        var resolver = new ConnectionProfileLocalGatewayEndpointResolver(
            new StubConnectionProfileReader(profile),
            new StubLocalGatewayAddressValidator(localIp));

        LocalGatewayEndpointResolution result = await resolver.ResolveAsync(CancellationToken.None);

        Assert.True(result.IsReady);
        Assert.Equal($"http://{localIp}:18080/", result.ApiBaseUri!.AbsoluteUri);
        Assert.Equal($"http://{localIp}:3300/control", result.DashboardUri!.AbsoluteUri.TrimEnd('/'));
    }

    [Fact]
    public async Task ResolveAsync_RemoteProfileAddress_IsRejectedEvenWhenFixedProfileIdMatches()
    {
        var profile = CreateLocalProfile(
            baseUrl: "https://relay.example.com",
            codexUrl: "https://relay.example.com/v1");
        var resolver = new ConnectionProfileLocalGatewayEndpointResolver(
            new StubConnectionProfileReader(profile),
            new StubLocalGatewayAddressValidator("127.0.0.1"));

        LocalGatewayEndpointResolution result = await resolver.ResolveAsync(CancellationToken.None);

        Assert.Equal(LocalGatewayEndpointResolutionStatus.ApiAddressNotLocal, result.Status);
        Assert.False(result.IsReady);
    }

    [Fact]
    public async Task ResolveAsync_RemoteDashboard_IsNotOpenedAndUsesSafeNativeFallback()
    {
        var profile = CreateLocalProfile(
            baseUrl: "http://127.0.0.1:8080",
            codexUrl: "http://127.0.0.1:8080/v1",
            dashboardUrl: "https://relay.example.com/dashboard");
        var resolver = new ConnectionProfileLocalGatewayEndpointResolver(
            new StubConnectionProfileReader(profile),
            new StubLocalGatewayAddressValidator("127.0.0.1"));

        LocalGatewayEndpointResolution result = await resolver.ResolveAsync(CancellationToken.None);

        Assert.True(result.IsReady);
        Assert.Equal("http://127.0.0.1:8080/dashboard", result.DashboardUri!.AbsoluteUri.TrimEnd('/'));
    }

    [Fact]
    public async Task ResolveAsync_MissingFixedProfile_RequiresConnectionCenterRecovery()
    {
        var resolver = new ConnectionProfileLocalGatewayEndpointResolver(
            new StubConnectionProfileReader(profile: null),
            new StubLocalGatewayAddressValidator("127.0.0.1"));

        LocalGatewayEndpointResolution result = await resolver.ResolveAsync(CancellationToken.None);

        Assert.Equal(LocalGatewayEndpointResolutionStatus.ProfileMissing, result.Status);
        Assert.True(result.RequiresConfigurationFix);
    }

    private static ConnectionProfile CreateLocalProfile(
        string baseUrl,
        string codexUrl,
        string? dashboardUrl = null)
        => new()
        {
            Id = ConnectionProfileIds.LocalMachine,
            Name = "本机中转",
            Kind = ConnectionProfileKind.Local,
            BaseUrl = baseUrl,
            ClientBaseUrls = new Dictionary<CliKind, string>
            {
                [CliKind.Codex] = codexUrl,
            },
            DashboardUrl = dashboardUrl,
        };

    private sealed class StubConnectionProfileReader(ConnectionProfile? profile) : IConnectionProfileReader
    {
        public Task<IReadOnlyList<ConnectionProfile>> GetAllAsync(CancellationToken cancellationToken = default)
            => Task.FromResult<IReadOnlyList<ConnectionProfile>>(profile is null ? [] : [profile]);

        public Task<ConnectionProfile?> GetByIdAsync(string id, CancellationToken cancellationToken = default)
            => Task.FromResult(string.Equals(id, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase)
                ? profile
                : null);
    }

    private sealed class StubLocalGatewayAddressValidator(params string[] acceptedHosts) : ILocalGatewayAddressValidator
    {
        public bool IsAddressOnThisComputer(Uri uri)
            => acceptedHosts.Contains(uri.Host.Trim('[', ']'), StringComparer.OrdinalIgnoreCase);
    }
}
