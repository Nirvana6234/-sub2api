using AiSwitchGui;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class LocalGatewayHealthMonitorTests
{
    [Fact]
    public async Task ConsecutiveFailuresTriggerOneVerifiedRecovery()
    {
        var controller = new RecoveryGatewayController(
            CreateStatus(healthy: false),
            CreateStatus(healthy: false),
            CreateStatus(healthy: true))
        {
            WaitForWebResult = true,
        };
        var time = new MutableTimeProvider(DateTimeOffset.Parse("2026-07-27T12:00:00Z"));
        var monitor = CreateMonitor(controller, time, failureThreshold: 2, maximumAttempts: 3);
        var updates = new List<LocalGatewayRecoveryUpdate>();
        monitor.StateChanged += updates.Add;

        await monitor.CheckOnceAsync(CancellationToken.None);
        Assert.Equal(0, controller.StartCalls);

        await monitor.CheckOnceAsync(CancellationToken.None);

        Assert.Equal(1, controller.StartCalls);
        Assert.Equal(1, controller.WaitForWebCalls);
        Assert.Contains(updates, update => update.State == LocalGatewayRecoveryState.Recovering);
        Assert.Equal(LocalGatewayRecoveryState.Recovered, updates[^1].State);
    }

    [Fact]
    public async Task RecoveryAttemptsAreBoundedInsideRollingWindow()
    {
        var controller = new RecoveryGatewayController(CreateStatus(healthy: false))
        {
            StartResult = new CommandResult { ExitCode = 1, StdErr = "missing runtime" },
        };
        var time = new MutableTimeProvider(DateTimeOffset.Parse("2026-07-27T12:00:00Z"));
        var monitor = CreateMonitor(controller, time, failureThreshold: 1, maximumAttempts: 2);
        var updates = new List<LocalGatewayRecoveryUpdate>();
        monitor.StateChanged += updates.Add;

        await monitor.CheckOnceAsync(CancellationToken.None);
        time.Advance(TimeSpan.FromSeconds(2));
        await monitor.CheckOnceAsync(CancellationToken.None);
        time.Advance(TimeSpan.FromSeconds(4));
        await monitor.CheckOnceAsync(CancellationToken.None);

        Assert.Equal(2, controller.StartCalls);
        Assert.Equal(LocalGatewayRecoveryState.Suspended, updates[^1].State);
        Assert.Contains("已尝试 2 次", updates[^1].Message, StringComparison.Ordinal);
    }

    [Fact]
    public async Task HealthyGatewayNeverStartsRecovery()
    {
        var controller = new RecoveryGatewayController(CreateStatus(healthy: true));
        var monitor = CreateMonitor(
            controller,
            new MutableTimeProvider(DateTimeOffset.Parse("2026-07-27T12:00:00Z")),
            failureThreshold: 1,
            maximumAttempts: 3);

        await monitor.CheckOnceAsync(CancellationToken.None);
        await monitor.CheckOnceAsync(CancellationToken.None);

        Assert.Equal(0, controller.StartCalls);
    }

    [Fact]
    public void GatewayPageShowsAutomaticRecoveryFailure()
    {
        var viewModel = new GatewayViewModel(new RecoveryGatewayController(CreateStatus(healthy: true)));

        viewModel.ApplyRecoveryUpdate(new LocalGatewayRecoveryUpdate(
            LocalGatewayRecoveryState.Suspended,
            "自动恢复已暂停",
            3));

        Assert.True(viewModel.HasAutomaticRecoveryFailure);
        Assert.Equal("自动恢复已暂停", viewModel.AutomaticRecoveryLabel);
    }

    [Fact]
    public void PackagedStartupRunsPreflightAndRestrictedPostgres()
    {
        string root = FindWorkspaceRoot();
        string startScript = File.ReadAllText(Path.Combine(root, "packaging", "windows", "start-sub2api-local.ps1"));
        string buildScript = File.ReadAllText(Path.Combine(root, "packaging", "windows", "build-full-windows-package.ps1"));
        string preflightScript = Path.Combine(root, "packaging", "windows", "Test-LanAi-Workspace.ps1");

        Assert.True(File.Exists(preflightScript));
        Assert.Contains("Test-LanAi-Workspace.ps1", startScript, StringComparison.Ordinal);
        Assert.Contains("/trustlevel:0x20000", startScript, StringComparison.Ordinal);
        Assert.Contains("Start-RestrictedPostgres", startScript, StringComparison.Ordinal);
        Assert.Contains("Test-LanAi-Workspace.ps1", buildScript, StringComparison.Ordinal);
    }

    [Fact]
    public void StartupFailureUsesActionableCommandOutput()
    {
        string message = MainWindowViewModel.DescribeGatewayCommandFailure(new CommandResult
        {
            ExitCode = 1,
            StdErr = "Missing required file: redis-server.exe",
        });

        Assert.Contains("redis-server.exe", message, StringComparison.Ordinal);
    }

    private static LocalGatewayHealthMonitor CreateMonitor(
        ILocalGatewayController controller,
        TimeProvider timeProvider,
        int failureThreshold,
        int maximumAttempts) => new(
            controller,
            timeProvider,
            probeInterval: TimeSpan.FromMinutes(1),
            recoveryTimeout: TimeSpan.FromSeconds(1),
            attemptWindow: TimeSpan.FromMinutes(10),
            baseCooldown: TimeSpan.FromSeconds(1),
            failureThreshold,
            maximumAttempts);

    private static LocalGatewayStatus CreateStatus(bool healthy)
    {
        var status = new LocalGatewayStatus
        {
            ControlAvailable = true,
            WebReachable = healthy,
            Summary = healthy ? "运行正常" : "服务异常",
        };
        foreach (string service in new[] { "sub2api", "postgres", "redis" })
        {
            status.Services.Add(new LocalGatewayServiceStatus
            {
                Service = service,
                State = healthy ? "running" : "stopped",
                Health = healthy ? "healthy" : string.Empty,
            });
        }
        return status;
    }

    private static string FindWorkspaceRoot()
    {
        DirectoryInfo? directory = new(AppContext.BaseDirectory);
        while (directory is not null)
        {
            if (Directory.Exists(Path.Combine(directory.FullName, "packaging", "windows")))
            {
                return directory.FullName;
            }
            directory = directory.Parent;
        }
        throw new DirectoryNotFoundException("Workspace root was not found from the test output directory.");
    }

    private sealed class MutableTimeProvider(DateTimeOffset utcNow) : TimeProvider
    {
        private DateTimeOffset _utcNow = utcNow;

        public override DateTimeOffset GetUtcNow() => _utcNow;

        public void Advance(TimeSpan duration) => _utcNow += duration;
    }

    private sealed class RecoveryGatewayController(params LocalGatewayStatus[] statuses) : ILocalGatewayController
    {
        private readonly Queue<LocalGatewayStatus> _statuses = new(statuses);
        private LocalGatewayStatus _lastStatus = statuses.Last();

        public CommandResult StartResult { get; set; } = new() { ExitCode = 0 };
        public bool WaitForWebResult { get; set; }
        public int StartCalls { get; private set; }
        public int WaitForWebCalls { get; private set; }

        public LocalGatewayStatus GetStartupStatus() => _lastStatus;

        public Task<LocalGatewayStatus> GetStatusAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (_statuses.Count > 0)
            {
                _lastStatus = _statuses.Dequeue();
            }
            return Task.FromResult(_lastStatus);
        }

        public Task<CommandResult> StartAsync(CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            StartCalls++;
            return Task.FromResult(StartResult);
        }

        public Task<CommandResult> ConfigureNativeRootAsync(string selectedPath, CancellationToken cancellationToken)
            => Task.FromResult(new CommandResult { ExitCode = 0 });

        public Task<CommandResult> StopAsync(CancellationToken cancellationToken)
            => Task.FromResult(new CommandResult { ExitCode = 0 });

        public Task<CommandResult> RestartAsync(CancellationToken cancellationToken)
            => Task.FromResult(new CommandResult { ExitCode = 0 });

        public Task<bool> WaitForWebAsync(TimeSpan timeout, CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            WaitForWebCalls++;
            return Task.FromResult(WaitForWebResult);
        }

        public Task OpenDashboardAsync(string url, CancellationToken cancellationToken)
            => Task.CompletedTask;
    }
}
