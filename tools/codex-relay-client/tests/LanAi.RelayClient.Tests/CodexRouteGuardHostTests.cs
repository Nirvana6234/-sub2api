using System.IO;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.Workspace.Injection;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class CodexRouteGuardHostTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), $"relay-routeguard-{Guid.NewGuid():N}");

    [Fact]
    public async Task ASuccessfulLaunchStartsTheRouteGuard()
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync("a@b.com", "pw");
        var naming = new ManagedKeyNaming(new FixedInstallId("testinst"));
        relay.OnListKeys = () =>
        [
            new RelayApiKey
            {
                Id = 42,
                Name = naming.KeyName(),
                Key = "sk-relay",
                ExpiresAt = DateTimeOffset.UtcNow.AddHours(12),
            },
        ];

        var paths = new CodexPaths(Path.Combine(_root, "codex"));
        var protector = new TestSnapshotProtector();
        var writer = new CodexConfigWriter(
            paths,
            new CodexAuthSnapshot(protector, Path.Combine(_root, "legacy-auth.json")),
            new CodexFileSnapshot(paths, Path.Combine(_root, "snapshot"), protector));
        var launcher = new FakeCodexAppLauncher();
        var guard = new FakeCodexRouteGuardHost();
        var startup = new CodexStartup(relay, session, naming, writer, launcher, guard);

        CodexStartupResult result = await startup.RunAsync(null, "https://relay.test/v1");

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.Equal(1, guard.StartCallCount);
    }

    [Fact]
    public async Task ADebugPortThatNeverOpensStillCountsAsAPlainLaunch()
    {
        // Newer ChatGPT builds can ignore or block --remote-debugging-port. The
        // process still starts and routing is already applied by this point, so
        // this must not be reported to the user as "拉不起 ChatGPT".
        //
        // The guard still has to start in this branch. Nothing needs the debug port
        // any more — the CDP overlay that did is gone — but an earlier version
        // returned before calling StartAsync at all here, which silently left the
        // route guard off on every machine where the port never opens. That guard is
        // the only thing that notices an official ChatGPT sign-in rewriting
        // config.toml and dropping the relay route.
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync("a@b.com", "pw");
        var naming = new ManagedKeyNaming(new FixedInstallId("testinst"));
        relay.OnListKeys = () =>
        [
            new RelayApiKey
            {
                Id = 42,
                Name = naming.KeyName(),
                Key = "sk-relay",
                ExpiresAt = DateTimeOffset.UtcNow.AddHours(12),
            },
        ];

        var paths = new CodexPaths(Path.Combine(_root, "codex"));
        var protector = new TestSnapshotProtector();
        var writer = new CodexConfigWriter(
            paths,
            new CodexAuthSnapshot(protector, Path.Combine(_root, "legacy-auth.json")),
            new CodexFileSnapshot(paths, Path.Combine(_root, "snapshot"), protector));
        var launcher = new FakeCodexAppLauncher { Outcome = CodexLaunchOutcome.DebugPortUnavailable };
        var guard = new FakeCodexRouteGuardHost();
        var startup = new CodexStartup(relay, session, naming, writer, launcher, guard);

        CodexStartupResult result = await startup.RunAsync(null, "https://relay.test/v1");

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.Equal(1, guard.StartCallCount);
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }
}

internal sealed class FakeCodexAppLauncher : ICodexAppLauncher
{
    public bool IsInstalled { get; set; } = true;

    public CodexLaunchOutcome Outcome { get; set; } = CodexLaunchOutcome.Launched;

    public int EnsureCallCount { get; private set; }

    public Task<CodexLaunchResult> EnsureDebugPortAsync(
        CodexLaunchRequest request,
        CancellationToken cancellationToken = default)
    {
        EnsureCallCount++;
        return Task.FromResult(new CodexLaunchResult(
            Outcome,
            request.Port,
            123,
            "ready"));
    }
}

internal sealed class FakeCodexRouteGuardHost : ICodexRouteGuardHost
{
    public int StartCallCount { get; private set; }

    public int StopCallCount { get; private set; }

    public Task StartAsync(string apiKey, string baseUrl, CancellationToken cancellationToken = default)
    {
        StartCallCount++;
        return Task.CompletedTask;
    }

    public Task StopAsync()
    {
        StopCallCount++;
        return Task.CompletedTask;
    }
}
