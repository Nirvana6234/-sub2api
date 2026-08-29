using AiSwitchGui;
using System.Net.Http;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class GatewayViewModelTests
{
    [Fact]
    public void AutomaticStartupRepairsMissingNativeDependenciesEvenWhenHttpIsReachable()
    {
        var status = new LocalGatewayStatus
        {
            ControlAvailable = true,
            WebReachable = true,
        };
        status.Services.Add(new LocalGatewayServiceStatus
        {
            Service = "sub2api",
            State = "running",
            Health = "healthy",
            Status = "监听中",
        });
        status.Services.Add(new LocalGatewayServiceStatus
        {
            Service = "redis",
            State = "stopped",
            Status = "未启动",
        });

        Assert.True(MainWindowViewModel.ShouldStartLocalGateway(status, "local-control-token"));
        Assert.Equal("原生服务启动中或不完整", LocalGatewayService.BuildNativeSummary(status));
    }

    [Fact]
    public void AutomaticStartupSkipsWhenHttpTokenAndNativeDependenciesAreHealthy()
    {
        var status = new LocalGatewayStatus
        {
            ControlAvailable = true,
            WebReachable = true,
        };
        status.Services.Add(new LocalGatewayServiceStatus
        {
            Service = "redis",
            State = "running",
            Health = "healthy",
            Status = "监听中",
        });

        Assert.False(MainWindowViewModel.ShouldStartLocalGateway(status, "local-control-token"));
    }

    [Fact]
    public async Task NativeStatusUsesRecoveredPostgresPort()
    {
        string root = Path.Combine(Path.GetTempPath(), "LanAiGatewayTests", Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(Path.Combine(root, "sub2api"));
        Directory.CreateDirectory(Path.Combine(root, ".local", "pgdata-recovered-reset"));
        await File.WriteAllTextAsync(Path.Combine(root, "start-sub2api-local.ps1"), "# start");
        await File.WriteAllTextAsync(Path.Combine(root, "stop-sub2api-local.ps1"), "# stop");
        await File.WriteAllTextAsync(
            Path.Combine(root, ".local", "pgdata-recovered-reset", "PG_VERSION"),
            "16");

        try
        {
            using var httpClient = new HttpClient(new FixedStatusHandler(System.Net.HttpStatusCode.OK));
            var service = new LocalGatewayService(
                composeFile: null,
                nativeRoot: root,
                httpClient: httpClient);

            LocalGatewayStatus status = await service.GetStatusAsync(CancellationToken.None);

            LocalGatewayServiceStatus postgres = Assert.Single(
                status.Services,
                item => item.Service == "postgres");
            Assert.Equal("127.0.0.1:55434", postgres.Ports);
        }
        finally
        {
            Directory.Delete(root, recursive: true);
        }
    }
    [Fact]
    public void GatewayView_UsesParameterlessTypeSafeUsageNavigation()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "Views", "GatewayView.xaml"));
        string xaml = File.ReadAllText(sourcePath);

        Assert.Contains("OpenUsageDashboardCommand", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("CommandParameter=\"stats\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("DataContext.NavigateCommand", xaml, StringComparison.Ordinal);
        Assert.Equal(1, xaml.Split("PrimaryGatewayActionCommand", StringSplitOptions.None).Length - 1);
        Assert.Contains("Visibility=\"{Binding ShowLocalServiceControls", xaml, StringComparison.Ordinal);
        Assert.Contains("Command=\"{Binding RefreshStatusCommand}\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Text=\"{Binding CacheStatusLabel}\"", xaml, StringComparison.Ordinal);
        Assert.Contains("切换页面不会自动刷新", xaml, StringComparison.Ordinal);
    }

    [Fact]
    public void GatewayView_ReadOnlyRunBindingsAreExplicitlyOneWay()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "Views", "GatewayView.xaml"));
        string xaml = File.ReadAllText(sourcePath);

        Assert.Contains("{Binding SuccessRate, Mode=OneWay}", xaml, StringComparison.Ordinal);
        Assert.Contains("{Binding PlatformQuotaLabel, Mode=OneWay}", xaml, StringComparison.Ordinal);
        Assert.Contains("{Binding RecentFailureLabel, Mode=OneWay}", xaml, StringComparison.Ordinal);
    }

    [Fact]
    public void GatewayControls_DoNotExposeRebuildOrAutomaticRepairCommands()
    {
        string[] commandNames = typeof(GatewayViewModel)
            .GetProperties()
            .Select(property => property.Name)
            .Where(name => name.EndsWith("Command", StringComparison.Ordinal))
            .ToArray();

        Assert.DoesNotContain("RebuildGatewayCommand", commandNames);
        Assert.DoesNotContain("DiagnoseAndRepairCommand", commandNames);
        Assert.DoesNotContain("RepairGatewayCommand", commandNames);

        Assert.Contains("StartGatewayCommand", commandNames);
        Assert.Contains("StopGatewayCommand", commandNames);
        Assert.Contains("RestartGatewayCommand", commandNames);
        Assert.Contains("RefreshStatusCommand", commandNames);
        Assert.Contains("OpenDashboardCommand", commandNames);
    }

    [Fact]
    public async Task ManualRefresh_MapsRuntimeStatusAndServices()
    {
        LocalGatewayStatus status = CreateStatus(webReachable: true);
        status.Services.Add(new LocalGatewayServiceStatus
        {
            Service = "sub2api",
            Name = "sub2api-api",
            State = "running",
            Health = "healthy",
            Ports = "0.0.0.0:8080->8080/tcp",
        });
        var controller = new StubGatewayController(status);
        var viewModel = new GatewayViewModel(controller);

        await viewModel.RefreshStatusCommand.ExecuteAsync(null);

        Assert.Equal(1, controller.StatusCalls);
        Assert.True(viewModel.IsHealthy);
        Assert.False(viewModel.HasFailure);
        Assert.Equal("当前设备已可使用", viewModel.ModeLabel);
        Assert.Equal("本机服务正在运行，本机和已配置的局域网设备都可以使用。", viewModel.GatewayUsageHint);
        Assert.Equal("打开本机后台", viewModel.PrimaryGatewayActionLabel);
        Assert.True(viewModel.ShowLocalServiceControls);
        Assert.Equal(1, viewModel.ServiceSummaryColumnSpan);
        Assert.Equal("http://127.0.0.1:8080", viewModel.WebUrl);
        GatewayServiceRowViewModel service = Assert.Single(viewModel.Services);
        Assert.Equal("本机中转", service.Name);
        Assert.Equal("运行中", service.State);
        Assert.True(service.IsHealthy);
    }

    [Fact]
    public async Task StoppedGateway_PrimaryActionUsesPlainUserLanguage()
    {
        var viewModel = new GatewayViewModel(new StubGatewayController(CreateStatus(webReachable: false)));

        await viewModel.InitializeAsync();

        Assert.Equal("启动并打开本机后台", viewModel.PrimaryGatewayActionLabel);
        Assert.Equal("可以启动本机服务，也可以在连接中心选择局域网或云端后台。", viewModel.GatewayUsageHint);
    }

    [Fact]
    public async Task SelectedCloudSource_IsUsedForDataAndPrimaryAction()
    {
        var controller = new StubGatewayController(CreateStatus(webReachable: false));
        var probe = new SelectiveBackendProbe(uri =>
            string.Equals(uri.Host, "relay.example.test", StringComparison.OrdinalIgnoreCase));
        var viewModel = new GatewayViewModel(
            controller,
            sessionManager: null,
            localGatewayEndpointResolver: null,
            endpointProbeService: null,
            serviceSummaryClient: null,
            backendProbe: probe);
        ConnectionProfile[] connections =
        [
            CreateProfile(
                ConnectionProfileIds.LocalMachine,
                "本机中转",
                ConnectionProfileKind.Local,
                "http://127.0.0.1:8080/v1"),
            CreateProfile(
                "cloud-a",
                "云端 A",
                ConnectionProfileKind.Cloud,
                "https://relay.example.test/v1",
                "https://relay.example.test/dashboard"),
        ];
        viewModel.ApplyConnections(
            connections,
            new ConnectionProfileSelection("cloud-a", ConnectionProfileIds.LocalMachine, "cloud-a"));

        await viewModel.InitializeAsync();
        await viewModel.RefreshStatusCommand.ExecuteAsync(null);

        Assert.Equal("当前后台：云端 A · 主页：https://relay.example.test/dashboard", viewModel.BackendSourceLabel);
        Assert.Contains("云端 A", viewModel.GatewayUsageHint, StringComparison.Ordinal);
        Assert.Equal("打开云端 A后台", viewModel.PrimaryGatewayActionLabel);
        Assert.True(viewModel.PrimaryGatewayActionCommand.CanExecute(null));
        Assert.False(viewModel.ShowLocalServiceControls);
        Assert.Equal(3, viewModel.ServiceSummaryColumnSpan);
        Assert.NotEmpty(probe.Probed);
        Assert.All(probe.Probed, uri => Assert.Equal("relay.example.test", uri.Host));
        Assert.Equal(0, controller.StatusCalls);
        Assert.DoesNotContain("docker-compose.local.yml", viewModel.OperationLog, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task SelectedRemoteSource_DoesNotFallBackToReachableLocalBackend()
    {
        var controller = new StubGatewayController(CreateStatus(webReachable: true));
        var probe = new SelectiveBackendProbe(uri =>
            string.Equals(uri.Host, "127.0.0.1", StringComparison.OrdinalIgnoreCase));
        var viewModel = new GatewayViewModel(
            controller,
            sessionManager: null,
            localGatewayEndpointResolver: null,
            endpointProbeService: null,
            serviceSummaryClient: null,
            backendProbe: probe);
        ConnectionProfile[] connections =
        [
            CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local, "http://127.0.0.1:8080/v1"),
            CreateProfile("cloud-a", "云端 A", ConnectionProfileKind.Cloud, "https://relay.example.test/v1"),
        ];
        viewModel.ApplyConnections(
            connections,
            new ConnectionProfileSelection("cloud-a", ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine),
            new ConnectionProfileRouting("cloud-a", "cloud-a", "cloud-a"));

        await viewModel.InitializeAsync();

        Assert.Equal("当前后台：云端 A · 主页：https://relay.example.test", viewModel.BackendSourceLabel);
        Assert.Equal("当前来源：云端 A", viewModel.GatewaySummary);
        Assert.Equal("打开云端 A后台", viewModel.PrimaryGatewayActionLabel);
        Assert.True(viewModel.PrimaryGatewayActionCommand.CanExecute(null));
        Assert.NotEmpty(probe.Probed);
        Assert.All(probe.Probed, uri => Assert.Equal("relay.example.test", uri.Host));
    }

    [Fact]
    public async Task SelectedPublicHttpCloudSource_ExposesNormalLogin()
    {
        var viewModel = new GatewayViewModel(new StubGatewayController(CreateStatus(webReachable: true)));
        ConnectionProfile[] connections =
        [
            CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local, "http://127.0.0.1:8080/v1"),
            CreateProfile("cloud-http", "远程来源", ConnectionProfileKind.Cloud, "http://relay.example.test/v1"),
        ];
        viewModel.ApplyConnections(
            connections,
            new ConnectionProfileSelection("cloud-http", ConnectionProfileIds.LocalMachine, "cloud-http"),
            new ConnectionProfileRouting("cloud-http", "cloud-http", "cloud-http"));

        await viewModel.InitializeAsync();

        Assert.Equal("当前来源：远程来源", viewModel.GatewaySummary);
        Assert.Equal("当前后台：远程来源 · 主页：http://relay.example.test", viewModel.BackendSourceLabel);
        Assert.Equal("打开远程来源后台", viewModel.PrimaryGatewayActionLabel);
        Assert.True(viewModel.PrimaryGatewayActionCommand.CanExecute(null));
        Assert.False(viewModel.ShowLocalServiceControls);
        Assert.Equal(3, viewModel.ServiceSummaryColumnSpan);
    }

    [Fact]
    public async Task Login_WaitsForServiceSummaryBeforeReportingSuccess()
    {
        var session = new StubSessionManager();
        var summary = new StubServiceSummaryClient();
        var viewModel = new GatewayViewModel(
            new StubGatewayController(CreateStatus(webReachable: false)),
            session,
            localGatewayEndpointResolver: null,
            endpointProbeService: null,
            serviceSummaryClient: summary,
            backendProbe: new SelectiveBackendProbe(_ => true));
        ConnectionProfile source = CreateProfile(
            "cloud-http",
            "远程来源",
            ConnectionProfileKind.Cloud,
            "http://relay.example.test/v1");
        viewModel.ApplyConnections(
            [source],
            new ConnectionProfileSelection("cloud-http", null, "cloud-http"));
        await viewModel.InitializeAsync();
        viewModel.LoginEmail = "user@example.test";

        bool succeeded = await viewModel.LoginLocalAccountAsync("password");

        Assert.True(succeeded);
        Assert.Equal(1, summary.LoadCalls);
        Assert.Equal("$9.00", viewModel.AccountBalanceLabel);
        Assert.Null(typeof(GatewayViewModel).GetProperty("ContributionBalanceLabel"));
        Assert.Equal("3 次", viewModel.TodayRequestLabel);
        Assert.Equal("$0.2500", viewModel.TodayActualCostLabel);
        Assert.Equal("服务摘要已更新。", viewModel.LoginStatus);
    }

    [Fact]
    public async Task ReturningToGatewayPage_UsesCacheUntilTheUserRefreshes()
    {
        var controller = new StubGatewayController(CreateStatus(webReachable: true));
        var clock = new MutableTimeProvider(new DateTimeOffset(2026, 7, 17, 0, 0, 0, TimeSpan.Zero));
        var viewModel = new GatewayViewModel(
            controller,
            sessionManager: null,
            localGatewayEndpointResolver: null,
            timeProvider: clock);

        await viewModel.InitializeAsync();
        await viewModel.InitializeAsync();

        Assert.Equal(0, controller.StatusCalls);
        Assert.Contains("点击刷新", viewModel.CacheStatusLabel, StringComparison.Ordinal);

        await viewModel.RefreshStatusCommand.ExecuteAsync(null);
        Assert.Equal(1, controller.StatusCalls);

        await viewModel.InitializeAsync();
        Assert.Equal(1, controller.StatusCalls);
        Assert.Contains("10 分钟缓存", viewModel.CacheStatusLabel, StringComparison.Ordinal);

        clock.Advance(TimeSpan.FromMinutes(11));
        await viewModel.InitializeAsync();
        Assert.Equal(1, controller.StatusCalls);
        Assert.Contains("超过 10 分钟", viewModel.CacheStatusLabel, StringComparison.Ordinal);

        await viewModel.RefreshStatusCommand.ExecuteAsync(null);
        Assert.Equal(2, controller.StatusCalls);
    }

    [Theory]
    [InlineData(System.Net.HttpStatusCode.OK, true, "本机中转运行正常")]
    [InlineData(System.Net.HttpStatusCode.ServiceUnavailable, false, "本机中转未运行")]
    public async Task MissingComposeFile_DefaultsToNativeRuntimeWithoutAControlFileWarning(
        System.Net.HttpStatusCode responseStatus,
        bool expectedReachable,
        string expectedSummary)
    {
        using var httpClient = new HttpClient(new FixedStatusHandler(responseStatus));
        var service = new LocalGatewayService(
            composeFile: null,
            nativeRoot: null,
            httpClient: httpClient);

        LocalGatewayStatus status = await service.GetStatusAsync(CancellationToken.None);

        Assert.True(status.NativeMode);
        Assert.False(status.ControlAvailable);
        Assert.Equal(LocalGatewayService.NativeWebUrl, status.WebUrl);
        Assert.Equal(expectedReachable, status.WebReachable);
        Assert.Equal(expectedSummary, status.Summary);
        Assert.DoesNotContain("docker-compose", status.Summary, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("控制", status.Summary, StringComparison.Ordinal);
    }

    [Fact]
    public async Task ConfigureNativeRoot_PersistsValidatedWorkspaceAndEnablesControlImmediately()
    {
        string root = Path.Combine(Path.GetTempPath(), "LanAiGatewayTests", Guid.NewGuid().ToString("N"));
        string selectedSub2Api = Path.Combine(root, "sub2api");
        string hintFile = Path.Combine(root, "settings", "native-path.txt");
        Directory.CreateDirectory(selectedSub2Api);
        await File.WriteAllTextAsync(Path.Combine(root, "start-sub2api-local.ps1"), "# start");
        await File.WriteAllTextAsync(Path.Combine(root, "stop-sub2api-local.ps1"), "# stop");
        try
        {
            using var httpClient = new HttpClient(new FixedStatusHandler(System.Net.HttpStatusCode.ServiceUnavailable));
            var service = new LocalGatewayService(
                composeFile: null,
                nativeRoot: null,
                httpClient: httpClient,
                nativeHintFile: hintFile);

            CommandResult result = await service.ConfigureNativeRootAsync(selectedSub2Api, CancellationToken.None);
            LocalGatewayStatus status = service.GetStartupStatus();

            Assert.True(result.Success, result.CombinedOutput);
            Assert.Equal(root, status.NativeRoot);
            Assert.True(status.ControlAvailable);
            Assert.True(status.NativeMode);
            Assert.Equal(root, await File.ReadAllTextAsync(hintFile));
        }
        finally
        {
            if (Directory.Exists(root))
            {
                Directory.Delete(root, recursive: true);
            }
        }
    }

    [Fact]
    public async Task StartupStatus_ReloadsTheLatestLocallySavedNativeWorkspace()
    {
        string testRoot = Path.Combine(Path.GetTempPath(), "LanAiGatewayTests", Guid.NewGuid().ToString("N"));
        string firstRoot = Path.Combine(testRoot, "first");
        string secondRoot = Path.Combine(testRoot, "second");
        string hintFile = Path.Combine(testRoot, "settings", "native-path.txt");
        foreach (string root in new[] { firstRoot, secondRoot })
        {
            Directory.CreateDirectory(Path.Combine(root, "sub2api"));
            await File.WriteAllTextAsync(Path.Combine(root, "start-sub2api-local.ps1"), "# start");
            await File.WriteAllTextAsync(Path.Combine(root, "stop-sub2api-local.ps1"), "# stop");
        }

        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(hintFile)!);
            await File.WriteAllTextAsync(hintFile, secondRoot);
            using var httpClient = new HttpClient(new FixedStatusHandler(System.Net.HttpStatusCode.ServiceUnavailable));
            var service = new LocalGatewayService(
                composeFile: null,
                nativeRoot: firstRoot,
                httpClient: httpClient,
                nativeHintFile: hintFile);

            LocalGatewayStatus status = service.GetStartupStatus();

            Assert.Equal(secondRoot, status.NativeRoot);
            Assert.True(status.ControlAvailable);
        }
        finally
        {
            if (Directory.Exists(testRoot))
            {
                Directory.Delete(testRoot, recursive: true);
            }
        }
    }

    [Fact]
    public async Task StartCommand_DisablesAllOtherOperationsUntilTheCommandAndProbeFinish()
    {
        LocalGatewayStatus status = CreateStatus(webReachable: true);
        var startCompletion = new TaskCompletionSource<CommandResult>(
            TaskCreationOptions.RunContinuationsAsynchronously);
        var controller = new StubGatewayController(status)
        {
            StartHandler = _ => startCompletion.Task,
            WaitForWebResult = true,
        };
        var viewModel = new GatewayViewModel(controller);

        Task running = viewModel.StartGatewayCommand.ExecuteAsync(null);

        Assert.True(viewModel.IsBusy);
        Assert.False(viewModel.RestartGatewayCommand.CanExecute(null));
        Assert.False(viewModel.RefreshStatusCommand.CanExecute(null));

        viewModel.RestartGatewayCommand.Execute(null);
        Assert.Equal(0, controller.RestartCalls);

        startCompletion.SetResult(new CommandResult
        {
            ExitCode = 0,
            StdOut = "started",
        });
        await running;

        Assert.False(viewModel.IsBusy);
        Assert.Equal(1, controller.StartCalls);
        Assert.Equal(1, controller.WaitForWebCalls);
        Assert.Contains("后台健康检查已通过", viewModel.OperationLog, StringComparison.Ordinal);
        Assert.True(viewModel.RestartGatewayCommand.CanExecute(null));
    }

    [Fact]
    public async Task FailedCommand_RemainsVisibleAndWritesSanitizedOperationLog()
    {
        LocalGatewayStatus status = CreateStatus(webReachable: false);
        var controller = new StubGatewayController(status)
        {
            StartHandler = _ => Task.FromResult(new CommandResult
            {
                ExitCode = 17,
                StdErr = "API_KEY=secret-value docker failed",
            }),
        };
        var viewModel = new GatewayViewModel(controller);

        await viewModel.StartGatewayCommand.ExecuteAsync(null);

        Assert.True(viewModel.HasFailure);
        Assert.Contains("启动中转失败", viewModel.StatusNotice, StringComparison.Ordinal);
        Assert.Contains("退出码 17", viewModel.OperationLog, StringComparison.Ordinal);
        Assert.Contains("API_KEY=<已隐藏>", viewModel.OperationLog, StringComparison.Ordinal);
        Assert.DoesNotContain("secret-value", viewModel.OperationLog, StringComparison.Ordinal);
        Assert.Equal(1, controller.StatusCalls);
    }

    [Fact]
    public async Task LanDashboard_PrefersExplicitConnectionCenterAddressOverApiAndRuntimeProbeAddresses()
    {
        LocalGatewayStatus status = CreateStatus(webReachable: true);
        status.Diagnostics.LanHealthUrl = "http://172.22.96.1:8080/health";
        var controller = new StubGatewayController(status);
        var viewModel = new GatewayViewModel(controller);
        viewModel.ApplyConnections(
        [
            CreateLanProfile(
                "http://192.168.31.247:8080/v1",
                "http://192.168.31.247:3000/dashboard"),
        ]);

        await viewModel.RefreshStatusCommand.ExecuteAsync(null);
        await viewModel.OpenLanDashboardCommand.ExecuteAsync(null);

        Assert.Equal("http://192.168.31.247:3000/dashboard", viewModel.LanDashboardUrl);
        Assert.Equal("http://192.168.31.247:3000/dashboard", Assert.Single(controller.OpenedDashboardUrls));
        Assert.Contains("连接中心", viewModel.LanDashboardStatusLabel, StringComparison.Ordinal);
    }

    [Fact]
    public async Task LanDashboard_RequiresExplicitConnectionCenterDashboardAddress()
    {
        LocalGatewayStatus status = CreateStatus(webReachable: true);
        status.Diagnostics.LanHealthUrl = "http://172.22.96.1:8080/health";
        var controller = new StubGatewayController(status);
        var viewModel = new GatewayViewModel(controller);
        viewModel.ApplyConnections(
        [
            CreateLanProfile("http://192.168.31.247:8080/v1"),
        ]);

        await viewModel.InitializeAsync();
        Assert.False(viewModel.OpenLanDashboardCommand.CanExecute(null));
        Assert.Empty(controller.OpenedDashboardUrls);
        Assert.Contains("局域网后台地址", viewModel.LanDashboardStatusLabel, StringComparison.Ordinal);
    }

    [Fact]
    public async Task LanDashboard_InvalidExplicitConnectionCenterAddressIsDisabled()
    {
        LocalGatewayStatus status = CreateStatus(webReachable: true);
        status.Diagnostics.LanHealthUrl = "http://172.22.96.1:8080/health";
        var controller = new StubGatewayController(status);
        var viewModel = new GatewayViewModel(controller);
        viewModel.ApplyConnections(
        [
            CreateLanProfile("https://192.168.31.247:9443/v1", "http://192.168.x.x:3000/dashboard"),
        ]);

        await viewModel.InitializeAsync();

        Assert.False(viewModel.OpenLanDashboardCommand.CanExecute(null));
        Assert.Empty(controller.OpenedDashboardUrls);
        Assert.Contains("局域网后台地址", viewModel.LanDashboardStatusLabel, StringComparison.Ordinal);
    }

    [Fact]
    public async Task LanDashboard_RuntimeProbeAddressAloneDoesNotBecomeTheBrowserTarget()
    {
        LocalGatewayStatus status = CreateStatus(webReachable: true);
        status.Diagnostics.LanHealthUrl = "http://172.22.96.1:8080/health";
        var controller = new StubGatewayController(status);
        var viewModel = new GatewayViewModel(controller);

        await viewModel.InitializeAsync();

        Assert.False(viewModel.OpenLanDashboardCommand.CanExecute(null));
        Assert.Empty(controller.OpenedDashboardUrls);
        Assert.Contains("连接中心", viewModel.LanDashboardStatusLabel, StringComparison.Ordinal);
    }

    [Fact]
    public async Task LanDashboard_InvalidConnectionCenterAddressIsDisabledEvenWhenRuntimeProbeHasAnAddress()
    {
        LocalGatewayStatus status = CreateStatus(webReachable: true);
        status.Diagnostics.LanHealthUrl = "http://172.22.96.1:8080/health";
        var controller = new StubGatewayController(status);
        var viewModel = new GatewayViewModel(controller);
        viewModel.ApplyConnections(
        [
            CreateLanProfile("http://192.168.x.x:8080/v1"),
        ]);

        await viewModel.InitializeAsync();

        Assert.False(viewModel.OpenLanDashboardCommand.CanExecute(null));
        Assert.Empty(controller.OpenedDashboardUrls);
        Assert.Contains("连接中心", viewModel.LanDashboardStatusLabel, StringComparison.Ordinal);
    }

    [Fact]
    public async Task StartAndOpenDashboard_StartsWaitsAndOpensTheVerifiedLocalDashboard()
    {
        LocalGatewayStatus status = CreateStatus(webReachable: true);
        var controller = new StubGatewayController(status) { WaitForWebResult = true };
        var viewModel = new GatewayViewModel(controller);

        await viewModel.StartAndOpenDashboardCommand.ExecuteAsync(null);

        Assert.Equal(1, controller.StartCalls);
        Assert.Equal(1, controller.WaitForWebCalls);
        Assert.Equal(new Uri(LocalGatewayService.NativeWebUrl).AbsoluteUri, Assert.Single(controller.OpenedDashboardUrls));
    }

    [Fact]
    public async Task RefreshFailure_RedactsSensitiveExceptionTextInStatusNotice()
    {
        LocalGatewayStatus status = CreateStatus(webReachable: false);
        var controller = new StubGatewayController(status)
        {
            StatusException = new InvalidOperationException(
                "password=real-secret access_token=jwt-secret Bearer bearer-secret"),
        };
        var viewModel = new GatewayViewModel(controller);

        await viewModel.RefreshStatusCommand.ExecuteAsync(null);

        Assert.Contains("<已隐藏>", viewModel.StatusNotice, StringComparison.Ordinal);
        Assert.DoesNotContain("real-secret", viewModel.StatusNotice, StringComparison.Ordinal);
        Assert.DoesNotContain("jwt-secret", viewModel.StatusNotice, StringComparison.Ordinal);
        Assert.DoesNotContain("bearer-secret", viewModel.StatusNotice, StringComparison.Ordinal);
    }

    private static LocalGatewayStatus CreateStatus(bool webReachable)
        => new()
        {
            NativeMode = true,
            ControlAvailable = true,
            NativeRoot = "E:\\gateway",
            WebUrl = LocalGatewayService.NativeWebUrl,
            WebReachable = webReachable,
            Summary = webReachable ? "原生模式运行正常" : "原生模式未启动",
        };

    private static ConnectionProfile CreateLanProfile(string baseUrl, string? dashboardUrl = null)
        => new()
        {
            Id = ConnectionProfileIds.LanDefault,
            Name = "局域网中转",
            Kind = ConnectionProfileKind.Lan,
            BaseUrl = baseUrl,
            DashboardUrl = dashboardUrl,
            ClientBaseUrls = new Dictionary<CliKind, string>
            {
                [CliKind.Codex] = baseUrl,
            },
            EnabledClients = [CliKind.Codex],
        };

    private static ConnectionProfile CreateProfile(
        string id,
        string name,
        ConnectionProfileKind kind,
        string baseUrl,
        string? dashboardUrl = null)
        => new()
        {
            Id = id,
            Name = name,
            Kind = kind,
            BaseUrl = baseUrl,
            DashboardUrl = dashboardUrl,
        };

    private sealed class SelectiveBackendProbe(Func<Uri, bool> isAvailable) : ILocalGatewayStatsProbe
    {
        public List<Uri> Probed { get; } = [];

        public Task<LocalGatewayStatsProbeResult> ProbeAsync(Uri apiBaseUri, CancellationToken cancellationToken)
        {
            Probed.Add(apiBaseUri);
            return Task.FromResult(isAvailable(apiBaseUri)
                ? LocalGatewayStatsProbeResult.Available
                : LocalGatewayStatsProbeResult.Unavailable);
        }
    }

    private sealed class StubSessionManager : ISub2ApiSessionManager
    {
        private Sub2ApiSessionAccess? _access;

        public Sub2ApiSessionState Current { get; private set; } = Sub2ApiSessionState.SignedOut;

        public event EventHandler? SessionChanged;

        public Task RestoreAsync(Uri apiBaseUri, CancellationToken cancellationToken) => Task.CompletedTask;

        public Task<Sub2ApiSessionAccess> LoginAsync(
            Uri apiBaseUri,
            string email,
            string password,
            CancellationToken cancellationToken)
            => LoginAsync(apiBaseUri, email, password, false, cancellationToken);

        public Task<Sub2ApiSessionAccess> LoginAsync(
            Uri apiBaseUri,
            string email,
            string password,
            bool allowInsecurePublicHttp,
            CancellationToken cancellationToken)
        {
            _access = new Sub2ApiSessionAccess(
                new Uri("http://relay.example.test/"),
                "access-token",
                7,
                "user",
                5m,
                0m,
                DateTimeOffset.UtcNow.AddMinutes(30));
            Current = new Sub2ApiSessionState(
                true,
                false,
                false,
                "普通用户",
                5m,
                0m,
                _access.ExpiresAtUtc,
                _access.ApiBaseUri,
                "已登录");
            SessionChanged?.Invoke(this, EventArgs.Empty);
            return Task.FromResult(_access);
        }

        public Task<Sub2ApiSessionAccess> GetAccessAsync(Uri apiBaseUri, CancellationToken cancellationToken)
            => Task.FromResult(_access ?? throw new InvalidOperationException("Not signed in."));

        public Task LogoutAsync(CancellationToken cancellationToken) => Task.CompletedTask;

        public void Dispose()
        {
        }
    }

    private sealed class StubServiceSummaryClient : ISub2ApiServiceSummaryClient
    {
        public int LoadCalls { get; private set; }

        public Task<Sub2ApiServiceSummary> LoadAsync(
            Sub2ApiSessionAccess access,
            CancellationToken cancellationToken)
        {
            LoadCalls++;
            return Task.FromResult(new Sub2ApiServiceSummary(
                9m,
                0m,
                3,
                100,
                0.25,
                2,
                1,
                0,
                Array.Empty<PlatformQuotaSummary>(),
                null));
        }
    }

    private sealed class StubGatewayController : ILocalGatewayController
    {
        public StubGatewayController(LocalGatewayStatus status)
        {
            Status = status;
        }

        public LocalGatewayStatus Status { get; set; }

        public Func<CancellationToken, Task<CommandResult>>? StartHandler { get; init; }

        public Exception? StatusException { get; init; }

        public bool WaitForWebResult { get; set; }

        public int StatusCalls { get; private set; }

        public int StartCalls { get; private set; }

        public int RestartCalls { get; private set; }

        public int WaitForWebCalls { get; private set; }

        public List<string> OpenedDashboardUrls { get; } = [];

        public LocalGatewayStatus GetStartupStatus() => Status;

        public Task<LocalGatewayStatus> GetStatusAsync(CancellationToken cancellationToken)
        {
            StatusCalls++;
            if (StatusException is not null)
            {
                throw StatusException;
            }

            return Task.FromResult(Status);
        }

        public Task<CommandResult> StartAsync(CancellationToken cancellationToken)
        {
            StartCalls++;
            return StartHandler?.Invoke(cancellationToken) ?? SuccessfulCommand();
        }

        public Task<CommandResult> ConfigureNativeRootAsync(string selectedPath, CancellationToken cancellationToken)
        {
            Status.NativeRoot = selectedPath;
            Status.NativeMode = true;
            Status.ControlAvailable = true;
            return SuccessfulCommand();
        }

        public Task<CommandResult> StopAsync(CancellationToken cancellationToken)
            => SuccessfulCommand();

        public Task<CommandResult> RestartAsync(CancellationToken cancellationToken)
        {
            RestartCalls++;
            return SuccessfulCommand();
        }

        public Task<bool> WaitForWebAsync(TimeSpan timeout, CancellationToken cancellationToken)
        {
            WaitForWebCalls++;
            return Task.FromResult(WaitForWebResult);
        }

        public Task OpenDashboardAsync(string url, CancellationToken cancellationToken)
        {
            OpenedDashboardUrls.Add(url);
            return Task.CompletedTask;
        }

        private static Task<CommandResult> SuccessfulCommand()
            => Task.FromResult(new CommandResult { ExitCode = 0, StdOut = "ok" });
    }

    private sealed class MutableTimeProvider(DateTimeOffset utcNow) : TimeProvider
    {
        private DateTimeOffset _utcNow = utcNow;

        public override DateTimeOffset GetUtcNow() => _utcNow;

        public void Advance(TimeSpan duration) => _utcNow += duration;
    }

    private sealed class FixedStatusHandler(System.Net.HttpStatusCode statusCode) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
            => Task.FromResult(new HttpResponseMessage(statusCode));
    }
}
