using System.IO;
using System.Text.Json;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class ContextFilterUsageStoreTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), $"relay-cf-usage-{Guid.NewGuid():N}");

    private ContextFilterUsageStore CreateStore() =>
        new(Path.Combine(_root, "context-filter-usage.json"));

    [Fact]
    public void StartsAtZeroWithNoFileOnDisk()
    {
        ContextFilterUsage usage = CreateStore().Load();

        Assert.Equal(0, usage.TotalBytesBefore);
        Assert.Equal(0, usage.TotalBytesSaved);
    }

    [Fact]
    public void AccumulatesAcrossCallsAndPersists()
    {
        ContextFilterUsageStore store = CreateStore();

        store.Add(1000, 400);
        ContextFilterUsage after = store.Add(500, 100);

        Assert.Equal(1500, after.TotalBytesBefore);
        Assert.Equal(500, after.TotalBytesSaved);

        // A fresh instance reads the same totals back from disk — this is the whole
        // point: the number has to survive the client restarting, not just this run.
        ContextFilterUsage reloaded = CreateStore().Load();
        Assert.Equal(1500, reloaded.TotalBytesBefore);
        Assert.Equal(500, reloaded.TotalBytesSaved);
    }

    [Fact]
    public void ClampsAnImplausibleSavedValueRatherThanTrustingIt()
    {
        // Saved bytes are a header the filter sets; a store that trusted a value
        // larger than what was processed would let one bad request break the
        // percentage in ContextFilterUsageStore.Describe for every reading after it.
        ContextFilterUsageStore store = CreateStore();

        ContextFilterUsage usage = store.Add(100, 900);

        Assert.Equal(100, usage.TotalBytesBefore);
        Assert.Equal(100, usage.TotalBytesSaved);
    }

    [Fact]
    public void IgnoresACallWithNothingProcessed()
    {
        ContextFilterUsageStore store = CreateStore();
        store.Add(1000, 400);

        ContextFilterUsage unchanged = store.Add(0, 0);

        Assert.Equal(1000, unchanged.TotalBytesBefore);
        Assert.Equal(400, unchanged.TotalBytesSaved);
    }

    [Fact]
    public void ADamagedFileIsTreatedAsZeroRatherThanThrowing()
    {
        Directory.CreateDirectory(_root);
        string path = Path.Combine(_root, "context-filter-usage.json");
        File.WriteAllText(path, "{ not json");

        ContextFilterUsageStore store = new(path);

        Assert.Equal(ContextFilterUsage.Zero, store.Load());
        // And it can still be written to from here — a damaged file must not
        // permanently disable the counter, just reset it.
        ContextFilterUsage usage = store.Add(200, 50);
        Assert.Equal(200, usage.TotalBytesBefore);
    }

    [Fact]
    public void ConcurrentAddsDoNotLoseEitherIncrement()
    {
        // Forwarded requests run concurrently (LocalPawRelay.ServeAsync fires each
        // HandleAsync without awaiting it), so two turns finishing close together must
        // not both read the same starting total and have the second write clobber the
        // first — the classic lost-update race on a read-modify-write file.
        ContextFilterUsageStore store = CreateStore();

        const int concurrency = 20;
        Parallel.For(0, concurrency, _ => store.Add(10, 3));

        ContextFilterUsage usage = store.Load();
        Assert.Equal(10 * concurrency, usage.TotalBytesBefore);
        Assert.Equal(3 * concurrency, usage.TotalBytesSaved);
    }

    [Fact]
    public void EstimatesTokensFromBytesUsingTheDocumentedRatio()
    {
        Assert.Equal(0, ContextFilterUsageStore.EstimateTokens(0));
        Assert.Equal(0, ContextFilterUsageStore.EstimateTokens(-5));
        Assert.Equal(1, ContextFilterUsageStore.EstimateTokens(2));
        Assert.Equal(1000, ContextFilterUsageStore.EstimateTokens(4000));
    }

    [Fact]
    public void DescribesZeroUsageAsNoDataYetRatherThanZeroPercent()
    {
        // "0 tokens, 0 saved, 0.0%" reads as a fault. Nothing has run through the
        // filter yet, which is a different, unremarkable state.
        string line = ContextFilterUsageStore.Describe(ContextFilterUsage.Zero);

        Assert.Equal("尚无压缩数据", line);
    }

    [Fact]
    public void DescribesNonZeroUsageWithBothTotalsAndAPercentage()
    {
        // 40,000 bytes / 4,000 bytes saved -> under the 万 threshold, shown as exact
        // counts (N0) rather than abbreviated — small numbers should read as numbers.
        var usage = new ContextFilterUsage(TotalBytesBefore: 4_000, TotalBytesSaved: 1_000);

        string line = ContextFilterUsageStore.Describe(usage);

        Assert.Contains(1_000L.ToString("N0"), line, StringComparison.Ordinal);
        Assert.Contains(250L.ToString("N0"), line, StringComparison.Ordinal);
        Assert.Contains("25.0%", line, StringComparison.Ordinal);
        Assert.Contains("token", line, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void DescribesLargeUsageInWanRatherThanAWallOfDigits()
    {
        // A cumulative counter is expected to grow for as long as the client stays
        // installed. 40,000,000 bytes before / 4,000,000 saved -> 10,000,000 / 1,000,000
        // estimated tokens, both comfortably inside the 万 range (under the 亿 threshold).
        var usage = new ContextFilterUsage(TotalBytesBefore: 40_000_000, TotalBytesSaved: 4_000_000);

        string line = ContextFilterUsageStore.Describe(usage);

        Assert.Contains("万", line, StringComparison.Ordinal);
        Assert.DoesNotContain("亿", line, StringComparison.Ordinal);
        Assert.Contains("10.0%", line, StringComparison.Ordinal);
    }

    [Fact]
    public void DescribesVeryLargeUsageInYiOnceItCrossesTheThreshold()
    {
        // 400,000,000,000 bytes -> 100,000,000,000 estimated tokens -> 1,000.## 亿.
        var usage = new ContextFilterUsage(TotalBytesBefore: 400_000_000_000, TotalBytesSaved: 40_000_000_000);

        string line = ContextFilterUsageStore.Describe(usage);

        Assert.Contains("亿", line, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData(0, "0")]
    [InlineData(9_999, "9,999")]
    [InlineData(10_000, "1万")]
    [InlineData(15_000, "1.5万")]
    [InlineData(12_500, "1.25万")]
    [InlineData(90_000_000, "9000万")]
    [InlineData(100_000_000, "1亿")]
    [InlineData(125_000_000, "1.25亿")]
    public void FormatCountSwitchesUnitsAtTheWanAndYiBoundaries(long value, string expected)
    {
        Assert.Equal(expected, ContextFilterUsageStore.FormatCount(value));
    }

    [Fact]
    public void AddNeverWrapsPastLongMaxValueIntoANegativeTotal()
    {
        // Plain `+` on long is unchecked in this codebase, so at the real limit it
        // would silently wrap to a large negative number rather than throw — a
        // counter that starts counting down with nothing on screen to explain why.
        // Written directly rather than reached by calling Add() long.MaxValue/100
        // times: this constructs the near-overflow state that would otherwise take
        // years of real usage to reach.
        string path = Path.Combine(_root, "context-filter-usage.json");
        Directory.CreateDirectory(_root);
        var totals = new ContextFilterUsageStore.Totals
        {
            TotalBytesBefore = long.MaxValue - 10,
            TotalBytesSaved = 0,
        };
        File.WriteAllBytes(
            path,
            JsonSerializer.SerializeToUtf8Bytes(totals, ClientJsonContext.Default.ContextFilterUsageTotals));

        ContextFilterUsageStore store = new(path);
        ContextFilterUsage usage = store.Add(100, 0);

        Assert.True(usage.TotalBytesBefore > 0, "计数不应该翻成负数");
        Assert.Equal(long.MaxValue, usage.TotalBytesBefore);
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }
}
