using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class ClientShutdownCoordinatorTests
{
    [Fact]
    public void ProcessExitFallbackCompletesReleaseBeforeReturning()
    {
        bool released = false;
        var coordinator = new ClientShutdownCoordinator(async () =>
        {
            await Task.Yield();
            released = true;
        });

        bool finished = coordinator.ReleaseBeforeProcessExit(TimeSpan.FromSeconds(10));

        Assert.True(finished);
        Assert.True(released);
    }

    [Fact]
    public void AReleaseThatNeverFinishesDoesNotHoldTheExitForever()
    {
        // The cleanup stuck on something that never answers (a socket, a child process): exit runs
        // on the UI thread, so waiting without a limit is an application that spins until it is
        // force-quit.
        var coordinator = new ClientShutdownCoordinator(() => new TaskCompletionSource().Task);

        var clock = System.Diagnostics.Stopwatch.StartNew();
        bool finished = coordinator.ReleaseBeforeProcessExit(TimeSpan.FromMilliseconds(200));
        clock.Stop();

        Assert.False(finished);
        Assert.True(clock.Elapsed < TimeSpan.FromSeconds(5), $"waited {clock.Elapsed}");
    }

    [Fact]
    public async Task TrayExitAndProcessExitFallbackShareOneRelease()
    {
        int releaseCount = 0;
        var coordinator = new ClientShutdownCoordinator(() =>
        {
            releaseCount++;
            return Task.CompletedTask;
        });

        await coordinator.ReleaseAsync();
        coordinator.ReleaseBeforeProcessExit(TimeSpan.FromSeconds(10));

        Assert.Equal(1, releaseCount);
    }
}
