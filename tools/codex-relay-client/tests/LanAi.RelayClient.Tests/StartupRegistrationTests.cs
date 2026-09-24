using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class StartupRegistrationTests
{
    [Fact]
    public void FirstRunDefaultsToEnabled()
    {
        Assert.True(StartupRegistrationPolicy.DefaultEnabled(null));
    }

    [Fact]
    public void ExplicitDisabledPreferenceStaysDisabled()
    {
        Assert.False(StartupRegistrationPolicy.DefaultEnabled("disabled"));
    }

    [Fact]
    public void StartupCommandQuotesTheExecutablePath()
    {
        Assert.Equal(
            "\"C:\\Program Files\\Gongfei\\LanAi.RelayClient.exe\"",
            StartupRegistrationPolicy.CommandFor("C:\\Program Files\\Gongfei\\LanAi.RelayClient.exe"));
    }
}
