using System.IO;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.Workspace.Injection;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class CodexEnhancementTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), $"relay-enhancement-{Guid.NewGuid():N}");

    [Fact]
    public async Task AnInjectionFailureDoesNotTurnASuccessfulCodexLaunchIntoAFailure()
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
        var enhancement = new FakeCodexEnhancementHost { StartResult = false };
        var startup = new CodexStartup(relay, session, naming, writer, launcher, enhancement);

        CodexStartupResult result = await startup.RunAsync(null, "https://relay.test/v1");

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.Equal(1, enhancement.StartCallCount);
    }

    [Fact]
    public async Task ADebugPortThatNeverOpensStillCountsAsAPlainLaunch()
    {
        // Newer ChatGPT builds can ignore or block --remote-debugging-port. The
        // process still starts and routing is already applied by this point, so
        // this must not be reported to the user as "拉不起 ChatGPT".
        //
        // Enhancement still has to start here even though there is no debug port
        // to attach to: RelayInjectionHost starts its CodexRouteGuard *before* it
        // ever tries the CDP connection (see RelayInjectionHost.StartAsync), so
        // the overlay/sentinel go missing but the guard — the thing that notices
        // an official ChatGPT login rewriting config.toml later and reapplies it
        // — does not. An earlier version of this fix returned before calling
        // StartAsync at all in this branch, which silently disabled that guard on
        // every machine where the debug port never opens.
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
        var enhancement = new FakeCodexEnhancementHost();
        var startup = new CodexStartup(relay, session, naming, writer, launcher, enhancement);

        CodexStartupResult result = await startup.RunAsync(null, "https://relay.test/v1");

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.Equal(1, enhancement.StartCallCount);
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

internal sealed class FakeCodexEnhancementHost : ICodexEnhancementHost
{
    public bool StartResult { get; set; } = true;

    public int StartCallCount { get; private set; }

    public int StopCallCount { get; private set; }

    public Task<bool> StartAsync(string apiKey, string baseUrl, CancellationToken cancellationToken = default)
    {
        StartCallCount++;
        return Task.FromResult(StartResult);
    }

    public Task StopAsync()
    {
        StopCallCount++;
        return Task.CompletedTask;
    }
}
