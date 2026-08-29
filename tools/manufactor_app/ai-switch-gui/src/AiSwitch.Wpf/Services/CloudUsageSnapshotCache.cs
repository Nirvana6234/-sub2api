using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Services;

internal interface ICloudUsageSnapshotCache
{
    TimeSpan Freshness { get; }

    Task<CloudUsageSnapshotCacheResult> GetOrLoadAsync(
        Uri apiBaseUri,
        string identity,
        int trendDays,
        bool forceRefresh,
        Func<CancellationToken, Task<StatsSnapshot>> loader,
        CancellationToken cancellationToken);

    void Store(Uri apiBaseUri, string identity, int trendDays, StatsSnapshot snapshot);

    void Clear();
}

internal sealed record CloudUsageSnapshotCacheResult(
    StatsSnapshot Snapshot,
    DateTimeOffset CalibratedAtUtc,
    bool WasCached);

internal sealed class CloudUsageSnapshotCache : ICloudUsageSnapshotCache
{
    private readonly object _sync = new();
    private readonly Dictionary<CacheKey, CacheEntry> _entries = [];
    private readonly Dictionary<PendingKey, Task<CloudUsageSnapshotCacheResult>> _pending = [];
    private readonly Func<DateTimeOffset> _clock;
    private long _generation;

    public CloudUsageSnapshotCache(TimeSpan? freshness = null, Func<DateTimeOffset>? clock = null)
    {
        Freshness = freshness ?? TimeSpan.FromMinutes(10);
        if (Freshness <= TimeSpan.Zero)
        {
            throw new ArgumentOutOfRangeException(nameof(freshness));
        }

        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    public TimeSpan Freshness { get; }

    public Task<CloudUsageSnapshotCacheResult> GetOrLoadAsync(
        Uri apiBaseUri,
        string identity,
        int trendDays,
        bool forceRefresh,
        Func<CancellationToken, Task<StatsSnapshot>> loader,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(apiBaseUri);
        ArgumentException.ThrowIfNullOrWhiteSpace(identity);
        ArgumentNullException.ThrowIfNull(loader);
        var key = new CacheKey(NormalizeEndpoint(apiBaseUri), identity.Trim().ToLowerInvariant(), trendDays);
        Task<CloudUsageSnapshotCacheResult> task;
        TaskCompletionSource<CloudUsageSnapshotCacheResult>? completion = null;
        PendingKey pendingKey;

        lock (_sync)
        {
            DateTimeOffset now = _clock();
            if (!forceRefresh &&
                _entries.TryGetValue(key, out CacheEntry? cached) &&
                now - cached.CalibratedAtUtc < Freshness)
            {
                return Task.FromResult(new CloudUsageSnapshotCacheResult(
                    cached.Snapshot,
                    cached.CalibratedAtUtc,
                    WasCached: true));
            }

            pendingKey = new PendingKey(key, _generation);
            if (!_pending.TryGetValue(pendingKey, out task!))
            {
                completion = new TaskCompletionSource<CloudUsageSnapshotCacheResult>(
                    TaskCreationOptions.RunContinuationsAsynchronously);
                task = completion.Task;
                _pending[pendingKey] = task;
            }
        }

        if (completion is not null)
        {
            _ = LoadAndStoreAsync(key, pendingKey, loader, completion);
        }

        return task.WaitAsync(cancellationToken);
    }

    public void Store(Uri apiBaseUri, string identity, int trendDays, StatsSnapshot snapshot)
    {
        ArgumentNullException.ThrowIfNull(apiBaseUri);
        ArgumentException.ThrowIfNullOrWhiteSpace(identity);
        ArgumentNullException.ThrowIfNull(snapshot);
        var key = new CacheKey(NormalizeEndpoint(apiBaseUri), identity.Trim().ToLowerInvariant(), trendDays);
        lock (_sync)
        {
            _entries[key] = new CacheEntry(snapshot, _clock());
        }
    }

    public void Clear()
    {
        lock (_sync)
        {
            _generation++;
            _entries.Clear();
        }
    }

    private async Task LoadAndStoreAsync(
        CacheKey key,
        PendingKey pendingKey,
        Func<CancellationToken, Task<StatsSnapshot>> loader,
        TaskCompletionSource<CloudUsageSnapshotCacheResult> completion)
    {
        try
        {
            StatsSnapshot snapshot = await loader(CancellationToken.None).ConfigureAwait(false);
            DateTimeOffset calibratedAt = _clock();
            lock (_sync)
            {
                if (pendingKey.Generation == _generation)
                {
                    _entries[key] = new CacheEntry(snapshot, calibratedAt);
                }
            }

            completion.TrySetResult(new CloudUsageSnapshotCacheResult(
                snapshot,
                calibratedAt,
                WasCached: false));
        }
        catch (Exception exception)
        {
            completion.TrySetException(exception);
        }
        finally
        {
            lock (_sync)
            {
                _pending.Remove(pendingKey);
            }
        }
    }

    private static string NormalizeEndpoint(Uri apiBaseUri)
        => apiBaseUri.GetLeftPart(UriPartial.Path).TrimEnd('/').ToLowerInvariant();

    private sealed record CacheKey(string Endpoint, string Identity, int TrendDays);

    private sealed record PendingKey(CacheKey Key, long Generation);

    private sealed record CacheEntry(StatsSnapshot Snapshot, DateTimeOffset CalibratedAtUtc);
}
