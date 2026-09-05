using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text.Json;
using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Services;

internal interface ISub2ApiServiceSummaryClient
{
    Task<Sub2ApiServiceSummary> LoadAsync(
        Sub2ApiSessionAccess access,
        CancellationToken cancellationToken);
}

internal sealed record Sub2ApiServiceSummary(
    decimal Balance,
    decimal FrozenBalance,
    long TodayRequests,
    long TodayTokens,
    double TodayActualCost,
    int ApiKeyCount,
    int ActiveApiKeyCount,
    int RecentFailureCount,
    IReadOnlyList<PlatformQuotaSummary> PlatformQuotas,
    AdminServiceSummary? Administrator,
    bool UsageAvailable = true,
    bool RecentFailuresAvailable = true);

internal sealed record PlatformQuotaSummary(
    string Platform,
    decimal? DailyLimit,
    decimal DailyUsage,
    decimal? WeeklyLimit,
    decimal WeeklyUsage,
    decimal? MonthlyLimit,
    decimal MonthlyUsage);

internal sealed record AdminServiceSummary(
    double CurrentQps,
    double CurrentTps,
    double ErrorRatePercent,
    int? P95LatencyMilliseconds,
    long CurrentConcurrency,
    long WaitingInQueue,
    long TotalAccounts,
    long AvailableAccounts,
    long RateLimitedAccounts,
    long ErrorAccounts,
    string Version,
    string UpdateStatus,
    string LogHealth);

/// <summary>
/// Reads only aggregate service-control data. Optional ops endpoints fail
/// independently so a disabled monitoring module does not hide the user's
/// balance and usage summary.
/// </summary>
internal sealed class Sub2ApiServiceSummaryClient : ISub2ApiServiceSummaryClient, IDisposable
{
    private readonly HttpClient _httpClient;
    private readonly bool _ownsHttpClient;

    public Sub2ApiServiceSummaryClient()
        : this(new HttpClient(new HttpClientHandler
            {
                UseProxy = false,
                AllowAutoRedirect = false,
            })
            {
                Timeout = TimeSpan.FromSeconds(12),
            },
            ownsHttpClient: true)
    {
    }

    internal Sub2ApiServiceSummaryClient(
        HttpClient httpClient,
        bool ownsHttpClient = false)
    {
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        _ownsHttpClient = ownsHttpClient;
    }

    public async Task<Sub2ApiServiceSummary> LoadAsync(
        Sub2ApiSessionAccess access,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(access);
        Task<JsonElement?> usageTask = LoadUserUsageAsync(access, cancellationToken);
        Task<JsonElement?> profileTask = GetOptionalDataAsync(access, "api/v1/user/profile", cancellationToken);
        Task<JsonElement?> quotaTask = GetOptionalDataAsync(access, "api/v1/user/platform-quotas", cancellationToken);
        Task<JsonElement?> errorsTask = GetOptionalEnvelopeAsync(
            access,
            BuildTodayErrorsPath(),
            cancellationToken);

        await Task.WhenAll(usageTask, profileTask, quotaTask, errorsTask).ConfigureAwait(false);
        JsonElement? usage = await usageTask.ConfigureAwait(false);
        JsonElement? profile = await profileTask.ConfigureAwait(false);
        JsonElement? errors = await errorsTask.ConfigureAwait(false);

        decimal balance = GetDecimal(profile, "balance") ?? access.Balance;
        decimal frozenBalance = GetDecimal(profile, "frozen_balance") ?? access.FrozenBalance;
        int recentFailures = GetPaginationTotal(errors) ?? 0;
        IReadOnlyList<PlatformQuotaSummary> quotas = ParsePlatformQuotas(await quotaTask.ConfigureAwait(false));
        AdminServiceSummary? admin = access.IsAdministrator
            ? await LoadAdministratorAsync(access, cancellationToken).ConfigureAwait(false)
            : null;

        return new Sub2ApiServiceSummary(
            balance,
            frozenBalance,
            GetInt64(usage, "today_requests") ?? GetInt64(usage, "total_requests") ?? 0,
            GetInt64(usage, "today_tokens") ?? GetInt64(usage, "total_tokens") ?? 0,
            GetDouble(usage, "today_actual_cost") ?? GetDouble(usage, "total_actual_cost") ?? 0,
            GetInt32(usage, "total_api_keys") ?? 0,
            GetInt32(usage, "active_api_keys") ?? 0,
            recentFailures,
            quotas,
            admin,
            usage is not null,
            errors is not null);
    }

    private async Task<JsonElement?> LoadUserUsageAsync(
        Sub2ApiSessionAccess access,
        CancellationToken cancellationToken)
    {
        JsonElement? dashboard = await GetOptionalDataAsync(
                access,
                "api/v1/usage/dashboard/stats",
                cancellationToken)
            .ConfigureAwait(false);
        if (dashboard is not null)
        {
            return dashboard;
        }

        // The range endpoint is the same authoritative ledger used by the
        // detailed usage dashboard. It keeps the service summary useful when
        // the lightweight dashboard aggregate is unavailable on a deployment.
        return await GetOptionalDataAsync(
                access,
                BuildTodayUsageStatsPath(),
                cancellationToken)
            .ConfigureAwait(false);
    }

    private static string BuildTodayUsageStatsPath()
    {
        (string today, string timezone) = GetTodayQueryValues();
        return "api/v1/usage/stats" +
               $"?start_date={today}&end_date={today}&timezone={Uri.EscapeDataString(timezone)}";
    }

    private static string BuildTodayErrorsPath()
    {
        (string today, string timezone) = GetTodayQueryValues();
        return "api/v1/usage/errors?page=1&page_size=1&sort_by=created_at&sort_order=desc" +
               $"&start_date={today}&end_date={today}&timezone={Uri.EscapeDataString(timezone)}";
    }

    private static (string Today, string Timezone) GetTodayQueryValues()
    {
        string today = DateTime.Today.ToString("yyyy-MM-dd", System.Globalization.CultureInfo.InvariantCulture);
        string timezone = TimeZoneInfo.TryConvertWindowsIdToIanaId(TimeZoneInfo.Local.Id, out string? iana)
            ? iana
            : TimeZoneInfo.Local.Id;
        return (today, timezone);
    }

    public void Dispose()
    {
        if (_ownsHttpClient)
        {
            _httpClient.Dispose();
        }
    }

    private async Task<AdminServiceSummary> LoadAdministratorAsync(
        Sub2ApiSessionAccess access,
        CancellationToken cancellationToken)
    {
        Task<JsonElement?> snapshotTask = GetOptionalDataAsync(
            access,
            "api/v1/admin/ops/dashboard/snapshot-v2",
            cancellationToken);
        Task<JsonElement?> realtimeTask = GetOptionalDataAsync(
            access,
            "api/v1/admin/ops/realtime-traffic?window=1min",
            cancellationToken);
        Task<JsonElement?> concurrencyTask = GetOptionalDataAsync(
            access,
            "api/v1/admin/ops/concurrency",
            cancellationToken);
        Task<JsonElement?> availabilityTask = GetOptionalDataAsync(
            access,
            "api/v1/admin/ops/account-availability",
            cancellationToken);
        Task<JsonElement?> versionTask = GetOptionalDataAsync(
            access,
            "api/v1/admin/system/version",
            cancellationToken);
        Task<JsonElement?> updateTask = GetOptionalDataAsync(
            access,
            "api/v1/admin/system/check-updates",
            cancellationToken);
        Task<JsonElement?> logHealthTask = GetOptionalDataAsync(
            access,
            "api/v1/admin/ops/system-logs/health",
            cancellationToken);

        await Task.WhenAll(
            snapshotTask,
            realtimeTask,
            concurrencyTask,
            availabilityTask,
            versionTask,
            updateTask,
            logHealthTask).ConfigureAwait(false);

        JsonElement? snapshot = await snapshotTask.ConfigureAwait(false);
        JsonElement? realtime = await realtimeTask.ConfigureAwait(false);
        JsonElement? concurrency = await concurrencyTask.ConfigureAwait(false);
        JsonElement? availability = await availabilityTask.ConfigureAwait(false);
        return new AdminServiceSummary(
            GetDouble(realtime, "qps", "current") ?? GetDouble(snapshot, "overview", "qps", "current") ?? 0d,
            GetDouble(realtime, "tps", "current") ?? GetDouble(snapshot, "overview", "tps", "current") ?? 0d,
            NormalizePercent(GetDouble(snapshot, "overview", "error_rate") ?? 0d),
            GetInt32(snapshot, "overview", "duration", "p95_ms"),
            SumObjectValues(concurrency, "platform", "current_in_use"),
            SumObjectValues(concurrency, "platform", "waiting_in_queue"),
            SumObjectValues(availability, "platform", "total_accounts"),
            SumObjectValues(availability, "platform", "available_count"),
            SumObjectValues(availability, "platform", "rate_limit_count"),
            SumObjectValues(availability, "platform", "error_count"),
            GetString(await versionTask.ConfigureAwait(false), "version") ?? "未知",
            DescribeUpdate(await updateTask.ConfigureAwait(false)),
            DescribeLogHealth(await logHealthTask.ConfigureAwait(false)));
    }

    private async Task<JsonElement?> GetOptionalDataAsync(
        Sub2ApiSessionAccess access,
        string relativePath,
        CancellationToken cancellationToken)
    {
        JsonElement? envelope = await GetOptionalEnvelopeAsync(access, relativePath, cancellationToken).ConfigureAwait(false);
        return envelope is { ValueKind: JsonValueKind.Object } root &&
               root.TryGetProperty("data", out JsonElement data)
            ? data.Clone()
            : null;
    }

    private async Task<JsonElement?> GetOptionalEnvelopeAsync(
        Sub2ApiSessionAccess access,
        string relativePath,
        CancellationToken cancellationToken)
    {
        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Get, new Uri(access.ApiBaseUri, relativePath));
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", access.AccessToken);
            using HttpResponseMessage response = await _httpClient
                .SendAsync(request, HttpCompletionOption.ResponseContentRead, cancellationToken)
                .ConfigureAwait(false);
            if (!response.IsSuccessStatusCode)
            {
                return null;
            }

            byte[] bytes = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
            try
            {
                using JsonDocument document = JsonDocument.Parse(bytes);
                JsonElement root = document.RootElement;
                return root.ValueKind == JsonValueKind.Object && IsSuccessfulEnvelope(root)
                    ? root.Clone()
                    : null;
            }
            finally
            {
                CryptographicOperations.ZeroMemory(bytes);
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (Exception exception) when (
            exception is not OperationCanceledException || !cancellationToken.IsCancellationRequested)
        {
            return null;
        }
    }

    private static bool IsSuccessfulEnvelope(JsonElement root)
    {
        if (!root.TryGetProperty("code", out JsonElement code))
        {
            return true;
        }

        if (code.ValueKind == JsonValueKind.Number && code.TryGetInt32(out int numeric))
        {
            return numeric == 0;
        }

        if (code.ValueKind == JsonValueKind.String)
        {
            string? value = code.GetString()?.Trim();
            return string.Equals(value, "0", StringComparison.OrdinalIgnoreCase) ||
                   string.Equals(value, "ok", StringComparison.OrdinalIgnoreCase) ||
                   string.Equals(value, "success", StringComparison.OrdinalIgnoreCase);
        }

        return false;
    }

    private static IReadOnlyList<PlatformQuotaSummary> ParsePlatformQuotas(JsonElement? data)
    {
        if (!TryGet(data, out JsonElement quotas, "platform_quotas") || quotas.ValueKind != JsonValueKind.Array)
        {
            return Array.Empty<PlatformQuotaSummary>();
        }

        return quotas.EnumerateArray()
            .Where(item => item.ValueKind == JsonValueKind.Object)
            .Select(item => new PlatformQuotaSummary(
                GetString(item, "platform") ?? "unknown",
                GetDecimal(item, "daily_limit_usd"),
                GetDecimal(item, "daily_usage_usd") ?? 0m,
                GetDecimal(item, "weekly_limit_usd"),
                GetDecimal(item, "weekly_usage_usd") ?? 0m,
                GetDecimal(item, "monthly_limit_usd"),
                GetDecimal(item, "monthly_usage_usd") ?? 0m))
            .ToArray();
    }

    private static int? GetPaginationTotal(JsonElement? root)
        => GetInt32(root, "pagination", "total") ??
           GetInt32(root, "data", "total") ??
           GetInt32(root, "total");

    private static long SumObjectValues(JsonElement? root, string collection, string property)
    {
        if (!TryGet(root, out JsonElement values, collection) || values.ValueKind != JsonValueKind.Object)
        {
            return 0;
        }

        long total = 0;
        foreach (JsonProperty item in values.EnumerateObject())
        {
            if (TryGet(item.Value, out JsonElement number, property) && number.TryGetInt64(out long parsed))
            {
                total += parsed;
            }
        }

        return total;
    }

    private static string DescribeUpdate(JsonElement? data)
    {
        bool? available = GetBoolean(data, "update_available") ?? GetBoolean(data, "has_update");
        string? latest = GetString(data, "latest_version");
        return available == true
            ? string.IsNullOrWhiteSpace(latest) ? "有可用更新" : $"可更新至 {latest}"
            : available == false ? "已是最新版本" : "暂未检查";
    }

    private static string DescribeLogHealth(JsonElement? data)
    {
        bool? healthy = GetBoolean(data, "healthy") ?? GetBoolean(data, "is_healthy");
        return healthy switch
        {
            true => "日志采集正常",
            false => "日志采集异常",
            null => "未启用或暂无数据",
        };
    }

    private static double NormalizePercent(double value) => value is >= 0 and <= 1 ? value * 100d : value;

    private static string? GetString(JsonElement? root, params string[] path)
        => TryGet(root, out JsonElement value, path) && value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;

    private static bool? GetBoolean(JsonElement? root, params string[] path)
        => TryGet(root, out JsonElement value, path) && value.ValueKind is JsonValueKind.True or JsonValueKind.False
            ? value.GetBoolean()
            : null;

    private static decimal? GetDecimal(JsonElement? root, params string[] path)
    {
        if (!TryGet(root, out JsonElement value, path))
        {
            return null;
        }

        if (value.ValueKind == JsonValueKind.Number && value.TryGetDecimal(out decimal number))
        {
            return number;
        }

        return value.ValueKind == JsonValueKind.String &&
               decimal.TryParse(
                   value.GetString(),
                   System.Globalization.NumberStyles.Float,
                   System.Globalization.CultureInfo.InvariantCulture,
                   out number)
            ? number
            : null;
    }

    private static double? GetDouble(JsonElement? root, params string[] path)
    {
        if (!TryGet(root, out JsonElement value, path))
        {
            return null;
        }

        if (value.ValueKind == JsonValueKind.Number && value.TryGetDouble(out double number))
        {
            return number;
        }

        return value.ValueKind == JsonValueKind.String &&
               double.TryParse(
                   value.GetString(),
                   System.Globalization.NumberStyles.Float,
                   System.Globalization.CultureInfo.InvariantCulture,
                   out number)
            ? number
            : null;
    }

    private static int? GetInt32(JsonElement? root, params string[] path)
    {
        if (!TryGet(root, out JsonElement value, path))
        {
            return null;
        }

        if (value.ValueKind == JsonValueKind.Number && value.TryGetInt32(out int number))
        {
            return number;
        }

        return value.ValueKind == JsonValueKind.String &&
               int.TryParse(
                   value.GetString(),
                   System.Globalization.NumberStyles.Integer,
                   System.Globalization.CultureInfo.InvariantCulture,
                   out number)
            ? number
            : null;
    }

    private static long? GetInt64(JsonElement? root, params string[] path)
    {
        if (!TryGet(root, out JsonElement value, path))
        {
            return null;
        }

        if (value.ValueKind == JsonValueKind.Number && value.TryGetInt64(out long number))
        {
            return number;
        }

        return value.ValueKind == JsonValueKind.String &&
               long.TryParse(
                   value.GetString(),
                   System.Globalization.NumberStyles.Integer,
                   System.Globalization.CultureInfo.InvariantCulture,
                   out number)
            ? number
            : null;
    }

    private static bool TryGet(JsonElement? root, out JsonElement value, params string[] path)
    {
        value = default;
        if (root is not { } current)
        {
            return false;
        }

        foreach (string segment in path)
        {
            if (current.ValueKind != JsonValueKind.Object || !current.TryGetProperty(segment, out current))
            {
                return false;
            }
        }

        value = current;
        return true;
    }
}
