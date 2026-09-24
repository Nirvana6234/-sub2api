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

        coordinator.ReleaseBeforeProcessExit();

        Assert.True(released);
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
        coordinator.ReleaseBeforeProcessExit();

        Assert.Equal(1, releaseCount);
    }
}
