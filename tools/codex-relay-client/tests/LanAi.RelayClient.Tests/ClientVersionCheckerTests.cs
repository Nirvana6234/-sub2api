using System.Net;
using System.Net.Http;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class ClientVersionCheckerTests
{
    [Fact]
    public async Task ReturnsUpdateWhenManifestVersionIsNewer()
    {
        using var http = new HttpClient(new StaticResponseHandler("""
            {"version":"0.2","download_page":"/download/client","release_notes":"测试更新"}
            """))
        {
            BaseAddress = new Uri("https://relay.test/"),
        };
        var checker = new ClientVersionChecker(http, new Version(0, 1));

        ClientUpdateInfo? update = await checker.CheckAsync();

        Assert.NotNull(update);
        Assert.Equal("Ver0.2", update.VersionLabel);
        Assert.Equal(new Uri("https://relay.test/download/client"), update.DownloadPage);
    }

    [Fact]
    public async Task IgnoresCurrentAndMalformedVersions()
    {
        using var currentHttp = new HttpClient(new StaticResponseHandler("{" + "\"version\":\"0.1\"}"))
        {
            BaseAddress = new Uri("https://relay.test/"),
        };
        using var malformedHttp = new HttpClient(new StaticResponseHandler("{\"version\":\"not-a-version\"}"))
        {
            BaseAddress = new Uri("https://relay.test/"),
        };

        Assert.Null(await new ClientVersionChecker(currentHttp, new Version(0, 1)).CheckAsync());
        Assert.Null(await new ClientVersionChecker(malformedHttp, new Version(0, 1)).CheckAsync());
    }

    /// <remarks>
    /// The displayed version is asserted against <see cref="ClientOptions.CurrentVersion"/>
    /// rather than a literal. It used to read <c>"Ver0.1"</c>, which pinned the
    /// hardcoded string the view model returned — so the test passed for exactly as
    /// long as nobody released anything, and had to be edited by hand on the first
    /// version bump. Written this way it fails only if the screen and the update check
    /// disagree, which is the thing worth catching.
    /// </remarks>
    [Fact]
    public async Task UpdateViewModelShowsThisBuildsVersionAndTheOfferedOne()
    {
        var update = new ClientUpdateInfo(
            new Version(0, 3),
            new Uri("https://relay.test/download/client"),
            "测试更新");
        var viewModel = new ClientUpdateViewModel(_ => Task.FromResult<ClientUpdateInfo?>(update));

        await viewModel.CheckAsync();

        Version current = ClientOptions.CurrentVersion;
        Assert.Equal($"Ver{current.Major}.{current.Minor}", viewModel.CurrentVersionText);
        Assert.True(viewModel.HasUpdate);
        Assert.Equal("发现新版本 Ver0.3，点击更新", viewModel.UpdateMessage);
        Assert.Equal(update.DownloadPage, viewModel.DownloadPage);
    }

    /// <remarks>
    /// The release-checklist item this pins: a build whose own version has fallen
    /// behind the manifest offers its users an update to the release they are already
    /// running, forever. The manifest is served, so it cannot be asserted here — what
    /// can be is that the client stops offering an update once the two agree.
    /// </remarks>
    [Fact]
    public async Task AManifestMatchingThisBuildOffersNoUpdate()
    {
        Version current = ClientOptions.CurrentVersion;
        string manifest =
            $$"""{"version":"{{current.Major}}.{{current.Minor}}","download_page":"/download"}""";
        using var http = new HttpClient(new StaticResponseHandler(manifest))
        {
            BaseAddress = new Uri("https://relay.test/"),
        };

        Assert.Null(await new ClientVersionChecker(http, current).CheckAsync());
    }

    private sealed class StaticResponseHandler(string json) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) =>
            Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent(json),
            });
    }
}
