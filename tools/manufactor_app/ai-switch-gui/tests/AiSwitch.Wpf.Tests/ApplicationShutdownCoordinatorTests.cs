using System.Diagnostics;
using LanAi.Workspace.Wpf.Services;

namespace AiSwitch.Wpf.Tests;

public sealed class ApplicationShutdownCoordinatorTests
{
    [Fact]
    public async Task RunAsync_ReturnsWithinLimit_WhenCleanupDoesNotComplete()
    {
        var neverCompletes = new TaskCompletionSource(
            TaskCreationOptions.RunContinuationsAsynchronously);
        TimeSpan limit = TimeSpan.FromMilliseconds(120);
        var stopwatch = Stopwatch.StartNew();

        await ApplicationShutdownCoordinator.RunAsync(limit, neverCompletes.Task);

        stopwatch.Stop();
        Assert.True(stopwatch.Elapsed < TimeSpan.FromSeconds(3));
        Assert.False(neverCompletes.Task.IsCompleted);
        neverCompletes.TrySetResult();
    }

    [Fact]
    public async Task RunAsync_ObservesCleanupFailureAndStillReturns()
    {
        await ApplicationShutdownCoordinator.RunAsync(
            TimeSpan.FromSeconds(1),
            Task.FromException(new InvalidOperationException("cleanup failed")),
            Task.CompletedTask);
    }

    [Fact]
    public async Task RunAsync_WaitsForAllCleanupWithinLimit()
    {
        var first = new TaskCompletionSource(
            TaskCreationOptions.RunContinuationsAsynchronously);
        var second = new TaskCompletionSource(
            TaskCreationOptions.RunContinuationsAsynchronously);
        Task shutdown = ApplicationShutdownCoordinator.RunAsync(
            TimeSpan.FromSeconds(1),
            first.Task,
            second.Task);

        first.SetResult();
        Assert.False(shutdown.IsCompleted);
        second.SetResult();

        await shutdown;
    }

    [Fact]
    public async Task RunCriticalThenBoundedAsync_DoesNotCloseBeforeCriticalRestoreCompletes()
    {
        var criticalRestore = new TaskCompletionSource(
            TaskCreationOptions.RunContinuationsAsynchronously);
        var boundedCleanup = new TaskCompletionSource(
            TaskCreationOptions.RunContinuationsAsynchronously);
        TimeSpan cleanupLimit = TimeSpan.FromMilliseconds(120);

        Task shutdown = ApplicationShutdownCoordinator.RunCriticalThenBoundedAsync(
            cleanupLimit,
            criticalRestore.Task,
            boundedCleanup.Task);

        await Task.Delay(cleanupLimit + TimeSpan.FromMilliseconds(80));
        Assert.False(shutdown.IsCompleted);

        criticalRestore.SetResult();
        await shutdown.WaitAsync(TimeSpan.FromSeconds(3));
        Assert.False(boundedCleanup.Task.IsCompleted);
        boundedCleanup.TrySetResult();
    }
}
