using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class ClientVersionCheckerTests
{
    private const string DownloadPage = ClientOptions.ServerAddress + "download";
    private const string WindowsPackagePage = ClientOptions.ServerAddress + "api/v1/download/client";

    private static Func<CancellationToken, Task<PublicSettings>> Serving(
        string? windows = null,
        string? mac = null,
        string? macPackageUrl = null,
        bool downloadEnabled = true) =>
        _ => Task.FromResult(new PublicSettings(
            clientDownloadEnabled: downloadEnabled,
            clientLatestVersion: windows,
            clientLatestVersionMac: mac,
            clientDownloadDirectUrlMac: macPackageUrl));

    [Fact]
    public async Task OffersTheWindowsVersionWhenItIsNewer()
    {
        var checker = new ClientVersionChecker(Serving(windows: "0.2"), new Version(0, 1), isMacOS: false);

        ClientCheckResult result = await checker.CheckAsync();

        Assert.Equal(ClientCheckStatus.Available, result.Status);
        Assert.Equal("Ver0.2", result.Update?.VersionLabel);
        Assert.Equal(new Uri(DownloadPage), result.Update?.DownloadPage);
    }

    /// <summary>
    /// Windows gets a channel it can act on by itself — a package URL, and nothing more is
    /// needed to try a self-replace.
    /// </summary>
    [Fact]
    public async Task WindowsGetsASelfReplaceChannelPointedAtTheDownloadProxy()
    {
        var checker = new ClientVersionChecker(Serving(windows: "0.2"), new Version(0, 1), isMacOS: false);

        ClientUpdateInfo? update = (await checker.CheckAsync()).Update;

        Assert.Equal(ClientUpdateChannel.SelfReplace, update?.Channel);
        Assert.Equal(new Uri(WindowsPackagePage), update?.PackageUrl);
        Assert.Null(update?.TerminalCommand);
    }

    /// <summary>
    /// The install script is derived from the package link's own directory — same convention
    /// as ClientDownloadView.vue's macInstallScriptUrl, since the release pipeline publishes
    /// both under one path.
    /// </summary>
    [Fact]
    public async Task MacGetsATerminalCommandDerivedFromThePackageDirectory()
    {
        var checker = new ClientVersionChecker(
            Serving(mac: "0.2", macPackageUrl: "https://download.example.com/downloads/client_v0.2_macos-arm64.tar.gz"),
            new Version(0, 1),
            isMacOS: true);

        ClientUpdateInfo? update = (await checker.CheckAsync()).Update;

        Assert.Equal(ClientUpdateChannel.RunInTerminal, update?.Channel);
        Assert.Equal(
            "curl -fsSL https://download.example.com/downloads/install-mac.sh | bash",
            update?.TerminalCommand);
        Assert.Null(update?.PackageUrl);
    }

    /// <summary>
    /// No package link means mac has not shipped yet — offering a command that 404s is worse
    /// than sending the user to the download page, which says so.
    /// </summary>
    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("not-a-url")]
    [InlineData("javascript:alert(1)")]
    public async Task MacFallsBackToTheDownloadPageWithoutAUsablePackageLink(string? macPackageUrl)
    {
        var checker = new ClientVersionChecker(
            Serving(mac: "0.2", macPackageUrl: macPackageUrl), new Version(0, 1), isMacOS: true);

        ClientUpdateInfo? update = (await checker.CheckAsync()).Update;

        Assert.Equal(ClientUpdateChannel.OpenDownloadPage, update?.Channel);
        Assert.Null(update?.TerminalCommand);
        Assert.Null(update?.PackageUrl);
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
            Serving(windows: "0.5", mac: "0.3", macPackageUrl: "https://d.example/x.tar.gz"), new Version(0, 1), isMacOS);

        ClientCheckResult result = await checker.CheckAsync();

        Assert.Equal(expected, result.Update?.VersionLabel);
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

        Assert.Equal(ClientCheckStatus.UpToDate, (await checker.CheckAsync()).Status);
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

        ClientCheckResult result = await checker.CheckAsync();
        Assert.Equal(ClientCheckStatus.UpToDate, result.Status);
        Assert.Null(result.Update);
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

        Assert.Equal(ClientCheckStatus.ChannelDisabled, (await checker.CheckAsync()).Status);
    }

    /// <remarks>
    /// Distinct from <see cref="NoUpdateIsOfferedWhileTheDownloadPageIsDisabled"/> and
    /// <see cref="ATrailingZeroDoesNotOutrankTheSameVersion"/> on purpose: a button the user
    /// just pressed must not describe a network failure the same way it describes "you are
    /// current" or "downloads are off" — that silence is exactly how the previous version
    /// channel stayed broken in production, unnoticed, because every failure path answered
    /// "no update" and nothing distinguished them.
    /// </remarks>
    [Fact]
    public async Task AFailedSettingsFetchIsToldApartFromNoUpdate()
    {
        var checker = new ClientVersionChecker(
            _ => throw new RelayApiException(RelayFailure.NetworkUnreachable, "网络不可用"),
            new Version(0, 2),
            isMacOS: false);

        ClientCheckResult result = await checker.CheckAsync();
        Assert.Equal(ClientCheckStatus.CheckFailed, result.Status);
        Assert.Null(result.Update);
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

        Assert.Equal(ClientCheckStatus.UpToDate, (await checker.CheckAsync()).Status);
    }
}
