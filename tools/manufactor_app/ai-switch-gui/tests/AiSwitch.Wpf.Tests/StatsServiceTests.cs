using System.Collections.Concurrent;
using System.Net;
using System.Net.Http;
using System.Text;
using AiSwitchGui;

namespace AiSwitch.Wpf.Tests;

public sealed class StatsServiceTests
{
    [Theory]
    [InlineData(1, "2026-07-14", "2026-07-14")]
    [InlineData(7, "2026-07-08", "2026-07-14")]
    [InlineData(30, "2026-06-15", "2026-07-14")]
    public void BuildDashboardRangeQuery_UsesInclusiveLocalCalendarWindow(
        int days,
        string expectedStart,
        string expectedEnd)
    {
        string query = StatsService.BuildDashboardRangeQuery(
            days,
            new DateOnly(2026, 7, 14),
            "Asia/Shanghai");

        Assert.Equal(
            $"start_date={expectedStart}&end_date={expectedEnd}&granularity=day&timezone=Asia%2FShanghai",
            query);
    }

    [Fact]
    public void CloudUsageDateRange_AllowsHourlyTrendGranularity()
    {
        var range = new CloudUsageDateRange(
            new DateOnly(2026, 7, 14),
            new DateOnly(2026, 7, 14),
            "Asia/Shanghai");

        Assert.Equal(
            "start_date=2026-07-14&end_date=2026-07-14&granularity=hour&timezone=Asia%2FShanghai",
            range.ToQueryString(granularity: "hour"));
    }

    [Fact]
    public async Task GetDashboardSnapshotAsync_UsesHourlyTrendOnlyForToday()
    {
        var handler = new DashboardHandler();
        using var httpClient = new HttpClient(handler)
        {
            BaseAddress = new Uri("https://statistics.example/"),
            Timeout = TimeSpan.FromSeconds(3),
        };
        var service = new StatsService(
            new StatsSettings
            {
                GatewayBaseUrl = "https://statistics.example",
                Email = "user@example.com",
                Password = "one-time-password",
                TrendDays = 1,
            },
            httpClient,
            utcNow: () => new DateTimeOffset(2026, 7, 13, 17, 10, 0, TimeSpan.Zero),
            timeZoneResolver: () => "Asia/Shanghai");

        await service.GetDashboardSnapshotAsync(CancellationToken.None);

        RecordedRequest[] dashboardCalls = handler.Requests
            .Where(call => call.Uri.AbsolutePath != "/api/v1/auth/login")
            .ToArray();
        RecordedRequest trend = Assert.Single(
            dashboardCalls,
            call => call.Uri.AbsolutePath == "/api/v1/usage/dashboard/trend");
        Assert.Contains("granularity=hour", trend.Uri.Query, StringComparison.Ordinal);
        Assert.All(
            dashboardCalls.Where(call => call.Uri.AbsolutePath != "/api/v1/usage/dashboard/trend"),
            call => Assert.Contains("granularity=day", call.Uri.Query, StringComparison.Ordinal));
    }

    [Fact]
    public async Task GetDashboardSnapshotAsync_UsesOneExactRangeWithoutLoadingUsageDetails()
    {
        var handler = new DashboardHandler();
        using var httpClient = new HttpClient(handler)
        {
            BaseAddress = new Uri("https://statistics.example/"),
            Timeout = TimeSpan.FromSeconds(3),
        };
        var service = new StatsService(
            new StatsSettings
            {
                GatewayBaseUrl = "https://statistics.example",
                Email = "user@example.com",
                Password = "one-time-password",
                TrendDays = 30,
            },
            httpClient,
            utcNow: () => new DateTimeOffset(2026, 7, 13, 17, 10, 0, TimeSpan.Zero),
            timeZoneResolver: () => "Asia/Shanghai");

        CloudDashboardSnapshot snapshot = await service.GetDashboardSnapshotAsync(CancellationToken.None);

        Assert.Equal(new DateOnly(2026, 6, 15), snapshot.Range.StartDate);
        Assert.Equal(new DateOnly(2026, 7, 14), snapshot.Range.EndDate);
        Assert.Equal("Asia/Shanghai", snapshot.Range.TimeZone);
        Assert.Equal(30, snapshot.Range.Days);
        Assert.Equal(9L, snapshot.Metrics.TotalRequests);
        Assert.Equal(1_240L, snapshot.Metrics.TotalTokens);
        Assert.Equal(2.75, snapshot.Metrics.TotalActualCost);
        Assert.Equal("gpt-5.4", Assert.Single(snapshot.Models).Model);
        Assert.Equal("2026-07-14", Assert.Single(snapshot.Trend).Date);

        RecordedRequest[] calls = handler.Requests.ToArray();
        Assert.Equal(4, calls.Length);
        Assert.Single(calls, call => call.Uri.AbsolutePath == "/api/v1/auth/login");

        RecordedRequest[] dashboardCalls = calls
            .Where(call => call.Uri.AbsolutePath != "/api/v1/auth/login")
            .ToArray();
        Assert.Equal(3, dashboardCalls.Length);
        Assert.All(dashboardCalls, call =>
        {
            Assert.Equal("Bearer", call.AuthorizationScheme);
            Assert.Equal("access-token", call.AuthorizationParameter);
            Assert.Contains("start_date=2026-06-15", call.Uri.Query, StringComparison.Ordinal);
            Assert.Contains("end_date=2026-07-14", call.Uri.Query, StringComparison.Ordinal);
            Assert.Contains("timezone=Asia%2FShanghai", call.Uri.Query, StringComparison.Ordinal);
        });

        Assert.Single(dashboardCalls, call => call.Uri.AbsolutePath == "/api/v1/usage/stats");
        Assert.Single(dashboardCalls, call => call.Uri.AbsolutePath == "/api/v1/usage/dashboard/models");
        Assert.Single(dashboardCalls, call => call.Uri.AbsolutePath == "/api/v1/usage/dashboard/trend");
        Assert.DoesNotContain(dashboardCalls, call => call.Uri.AbsolutePath == "/api/v1/usage");
    }

    private sealed record RecordedRequest(Uri Uri, string? AuthorizationScheme, string? AuthorizationParameter);

    private sealed class DashboardHandler : HttpMessageHandler
    {
        public ConcurrentQueue<RecordedRequest> Requests { get; } = new();

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            Uri uri = request.RequestUri ?? throw new InvalidOperationException("Request URI was not supplied.");
            Requests.Enqueue(new RecordedRequest(
                uri,
                request.Headers.Authorization?.Scheme,
                request.Headers.Authorization?.Parameter));

            string body = uri.AbsolutePath switch
            {
                "/api/v1/auth/login" => "{\"code\":0,\"data\":{\"access_token\":\"access-token\"}}",
                "/api/v1/usage/stats" => "{\"code\":0,\"data\":{\"total_requests\":9,\"total_input_tokens\":700,\"total_output_tokens\":300,\"total_cache_tokens\":240,\"total_cache_read_tokens\":200,\"total_cache_creation_tokens\":40,\"total_tokens\":1240,\"total_cost\":3.1,\"total_actual_cost\":2.75,\"average_duration_ms\":873}}",
                "/api/v1/usage/dashboard/models" => "{\"code\":0,\"data\":{\"models\":[{\"model\":\"gpt-5.4\",\"requests\":9,\"input_tokens\":700,\"output_tokens\":300,\"cache_read_tokens\":200,\"cache_creation_tokens\":40,\"total_tokens\":1240,\"cost\":3.1,\"actual_cost\":2.75}]}}",
                "/api/v1/usage/dashboard/trend" => "{\"code\":0,\"data\":{\"trend\":[{\"date\":\"2026-07-14\",\"requests\":9,\"input_tokens\":700,\"output_tokens\":300,\"cache_read_tokens\":200,\"cache_creation_tokens\":40,\"total_tokens\":1240,\"cost\":3.1,\"actual_cost\":2.75}]}}",
                "/api/v1/usage" => "{\"code\":0,\"data\":{\"items\":[{\"model\":\"gpt-5.4\",\"request_type\":\"stream\",\"stream\":true,\"input_tokens\":700,\"output_tokens\":300,\"cache_read_tokens\":200,\"cache_creation_tokens\":40,\"total_cost\":3.1,\"actual_cost\":2.75,\"duration_ms\":873,\"created_at\":\"2026-07-14T09:10:00Z\",\"api_key_id\":91,\"request_id\":\"not-mapped-into-dashboard\",\"ip_address\":\"127.0.0.1\"}],\"total\":1,\"page\":1,\"page_size\":25,\"pages\":1}}",
                _ => throw new InvalidOperationException($"Unexpected endpoint: {uri.AbsolutePath}"),
            };

            return Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent(body, Encoding.UTF8, "application/json"),
            });
        }
    }
}
