using LanAi.Workspace.Injection.Sentinel;
using LanAi.Workspace.Wpf.ViewModels;
using Xunit;

namespace AiSwitch.Wpf.Tests;

public sealed class RelaySwitchPromptViewModelTests
{
    private static RelaySwitchPrompt Prompt(RelaySwitchReason reason, string? resetText = null)
        => new(
            reason,
            new CodexLimitSnapshot(
                reason == RelaySwitchReason.LimitReached ? CodexLimitLevel.Reached : CodexLimitLevel.Approaching,
                new CodexLimitFacts { ResetText = resetText },
                DateTimeOffset.UtcNow),
            null);

    private static RelaySwitchPromptViewModel Create(
        RelaySwitchOutcome? outcome = null,
        Action? onDecline = null)
        => new(
            _ => Task.FromResult(outcome ?? new RelaySwitchOutcome(true, "已切换到本机中转")),
            onDecline ?? (() => { }));

    /// <summary>
    /// The two facts that drive the decision must always be stated: history survives,
    /// cloud tasks pause.
    /// </summary>
    [Fact]
    public void ReachedMessageStatesHistoryIsKeptAndCloudTasksPause()
    {
        var (title, message, accept) = RelaySwitchPromptViewModel.Describe(
            Prompt(RelaySwitchReason.LimitReached));

        Assert.Contains("已用尽", title);
        Assert.Contains("聊天记录与记忆会完整保留", message);
        Assert.Contains("云端任务会暂停", message);
        Assert.Contains("共飞中转", accept);
    }

    [Fact]
    public void ApproachingMessageFramesItAsAvoidingInterruption()
    {
        var (title, message, _) = RelaySwitchPromptViewModel.Describe(
            Prompt(RelaySwitchReason.ApproachingLimit));

        Assert.Contains("接近上限", title);
        Assert.Contains("避免中断", message);
        Assert.Contains("聊天记录与记忆会完整保留", message);
    }

    /// <summary>
    /// A clobbered route is not the user's doing, so the wording explains the cause
    /// rather than implying they ran out of allowance.
    /// </summary>
    [Fact]
    public void RoutingLostMessageExplainsTheCause()
    {
        var (title, message, accept) = RelaySwitchPromptViewModel.Describe(
            new RelaySwitchPrompt(RelaySwitchReason.RoutingLost, null, null));

        Assert.Contains("路由被重置", title);
        Assert.Contains("重写了配置", message);
        Assert.Contains("重新登录", message);
        Assert.Contains("重新应用", accept);
    }

    [Fact]
    public void ShowPopulatesAndRevealsTheCard()
    {
        var viewModel = Create();

        viewModel.Show(Prompt(RelaySwitchReason.LimitReached, "用量重置于 15:00"));

        Assert.True(viewModel.IsVisible);
        Assert.Contains("已用尽", viewModel.Title);
        Assert.Contains("用量重置于 15:00", viewModel.ResetHint);
        Assert.Null(viewModel.OutcomeMessage);
    }

    [Fact]
    public void ResetHintIsOmittedWhenThePageDidNotProvideOne()
    {
        var viewModel = Create();

        viewModel.Show(Prompt(RelaySwitchReason.LimitReached));

        Assert.Equal(string.Empty, viewModel.ResetHint);
    }

    [Fact]
    public async Task AcceptingASuccessfulSwitchClosesTheCard()
    {
        var viewModel = Create(new RelaySwitchOutcome(true, "已切换到本机中转"));
        viewModel.Show(Prompt(RelaySwitchReason.LimitReached));

        await viewModel.AcceptCommand.ExecuteAsync(null);

        Assert.False(viewModel.IsVisible);
        Assert.Equal("已切换到本机中转", viewModel.OutcomeMessage);
        Assert.False(viewModel.IsBusy);
    }

    /// <summary>
    /// A failed switch must stay on screen — silently closing would leave the user
    /// believing they had been switched over.
    /// </summary>
    [Fact]
    public async Task AFailedSwitchKeepsTheCardVisibleWithTheReason()
    {
        var viewModel = Create(new RelaySwitchOutcome(false, "校验失败：中转不可达"));
        viewModel.Show(Prompt(RelaySwitchReason.LimitReached));

        await viewModel.AcceptCommand.ExecuteAsync(null);

        Assert.True(viewModel.IsVisible);
        Assert.Contains("中转不可达", viewModel.OutcomeMessage);
    }

    [Fact]
    public void DismissingReportsTheDeclineSoTheEpisodeStaysQuiet()
    {
        var declined = 0;
        var viewModel = Create(onDecline: () => declined++);
        viewModel.Show(Prompt(RelaySwitchReason.LimitReached));

        viewModel.DismissCommand.Execute(null);

        Assert.Equal(1, declined);
        Assert.False(viewModel.IsVisible);
    }
}
