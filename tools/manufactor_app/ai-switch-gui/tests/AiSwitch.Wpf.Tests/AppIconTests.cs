using LanAi.Workspace.Wpf.Controls;

namespace AiSwitch.Wpf.Tests;

public sealed class AppIconTests
{
    [Theory]
    [InlineData("Overview")]
    [InlineData("Stats")]
    [InlineData("Projects")]
    [InlineData("Chat")]
    [InlineData("History")]
    [InlineData("Connections")]
    [InlineData("Plus")]
    [InlineData("Settings")]
    [InlineData("ChevronRight")]
    [InlineData("Bell")]
    [InlineData("Help")]
    [InlineData("Minimize")]
    [InlineData("Maximize")]
    [InlineData("Close")]
    [InlineData("Computer")]
    [InlineData("ArrowRight")]
    [InlineData("Network")]
    [InlineData("Info")]
    [InlineData("Warning")]
    [InlineData("LocalGateway")]
    [InlineData("LanGateway")]
    [InlineData("CloudGateway")]
    [InlineData("Send")]
    public void BuiltInIconKey_IsBackedByEmbeddedVectorGeometry(string kind)
    {
        Assert.True(AppIcon.IsSupported(kind));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("unknown-icon")]
    public void UnknownIconKey_IsNotRendered(string? kind)
    {
        Assert.False(AppIcon.IsSupported(kind));
    }
}
