using System.IO;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class LocalProxyUsageStoreTests : IDisposable
{
    private readonly string _directory = Path.Combine(Path.GetTempPath(), "lp-usage-" + Guid.NewGuid().ToString("N"));
    private DateTimeOffset _now = new(2026, 9, 23, 10, 0, 0, TimeSpan.FromHours(8));

    private LocalProxyUsageStore NewStore() => new(Path.Combine(_directory, "usage.json"), () => _now);

    public void Dispose()
    {
        if (Directory.Exists(_directory))
        {
            Directory.Delete(_directory, recursive: true);
        }
    }

    [Fact]
    public void TurnsAddUpPerDayToolAndAccountAndSurviveARestart()
    {
        LocalProxyUsageStore store = NewStore();
        store.Add(new LocalProxyUsage(LocalProxyKind.Codex, 7, 100, 10, 60));
        store.Add(new LocalProxyUsage(LocalProxyKind.Codex, 7, 50, 5, 0));
        store.Add(new LocalProxyUsage(LocalProxyKind.ClaudeCode, 8, 30, 3, 20));

        IReadOnlyList<LocalProxyUsageDay> days = NewStore().Load();

        Assert.Equal(2, days.Count);
        LocalProxyUsageTotals codex = LocalProxyUsageStore.Sum(days, LocalProxyKind.Codex, "2026-09-23");
        Assert.Equal(new LocalProxyUsageTotals(2, 150, 15, 60), codex);
        Assert.Equal(new LocalProxyUsageTotals(3, 180, 18, 80), LocalProxyUsageStore.Sum(days, null, "2026-09-23"));
    }

    [Fact]
    public void OnlyTheLastMonthIsKept()
    {
        LocalProxyUsageStore store = NewStore();
        store.Add(new LocalProxyUsage(LocalProxyKind.Codex, 7, 1, 1, 0));

        _now = _now.AddDays(LocalProxyUsageStore.KeepDays);
        store.Add(new LocalProxyUsage(LocalProxyKind.Codex, 7, 2, 2, 0));

        LocalProxyUsageDay day = Assert.Single(store.Load());
        Assert.Equal(LocalProxyUsageStore.DateKey(_now), day.Date);
    }

    [Fact]
    public void ADamagedFileStartsOverInsteadOfThrowing()
    {
        Directory.CreateDirectory(_directory);
        File.WriteAllText(Path.Combine(_directory, "usage.json"), "{not json");

        LocalProxyUsageStore store = NewStore();
        store.Add(new LocalProxyUsage(LocalProxyKind.Codex, 7, 1, 1, 0));

        Assert.Single(store.Load());
    }

    [Fact]
    public void ConcurrentTurnsDoNotLoseIncrements()
    {
        LocalProxyUsageStore store = NewStore();

        Parallel.For(0, 40, _ => store.Add(new LocalProxyUsage(LocalProxyKind.Codex, 7, 1, 1, 0)));

        Assert.Equal(40, Assert.Single(store.Load()).Requests);
    }

    [Fact]
    public void TheCardLineSaysSoWhenThereIsNothing()
    {
        Assert.Equal("暂无用量", LocalProxyUsageStore.Describe(default));
        Assert.Equal(
            "3 次请求 · 输入 1.5万 · 输出 250 tokens",
            LocalProxyUsageStore.Describe(new LocalProxyUsageTotals(3, 15_000, 250, 0)));
    }
}

public sealed class LocalProxyCredentialCacheTests
{
    private DateTimeOffset _now = new(2026, 9, 23, 10, 0, 0, TimeSpan.Zero);

    private (LocalProxyCredentialCache Cache, FakeRelayClient Relay) Build(DateTimeOffset? expiresAt)
    {
        int issued = 0;
        var relay = new FakeRelayClient
        {
            OnLocalProxyCredential = id => new LocalProxyCredential(
                accountId: id, accessToken: $"at-{++issued}", expiresAt: expiresAt),
        };
        return (new LocalProxyCredentialCache(relay, _ => Task.FromResult("jwt"), () => _now), relay);
    }

    [Fact]
    public async Task ATokenIsReusedUntilShortlyBeforeItExpires()
    {
        (LocalProxyCredentialCache cache, FakeRelayClient relay) = Build(_now.AddMinutes(10));

        Assert.Equal("at-1", (await cache.GetAsync(7, false, default)).AccessToken);
        _now = _now.AddMinutes(7);
        Assert.Equal("at-1", (await cache.GetAsync(7, false, default)).AccessToken);
        _now = _now.AddMinutes(2);
        Assert.Equal("at-2", (await cache.GetAsync(7, false, default)).AccessToken);
        Assert.Equal(2, relay.LocalProxyCredentialRequests.Count);
    }

    [Fact]
    public async Task ATokenWithNoExpiryIsStillAskedForAgainWithinTheHalfHour()
    {
        (LocalProxyCredentialCache cache, _) = Build(expiresAt: null);

        await cache.GetAsync(7, false, default);
        _now = _now.AddMinutes(31);

        Assert.Equal("at-2", (await cache.GetAsync(7, false, default)).AccessToken);
    }

    [Fact]
    public async Task ForcingOrClearingAsksTheServerAgain()
    {
        (LocalProxyCredentialCache cache, _) = Build(_now.AddHours(1));

        await cache.GetAsync(7, false, default);
        Assert.Equal("at-2", (await cache.GetAsync(7, forceRefresh: true, default)).AccessToken);
        cache.Clear();
        Assert.Equal("at-3", (await cache.GetAsync(7, false, default)).AccessToken);
    }

    [Fact]
    public async Task EachAccountHasItsOwnToken()
    {
        (LocalProxyCredentialCache cache, FakeRelayClient relay) = Build(_now.AddHours(1));

        await cache.GetAsync(7, false, default);
        await cache.GetAsync(8, false, default);
        await cache.GetAsync(7, false, default);

        Assert.Equal([7L, 8L], relay.LocalProxyCredentialRequests);
    }
}
