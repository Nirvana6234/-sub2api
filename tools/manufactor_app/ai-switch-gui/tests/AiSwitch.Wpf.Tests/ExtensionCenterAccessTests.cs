using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class ExtensionCenterAccessTests
{
    [Fact]
    public void ExtensionCenter_RequiresExplicitConfirmationBeforeNavigation()
    {
        int confirmations = 0;
        bool accessConfirmed = false;

        bool denied = MainWindowViewModel.CanNavigateToExtensionCenter(
            "extensions",
            ref accessConfirmed,
            () =>
            {
                confirmations++;
                return false;
            });

        Assert.False(denied);
        Assert.Equal(1, confirmations);
        Assert.False(accessConfirmed);

        bool accepted = MainWindowViewModel.CanNavigateToExtensionCenter(
            "extensions",
            ref accessConfirmed,
            () =>
            {
                confirmations++;
                return true;
            });

        Assert.True(accepted);
        Assert.Equal(2, confirmations);
        Assert.True(accessConfirmed);
    }

    [Fact]
    public void AcceptedWarning_IsRememberedForTheCurrentApplicationRun()
    {
        int confirmations = 0;
        bool accessConfirmed = false;
        Func<bool> confirmation = () =>
        {
            confirmations++;
            return true;
        };

        Assert.True(MainWindowViewModel.CanNavigateToExtensionCenter(
            "extensions",
            ref accessConfirmed,
            confirmation));
        Assert.True(accessConfirmed);
        Assert.Equal(1, confirmations);

        Assert.True(MainWindowViewModel.CanNavigateToExtensionCenter(
            "stats",
            ref accessConfirmed,
            () => throw new InvalidOperationException("普通页面不应触发扩展中心警告。")));
        Assert.True(MainWindowViewModel.CanNavigateToExtensionCenter(
            "extensions",
            ref accessConfirmed,
            () => throw new InvalidOperationException("本次运行已经确认，不应再次警告。")));
        Assert.Equal(1, confirmations);
    }

    [Fact]
    public void WarningCopy_StatesTheIrreversibleAndSevereConsequences()
    {
        Assert.Contains("Codex", MainWindowViewModel.ExtensionCenterWarningMessage, StringComparison.Ordinal);
        Assert.Contains("Claude Code", MainWindowViewModel.ExtensionCenterWarningMessage, StringComparison.Ordinal);
        Assert.Contains("Gemini CLI", MainWindowViewModel.ExtensionCenterWarningMessage, StringComparison.Ordinal);
        Assert.Contains("无法自动恢复", MainWindowViewModel.ExtensionCenterWarningMessage, StringComparison.Ordinal);
        Assert.Contains("严重后果", MainWindowViewModel.ExtensionCenterWarningMessage, StringComparison.Ordinal);
        Assert.Equal("进入扩展中心前请确认", MainWindowViewModel.ExtensionCenterWarningTitle);
    }
}
