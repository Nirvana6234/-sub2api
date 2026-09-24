namespace LanAi.RelayClient.Services;

/// <summary>让托盘退出和进程退出兜底共用同一次异步清理。</summary>
internal sealed class ClientShutdownCoordinator
{
    private readonly Func<Task> _release;
    private readonly object _gate = new();
    private Task? _releaseTask;

    public ClientShutdownCoordinator(Func<Task> release) =>
        _release = release ?? throw new ArgumentNullException(nameof(release));

    public Task ReleaseAsync()
    {
        lock (_gate)
        {
            return _releaseTask ??= _release();
        }
    }

    /// <summary>供同步的 WPF OnExit 使用，并避免在 UI 上下文直接等待异步清理。</summary>
    public void ReleaseBeforeProcessExit() =>
        Task.Run(async () => await ReleaseAsync().ConfigureAwait(false))
            .GetAwaiter()
            .GetResult();
}
