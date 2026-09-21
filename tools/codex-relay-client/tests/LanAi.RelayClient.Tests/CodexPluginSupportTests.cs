using System.IO;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using LanAi.Workspace.Injection;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The relay has two users that come and go independently: Codex, started by the user's button,
/// and an editor plug-in, kept up by a checkbox. These pin how <see cref="CodexStartup"/> keeps
/// one from cutting off the other, and when it puts the plug-in configuration back.
/// </summary>
public sealed class CodexPluginSupportTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), $"relay-plugins-{Guid.NewGuid():N}");
    private readonly List<LocalPawRelay> _relays = [];

    public void Dispose()
    {
        foreach (LocalPawRelay relay in _relays)
        {
            relay.DisposeAsync().AsTask().GetAwaiter().GetResult();
        }

        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    private static PluginSupportRequest Wanted(string? model = "claude-opus-5") =>
        new(Enabled: true, GroupId: 5, GroupName: "claude", GroupIsClaude: true, Model: model);

    // ---- Turning it on ---------------------------------------------------------------------

    [Fact]
    public async Task ARequestedClaudeGroupStartsTheRelayAndPointsThePluginsAtIt()
    {
        Setup setup = await CreateSetupAsync();

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted());

        Assert.Equal(PluginSupportState.Active, result.State);
        Assert.NotNull(setup.Relay.Origin);
        Assert.Equal(setup.Relay.Origin, setup.Binding.AppliedOrigin);
        Assert.Equal(setup.Relay.Token, setup.Binding.AppliedToken);
        Assert.Equal("claude-opus-5", setup.Binding.AppliedModel);
    }

    /// <summary>
    /// An editor is not launched by this client, so the relay it points at has to be up whether
    /// or not the user ever presses 启动 ChatGPT.
    /// </summary>
    [Fact]
    public async Task TheRelayIsUpWithoutCodexEverBeingStarted()
    {
        Setup setup = await CreateSetupAsync();

        await setup.Startup.SyncPluginSupportAsync(Wanted());

        Assert.Equal(0, setup.Launcher.Count);
        Assert.NotNull(setup.Relay.Origin);
    }

    [Fact]
    public async Task NoModelChosenLeavesTheModelUnforced()
    {
        Setup setup = await CreateSetupAsync();

        await setup.Startup.SyncPluginSupportAsync(Wanted(model: null));

        Assert.Null(setup.Binding.AppliedModel);
        Assert.Equal(1, setup.Binding.ApplyCalls);
    }

    // ---- Turning it off, or not applying -----------------------------------------------------

    [Fact]
    public async Task AnUncheckedBoxPutsThingsBackAndNeverStartsTheRelay()
    {
        Setup setup = await CreateSetupAsync();

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted() with { Enabled = false });

        Assert.Equal(PluginSupportState.Off, result.State);
        Assert.Equal(0, setup.Binding.ApplyCalls);
        Assert.Equal(1, setup.Binding.RestoreCalls);
        Assert.Null(setup.Relay.Origin);
    }

    [Fact]
    public async Task ANonClaudeGroupSetsNothingUp()
    {
        Setup setup = await CreateSetupAsync();

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted() with { GroupIsClaude = false });

        Assert.Equal(PluginSupportState.WrongGroup, result.State);
        Assert.Equal(0, setup.Binding.ApplyCalls);
        Assert.Equal(1, setup.Binding.RestoreCalls);
    }

    [Fact]
    public async Task AutomaticRoutingHasNoGroupToPointThePluginsAt()
    {
        Setup setup = await CreateSetupAsync();

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted() with { GroupId = null, GroupIsClaude = false });

        Assert.Equal(PluginSupportState.WrongGroup, result.State);
        Assert.Equal(0, setup.Binding.ApplyCalls);
    }

    /// <summary>
    /// A previous run may have been killed with the configuration still pointing at the relay.
    /// The record of what to put back survives that, so putting back is attempted whether or not
    /// this process set anything up.
    /// </summary>
    [Fact]
    public async Task PuttingBackIsAttemptedEvenIfThisProcessNeverSetAnythingUp()
    {
        Setup setup = await CreateSetupAsync();

        await setup.Startup.SyncPluginSupportAsync(Wanted() with { Enabled = false });

        Assert.Equal(1, setup.Binding.RestoreCalls);
    }

    [Fact]
    public async Task ARelayStartedForThePluginsAloneStopsWhenTheyAreTurnedOff()
    {
        Setup setup = await CreateSetupAsync();
        await setup.Startup.SyncPluginSupportAsync(Wanted());
        Assert.NotNull(setup.Relay.Origin);

        await setup.Startup.SyncPluginSupportAsync(Wanted() with { Enabled = false });

        Assert.Null(setup.Relay.Origin);
    }

    /// <summary>
    /// The relay has two users. Turning the plug-ins off must not take it from Codex.
    /// </summary>
    [Fact]
    public async Task CodexStillOnTheRelayIsNotCutOffWhenThePluginsAreTurnedOff()
    {
        Setup setup = await CreateSetupAsync();
        CodexStartupResult started = await setup.Startup.RunAsync(groupId: 5, "https://relay.test/v1");
        Assert.Equal(CodexStartupStatus.Ready, started.Status);
        await setup.Startup.SyncPluginSupportAsync(Wanted());

        await setup.Startup.SyncPluginSupportAsync(Wanted() with { Enabled = false });

        Assert.NotNull(setup.Relay.Origin);
    }

    /// <summary>
    /// The other direction: a Codex launch that fails stops the relay, and must not do so while a
    /// plug-in is pointed at it. The dashboard would go on saying it was set up.
    /// </summary>
    [Fact]
    public async Task AFailedCodexLaunchDoesNotTakeTheRelayFromAPlugin()
    {
        Setup setup = await CreateSetupAsync();
        await setup.Startup.SyncPluginSupportAsync(Wanted());

        // Occupies the temporary file's name, so writing Codex's configuration fails and the
        // launch is abandoned partway.
        Directory.CreateDirectory(setup.Paths.ConfigPath + ".tmp");
        CodexStartupResult result = await setup.Startup.RunAsync(groupId: 5, "https://relay.test/v1");

        Assert.Equal(CodexStartupStatus.LocalFailure, result.Status);
        Assert.NotNull(setup.Relay.Origin);
    }

    // ---- Releasing ---------------------------------------------------------------------------

    /// <summary>
    /// What the plug-ins were pointed at should not outlive the thing answering there.
    /// </summary>
    [Fact]
    public async Task ReleasePutsThePluginConfigurationBackBeforeTheRelayStops()
    {
        Setup setup = await CreateSetupAsync();
        await setup.Startup.SyncPluginSupportAsync(Wanted());

        await setup.Startup.ReleaseAsync();

        Assert.Equal(1, setup.Binding.RestoreCalls);
        Assert.True(setup.Binding.RelayWasUpAtRestore, "restoring after the relay had stopped would leave a window pointing at nothing");
        Assert.Null(setup.Relay.Origin);
    }

    [Fact]
    public async Task AFailingRestoreNeverStopsTheRelease()
    {
        Setup setup = await CreateSetupAsync();
        await setup.Startup.SyncPluginSupportAsync(Wanted());
        setup.Binding.ThrowOnRestore = true;

        await setup.Startup.ReleaseAsync();

        Assert.Null(setup.Relay.Origin);
    }

    /// <summary>
    /// A sync that arrives while a release is in flight must not set the configuration up again
    /// for a relay that is about to stop.
    /// </summary>
    [Fact]
    public async Task ASyncArrivingDuringAReleaseDoesNotSetAnythingUpAgain()
    {
        var guard = new BlockingStopGuard();
        Setup setup = await CreateSetupAsync(routeGuard: guard);
        Task release = setup.Startup.ReleaseAsync();
        await guard.StopEntered.Task.WaitAsync(TimeSpan.FromSeconds(5));

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted());
        guard.AllowStop.SetResult();
        await release;

        Assert.Equal(PluginSupportState.NotApplicable, result.State);
        Assert.Equal(0, setup.Binding.ApplyCalls);
    }

    /// <summary>
    /// Once a release has finished, the client is still open — the user signs in again — and
    /// syncing works as before.
    /// </summary>
    [Fact]
    public async Task SyncingAfterAFinishedReleaseWorksAgain()
    {
        Setup setup = await CreateSetupAsync();
        await setup.Startup.ReleaseAsync();

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted());

        Assert.Equal(PluginSupportState.Active, result.State);
    }

    // ---- What goes wrong --------------------------------------------------------------------

    [Fact]
    public async Task ASignedOutUserIsToldSoAndNothingIsSetUp()
    {
        Setup setup = await CreateSetupAsync(signedIn: false);

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted());

        Assert.Equal(PluginSupportState.Problem, result.State);
        Assert.Contains("登录", result.Note, StringComparison.Ordinal);
        Assert.Equal(0, setup.Binding.ApplyCalls);
        Assert.Null(setup.Relay.Origin);
    }

    [Fact]
    public async Task ClaudeCodesOwnFileBeingUnsafeToEditIsReportedAsAProblem()
    {
        Setup setup = await CreateSetupAsync();
        setup.Binding.Report = new PluginBindingReport(
            new PluginConfigResult(PluginConfigOutcome.Skipped, "comments"),
            new PluginConfigResult(PluginConfigOutcome.Skipped, "not attempted"));

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted());

        Assert.Equal(PluginSupportState.Problem, result.State);
        Assert.Contains("settings.json", result.Note, StringComparison.Ordinal);
    }

    /// <summary>
    /// Claude Code itself is set up, so it works. The editor's prompt not taking is worth saying,
    /// but it is not the whole feature failing.
    /// </summary>
    [Fact]
    public async Task TheEditorNotTakingIsANoteOnAnOtherwiseWorkingSetup()
    {
        Setup setup = await CreateSetupAsync();
        setup.Binding.Report = new PluginBindingReport(
            new PluginConfigResult(PluginConfigOutcome.Applied),
            new PluginConfigResult(PluginConfigOutcome.Skipped, "unreadable"));

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted());

        Assert.Equal(PluginSupportState.Active, result.State);
        Assert.Contains("VS Code", result.Note, StringComparison.Ordinal);
    }

    [Fact]
    public async Task ABindingThatThrowsIsAProblemNotACrash()
    {
        Setup setup = await CreateSetupAsync();
        setup.Binding.ThrowOnApply = true;

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted());

        Assert.Equal(PluginSupportState.Problem, result.State);
    }

    [Fact]
    public async Task WithNoBindingThereIsNothingToDo()
    {
        Setup setup = await CreateSetupAsync(withBinding: false);

        PluginSupportResult result = await setup.Startup.SyncPluginSupportAsync(Wanted());

        Assert.Equal(PluginSupportState.NotApplicable, result.State);
        Assert.Null(setup.Relay.Origin);
    }

    // ---- Fixture -----------------------------------------------------------------------------

    private async Task<Setup> CreateSetupAsync(bool signedIn = true, bool withBinding = true, ICodexRouteGuardHost? routeGuard = null)
    {
        string home = Path.Combine(_root, "codex");
        var paths = new CodexPaths(home);
        Directory.CreateDirectory(home);
        File.WriteAllText(paths.AuthPath, "original-auth");
        File.WriteAllText(paths.ConfigPath, "original-config");

        var protector = new TestSnapshotProtector();
        var writer = new CodexConfigWriter(
            paths,
            new CodexAuthSnapshot(protector, Path.Combine(_root, "legacy-auth.json")),
            new CodexFileSnapshot(paths, Path.Combine(_root, "snapshot"), protector));
        writer.Apply("sk-relay", "https://relay.test/v1");
        File.WriteAllText(paths.ConfigPath, "original-config");
        File.WriteAllText(paths.AuthPath, "original-auth");

        var relayClient = new FakeRelayClient();
        var session = new RelaySessionManager(relayClient, new FakeSessionStore(), "https://relay.test/");
        if (signedIn)
        {
            await session.SignInAsync("a@b.com", "pw");
        }

        var naming = new ManagedKeyNaming(new FixedInstallId("testinst"));
        var relay = new LocalPawRelay("http://127.0.0.1:1", session.GetAccessTokenAsync);
        _relays.Add(relay);
        var launcher = new CountingLauncher();
        var binding = new FakeBinding(() => relay.Origin is not null);
        var startup = new CodexStartup(
            relayClient,
            session,
            naming,
            writer,
            launcher,
            routeGuard: routeGuard,
            localRelay: relay,
            contextFilter: null,
            sessions: null,
            plugins: withBinding ? binding : null);

        return new Setup(startup, relay, binding, launcher, paths);
    }

    private sealed record Setup(
        CodexStartup Startup,
        LocalPawRelay Relay,
        FakeBinding Binding,
        CountingLauncher Launcher,
        CodexPaths Paths);

    private sealed class FakeBinding(Func<bool> relayIsUp) : IPluginBinding
    {
        public int ApplyCalls { get; private set; }

        public int RestoreCalls { get; private set; }

        public string? AppliedOrigin { get; private set; }

        public string? AppliedToken { get; private set; }

        public string? AppliedModel { get; private set; }

        public bool RelayWasUpAtRestore { get; private set; }

        public bool ThrowOnApply { get; set; }

        public bool ThrowOnRestore { get; set; }

        public PluginBindingReport Report { get; set; } = new(
            new PluginConfigResult(PluginConfigOutcome.Applied),
            new PluginConfigResult(PluginConfigOutcome.Applied));

        public PluginBindingReport Apply(string relayOrigin, string relayToken, string? model)
        {
            ApplyCalls++;
            if (ThrowOnApply)
            {
                throw new IOException("disk on fire");
            }

            AppliedOrigin = relayOrigin;
            AppliedToken = relayToken;
            AppliedModel = model;
            return Report;
        }

        public PluginBindingReport Restore()
        {
            RestoreCalls++;
            RelayWasUpAtRestore = relayIsUp();
            if (ThrowOnRestore)
            {
                throw new IOException("disk on fire");
            }

            return new PluginBindingReport(
                new PluginConfigResult(PluginConfigOutcome.Restored),
                new PluginConfigResult(PluginConfigOutcome.Restored));
        }
    }

    private sealed class BlockingStopGuard : ICodexRouteGuardHost
    {
        public TaskCompletionSource StopEntered { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        public TaskCompletionSource AllowStop { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        public Task StartAsync(string apiKey, string baseUrl, CancellationToken cancellationToken = default) =>
            Task.CompletedTask;

        public async Task StopAsync()
        {
            StopEntered.TrySetResult();
            await AllowStop.Task;
        }
    }

    private sealed class CountingLauncher : ICodexAppLauncher
    {
        public bool IsInstalled => true;

        public int Count { get; private set; }

        public Task<CodexLaunchResult> EnsureDebugPortAsync(
            CodexLaunchRequest request,
            CancellationToken cancellationToken = default)
        {
            Count++;
            return Task.FromResult(new CodexLaunchResult(CodexLaunchOutcome.Launched, request.Port, 123, "ready"));
        }
    }
}
