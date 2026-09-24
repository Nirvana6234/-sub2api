using System.Globalization;
using LanAi.Workspace.Core;
using Microsoft.Data.Sqlite;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// SQLite-backed, privacy-bounded local observability store.  It intentionally
/// persists only numeric request aggregates plus source identifiers/labels and
/// health outcomes.  It never accepts network endpoints, prompts, responses or
/// credentials, and it keeps storage bounded by retention age and row count.
/// </summary>
public sealed class SqliteLocalTelemetryRepository : ILocalTelemetryRepository, IDisposable
{
    private readonly string _connectionString;
    private readonly string _databaseDirectory;
    private readonly LocalTelemetryStorageOptions _options;
    private readonly TimeProvider _timeProvider;
    private readonly SemaphoreSlim _initializationGate = new(1, 1);
    private readonly SemaphoreSlim _writeGate = new(1, 1);
    private volatile bool _initialized;
    private bool _disposed;

    public SqliteLocalTelemetryRepository(
        AppDataPaths paths,
        LocalTelemetryStorageOptions? options = null,
        TimeProvider? timeProvider = null)
        : this(
            paths?.TelemetryDatabasePath ?? throw new ArgumentNullException(nameof(paths)),
            options,
            timeProvider)
    {
    }

    public SqliteLocalTelemetryRepository(
        string databasePath,
        LocalTelemetryStorageOptions? options = null,
        TimeProvider? timeProvider = null)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(databasePath);

        string fullPath = Path.GetFullPath(Environment.ExpandEnvironmentVariables(databasePath));
        _databaseDirectory = Path.GetDirectoryName(fullPath)
            ?? throw new ArgumentException("The database path must include a directory.", nameof(databasePath));

        _connectionString = new SqliteConnectionStringBuilder
        {
            DataSource = fullPath,
            Mode = SqliteOpenMode.ReadWriteCreate,
            Cache = SqliteCacheMode.Shared,
            Pooling = true,
        }.ToString();
        _options = options ?? new LocalTelemetryStorageOptions();
        _options.Validate();
        _timeProvider = timeProvider ?? TimeProvider.System;
    }

    public async Task RecordUsageAsync(
        LocalUsageTelemetryEvent telemetryEvent,
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        ArgumentNullException.ThrowIfNull(telemetryEvent);
        await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);

        await _writeGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await using SqliteConnection connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
            using SqliteTransaction transaction = connection.BeginTransaction();

            await using (SqliteCommand command = connection.CreateCommand())
            {
                command.Transaction = transaction;
                command.CommandText = """
                    INSERT INTO local_usage_events (
                        occurred_at, cli_kind, source_id, source_label, model,
                        input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens,
                        succeeded, elapsed_ms, estimated_cost, first_token_ms,
                        status_category, is_streaming, pricing_model)
                    VALUES (
                        $occurred_at, $cli_kind, $source_id, $source_label, $model,
                        $input_tokens, $output_tokens, $cached_input_tokens, $cache_creation_tokens,
                        $succeeded, $elapsed_ms, $estimated_cost, $first_token_ms,
                        $status_category, $is_streaming, $pricing_model);
                    """;
                command.Parameters.AddWithValue("$occurred_at", FormatTimestamp(telemetryEvent.Timestamp));
                command.Parameters.AddWithValue("$cli_kind", (int)telemetryEvent.CliKind);
                command.Parameters.AddWithValue("$source_id", (object?)telemetryEvent.SourceId ?? DBNull.Value);
                command.Parameters.AddWithValue("$source_label", (object?)telemetryEvent.SourceLabel ?? DBNull.Value);
                command.Parameters.AddWithValue("$model", (object?)telemetryEvent.Model ?? DBNull.Value);
                command.Parameters.AddWithValue("$input_tokens", telemetryEvent.InputTokens);
                command.Parameters.AddWithValue("$output_tokens", telemetryEvent.OutputTokens);
                command.Parameters.AddWithValue("$cached_input_tokens", telemetryEvent.CachedInputTokens);
                command.Parameters.AddWithValue("$cache_creation_tokens", telemetryEvent.CacheCreationTokens);
                command.Parameters.AddWithValue("$succeeded", telemetryEvent.Succeeded ? 1 : 0);
                command.Parameters.AddWithValue("$elapsed_ms", (object?)telemetryEvent.ElapsedMilliseconds ?? DBNull.Value);
                command.Parameters.AddWithValue("$estimated_cost", (object?)telemetryEvent.EstimatedCost ?? DBNull.Value);
                command.Parameters.AddWithValue("$first_token_ms", (object?)telemetryEvent.FirstTokenMilliseconds ?? DBNull.Value);
                command.Parameters.AddWithValue("$status_category", (object?)telemetryEvent.StatusCategory ?? DBNull.Value);
                command.Parameters.AddWithValue("$is_streaming", telemetryEvent.IsStreaming is { } streaming ? streaming ? 1 : 0 : DBNull.Value);
                command.Parameters.AddWithValue("$pricing_model", (object?)telemetryEvent.PricingModel ?? DBNull.Value);
                await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
            }

            await PruneAsync(connection, transaction, cancellationToken).ConfigureAwait(false);
            transaction.Commit();
        }
        finally
        {
            _writeGate.Release();
        }
    }

    public async Task RecordNetworkProbeAsync(
        LocalNetworkHealthProbe probe,
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        ArgumentNullException.ThrowIfNull(probe);
        await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);

        await _writeGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await using SqliteConnection connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
            using SqliteTransaction transaction = connection.BeginTransaction();

            await using (SqliteCommand command = connection.CreateCommand())
            {
                command.Transaction = transaction;
                command.CommandText = """
                    INSERT INTO local_network_probes (
                        occurred_at, source_id, source_label, succeeded, latency_ms, status_category)
                    VALUES ($occurred_at, $source_id, $source_label, $succeeded, $latency_ms, $status_category);
                    """;
                command.Parameters.AddWithValue("$occurred_at", FormatTimestamp(probe.Timestamp));
                command.Parameters.AddWithValue("$source_id", (object?)probe.SourceId ?? DBNull.Value);
                command.Parameters.AddWithValue("$source_label", (object?)probe.SourceLabel ?? DBNull.Value);
                command.Parameters.AddWithValue("$succeeded", probe.Succeeded ? 1 : 0);
                command.Parameters.AddWithValue("$latency_ms", (object?)probe.LatencyMilliseconds ?? DBNull.Value);
                command.Parameters.AddWithValue("$status_category", (object?)probe.StatusCategory ?? DBNull.Value);
                await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
            }

            await PruneAsync(connection, transaction, cancellationToken).ConfigureAwait(false);
            transaction.Commit();
        }
        finally
        {
            _writeGate.Release();
        }
    }

    public async Task<LocalTelemetrySnapshot> GetSnapshotAsync(
        TimeZoneInfo? timeZone = null,
        CancellationToken cancellationToken = default)
    {
        LocalTelemetryRangeSnapshot range = await GetRangeSnapshotAsync(
                days: 7,
                timeZone,
                cancellationToken)
            .ConfigureAwait(false);
        LocalTelemetryUsageSummary todayUsage = range.DailyUsage.Count == 0
            ? LocalTelemetryUsageSummary.Empty
            : range.DailyUsage[^1].Usage;
        return new LocalTelemetrySnapshot(
            range.GeneratedAt,
            todayUsage,
            range.Usage,
            range.DailyUsage,
            range.LatestNetworkStatus,
            range.BySource,
            range.ByCli,
            range.ByModel,
            range.RecentActivity,
            range.RecentHourlyUsage);
    }

    public async Task<IReadOnlyList<LocalNetworkHealthSummary>> GetNetworkHealthSummariesAsync(
        DateTimeOffset sinceUtc,
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        if (sinceUtc == default)
        {
            throw new ArgumentException("A UTC range start is required.", nameof(sinceUtc));
        }

        await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);
        await using SqliteConnection connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = $"""
            SELECT occurred_at, source_id, source_label, succeeded, latency_ms, status_category
            FROM local_network_probes
            WHERE occurred_at >= $since_utc
            ORDER BY source_id, occurred_at, id;
            """;
        command.Parameters.AddWithValue("$since_utc", FormatTimestamp(sinceUtc.ToUniversalTime()));

        var groups = new Dictionary<string, List<NetworkProbeRow>>(StringComparer.OrdinalIgnoreCase);
        await using SqliteDataReader reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
        while (await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
        {
            string? sourceId = reader.IsDBNull(1) ? null : reader.GetString(1);
            string? sourceLabel = reader.IsDBNull(2) ? null : reader.GetString(2);
            string key = sourceId ?? sourceLabel ?? "unknown";
            if (!groups.TryGetValue(key, out List<NetworkProbeRow>? rows))
            {
                rows = [];
                groups[key] = rows;
            }

            rows.Add(new NetworkProbeRow(
                ParseTimestamp(reader.GetString(0)),
                sourceId,
                sourceLabel,
                reader.GetInt32(3) != 0,
                reader.IsDBNull(4) ? null : reader.GetInt32(4),
                reader.IsDBNull(5) ? null : reader.GetString(5)));
        }

        return groups.Values
            .Select(CreateNetworkHealthSummary)
            .OrderBy(summary => summary.SourceLabel ?? summary.SourceId, StringComparer.CurrentCultureIgnoreCase)
            .ToArray();
    }

    public async Task<IReadOnlyList<LocalNetworkHealthProbe>> GetRecentNetworkProbesAsync(
        string sourceId,
        int limit = 60,
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        if (string.IsNullOrWhiteSpace(sourceId))
        {
            throw new ArgumentException("A source identifier is required.", nameof(sourceId));
        }

        if (limit is < 1 or > 240)
        {
            throw new ArgumentOutOfRangeException(nameof(limit), "Probe history limit must be between 1 and 240.");
        }

        await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);
        await using SqliteConnection connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = """
            SELECT occurred_at, source_id, source_label, succeeded, latency_ms, status_category
            FROM local_network_probes
            WHERE source_id = $source_id
            ORDER BY occurred_at DESC, id DESC
            LIMIT $limit;
            """;
        command.Parameters.AddWithValue("$source_id", sourceId.Trim());
        command.Parameters.AddWithValue("$limit", limit);

        var probes = new List<LocalNetworkHealthProbe>(limit);
        await using SqliteDataReader reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
        while (await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
        {
            probes.Add(new LocalNetworkHealthProbe(
                ParseTimestamp(reader.GetString(0)),
                reader.IsDBNull(1) ? null : reader.GetString(1),
                reader.IsDBNull(2) ? null : reader.GetString(2),
                reader.GetInt32(3) != 0,
                reader.IsDBNull(4) ? null : reader.GetInt32(4),
                reader.IsDBNull(5) ? null : reader.GetString(5)));
        }

        probes.Reverse();
        return probes;
    }

    public async Task<LocalTelemetryRangeSnapshot> GetRangeSnapshotAsync(
        int days,
        TimeZoneInfo? timeZone = null,
        CancellationToken cancellationToken = default)
        => await GetFilteredRangeSnapshotAsync(
                days,
                new LocalTelemetryQueryFilter(),
                timeZone,
                cancellationToken)
            .ConfigureAwait(false);

    public async Task<LocalTelemetryRangeSnapshot> GetFilteredRangeSnapshotAsync(
        int days,
        LocalTelemetryQueryFilter filter,
        TimeZoneInfo? timeZone = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(filter);
        ObjectDisposedException.ThrowIf(_disposed, this);
        await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);
        await PruneStoredEventsAsync(cancellationToken).ConfigureAwait(false);

        int normalizedDays = Math.Clamp(days, 1, 30);
        TimeZoneInfo effectiveTimeZone = timeZone ?? TimeZoneInfo.Local;
        DateTimeOffset now = _timeProvider.GetUtcNow();
        DateTimeOffset localNow = TimeZoneInfo.ConvertTime(now, effectiveTimeZone);
        DateOnly today = DateOnly.FromDateTime(localNow.Date);
        DateOnly firstDay = today.AddDays(-(normalizedDays - 1));
        DateTimeOffset queryEndExclusive = now.AddTicks(1);

        await using SqliteConnection connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
        DateTimeOffset rangeStart = GetStartOfLocalDayUtc(firstDay, effectiveTimeZone);
        LocalTelemetryUsageSummary rangeUsage = await GetUsageSummaryAsync(
                connection,
                rangeStart,
                queryEndExclusive,
                filter,
                cancellationToken)
            .ConfigureAwait(false);

        var dailyUsage = new List<LocalTelemetryDailyUsage>(capacity: normalizedDays);
        for (int offset = 0; offset < normalizedDays; offset++)
        {
            DateOnly day = firstDay.AddDays(offset);
            DateTimeOffset start = GetStartOfLocalDayUtc(day, effectiveTimeZone);
            DateTimeOffset end = day == today
                ? queryEndExclusive
                : GetStartOfLocalDayUtc(day.AddDays(1), effectiveTimeZone);
            LocalTelemetryUsageSummary usage = await GetUsageSummaryAsync(
                    connection,
                    start,
                    end,
                    filter,
                    cancellationToken)
                .ConfigureAwait(false);
            dailyUsage.Add(new LocalTelemetryDailyUsage(day, usage));
        }

        DateTimeOffset currentUtcHour = new(
            now.UtcDateTime.Year,
            now.UtcDateTime.Month,
            now.UtcDateTime.Day,
            now.UtcDateTime.Hour,
            0,
            0,
            TimeSpan.Zero);
        var hourlyUsage = new List<LocalTelemetryHourlyUsage>(capacity: 24);
        for (int offset = 0; offset < 24; offset++)
        {
            DateTimeOffset start = currentUtcHour.AddHours(offset - 23);
            DateTimeOffset end = offset == 23
                ? queryEndExclusive
                : start.AddHours(1);
            LocalTelemetryUsageSummary usage = await GetUsageSummaryAsync(
                    connection,
                    start,
                    end,
                    filter,
                    cancellationToken)
                .ConfigureAwait(false);
            hourlyUsage.Add(new LocalTelemetryHourlyUsage(start, usage));
        }

        LocalNetworkHealthStatus? latestNetworkStatus = await GetLatestNetworkStatusAsync(
                connection,
                cancellationToken)
            .ConfigureAwait(false);

        IReadOnlyList<LocalTelemetryUsageBreakdown> bySource = await GetUsageBreakdownAsync(
                connection,
                rangeStart,
                queryEndExclusive,
                LocalTelemetryBreakdownDimension.Source,
                filter,
                cancellationToken)
            .ConfigureAwait(false);
        IReadOnlyList<LocalTelemetryUsageBreakdown> byCli = await GetUsageBreakdownAsync(
                connection,
                rangeStart,
                queryEndExclusive,
                LocalTelemetryBreakdownDimension.Cli,
                filter,
                cancellationToken)
            .ConfigureAwait(false);
        IReadOnlyList<LocalTelemetryUsageBreakdown> byModel = await GetUsageBreakdownAsync(
                connection,
                rangeStart,
                queryEndExclusive,
                LocalTelemetryBreakdownDimension.Model,
                filter,
                cancellationToken)
            .ConfigureAwait(false);
        IReadOnlyList<LocalTelemetryRecentActivity> recentActivity = await GetRecentActivityAsync(
                connection,
                rangeStart,
                queryEndExclusive,
                filter,
                cancellationToken)
            .ConfigureAwait(false);

        return new LocalTelemetryRangeSnapshot(
            now,
            normalizedDays,
            rangeUsage,
            dailyUsage,
            latestNetworkStatus,
            bySource,
            byCli,
            byModel,
            recentActivity,
            hourlyUsage);
    }

    /// <summary>
    /// Clears only the old history-import rows that have no source identity at
    /// all.  Live workspace telemetry always carries the selected Connection
    /// Center source, whereas the retired importer wrote anonymous cumulative
    /// Codex/Claude snapshots.  Keeping these categories apart avoids turning
    /// a copied or resumed history file into fictitious recent usage.
    /// </summary>
    public async Task<int> RemoveLegacyHistoryImportEventsAsync(
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);

        await _writeGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await using SqliteConnection connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
            await using SqliteCommand command = connection.CreateCommand();
            command.CommandText = """
                DELETE FROM local_usage_events
                WHERE source_id IS NULL
                  AND source_label IS NULL;
                """;
            return await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            _writeGate.Release();
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _initializationGate.Dispose();
        _writeGate.Dispose();
    }

    private async Task EnsureInitializedAsync(CancellationToken cancellationToken)
    {
        if (_initialized)
        {
            return;
        }

        await _initializationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (_initialized)
            {
                return;
            }

            Directory.CreateDirectory(_databaseDirectory);
            await using SqliteConnection connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
            await using (SqliteCommand command = connection.CreateCommand())
            {
                command.CommandText = """
                    PRAGMA journal_mode = WAL;
                    PRAGMA busy_timeout = 5000;

                    CREATE TABLE IF NOT EXISTS local_usage_events (
                        id INTEGER PRIMARY KEY,
                        occurred_at TEXT NOT NULL,
                        cli_kind INTEGER NOT NULL,
                        source_id TEXT NULL,
                        source_label TEXT NULL,
                        model TEXT NULL,
                        input_tokens INTEGER NOT NULL CHECK (input_tokens >= 0),
                        output_tokens INTEGER NOT NULL CHECK (output_tokens >= 0),
                        cached_input_tokens INTEGER NOT NULL CHECK (cached_input_tokens >= 0),
                        cache_creation_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_creation_tokens >= 0),
                        succeeded INTEGER NOT NULL CHECK (succeeded IN (0, 1)),
                        elapsed_ms INTEGER NULL CHECK (elapsed_ms IS NULL OR elapsed_ms >= 0),
                        estimated_cost REAL NULL CHECK (estimated_cost IS NULL OR estimated_cost >= 0),
                        first_token_ms INTEGER NULL CHECK (first_token_ms IS NULL OR first_token_ms >= 0),
                        status_category TEXT NULL,
                        is_streaming INTEGER NULL CHECK (is_streaming IS NULL OR is_streaming IN (0, 1)),
                        pricing_model TEXT NULL
                    );

                    CREATE INDEX IF NOT EXISTS ix_local_usage_events_occurred_at
                        ON local_usage_events(occurred_at DESC, id DESC);

                    CREATE TABLE IF NOT EXISTS local_usage_daily_rollups (
                        day_utc TEXT PRIMARY KEY,
                        request_count INTEGER NOT NULL CHECK (request_count >= 0),
                        successful_request_count INTEGER NOT NULL CHECK (successful_request_count >= 0),
                        input_tokens INTEGER NOT NULL CHECK (input_tokens >= 0),
                        output_tokens INTEGER NOT NULL CHECK (output_tokens >= 0),
                        cached_input_tokens INTEGER NOT NULL CHECK (cached_input_tokens >= 0),
                        cache_creation_tokens INTEGER NOT NULL CHECK (cache_creation_tokens >= 0),
                        elapsed_total_ms INTEGER NOT NULL CHECK (elapsed_total_ms >= 0),
                        elapsed_sample_count INTEGER NOT NULL CHECK (elapsed_sample_count >= 0),
                        estimated_cost REAL NULL CHECK (estimated_cost IS NULL OR estimated_cost >= 0)
                    );

                    CREATE TABLE IF NOT EXISTS local_network_probes (
                        id INTEGER PRIMARY KEY,
                        occurred_at TEXT NOT NULL,
                        source_id TEXT NULL,
                        source_label TEXT NULL,
                        succeeded INTEGER NOT NULL CHECK (succeeded IN (0, 1)),
                        latency_ms INTEGER NULL CHECK (latency_ms IS NULL OR latency_ms >= 0),
                        status_category TEXT NULL
                    );

                    CREATE INDEX IF NOT EXISTS ix_local_network_probes_occurred_at
                        ON local_network_probes(occurred_at DESC, id DESC);

                    PRAGMA user_version = 3;
                    """;
                await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
            }

            await EnsureColumnAsync(
                    connection,
                    "local_usage_events",
                    "cache_creation_tokens",
                    "INTEGER NOT NULL DEFAULT 0 CHECK (cache_creation_tokens >= 0)",
                    cancellationToken)
                .ConfigureAwait(false);
            await EnsureColumnAsync(
                    connection,
                    "local_network_probes",
                    "status_category",
                    "TEXT NULL",
                    cancellationToken)
                .ConfigureAwait(false);
            await EnsureColumnAsync(
                    connection,
                    "local_usage_events",
                    "estimated_cost",
                    "REAL NULL CHECK (estimated_cost IS NULL OR estimated_cost >= 0)",
                    cancellationToken)
                .ConfigureAwait(false);
            await EnsureColumnAsync(connection, "local_usage_events", "first_token_ms", "INTEGER NULL CHECK (first_token_ms IS NULL OR first_token_ms >= 0)", cancellationToken).ConfigureAwait(false);
            await EnsureColumnAsync(connection, "local_usage_events", "status_category", "TEXT NULL", cancellationToken).ConfigureAwait(false);
            await EnsureColumnAsync(connection, "local_usage_events", "is_streaming", "INTEGER NULL CHECK (is_streaming IS NULL OR is_streaming IN (0, 1))", cancellationToken).ConfigureAwait(false);
            await EnsureColumnAsync(connection, "local_usage_events", "pricing_model", "TEXT NULL", cancellationToken).ConfigureAwait(false);

            using SqliteTransaction transaction = connection.BeginTransaction();
            await PruneAsync(connection, transaction, cancellationToken).ConfigureAwait(false);
            transaction.Commit();
            _initialized = true;
        }
        finally
        {
            _initializationGate.Release();
        }
    }

    private async Task<LocalTelemetryUsageSummary> GetUsageSummaryAsync(
        SqliteConnection connection,
        DateTimeOffset startInclusive,
        DateTimeOffset endExclusive,
        LocalTelemetryQueryFilter? filter,
        CancellationToken cancellationToken)
    {
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = $"""
            SELECT COUNT(*),
                   COALESCE(SUM(CASE WHEN succeeded = 1 THEN 1 ELSE 0 END), 0),
                   COALESCE(SUM(input_tokens), 0),
                   COALESCE(SUM(output_tokens), 0),
                   COALESCE(SUM(cached_input_tokens), 0),
                   COALESCE(SUM(cache_creation_tokens), 0),
                   AVG(elapsed_ms),
                   SUM(estimated_cost)
            FROM local_usage_events
            WHERE occurred_at >= $start_inclusive
              AND occurred_at < $end_exclusive
              {BuildFilterSql(filter)};
            """;
        command.Parameters.AddWithValue("$start_inclusive", FormatTimestamp(startInclusive));
        command.Parameters.AddWithValue("$end_exclusive", FormatTimestamp(endExclusive));
        AddFilterParameters(command, filter);

        await using SqliteDataReader reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
        if (!await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
        {
            return LocalTelemetryUsageSummary.Empty;
        }

        long requestCount = reader.GetInt64(0);
        long successfulCount = reader.GetInt64(1);
        long inputTokens = reader.GetInt64(2);
        long outputTokens = reader.GetInt64(3);
        long cachedInputTokens = reader.GetInt64(4);
        long cacheCreationTokens = reader.GetInt64(5);
        double? averageLatency = reader.IsDBNull(6) ? null : reader.GetDouble(6);
        double? estimatedCost = reader.IsDBNull(7) ? null : reader.GetDouble(7);
        double? successRate = requestCount == 0
            ? null
            : (successfulCount * 100d) / requestCount;

        return new LocalTelemetryUsageSummary(
            requestCount,
            successfulCount,
            requestCount - successfulCount,
            inputTokens,
            outputTokens,
            cachedInputTokens,
            successRate,
            averageLatency)
        {
            CacheCreationTokens = cacheCreationTokens,
            EstimatedCost = estimatedCost,
        };
    }

    private static async Task<IReadOnlyList<LocalTelemetryUsageBreakdown>> GetUsageBreakdownAsync(
        SqliteConnection connection,
        DateTimeOffset startInclusive,
        DateTimeOffset endExclusive,
        LocalTelemetryBreakdownDimension dimension,
        LocalTelemetryQueryFilter? filter,
        CancellationToken cancellationToken)
    {
        string projection;
        string grouping;
        switch (dimension)
        {
            case LocalTelemetryBreakdownDimension.Source:
                projection = "source_id, source_label, NULL AS cli_kind, NULL AS model";
                grouping = "source_id, source_label";
                break;
            case LocalTelemetryBreakdownDimension.Cli:
                projection = "NULL AS source_id, NULL AS source_label, cli_kind, NULL AS model";
                grouping = "cli_kind";
                break;
            case LocalTelemetryBreakdownDimension.Model:
                projection = "NULL AS source_id, NULL AS source_label, NULL AS cli_kind, model";
                grouping = "model";
                break;
            default:
                throw new ArgumentOutOfRangeException(nameof(dimension));
        }

        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = $"""
            SELECT {projection},
                   COUNT(*) AS request_count,
                   COALESCE(SUM(CASE WHEN succeeded = 1 THEN 1 ELSE 0 END), 0) AS successful_count,
                   COALESCE(SUM(input_tokens), 0) AS input_tokens,
                   COALESCE(SUM(output_tokens), 0) AS output_tokens,
                   COALESCE(SUM(cached_input_tokens), 0) AS cached_input_tokens,
                   COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
                   AVG(elapsed_ms) AS average_latency_ms,
                   SUM(estimated_cost) AS estimated_cost
            FROM local_usage_events
            WHERE occurred_at >= $start_inclusive
              AND occurred_at < $end_exclusive
              {BuildFilterSql(filter)}
            GROUP BY {grouping}
            ORDER BY request_count DESC,
                     (COALESCE(SUM(input_tokens), 0) + COALESCE(SUM(output_tokens), 0)) DESC
            LIMIT $limit;
            """;
        command.Parameters.AddWithValue("$start_inclusive", FormatTimestamp(startInclusive));
        command.Parameters.AddWithValue("$end_exclusive", FormatTimestamp(endExclusive));
        AddFilterParameters(command, filter);
        command.Parameters.AddWithValue("$limit", 24);

        var rows = new List<LocalTelemetryUsageBreakdown>();
        await using SqliteDataReader reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
        while (await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
        {
            CliKind? cliKind = null;
            if (!reader.IsDBNull(2))
            {
                int rawCli = reader.GetInt32(2);
                if (!Enum.IsDefined((CliKind)rawCli))
                {
                    // The table is private to this application, but an invalid
                    // legacy row must never be mislabeled as a different CLI.
                    continue;
                }

                cliKind = (CliKind)rawCli;
            }

            rows.Add(new LocalTelemetryUsageBreakdown(
                reader.IsDBNull(0) ? null : reader.GetString(0),
                reader.IsDBNull(1) ? null : reader.GetString(1),
                cliKind,
                reader.IsDBNull(3) ? null : reader.GetString(3),
                ReadUsageSummary(reader, 4)));
        }

        return rows;
    }

    private static async Task<IReadOnlyList<LocalTelemetryRecentActivity>> GetRecentActivityAsync(
        SqliteConnection connection,
        DateTimeOffset startInclusive,
        DateTimeOffset endExclusive,
        LocalTelemetryQueryFilter? filter,
        CancellationToken cancellationToken)
    {
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = $"""
            SELECT occurred_at, cli_kind, source_id, source_label, model,
                   succeeded, input_tokens, output_tokens, cached_input_tokens,
                   elapsed_ms, cache_creation_tokens, estimated_cost, first_token_ms,
                   status_category, is_streaming, pricing_model
            FROM local_usage_events
            WHERE occurred_at >= $start_inclusive
              AND occurred_at < $end_exclusive
              {BuildFilterSql(filter)}
            ORDER BY occurred_at DESC, id DESC
            LIMIT $limit;
            """;
        command.Parameters.AddWithValue("$start_inclusive", FormatTimestamp(startInclusive));
        command.Parameters.AddWithValue("$end_exclusive", FormatTimestamp(endExclusive));
        AddFilterParameters(command, filter);
        command.Parameters.AddWithValue("$limit", 18);

        var rows = new List<LocalTelemetryRecentActivity>();
        await using SqliteDataReader reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
        while (await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
        {
            int rawCli = reader.GetInt32(1);
            if (!Enum.IsDefined((CliKind)rawCli))
            {
                continue;
            }

            rows.Add(new LocalTelemetryRecentActivity(
                ParseTimestamp(reader.GetString(0)),
                (CliKind)rawCli,
                reader.IsDBNull(2) ? null : reader.GetString(2),
                reader.IsDBNull(3) ? null : reader.GetString(3),
                reader.IsDBNull(4) ? null : reader.GetString(4),
                reader.GetInt32(5) != 0,
                reader.GetInt64(6),
                reader.GetInt64(7),
                reader.GetInt64(8),
                reader.IsDBNull(9) ? null : reader.GetInt32(9),
                reader.GetInt64(10),
                reader.IsDBNull(11) ? null : reader.GetDouble(11),
                reader.IsDBNull(12) ? null : reader.GetInt32(12),
                reader.IsDBNull(13) ? null : reader.GetString(13),
                reader.IsDBNull(14) ? null : reader.GetInt32(14) != 0,
                reader.IsDBNull(15) ? null : reader.GetString(15)));
        }

        return rows;
    }

    private static string BuildFilterSql(LocalTelemetryQueryFilter? filter)
    {
        if (filter is null || filter.IsEmpty) return string.Empty;
        var clauses = new List<string>();
        if (!string.IsNullOrWhiteSpace(filter.SourceId)) clauses.Add("AND source_id = $filter_source_id");
        if (filter.CliKind is not null) clauses.Add("AND cli_kind = $filter_cli_kind");
        if (!string.IsNullOrWhiteSpace(filter.Model)) clauses.Add("AND model = $filter_model");
        return string.Join(Environment.NewLine, clauses);
    }

    private static void AddFilterParameters(SqliteCommand command, LocalTelemetryQueryFilter? filter)
    {
        if (filter is null) return;
        if (!string.IsNullOrWhiteSpace(filter.SourceId)) command.Parameters.AddWithValue("$filter_source_id", filter.SourceId);
        if (filter.CliKind is not null) command.Parameters.AddWithValue("$filter_cli_kind", (int)filter.CliKind.Value);
        if (!string.IsNullOrWhiteSpace(filter.Model)) command.Parameters.AddWithValue("$filter_model", filter.Model);
    }

    private static LocalTelemetryUsageSummary ReadUsageSummary(SqliteDataReader reader, int offset)
    {
        long requestCount = reader.GetInt64(offset);
        long successfulCount = reader.GetInt64(offset + 1);
        double? averageLatency = reader.IsDBNull(offset + 6) ? null : reader.GetDouble(offset + 6);
        double? estimatedCost = reader.IsDBNull(offset + 7) ? null : reader.GetDouble(offset + 7);
        return new LocalTelemetryUsageSummary(
            requestCount,
            successfulCount,
            requestCount - successfulCount,
            reader.GetInt64(offset + 2),
            reader.GetInt64(offset + 3),
            reader.GetInt64(offset + 4),
            requestCount == 0 ? null : (successfulCount * 100d) / requestCount,
            averageLatency)
        {
            CacheCreationTokens = reader.GetInt64(offset + 5),
            EstimatedCost = estimatedCost,
        };
    }

    private static LocalNetworkHealthSummary CreateNetworkHealthSummary(IReadOnlyList<NetworkProbeRow> rows)
    {
        NetworkProbeRow latest = rows[^1];
        long successful = rows.LongCount(row => row.Succeeded);
        int[] latencies = rows
            .Where(row => row.Succeeded && row.LatencyMilliseconds is not null)
            .Select(row => row.LatencyMilliseconds!.Value)
            .Order()
            .ToArray();
        DateTimeOffset? lastSuccess = rows
            .Where(row => row.Succeeded)
            .Select(row => (DateTimeOffset?)row.OccurredAt)
            .LastOrDefault();
        return new LocalNetworkHealthSummary(
            latest.SourceId,
            latest.SourceLabel,
            rows.Count,
            successful,
            rows.Count == 0 ? null : successful * 100d / rows.Count,
            Percentile(latencies, 0.50),
            Percentile(latencies, 0.95),
            lastSuccess,
            latest.StatusCategory);
    }

    private static int? Percentile(IReadOnlyList<int> sortedValues, double percentile)
    {
        if (sortedValues.Count == 0)
        {
            return null;
        }

        int index = Math.Clamp((int)Math.Ceiling(percentile * sortedValues.Count) - 1, 0, sortedValues.Count - 1);
        return sortedValues[index];
    }

    private sealed record NetworkProbeRow(
        DateTimeOffset OccurredAt,
        string? SourceId,
        string? SourceLabel,
        bool Succeeded,
        int? LatencyMilliseconds,
        string? StatusCategory);

    private enum LocalTelemetryBreakdownDimension
    {
        Source,
        Cli,
        Model,
    }

    private static async Task EnsureColumnAsync(
        SqliteConnection connection,
        string tableName,
        string columnName,
        string columnDefinition,
        CancellationToken cancellationToken)
    {
        await using (SqliteCommand pragma = connection.CreateCommand())
        {
            pragma.CommandText = $"PRAGMA table_info({tableName});";
            await using SqliteDataReader reader = await pragma.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
            while (await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
            {
                if (string.Equals(reader.GetString(1), columnName, StringComparison.OrdinalIgnoreCase))
                {
                    return;
                }
            }
        }

        await using SqliteCommand alter = connection.CreateCommand();
        alter.CommandText = $"ALTER TABLE {tableName} ADD COLUMN {columnName} {columnDefinition};";
        await alter.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
    }

    private static async Task<LocalNetworkHealthStatus?> GetLatestNetworkStatusAsync(
        SqliteConnection connection,
        CancellationToken cancellationToken)
    {
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = """
            SELECT occurred_at, source_id, source_label, succeeded, latency_ms
            FROM local_network_probes
            ORDER BY occurred_at DESC, id DESC
            LIMIT 1;
            """;

        await using SqliteDataReader reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
        if (!await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
        {
            return null;
        }

        return new LocalNetworkHealthStatus(
            ParseTimestamp(reader.GetString(0)),
            reader.IsDBNull(1) ? null : reader.GetString(1),
            reader.IsDBNull(2) ? null : reader.GetString(2),
            reader.GetInt32(3) != 0,
            reader.IsDBNull(4) ? null : reader.GetInt32(4));
    }

    private async Task PruneAsync(
        SqliteConnection connection,
        SqliteTransaction transaction,
        CancellationToken cancellationToken)
    {
        DateTimeOffset now = _timeProvider.GetUtcNow();
        DateTimeOffset usageDetailCutoff = now.Subtract(_options.EffectiveUsageDetailAge);
        await RollUpUsageBeforeAsync(
                connection,
                transaction,
                usageDetailCutoff,
                cancellationToken)
            .ConfigureAwait(false);
        await PruneTableAsync(
                connection,
                transaction,
                "local_usage_events",
                _options.MaxUsageEventCount,
                usageDetailCutoff,
                cancellationToken)
            .ConfigureAwait(false);
        await PruneTableAsync(
                connection,
                transaction,
                "local_network_probes",
                _options.MaxNetworkProbeCount,
                now.Subtract(_options.MaximumAge),
                cancellationToken)
            .ConfigureAwait(false);
        await PruneDailyRollupsAsync(
                connection,
                transaction,
                _options.MaxDailyRollupCount,
                cancellationToken)
            .ConfigureAwait(false);
    }

    private static async Task RollUpUsageBeforeAsync(
        SqliteConnection connection,
        SqliteTransaction transaction,
        DateTimeOffset cutoff,
        CancellationToken cancellationToken)
    {
        await using SqliteCommand command = connection.CreateCommand();
        command.Transaction = transaction;
        command.CommandText = """
            INSERT INTO local_usage_daily_rollups (
                day_utc, request_count, successful_request_count,
                input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens,
                elapsed_total_ms, elapsed_sample_count, estimated_cost)
            SELECT substr(occurred_at, 1, 10),
                   COUNT(*),
                   COALESCE(SUM(CASE WHEN succeeded = 1 THEN 1 ELSE 0 END), 0),
                   COALESCE(SUM(input_tokens), 0),
                   COALESCE(SUM(output_tokens), 0),
                   COALESCE(SUM(cached_input_tokens), 0),
                   COALESCE(SUM(cache_creation_tokens), 0),
                   COALESCE(SUM(elapsed_ms), 0),
                   COUNT(elapsed_ms),
                   SUM(estimated_cost)
            FROM local_usage_events
            WHERE occurred_at < $cutoff
            GROUP BY substr(occurred_at, 1, 10)
            ON CONFLICT(day_utc) DO UPDATE SET
                request_count = local_usage_daily_rollups.request_count + excluded.request_count,
                successful_request_count = local_usage_daily_rollups.successful_request_count + excluded.successful_request_count,
                input_tokens = local_usage_daily_rollups.input_tokens + excluded.input_tokens,
                output_tokens = local_usage_daily_rollups.output_tokens + excluded.output_tokens,
                cached_input_tokens = local_usage_daily_rollups.cached_input_tokens + excluded.cached_input_tokens,
                cache_creation_tokens = local_usage_daily_rollups.cache_creation_tokens + excluded.cache_creation_tokens,
                elapsed_total_ms = local_usage_daily_rollups.elapsed_total_ms + excluded.elapsed_total_ms,
                elapsed_sample_count = local_usage_daily_rollups.elapsed_sample_count + excluded.elapsed_sample_count,
                estimated_cost = CASE
                    WHEN local_usage_daily_rollups.estimated_cost IS NULL THEN excluded.estimated_cost
                    WHEN excluded.estimated_cost IS NULL THEN local_usage_daily_rollups.estimated_cost
                    ELSE local_usage_daily_rollups.estimated_cost + excluded.estimated_cost
                END;
            """;
        command.Parameters.AddWithValue("$cutoff", FormatTimestamp(cutoff));
        await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
    }

    private static async Task PruneDailyRollupsAsync(
        SqliteConnection connection,
        SqliteTransaction transaction,
        int maxCount,
        CancellationToken cancellationToken)
    {
        await using SqliteCommand command = connection.CreateCommand();
        command.Transaction = transaction;
        command.CommandText = """
            DELETE FROM local_usage_daily_rollups
            WHERE day_utc IN (
                SELECT day_utc
                FROM local_usage_daily_rollups
                ORDER BY day_utc DESC
                LIMIT -1 OFFSET $max_count
            );
            """;
        command.Parameters.AddWithValue("$max_count", maxCount);
        await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
    }

    private async Task PruneStoredEventsAsync(CancellationToken cancellationToken)
    {
        await _writeGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await using SqliteConnection connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
            using SqliteTransaction transaction = connection.BeginTransaction();
            await PruneAsync(connection, transaction, cancellationToken).ConfigureAwait(false);
            transaction.Commit();
        }
        finally
        {
            _writeGate.Release();
        }
    }

    private static async Task PruneTableAsync(
        SqliteConnection connection,
        SqliteTransaction transaction,
        string tableName,
        int maxCount,
        DateTimeOffset cutoff,
        CancellationToken cancellationToken)
    {
        string deleteExpiredSql = $"DELETE FROM {tableName} WHERE occurred_at < $cutoff;";
        await using (SqliteCommand deleteExpired = connection.CreateCommand())
        {
            deleteExpired.Transaction = transaction;
            deleteExpired.CommandText = deleteExpiredSql;
            deleteExpired.Parameters.AddWithValue("$cutoff", FormatTimestamp(cutoff));
            await deleteExpired.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
        }

        string deleteOverflowSql = $"""
            DELETE FROM {tableName}
            WHERE id IN (
                SELECT id
                FROM {tableName}
                ORDER BY occurred_at DESC, id DESC
                LIMIT -1 OFFSET $max_count
            );
            """;
        await using SqliteCommand deleteOverflow = connection.CreateCommand();
        deleteOverflow.Transaction = transaction;
        deleteOverflow.CommandText = deleteOverflowSql;
        deleteOverflow.Parameters.AddWithValue("$max_count", maxCount);
        await deleteOverflow.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
    }

    private async Task<SqliteConnection> OpenConnectionAsync(CancellationToken cancellationToken)
    {
        var connection = new SqliteConnection(_connectionString);
        try
        {
            await connection.OpenAsync(cancellationToken).ConfigureAwait(false);
            await using SqliteCommand command = connection.CreateCommand();
            command.CommandText = "PRAGMA busy_timeout = 5000;";
            await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
            return connection;
        }
        catch
        {
            await connection.DisposeAsync().ConfigureAwait(false);
            throw;
        }
    }

    private static DateTimeOffset GetStartOfLocalDayUtc(DateOnly day, TimeZoneInfo timeZone)
    {
        DateTime localMidnight = day.ToDateTime(TimeOnly.MinValue, DateTimeKind.Unspecified);
        TimeSpan offset = timeZone.GetUtcOffset(localMidnight);
        return new DateTimeOffset(localMidnight, offset).ToUniversalTime();
    }

    private static string FormatTimestamp(DateTimeOffset value)
        => value.ToUniversalTime().ToString("O", CultureInfo.InvariantCulture);

    private static DateTimeOffset ParseTimestamp(string value)
        => DateTimeOffset.Parse(value, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind);
}
