using System.Net.Http;
using System.Net.Http.Json;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace AiSwitchGui;

// 面向用户的流量统计服务：用账号密码登录 Sub2API，拉取本账号的用量数据。
// 数据来源（JWT 认证）：
//   POST /api/v1/auth/login                  -> access_token
//   GET  /api/v1/usage/dashboard/stats       -> 总览
//   GET  /api/v1/usage/dashboard/models      -> 按模型
//   GET  /api/v1/usage/stats                 -> 指定日期范围的总览/时延
//   GET  /api/v1/usage/dashboard/models      -> 指定日期范围的模型账本
//   GET  /api/v1/usage/dashboard/trend       -> 指定日期范围的趋势
//   GET  /api/v1/usage                       -> 指定日期范围的近期活动（最小化 DTO）
internal sealed class StatsService
{
    // Keep the server and desktop page sizes identical.  Additional pages are
    // requested on demand instead of silently truncating activity at 1,000 rows.
    internal const int RecentActivityPageSize = 25;

    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
        NumberHandling = JsonNumberHandling.AllowReadingFromString,
    };

    private readonly HttpClient _httpClient;
    private readonly Func<DateTimeOffset> _utcNow;
    private readonly Func<string> _timeZoneResolver;
    private readonly SemaphoreSlim _loginGate = new(1, 1);
    private StatsSettings _settings;
    private string? _accessToken;

    public StatsService(StatsSettings settings)
        : this(settings, new HttpClient { Timeout = TimeSpan.FromSeconds(15) })
    {
    }

    /// <summary>
    /// Test-friendly constructor.  The caller retains ownership of
    /// <paramref name="httpClient"/>.
    /// </summary>
    internal StatsService(
        StatsSettings settings,
        HttpClient httpClient,
        Func<DateTimeOffset>? utcNow = null,
        Func<string>? timeZoneResolver = null)
    {
        _settings = settings ?? throw new ArgumentNullException(nameof(settings));
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        _utcNow = utcNow ?? (() => DateTimeOffset.UtcNow);
        _timeZoneResolver = timeZoneResolver ?? ResolveIanaTimeZone;
    }

    public void UpdateSettings(StatsSettings settings)
    {
        _settings = settings;
        _accessToken = null;
    }

    private string BaseUrl => _settings.GatewayBaseUrl.TrimEnd('/');

    public async Task<string> LoginAsync(CancellationToken cancellationToken)
    {
        await _loginGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            return await LoginCoreAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            _loginGate.Release();
        }
    }

    private async Task<string> LoginCoreAsync(CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(_settings.Email) || string.IsNullOrWhiteSpace(_settings.Password))
        {
            throw new InvalidOperationException("请先填写账号邮箱和密码。");
        }

        var payload = new { email = _settings.Email, password = _settings.Password };
        using var content = new StringContent(JsonSerializer.Serialize(payload), Encoding.UTF8, "application/json");
        using var response = await _httpClient.PostAsync($"{BaseUrl}/api/v1/auth/login", content, cancellationToken);
        var body = await response.Content.ReadAsStringAsync(cancellationToken);
        if (!response.IsSuccessStatusCode)
        {
            throw new InvalidOperationException($"登录失败 HTTP {(int)response.StatusCode}：{Truncate(body)}");
        }

        var env = JsonSerializer.Deserialize<Envelope<LoginData>>(body, JsonOptions);
        if (env?.Code != 0 || env.Data is null || string.IsNullOrWhiteSpace(env.Data.AccessToken))
        {
            throw new InvalidOperationException($"登录失败：{env?.Message ?? "返回内容异常"}");
        }

        _accessToken = env.Data.AccessToken;
        return _accessToken;
    }

    public Task<StatsOverview> GetOverviewAsync(CancellationToken cancellationToken)
        => GetAsync<StatsOverview>("/api/v1/usage/dashboard/stats", cancellationToken);

    public Task<UsageRangeOverview> GetRangeOverviewAsync(CancellationToken cancellationToken)
        => GetRangeOverviewAsync(CreateDashboardRange(), cancellationToken);

    internal Task<UsageRangeOverview> GetRangeOverviewAsync(
        CloudUsageDateRange range,
        CancellationToken cancellationToken)
        => GetAsync<UsageRangeOverview>(
            $"/api/v1/usage/stats?{range.ToQueryString()}",
            cancellationToken);

    public async Task<IReadOnlyList<ModelStat>> GetModelsAsync(CancellationToken cancellationToken)
        => await GetModelsAsync(CreateDashboardRange(), cancellationToken).ConfigureAwait(false);

    internal async Task<IReadOnlyList<ModelStat>> GetModelsAsync(
        CloudUsageDateRange range,
        CancellationToken cancellationToken)
    {
        var data = await GetAsync<ModelsData>(
            $"/api/v1/usage/dashboard/models?{range.ToQueryString()}",
            cancellationToken);
        return data.Models ?? new List<ModelStat>();
    }

    public async Task<IReadOnlyList<TrendPoint>> GetTrendAsync(CancellationToken cancellationToken)
        => await GetTrendAsync(CreateDashboardRange(), cancellationToken).ConfigureAwait(false);

    internal async Task<IReadOnlyList<TrendPoint>> GetTrendAsync(
        CloudUsageDateRange range,
        CancellationToken cancellationToken)
    {
        var data = await GetAsync<TrendData>(
            $"/api/v1/usage/dashboard/trend?{range.ToQueryString(granularity: range.Days == 1 ? "hour" : "day")}",
            cancellationToken);
        return data.Trend ?? new List<TrendPoint>();
    }

    /// <summary>
    /// Reads the latest account activity inside the same selected range as the
    /// metrics, model ledger, and trend.  The DTO intentionally excludes API
    /// key material, request IDs, endpoint URLs, IP addresses, and user-agent
    /// values returned by the raw Sub2API usage-log endpoint.
    /// </summary>
    public Task<CloudUsageActivityPage> GetActivityAsync(CancellationToken cancellationToken)
        => GetActivityAsync(CreateDashboardRange(), cancellationToken);

    internal Task<CloudUsageActivityPage> GetActivityAsync(
        CloudUsageDateRange range,
        CancellationToken cancellationToken)
        => GetActivityAsync(range, page: 1, RecentActivityPageSize, cancellationToken);

    internal Task<CloudUsageActivityPage> GetActivityAsync(
        CloudUsageDateRange range,
        int page,
        int pageSize,
        CancellationToken cancellationToken)
        => GetAsync<CloudUsageActivityPage>(
            $"/api/v1/usage?{range.ToQueryString(includeGranularity: false)}" +
            $"&page={Math.Max(1, page)}&page_size={Math.Clamp(pageSize, 1, 100)}&sort_by=created_at&sort_order=desc",
            cancellationToken);

    /// <summary>
    /// Captures one consistent dashboard range.  Calculating the range once is
    /// important around midnight: every panel must describe the same calendar
    /// window rather than independently drifting between two days.
    /// </summary>
    public async Task<CloudDashboardSnapshot> GetDashboardSnapshotAsync(CancellationToken cancellationToken)
    {
        await EnsureAccessTokenAsync(cancellationToken).ConfigureAwait(false);
        CloudUsageDateRange range = CreateDashboardRange();
        Task<UsageRangeOverview> metricsTask = GetRangeOverviewAsync(range, cancellationToken);
        Task<IReadOnlyList<ModelStat>> modelsTask = GetModelsAsync(range, cancellationToken);
        Task<IReadOnlyList<TrendPoint>> trendTask = GetTrendAsync(range, cancellationToken);
        await Task.WhenAll(metricsTask, modelsTask, trendTask).ConfigureAwait(false);

        return new CloudDashboardSnapshot(
            range,
            await metricsTask.ConfigureAwait(false),
            await modelsTask.ConfigureAwait(false),
            await trendTask.ConfigureAwait(false));
    }

    internal static string BuildDashboardRangeQuery(int days, string? timeZone = null)
    {
        string zone = ResolveTimeZone(timeZone);
        return CreateDashboardDateRange(days, GetCurrentDateInTimeZone(zone), zone).ToQueryString();
    }

    internal static string BuildDashboardRangeQuery(int days, DateOnly endDate, string? timeZone = null)
        => CreateDashboardDateRange(days, endDate, timeZone).ToQueryString();

    internal static CloudUsageDateRange CreateDashboardDateRange(
        int days,
        DateOnly endDate,
        string? timeZone = null)
    {
        int normalizedDays = NormalizeDashboardDays(days);
        return new CloudUsageDateRange(
            endDate.AddDays(-(normalizedDays - 1)),
            endDate,
            ResolveTimeZone(timeZone));
    }

    internal static string ResolveIanaTimeZone()
    {
        string id = TimeZoneInfo.Local.Id;
        if (id.Contains('/', StringComparison.Ordinal))
        {
            return id;
        }

        return id switch
        {
            "China Standard Time" => "Asia/Shanghai",
            "Taipei Standard Time" => "Asia/Taipei",
            "Tokyo Standard Time" => "Asia/Tokyo",
            "Korea Standard Time" => "Asia/Seoul",
            "Singapore Standard Time" => "Asia/Singapore",
            "India Standard Time" => "Asia/Kolkata",
            "UTC" => "UTC",
            _ => "UTC",
        };
    }

    private CloudUsageDateRange CreateDashboardRange()
    {
        string zone = ResolveTimeZone(_timeZoneResolver());
        DateOnly endDate = GetDateInTimeZone(_utcNow(), zone);
        return CreateDashboardDateRange(_settings.TrendDays, endDate, zone);
    }

    private static int NormalizeDashboardDays(int days) => days is 1 or 7 or 30 ? days : 7;

    private static string ResolveTimeZone(string? timeZone)
        => string.IsNullOrWhiteSpace(timeZone) ? ResolveIanaTimeZone() : timeZone.Trim();

    private static DateOnly GetCurrentDateInTimeZone(string timeZone)
        => GetDateInTimeZone(DateTimeOffset.UtcNow, timeZone);

    private static DateOnly GetDateInTimeZone(DateTimeOffset instant, string timeZone)
    {
        TimeZoneInfo zone = ResolveTimeZoneInfo(timeZone);
        return DateOnly.FromDateTime(TimeZoneInfo.ConvertTime(instant, zone).DateTime);
    }

    private static TimeZoneInfo ResolveTimeZoneInfo(string timeZone)
    {
        try
        {
            return TimeZoneInfo.FindSystemTimeZoneById(timeZone);
        }
        catch (TimeZoneNotFoundException)
        {
            // .NET 8 normally accepts IANA IDs on Windows.  Keep these
            // explicit mappings for systems where that compatibility layer is
            // unavailable, so the query date still matches its IANA timezone.
            string? windowsId = timeZone switch
            {
                "Asia/Shanghai" => "China Standard Time",
                "Asia/Taipei" => "Taipei Standard Time",
                "Asia/Tokyo" => "Tokyo Standard Time",
                "Asia/Seoul" => "Korea Standard Time",
                "Asia/Singapore" => "Singapore Standard Time",
                "Asia/Kolkata" => "India Standard Time",
                _ => null,
            };
            if (!string.IsNullOrWhiteSpace(windowsId))
            {
                try
                {
                    return TimeZoneInfo.FindSystemTimeZoneById(windowsId);
                }
                catch (TimeZoneNotFoundException)
                {
                    // Use UTC below if the host lacks the fallback zone too.
                }
                catch (InvalidTimeZoneException)
                {
                    // A corrupt local zone registry must not prevent stats.
                }
            }
        }
        catch (InvalidTimeZoneException)
        {
            // A corrupt local zone registry must not prevent stats.
        }

        return TimeZoneInfo.Utc;
    }

    private async Task EnsureAccessTokenAsync(CancellationToken cancellationToken)
    {
        if (!string.IsNullOrWhiteSpace(_accessToken))
        {
            return;
        }

        await _loginGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (string.IsNullOrWhiteSpace(_accessToken))
            {
                await LoginCoreAsync(cancellationToken).ConfigureAwait(false);
            }
        }
        finally
        {
            _loginGate.Release();
        }
    }

    private async Task<T> GetAsync<T>(string path, CancellationToken cancellationToken)
    {
        await EnsureAccessTokenAsync(cancellationToken).ConfigureAwait(false);

        var result = await SendAuthorizedAsync(path, cancellationToken);
        if (result.StatusCode == System.Net.HttpStatusCode.Unauthorized)
        {
            // token 过期，重新登录后重试一次。
            await LoginAsync(cancellationToken);
            result = await SendAuthorizedAsync(path, cancellationToken);
        }

        if (!result.Success)
        {
            throw new InvalidOperationException($"读取统计失败 HTTP {(int)result.StatusCode}：{Truncate(result.Body)}");
        }

        var env = JsonSerializer.Deserialize<Envelope<T>>(result.Body, JsonOptions);
        if (env?.Code != 0 || env.Data is null)
        {
            throw new InvalidOperationException($"读取统计失败：{env?.Message ?? "返回内容异常"}");
        }

        return env.Data;
    }

    private async Task<(bool Success, System.Net.HttpStatusCode StatusCode, string Body)> SendAuthorizedAsync(
        string path,
        CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, $"{BaseUrl}{path}");
        request.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", _accessToken);
        using var response = await _httpClient.SendAsync(request, cancellationToken);
        var body = await response.Content.ReadAsStringAsync(cancellationToken);
        return (response.IsSuccessStatusCode, response.StatusCode, body);
    }

    private static string Truncate(string? value)
    {
        if (string.IsNullOrEmpty(value)) return string.Empty;
        return value.Length <= 200 ? value : value[..200];
    }

    private sealed class Envelope<T>
    {
        [JsonPropertyName("code")] public int Code { get; set; }
        [JsonPropertyName("message")] public string? Message { get; set; }
        [JsonPropertyName("data")] public T? Data { get; set; }
    }

    private sealed class LoginData
    {
        [JsonPropertyName("access_token")] public string? AccessToken { get; set; }
    }

    private sealed class ModelsData
    {
        [JsonPropertyName("models")] public List<ModelStat>? Models { get; set; }
    }

    private sealed class TrendData
    {
        [JsonPropertyName("trend")] public List<TrendPoint>? Trend { get; set; }
    }
}

internal sealed class StatsOverview
{
    [JsonPropertyName("total_api_keys")] public int TotalApiKeys { get; set; }
    [JsonPropertyName("active_api_keys")] public int ActiveApiKeys { get; set; }
    [JsonPropertyName("total_requests")] public long TotalRequests { get; set; }
    [JsonPropertyName("total_input_tokens")] public long TotalInputTokens { get; set; }
    [JsonPropertyName("total_output_tokens")] public long TotalOutputTokens { get; set; }
    [JsonPropertyName("total_cache_read_tokens")] public long TotalCacheReadTokens { get; set; }
    [JsonPropertyName("total_cache_creation_tokens")] public long TotalCacheCreationTokens { get; set; }
    [JsonPropertyName("total_tokens")] public long TotalTokens { get; set; }
    [JsonPropertyName("total_cost")] public double TotalCost { get; set; }
    [JsonPropertyName("total_actual_cost")] public double TotalActualCost { get; set; }
    [JsonPropertyName("today_requests")] public long TodayRequests { get; set; }
    [JsonPropertyName("today_input_tokens")] public long TodayInputTokens { get; set; }
    [JsonPropertyName("today_output_tokens")] public long TodayOutputTokens { get; set; }
    [JsonPropertyName("today_cache_read_tokens")] public long TodayCacheReadTokens { get; set; }
    [JsonPropertyName("today_tokens")] public long TodayTokens { get; set; }
    [JsonPropertyName("today_cost")] public double TodayCost { get; set; }
    [JsonPropertyName("today_actual_cost")] public double TodayActualCost { get; set; }
    [JsonPropertyName("average_duration_ms")] public double AverageDurationMs { get; set; }
    [JsonPropertyName("rpm")] public double Rpm { get; set; }
    [JsonPropertyName("tpm")] public double Tpm { get; set; }
}

/// <summary>
/// A caller-selected time-range aggregate from /usage/stats.  It intentionally
/// contains no account, endpoint, request body, or credential details.
/// </summary>
internal sealed class UsageRangeOverview
{
    [JsonPropertyName("total_requests")] public long TotalRequests { get; set; }
    [JsonPropertyName("total_input_tokens")] public long TotalInputTokens { get; set; }
    [JsonPropertyName("total_output_tokens")] public long TotalOutputTokens { get; set; }
    [JsonPropertyName("total_cache_tokens")] public long TotalCacheTokens { get; set; }
    [JsonPropertyName("total_cache_read_tokens")] public long TotalCacheReadTokens { get; set; }
    [JsonPropertyName("total_cache_creation_tokens")] public long TotalCacheCreationTokens { get; set; }
    [JsonPropertyName("total_tokens")] public long TotalTokens { get; set; }
    [JsonPropertyName("total_cost")] public double TotalCost { get; set; }
    [JsonPropertyName("total_actual_cost")] public double TotalActualCost { get; set; }
    [JsonPropertyName("average_duration_ms")] public double AverageDurationMs { get; set; }

    public static UsageRangeOverview FromTrend(IEnumerable<TrendPoint> trend)
    {
        ArgumentNullException.ThrowIfNull(trend);
        TrendPoint[] points = trend.ToArray();
        return new UsageRangeOverview
        {
            TotalRequests = points.Sum(point => point.Requests),
            TotalInputTokens = points.Sum(point => point.InputTokens),
            TotalOutputTokens = points.Sum(point => point.OutputTokens),
            TotalCacheTokens = points.Sum(point => point.CacheReadTokens + point.CacheCreationTokens),
            TotalCacheReadTokens = points.Sum(point => point.CacheReadTokens),
            TotalCacheCreationTokens = points.Sum(point => point.CacheCreationTokens),
            TotalTokens = points.Sum(point => point.TotalTokens),
            TotalCost = points.Sum(point => point.Cost),
            TotalActualCost = points.Sum(point => point.ActualCost),
        };
    }
}

/// <summary>
/// A local-calendar range sent to Sub2API.  Its endpoints are inclusive in the
/// public API, hence a seven-day selection is today plus the preceding six
/// calendar days.
/// </summary>
internal readonly record struct CloudUsageDateRange(
    DateOnly StartDate,
    DateOnly EndDate,
    string TimeZone)
{
    public int Days => EndDate.DayNumber - StartDate.DayNumber + 1;

    public string ToQueryString(bool includeGranularity = true, string granularity = "day")
    {
        string query = $"start_date={StartDate:yyyy-MM-dd}&end_date={EndDate:yyyy-MM-dd}";
        if (includeGranularity)
        {
            string normalizedGranularity = string.Equals(granularity, "hour", StringComparison.OrdinalIgnoreCase)
                ? "hour"
                : "day";
            query += $"&granularity={normalizedGranularity}";
        }

        return query + $"&timezone={Uri.EscapeDataString(TimeZone)}";
    }
}

/// <summary>
/// One range-consistent cloud dashboard refresh.  <see cref="Metrics"/>,
/// <see cref="Models"/> and <see cref="Trend"/> use <see cref="Range"/>
/// exactly, so a period switch cannot leave a stale panel from the previous
/// selection visible. Usage-detail rows are intentionally excluded because
/// the desktop dashboard does not display them.
/// </summary>
internal sealed record CloudDashboardSnapshot(
    CloudUsageDateRange Range,
    UsageRangeOverview Metrics,
    IReadOnlyList<ModelStat> Models,
    IReadOnlyList<TrendPoint> Trend);

/// <summary>
/// A privacy-minimized page of user-visible activity.  It intentionally maps
/// only dashboard fields; raw usage-log fields such as API-key identifiers,
/// request IDs, user agents, IP addresses, and endpoint URLs are discarded.
/// </summary>
internal sealed class CloudUsageActivityPage
{
    [JsonPropertyName("items")] public List<CloudUsageActivity> Items { get; set; } = [];
    [JsonPropertyName("total")] public long Total { get; set; }
    [JsonPropertyName("page")] public int Page { get; set; }
    [JsonPropertyName("page_size")] public int PageSize { get; set; }
    [JsonPropertyName("pages")] public int Pages { get; set; }
}

/// <summary>
/// A compact, safe representation of one recent usage record.
/// </summary>
internal sealed class CloudUsageActivity
{
    [JsonPropertyName("created_at")] public DateTimeOffset CreatedAt { get; set; }
    [JsonPropertyName("model")] public string Model { get; set; } = string.Empty;
    [JsonPropertyName("request_type")] public string? RequestType { get; set; }
    [JsonPropertyName("stream")] public bool Stream { get; set; }
    [JsonPropertyName("input_tokens")] public long InputTokens { get; set; }
    [JsonPropertyName("output_tokens")] public long OutputTokens { get; set; }
    [JsonPropertyName("cache_read_tokens")] public long CacheReadTokens { get; set; }
    [JsonPropertyName("cache_creation_tokens")] public long CacheCreationTokens { get; set; }
    [JsonPropertyName("actual_cost")] public double ActualCost { get; set; }
    [JsonPropertyName("total_cost")] public double TotalCost { get; set; }
    [JsonPropertyName("duration_ms")] public long? DurationMilliseconds { get; set; }
    [JsonPropertyName("first_token_ms")] public long? FirstTokenMilliseconds { get; set; }

    public long TotalTokens => InputTokens + OutputTokens + CacheReadTokens + CacheCreationTokens;
}

internal sealed class ModelStat
{
    [JsonPropertyName("model")] public string Model { get; set; } = string.Empty;
    [JsonPropertyName("requests")] public long Requests { get; set; }
    [JsonPropertyName("input_tokens")] public long InputTokens { get; set; }
    [JsonPropertyName("output_tokens")] public long OutputTokens { get; set; }
    [JsonPropertyName("cache_read_tokens")] public long CacheReadTokens { get; set; }
    [JsonPropertyName("cache_creation_tokens")] public long CacheCreationTokens { get; set; }
    [JsonPropertyName("total_tokens")] public long TotalTokens { get; set; }
    [JsonPropertyName("cost")] public double Cost { get; set; }
    [JsonPropertyName("actual_cost")] public double ActualCost { get; set; }
}

internal sealed class TrendPoint
{
    [JsonPropertyName("date")] public string Date { get; set; } = string.Empty;
    [JsonPropertyName("requests")] public long Requests { get; set; }
    [JsonPropertyName("input_tokens")] public long InputTokens { get; set; }
    [JsonPropertyName("output_tokens")] public long OutputTokens { get; set; }
    [JsonPropertyName("cache_read_tokens")] public long CacheReadTokens { get; set; }
    [JsonPropertyName("cache_creation_tokens")] public long CacheCreationTokens { get; set; }
    [JsonPropertyName("total_tokens")] public long TotalTokens { get; set; }
    [JsonPropertyName("cost")] public double Cost { get; set; }
    [JsonPropertyName("actual_cost")] public double ActualCost { get; set; }
}
