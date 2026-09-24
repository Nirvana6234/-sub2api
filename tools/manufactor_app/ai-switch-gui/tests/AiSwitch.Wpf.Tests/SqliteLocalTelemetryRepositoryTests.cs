using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using Microsoft.Data.Sqlite;

namespace AiSwitch.Wpf.Tests;

public sealed class SqliteLocalTelemetryRepositoryTests
{
    [Fact]
    public async Task SnapshotAsync_AggregatesTodaySevenDaysDailyUsageAndLatestNetworkProbe()
    {
        using var fixture = new TemporaryTelemetryStore();
        DateTimeOffset now = new(2026, 7, 14, 14, 0, 0, TimeSpan.Zero);
        using var repository = new SqliteLocalTelemetryRepository(
            fixture.DatabasePath,
            timeProvider: new FixedTimeProvider(now));

        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 14, 9, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 120,
            outputTokens: 30,
            cachedInputTokens: 12,
            succeeded: true,
            elapsedMilliseconds: 100,
            cacheCreationTokens: 3,
            estimatedCost: 0.25));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 14, 10, 30, 0, TimeSpan.Zero),
            CliKind.ClaudeCode,
            inputTokens: 50,
            outputTokens: 0,
            cachedInputTokens: 0,
            succeeded: false,
            elapsedMilliseconds: 300,
            cacheCreationTokens: 1));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 12, 12, 0, 0, TimeSpan.Zero),
            CliKind.GeminiCli,
            inputTokens: 25,
            outputTokens: 10,
            cachedInputTokens: 2,
            succeeded: true,
            elapsedMilliseconds: 200,
            cacheCreationTokens: 2,
            estimatedCost: 0.10));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 6, 12, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 9,
            outputTokens: 9,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 99));

        await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
            new DateTimeOffset(2026, 7, 14, 8, 0, 0, TimeSpan.Zero),
            "local-machine",
            "本机中转",
            succeeded: false,
            latencyMilliseconds: null));
        await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
            new DateTimeOffset(2026, 7, 14, 13, 0, 0, TimeSpan.Zero),
            "local-machine",
            "本机中转",
            succeeded: true,
            latencyMilliseconds: 41));

        LocalTelemetrySnapshot snapshot = await repository.GetSnapshotAsync(TimeZoneInfo.Utc);

        Assert.Equal(now, snapshot.GeneratedAt);
        Assert.Equal(2, snapshot.Today.RequestCount);
        Assert.Equal(1, snapshot.Today.SuccessfulRequestCount);
        Assert.Equal(1, snapshot.Today.FailedRequestCount);
        Assert.Equal(170, snapshot.Today.InputTokens);
        Assert.Equal(30, snapshot.Today.OutputTokens);
        Assert.Equal(12, snapshot.Today.CachedInputTokens);
        Assert.Equal(4, snapshot.Today.CacheCreationTokens);
        Assert.Equal(0.25d, snapshot.Today.EstimatedCost);
        Assert.Equal(50d, snapshot.Today.SuccessRatePercent);
        Assert.Equal(200d, snapshot.Today.AverageLatencyMilliseconds);

        Assert.Equal(3, snapshot.LastSevenDays.RequestCount);
        Assert.Equal(2, snapshot.LastSevenDays.SuccessfulRequestCount);
        Assert.Equal(195, snapshot.LastSevenDays.InputTokens);
        Assert.Equal(40, snapshot.LastSevenDays.OutputTokens);
        Assert.Equal(14, snapshot.LastSevenDays.CachedInputTokens);
        Assert.Equal(6, snapshot.LastSevenDays.CacheCreationTokens);
        Assert.Equal(0.35d, snapshot.LastSevenDays.EstimatedCost);
        Assert.Equal(200d, snapshot.LastSevenDays.AverageLatencyMilliseconds);

        Assert.Equal(7, snapshot.LastSevenDaysDailyUsage.Count);
        Assert.Equal(
            new long[] { 0, 0, 0, 0, 1, 0, 2 },
            snapshot.LastSevenDaysDailyUsage.Select(day => day.Usage.RequestCount));
        Assert.Equal(
            new DateOnly[]
            {
                new(2026, 7, 8),
                new(2026, 7, 9),
                new(2026, 7, 10),
                new(2026, 7, 11),
                new(2026, 7, 12),
                new(2026, 7, 13),
                new(2026, 7, 14),
            },
            snapshot.LastSevenDaysDailyUsage.Select(day => day.Date));

        Assert.Equal(24, snapshot.LastTwentyFourHoursHourlyUsage.Count);
        LocalTelemetryHourlyUsage tenAm = Assert.Single(
            snapshot.LastTwentyFourHoursHourlyUsage,
            item => item.HourStart == new DateTimeOffset(2026, 7, 14, 10, 0, 0, TimeSpan.Zero));
        Assert.Equal(1, tenAm.Usage.RequestCount);
        Assert.Equal(1, tenAm.Usage.CacheCreationTokens);

        LocalNetworkHealthStatus network = Assert.IsType<LocalNetworkHealthStatus>(snapshot.LatestNetworkStatus);
        Assert.True(network.Succeeded);
        Assert.Equal(41, network.LatencyMilliseconds);
        Assert.Equal("local-machine", network.SourceId);
        Assert.Equal("本机中转", network.SourceLabel);
        Assert.Equal(new DateTimeOffset(2026, 7, 14, 13, 0, 0, TimeSpan.Zero), network.CheckedAt);

        LocalTelemetryUsageBreakdown source = Assert.Single(snapshot.LastSevenDaysBySource);
        Assert.Equal("local-machine", source.SourceId);
        Assert.Equal("本机中转", source.SourceLabel);
        Assert.Equal(3, source.Usage.RequestCount);
        Assert.Equal(6, source.Usage.CacheCreationTokens);

        Assert.Equal(3, snapshot.LastSevenDaysByCli.Count);
        LocalTelemetryUsageBreakdown codex = Assert.Single(
            snapshot.LastSevenDaysByCli,
            item => item.CliKind == CliKind.Codex);
        Assert.Equal(1, codex.Usage.RequestCount);

        LocalTelemetryUsageBreakdown model = Assert.Single(snapshot.LastSevenDaysByModel);
        Assert.Equal("gpt-5", model.Model);
        Assert.Equal(3, model.Usage.RequestCount);

        LocalTelemetryRecentActivity recent = Assert.IsType<LocalTelemetryRecentActivity>(snapshot.RecentActivity.First());
        Assert.Equal(CliKind.ClaudeCode, recent.CliKind);
        Assert.False(recent.Succeeded);
        Assert.Equal(1, recent.CacheCreationTokens);
    }

    [Fact]
    public async Task NetworkHealthSummariesAsync_ComputesPerRouteSuccessRateAndPercentiles()
    {
        using var fixture = new TemporaryTelemetryStore();
        DateTimeOffset now = new(2026, 7, 15, 12, 0, 0, TimeSpan.Zero);
        using var repository = new SqliteLocalTelemetryRepository(
            fixture.DatabasePath,
            timeProvider: new FixedTimeProvider(now));

        await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
            now.AddHours(-4), "route-Codex", "Codex · 云端来源", true, 10, "ok"));
        await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
            now.AddHours(-3), "route-Codex", "Codex · 云端来源", true, 20, "ok"));
        await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
            now.AddHours(-2), "route-Codex", "Codex · 云端来源", true, 40, "ok"));
        await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
            now.AddHours(-1), "route-Codex", "Codex · 云端来源", false, null, "timeout"));
        await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
            now.AddHours(-30), "route-Codex", "Codex · 旧来源", true, 999, "ok"));

        LocalNetworkHealthSummary summary = Assert.Single(
            await repository.GetNetworkHealthSummariesAsync(now.AddHours(-24)));

        Assert.Equal(4, summary.ProbeCount);
        Assert.Equal(3, summary.SuccessfulProbeCount);
        Assert.Equal(75d, summary.SuccessRatePercent);
        Assert.Equal(20, summary.P50LatencyMilliseconds);
        Assert.Equal(40, summary.P95LatencyMilliseconds);
        Assert.Equal(now.AddHours(-2), summary.LastSuccessAt);
        Assert.Equal("timeout", summary.LatestStatusCategory);
    }

    [Fact]
    public async Task RecentNetworkProbesAsync_FiltersSourceLimitsAndReturnsChronologicalPoints()
    {
        using var fixture = new TemporaryTelemetryStore();
        DateTimeOffset now = new(2026, 7, 15, 12, 0, 0, TimeSpan.Zero);
        using var repository = new SqliteLocalTelemetryRepository(
            fixture.DatabasePath,
            timeProvider: new FixedTimeProvider(now));

        for (int index = 0; index < 65; index++)
        {
            await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
                now.AddMinutes(index - 65),
                "cloud-source",
                "云端来源",
                index % 4 != 0,
                index,
                index % 4 == 0 ? "timeout" : "ok"));
        }

        await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
            now,
            "other-source",
            "其他来源",
            true,
            999,
            "ok"));

        IReadOnlyList<LocalNetworkHealthProbe> probes = await repository
            .GetRecentNetworkProbesAsync("cloud-source", 60);

        Assert.Equal(60, probes.Count);
        Assert.All(probes, probe => Assert.Equal("cloud-source", probe.SourceId));
        Assert.True(probes.Zip(probes.Skip(1)).All(pair => pair.First.Timestamp <= pair.Second.Timestamp));
        Assert.Equal(5, probes[0].LatencyMilliseconds);
        Assert.Equal(64, probes[^1].LatencyMilliseconds);
    }

    [Fact]
    public async Task RecentActivity_PersistsFirstTokenStreamingStatusAndPricingModel()
    {
        using var fixture = new TemporaryTelemetryStore();
        DateTimeOffset now = new(2026, 7, 15, 12, 0, 0, TimeSpan.Zero);
        using var repository = new SqliteLocalTelemetryRepository(
            fixture.DatabasePath,
            timeProvider: new FixedTimeProvider(now));
        await repository.RecordUsageAsync(new LocalUsageTelemetryEvent(
            now.AddMinutes(-1),
            CliKind.GeminiCli,
            "cloud-source",
            "云端来源",
            "gemini-3.5-flash-extra-low",
            100,
            20,
            40,
            true,
            800,
            estimatedCost: 0.001,
            firstTokenMilliseconds: 120,
            statusCategory: "success",
            isStreaming: true,
            pricingModel: "gemini-3.5-flash"));

        LocalTelemetryRangeSnapshot snapshot = await repository.GetRangeSnapshotAsync(1, TimeZoneInfo.Utc);
        LocalTelemetryRecentActivity activity = Assert.Single(snapshot.RecentActivity);

        Assert.Equal(120, activity.FirstTokenMilliseconds);
        Assert.Equal("success", activity.StatusCategory);
        Assert.True(activity.IsStreaming);
        Assert.Equal("gemini-3.5-flash", activity.PricingModel);
    }

    [Fact]
    public async Task RecordAsync_PrunesExpiredAndOverflowRowsForBothEventKinds()
    {
        using var fixture = new TemporaryTelemetryStore();
        DateTimeOffset now = new(2026, 7, 14, 14, 0, 0, TimeSpan.Zero);
        using var repository = new SqliteLocalTelemetryRepository(
            fixture.DatabasePath,
            new LocalTelemetryStorageOptions
            {
                MaxUsageEventCount = 2,
                MaxNetworkProbeCount = 1,
                MaximumAge = TimeSpan.FromDays(7),
            },
            new FixedTimeProvider(now));

        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 1, 12, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 999,
            outputTokens: 999,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 1));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 12, 12, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 1,
            outputTokens: 1,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 1));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 13, 12, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 2,
            outputTokens: 2,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 2));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 14, 12, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 3,
            outputTokens: 3,
            cachedInputTokens: 0,
            succeeded: false,
            elapsedMilliseconds: 3));

        await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
            new DateTimeOffset(2026, 7, 12, 12, 0, 0, TimeSpan.Zero),
            "local-machine",
            "本机中转",
            succeeded: false,
            latencyMilliseconds: 500));
        await repository.RecordNetworkProbeAsync(new LocalNetworkHealthProbe(
            new DateTimeOffset(2026, 7, 14, 12, 0, 0, TimeSpan.Zero),
            "local-machine",
            "本机中转",
            succeeded: true,
            latencyMilliseconds: 25));

        LocalTelemetrySnapshot snapshot = await repository.GetSnapshotAsync(TimeZoneInfo.Utc);

        Assert.Equal(2, snapshot.LastSevenDays.RequestCount);
        Assert.Equal(5, snapshot.LastSevenDays.InputTokens);
        Assert.Equal(5, snapshot.LastSevenDays.OutputTokens);
        LocalNetworkHealthStatus network = Assert.IsType<LocalNetworkHealthStatus>(snapshot.LatestNetworkStatus);
        Assert.True(network.Succeeded);
        Assert.Equal(25, network.LatencyMilliseconds);
    }

    [Fact]
    public async Task RecordUsageAsync_RollsExpiredDetailIntoBoundedDailyTotalsBeforeDeletion()
    {
        using var fixture = new TemporaryTelemetryStore();
        DateTimeOffset now = new(2026, 7, 15, 12, 0, 0, TimeSpan.Zero);
        using var repository = new SqliteLocalTelemetryRepository(
            fixture.DatabasePath,
            new LocalTelemetryStorageOptions
            {
                MaxUsageEventCount = 100,
                MaxNetworkProbeCount = 100,
                MaximumAge = TimeSpan.FromDays(90),
                UsageDetailAge = TimeSpan.FromDays(30),
                MaxDailyRollupCount = 365,
            },
            new FixedTimeProvider(now));

        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 6, 1, 9, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 10,
            outputTokens: 2,
            cachedInputTokens: 4,
            succeeded: true,
            elapsedMilliseconds: 150,
            cacheCreationTokens: 1,
            estimatedCost: 0.20));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 6, 1, 13, 0, 0, TimeSpan.Zero),
            CliKind.ClaudeCode,
            inputTokens: 20,
            outputTokens: 3,
            cachedInputTokens: 5,
            succeeded: false,
            elapsedMilliseconds: 250,
            cacheCreationTokens: 2));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 10, 9, 0, 0, TimeSpan.Zero),
            CliKind.GeminiCli,
            inputTokens: 7,
            outputTokens: 1,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 75));

        LocalTelemetryRangeSnapshot snapshot = await repository.GetRangeSnapshotAsync(30, TimeZoneInfo.Utc);
        Assert.Equal(1, snapshot.Usage.RequestCount);
        Assert.Equal(7, snapshot.Usage.InputTokens);

        await using var connection = new SqliteConnection(new SqliteConnectionStringBuilder
        {
            DataSource = fixture.DatabasePath,
        }.ToString());
        await connection.OpenAsync();

        await using (SqliteCommand countDetail = connection.CreateCommand())
        {
            countDetail.CommandText = "SELECT COUNT(*) FROM local_usage_events;";
            Assert.Equal(1L, (long)(await countDetail.ExecuteScalarAsync())!);
        }

        await using SqliteCommand rollup = connection.CreateCommand();
        rollup.CommandText = """
            SELECT day_utc, request_count, successful_request_count,
                   input_tokens, output_tokens, cached_input_tokens,
                   cache_creation_tokens, elapsed_total_ms,
                   elapsed_sample_count, estimated_cost
            FROM local_usage_daily_rollups;
            """;
        await using SqliteDataReader reader = await rollup.ExecuteReaderAsync();
        Assert.True(await reader.ReadAsync());
        Assert.Equal("2026-06-01", reader.GetString(0));
        Assert.Equal(2, reader.GetInt64(1));
        Assert.Equal(1, reader.GetInt64(2));
        Assert.Equal(30, reader.GetInt64(3));
        Assert.Equal(5, reader.GetInt64(4));
        Assert.Equal(9, reader.GetInt64(5));
        Assert.Equal(3, reader.GetInt64(6));
        Assert.Equal(400, reader.GetInt64(7));
        Assert.Equal(2, reader.GetInt64(8));
        Assert.Equal(0.20d, reader.GetDouble(9), precision: 10);
        Assert.False(await reader.ReadAsync());
    }

    [Fact]
    public async Task RemoveLegacyHistoryImportEventsAsync_RemovesOnlyAnonymousLegacyRows()
    {
        using var fixture = new TemporaryTelemetryStore();
        DateTimeOffset now = new(2026, 7, 14, 14, 0, 0, TimeSpan.Zero);
        using var repository = new SqliteLocalTelemetryRepository(
            fixture.DatabasePath,
            timeProvider: new FixedTimeProvider(now));

        // This mirrors the retired importer: it had no selected Connection
        // Center source because it was derived from an official history file.
        await repository.RecordUsageAsync(new LocalUsageTelemetryEvent(
            now,
            CliKind.Codex,
            sourceId: null,
            sourceLabel: null,
            model: "gpt-5",
            inputTokens: 900_000,
            outputTokens: 10,
            cachedInputTokens: 800_000,
            succeeded: true,
            elapsedMilliseconds: null));
        await repository.RecordUsageAsync(Usage(
            now,
            CliKind.Codex,
            inputTokens: 12,
            outputTokens: 3,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 90,
            sourceId: "cloud-current",
            sourceLabel: "连接中心当前来源 · 云端来源",
            model: "gpt-5"));

        int removed = await repository.RemoveLegacyHistoryImportEventsAsync();
        LocalTelemetryRangeSnapshot snapshot = await repository.GetRangeSnapshotAsync(1, TimeZoneInfo.Utc);

        Assert.Equal(1, removed);
        Assert.Equal(1, snapshot.Usage.RequestCount);
        Assert.Equal(12, snapshot.Usage.InputTokens);
        LocalTelemetryUsageBreakdown source = Assert.Single(snapshot.BySource);
        Assert.Equal("cloud-current", source.SourceId);
        Assert.Equal("连接中心当前来源 · 云端来源", source.SourceLabel);
        Assert.Equal(0, await repository.RemoveLegacyHistoryImportEventsAsync());
    }

    [Fact]
    public async Task RangeSnapshotAsync_UsesExactCalendarBoundariesForEveryDashboardSection()
    {
        using var fixture = new TemporaryTelemetryStore();
        DateTimeOffset now = new(2026, 7, 14, 14, 0, 0, TimeSpan.Zero);
        using var repository = new SqliteLocalTelemetryRepository(
            fixture.DatabasePath,
            timeProvider: new FixedTimeProvider(now));

        // This observation is one tick before the 30-day boundary and must not
        // leak into any selected dashboard range.
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 6, 14, 23, 59, 59, TimeSpan.Zero).AddTicks(9_999_999),
            CliKind.GeminiCli,
            inputTokens: 900,
            outputTokens: 9,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 90,
            sourceId: "outside-thirty",
            sourceLabel: "30 天外",
            model: "outside-30"));

        // The three inclusive calendar boundaries: 30 days, seven days, and
        // today.  Each has a distinct safe source, CLI and model so the
        // dashboard rankings prove they are calculated from the same range.
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 6, 15, 0, 0, 0, TimeSpan.Zero),
            CliKind.GeminiCli,
            inputTokens: 10,
            outputTokens: 1,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 10,
            sourceId: "thirty-boundary",
            sourceLabel: "30 天边界",
            model: "model-30"));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 7, 23, 59, 59, TimeSpan.Zero).AddTicks(9_999_999),
            CliKind.ClaudeCode,
            inputTokens: 20,
            outputTokens: 2,
            cachedInputTokens: 0,
            succeeded: false,
            elapsedMilliseconds: 20,
            sourceId: "thirty-only",
            sourceLabel: "仅 30 天",
            model: "model-thirty-only"));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 8, 0, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 30,
            outputTokens: 3,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 30,
            sourceId: "seven-boundary",
            sourceLabel: "7 天边界",
            model: "model-7"));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 13, 23, 59, 59, TimeSpan.Zero).AddTicks(9_999_999),
            CliKind.GeminiCli,
            inputTokens: 40,
            outputTokens: 4,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 40,
            sourceId: "this-week",
            sourceLabel: "本周",
            model: "model-week"));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 14, 0, 0, 0, TimeSpan.Zero),
            CliKind.ClaudeCode,
            inputTokens: 50,
            outputTokens: 5,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 50,
            sourceId: "today",
            sourceLabel: "今天",
            model: "model-today"));
        await repository.RecordUsageAsync(Usage(
            now,
            CliKind.Codex,
            inputTokens: 60,
            outputTokens: 6,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 60,
            sourceId: "today",
            sourceLabel: "今天",
            model: "model-today"));

        // A future observation may be queued by another client, but it is not
        // a fact available to a dashboard generated at 'now'.
        await repository.RecordUsageAsync(Usage(
            now.AddTicks(1),
            CliKind.Codex,
            inputTokens: 700,
            outputTokens: 7,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 70,
            sourceId: "future",
            sourceLabel: "未来",
            model: "model-future"));

        LocalTelemetryRangeSnapshot today = await repository.GetRangeSnapshotAsync(1, TimeZoneInfo.Utc);
        LocalTelemetryRangeSnapshot sevenDays = await repository.GetRangeSnapshotAsync(7, TimeZoneInfo.Utc);
        LocalTelemetryRangeSnapshot thirtyDays = await repository.GetRangeSnapshotAsync(30, TimeZoneInfo.Utc);

        AssertDashboardRange(
            today,
            expectedDays: 1,
            expectedStart: new DateOnly(2026, 7, 14),
            expectedRequestCount: 2,
            expectedInputTokens: 110,
            expectedSourceIds: ["today"],
            expectedCliKinds: [CliKind.ClaudeCode, CliKind.Codex],
            expectedModels: ["model-today"],
            expectedRecentCount: 2);
        Assert.Equal(new long[] { 2 }, today.DailyUsage.Select(day => day.Usage.RequestCount));

        AssertDashboardRange(
            sevenDays,
            expectedDays: 7,
            expectedStart: new DateOnly(2026, 7, 8),
            expectedRequestCount: 4,
            expectedInputTokens: 180,
            expectedSourceIds: ["seven-boundary", "this-week", "today"],
            expectedCliKinds: [CliKind.ClaudeCode, CliKind.Codex, CliKind.GeminiCli],
            expectedModels: ["model-7", "model-today", "model-week"],
            expectedRecentCount: 4);
        Assert.Equal(
            new long[] { 1, 0, 0, 0, 0, 1, 2 },
            sevenDays.DailyUsage.Select(day => day.Usage.RequestCount));

        AssertDashboardRange(
            thirtyDays,
            expectedDays: 30,
            expectedStart: new DateOnly(2026, 6, 15),
            expectedRequestCount: 6,
            expectedInputTokens: 210,
            expectedSourceIds: ["seven-boundary", "thirty-boundary", "thirty-only", "this-week", "today"],
            expectedCliKinds: [CliKind.ClaudeCode, CliKind.Codex, CliKind.GeminiCli],
            expectedModels: ["model-30", "model-7", "model-thirty-only", "model-today", "model-week"],
            expectedRecentCount: 6);
        Assert.Equal(1, thirtyDays.DailyUsage.Single(day => day.Date == new DateOnly(2026, 6, 15)).Usage.RequestCount);
        Assert.Equal(1, thirtyDays.DailyUsage.Single(day => day.Date == new DateOnly(2026, 7, 7)).Usage.RequestCount);

        Assert.All(thirtyDays.RecentActivity, item => Assert.InRange(item.OccurredAt, new DateTimeOffset(2026, 6, 15, 0, 0, 0, TimeSpan.Zero), now));
        Assert.DoesNotContain(thirtyDays.RecentActivity, item => item.SourceId is "outside-thirty" or "future");
    }

    [Fact]
    public async Task RangeSnapshotAsync_UsesTheRequestedTimeZoneForCalendarBoundaries()
    {
        using var fixture = new TemporaryTelemetryStore();
        DateTimeOffset now = new(2026, 7, 14, 1, 0, 0, TimeSpan.Zero);
        TimeZoneInfo utcPlusEight = TimeZoneInfo.CreateCustomTimeZone(
            "UTC+08-test",
            TimeSpan.FromHours(8),
            "UTC+08-test",
            "UTC+08-test");
        using var repository = new SqliteLocalTelemetryRepository(
            fixture.DatabasePath,
            timeProvider: new FixedTimeProvider(now));

        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 7, 15, 59, 59, TimeSpan.Zero).AddTicks(9_999_999),
            CliKind.GeminiCli,
            inputTokens: 1,
            outputTokens: 0,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 1,
            sourceId: "before-seven-local",
            sourceLabel: "本地 7 天前",
            model: "before-seven-local"));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 7, 16, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 2,
            outputTokens: 0,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 2,
            sourceId: "seven-local-boundary",
            sourceLabel: "本地 7 天边界",
            model: "seven-local-boundary"));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 13, 15, 59, 59, TimeSpan.Zero).AddTicks(9_999_999),
            CliKind.ClaudeCode,
            inputTokens: 4,
            outputTokens: 0,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 4,
            sourceId: "before-today-local",
            sourceLabel: "本地今天前",
            model: "before-today-local"));
        await repository.RecordUsageAsync(Usage(
            new DateTimeOffset(2026, 7, 13, 16, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 8,
            outputTokens: 0,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 8,
            sourceId: "today-local-boundary",
            sourceLabel: "本地今天边界",
            model: "today-local-boundary"));

        LocalTelemetryRangeSnapshot today = await repository.GetRangeSnapshotAsync(1, utcPlusEight);
        LocalTelemetryRangeSnapshot sevenDays = await repository.GetRangeSnapshotAsync(7, utcPlusEight);

        Assert.Equal(new DateOnly(2026, 7, 14), Assert.Single(today.DailyUsage).Date);
        Assert.Equal(1, today.Usage.RequestCount);
        Assert.Equal(8, today.Usage.InputTokens);
        Assert.Equal(["today-local-boundary"], today.BySource.Select(item => item.SourceId));
        Assert.Equal(new DateOnly(2026, 7, 8), sevenDays.DailyUsage[0].Date);
        Assert.Equal(new DateOnly(2026, 7, 14), sevenDays.DailyUsage[^1].Date);
        Assert.Equal(3, sevenDays.Usage.RequestCount);
        Assert.Equal(14, sevenDays.Usage.InputTokens);
        Assert.DoesNotContain(sevenDays.BySource, item => item.SourceId == "before-seven-local");
    }

    [Theory]
    [InlineData("https://example.test/v1")]
    [InlineData("https://user:password@example.test/v1")]
    [InlineData("https://example.test/v1?access_token=secret")]
    [InlineData("Authorization: Bearer secret-value")]
    [InlineData("api_key=secret-value")]
    public void EventContracts_RejectCredentialBearingMetadata(string unsafeValue)
    {
        Assert.Throws<ArgumentException>(() => Usage(
            new DateTimeOffset(2026, 7, 14, 12, 0, 0, TimeSpan.Zero),
            CliKind.Codex,
            inputTokens: 1,
            outputTokens: 1,
            cachedInputTokens: 0,
            succeeded: true,
            elapsedMilliseconds: 1,
            sourceLabel: unsafeValue));

        Assert.Throws<ArgumentException>(() => new LocalNetworkHealthProbe(
            new DateTimeOffset(2026, 7, 14, 12, 0, 0, TimeSpan.Zero),
            unsafeValue,
            sourceLabel: null,
            succeeded: true,
            latencyMilliseconds: 1));
    }

    [Fact]
    public async Task FilteredRangeSnapshot_FiltersSourceCliAndModelAcrossAllPanels()
    {
        using var fixture = new TemporaryTelemetryStore();
        DateTimeOffset now = new(2026, 7, 15, 12, 0, 0, TimeSpan.Zero);
        using var repository = new SqliteLocalTelemetryRepository(fixture.DatabasePath, timeProvider: new FixedTimeProvider(now));
        await repository.RecordUsageAsync(Usage(now.AddHours(-2), CliKind.Codex, 100, 10, 5, true, 80,
            sourceId: "a", sourceLabel: "来源 A", model: "gpt-a"));
        await repository.RecordUsageAsync(Usage(now.AddHours(-1), CliKind.ClaudeCode, 200, 20, 0, false, 160,
            sourceId: "b", sourceLabel: "来源 B", model: "claude-b"));

        LocalTelemetryRangeSnapshot snapshot = await repository.GetFilteredRangeSnapshotAsync(
            7,
            new LocalTelemetryQueryFilter("a", CliKind.Codex, "gpt-a"),
            TimeZoneInfo.Utc);

        Assert.Equal(1, snapshot.Usage.RequestCount);
        Assert.Equal(100, snapshot.Usage.InputTokens);
        Assert.Equal(1, snapshot.DailyUsage.Sum(item => item.Usage.RequestCount));
        Assert.Equal(1, snapshot.RecentHourlyUsage.Sum(item => item.Usage.RequestCount));
        Assert.Equal("a", Assert.Single(snapshot.BySource).SourceId);
        Assert.Equal(CliKind.Codex, Assert.Single(snapshot.ByCli).CliKind);
        Assert.Equal("gpt-a", Assert.Single(snapshot.ByModel).Model);
        Assert.Equal("a", Assert.Single(snapshot.RecentActivity).SourceId);
    }

    private static LocalUsageTelemetryEvent Usage(
        DateTimeOffset timestamp,
        CliKind cliKind,
        long inputTokens,
        long outputTokens,
        long cachedInputTokens,
        bool succeeded,
        int? elapsedMilliseconds,
        string sourceId = "local-machine",
        string sourceLabel = "本机中转",
        string model = "gpt-5",
        long cacheCreationTokens = 0,
        double? estimatedCost = null)
        => new(
            timestamp,
            cliKind,
            sourceId,
            sourceLabel,
            model,
            inputTokens,
            outputTokens,
            cachedInputTokens,
            succeeded,
            elapsedMilliseconds,
            cacheCreationTokens,
            estimatedCost);

    private static void AssertDashboardRange(
        LocalTelemetryRangeSnapshot snapshot,
        int expectedDays,
        DateOnly expectedStart,
        long expectedRequestCount,
        long expectedInputTokens,
        IReadOnlyList<string> expectedSourceIds,
        IReadOnlyList<CliKind> expectedCliKinds,
        IReadOnlyList<string> expectedModels,
        int expectedRecentCount)
    {
        Assert.Equal(expectedDays, snapshot.Days);
        Assert.Equal(expectedDays, snapshot.DailyUsage.Count);
        Assert.Equal(expectedStart, snapshot.DailyUsage[0].Date);
        Assert.Equal(expectedStart.AddDays(expectedDays - 1), snapshot.DailyUsage[^1].Date);
        Assert.Equal(expectedRequestCount, snapshot.Usage.RequestCount);
        Assert.Equal(expectedInputTokens, snapshot.Usage.InputTokens);
        Assert.Equal(expectedRecentCount, snapshot.RecentActivity.Count);
        Assert.Equal(
            expectedSourceIds.Order(StringComparer.Ordinal),
            snapshot.BySource.Select(item => item.SourceId).OfType<string>().Order(StringComparer.Ordinal));
        Assert.Equal(
            expectedCliKinds.Order(),
            snapshot.ByCli.Select(item => item.CliKind).OfType<CliKind>().Order());
        Assert.Equal(
            expectedModels.Order(StringComparer.Ordinal),
            snapshot.ByModel.Select(item => item.Model).OfType<string>().Order(StringComparer.Ordinal));
        Assert.True(snapshot.RecentActivity.SequenceEqual(snapshot.RecentActivity.OrderByDescending(item => item.OccurredAt)));
    }

    private sealed class FixedTimeProvider(DateTimeOffset utcNow) : TimeProvider
    {
        public override DateTimeOffset GetUtcNow() => utcNow;
    }

    private sealed class TemporaryTelemetryStore : IDisposable
    {
        public TemporaryTelemetryStore()
        {
            Root = Path.Combine(Path.GetTempPath(), "LanAi.Workspace.Tests", Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(Root);
            DatabasePath = Path.Combine(Root, "telemetry.db");
        }

        public string Root { get; }

        public string DatabasePath { get; }

        public void Dispose()
        {
            SqliteConnection.ClearAllPools();
            string fullRoot = Path.GetFullPath(Root);
            string safeParent = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "LanAi.Workspace.Tests"));
            if (fullRoot.StartsWith(safeParent, StringComparison.OrdinalIgnoreCase) && Directory.Exists(fullRoot))
            {
                Directory.Delete(fullRoot, recursive: true);
            }
        }
    }
}
