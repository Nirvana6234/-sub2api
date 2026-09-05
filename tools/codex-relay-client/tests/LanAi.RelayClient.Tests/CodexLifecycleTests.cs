using System.IO;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.Workspace.Injection;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class CodexLifecycleTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), $"relay-lifecycle-{Guid.NewGuid():N}");

    [Fact]
    public async Task StartingWithAClaudePreferenceWritesTheSelectedModel()
    {
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () => [ManagedKey(setup.Naming, 42)];
        File.WriteAllText(setup.Paths.ConfigPath, "model = \"gpt-5.6-sol\"");

        CodexStartupResult result = await setup.Startup.RunAsync(
            groupId: 3,
            apiBaseUrl: "https://relay.test/v1",
            preferredModel: "claude-opus-5");

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.Contains(
            "model = \"claude-opus-5\"",
            File.ReadAllText(setup.Paths.ConfigPath),
            StringComparison.Ordinal);
    }

    [Fact]
    public async Task ReleaseDeletesTheManagedKeyAndRestoresBothFiles()
    {
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () => [ManagedKey(setup.Naming, 42)];

        await setup.Startup.ReleaseAsync();

        Assert.Equal(1, setup.Relay.DeleteKeyCallCount);
        Assert.Equal(42, setup.Relay.LastDeletedKeyId);
        Assert.Equal("original-auth", File.ReadAllText(setup.Paths.AuthPath));
        Assert.Equal("original-config", File.ReadAllText(setup.Paths.ConfigPath));
    }

    [Fact]
    public async Task AServerFailureCannotPreventLocalFileRestoration()
    {
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () => [ManagedKey(setup.Naming, 42)];
        setup.Relay.OnDeleteKey = _ => throw new RelayApiException(RelayFailure.NetworkUnreachable, "offline");

        await setup.Startup.ReleaseAsync();

        Assert.Equal("original-auth", File.ReadAllText(setup.Paths.AuthPath));
        Assert.Equal("original-config", File.ReadAllText(setup.Paths.ConfigPath));
    }

    [Fact]
    public async Task HealthCheckPropagatesRateLimitingToThePollingCoordinator()
    {
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () =>
            throw new RelayApiException(RelayFailure.RateLimited, "slow down");

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => setup.Startup.CheckAsync());

        Assert.Equal(RelayFailure.RateLimited, error.Failure);
    }

    [Fact]
    public async Task LeaseRenewalPropagatesRateLimitingToThePollingCoordinator()
    {
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () =>
        [
            new RelayApiKey
            {
                Id = 42,
                Name = setup.Naming.KeyName(),
                Key = "sk-relay",
                ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(10),
            },
        ];
        setup.Relay.OnRenewKey = (_, _) =>
            throw new RelayApiException(RelayFailure.RateLimited, "slow down");

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => setup.Startup.RenewLeaseIfDueAsync());

        Assert.Equal(RelayFailure.RateLimited, error.Failure);
    }

    [Fact]
    public async Task AFailedFileRestoreCanBeRetriedByTheExitFallback()
    {
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () => [ManagedKey(setup.Naming, 42)];
        string manifestPath = Path.Combine(setup.SnapshotRoot, "manifest.json");
        File.WriteAllText(manifestPath, "{ damaged");

        await setup.Startup.ReleaseAsync();

        Assert.NotEqual("original-auth", File.ReadAllText(setup.Paths.AuthPath));
        Assert.NotEqual("original-config", File.ReadAllText(setup.Paths.ConfigPath));

        File.WriteAllText(manifestPath, "{\"AuthExisted\":true,\"ConfigExisted\":true}");
        await setup.Startup.ReleaseAsync();

        Assert.Equal("original-auth", File.ReadAllText(setup.Paths.AuthPath));
        Assert.Equal("original-config", File.ReadAllText(setup.Paths.ConfigPath));
    }

    [Fact]
    public async Task ReleasingTwiceDoesNotDeleteTheKeyTwice()
    {
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () => [ManagedKey(setup.Naming, 42)];

        await setup.Startup.ReleaseAsync();
        await setup.Startup.ReleaseAsync();

        Assert.Equal(1, setup.Relay.DeleteKeyCallCount);
    }

    [Fact]
    public async Task ReleaseDeletesCurrentAndEarlierInstallationsKeysOnly()
    {
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () =>
        [
            ManagedKey(setup.Naming, 42),
            new RelayApiKey
            {
                Id = 41,
                Name = ManagedKeyNaming.MachinePrefix() + "old-install",
                Key = "sk-old",
                ExpiresAt = DateTimeOffset.UtcNow.AddHours(6),
            },
            new RelayApiKey { Id = 40, Name = "共飞直连客户端-其他机器-install", Key = "sk-other" },
            new RelayApiKey { Id = 39, Name = "用户手建 key", Key = "sk-user" },
        ];

        await setup.Startup.ReleaseAsync();

        Assert.Equal([41L, 42L], setup.Relay.DeletedKeyIds.Order());
    }

    [Fact]
    public async Task OneOldKeyFailureDoesNotPreventDeletingTheCurrentKey()
    {
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () =>
        [
            ManagedKey(setup.Naming, 42),
            new RelayApiKey
            {
                Id = 41,
                Name = ManagedKeyNaming.MachinePrefix() + "old-install",
                Key = "sk-old",
                ExpiresAt = DateTimeOffset.UtcNow.AddHours(6),
            },
        ];
        setup.Relay.OnDeleteKey = id =>
        {
            if (id == 41)
            {
                throw new RelayApiException(RelayFailure.ServerError, "old key failed");
            }
        };

        await setup.Startup.ReleaseAsync();

        Assert.Equal([41L, 42L], setup.Relay.DeletedKeyIds);
        Assert.Equal("original-auth", File.ReadAllText(setup.Paths.AuthPath));
        Assert.Equal("original-config", File.ReadAllText(setup.Paths.ConfigPath));
    }

    [Fact]
    public async Task ReleaseWaitsForAnInFlightStartAndStopsItsEnhancement()
    {
        var enhancement = new BlockingEnhancementHost();
        Setup setup = await CreateSetupAsync(enhancement);
        setup.Relay.OnListKeys = () => [ManagedKey(setup.Naming, 42)];

        Task<CodexStartupResult> run = setup.Startup.RunAsync(null, "https://relay.test/v1");
        await enhancement.StartEntered.Task.WaitAsync(TimeSpan.FromSeconds(5));

        Task release = setup.Startup.ReleaseAsync();
        await Task.Yield();

        Assert.False(release.IsCompleted);

        enhancement.AllowStart.SetResult();
        await Task.WhenAll(run, release);

        Assert.False(enhancement.IsActive);
        Assert.Equal("original-auth", File.ReadAllText(setup.Paths.AuthPath));
        Assert.Equal("original-config", File.ReadAllText(setup.Paths.ConfigPath));
    }

    [Fact]
    public async Task AStartRequestedDuringReleaseCannotReapplyTheRouteAfterwards()
    {
        var enhancement = new BlockingStopEnhancementHost();
        Setup setup = await CreateSetupAsync(enhancement);
        setup.Relay.OnListKeys = () => [ManagedKey(setup.Naming, 42)];

        Task release = setup.Startup.ReleaseAsync();
        await enhancement.StopEntered.Task.WaitAsync(TimeSpan.FromSeconds(5));

        Task<CodexStartupResult> run = setup.Startup.RunAsync(null, "https://relay.test/v1");
        enhancement.AllowStop.SetResult();

        await release;
        CodexStartupResult result = await run;

        Assert.Equal(CodexStartupStatus.LocalFailure, result.Status);
        Assert.Equal(0, setup.Launcher.EnsureCallCount);
        Assert.Equal(0, enhancement.StartCallCount);
        Assert.Equal("original-auth", File.ReadAllText(setup.Paths.AuthPath));
        Assert.Equal("original-config", File.ReadAllText(setup.Paths.ConfigPath));
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    private async Task<Setup> CreateSetupAsync(ICodexEnhancementHost? enhancement = null)
    {
        string home = Path.Combine(_root, "codex");
        string snapshots = Path.Combine(_root, "snapshot");
        var paths = new CodexPaths(home);
        Directory.CreateDirectory(home);
        File.WriteAllText(paths.AuthPath, "original-auth");
        File.WriteAllText(paths.ConfigPath, "original-config");

        var protector = new TestSnapshotProtector();
        var writer = new CodexConfigWriter(
            paths,
            new CodexAuthSnapshot(protector, Path.Combine(_root, "legacy-auth.json")),
            new CodexFileSnapshot(paths, snapshots, protector));
        writer.Apply("sk-relay", "https://relay.test/v1");

        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync("a@b.com", "pw");
        var naming = new ManagedKeyNaming(new FixedInstallId("testinst"));
        var launcher = new FakeCodexAppLauncher();
        var startup = new CodexStartup(
            relay,
            session,
            naming,
            writer,
            launcher,
            enhancement);
        return new Setup(relay, naming, startup, paths, launcher, snapshots);
    }

    private static RelayApiKey ManagedKey(ManagedKeyNaming naming, long id) =>
        new()
        {
            Id = id,
            Name = naming.KeyName(),
            Key = "sk-relay",
            ExpiresAt = DateTimeOffset.UtcNow.AddHours(12),
        };

    private sealed record Setup(
        FakeRelayClient Relay,
        ManagedKeyNaming Naming,
        CodexStartup Startup,
        CodexPaths Paths,
        FakeCodexAppLauncher Launcher,
        string SnapshotRoot);

    private sealed class BlockingEnhancementHost : ICodexEnhancementHost
    {
        public TaskCompletionSource StartEntered { get; } =
            new(TaskCreationOptions.RunContinuationsAsynchronously);

        public TaskCompletionSource AllowStart { get; } =
            new(TaskCreationOptions.RunContinuationsAsynchronously);

        public bool IsActive { get; private set; }

        public async Task<bool> StartAsync(
            string apiKey,
            string baseUrl,
            CancellationToken cancellationToken = default)
        {
            StartEntered.SetResult();
            await AllowStart.Task.WaitAsync(cancellationToken);
            IsActive = true;
            return true;
        }

        public Task StopAsync()
        {
            IsActive = false;
            return Task.CompletedTask;
        }
    }

    private sealed class BlockingStopEnhancementHost : ICodexEnhancementHost
    {
        public TaskCompletionSource StopEntered { get; } =
            new(TaskCreationOptions.RunContinuationsAsynchronously);

        public TaskCompletionSource AllowStop { get; } =
            new(TaskCreationOptions.RunContinuationsAsynchronously);

        public int StartCallCount { get; private set; }

        public Task<bool> StartAsync(
            string apiKey,
            string baseUrl,
            CancellationToken cancellationToken = default)
        {
            StartCallCount++;
            return Task.FromResult(true);
        }

        public async Task StopAsync()
        {
            StopEntered.SetResult();
            await AllowStop.Task;
        }
    }
}
