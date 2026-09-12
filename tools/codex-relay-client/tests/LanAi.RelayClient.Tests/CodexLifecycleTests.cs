using System.IO;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
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
    public async Task ForceNewKeyReplacesAnUnexpiredKeyInsteadOfReusingIt()
    {
        // IsSpent only looks at expiry, so a key that is broken some other way — no
        // group bound, revoked from the panel — still looks perfectly reusable to the
        // normal path. forceNewKey is how the repair button says "reuse is exactly
        // the problem here": it has to skip that check, delete the old key so
        // repairing repeatedly does not litter the list, and mint a real replacement.
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () => [ManagedKey(setup.Naming, 42)];
        setup.Relay.OnCreateKey = (name, groupId) =>
            new RelayApiKey { Id = 99, Name = name, Key = "sk-fresh", GroupId = groupId };

        CodexStartupResult result = await setup.Startup.RunAsync(
            groupId: 3,
            apiBaseUrl: "https://relay.test/v1",
            forceNewKey: true);

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.Equal(1, setup.Relay.DeleteKeyCallCount);
        Assert.Equal(42, setup.Relay.LastDeletedKeyId);
        Assert.Contains("sk-fresh", File.ReadAllText(setup.Paths.AuthPath), StringComparison.Ordinal);
    }

    [Fact]
    public async Task ForceNewKeyStillIssuesAReplacementWhenDeletingTheOldOneFails()
    {
        // The new key is what the user is actually here for; a delete outage must
        // not stand between them and it. The stale key is left for the next release
        // to sweep up as an orphan instead.
        Setup setup = await CreateSetupAsync();
        setup.Relay.OnListKeys = () => [ManagedKey(setup.Naming, 42)];
        setup.Relay.OnDeleteKey = _ => throw new RelayApiException(RelayFailure.ServerError, "boom");
        setup.Relay.OnCreateKey = (name, groupId) =>
            new RelayApiKey { Id = 99, Name = name, Key = "sk-fresh", GroupId = groupId };

        CodexStartupResult result = await setup.Startup.RunAsync(
            groupId: 3,
            apiBaseUrl: "https://relay.test/v1",
            forceNewKey: true);

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.Contains("sk-fresh", File.ReadAllText(setup.Paths.AuthPath), StringComparison.Ordinal);
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
        var enhancement = new BlockingRouteGuardHost();
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
        var enhancement = new BlockingStopRouteGuardHost();
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

    [Fact]
    public async Task TheLocalTransportRefusesToLaunchBeforeAGroupIsChosen()
    {
        // The relay turns away every request that carries no group. Launching anyway
        // would write the config, restart Codex and report 就绪, and then fail every
        // single turn with nothing on screen to explain why.
        await using var loopback = new LocalPawRelay("https://relay.test/", _ => Task.FromResult("jwt"));
        Setup setup = await CreateSetupAsync(localRelay: loopback);

        CodexStartupResult result = await setup.Startup.RunAsync(groupId: null, "https://relay.test/v1");

        Assert.Equal(CodexStartupStatus.RelayUnavailable, result.Status);
        Assert.Contains("分组", result.Message, StringComparison.Ordinal);
        Assert.Equal(0, setup.Launcher.EnsureCallCount);
        Assert.Null(loopback.BaseAddress);
    }

    [Fact]
    public async Task TheLocalTransportBindsTheChosenGroupAndSkipsManagedKeys()
    {
        await using var loopback = new LocalPawRelay("https://relay.test/", _ => Task.FromResult("jwt"));
        Setup setup = await CreateSetupAsync(localRelay: loopback);
        setup.Relay.OnListKeys = () => throw new InvalidOperationException("the local transport issues no keys");

        CodexStartupResult result = await setup.Startup.RunAsync(groupId: 12, "https://relay.test/v1");

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.True(setup.Startup.UsesLocalTransport);
        Assert.NotNull(loopback.BaseAddress);
        // Codex is pointed at the loopback port, never at the server, and is given the
        // local token rather than anything that means something off this machine.
        Assert.Contains("127.0.0.1", File.ReadAllText(setup.Paths.ConfigPath), StringComparison.Ordinal);
        Assert.Contains(loopback.Token, File.ReadAllText(setup.Paths.AuthPath), StringComparison.Ordinal);
    }

    private async Task<Setup> CreateSetupAsync(
        ICodexRouteGuardHost? enhancement = null,
        LocalPawRelay? localRelay = null)
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
            enhancement,
            localRelay);
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

    private sealed class BlockingRouteGuardHost : ICodexRouteGuardHost
    {
        public TaskCompletionSource StartEntered { get; } =
            new(TaskCreationOptions.RunContinuationsAsynchronously);

        public TaskCompletionSource AllowStart { get; } =
            new(TaskCreationOptions.RunContinuationsAsynchronously);

        public bool IsActive { get; private set; }

        public async Task StartAsync(
            string apiKey,
            string baseUrl,
            CancellationToken cancellationToken = default)
        {
            StartEntered.SetResult();
            await AllowStart.Task.WaitAsync(cancellationToken);
            IsActive = true;
        }

        public Task StopAsync()
        {
            IsActive = false;
            return Task.CompletedTask;
        }
    }

    private sealed class BlockingStopRouteGuardHost : ICodexRouteGuardHost
    {
        public TaskCompletionSource StopEntered { get; } =
            new(TaskCreationOptions.RunContinuationsAsynchronously);

        public TaskCompletionSource AllowStop { get; } =
            new(TaskCreationOptions.RunContinuationsAsynchronously);

        public int StartCallCount { get; private set; }

        public Task StartAsync(
            string apiKey,
            string baseUrl,
            CancellationToken cancellationToken = default)
        {
            StartCallCount++;
            return Task.CompletedTask;
        }

        public async Task StopAsync()
        {
            StopEntered.SetResult();
            await AllowStop.Task;
        }
    }
}
