using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class RelayEndpointResolverTests
{
    [Fact]
    public void EmptyAdvertisedAddressFallsBackToConnectedServer()
    {
        Assert.Equal(
            "http://192.168.216.1:8080/v1",
            RelayEndpointResolver.ResolveApiBaseUrl("http://192.168.216.1:8080/", null));
    }

    [Fact]
    public void LoopbackAdvertisedAddressUsesConnectedServerAndKeepsPath()
    {
        Assert.Equal(
            "http://192.168.216.1:8080/v1",
            RelayEndpointResolver.ResolveApiBaseUrl(
                "http://192.168.216.1:8080/",
                "http://127.0.0.1:8080/v1"));
    }

    [Fact]
    public void ExplicitPublicAddressRemainsAuthoritative()
    {
        Assert.Equal(
            "https://relay.example.com/openai/v1",
            RelayEndpointResolver.ResolveApiBaseUrl(
                "http://192.168.216.1:8080/",
                "https://relay.example.com/openai/v1"));
    }

    /// <summary>
    /// The resolved endpoint is written into Codex's config and then carries the
    /// relay API key on every request. A client that reached the server over
    /// https must not be talked down to plaintext by what the server advertises.
    /// </summary>
    [Fact]
    public void AnAdvertisedPlaintextEndpointCannotDowngradeAnHttpsClient()
    {
        Assert.Equal(
            "https://relay.example.com/openai/v1",
            RelayEndpointResolver.ResolveApiBaseUrl(
                "https://gongfeiai.com/",
                "http://relay.example.com/openai/v1"));
    }

    /// <summary>An explicit :80 must not survive as "https://host:80".</summary>
    [Fact]
    public void UpgradingDropsThePlaintextDefaultPort()
    {
        Assert.Equal(
            "https://relay.example.com/v1",
            RelayEndpointResolver.ResolveApiBaseUrl(
                "https://gongfeiai.com/",
                "http://relay.example.com:80/v1"));
    }

    /// <summary>A non-default port belongs to the endpoint and is kept.</summary>
    [Fact]
    public void UpgradingKeepsAnExplicitNonDefaultPort()
    {
        Assert.Equal(
            "https://relay.example.com:8443/v1",
            RelayEndpointResolver.ResolveApiBaseUrl(
                "https://gongfeiai.com/",
                "http://relay.example.com:8443/v1"));
    }

    /// <summary>
    /// The guard only preserves security the connection already had. A build
    /// pointed at an http server keeps talking http rather than inventing TLS
    /// the server may not serve.
    /// </summary>
    [Fact]
    public void AnHttpClientIsNotForcedOntoHttps()
    {
        Assert.Equal(
            "http://relay.example.com/v1",
            RelayEndpointResolver.ResolveApiBaseUrl(
                "http://192.168.216.1:8080/",
                "http://relay.example.com/v1"));
    }

    /// <summary>The production build advertises nothing, so it falls back to https.</summary>
    [Fact]
    public void ProductionFallbackStaysOnHttps()
    {
        Assert.Equal(
            "https://gongfeiai.com/v1",
            RelayEndpointResolver.ResolveApiBaseUrl("https://gongfeiai.com/", null));
    }
}
