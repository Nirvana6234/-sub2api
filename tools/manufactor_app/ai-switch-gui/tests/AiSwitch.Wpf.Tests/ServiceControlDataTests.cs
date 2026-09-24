using System.Collections.Concurrent;
using System.Net;
using System.Net.Http;
using System.Text;
using AiSwitchGui;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class ServiceControlDataTests
{
    [Fact]
    public async Task ServiceSummary_UserDataKeepsCloudActualsSeparateWithoutReadingContributionWallet()
    {
        var handler = new SummaryHandler();
        using var client = new Sub2ApiServiceSummaryClient(
            new HttpClient(handler),
            ownsHttpClient: true);

        Sub2ApiServiceSummary summary = await client.LoadAsync(
            Access("user"),
            CancellationToken.None);

        Assert.Equal(9m, summary.Balance);
        Assert.Equal(3, summary.TodayRequests);
        Assert.Equal(100, summary.TodayTokens);
        Assert.Equal(0.25, summary.TodayActualCost, 5);
        Assert.Equal(4, summary.RecentFailureCount);
        Assert.True(summary.RecentFailuresAvailable);
        PlatformQuotaSummary quota = Assert.Single(summary.PlatformQuotas);
        Assert.Null(quota.DailyLimit);
        Assert.Equal(2.5m, quota.DailyUsage);
        Assert.Null(summary.Administrator);
        Assert.Contains(handler.Paths, path => path.StartsWith("/api/v1/usage/dashboard/stats", StringComparison.Ordinal));
        Assert.DoesNotContain(handler.Paths, path => path.StartsWith("/api/v1/account-contributions", StringComparison.Ordinal));
        Assert.DoesNotContain(handler.Paths, path => path.Contains("snapshot-v2", StringComparison.Ordinal));
        string errorsPath = Assert.Single(handler.Paths, path => path.StartsWith("/api/v1/usage/errors", StringComparison.Ordinal));
        Assert.Contains("start_date=", errorsPath, StringComparison.Ordinal);
        Assert.Contains("end_date=", errorsPath, StringComparison.Ordinal);
        Assert.Contains("timezone=", errorsPath, StringComparison.Ordinal);
    }

    [Fact]
    public async Task ServiceSummary_AdminDataUsesPlatformAggregatesWithoutAccountDetails()
    {
        using var client = new Sub2ApiServiceSummaryClient(
            new HttpClient(new SummaryHandler()),
            ownsHttpClient: true);

        Sub2ApiServiceSummary summary = await client.LoadAsync(
            Access("admin"),
            CancellationToken.None);

        AdminServiceSummary admin = Assert.IsType<AdminServiceSummary>(summary.Administrator);
        Assert.Equal(1.2, admin.CurrentQps, 5);
        Assert.Equal(45, admin.CurrentTps, 5);
        Assert.Equal(2.5, admin.ErrorRatePercent, 5);
        Assert.Equal(420, admin.P95LatencyMilliseconds);
        Assert.Equal(3, admin.CurrentConcurrency);
        Assert.Equal(1, admin.WaitingInQueue);
        Assert.Equal(12, admin.TotalAccounts);
        Assert.Equal(9, admin.AvailableAccounts);
        Assert.Equal("1.2.3", admin.Version);
        Assert.Equal("日志采集正常", admin.LogHealth);
    }

    [Fact]
    public async Task ServiceSummary_MalformedOptionalSection_DoesNotDiscardValidUsage()
    {
        using var client = new Sub2ApiServiceSummaryClient(
            new HttpClient(new PartialSummaryHandler()),
            ownsHttpClient: true);

        Sub2ApiServiceSummary summary = await client.LoadAsync(Access("user"), CancellationToken.None);

        Assert.True(summary.UsageAvailable);
        Assert.Equal(3, summary.TodayRequests);
        Assert.Equal(100, summary.TodayTokens);
        Assert.Equal(0.25, summary.TodayActualCost, 5);
        Assert.Equal(5m, summary.Balance);
    }

    [Fact]
    public async Task ServiceSummary_DashboardStatsUnavailable_FallsBackToTodayLedgerRange()
    {
        var handler = new UsageFallbackHandler();
        using var client = new Sub2ApiServiceSummaryClient(
            new HttpClient(handler),
            ownsHttpClient: true);

        Sub2ApiServiceSummary summary = await client.LoadAsync(Access("user"), CancellationToken.None);

        Assert.True(summary.UsageAvailable);
        Assert.Equal(8, summary.TodayRequests);
        Assert.Equal(2048, summary.TodayTokens);
        Assert.Equal(1.5, summary.TodayActualCost, 5);
        Assert.Contains(handler.Paths, path => path.StartsWith("/api/v1/usage/dashboard/stats", StringComparison.Ordinal));
        string fallbackPath = Assert.Single(
            handler.Paths,
            path => path.StartsWith("/api/v1/usage/stats", StringComparison.Ordinal));
        Assert.Contains("start_date=", fallbackPath, StringComparison.Ordinal);
        Assert.Contains("end_date=", fallbackPath, StringComparison.Ordinal);
        Assert.Contains("timezone=", fallbackPath, StringComparison.Ordinal);
    }

    [Fact]
    public async Task ServiceSummary_DisabledErrorHistory_DoesNotHideOtherUserData()
    {
        using var client = new Sub2ApiServiceSummaryClient(
            new HttpClient(new DisabledErrorsHandler()),
            ownsHttpClient: true);

        Sub2ApiServiceSummary summary = await client.LoadAsync(Access("user"), CancellationToken.None);

        Assert.True(summary.UsageAvailable);
        Assert.False(summary.RecentFailuresAvailable);
        Assert.Equal(3, summary.TodayRequests);
    }

    [Fact]
    public async Task EndpointProbe_ProbesAllRoutesAndClassifiesAuthenticationFailure()
    {
        var telemetry = new MemoryTelemetryRepository();
        var credentials = new StubCredentialProvider();
        using var service = new EndpointProbeService(
            credentials,
            telemetry,
            new HttpClient(new ProbeHandler()),
            ownsHttpClient: true);
        ConnectionProfile profile = new()
        {
            Id = "cloud",
            Name = "云端来源",
            Kind = ConnectionProfileKind.Cloud,
            BaseUrl = "https://example.test",
            ClientBaseUrls = new Dictionary<CliKind, string>
            {
                [CliKind.Codex] = "https://example.test/openai/v1",
                [CliKind.ClaudeCode] = "https://example.test/claude",
                [CliKind.GeminiCli] = "https://example.test/gemini",
                [CliKind.GrokCli] = "https://example.test/grok/v1",
            },
            EnabledClients = Enum.GetValues<CliKind>(),
        };
        var routing = new ConnectionProfileRouting("cloud", "cloud", "cloud");

        IReadOnlyList<EndpointHealthResult> results = await service.ProbeAllAsync(
            [profile],
            routing,
            new ConnectionProfileSelection("cloud", null, "cloud"),
            CancellationToken.None);

        Assert.Equal(4, results.Count);
        Assert.True(results.Single(item => item.CliKind == CliKind.Codex).Succeeded);
        EndpointHealthResult claude = results.Single(item => item.CliKind == CliKind.ClaudeCode);
        Assert.False(claude.Succeeded);
        Assert.Equal("authentication", claude.StatusCategory);
        Assert.Equal(4, telemetry.Probes.Count);
        Assert.All(telemetry.Probes, probe => Assert.StartsWith("route-", probe.SourceId, StringComparison.Ordinal));
    }

    private static Sub2ApiSessionAccess Access(string role)
        => new(
            new Uri("http://127.0.0.1:8080/"),
            "access-token",
            7,
            role,
            5m,
            1m,
            DateTimeOffset.UtcNow.AddMinutes(30));

    private sealed class SummaryHandler : HttpMessageHandler
    {
        public ConcurrentBag<string> Paths { get; } = [];

        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            string path = request.RequestUri!.PathAndQuery;
            Paths.Add(path);
            string json = path switch
            {
                var value when value.StartsWith("/api/v1/user/profile", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"balance\":9,\"frozen_balance\":1}}",
                var value when value.StartsWith("/api/v1/usage/dashboard/stats", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"today_requests\":3,\"today_tokens\":100,\"today_actual_cost\":0.25,\"total_api_keys\":2,\"active_api_keys\":1}}",
                var value when value.StartsWith("/api/v1/user/platform-quotas", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"platform_quotas\":[{\"platform\":\"openai\",\"daily_limit_usd\":null,\"daily_usage_usd\":\"2.5\",\"weekly_limit_usd\":null,\"monthly_limit_usd\":null}]}}",
                var value when value.StartsWith("/api/v1/usage/errors", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":[],\"pagination\":{\"total\":4}}",
                var value when value.StartsWith("/api/v1/admin/ops/dashboard/snapshot-v2", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"overview\":{\"error_rate\":0.025,\"duration\":{\"p95_ms\":420}}}}",
                var value when value.StartsWith("/api/v1/admin/ops/realtime-traffic", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"qps\":{\"current\":1.2},\"tps\":{\"current\":45}}}",
                var value when value.StartsWith("/api/v1/admin/ops/concurrency", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"platform\":{\"openai\":{\"current_in_use\":2,\"waiting_in_queue\":1},\"gemini\":{\"current_in_use\":1,\"waiting_in_queue\":0}}}}",
                var value when value.StartsWith("/api/v1/admin/ops/account-availability", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"platform\":{\"openai\":{\"total_accounts\":10,\"available_count\":8,\"rate_limit_count\":1,\"error_count\":1},\"gemini\":{\"total_accounts\":2,\"available_count\":1,\"rate_limit_count\":0,\"error_count\":1}}}}",
                var value when value.StartsWith("/api/v1/admin/system/version", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"version\":\"1.2.3\"}}",
                var value when value.StartsWith("/api/v1/admin/system/check-updates", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"update_available\":false}}",
                var value when value.StartsWith("/api/v1/admin/ops/system-logs/health", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"healthy\":true}}",
                _ => "{\"code\":0,\"data\":{}}",
            };
            return Task.FromResult(Json(json));
        }
    }

    private sealed class ProbeHandler : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            HttpStatusCode status = request.RequestUri!.AbsolutePath.Contains("/claude/", StringComparison.Ordinal)
                ? HttpStatusCode.Unauthorized
                : HttpStatusCode.OK;
            return Task.FromResult(Json("{}", status));
        }
    }

    private sealed class PartialSummaryHandler : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            string path = request.RequestUri!.AbsolutePath;
            string json = path switch
            {
                "/api/v1/usage/dashboard/stats" =>
                    "{\"code\":0,\"data\":{\"today_requests\":3,\"today_tokens\":100,\"today_actual_cost\":0.25}}",
                "/api/v1/user/profile" => "{\"code\":{\"unexpected\":true},\"data\":{}}",
                _ => "{\"code\":0,\"data\":{}}",
            };
            return Task.FromResult(Json(json));
        }
    }

    private sealed class UsageFallbackHandler : HttpMessageHandler
    {
        public ConcurrentBag<string> Paths { get; } = [];

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            string path = request.RequestUri!.PathAndQuery;
            Paths.Add(path);
            return Task.FromResult(path.StartsWith("/api/v1/usage/dashboard/stats", StringComparison.Ordinal)
                ? Json("{\"code\":500,\"message\":\"aggregate unavailable\"}", HttpStatusCode.InternalServerError)
                : path.StartsWith("/api/v1/usage/stats", StringComparison.Ordinal)
                    ? Json("{\"code\":0,\"data\":{\"total_requests\":8,\"total_tokens\":2048,\"total_actual_cost\":1.5}}")
                    : Json("{\"code\":0,\"data\":{}}"));
        }
    }

    private sealed class DisabledErrorsHandler : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            string path = request.RequestUri!.PathAndQuery;
            string json = path switch
            {
                var value when value.StartsWith("/api/v1/usage/errors", StringComparison.Ordinal) =>
                    "{\"code\":403,\"message\":\"Error requests view is disabled\"}",
                var value when value.StartsWith("/api/v1/usage/dashboard/stats", StringComparison.Ordinal) =>
                    "{\"code\":0,\"data\":{\"today_requests\":3,\"today_tokens\":100,\"today_actual_cost\":0.25}}",
                _ => "{\"code\":0,\"data\":{}}",
            };
            HttpStatusCode status = path.StartsWith("/api/v1/usage/errors", StringComparison.Ordinal)
                ? HttpStatusCode.Forbidden
                : HttpStatusCode.OK;
            return Task.FromResult(Json(json, status));
        }
    }

    private sealed class StubCredentialProvider : IConnectionCredentialProvider
    {
        public ValueTask<string?> GetSecretAsync(string connectionProfileId, CliKind client, CancellationToken cancellationToken = default)
            => ValueTask.FromResult<string?>("secret-value");
    }

    private sealed class MemoryTelemetryRepository : ILocalTelemetryRepository
    {
        public ConcurrentBag<LocalNetworkHealthProbe> Probes { get; } = [];

        public Task RecordUsageAsync(LocalUsageTelemetryEvent telemetryEvent, CancellationToken cancellationToken = default)
            => Task.CompletedTask;

        public Task RecordNetworkProbeAsync(LocalNetworkHealthProbe probe, CancellationToken cancellationToken = default)
        {
            Probes.Add(probe);
            return Task.CompletedTask;
        }

        public Task<LocalTelemetrySnapshot> GetSnapshotAsync(TimeZoneInfo? timeZone = null, CancellationToken cancellationToken = default)
            => Task.FromResult(new LocalTelemetrySnapshot(
                DateTimeOffset.UtcNow,
                LocalTelemetryUsageSummary.Empty,
                LocalTelemetryUsageSummary.Empty,
                Array.Empty<LocalTelemetryDailyUsage>(),
                null));

        public Task<IReadOnlyList<LocalNetworkHealthSummary>> GetNetworkHealthSummariesAsync(DateTimeOffset sinceUtc, CancellationToken cancellationToken = default)
            => Task.FromResult<IReadOnlyList<LocalNetworkHealthSummary>>(Probes.Select(probe => new LocalNetworkHealthSummary(
                probe.SourceId,
                probe.SourceLabel,
                1,
                probe.Succeeded ? 1 : 0,
                probe.Succeeded ? 100 : 0,
                probe.LatencyMilliseconds,
                probe.LatencyMilliseconds,
                probe.Succeeded ? probe.Timestamp : null,
                probe.StatusCategory)).ToArray());
    }

    private static HttpResponseMessage Json(string json, HttpStatusCode status = HttpStatusCode.OK)
        => new(status) { Content = new StringContent(json, Encoding.UTF8, "application/json") };
}

