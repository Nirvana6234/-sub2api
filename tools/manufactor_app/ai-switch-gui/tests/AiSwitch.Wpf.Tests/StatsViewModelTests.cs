using AiSwitchGui;
using System.Net;
using System.Net.Http;
using System.Text;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class StatsViewModelTests
{
    [Fact]
    public async Task InitializeAsync_MigratesPlaintextPasswordAndKeepsDashboardPinnedToLocalBackend()
    {
        const string secret = "saved-secret";
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "http://192.168.1.8:8080",
            Email = "user@example.com",
            Password = secret,
            TrendDays = 14,
        });
        var viewModel = CreateManualViewModel(controller);

        await viewModel.InitializeAsync();

        Assert.Equal(string.Empty, viewModel.GatewayBaseUrl);
        Assert.Equal("user@example.com", viewModel.Email);
        // The dashboard now intentionally supports the three explicit
        // calendar windows exposed by the UI.  Legacy unsupported values are
        // migrated to the default seven-day window instead of silently being
        // sent to the backend.
        Assert.Equal(7, viewModel.SelectedTrendDays);
        Assert.False(viewModel.HasSavedPassword);
        Assert.False(viewModel.ShowManualCloudCredentialForm);
        Assert.Equal("当前后台：本机中转", viewModel.BackendSourceLabel);
        Assert.Contains("不会改用远程来源", viewModel.CloudConnectionNotice, StringComparison.Ordinal);
        Assert.Contains("本次", viewModel.PasswordHint, StringComparison.Ordinal);
        Assert.DoesNotContain(secret, viewModel.PasswordHint, StringComparison.Ordinal);
        Assert.DoesNotContain(secret, viewModel.StatusNotice, StringComparison.Ordinal);
        Assert.Null(typeof(StatsViewModel).GetProperty("Password"));
        Assert.NotNull(controller.SavedSettings);
        Assert.Equal(string.Empty, controller.SavedSettings!.Password);
    }

    [Fact]
    public async Task RefreshAsync_ManualCloudUsesOnlySubmittedPasswordAndMapsAllThreeDashboardResponses()
    {
        const string submittedPassword = "one-time-secret";
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "http://gateway.example:8080/v1",
            Email = "user@example.com",
            Password = "legacy-secret",
            TrendDays = 7,
        })
        {
            Snapshot = CreateSnapshot(),
        };
        var viewModel = CreateManualViewModel(controller);
        await viewModel.InitializeAsync();

        bool succeeded = await viewModel.RefreshAsync(submittedPassword);

        Assert.True(succeeded);
        Assert.Equal(StatisticsScope.Cloud, viewModel.SelectedScope);
        Assert.Equal("本机后台", viewModel.PreferredDataSourceLabel);
        Assert.NotNull(controller.SavedSettings);
        Assert.Equal(string.Empty, controller.SavedSettings!.Password);
        Assert.Equal(submittedPassword, controller.RefreshSettings!.Password);
        Assert.Equal("http://gateway.example:8080", controller.SavedSettings.GatewayBaseUrl);
        Assert.Equal("12.35万", viewModel.TotalRequests);
        Assert.Equal("987.65万", viewModel.TotalTokens);
        Assert.Equal("$45.60", viewModel.TotalCost);
        ModelStatsRowViewModel model = Assert.Single(viewModel.Models);
        Assert.Equal("gpt-5.4", model.Model);
        Assert.Equal("$1.20", model.Cost);
        Assert.Equal(2, viewModel.Trend.Count);
        Assert.Equal("$1.10", viewModel.Trend[0].Cost);
        Assert.Contains("2026-07-13", viewModel.RecentTrendText, StringComparison.Ordinal);
        Assert.Contains("$3.30 官方费用", viewModel.TrendSummary, StringComparison.Ordinal);
        Assert.Contains("$45.60 官方费用", viewModel.CloudLifetimeSummary, StringComparison.Ordinal);
        Assert.True(viewModel.HasData);
        Assert.False(viewModel.HasFailure);
    }

    [Fact]
    public async Task RefreshAsync_CloudDashboardUsesTheSelectedRangeForCardsAndTrend()
    {
        var range = new UsageRangeOverview
        {
            TotalRequests = 9,
            TotalInputTokens = 400,
            TotalOutputTokens = 300,
            TotalCacheReadTokens = 200,
            TotalCacheCreationTokens = 100,
            TotalTokens = 1_000,
            TotalCost = 4.25,
            TotalActualCost = 3.5,
            AverageDurationMs = 876,
        };
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "https://gateway.example",
            Email = "user@example.com",
            TrendDays = 30,
        })
        {
            Snapshot = CreateSnapshot() with
            {
                RangeOverview = range,
                Range = new CloudUsageDateRange(
                    new DateOnly(2026, 6, 15),
                    new DateOnly(2026, 7, 14),
                    "Asia/Shanghai"),
            },
        };
        var viewModel = CreateManualViewModel(controller);

        bool succeeded = await viewModel.RefreshAsync("one-time-secret");

        Assert.True(succeeded);
        Assert.Equal(30, viewModel.SelectedTrendDays);
        Assert.Equal("9", viewModel.CloudRangeRequests);
        Assert.Equal("1,000", viewModel.CloudRangeTokens);
        Assert.Equal("$4.25", viewModel.CloudRangeActualCost);
        Assert.Equal("0.9s", viewModel.CloudRangeAverageLatency);
        Assert.Equal("400", viewModel.CloudRangeInputTokens);
        Assert.Equal("300", viewModel.CloudRangeOutputTokens);
        Assert.Equal("200", viewModel.CloudRangeCacheReadTokens);
        Assert.Equal("28.6%", viewModel.CloudRangeCacheHitRate);
        Assert.Contains("历史累计", viewModel.CloudLifetimeSummary, StringComparison.Ordinal);
        Assert.Equal(2, viewModel.CloudTrend.Count);
        Assert.Equal(["7/12", "7/13"], viewModel.CloudTrend.Select(point => point.Label));
    }

    [Fact]
    public async Task RefreshAsync_TodayTrendUsesTwentyFourHourlyBuckets()
    {
        DateOnly today = DateOnly.FromDateTime(DateTime.Now);
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "https://gateway.example",
            Email = "user@example.com",
            TrendDays = 1,
        })
        {
            Snapshot = CreateSnapshot() with
            {
                Trend =
                [
                    new TrendPoint
                    {
                        Date = $"{today:yyyy-MM-dd} 03:00",
                        Requests = 2,
                        TotalTokens = 1_200,
                    },
                    new TrendPoint
                    {
                        Date = $"{today:yyyy-MM-dd} 14:00",
                        Requests = 5,
                        TotalTokens = 8_800,
                    },
                ],
                Range = new CloudUsageDateRange(today, today, "Asia/Shanghai"),
            },
        };
        var viewModel = CreateManualViewModel(controller);

        bool succeeded = await viewModel.RefreshAsync("one-time-secret");

        Assert.True(succeeded);
        Assert.Equal(24, viewModel.CloudTrend.Count);
        Assert.Equal("00:00", viewModel.CloudTrend[0].Label);
        Assert.Equal("03:00", viewModel.CloudTrend[3].Label);
        Assert.Equal(1_200d, viewModel.CloudTrend[3].Value);
        Assert.Equal("14:00", viewModel.CloudTrend[14].Label);
        Assert.Equal(8_800d, viewModel.CloudTrend[14].Value);
        Assert.Equal("23:00", viewModel.CloudTrend[23].Label);
        Assert.Equal(0d, viewModel.CloudTrend[23].Value);
    }

    [Fact]
    public async Task RangeSelection_UsesOnlyTodaySevenAndThirtyDayDashboardWindows()
    {
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "https://gateway.example",
            Email = "user@example.com",
            TrendDays = 7,
        });
        var viewModel = CreateManualViewModel(controller);
        await viewModel.InitializeAsync();

        await viewModel.SelectThirtyDayRangeCommand.ExecuteAsync(null);
        Assert.Equal(30, viewModel.SelectedTrendDays);
        Assert.True(viewModel.IsThirtyDayRangeSelected);
        Assert.Equal("近 30 天", viewModel.DashboardRangeLabel);
        Assert.Equal(30, controller.SavedSettings!.TrendDays);

        await viewModel.SelectTodayRangeCommand.ExecuteAsync(null);
        Assert.Equal(1, viewModel.SelectedTrendDays);
        Assert.True(viewModel.IsTodayRangeSelected);
        Assert.Equal("今天", viewModel.DashboardRangeLabel);
        Assert.Equal(1, controller.SavedSettings!.TrendDays);
    }

    [Fact]
    public async Task RefreshAsync_FailureNeverEchoesSubmittedPassword()
    {
        const string submittedPassword = "new-super-secret";
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "http://127.0.0.1:8080",
            Email = "user@example.com",
            Password = string.Empty,
            TrendDays = 7,
        })
        {
            RefreshError = new InvalidOperationException(
                $"password={submittedPassword} login rejected"),
        };
        var viewModel = CreateManualViewModel(controller);

        bool succeeded = await viewModel.RefreshAsync(submittedPassword);

        Assert.False(succeeded);
        Assert.True(viewModel.HasFailure);
        Assert.Equal(StatisticsScope.Cloud, viewModel.SelectedScope);
        Assert.Equal("本机后台", viewModel.PreferredDataSourceLabel);
        Assert.Equal(string.Empty, controller.SavedSettings!.Password);
        Assert.DoesNotContain(submittedPassword, viewModel.StatusNotice, StringComparison.Ordinal);
        Assert.Contains("password=<已隐藏>", viewModel.StatusNotice, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task RefreshAsync_BackendFailureStaysOnLocalSub2ApiDashboard()
    {
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "https://gateway.example",
            Email = "user@example.com",
            TrendDays = 7,
        });
        var viewModel = CreateManualViewModel(controller);

        Assert.True(await viewModel.RefreshAsync("one-time-secret"));
        Assert.Equal(StatisticsScope.Cloud, viewModel.SelectedScope);

        controller.RefreshError = new HttpRequestException("gateway unavailable");

        Assert.False(await viewModel.RefreshAsync("one-time-secret"));
        Assert.Equal(StatisticsScope.Cloud, viewModel.SelectedScope);
        Assert.Equal("本机后台", viewModel.PreferredDataSourceLabel);
        Assert.DoesNotContain("自动使用本机记录", viewModel.PreferredDataSourceDetail, StringComparison.Ordinal);
    }

    [Fact]
    public async Task RefreshAsync_RejectsConcurrentSubmissionUntilCurrentRefreshCompletes()
    {
        var completion = new TaskCompletionSource<StatsSnapshot>(
            TaskCreationOptions.RunContinuationsAsynchronously);
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "http://gateway.example:8080",
            Email = "user@example.com",
            Password = string.Empty,
            TrendDays = 7,
        })
        {
            RefreshHandler = (_, _) => completion.Task,
        };
        var viewModel = CreateManualViewModel(controller);
        await viewModel.InitializeAsync();

        Task<bool> first = viewModel.RefreshAsync("first-secret");

        Assert.True(viewModel.IsBusy);
        Assert.False(viewModel.CanRefresh);
        Assert.False(await viewModel.RefreshAsync("ignored"));
        Assert.Equal(1, controller.RefreshCalls);

        completion.SetResult(CreateSnapshot());
        Assert.True(await first);
        Assert.False(viewModel.IsBusy);
        Assert.False(viewModel.CanRefresh);
    }

    [Fact]
    public void ApplyLocalTelemetrySnapshot_BuildsMetricCurvesWithoutUsingNetworkStatus()
    {
        var viewModel = CreateManualViewModel(new StubStatsController(new StatsSettings()));
        DateTimeOffset now = DateTimeOffset.UtcNow;
        var today = new LocalTelemetryUsageSummary(
            RequestCount: 2,
            SuccessfulRequestCount: 1,
            FailedRequestCount: 1,
            InputTokens: 40,
            OutputTokens: 20,
            CachedInputTokens: 5,
            SuccessRatePercent: 50,
            AverageLatencyMilliseconds: 120);
        var sevenDays = new LocalTelemetryUsageSummary(
            RequestCount: 3,
            SuccessfulRequestCount: 2,
            FailedRequestCount: 1,
            InputTokens: 70,
            OutputTokens: 50,
            CachedInputTokens: 8,
            SuccessRatePercent: 66.6666667,
            AverageLatencyMilliseconds: 321.5);
        var snapshot = new LocalTelemetrySnapshot(
            now,
            today,
            sevenDays,
            [
                new LocalTelemetryDailyUsage(DateOnly.FromDateTime(now.UtcDateTime), sevenDays),
            ],
            new LocalNetworkHealthStatus(
                now,
                SourceId: "local-machine",
                SourceLabel: "本机中转",
                Succeeded: false,
                LatencyMilliseconds: 1_100),
            [
                new LocalTelemetryUsageBreakdown("local-machine", "本机中转", null, null, sevenDays),
            ],
            [
                new LocalTelemetryUsageBreakdown(null, null, CliKind.Codex, null, sevenDays),
            ],
            [
                new LocalTelemetryUsageBreakdown(null, null, null, "gpt-5", sevenDays),
            ],
            [
                new LocalTelemetryRecentActivity(now, CliKind.Codex, "local-machine", "本机中转", "gpt-5", true, 40, 20, 5, 120),
            ],
            [
                new LocalTelemetryHourlyUsage(now, sevenDays),
            ]);

        viewModel.ApplyLocalTelemetrySnapshot(snapshot);

        Assert.Equal(StatisticsScope.Cloud, viewModel.SelectedScope);
        Assert.False(viewModel.IsLocalStatisticsSelected);
        Assert.True(viewModel.IsCloudStatisticsSelected);
        Assert.True(viewModel.HasLocalTelemetry);
        Assert.Equal("65", viewModel.LocalTodayTokens);
        Assert.Equal("128", viewModel.LocalSevenDayTokens);
        Assert.Equal("66.7%", viewModel.LocalSuccessRate);
        Assert.Equal("322 ms", viewModel.LocalAverageLatency);
        Assert.Equal("尚未检测", viewModel.LocalNetworkStatus);
        Assert.DoesNotContain("1,100", viewModel.LocalNetworkDetail, StringComparison.Ordinal);
        Assert.Single(viewModel.LocalTrend);
        Assert.Single(viewModel.LocalRequestTrend);
        Assert.Single(viewModel.LocalInputTokenTrend);
        Assert.Single(viewModel.LocalOutputTokenTrend);
        Assert.Single(viewModel.LocalCacheReadTrend);
        Assert.Single(viewModel.LocalCacheWriteTrend);
        Assert.Single(viewModel.LocalSuccessRateTrend);
        Assert.Single(viewModel.LocalLatencyTrend);
        Assert.Single(viewModel.LocalCacheHitRateTrend);
        Assert.Single(viewModel.LocalSources);
        Assert.Single(viewModel.LocalCliBreakdowns);
        Assert.Single(viewModel.LocalModels);
        Assert.Single(viewModel.LocalRecentActivity);
        Assert.Single(viewModel.LocalHourlyTrend);
    }

    [Fact]
    public void ApplyLocalTelemetrySnapshot_PreservesAllSevenCalendarDaysForTheLineChart()
    {
        var viewModel = CreateManualViewModel(new StubStatsController(new StatsSettings()));
        DateOnly firstDay = new(2026, 7, 8);
        var activeUsage = new LocalTelemetryUsageSummary(
            RequestCount: 2,
            SuccessfulRequestCount: 2,
            FailedRequestCount: 0,
            InputTokens: 9,
            OutputTokens: 11,
            CachedInputTokens: 4,
            SuccessRatePercent: 100,
            AverageLatencyMilliseconds: 80)
        {
            CacheCreationTokens = 3,
        };
        LocalTelemetryDailyUsage[] dailyUsage = Enumerable.Range(0, 7)
            .Select(index => new LocalTelemetryDailyUsage(
                firstDay.AddDays(index),
                index == 3 ? activeUsage : LocalTelemetryUsageSummary.Empty))
            .ToArray();
        var snapshot = new LocalTelemetrySnapshot(
            new DateTimeOffset(2026, 7, 14, 12, 0, 0, TimeSpan.Zero),
            activeUsage,
            activeUsage,
            dailyUsage,
            null);

        viewModel.ApplyLocalTelemetrySnapshot(snapshot);

        Assert.True(viewModel.HasLocalTelemetry);
        Assert.Equal(7, viewModel.LocalTrend.Count);
        Assert.Equal(
            [0d, 0d, 0d, 27d, 0d, 0d, 0d],
            viewModel.LocalTrend.Select(point => point.Value));
        Assert.Equal(
            Enumerable.Range(0, 7).Select(index =>
                firstDay.AddDays(index).ToString("M/d", System.Globalization.CultureInfo.CurrentCulture)),
            viewModel.LocalTrend.Select(point => point.Label));
        Assert.Equal("2 次请求 · 27 已记录 Token", viewModel.LocalTrend[3].Detail);
    }

    [Fact]
    public void ApplyLocalTelemetryRangeSnapshot_TodayUsesHourlyBucketsForEveryMetricCurve()
    {
        var viewModel = CreateManualViewModel(new StubStatsController(new StatsSettings()));
        DateTimeOffset firstHour = new(2026, 7, 20, 8, 0, 0, TimeZoneInfo.Local.GetUtcOffset(new DateTime(2026, 7, 20)));
        LocalTelemetryHourlyUsage[] hours = Enumerable.Range(0, 3)
            .Select(index => new LocalTelemetryHourlyUsage(
                firstHour.AddHours(index),
                new LocalTelemetryUsageSummary(
                    RequestCount: index + 1,
                    SuccessfulRequestCount: index + 1,
                    FailedRequestCount: 0,
                    InputTokens: 10 * (index + 1),
                    OutputTokens: 5 * (index + 1),
                    CachedInputTokens: 2 * index,
                    SuccessRatePercent: 100,
                    AverageLatencyMilliseconds: 80 + index)))
            .ToArray();
        LocalTelemetryUsageSummary total = new(
            RequestCount: 6,
            SuccessfulRequestCount: 6,
            FailedRequestCount: 0,
            InputTokens: 60,
            OutputTokens: 30,
            CachedInputTokens: 6,
            SuccessRatePercent: 100,
            AverageLatencyMilliseconds: 81);
        var snapshot = new LocalTelemetryRangeSnapshot(
            generatedAt: firstHour.AddHours(3),
            days: 1,
            usage: total,
            dailyUsage: [new LocalTelemetryDailyUsage(DateOnly.FromDateTime(firstHour.LocalDateTime), total)],
            latestNetworkStatus: null,
            bySource: [],
            byCli: [],
            byModel: [],
            recentActivity: [],
            recentHourlyUsage: hours);

        viewModel.ApplyLocalTelemetryRangeSnapshot(snapshot);

        Assert.Equal(3, viewModel.LocalTrend.Count);
        Assert.Equal([1d, 2d, 3d], viewModel.LocalRequestTrend.Select(point => point.Value));
        Assert.Equal([10d, 20d, 30d], viewModel.LocalInputTokenTrend.Select(point => point.Value));
        Assert.Equal([80d, 81d, 82d], viewModel.LocalLatencyTrend.Select(point => point.Value));
    }

    [Fact]
    public void ApplyConnections_PopulatesAllConfiguredSourcesAndSelectsTheActiveSource()
    {
        var viewModel = CreateManualViewModel(new StubStatsController(new StatsSettings()));
        ConnectionProfile[] connections =
        [
            new ConnectionProfile
            {
                Id = ConnectionProfileIds.LocalMachine,
                Name = "本机中转",
                Kind = ConnectionProfileKind.Local,
                BaseUrl = "http://127.0.0.1:8080/v1",
            },
            new ConnectionProfile
            {
                Id = ConnectionProfileIds.LanDefault,
                Name = "局域网中转",
                Kind = ConnectionProfileKind.Lan,
                BaseUrl = "http://192.168.31.8:8080/v1",
            },
            new ConnectionProfile
            {
                Id = "remote-team",
                Name = "团队云端",
                Kind = ConnectionProfileKind.Cloud,
                BaseUrl = "https://api.example.test/v1",
            },
        ];

        viewModel.ApplyConnections(
            connections,
            new ConnectionProfileSelection(
                CloudProfileId: "remote-team",
                LocalProfileId: ConnectionProfileIds.LocalMachine,
                ActiveProfileId: "remote-team"));

        Assert.Equal(4, viewModel.LocalSourceFilters.Count);
        Assert.Equal("全部来源", viewModel.LocalSourceFilters[0].DisplayLabel);
        Assert.Contains(viewModel.LocalSourceFilters, option =>
            option.SourceId == ConnectionProfileIds.LocalMachine && option.Label == "本机中转");
        Assert.Contains(viewModel.LocalSourceFilters, option =>
            option.SourceId == ConnectionProfileIds.LanDefault && option.Label == "局域网中转");
        Assert.Contains(viewModel.LocalSourceFilters, option =>
            option.SourceId == "remote-team" && option.DisplayLabel == "团队云端（当前）");
        Assert.Equal("remote-team", viewModel.SelectedLocalSourceFilter?.SourceId);
        Assert.Equal("当前后台：本机中转", viewModel.BackendSourceLabel);
    }

    [Fact]
    public async Task InitializeAsync_SelectedCloudSourceUsesConfiguredLocalBackendWithoutProbeDependency()
    {
        var controller = new StubStatsController(new StatsSettings());
        var viewModel = new StatsViewModel(
            controller,
            localCloudStatisticsClient: new StubLocalCloudStatisticsClient(),
            localGatewayAuthorizationStore: new StubAdministratorAuthorizationStore(),
            localUserStatsAuthorizationStore: new StubUserAuthorizationStore(),
            localGatewayEndpointResolver: new StubLocalGatewayEndpointResolver(
                LocalGatewayEndpointResolution.ManualCloudOnly));
        ConnectionProfile[] connections =
        [
            new ConnectionProfile
            {
                Id = ConnectionProfileIds.LocalMachine,
                Name = "本机中转",
                Kind = ConnectionProfileKind.Local,
                BaseUrl = "http://127.0.0.1:8080/v1",
            },
            new ConnectionProfile
            {
                Id = "cloud-a",
                Name = "云端 A",
                Kind = ConnectionProfileKind.Cloud,
                BaseUrl = "https://relay.example.test/v1",
            },
        ];
        viewModel.ApplyConnections(
            connections,
            new ConnectionProfileSelection("cloud-a", ConnectionProfileIds.LocalMachine, "cloud-a"));

        await viewModel.InitializeAsync();

        Assert.True(viewModel.IsLocalGatewayAvailable);
        Assert.Equal("当前后台：本机中转", viewModel.BackendSourceLabel);
        Assert.True(viewModel.RequiresLocalAuthorization);
        Assert.Contains("本机后台", viewModel.CloudConnectionNotice, StringComparison.Ordinal);
    }

    [Fact]
    public async Task InitializeAsync_SelectedPublicHttpCloudSourceDoesNotProbeAnyEndpoint()
    {
        var viewModel = new StatsViewModel(
            new StubStatsController(new StatsSettings()),
            localCloudStatisticsClient: new StubLocalCloudStatisticsClient(),
            localGatewayAuthorizationStore: new StubAdministratorAuthorizationStore(),
            localUserStatsAuthorizationStore: new StubUserAuthorizationStore(),
            localGatewayEndpointResolver: new StubLocalGatewayEndpointResolver(
                LocalGatewayEndpointResolution.ManualCloudOnly));
        ConnectionProfile[] connections =
        [
            new ConnectionProfile
            {
                Id = "cloud-http",
                Name = "公网 HTTP",
                Kind = ConnectionProfileKind.Cloud,
                BaseUrl = "http://relay.example.test/v1",
            },
        ];
        viewModel.ApplyConnections(
            connections,
            new ConnectionProfileSelection("cloud-http", null, "cloud-http"));

        await viewModel.InitializeAsync();

        Assert.False(viewModel.IsLocalGatewayAvailable);
        Assert.False(viewModel.ShowManualCloudCredentialForm);
        Assert.Equal("当前后台：本机中转", viewModel.BackendSourceLabel);
    }

    [Fact]
    public async Task InitializeAsync_LocalGatewayOnlineWithoutAuthorization_HidesManualCredentialsAndRequestsOneTimeAuthorization()
    {
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "https://remote.example",
            Email = "remote@example.com",
            TrendDays = 7,
        });
        var viewModel = new StatsViewModel(
            controller,
            localCloudStatisticsClient: new StubLocalCloudStatisticsClient(),
            localGatewayAuthorizationStore: new StubAdministratorAuthorizationStore(),
            localUserStatsAuthorizationStore: new StubUserAuthorizationStore(),
            localGatewayEndpointResolver: StubLocalGatewayEndpointResolver.Ready());

        await viewModel.InitializeAsync();

        Assert.True(viewModel.IsLocalGatewayAvailable);
        Assert.False(viewModel.ShowManualCloudCredentialForm);
        Assert.True(viewModel.RequiresLocalAuthorization);
        Assert.False(viewModel.CanRefresh);
        Assert.Equal(string.Empty, viewModel.GatewayBaseUrl);
        Assert.Equal(0, controller.RefreshCalls);
        Assert.Contains("不会", viewModel.CloudAuthorizationNotice, StringComparison.Ordinal);
    }

    [Fact]
    public async Task InitializeAsync_InvalidConnectionCenterLocalProfile_DoesNotRedirectDashboardAwayFromLocalhost()
    {
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "https://remote.example",
            Email = "remote@example.com",
            TrendDays = 7,
        });
        var viewModel = new StatsViewModel(
            controller,
            localCloudStatisticsClient: new StubLocalCloudStatisticsClient(),
            localGatewayAuthorizationStore: new StubAdministratorAuthorizationStore(),
            localUserStatsAuthorizationStore: new StubUserAuthorizationStore(),
            localGatewayEndpointResolver: new StubLocalGatewayEndpointResolver(
                LocalGatewayEndpointResolution.ApiAddressNotLocal));

        await viewModel.InitializeAsync();

        Assert.False(viewModel.HasLocalGatewayConfigurationIssue);
        Assert.False(viewModel.ShowManualCloudCredentialForm);
        Assert.False(viewModel.CanRefresh);
        Assert.Equal("当前后台：本机中转", viewModel.BackendSourceLabel);
        Assert.Contains("不会改用远程来源", viewModel.CloudConnectionNotice, StringComparison.Ordinal);
    }

    [Fact]
    public async Task InitializeAsync_LocalAdministratorCredential_AutoReadsAdminDashboardWithoutAccountPassword()
    {
        var controller = new StubStatsController(new StatsSettings
        {
            GatewayBaseUrl = "https://remote.example",
            Email = "remote@example.com",
            Password = "legacy-plaintext",
            TrendDays = 14,
        });
        var client = new StubLocalCloudStatisticsClient
        {
            AdministratorSnapshot = CreateSnapshot() with { Scope = CloudStatisticsScope.LocalAdministrator },
        };
        var endpointResolver = StubLocalGatewayEndpointResolver.Ready("http://127.0.0.1:18080/");
        var viewModel = new StatsViewModel(
            controller,
            localCloudStatisticsClient: client,
            localGatewayAuthorizationStore: new StubAdministratorAuthorizationStore("admin-key-never-shown"),
            localUserStatsAuthorizationStore: new StubUserAuthorizationStore(),
            localGatewayEndpointResolver: endpointResolver);

        await viewModel.InitializeAsync();

        Assert.True(viewModel.HasLocalAdministratorAuthorization);
        Assert.True(viewModel.CanRefresh);
        Assert.Equal(1, client.AdministratorRefreshCalls);
        Assert.Equal(StatisticsScope.Cloud, viewModel.SelectedScope);
        Assert.Equal("本机后台", viewModel.PreferredDataSourceLabel);
        Assert.Equal("http://127.0.0.1:8080/", client.LastAdministratorGatewayBaseUrl);
        Assert.Equal(0, controller.RefreshCalls);
        Assert.Contains("管理员全站聚合", viewModel.CloudDataScope, StringComparison.Ordinal);
        Assert.DoesNotContain("admin-key-never-shown", viewModel.StatusNotice, StringComparison.Ordinal);
        Assert.Equal(string.Empty, controller.SavedSettings!.Password);
    }

    [Fact]
    public async Task LocalUserDashboard_UsesExplicitDateRangeForModelsAndTrend()
    {
        var handler = new RecordingStatisticsHandler();
        using var httpClient = new HttpClient(handler)
        {
            Timeout = TimeSpan.FromSeconds(3),
        };
        var client = new LocalCloudStatisticsClient(httpClient);

        await client.AuthorizeUserAsync(
            "http://127.0.0.1:8080",
            "user@example.com",
            "one-time-password",
            trendDays: 30,
            CancellationToken.None);

        Uri trend = Assert.Single(handler.Requests, uri => uri.AbsolutePath.EndsWith("/usage/dashboard/trend", StringComparison.Ordinal));
        Uri models = Assert.Single(handler.Requests, uri => uri.AbsolutePath.EndsWith("/usage/dashboard/models", StringComparison.Ordinal));
        Assert.Contains("start_date=", trend.Query, StringComparison.Ordinal);
        Assert.Contains("end_date=", trend.Query, StringComparison.Ordinal);
        Assert.Contains("granularity=day", trend.Query, StringComparison.Ordinal);
        Assert.DoesNotContain("days=", trend.Query, StringComparison.Ordinal);
        Assert.Contains("start_date=", models.Query, StringComparison.Ordinal);
        Assert.Contains("end_date=", models.Query, StringComparison.Ordinal);
    }

    [Fact]
    public async Task LocalUserDashboard_UsesHourlyTrendForToday()
    {
        var handler = new RecordingStatisticsHandler();
        using var httpClient = new HttpClient(handler)
        {
            Timeout = TimeSpan.FromSeconds(3),
        };
        var client = new LocalCloudStatisticsClient(httpClient);

        await client.AuthorizeUserAsync(
            "http://127.0.0.1:8080",
            "user@example.com",
            "one-time-password",
            trendDays: 1,
            CancellationToken.None);

        Uri trend = Assert.Single(
            handler.Requests,
            uri => uri.AbsolutePath.EndsWith("/usage/dashboard/trend", StringComparison.Ordinal));
        Uri models = Assert.Single(
            handler.Requests,
            uri => uri.AbsolutePath.EndsWith("/usage/dashboard/models", StringComparison.Ordinal));
        Assert.Contains("granularity=hour", trend.Query, StringComparison.Ordinal);
        Assert.Contains("granularity=day", models.Query, StringComparison.Ordinal);
    }

    [Fact]
    public async Task LocalCloudStatisticsClient_AllowsPublicHttpSelectedSource()
    {
        var handler = new RecordingStatisticsHandler();
        using var httpClient = new HttpClient(handler) { Timeout = TimeSpan.FromSeconds(3) };
        var client = new LocalCloudStatisticsClient(httpClient);

        await client.RefreshWithAccessTokenAsync(
            "http://relay.example.test/v1",
            "access-token",
            trendDays: 7,
            administrator: false,
            CancellationToken.None);

        Assert.Contains(handler.Requests, uri =>
            uri.AbsoluteUri.StartsWith("http://relay.example.test/api/v1/usage/stats", StringComparison.Ordinal));
    }

    [Fact]
    public async Task LocalCloudStatisticsClient_AllowsVerifiedLocalCustomPort()
    {
        var handler = new RecordingStatisticsHandler();
        using var httpClient = new HttpClient(handler)
        {
            Timeout = TimeSpan.FromSeconds(3),
        };
        var client = new LocalCloudStatisticsClient(httpClient);

        await client.AuthorizeUserAsync(
            "http://localhost:18080/v1",
            "user@example.com",
            "one-time-password",
            trendDays: 7,
            CancellationToken.None);

        Assert.NotEmpty(handler.Requests);
        Assert.All(handler.Requests, request =>
        {
            Assert.Equal("localhost", request.Host, StringComparer.OrdinalIgnoreCase);
            Assert.Equal(18080, request.Port);
        });
    }

    private static StatsSnapshot CreateSnapshot()
        => new(
            new StatsOverview
            {
                TotalApiKeys = 3,
                ActiveApiKeys = 2,
                TotalRequests = 123_456,
                TodayRequests = 456,
                TotalTokens = 9_876_543,
                TodayTokens = 87_654,
                TotalCost = 45.6,
                TotalActualCost = 40.5,
                TodayCost = 1.25,
                TotalCacheReadTokens = 222_222,
                AverageDurationMs = 2345,
                Rpm = 1.5,
                Tpm = 3210,
            },
            [
                new ModelStat
                {
                    Model = "gpt-5.4",
                    Requests = 10,
                    InputTokens = 100,
                    OutputTokens = 50,
                    TotalTokens = 150,
                    Cost = 1.2,
                    ActualCost = 1.1,
                },
            ],
            [
                new TrendPoint
                {
                    Date = "2026-07-12",
                    Requests = 100,
                    TotalTokens = 10_000,
                    Cost = 1.1,
                    ActualCost = 1.0,
                },
                new TrendPoint
                {
                    Date = "2026-07-13",
                    Requests = 200,
                    TotalTokens = 20_000,
                    Cost = 2.2,
                    ActualCost = 2.0,
                },
            ]);

    private static StatsViewModel CreateManualViewModel(StubStatsController controller)
        => new(
            controller,
            localCloudStatisticsClient: new StubLocalCloudStatisticsClient(),
            localGatewayAuthorizationStore: new StubAdministratorAuthorizationStore(),
            localUserStatsAuthorizationStore: new StubUserAuthorizationStore());

    private sealed class StubStatsController : IStatsController
    {
        private readonly StatsSettings _settings;

        public StubStatsController(StatsSettings settings)
        {
            _settings = Clone(settings);
            Snapshot = CreateSnapshot();
        }

        public StatsSnapshot Snapshot { get; set; }

        public Exception? RefreshError { get; set; }

        public Func<StatsSettings, CancellationToken, Task<StatsSnapshot>>? RefreshHandler { get; init; }

        public StatsSettings? SavedSettings { get; private set; }

        public StatsSettings? RefreshSettings { get; private set; }

        public int RefreshCalls { get; private set; }

        public Task<StatsSettings> LoadSettingsAsync(CancellationToken cancellationToken)
            => Task.FromResult(Clone(_settings));

        public Task SaveSettingsAsync(StatsSettings settings, CancellationToken cancellationToken)
        {
            SavedSettings = Clone(settings);
            return Task.CompletedTask;
        }

        public Task<StatsSnapshot> RefreshAsync(
            StatsSettings settings,
            CancellationToken cancellationToken)
        {
            RefreshCalls++;
            RefreshSettings = Clone(settings);
            if (RefreshError is not null)
            {
                return Task.FromException<StatsSnapshot>(RefreshError);
            }

            return RefreshHandler?.Invoke(Clone(settings), cancellationToken)
                   ?? Task.FromResult(Snapshot);
        }

        private static StatsSettings Clone(StatsSettings settings)
            => new()
            {
                GatewayBaseUrl = settings.GatewayBaseUrl,
                Email = settings.Email,
                Password = settings.Password,
                TrendDays = settings.TrendDays,
            };
    }

    private sealed class StubLocalGatewayEndpointResolver(LocalGatewayEndpointResolution resolution) : ILocalGatewayEndpointResolver
    {
        public static StubLocalGatewayEndpointResolver Ready(
            string apiBaseUrl = "http://127.0.0.1:8080/",
            string? dashboardUrl = "http://127.0.0.1:8080/dashboard")
            => new(new LocalGatewayEndpointResolution(
                LocalGatewayEndpointResolutionStatus.Ready,
                new Uri(apiBaseUrl),
                dashboardUrl is null ? null : new Uri(dashboardUrl)));

        public Task<LocalGatewayEndpointResolution> ResolveAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            return Task.FromResult(resolution);
        }
    }

    private sealed class StubLocalCloudStatisticsClient : ILocalCloudStatisticsClient
    {
        public StatsSnapshot AdministratorSnapshot { get; set; } = CreateSnapshot() with
        {
            Scope = CloudStatisticsScope.LocalAdministrator,
        };

        public int AdministratorRefreshCalls { get; private set; }

        public string? LastAdministratorGatewayBaseUrl { get; private set; }

        public Task<StatsSnapshot> RefreshAdministratorAsync(
            string gatewayBaseUrl,
            string administratorApiKey,
            int trendDays,
            CancellationToken cancellationToken)
        {
            AdministratorRefreshCalls++;
            LastAdministratorGatewayBaseUrl = gatewayBaseUrl;
            return Task.FromResult(AdministratorSnapshot);
        }

        public Task<LocalUserAuthorizationResult> AuthorizeUserAsync(
            string gatewayBaseUrl,
            string email,
            string password,
            int trendDays,
            CancellationToken cancellationToken)
            => Task.FromResult(new LocalUserAuthorizationResult(
                CreateSnapshot() with { Scope = CloudStatisticsScope.LocalUser },
                "refresh-token"));

        public Task<LocalUserRefreshResult> RefreshUserAsync(
            string gatewayBaseUrl,
            string refreshToken,
            int trendDays,
            CancellationToken cancellationToken)
            => Task.FromResult(new LocalUserRefreshResult(
                CreateSnapshot() with { Scope = CloudStatisticsScope.LocalUser },
                "rotated-refresh-token"));

        public Task<StatsSnapshot> RefreshWithAccessTokenAsync(
            string gatewayBaseUrl,
            string accessToken,
            int trendDays,
            bool administrator,
            CancellationToken cancellationToken)
            => Task.FromResult(administrator
                ? AdministratorSnapshot
                : CreateSnapshot() with { Scope = CloudStatisticsScope.LocalUser });
    }

    private sealed class StubAdministratorAuthorizationStore : ILocalGatewayAuthorizationStore
    {
        private LocalGatewayAuthorization _authorization;

        public StubAdministratorAuthorizationStore(string? administratorApiKey = null)
        {
            _authorization = string.IsNullOrWhiteSpace(administratorApiKey)
                ? LocalGatewayAuthorization.Unavailable
                : LocalGatewayAuthorization.Available(
                    administratorApiKey,
                    LocalGatewayAuthorizationSource.WindowsCredentialManager);
        }

        public LocalGatewayAuthorization GetCurrentAuthorization() => _authorization;

        public LocalGatewayAuthorizationSaveResult SaveAdministratorApiKey(string administratorApiKey)
        {
            if (string.IsNullOrWhiteSpace(administratorApiKey))
            {
                return LocalGatewayAuthorizationSaveResult.Invalid;
            }

            _authorization = LocalGatewayAuthorization.Available(
                administratorApiKey,
                LocalGatewayAuthorizationSource.WindowsCredentialManager);
            return LocalGatewayAuthorizationSaveResult.Saved;
        }

        public bool ClearSavedAuthorization()
        {
            bool wasAvailable = _authorization.IsAvailable;
            _authorization = LocalGatewayAuthorization.Unavailable;
            return wasAvailable;
        }
    }

    private sealed class StubUserAuthorizationStore : ILocalUserStatsAuthorizationStore
    {
        private LocalUserStatsAuthorization _authorization = LocalUserStatsAuthorization.Unavailable;

        public LocalUserStatsAuthorization GetCurrent() => _authorization;

        public LocalUserStatsAuthorizationSaveResult Save(string refreshToken)
        {
            if (string.IsNullOrWhiteSpace(refreshToken))
            {
                return LocalUserStatsAuthorizationSaveResult.Invalid;
            }

            _authorization = LocalUserStatsAuthorization.Available(refreshToken);
            return LocalUserStatsAuthorizationSaveResult.Saved;
        }

        public bool Clear()
        {
            bool wasAvailable = _authorization.IsAvailable;
            _authorization = LocalUserStatsAuthorization.Unavailable;
            return wasAvailable;
        }
    }

    private sealed class RecordingStatisticsHandler : HttpMessageHandler
    {
        public List<Uri> Requests { get; } = [];

        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            Requests.Add(request.RequestUri!);
            string path = request.RequestUri!.AbsolutePath;
            string body = path.EndsWith("/auth/login", StringComparison.Ordinal)
                ? "{\"code\":0,\"data\":{\"access_token\":\"access\",\"refresh_token\":\"refresh\"}}"
                : path.EndsWith("/models", StringComparison.Ordinal)
                    ? "{\"code\":0,\"data\":{\"models\":[]}}"
                    : path.EndsWith("/trend", StringComparison.Ordinal)
                        ? "{\"code\":0,\"data\":{\"trend\":[]}}"
                        : "{\"code\":0,\"data\":{}}";
            return Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent(body, Encoding.UTF8, "application/json"),
            });
        }
    }
}
