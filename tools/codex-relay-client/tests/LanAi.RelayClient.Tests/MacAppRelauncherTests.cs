using LanAi.RelayClient.Platform.MacOS;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>Which bundle 「重新启动助手」 opens again on macOS.</summary>
public sealed class MacAppRelauncherTests
{
    [Theory]
    [InlineData("/Applications/共飞-ChatGPT助手.app/Contents/MacOS/", "共飞-ChatGPT助手.app")]
    [InlineData("/Users/me/Applications/共飞-ChatGPT助手.app/Contents/MacOS", "共飞-ChatGPT助手.app")]
    public void TheBundleIsTwoLevelsAboveTheExecutable(string baseDirectory, string bundle) =>
        Assert.Equal(bundle, Path.GetFileName(MacAppRelauncher.BundleOf(baseDirectory)));

    [Theory]
    [InlineData("/Users/me/dev/bin/Release/net8.0/")]
    [InlineData("/Users/me/Something/Contents/MacOS/")]
    public void OutsideABundleThereIsNothingToOpen(string baseDirectory) =>
        Assert.Null(MacAppRelauncher.BundleOf(baseDirectory));
}
