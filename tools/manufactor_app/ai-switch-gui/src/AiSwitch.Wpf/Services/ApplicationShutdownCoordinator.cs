namespace LanAi.Workspace.Wpf.Services;

internal static class ApplicationShutdownCoordinator
{
    public static async Task RunCriticalThenBoundedAsync(
        TimeSpan timeout,
        Task criticalShutdownTask,
        params Task[] boundedShutdownTasks)
    {
        ArgumentNullException.ThrowIfNull(criticalShutdownTask);
        try
        {
            await criticalShutdownTask.ConfigureAwait(false);
        }
        catch
        {
            // The critical task has been observed. Persistent recovery state is
            // retained by the restore service when restoration does not finish.
        }

        await RunAsync(timeout, boundedShutdownTasks).ConfigureAwait(false);
    }

    public static async Task RunAsync(TimeSpan timeout, params Task[] shutdownTasks)
    {
        if (timeout <= TimeSpan.Zero)
        {
            throw new ArgumentOutOfRangeException(nameof(timeout));
        }

        ArgumentNullException.ThrowIfNull(shutdownTasks);
        if (shutdownTasks.Any(task => task is null))
        {
            throw new ArgumentException("退出任务不能包含 null。", nameof(shutdownTasks));
        }

        Task combinedShutdown = Task.WhenAll(shutdownTasks);
        try
        {
            await combinedShutdown.WaitAsync(timeout).ConfigureAwait(false);
        }
        catch (TimeoutException)
        {
            _ = ObserveLateCompletionAsync(combinedShutdown);
        }
        catch
        {
            // Cleanup failures have been observed. Closing the application is
            // still the only safe terminal state after shutdown has started.
        }
    }

    private static async Task ObserveLateCompletionAsync(Task shutdownTask)
    {
        try
        {
            await shutdownTask.ConfigureAwait(false);
        }
        catch
        {
            // The window has already committed to closing. Observe late faults
            // so process cleanup cannot surface as an unobserved task failure.
        }
    }
}
