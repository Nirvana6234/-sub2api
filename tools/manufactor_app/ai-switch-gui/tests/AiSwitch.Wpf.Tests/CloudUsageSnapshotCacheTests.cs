using AiSwitchGui;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class CloudUsageSnapshotCacheTests
{
    [Fact]
    public async Task Cache_ReusesSnapshotForTenMinutesAndReloadsAfterExpiry()
    {
        DateTimeOffset now = new(2026, 7, 17, 8, 0, 0, TimeSpan.Zero);
        var cache = new CloudUsageSnapshotCache(TimeSpan.FromMinutes(10), () => now);
        int loads = 0;
        var endpoint = new Uri("https://gateway.example/api/");

        CloudUsageSnapshotCacheResult first = await cache.GetOrLoadAsync(
            endpoint,
            "user:7:user",
            7,
            forceRefresh: false,
            _ => Task.FromResult(CreateSnapshot(++loads)),
            CancellationToken.None);
        now = now.AddMinutes(9);
        CloudUsageSnapshotCacheResult cached = await cache.GetOrLoadAsync(
            endpoint,
            "user:7:user",
            7,
            forceRefresh: false,
            _ => Task.FromResult(CreateSnapshot(++loads)),
            CancellationToken.None);
        now = now.AddMinutes(1);
        CloudUsageSnapshotCacheResult expired = await cache.GetOrLoadAsync(
            endpoint,
            "user:7:user",
            7,
            forceRefresh: false,
            _ => Task.FromResult(CreateSnapshot(++loads)),
            CancellationToken.None);

        Assert.False(first.WasCached);
        Assert.True(cached.WasCached);
        Assert.False(expired.WasCached);
        Assert.Equal(2, loads);
        Assert.Equal(1, cached.Snapshot.Overview.TotalRequests);
        Assert.Equal(2, expired.Snapshot.Overview.TotalRequests);
    }

    [Fact]
    public async Task Cache_ForceRefreshAndClearRequireNewCalibration()
    {
        var cache = new CloudUsageSnapshotCache();
        int loads = 0;
        var endpoint = new Uri("https://gateway.example");
        Func<CancellationToken, Task<StatsSnapshot>> loader = _ =>
            Task.FromResult(CreateSnapshot(++loads));

        _ = await cache.GetOrLoadAsync(endpoint, "local-admin", 30, false, loader, CancellationToken.None);
        _ = await cache.GetOrLoadAsync(endpoint, "local-admin", 30, true, loader, CancellationToken.None);
        cache.Clear();
        _ = await cache.GetOrLoadAsync(endpoint, "local-admin", 30, false, loader, CancellationToken.None);

        Assert.Equal(3, loads);
    }

    [Fact]
    public async Task Cache_CoalescesConcurrentPageRequests()
    {
        var cache = new CloudUsageSnapshotCache();
        int loads = 0;
        var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        async Task<StatsSnapshot> LoadAsync(CancellationToken _)
        {
            Interlocked.Increment(ref loads);
            await release.Task;
            return CreateSnapshot(1);
        }

        Task<CloudUsageSnapshotCacheResult> dashboard = cache.GetOrLoadAsync(
            new Uri("https://gateway.example"), "user:9:user", 7, false, LoadAsync, CancellationToken.None);
        Task<CloudUsageSnapshotCacheResult> overview = cache.GetOrLoadAsync(
            new Uri("https://gateway.example/"), "user:9:user", 7, false, LoadAsync, CancellationToken.None);
        release.SetResult();
        await Task.WhenAll(dashboard, overview);

        Assert.Equal(1, loads);
    }

    private static StatsSnapshot CreateSnapshot(long requests) => new(
        new StatsOverview { TotalRequests = requests },
        [],
        []);
}
