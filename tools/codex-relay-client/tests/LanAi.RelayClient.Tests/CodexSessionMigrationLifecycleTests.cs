using System.IO;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.Workspace.Injection;
using Microsoft.Data.Sqlite;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The migrator itself is covered in its own project. What is pinned here is where
/// <see cref="CodexStartup"/> calls it: before Codex starts, after the user's own
/// configuration is back, and never in a way that can stop either from happening.
/// </summary>
public sealed class CodexSessionMigrationLifecycleTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), $"relay-sessions-{Guid.NewGuid():N}");

    public void Dispose()
    {
        SqliteConnection.ClearAllPools();
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    /// <summary>
    /// Codex reads the list once, when it starts. Moving conversations after launch
    /// would leave the first list empty and look exactly like the bug being fixed.
    /// </summary>
    [Fact]
    public async Task ConversationsAreMovedBeforeCodexIsLaunched()
    {
        string? providerWhenLaunched = null;
        Setup setup = await CreateSetupAsync(onLaunch: () => providerWhenLaunched = ProviderOf(setup: null, "a"));
        setup.GivenThread("a", "custom");

        CodexStartupResult result = await setup.Startup.RunAsync(groupId: 3, "https://relay.test/v1");

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.Equal("gongfei", providerWhenLaunched);
    }

    [Fact]
    public async Task ReleasingCodexReturnsConversationsToTheProvidersTheyCameFrom()
    {
        Setup setup = await CreateSetupAsync(originalConfig: "model_provider = \"custom\"\n");
        setup.GivenThread("old", "codex_local_access");
        await setup.Startup.RunAsync(groupId: 3, "https://relay.test/v1");
        setup.GivenThread("made-under-relay", "gongfei");

        await setup.Startup.ReleaseAsync();

        Assert.Equal("codex_local_access", ProviderOf(setup, "old"));

        // No earlier provider to return to, so it goes where the restored configuration
        // now points — read from that configuration, not assumed.
        Assert.Equal("custom", ProviderOf(setup, "made-under-relay"));
    }

    /// <summary>
    /// Read from the file the user gets back, not from the one this client wrote. Reading
    /// too early would name <c>gongfei</c>, the one provider that is about to disappear.
    /// </summary>
    [Fact]
    public async Task TheFallbackComesFromTheRestoredConfigurationNotTheRelayOne()
    {
        Setup setup = await CreateSetupAsync(originalConfig: "# nothing selected\n");
        await setup.Startup.RunAsync(groupId: 3, "https://relay.test/v1");
        setup.GivenThread("made-under-relay", "gongfei");

        await setup.Startup.ReleaseAsync();

        Assert.Equal("openai", ProviderOf(setup, "made-under-relay"));
    }

    [Fact]
    public async Task ADatabaseHeldByCodexNeverStopsTheLaunch()
    {
        Setup setup = await CreateSetupAsync();
        setup.GivenThread("a", "custom");
        using var holder = new SqliteConnection(setup.Connection);
        holder.Open();
        using (SqliteCommand begin = holder.CreateCommand())
        {
            begin.CommandText = "BEGIN IMMEDIATE";
            begin.ExecuteNonQuery();
        }

        CodexStartupResult result = await setup.Startup.RunAsync(groupId: 3, "https://relay.test/v1");

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.Equal(1, setup.LaunchCount);
    }

    [Fact]
    public async Task ADamagedDatabaseNeverStopsTheLaunch()
    {
        Setup setup = await CreateSetupAsync();
        File.WriteAllText(setup.DatabasePath, "this is not a sqlite database");

        CodexStartupResult result = await setup.Startup.RunAsync(groupId: 3, "https://relay.test/v1");

        Assert.Equal(CodexStartupStatus.Ready, result.Status);
        Assert.Equal(1, setup.LaunchCount);
    }

    [Fact]
    public async Task ADamagedDatabaseNeverStopsTheRelease()
    {
        Setup setup = await CreateSetupAsync(originalConfig: "model_provider = \"custom\"\n");
        await setup.Startup.RunAsync(groupId: 3, "https://relay.test/v1");
        File.WriteAllText(setup.DatabasePath, "this is not a sqlite database");

        await setup.Startup.ReleaseAsync();

        // The user's own configuration came back regardless.
        Assert.Contains("custom", File.ReadAllText(setup.Paths.ConfigPath), StringComparison.Ordinal);
    }

    // ---- Fixture ------------------------------------------------------------------------

    private Setup? _current;

    private string ProviderOf(Setup? setup, string id)
    {
        Setup s = setup ?? _current ?? throw new InvalidOperationException("setup not created yet");
        var connectionString = new SqliteConnectionStringBuilder { DataSource = s.DatabasePath, Pooling = false }.ToString();
        using var connection = new SqliteConnection(connectionString);
        connection.Open();
        using SqliteCommand command = connection.CreateCommand();
        command.CommandText = "SELECT model_provider FROM threads WHERE id = $id";
        command.Parameters.AddWithValue("$id", id);
        return (string)command.ExecuteScalar()!;
    }

    private async Task<Setup> CreateSetupAsync(string originalConfig = "original-config", Action? onLaunch = null)
    {
        string home = Path.Combine(_root, "codex");
        string snapshots = Path.Combine(_root, "snapshot");
        var paths = new CodexPaths(home);
        Directory.CreateDirectory(home);
        File.WriteAllText(paths.AuthPath, "original-auth");
        File.WriteAllText(paths.ConfigPath, originalConfig);

        var protector = new TestSnapshotProtector();
        var writer = new CodexConfigWriter(
            paths,
            new CodexAuthSnapshot(protector, Path.Combine(_root, "legacy-auth.json")),
            new CodexFileSnapshot(paths, snapshots, protector));

        // Captures the snapshot of the user's own files, as the first real launch does.
        writer.Apply("sk-relay", "https://relay.test/v1");
        File.WriteAllText(paths.ConfigPath, originalConfig);
        File.WriteAllText(paths.AuthPath, "original-auth");

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

        var launcher = new CountingLauncher(() => onLaunch?.Invoke());
        var migrator = new CodexSessionProviderMigrator(
            paths,
            Path.Combine(_root, "session-backup"),
            TimeSpan.FromSeconds(1));
        var startup = new CodexStartup(
            relay,
            session,
            naming,
            writer,
            launcher,
            routeGuard: null,
            localRelay: null,
            contextFilter: null,
            sessions: migrator);

        _current = new Setup(startup, paths, launcher);
        _current.CreateDatabase();
        return _current;
    }

    private sealed class Setup(CodexStartup startup, CodexPaths paths, CountingLauncher launcher)
    {
        public CodexStartup Startup { get; } = startup;

        public CodexPaths Paths { get; } = paths;

        public string DatabasePath => Path.Combine(paths.Home, "state_5.sqlite");

        public string Connection =>
            new SqliteConnectionStringBuilder { DataSource = DatabasePath, Pooling = false, DefaultTimeout = 5 }.ToString();

        public int LaunchCount => launcher.Count;

        public void CreateDatabase()
        {
            using var connection = new SqliteConnection(Connection);
            connection.Open();
            using SqliteCommand command = connection.CreateCommand();
            command.CommandText =
                """
                PRAGMA journal_mode = WAL;
                CREATE TABLE threads (
                    id TEXT PRIMARY KEY,
                    source TEXT NOT NULL DEFAULT 'vscode',
                    model_provider TEXT NOT NULL,
                    archived INTEGER NOT NULL DEFAULT 0
                );
                """;
            command.ExecuteNonQuery();
        }

        public void GivenThread(string id, string provider)
        {
            using var connection = new SqliteConnection(Connection);
            connection.Open();
            using SqliteCommand command = connection.CreateCommand();
            command.CommandText = "INSERT INTO threads (id, model_provider) VALUES ($id, $provider)";
            command.Parameters.AddWithValue("$id", id);
            command.Parameters.AddWithValue("$provider", provider);
            command.ExecuteNonQuery();
        }
    }

    private sealed class CountingLauncher(Action onLaunch) : ICodexAppLauncher
    {
        public bool IsInstalled => true;

        public int Count { get; private set; }

        public Task<CodexLaunchResult> EnsureDebugPortAsync(
            CodexLaunchRequest request,
            CancellationToken cancellationToken = default)
        {
            Count++;
            onLaunch();
            return Task.FromResult(new CodexLaunchResult(CodexLaunchOutcome.Launched, request.Port, 123, "ready"));
        }
    }
}
