using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class CodexRouteGuardTests
{
    [Fact]
    public async Task AChangedRouteIsReapplied()
    {
        int applyCount = 0;
        var guard = new CodexRouteGuard(
            routeIsCurrent: () => false,
            reapply: _ =>
            {
                applyCount++;
                return Task.CompletedTask;
            });

        await guard.CheckOnceAsync(CancellationToken.None);

        Assert.Equal(1, applyCount);
    }

    [Fact]
    public async Task ACurrentRouteIsLeftAlone()
    {
        int applyCount = 0;
        var guard = new CodexRouteGuard(
            routeIsCurrent: () => true,
            reapply: _ =>
            {
                applyCount++;
                return Task.CompletedTask;
            });

        await guard.CheckOnceAsync(CancellationToken.None);

        Assert.Equal(0, applyCount);
    }

    [Fact]
    public async Task StoppingCancelsTheWatchWithoutAReapply()
    {
        int applyCount = 0;
        var delayEntered = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var guard = new CodexRouteGuard(
            routeIsCurrent: () => false,
            reapply: _ =>
            {
                applyCount++;
                return Task.CompletedTask;
            },
            delay: async (_, cancellationToken) =>
            {
                delayEntered.SetResult();
                await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
            });

        guard.Start();
        await delayEntered.Task;
        await guard.StopAsync();

        Assert.Equal(0, applyCount);
    }
}
