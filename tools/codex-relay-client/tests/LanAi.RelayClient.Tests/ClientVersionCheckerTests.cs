using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class ClientVersionCheckerTests
{
    private const string DownloadPage = ClientOptions.ServerAddress + "download";

    private static Func<CancellationToken, Task<PublicSettings>> Serving(
        string? windows = null,
        string? mac = null,
        bool downloadEnabled = true) =>
        _ => Task.FromResult(new PublicSettings(
            clientDownloadEnabled: downloadEnabled,
            clientLatestVersion: windows,
            clientLatestVersionMac: mac));

    [Fact]
    public async Task OffersTheWindowsVersionWhenItIsNewer()
    {
        var checker = new ClientVersionChecker(Serving(windows: "0.2"), new Version(0, 1), isMacOS: false);

        ClientUpdateInfo? update = await checker.CheckAsync();

        Assert.NotNull(update);
        Assert.Equal("Ver0.2", update.VersionLabel);
        Assert.Equal(new Uri(DownloadPage), update.DownloadPage);
    }

    /// <remarks>
    /// Both fields are populated with different numbers, so a checker that reads the
    /// wrong one still returns an update and only the version gives it away. The two
    /// platforms do not ship together — this is what stops a Windows release from
    /// telling every Mac user to upgrade to something that does not exist for them.
    /// </remarks>
    [Theory]
    [InlineData(false, "Ver0.5")]
    [InlineData(true, "Ver0.3")]
    public async Task EachPlatformReadsItsOwnField(bool isMacOS, string expected)
    {
        var checker = new ClientVersionChecker(
            Serving(windows: "0.5", mac: "0.3"), new Version(0, 1), isMacOS);

        ClientUpdateInfo? update = await checker.CheckAsync();

        Assert.Equal(expected, update?.VersionLabel);
    }

    /// <remarks>
    /// The regression this whole normalisation exists for. .NET orders <c>0.2.0</c>
    /// above <c>0.2</c> — an absent build component is -1, not 0 — so this setting
    /// would nag every user of 0.2 to install 0.2, forever, with the offered number
    /// rendering identically to the one already on screen.
    ///
    /// Written as literals on purpose: the test this replaced built its manifest from
    /// <c>$"{Major}.{Minor}"</c>, so it could not reach this case at all.
    /// </remarks>
    [Theory]
    [InlineData("0.2")]
    [InlineData("0.2.0")]
    [InlineData("0.2.0.0")]
    public async Task ATrailingZeroDoesNotOutrankTheSameVersion(string advertised)
    {
        var checker = new ClientVersionChecker(Serving(windows: advertised), new Version(0, 2), isMacOS: false);

        Assert.Null(await checker.CheckAsync());
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    [InlineData("not-a-version")]
    [InlineData("0.1")]
    public async Task NothingIsOfferedForAbsentOrOlderOrMalformedValues(string? advertised)
    {
        var checker = new ClientVersionChecker(Serving(windows: advertised), new Version(0, 2), isMacOS: false);

        Assert.Null(await checker.CheckAsync());
    }

    /// <remarks>
    /// The banner's only action opens <c>/download</c>, and that route refuses to
    /// render while the switch is off. An update the user is told about but cannot
    /// act on is worse than no banner at all.
    /// </remarks>
    [Fact]
    public async Task NoUpdateIsOfferedWhileTheDownloadPageIsDisabled()
    {
        var checker = new ClientVersionChecker(
            Serving(windows: "9.9", downloadEnabled: false), new Version(0, 2), isMacOS: false);

        Assert.Null(await checker.CheckAsync());
    }

    /// <remarks>
    /// Deliberately silent: this feeds a non-blocking banner, and a client that cannot
    /// reach the relay has worse things to report. The cost of that choice is that a
    /// broken version channel is indistinguishable from being up to date — which is
    /// exactly how the previous one stayed broken in production unnoticed.
    /// </remarks>
    [Fact]
    public async Task AFailedSettingsFetchIsTreatedAsNoUpdate()
    {
        var checker = new ClientVersionChecker(
            _ => throw new RelayApiException(RelayFailure.NetworkUnreachable, "网络不可用"),
            new Version(0, 2),
            isMacOS: false);

        Assert.Null(await checker.CheckAsync());
    }

    /// <remarks>
    /// The displayed version is asserted against <see cref="ClientOptions.CurrentVersion"/>
    /// rather than a literal. It used to read <c>"Ver0.1"</c>, which pinned the
    /// hardcoded string the view model returned — so the test passed for exactly as
    /// long as nobody released anything. Written this way it fails only if the screen
    /// and the update check disagree, which is the thing worth catching.
    /// </remarks>
    [Fact]
    public async Task UpdateViewModelShowsThisBuildsVersionAndTheOfferedOne()
    {
        var update = new ClientUpdateInfo(new Version(0, 3), new Uri(DownloadPage));
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
    /// behind what the server advertises offers its users an update to the release
    /// they are already running, forever.
    /// </remarks>
    [Fact]
    public async Task AnAdvertisedVersionMatchingThisBuildOffersNoUpdate()
    {
        Version current = ClientOptions.CurrentVersion;
        var checker = new ClientVersionChecker(
            Serving(windows: $"{current.Major}.{current.Minor}"), current, isMacOS: false);

        Assert.Null(await checker.CheckAsync());
    }
}
