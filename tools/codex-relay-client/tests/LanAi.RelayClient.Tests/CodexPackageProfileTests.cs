using System.Runtime.InteropServices;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Pins the host-to-package mapping.
/// </summary>
/// <remarks>
/// Worth testing precisely because the failure is quiet: the mirror serves whatever
/// path it is asked for, so a wrong mapping downloads successfully, reports success,
/// and produces a file the machine cannot open. Nothing throws.
/// </remarks>
public sealed class CodexPackageProfileTests
{
    [Theory]
    [InlineData("Windows", Architecture.X64, "win-x64")]
    [InlineData("Windows", Architecture.Arm64, "win-arm64")]
    [InlineData("MacOS", Architecture.Arm64, "mac-arm64")]
    [InlineData("MacOS", Architecture.X64, "mac-intel")]
    public void EachHostGetsItsOwnMirrorPath(
        string platform, Architecture architecture, string expected)
    {
        CodexPackageProfile? profile = CodexPackageProfile.Resolve(Host(platform), architecture);

        Assert.Equal(expected, profile!.MirrorPath);
        Assert.Equal($"https://codexapp.agentsmirror.com/latest/{expected}", profile.DownloadUri.ToString());
    }

    /// <remarks>
    /// The bug this whole type replaced: before it, every host asked for
    /// <c>win-x64</c>. If these two ever converge again, that bug is back.
    /// </remarks>
    [Fact]
    public void AMacNeverAsksForAWindowsBuild()
    {
        CodexPackageProfile? mac = CodexPackageProfile.Resolve(Host("MacOS"), Architecture.Arm64);
        CodexPackageProfile? windows = CodexPackageProfile.Resolve(Host("Windows"), Architecture.X64);

        Assert.DoesNotContain("win", mac!.MirrorPath, StringComparison.Ordinal);
        Assert.NotEqual(windows!.DownloadUri, mac.DownloadUri);
    }

    [Fact]
    public void MacPackagesAreDiskImagesAndWindowsPackagesAreNot()
    {
        CodexPackageProfile mac = CodexPackageProfile.Resolve(Host("MacOS"), Architecture.Arm64)!;
        CodexPackageProfile windows = CodexPackageProfile.Resolve(Host("Windows"), Architecture.X64)!;

        Assert.Contains(".dmg", mac.SupportedExtensions);
        Assert.DoesNotContain(".msix", mac.SupportedExtensions);
        Assert.EndsWith(".dmg", mac.DownloadFileName, StringComparison.Ordinal);
        Assert.True(mac.IsMac);

        Assert.Contains(".msix", windows.SupportedExtensions);
        Assert.DoesNotContain(".dmg", windows.SupportedExtensions);
        Assert.False(windows.IsMac);
    }

    /// <remarks>
    /// Null, not a Windows fallback. A host with no published build must fail to
    /// download rather than download something it cannot run.
    /// </remarks>
    [Theory]
    [InlineData("Other", Architecture.X64)]
    [InlineData("Other", Architecture.Arm64)]
    [InlineData("Windows", Architecture.X86)]
    [InlineData("MacOS", Architecture.Armv6)]
    public void AnUnpublishedHostResolvesToNothing(string platform, Architecture architecture)
    {
        Assert.Null(CodexPackageProfile.Resolve(Host(platform), architecture));
    }

    /// <remarks>
    /// The enum is internal to Core, and a public xUnit test method cannot take an
    /// internal parameter type — hence the string in the data and the parse here.
    /// </remarks>
    private static CodexHostPlatform Host(string name) => Enum.Parse<CodexHostPlatform>(name);

    [Fact]
    public void ThisMachineResolvesToTheWindowsBuild()
    {
        // The suite runs on Windows; this asserts the live path agrees with the table
        // rather than only the table agreeing with itself.
        CodexPackageProfile? profile = CodexPackageProfile.ForCurrentPlatform();

        Assert.NotNull(profile);
        Assert.StartsWith("win-", profile!.MirrorPath, StringComparison.Ordinal);
    }
}
