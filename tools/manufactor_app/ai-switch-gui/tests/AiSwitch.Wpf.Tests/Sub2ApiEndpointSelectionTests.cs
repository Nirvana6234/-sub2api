using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;

namespace AiSwitch.Wpf.Tests;

public sealed class Sub2ApiEndpointSelectionTests
{
    [Fact]
    public void GetCandidates_UsesOnlyTheAppliedCurrentSource()
    {
        ConnectionProfile[] connections =
        [
            Create(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local, "http://127.0.0.1:8080/v1"),
            Create(ConnectionProfileIds.LanDefault, "局域网中转", ConnectionProfileKind.Lan, "http://192.168.31.8:8080/v1"),
            Create("cloud-a", "云端 A", ConnectionProfileKind.Cloud, "https://relay.example.test/v1"),
        ];

        IReadOnlyList<Sub2ApiEndpointTarget> candidates = Sub2ApiEndpointSelector.GetCandidates(
            connections,
            new ConnectionProfileSelection("cloud-a", ConnectionProfileIds.LanDefault, "cloud-a"));

        Assert.Equal(["cloud-a"], candidates.Select(candidate => candidate.ProfileId));
        Assert.Equal("https://relay.example.test/", candidates[0].ApiBaseUri.AbsoluteUri);
        Assert.Equal("https://relay.example.test/", candidates[0].DashboardUri?.AbsoluteUri);
    }

    [Fact]
    public void GetCandidates_UnifiedAppliedRoutingOverridesStaleActiveSelection()
    {
        ConnectionProfile[] connections =
        [
            Create(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local, "http://127.0.0.1:8080/v1"),
            Create("cloud-a", "云端 A", ConnectionProfileKind.Cloud, "https://relay.example.test/v1"),
        ];

        IReadOnlyList<Sub2ApiEndpointTarget> candidates = Sub2ApiEndpointSelector.GetCandidates(
            connections,
            new ConnectionProfileSelection("cloud-a", ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine),
            new ConnectionProfileRouting("cloud-a", "cloud-a", "cloud-a"));

        Assert.Equal("cloud-a", Assert.Single(candidates).ProfileId);
    }

    [Fact]
    public void GetCandidates_UsesPublicHttpPrimarySourceAndRequiresConfirmation()
    {
        var source = new ConnectionProfile
        {
            Id = "cloud-mixed-endpoints",
            Name = "远程来源",
            Kind = ConnectionProfileKind.Cloud,
            BaseUrl = string.Empty,
            ClientBaseUrls = new Dictionary<CliKind, string>
            {
                [CliKind.Codex] = "http://public-insecure.example/v1",
                [CliKind.ClaudeCode] = "https://secure-relay.example/v1",
                [CliKind.GeminiCli] = "https://secure-relay.example",
            },
        };

        IReadOnlyList<Sub2ApiEndpointTarget> candidates = Sub2ApiEndpointSelector.GetCandidates(
            [source],
            new ConnectionProfileSelection("cloud-mixed-endpoints", null, "cloud-mixed-endpoints"));

        Sub2ApiEndpointTarget target = Assert.Single(candidates);
        Assert.Equal("http://public-insecure.example/", target.ApiBaseUri.AbsoluteUri);
        Assert.True(target.RequiresInsecureLoginConfirmation);
    }

    [Theory]
    [InlineData("https://relay.example.test/v1", true)]
    [InlineData("http://192.168.1.20:8080/v1", true)]
    [InlineData("http://relay.example.test/v1", false)]
    [InlineData("ftp://192.168.1.20/v1", false)]
    public void TryNormalizeApiBaseUri_EnforcesSecureCloudOrPrivateLan(string value, bool expected)
    {
        bool valid = Sub2ApiEndpointNormalizer.TryNormalizeApiBaseUri(value, out Uri? normalized);

        Assert.Equal(expected, valid);
        Assert.Equal(expected, normalized is not null);
    }

    [Fact]
    public void TryNormalizeApiBaseUri_AllowsPublicHttpOnlyWhenExplicitlyRequested()
    {
        Assert.True(Sub2ApiEndpointNormalizer.TryNormalizeApiBaseUri(
            "http://relay.example.test/v1",
            allowPublicHttp: true,
            out Uri? normalized));
        Assert.Equal("http://relay.example.test/", normalized!.AbsoluteUri);
    }

    private static ConnectionProfile Create(
        string id,
        string name,
        ConnectionProfileKind kind,
        string baseUrl)
        => new()
        {
            Id = id,
            Name = name,
            Kind = kind,
            BaseUrl = baseUrl,
        };
}
