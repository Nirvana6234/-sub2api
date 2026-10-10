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

    /// <summary>供同步的 OnExit 使用，并避免在 UI 上下文直接等待异步清理。</summary>
    /// <param name="timeout">最多等多久。退出时界面线程在这里被占住，清理里任何一步卡住（网络、子进程），
    /// 用户看到的就是一直转圈的应用；所以有上限，超时即放弃等待。</param>
    /// <returns>清理在时限内完成返回 true；超时返回 false，清理仍在后台继续，由进程结束一并带走。</returns>
    public bool ReleaseBeforeProcessExit(TimeSpan timeout) =>
        Task.Run(async () => await ReleaseAsync().ConfigureAwait(false)).Wait(timeout);
}
