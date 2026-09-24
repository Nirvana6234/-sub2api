using System.Text.RegularExpressions;

namespace LanAi.Workspace.Core;

/// <summary>
/// Stores a single aggregate-safe request observation.  This contract purposely
/// has no prompt, response, URL, credential, project-path or native-session
/// fields: local observability must never become a second conversation store.
/// </summary>
public sealed record LocalUsageTelemetryEvent
{
    public LocalUsageTelemetryEvent(
        DateTimeOffset timestamp,
        CliKind cliKind,
        string? sourceId,
        string? sourceLabel,
        string? model,
        long inputTokens,
        long outputTokens,
        long cachedInputTokens,
        bool succeeded,
        int? elapsedMilliseconds,
        long cacheCreationTokens = 0,
        double? estimatedCost = null,
        int? firstTokenMilliseconds = null,
        string? statusCategory = null,
        bool? isStreaming = null,
        string? pricingModel = null)
    {
        if (timestamp == default)
        {
            throw new ArgumentException("A telemetry timestamp is required.", nameof(timestamp));
        }

        if (!Enum.IsDefined(cliKind))
        {
            throw new ArgumentOutOfRangeException(nameof(cliKind));
        }

        ValidateTokenCount(inputTokens, nameof(inputTokens));
        ValidateTokenCount(outputTokens, nameof(outputTokens));
        ValidateTokenCount(cachedInputTokens, nameof(cachedInputTokens));
        ValidateTokenCount(cacheCreationTokens, nameof(cacheCreationTokens));
        ValidateElapsedMilliseconds(elapsedMilliseconds, nameof(elapsedMilliseconds));
        ValidateElapsedMilliseconds(firstTokenMilliseconds, nameof(firstTokenMilliseconds));
        ValidateEstimatedCost(estimatedCost, nameof(estimatedCost));

        Timestamp = timestamp.ToUniversalTime();
        CliKind = cliKind;
        SourceId = LocalTelemetrySafeMetadata.Normalize(sourceId, nameof(sourceId), 160);
        SourceLabel = LocalTelemetrySafeMetadata.Normalize(sourceLabel, nameof(sourceLabel), 160);
        Model = LocalTelemetrySafeMetadata.Normalize(model, nameof(model), 160);
        InputTokens = inputTokens;
        OutputTokens = outputTokens;
        CachedInputTokens = cachedInputTokens;
        CacheCreationTokens = cacheCreationTokens;
        Succeeded = succeeded;
        ElapsedMilliseconds = elapsedMilliseconds;
        EstimatedCost = estimatedCost;
        FirstTokenMilliseconds = firstTokenMilliseconds;
        StatusCategory = LocalTelemetrySafeMetadata.Normalize(statusCategory, nameof(statusCategory), 64);
        IsStreaming = isStreaming;
        PricingModel = LocalTelemetrySafeMetadata.Normalize(pricingModel, nameof(pricingModel), 160);
    }

    public DateTimeOffset Timestamp { get; }

    public CliKind CliKind { get; }

    /// <summary>
    /// An application-generated connection profile identifier, never an endpoint.
    /// </summary>
    public string? SourceId { get; }

    /// <summary>
    /// A display-safe connection name, never an endpoint, secret, or
    /// credential hint.
    /// </summary>
    public string? SourceLabel { get; }

    public string? Model { get; }

    public long InputTokens { get; }

    public long OutputTokens { get; }

    public long CachedInputTokens { get; }

    /// <summary>
    /// Cache-write/create tokens when the active graphical client reports them.
    /// A zero value means that no cache-write amount was observed; it must not
    /// be interpreted as a provider-side guarantee that no cache was written.
    /// </summary>
    public long CacheCreationTokens { get; }

    public bool Succeeded { get; }

    public int? ElapsedMilliseconds { get; }

    /// <summary>
    /// Optional local estimate supplied by the caller.  The workspace does not
    /// invent a price when the active client did not report one.
    /// </summary>
    public double? EstimatedCost { get; }

    public int? FirstTokenMilliseconds { get; }

    public string? StatusCategory { get; }

    public bool? IsStreaming { get; }

    public string? PricingModel { get; }

    private static void ValidateTokenCount(long value, string parameterName)
    {
        if (value < 0)
        {
            throw new ArgumentOutOfRangeException(parameterName, "Token counts cannot be negative.");
        }
    }

    private static void ValidateElapsedMilliseconds(int? value, string parameterName)
    {
        if (value is < 0)
        {
            throw new ArgumentOutOfRangeException(parameterName, "Elapsed milliseconds cannot be negative.");
        }
    }

    private static void ValidateEstimatedCost(double? value, string parameterName)
    {
        if (value is < 0 || value is { } concrete && (double.IsNaN(concrete) || double.IsInfinity(concrete)))
        {
            throw new ArgumentOutOfRangeException(parameterName, "Estimated cost must be a finite non-negative value.");
        }
    }
}

/// <summary>
/// Describes the result of a health probe performed by the caller.  The
/// repository never performs HTTP work and therefore never receives an URL or
/// authorization value.
/// </summary>
public sealed record LocalNetworkHealthProbe
{
    public LocalNetworkHealthProbe(
        DateTimeOffset timestamp,
        string? sourceId,
        string? sourceLabel,
        bool succeeded,
        int? latencyMilliseconds,
        string? statusCategory = null)
    {
        if (timestamp == default)
        {
            throw new ArgumentException("A probe timestamp is required.", nameof(timestamp));
        }

        if (latencyMilliseconds is < 0)
        {
            throw new ArgumentOutOfRangeException(
                nameof(latencyMilliseconds),
                "Latency milliseconds cannot be negative.");
        }

        Timestamp = timestamp.ToUniversalTime();
        SourceId = LocalTelemetrySafeMetadata.Normalize(sourceId, nameof(sourceId), 160);
        SourceLabel = LocalTelemetrySafeMetadata.Normalize(sourceLabel, nameof(sourceLabel), 160);
        Succeeded = succeeded;
        LatencyMilliseconds = latencyMilliseconds;
        StatusCategory = LocalTelemetrySafeMetadata.Normalize(statusCategory, nameof(statusCategory), 64);
    }

    public DateTimeOffset Timestamp { get; }

    public string? SourceId { get; }

    public string? SourceLabel { get; }

    public bool Succeeded { get; }

    public int? LatencyMilliseconds { get; }

    public string? StatusCategory { get; }
}

public sealed record LocalNetworkHealthSummary(
    string? SourceId,
    string? SourceLabel,
    long ProbeCount,
    long SuccessfulProbeCount,
    double? SuccessRatePercent,
    int? P50LatencyMilliseconds,
    int? P95LatencyMilliseconds,
    DateTimeOffset? LastSuccessAt,
    string? LatestStatusCategory);

/// <summary>
/// Aggregate-only totals used by local charts and overview cards.
/// </summary>
public sealed record LocalTelemetryUsageSummary(
    long RequestCount,
    long SuccessfulRequestCount,
    long FailedRequestCount,
    long InputTokens,
    long OutputTokens,
    long CachedInputTokens,
    double? SuccessRatePercent,
    double? AverageLatencyMilliseconds)
{
    public long TotalTokens => InputTokens + OutputTokens;

    /// <summary>
    /// Cache-write/create amount when available from recorded client events.
    /// Existing or unsupported clients leave this at zero.
    /// </summary>
    public long CacheCreationTokens { get; init; }

    /// <summary>
    /// Optional estimated cost.  A null value means no trustworthy local price
    /// was supplied by the client, so the UI should present it as unavailable.
    /// </summary>
    public double? EstimatedCost { get; init; }

    /// <summary>
    /// A best-effort cache hit ratio over locally recorded input observations.
    /// It is intentionally null when no input/cached-input quantities exist.
    /// </summary>
    public double? CacheHitRatePercent => InputTokens + CachedInputTokens == 0
        ? null
        : (CachedInputTokens * 100d) / (InputTokens + CachedInputTokens);

    public static LocalTelemetryUsageSummary Empty { get; } =
        new(0, 0, 0, 0, 0, 0, null, null);
}

/// <summary>
/// One local calendar day in the seven-day usage series.  The date is expressed
/// in the time zone requested from <see cref="ILocalTelemetryRepository"/>.
/// </summary>
public sealed record LocalTelemetryDailyUsage(
    DateOnly Date,
    LocalTelemetryUsageSummary Usage);

/// <summary>
/// One UTC-hour bucket for the most recent 24 local telemetry hours.  The
/// timestamp is displayed in the caller's local time zone by the UI, while the
/// UTC storage boundary keeps daylight-saving transitions unambiguous.
/// </summary>
public sealed record LocalTelemetryHourlyUsage(
    DateTimeOffset HourStart,
    LocalTelemetryUsageSummary Usage);

/// <summary>
/// The most recent probe result available to the local client.  A null value in
/// a snapshot means that no caller has recorded a health probe yet.
/// </summary>
public sealed record LocalNetworkHealthStatus(
    DateTimeOffset CheckedAt,
    string? SourceId,
    string? SourceLabel,
    bool Succeeded,
    int? LatencyMilliseconds);

/// <summary>
/// One privacy-safe aggregation slice.  Depending on the collection that
/// contains it, exactly one of source, CLI, or model is normally populated.
/// The repository deliberately uses this generic shape so consumers cannot
/// infer a project path, prompt, URL, credential, or conversation identifier.
/// </summary>
public sealed record LocalTelemetryUsageBreakdown(
    string? SourceId,
    string? SourceLabel,
    CliKind? CliKind,
    string? Model,
    LocalTelemetryUsageSummary Usage);

/// <summary>
/// A compact, recent activity row for the local dashboard.  It intentionally
/// contains only the same bounded metadata and numeric observations accepted
/// by <see cref="LocalUsageTelemetryEvent"/>; it is not a transcript or a
/// request audit log.
/// </summary>
public sealed record LocalTelemetryRecentActivity(
    DateTimeOffset OccurredAt,
    CliKind CliKind,
    string? SourceId,
    string? SourceLabel,
    string? Model,
    bool Succeeded,
    long InputTokens,
    long OutputTokens,
    long CachedInputTokens,
    int? ElapsedMilliseconds,
    long CacheCreationTokens = 0,
    double? EstimatedCost = null,
    int? FirstTokenMilliseconds = null,
    string? StatusCategory = null,
    bool? IsStreaming = null,
    string? PricingModel = null)
{
    public long TotalTokens => InputTokens + OutputTokens;
}

public sealed record LocalTelemetryQueryFilter(
    string? SourceId = null,
    CliKind? CliKind = null,
    string? Model = null)
{
    public bool IsEmpty => string.IsNullOrWhiteSpace(SourceId) && CliKind is null && string.IsNullOrWhiteSpace(Model);
}

/// <summary>
/// Local, privacy-bounded view of usage and connectivity.  It contains only
/// aggregate counters, recent aggregate-safe observations, and the latest
/// caller-supplied health observation.  It never exposes prompt text, reply
/// text, API keys, passwords, full URLs, project paths, or native session IDs.
/// </summary>
public sealed record LocalTelemetrySnapshot
{
    /// <summary>
    /// Preserves the original compact constructor for callers that only need
    /// totals and network health.  Newer clients receive richer breakdowns
    /// from repository implementations through the overload below.
    /// </summary>
    public LocalTelemetrySnapshot(
        DateTimeOffset GeneratedAt,
        LocalTelemetryUsageSummary Today,
        LocalTelemetryUsageSummary LastSevenDays,
        IReadOnlyList<LocalTelemetryDailyUsage> LastSevenDaysDailyUsage,
        LocalNetworkHealthStatus? LatestNetworkStatus)
        : this(
            GeneratedAt,
            Today,
            LastSevenDays,
            LastSevenDaysDailyUsage,
            LatestNetworkStatus,
            Array.Empty<LocalTelemetryUsageBreakdown>(),
            Array.Empty<LocalTelemetryUsageBreakdown>(),
            Array.Empty<LocalTelemetryUsageBreakdown>(),
            Array.Empty<LocalTelemetryRecentActivity>(),
            Array.Empty<LocalTelemetryHourlyUsage>())
    {
    }

    public LocalTelemetrySnapshot(
        DateTimeOffset GeneratedAt,
        LocalTelemetryUsageSummary Today,
        LocalTelemetryUsageSummary LastSevenDays,
        IReadOnlyList<LocalTelemetryDailyUsage> LastSevenDaysDailyUsage,
        LocalNetworkHealthStatus? LatestNetworkStatus,
        IReadOnlyList<LocalTelemetryUsageBreakdown> LastSevenDaysBySource,
        IReadOnlyList<LocalTelemetryUsageBreakdown> LastSevenDaysByCli,
        IReadOnlyList<LocalTelemetryUsageBreakdown> LastSevenDaysByModel,
        IReadOnlyList<LocalTelemetryRecentActivity> RecentActivity)
        : this(
            GeneratedAt,
            Today,
            LastSevenDays,
            LastSevenDaysDailyUsage,
            LatestNetworkStatus,
            LastSevenDaysBySource,
            LastSevenDaysByCli,
            LastSevenDaysByModel,
            RecentActivity,
            Array.Empty<LocalTelemetryHourlyUsage>())
    {
    }

    public LocalTelemetrySnapshot(
        DateTimeOffset GeneratedAt,
        LocalTelemetryUsageSummary Today,
        LocalTelemetryUsageSummary LastSevenDays,
        IReadOnlyList<LocalTelemetryDailyUsage> LastSevenDaysDailyUsage,
        LocalNetworkHealthStatus? LatestNetworkStatus,
        IReadOnlyList<LocalTelemetryUsageBreakdown> LastSevenDaysBySource,
        IReadOnlyList<LocalTelemetryUsageBreakdown> LastSevenDaysByCli,
        IReadOnlyList<LocalTelemetryUsageBreakdown> LastSevenDaysByModel,
        IReadOnlyList<LocalTelemetryRecentActivity> RecentActivity,
        IReadOnlyList<LocalTelemetryHourlyUsage> LastTwentyFourHoursHourlyUsage)
    {
        this.GeneratedAt = GeneratedAt;
        this.Today = Today ?? throw new ArgumentNullException(nameof(Today));
        this.LastSevenDays = LastSevenDays ?? throw new ArgumentNullException(nameof(LastSevenDays));
        this.LastSevenDaysDailyUsage = LastSevenDaysDailyUsage ?? throw new ArgumentNullException(nameof(LastSevenDaysDailyUsage));
        this.LatestNetworkStatus = LatestNetworkStatus;
        this.LastSevenDaysBySource = LastSevenDaysBySource ?? throw new ArgumentNullException(nameof(LastSevenDaysBySource));
        this.LastSevenDaysByCli = LastSevenDaysByCli ?? throw new ArgumentNullException(nameof(LastSevenDaysByCli));
        this.LastSevenDaysByModel = LastSevenDaysByModel ?? throw new ArgumentNullException(nameof(LastSevenDaysByModel));
        this.RecentActivity = RecentActivity ?? throw new ArgumentNullException(nameof(RecentActivity));
        this.LastTwentyFourHoursHourlyUsage = LastTwentyFourHoursHourlyUsage ?? throw new ArgumentNullException(nameof(LastTwentyFourHoursHourlyUsage));
    }

    public DateTimeOffset GeneratedAt { get; }

    public LocalTelemetryUsageSummary Today { get; }

    public LocalTelemetryUsageSummary LastSevenDays { get; }

    public IReadOnlyList<LocalTelemetryDailyUsage> LastSevenDaysDailyUsage { get; }

    public LocalNetworkHealthStatus? LatestNetworkStatus { get; }

    /// <summary>Seven-day aggregation grouped by connection source.</summary>
    public IReadOnlyList<LocalTelemetryUsageBreakdown> LastSevenDaysBySource { get; }

    /// <summary>Seven-day aggregation grouped by graphical CLI surface.</summary>
    public IReadOnlyList<LocalTelemetryUsageBreakdown> LastSevenDaysByCli { get; }

    /// <summary>Seven-day aggregation grouped by reported model label.</summary>
    public IReadOnlyList<LocalTelemetryUsageBreakdown> LastSevenDaysByModel { get; }

    /// <summary>Most recent bounded local observations, newest first.</summary>
    public IReadOnlyList<LocalTelemetryRecentActivity> RecentActivity { get; }

    /// <summary>A contiguous 24-hour UTC-bucketed series, displayed locally.</summary>
    public IReadOnlyList<LocalTelemetryHourlyUsage> LastTwentyFourHoursHourlyUsage { get; }
}

/// <summary>
/// A privacy-bounded local usage dashboard for an explicit calendar-day range.
/// The range is inclusive of today and has a maximum of thirty days so the
/// desktop dashboard remains quick without turning local telemetry into an
/// audit log.
/// </summary>
public sealed record LocalTelemetryRangeSnapshot
{
    public LocalTelemetryRangeSnapshot(
        DateTimeOffset generatedAt,
        int days,
        LocalTelemetryUsageSummary usage,
        IReadOnlyList<LocalTelemetryDailyUsage> dailyUsage,
        LocalNetworkHealthStatus? latestNetworkStatus,
        IReadOnlyList<LocalTelemetryUsageBreakdown> bySource,
        IReadOnlyList<LocalTelemetryUsageBreakdown> byCli,
        IReadOnlyList<LocalTelemetryUsageBreakdown> byModel,
        IReadOnlyList<LocalTelemetryRecentActivity> recentActivity,
        IReadOnlyList<LocalTelemetryHourlyUsage> recentHourlyUsage)
    {
        if (days is < 1 or > 30)
        {
            throw new ArgumentOutOfRangeException(nameof(days), "The local dashboard range must be from 1 to 30 days.");
        }

        GeneratedAt = generatedAt;
        Days = days;
        Usage = usage ?? throw new ArgumentNullException(nameof(usage));
        DailyUsage = dailyUsage ?? throw new ArgumentNullException(nameof(dailyUsage));
        LatestNetworkStatus = latestNetworkStatus;
        BySource = bySource ?? throw new ArgumentNullException(nameof(bySource));
        ByCli = byCli ?? throw new ArgumentNullException(nameof(byCli));
        ByModel = byModel ?? throw new ArgumentNullException(nameof(byModel));
        RecentActivity = recentActivity ?? throw new ArgumentNullException(nameof(recentActivity));
        RecentHourlyUsage = recentHourlyUsage ?? throw new ArgumentNullException(nameof(recentHourlyUsage));
    }

    public DateTimeOffset GeneratedAt { get; }

    /// <summary>Inclusive calendar-day range ending on the current local day.</summary>
    public int Days { get; }

    public LocalTelemetryUsageSummary Usage { get; }

    /// <summary>A contiguous local-calendar-day series for <see cref="Days"/>.</summary>
    public IReadOnlyList<LocalTelemetryDailyUsage> DailyUsage { get; }

    public LocalNetworkHealthStatus? LatestNetworkStatus { get; }

    public IReadOnlyList<LocalTelemetryUsageBreakdown> BySource { get; }

    public IReadOnlyList<LocalTelemetryUsageBreakdown> ByCli { get; }

    public IReadOnlyList<LocalTelemetryUsageBreakdown> ByModel { get; }

    public IReadOnlyList<LocalTelemetryRecentActivity> RecentActivity { get; }

    /// <summary>Recent hourly detail for the selected day's dashboard drill-down.</summary>
    public IReadOnlyList<LocalTelemetryHourlyUsage> RecentHourlyUsage { get; }

    public static LocalTelemetryRangeSnapshot FromLegacy(LocalTelemetrySnapshot snapshot, int days)
    {
        ArgumentNullException.ThrowIfNull(snapshot);
        int normalizedDays = Math.Clamp(days, 1, 30);
        bool todayOnly = normalizedDays == 1;
        IReadOnlyList<LocalTelemetryDailyUsage> daily = todayOnly
            ? snapshot.LastSevenDaysDailyUsage.TakeLast(1).ToArray()
            : snapshot.LastSevenDaysDailyUsage;
        LocalTelemetryUsageSummary usage = todayOnly ? snapshot.Today : snapshot.LastSevenDays;
        return new LocalTelemetryRangeSnapshot(
            snapshot.GeneratedAt,
            normalizedDays,
            usage,
            daily,
            snapshot.LatestNetworkStatus,
            snapshot.LastSevenDaysBySource,
            snapshot.LastSevenDaysByCli,
            snapshot.LastSevenDaysByModel,
            snapshot.RecentActivity,
            snapshot.LastTwentyFourHoursHourlyUsage);
    }
}

/// <summary>
/// Bounds local telemetry storage by both event count and retention age.  These
/// limits are deliberately modest because this data exists only to power local
/// status and usage summaries, not an audit log.
/// </summary>
public sealed record LocalTelemetryStorageOptions
{
    public int MaxUsageEventCount { get; init; } = 10_000;

    public int MaxNetworkProbeCount { get; init; } = 2_000;

    public TimeSpan MaximumAge { get; init; } = TimeSpan.FromDays(90);

    /// <summary>
    /// How long individual request observations remain available.  Older rows
    /// are converted into aggregate-only UTC daily totals before deletion.  A
    /// null value uses the shorter of thirty days and <see cref="MaximumAge"/>,
    /// preserving the historical MaximumAge behavior for callers that set a
    /// shorter retention window.
    /// </summary>
    public TimeSpan? UsageDetailAge { get; init; }

    /// <summary>
    /// Maximum number of aggregate UTC days retained after request detail is
    /// compacted.  Ten years keeps long-term trends useful while remaining
    /// strictly bounded.
    /// </summary>
    public int MaxDailyRollupCount { get; init; } = 3_650;

    public TimeSpan EffectiveUsageDetailAge
        => UsageDetailAge ?? (MaximumAge < TimeSpan.FromDays(30) ? MaximumAge : TimeSpan.FromDays(30));

    /// <summary>
    /// Validates that the retention bounds are usable before a repository is
    /// created.  Applications may call this when binding configurable limits.
    /// </summary>
    public void Validate()
    {
        if (MaxUsageEventCount < 1)
        {
            throw new ArgumentOutOfRangeException(
                nameof(MaxUsageEventCount),
                "At least one usage event must be retained.");
        }

        if (MaxNetworkProbeCount < 1)
        {
            throw new ArgumentOutOfRangeException(
                nameof(MaxNetworkProbeCount),
                "At least one network probe must be retained.");
        }

        if (MaximumAge <= TimeSpan.Zero)
        {
            throw new ArgumentOutOfRangeException(
                nameof(MaximumAge),
                "The maximum telemetry age must be positive.");
        }

        if (UsageDetailAge is { } usageDetailAge && usageDetailAge <= TimeSpan.Zero)
        {
            throw new ArgumentOutOfRangeException(
                nameof(UsageDetailAge),
                "The usage-detail age must be positive when specified.");
        }

        if (MaxDailyRollupCount < 1)
        {
            throw new ArgumentOutOfRangeException(
                nameof(MaxDailyRollupCount),
                "At least one daily usage rollup must be retained.");
        }
    }
}

/// <summary>
/// Persists only aggregate-safe local request and network observations.
/// Implementations must not accept or reconstruct prompts, response bodies,
/// URLs, API keys, passwords, or other credentials.
/// </summary>
public interface ILocalTelemetryRepository
{
    Task RecordUsageAsync(
        LocalUsageTelemetryEvent telemetryEvent,
        CancellationToken cancellationToken = default);

    Task RecordNetworkProbeAsync(
        LocalNetworkHealthProbe probe,
        CancellationToken cancellationToken = default);

    /// <summary>
    /// Returns today, a seven-day aggregate and a contiguous seven-day daily
    /// series for the supplied local time zone.  When omitted, the computer's
    /// current local time zone is used.
    /// </summary>
    Task<LocalTelemetrySnapshot> GetSnapshotAsync(
        TimeZoneInfo? timeZone = null,
        CancellationToken cancellationToken = default);

    /// <summary>
    /// Returns a dashboard snapshot for today, the last seven days, or the last
    /// thirty days.  Existing repository implementations remain compatible via
    /// the seven-day snapshot fallback; the SQLite implementation overrides it
    /// with a true range query.
    /// </summary>
    async Task<LocalTelemetryRangeSnapshot> GetRangeSnapshotAsync(
        int days,
        TimeZoneInfo? timeZone = null,
        CancellationToken cancellationToken = default)
    {
        LocalTelemetrySnapshot snapshot = await GetSnapshotAsync(timeZone, cancellationToken).ConfigureAwait(false);
        return LocalTelemetryRangeSnapshot.FromLegacy(snapshot, days);
    }

    Task<LocalTelemetryRangeSnapshot> GetFilteredRangeSnapshotAsync(
        int days,
        LocalTelemetryQueryFilter filter,
        TimeZoneInfo? timeZone = null,
        CancellationToken cancellationToken = default)
        => filter.IsEmpty
            ? GetRangeSnapshotAsync(days, timeZone, cancellationToken)
            : GetRangeSnapshotAsync(days, timeZone, cancellationToken);

    /// <summary>
    /// Removes the legacy official-CLI history rows that predate source-aware
    /// telemetry.  Those rows were derived from cumulative JSONL snapshots,
    /// not verified per-request measurements, and therefore must never be
    /// presented as a recent local-usage dashboard.  Implementations that do
    /// not persist legacy rows can safely use the no-op default.
    /// </summary>
    Task<int> RemoveLegacyHistoryImportEventsAsync(
        CancellationToken cancellationToken = default)
        => Task.FromResult(0);

    Task<IReadOnlyList<LocalNetworkHealthSummary>> GetNetworkHealthSummariesAsync(
        DateTimeOffset sinceUtc,
        CancellationToken cancellationToken = default)
        => Task.FromResult<IReadOnlyList<LocalNetworkHealthSummary>>(Array.Empty<LocalNetworkHealthSummary>());

    /// <summary>
    /// Returns the most recent aggregate-safe probe points for one source in
    /// chronological order. Implementations must not expose the probed URL or
    /// any credential-bearing request data.
    /// </summary>
    Task<IReadOnlyList<LocalNetworkHealthProbe>> GetRecentNetworkProbesAsync(
        string sourceId,
        int limit = 60,
        CancellationToken cancellationToken = default)
        => Task.FromResult<IReadOnlyList<LocalNetworkHealthProbe>>(Array.Empty<LocalNetworkHealthProbe>());
}

internal static class LocalTelemetrySafeMetadata
{
    private static readonly Regex CredentialAssignmentPattern = new(
        @"\b(?:authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret)\b\s*[:=]",
        RegexOptions.CultureInvariant | RegexOptions.IgnoreCase,
        TimeSpan.FromMilliseconds(50));

    private static readonly Regex BearerOrKeyPattern = new(
        @"(?:\bbearer\s+\S+|\bsk-(?:ant-)?[a-z0-9_-]{8,}\b|\bAIza[a-z0-9_-]{20,}\b)",
        RegexOptions.CultureInvariant | RegexOptions.IgnoreCase,
        TimeSpan.FromMilliseconds(50));

    public static string? Normalize(string? value, string parameterName, int maximumLength)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return null;
        }

        string normalized = value.Trim();
        if (normalized.Length > maximumLength)
        {
            throw new ArgumentOutOfRangeException(
                parameterName,
                $"Telemetry metadata must be at most {maximumLength} characters.");
        }

        if (normalized.Any(char.IsControl))
        {
            throw new ArgumentException("Telemetry metadata cannot contain control characters.", parameterName);
        }

        if (ContainsAbsoluteUrl(normalized) ||
            CredentialAssignmentPattern.IsMatch(normalized) ||
            BearerOrKeyPattern.IsMatch(normalized))
        {
            throw new ArgumentException(
                "Telemetry metadata must not contain a URL or credential.",
                parameterName);
        }

        return normalized;
    }

    private static bool ContainsAbsoluteUrl(string value)
    {
        if (!Uri.TryCreate(value, UriKind.Absolute, out _))
        {
            return false;
        }

        return true;
    }
}
