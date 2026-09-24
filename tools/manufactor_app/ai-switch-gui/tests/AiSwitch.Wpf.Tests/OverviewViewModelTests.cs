using System.Collections.ObjectModel;
using AiSwitchGui;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class OverviewViewModelTests
{
    [Fact]
    public void NetworkProbeInterval_OffersOnlyTwoToFiveMinutesAndNotifiesUserChanges()
    {
        int selectedMinutes = 0;
        var viewModel = new OverviewViewModel(
            new ObservableCollection<ProjectCardViewModel>(),
            localTelemetryRepository: null,
            sessionManager: null,
            cloudStatisticsClient: null,
            minutes => selectedMinutes = minutes);

        Assert.Equal([2, 3, 5], viewModel.NetworkProbeIntervals.Select(option => option.Minutes));
        Assert.Equal(3, viewModel.SelectedNetworkProbeInterval.Minutes);

        viewModel.SelectedNetworkProbeInterval = viewModel.NetworkProbeIntervals.Single(option => option.Minutes == 5);

        Assert.Equal(5, selectedMinutes);
        viewModel.SetNetworkProbeInterval(2);
        Assert.Equal(2, viewModel.SelectedNetworkProbeInterval.Minutes);
        Assert.Equal(5, selectedMinutes);
        Assert.Equal(3, MainWindowViewModel.NormalizeNetworkProbeInterval(99));
    }

    [Fact]
    public void OverviewView_RemovesTheLocalRelayProbePanel()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "Views", "OverviewView.xaml"));
        string xaml = File.ReadAllText(sourcePath);

        Assert.DoesNotContain("Text=\"连接延迟\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"最近探测\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"可用性 · 7 天\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"近 60 次记录\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("SelectedNetworkProbeInterval", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"采样\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"近 7 日输入\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"近 7 日输出\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"缓存读取\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"请求成功率\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Text=\"缓存命中率\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Text=\"{Binding CacheHitRate}\"", xaml, StringComparison.Ordinal);
    }

    [Fact]
    public void NavigationItems_FollowThePrimaryWorkflowOrder()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "ViewModels", "MainWindowViewModel.cs"));
        string source = File.ReadAllText(sourcePath);
        string[] orderedItems =
        [
            "new(\"overview\", \"工作台\"",
            "new(\"transit-center\", \"中转中心\"",
            "new(\"stats\", \"用量仪表盘\"",
            "new(\"projects\", \"项目中心\"",
            "new(\"extensions\", \"扩展中心\"",
            "new(\"settings\", \"设置\"",
        ];

        int previousIndex = -1;
        foreach (string item in orderedItems)
        {
            int index = source.IndexOf(item, previousIndex + 1, StringComparison.Ordinal);
            Assert.True(index > previousIndex, $"Navigation item is missing or out of order: {item}");
            previousIndex = index;
        }
    }

    [Fact]
    public void NetworkProbeTargets_ContainOnlyEnabledBackupsInConfiguredOrder()
    {
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        ConnectionProfile lan = CreateProfile(ConnectionProfileIds.LanDefault, "局域网中转", ConnectionProfileKind.Lan);
        ConnectionProfile first = CreateProfile("remote-a", "备用 A", ConnectionProfileKind.Cloud);
        ConnectionProfile disabled = CreateProfile("remote-b", "未启用", ConnectionProfileKind.Cloud);
        ConnectionProfile second = CreateProfile("remote-c", "备用 C", ConnectionProfileKind.Cloud);
        var snapshot = new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            [local, lan, first, disabled, second],
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection(null, ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine),
            new ConnectionProfileRouting(
                ConnectionProfileIds.LocalMachine,
                ConnectionProfileIds.LocalMachine,
                ConnectionProfileIds.LocalMachine,
                ConnectionProfileIds.LocalMachine,
                [ConnectionProfileIds.LanDefault, "remote-c", "remote-a"]));

        IReadOnlyList<ConnectionProfile> targets = MainWindowViewModel.FindBackupProbeProfiles(snapshot);

        Assert.Equal(["remote-c", "remote-a"], targets.Select(profile => profile.Id));
        Assert.DoesNotContain(targets, profile => profile.Kind == ConnectionProfileKind.Local);
    }

    [Fact]
    public void NetworkProbe_UsesSafeFallbackLabelWhenProfileNameIsAnAbsoluteUrl()
    {
        ConnectionProfile profile = CreateProfile(
            "remote-url-name",
            "https://icode-xtu.ccwu.cc",
            ConnectionProfileKind.Cloud);

        LocalNetworkHealthProbe probe = MainWindowViewModel.CreateNetworkHealthProbe(
            profile,
            succeeded: true,
            latencyMilliseconds: 24);

        Assert.Equal(profile.Id, probe.SourceId);
        Assert.Equal("备用上游", probe.SourceLabel);
        Assert.True(probe.Succeeded);
        Assert.Equal(24, probe.LatencyMilliseconds);
    }

    [Fact]
    public void NetworkProbeLoop_ReloadsRoutingAndQueuesAChangedBackupSet()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "ViewModels", "MainWindowViewModel.cs"));
        string source = File.ReadAllText(sourcePath);

        Assert.Contains("_networkProbeProfiles = await LoadBackupProbeProfilesAsync(cancellationToken)", source, StringComparison.Ordinal);
        Assert.Contains("QueueBackupConnectionsProbe();", source, StringComparison.Ordinal);
        Assert.Contains("while (Interlocked.Exchange(ref _networkProbeRequested, 0) != 0)", source, StringComparison.Ordinal);
        Assert.Contains("profiles = await LoadBackupProbeProfilesAsync(cancellationToken)", source, StringComparison.Ordinal);
        Assert.Contains("await _networkProbeGate.WaitAsync(cancellationToken)", source, StringComparison.Ordinal);
        Assert.Contains("await ProbeBackupConnectionAsync(profile, cancellationToken)", source, StringComparison.Ordinal);
        Assert.Contains("WriteNetworkProbeDiagnostic($\"probe-complete source={profile.Id}", source, StringComparison.Ordinal);
        Assert.DoesNotContain("_networkProbeGate.WaitAsync(0, cancellationToken)", source, StringComparison.Ordinal);
    }

    [Fact]
    public void ApplySourceMonitor_BuildsSub2ApiStyleAvailabilityAndSixtyPointTimeline()
    {
        var viewModel = new OverviewViewModel(new ObservableCollection<ProjectCardViewModel>());
        DateTimeOffset now = new(2026, 7, 16, 8, 30, 0, TimeSpan.Zero);
        LocalNetworkHealthProbe[] history =
        [
            new(now.AddMinutes(-2), "cloud-source", "云端来源", true, 120, "ok"),
            new(now.AddMinutes(-1), "cloud-source", "云端来源", false, null, "timeout"),
        ];
        var summary = new LocalNetworkHealthSummary(
            "cloud-source",
            "云端来源",
            10,
            8,
            80,
            120,
            900,
            now.AddMinutes(-2),
            "timeout");

        viewModel.ApplySourceMonitor(summary, history);

        Assert.Equal("80.00%", viewModel.NetworkAvailability);
        Assert.Equal("—", viewModel.NetworkLatency);
        Assert.Equal("已记录 2 次", viewModel.NetworkProbeCount);
        Assert.False(viewModel.IsNetworkHealthy);
        Assert.Equal("连接异常", viewModel.NetworkHealth);
        Assert.Contains("超时", viewModel.NetworkHealthDetail, StringComparison.Ordinal);
        Assert.Equal(60, viewModel.NetworkTimeline.Count);
        Assert.Equal(24d, viewModel.NetworkTimeline[^2].Height);
        Assert.Equal(10d, viewModel.NetworkTimeline[^1].Height);
    }

    private static ConnectionProfile CreateProfile(string id, string name, ConnectionProfileKind kind) => new()
    {
        Id = id,
        Name = name,
        Kind = kind,
        BaseUrl = "https://example.test",
        ClientBaseUrls = new Dictionary<CliKind, string> { [CliKind.Codex] = "https://example.test/v1" },
        EnabledClients = [CliKind.Codex],
    };

    [Fact]
    public void ApplyBackendUsageSnapshot_UsesOnlyLocalSub2ApiAndNeverFallsBackToLocalObservation()
    {
        var viewModel = new OverviewViewModel(new ObservableCollection<ProjectCardViewModel>());
        var cloud = new StatsSnapshot(
            new StatsOverview { TodayRequests = 8, TodayTokens = 800 },
            Array.Empty<ModelStat>(),
            [new TrendPoint { Date = "2026-07-16", Requests = 20, TotalTokens = 2400 }],
            CloudStatisticsScope.LocalUser,
            new UsageRangeOverview
            {
                TotalRequests = 20,
                TotalTokens = 2400,
                TotalInputTokens = 1800,
                TotalOutputTokens = 600,
                TotalCacheReadTokens = 300,
                AverageDurationMs = 750,
            });

        Assert.True(viewModel.ApplyBackendUsageSnapshot(cloud));
        Assert.Equal("800", viewModel.TodayTokens);
        Assert.Equal("2,400", viewModel.SevenDayTokens);
        Assert.Equal("本机后台", viewModel.UsageDataSourceLabel);
        Assert.Equal("本机用量趋势", viewModel.UsageTrendTitle);
        Assert.Equal("20 次后台请求", viewModel.SevenDayRequestsDetail);
        Assert.Equal("750 ms", viewModel.AverageResponseTime);
        Assert.Equal("1,800", viewModel.InputTokens);
        Assert.Equal("600", viewModel.OutputTokens);
        Assert.Equal("300", viewModel.CachedInputTokens);
        Assert.Equal("14.3%", viewModel.CacheHitRate);
        Assert.Equal("近 7 日缓存复用占比", viewModel.CacheHitRateDetail);

        Assert.False(viewModel.ApplyBackendUsageSnapshot(snapshot: null));
        Assert.Equal("800", viewModel.TodayTokens);
        Assert.Equal("本机后台", viewModel.UsageDataSourceLabel);
        Assert.Equal("本机用量趋势", viewModel.UsageTrendTitle);
        Assert.Contains("暂时无法读取", viewModel.TelemetryStatusNotice, StringComparison.Ordinal);
    }
}
