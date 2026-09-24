using AiSwitchGui;
using LanAi.Workspace.Wpf.Services;
using Xunit;

namespace AiSwitch.Wpf.Tests;

public sealed class RelaySwitchGatewayAdapterTests
{
    [Theory]
    // Loopback relay on this machine.
    [InlineData("http://127.0.0.1:8080/v1", "local-machine", true)]
    [InlineData("http://localhost:8080/v1", "local-machine", true)]
    // Another machine on the LAN still counts as a 共飞 relay.
    [InlineData("http://192.168.31.252:8080/v1", "lan-default", true)]
    [InlineData("http://10.0.0.5:8080/v1", "lan-default", true)]
    [InlineData("http://172.16.4.9:8080/v1", "lan-default", true)]
    // Official endpoints.
    [InlineData("https://api.openai.com/v1", "cloud-default", false)]
    [InlineData("https://chatgpt.com/backend-api", "cloud-default", false)]
    public void BaseUrlDecidesRouting(string baseUrl, string sourceId, bool expected)
    {
        Assert.Equal(expected, RelaySwitchGatewayAdapter.PointsAtRelay(baseUrl, sourceId));
    }

    /// <summary>
    /// The URL carries the requests, so it overrides a stale source id in both
    /// directions — this is what the durability watch depends on.
    /// </summary>
    [Fact]
    public void BaseUrlOverridesAStaleSourceId()
    {
        Assert.False(RelaySwitchGatewayAdapter.PointsAtRelay("https://api.openai.com/v1", "local-machine"));
        Assert.True(RelaySwitchGatewayAdapter.PointsAtRelay("http://127.0.0.1:8080/v1", "cloud-default"));
    }

    [Theory]
    [InlineData("local-machine", true)]
    [InlineData("lan-default", true)]
    [InlineData("cloud-default", false)]
    public void SourceIdIsTheFallbackWhenNoUrlIsRecorded(string sourceId, bool expected)
    {
        // "<missing>" is what LiveStatus reports before any config has been read.
        Assert.Equal(expected, RelaySwitchGatewayAdapter.PointsAtRelay("<missing>", sourceId));
        Assert.Equal(expected, RelaySwitchGatewayAdapter.PointsAtRelay(null, sourceId));
        Assert.Equal(expected, RelaySwitchGatewayAdapter.PointsAtRelay("   ", sourceId));
    }

    [Fact]
    public void SourceIdConstantsMatchTheLegacyDefinitions()
    {
        // Guards against the adapter drifting from the ids the coordinator switches on.
        Assert.Equal("local-machine", ProfileSourceIds.LocalMachine);
        Assert.Equal("lan-default", ProfileSourceIds.LanDefault);
        Assert.Equal("cloud-default", ProfileSourceIds.Cloud);
    }
}
