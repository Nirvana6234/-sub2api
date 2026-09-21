using Microsoft.Data.Sqlite;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests;

/// <summary>
/// The migrator edits a database that holds the user's conversation history and that
/// Codex itself keeps open. What matters is what it leaves alone and what it can undo,
/// far more than what it changes.
/// </summary>
public sealed class CodexSessionProviderMigratorTests : IDisposable
{
    private const string Relay = "gongfei";

    private readonly string _home = Path.Combine(Path.GetTempPath(), $"codex-home-{Guid.NewGuid():N}");
    private readonly string _backupRoot = Path.Combine(Path.GetTempPath(), $"codex-sessions-{Guid.NewGuid():N}");
    private readonly CodexSessionProviderMigrator _migrator;

    public CodexSessionProviderMigratorTests()
    {
        Directory.CreateDirectory(_home);
        _migrator = new CodexSessionProviderMigrator(new CodexPaths(_home), _backupRoot, TimeSpan.FromSeconds(1));
    }

    public void Dispose()
    {
        // Pools would keep the files open and make the deletes below fail.
        SqliteConnection.ClearAllPools();
        foreach (string directory in new[] { _home, _backupRoot })
        {
            if (Directory.Exists(directory))
            {
                Directory.Delete(directory, recursive: true);
            }
        }
    }

    // ---- Moving conversations to the relay ---------------------------------------------

    [Fact]
    public void ConversationsFromOtherProvidersMoveToTheRelayAndNothingElseChanges()
    {
        GivenDatabase(
            Thread("a", "codex_local_access"),
            Thread("b", "custom"),
            Thread("c", Relay));

        SessionMigrationResult result = _migrator.MoveSessionsToRelay();

        Assert.Equal(SessionMigrationOutcome.Migrated, result.Outcome);
        Assert.Equal(2, result.Changed);
        Assert.Equal(Relay, ProviderOf("a"));
        Assert.Equal(Relay, ProviderOf("b"));
        Assert.Equal(Relay, ProviderOf("c"));
    }

    /// <summary>
    /// The list is ordered by these columns. Moving a conversation must not reorder it.
    /// </summary>
    [Fact]
    public void MovingAConversationDoesNotTouchItsTimestamps()
    {
        GivenDatabase(Thread("a", "custom", updatedAt: 1234));

        _migrator.MoveSessionsToRelay();

        (long updatedAt, long updatedAtMs, long recencyAt) = Timestamps("a");
        Assert.Equal(1234, updatedAt);
        Assert.Equal(1234_000, updatedAtMs);
        Assert.Equal(1234, recencyAt);
    }

    [Fact]
    public void ArchivedSubAgentInternalAndAmbientThreadsAreLeftAlone()
    {
        GivenDatabase(
            Thread("visible", "custom"),
            Thread("archived", "custom", archived: true),
            Thread("subagent", "custom", source: "{\"subagent\":{\"other\":\"guardian\"}}"),
            Thread("internal", "custom", source: "internal"),
            Thread("ambient", "custom", threadSource: "ambient_suggestions"));

        SessionMigrationResult result = _migrator.MoveSessionsToRelay();

        Assert.Equal(1, result.Changed);
        Assert.Equal(Relay, ProviderOf("visible"));
        foreach (string untouched in new[] { "archived", "subagent", "internal", "ambient" })
        {
            Assert.Equal("custom", ProviderOf(untouched));
        }
    }

    [Fact]
    public void WhenEverythingAlreadyBelongsToTheRelayNothingIsWrittenOrBackedUp()
    {
        GivenDatabase(Thread("a", Relay), Thread("b", Relay));

        SessionMigrationResult result = _migrator.MoveSessionsToRelay();

        Assert.Equal(SessionMigrationOutcome.NothingToDo, result.Outcome);
        Assert.False(Directory.Exists(_backupRoot));
    }

    [Fact]
    public void TheDatabaseIsBackedUpBeforeAnythingChanges()
    {
        GivenDatabase(Thread("a", "custom"), Thread("b", "sub2api"));

        SessionMigrationResult result = _migrator.MoveSessionsToRelay();

        string backup = Path.Combine(Assert.IsType<string>(result.BackupDirectory), "state_5.sqlite");
        Assert.True(File.Exists(backup));
        Assert.Equal("custom", ProviderIn(backup, "a"));
        Assert.Equal("sub2api", ProviderIn(backup, "b"));
    }

    [Fact]
    public void OnlyTheNewestBackupIsKept()
    {
        GivenDatabase(Thread("a", "custom"));
        _migrator.MoveSessionsToRelay();

        AddThread(Thread("b", "sub2api"));
        SessionMigrationResult second = _migrator.MoveSessionsToRelay();

        string[] backups = Directory.GetDirectories(_backupRoot);
        Assert.Single(backups);
        Assert.Equal(second.BackupDirectory, backups[0]);
    }

    // ---- Handing them back -------------------------------------------------------------

    [Fact]
    public void HandingBackReturnsEachConversationToItsOwnProvider()
    {
        GivenDatabase(
            Thread("a", "codex_local_access"),
            Thread("b", "custom"));
        _migrator.MoveSessionsToRelay();

        SessionMigrationResult result = _migrator.ReturnSessionsFromRelay("openai");

        Assert.Equal(SessionMigrationOutcome.Migrated, result.Outcome);
        Assert.Equal("codex_local_access", ProviderOf("a"));
        Assert.Equal("custom", ProviderOf("b"));
    }

    /// <summary>
    /// A conversation created while the client was in charge has no earlier provider,
    /// and would be stranded under one that no longer exists once Codex is handed back.
    /// </summary>
    [Fact]
    public void ConversationsCreatedUnderTheRelayGoToTheFallbackProvider()
    {
        GivenDatabase(Thread("old", "custom"));
        _migrator.MoveSessionsToRelay();
        AddThread(Thread("native", Relay));

        _migrator.ReturnSessionsFromRelay("openai");

        Assert.Equal("custom", ProviderOf("old"));
        Assert.Equal("openai", ProviderOf("native"));
    }

    [Fact]
    public void ArchivedConversationsAreHandedBackToo()
    {
        GivenDatabase(Thread("archived", Relay, archived: true));

        _migrator.ReturnSessionsFromRelay("openai");

        // A conversation restored from the archive later must still be able to open.
        Assert.Equal("openai", ProviderOf("archived"));
    }

    /// <summary>
    /// A launch only sees conversations that are not yet the relay's, so a journal that
    /// started empty each time would forget everything moved on an earlier launch that
    /// was never handed back.
    /// </summary>
    [Fact]
    public void OriginsRecordedOnEarlierLaunchesSurviveLaterOnes()
    {
        GivenDatabase(Thread("a", "custom"));
        _migrator.MoveSessionsToRelay();
        AddThread(Thread("b", "sub2api"));
        _migrator.MoveSessionsToRelay();

        _migrator.ReturnSessionsFromRelay("openai");

        Assert.Equal("custom", ProviderOf("a"));
        Assert.Equal("sub2api", ProviderOf("b"));
    }

    /// <summary>
    /// If something else re-tags a moved conversation, it goes back to where it was just
    /// before the client moved it the second time, not to where it was originally.
    /// </summary>
    [Fact]
    public void AConversationRetaggedBySomethingElseReturnsToItsMostRecentProvider()
    {
        GivenDatabase(Thread("a", "custom"));
        _migrator.MoveSessionsToRelay();
        Execute("UPDATE threads SET model_provider = 'codex_local_access' WHERE id = 'a'");
        _migrator.MoveSessionsToRelay();

        _migrator.ReturnSessionsFromRelay("openai");

        Assert.Equal("codex_local_access", ProviderOf("a"));
    }

    [Fact]
    public void HandingBackWorksWithoutAJournal()
    {
        GivenDatabase(Thread("a", Relay), Thread("b", Relay));

        SessionMigrationResult result = _migrator.ReturnSessionsFromRelay("openai");

        Assert.Equal(2, result.Changed);
        Assert.Equal("openai", ProviderOf("a"));
        Assert.Equal("openai", ProviderOf("b"));
    }

    [Fact]
    public void TheJournalIsClearedOnceEverythingIsBack()
    {
        GivenDatabase(Thread("a", "custom"));
        _migrator.MoveSessionsToRelay();
        Assert.True(File.Exists(Path.Combine(_backupRoot, "journal.json")));

        _migrator.ReturnSessionsFromRelay("openai");

        Assert.False(File.Exists(Path.Combine(_backupRoot, "journal.json")));
    }

    /// <summary>
    /// After a hand-back with nothing left to return, a stale journal would be read by
    /// the next one and send conversations somewhere they never came from.
    /// </summary>
    [Fact]
    public void AStaleJournalIsDroppedWhenNothingIsTaggedAsTheRelays()
    {
        GivenDatabase(Thread("a", "custom"));
        _migrator.MoveSessionsToRelay();
        _migrator.ReturnSessionsFromRelay("openai");
        Assert.Equal("custom", ProviderOf("a"));

        // The user re-tags it by hand; the old journal must not resurrect its origin.
        Execute("UPDATE threads SET model_provider = 'openai' WHERE id = 'a'");
        _migrator.ReturnSessionsFromRelay("openai");

        Assert.False(File.Exists(Path.Combine(_backupRoot, "journal.json")));
    }

    [Fact]
    public void HandingBackRefusesToUseTheRelayAsTheFallback()
    {
        GivenDatabase(Thread("a", Relay));

        SessionMigrationResult result = _migrator.ReturnSessionsFromRelay(Relay);

        Assert.Equal(SessionMigrationOutcome.Skipped, result.Outcome);
        Assert.Equal(Relay, ProviderOf("a"));
    }

    // ---- Things that are normal on a real machine, and must never throw ---------------

    [Fact]
    public void ANeverUsedCodexInstallIsSkippedAndNothingIsCreated()
    {
        Directory.Delete(_home, recursive: true);

        SessionMigrationResult moved = _migrator.MoveSessionsToRelay();
        SessionMigrationResult returned = _migrator.ReturnSessionsFromRelay("openai");

        Assert.Equal(SessionMigrationOutcome.Skipped, moved.Outcome);
        Assert.Equal(SessionMigrationOutcome.Skipped, returned.Outcome);
        Assert.False(Directory.Exists(_home));
        Assert.False(Directory.Exists(_backupRoot));
    }

    /// <summary>
    /// Opening a missing file for writing would create an empty database in Codex's own
    /// directory. That is worse than doing nothing.
    /// </summary>
    [Fact]
    public void NoStateDatabaseMeansNoStateDatabaseIsCreated()
    {
        SessionMigrationResult result = _migrator.MoveSessionsToRelay();

        Assert.Equal(SessionMigrationOutcome.Skipped, result.Outcome);
        Assert.Empty(Directory.GetFiles(_home));
    }

    [Fact]
    public void ADatabaseWithoutTheExpectedColumnsIsLeftUntouched()
    {
        Execute("CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT)", "state_5.sqlite");
        Execute("INSERT INTO threads VALUES ('a', 'x')", "state_5.sqlite");

        SessionMigrationResult result = _migrator.MoveSessionsToRelay();

        Assert.Equal(SessionMigrationOutcome.Skipped, result.Outcome);
        Assert.Equal(1L, Scalar("SELECT COUNT(*) FROM threads", "state_5.sqlite"));
    }

    [Fact]
    public void OnlyTheNewestNumberedDatabaseIsEdited()
    {
        GivenDatabase("state_4.sqlite", Thread("old", "custom"));
        GivenDatabase("state_6.sqlite", Thread("new", "custom"));

        _migrator.MoveSessionsToRelay();

        Assert.Equal("custom", ProviderOf("old", "state_4.sqlite"));
        Assert.Equal(Relay, ProviderOf("new", "state_6.sqlite"));
    }

    /// <summary>
    /// Codex holds this database while it runs. Losing the race is not a failure, and
    /// the migrator must not sit there or leave a backup of a change it never made.
    /// </summary>
    [Fact]
    public void ABusyDatabaseIsSkippedWithoutThrowingOrLeavingABackup()
    {
        GivenDatabase(Thread("a", "custom"));
        using var holder = new SqliteConnection(Connection("state_5.sqlite"));
        holder.Open();
        using (SqliteCommand begin = holder.CreateCommand())
        {
            begin.CommandText = "BEGIN IMMEDIATE";
            begin.ExecuteNonQuery();
        }

        SessionMigrationResult result = _migrator.MoveSessionsToRelay();

        Assert.Equal(SessionMigrationOutcome.Skipped, result.Outcome);
        Assert.False(Directory.Exists(_backupRoot) && Directory.GetDirectories(_backupRoot).Length > 0);
        holder.Close();
        Assert.Equal("custom", ProviderOf("a"));
    }

    [Fact]
    public void AnAbandonedRunLeavesTheEarlierBackupInPlace()
    {
        GivenDatabase(Thread("a", "custom"));
        SessionMigrationResult first = _migrator.MoveSessionsToRelay();

        AddThread(Thread("b", "sub2api"));
        using var holder = new SqliteConnection(Connection("state_5.sqlite"));
        holder.Open();
        using (SqliteCommand begin = holder.CreateCommand())
        {
            begin.CommandText = "BEGIN IMMEDIATE";
            begin.ExecuteNonQuery();
        }

        SessionMigrationResult second = _migrator.MoveSessionsToRelay();

        Assert.Equal(SessionMigrationOutcome.Skipped, second.Outcome);
        Assert.Equal(new[] { first.BackupDirectory }, Directory.GetDirectories(_backupRoot));
    }

    // ---- Fixture ------------------------------------------------------------------------

    private sealed record ThreadRow(
        string Id,
        string Provider,
        bool Archived,
        string Source,
        string? ThreadSource,
        long UpdatedAt);

    private static ThreadRow Thread(
        string id,
        string provider,
        bool archived = false,
        string source = "vscode",
        string? threadSource = null,
        long updatedAt = 1000) =>
        new(id, provider, archived, source, threadSource, updatedAt);

    private string Connection(string file) =>
        new SqliteConnectionStringBuilder
        {
            DataSource = Path.Combine(_home, file),
            Pooling = false,
            DefaultTimeout = 5,
        }.ToString();

    private void GivenDatabase(params ThreadRow[] threads) => GivenDatabase("state_5.sqlite", threads);

    /// <summary>
    /// Built from the real <c>threads</c> table's columns and its two update triggers,
    /// in WAL mode as Codex runs it, so a test that claims timestamps do not move is
    /// actually up against the triggers that would move them.
    /// </summary>
    private void GivenDatabase(string file, params ThreadRow[] threads)
    {
        Execute(
            """
            PRAGMA journal_mode = WAL;
            CREATE TABLE threads (
                id TEXT PRIMARY KEY,
                rollout_path TEXT NOT NULL DEFAULT '',
                created_at INTEGER NOT NULL DEFAULT 0,
                updated_at INTEGER NOT NULL,
                source TEXT NOT NULL,
                model_provider TEXT NOT NULL,
                cwd TEXT NOT NULL DEFAULT '',
                title TEXT NOT NULL DEFAULT '',
                archived INTEGER NOT NULL DEFAULT 0,
                thread_source TEXT,
                preview TEXT NOT NULL DEFAULT '',
                updated_at_ms INTEGER,
                recency_at INTEGER NOT NULL DEFAULT 0,
                recency_at_ms INTEGER NOT NULL DEFAULT 0
            );
            CREATE TRIGGER threads_updated_at_ms_after_update
            AFTER UPDATE OF updated_at ON threads
            WHEN NEW.updated_at != OLD.updated_at AND NEW.updated_at_ms IS OLD.updated_at_ms
            BEGIN
                UPDATE threads SET updated_at_ms = NEW.updated_at * 1000 WHERE id = NEW.id;
            END;
            CREATE INDEX idx_threads_provider ON threads(model_provider);
            """,
            file);

        foreach (ThreadRow thread in threads)
        {
            AddThread(thread, file);
        }
    }

    private void AddThread(ThreadRow thread, string file = "state_5.sqlite")
    {
        using var connection = new SqliteConnection(Connection(file));
        connection.Open();
        using SqliteCommand command = connection.CreateCommand();
        command.CommandText =
            """
            INSERT INTO threads
                (id, updated_at, source, model_provider, archived, thread_source,
                 updated_at_ms, recency_at, recency_at_ms)
            VALUES ($id, $updated, $source, $provider, $archived, $threadSource,
                    $updated * 1000, $updated, $updated * 1000)
            """;
        command.Parameters.AddWithValue("$id", thread.Id);
        command.Parameters.AddWithValue("$updated", thread.UpdatedAt);
        command.Parameters.AddWithValue("$source", thread.Source);
        command.Parameters.AddWithValue("$provider", thread.Provider);
        command.Parameters.AddWithValue("$archived", thread.Archived ? 1 : 0);
        command.Parameters.AddWithValue("$threadSource", (object?)thread.ThreadSource ?? DBNull.Value);
        command.ExecuteNonQuery();
    }

    private void Execute(string sql, string file = "state_5.sqlite")
    {
        using var connection = new SqliteConnection(Connection(file));
        connection.Open();
        using SqliteCommand command = connection.CreateCommand();
        command.CommandText = sql;
        command.ExecuteNonQuery();
    }

    private long Scalar(string sql, string file = "state_5.sqlite")
    {
        using var connection = new SqliteConnection(Connection(file));
        connection.Open();
        using SqliteCommand command = connection.CreateCommand();
        command.CommandText = sql;
        return Convert.ToInt64(command.ExecuteScalar());
    }

    private string ProviderOf(string id, string file = "state_5.sqlite") =>
        ProviderIn(Path.Combine(_home, file), id);

    private static string ProviderIn(string path, string id)
    {
        var connectionString = new SqliteConnectionStringBuilder { DataSource = path, Pooling = false }.ToString();
        using var connection = new SqliteConnection(connectionString);
        connection.Open();
        using SqliteCommand command = connection.CreateCommand();
        command.CommandText = "SELECT model_provider FROM threads WHERE id = $id";
        command.Parameters.AddWithValue("$id", id);
        return (string)command.ExecuteScalar()!;
    }

    private (long UpdatedAt, long UpdatedAtMs, long RecencyAt) Timestamps(string id)
    {
        using var connection = new SqliteConnection(Connection("state_5.sqlite"));
        connection.Open();
        using SqliteCommand command = connection.CreateCommand();
        command.CommandText = "SELECT updated_at, updated_at_ms, recency_at FROM threads WHERE id = $id";
        command.Parameters.AddWithValue("$id", id);
        using SqliteDataReader reader = command.ExecuteReader();
        Assert.True(reader.Read());
        return (reader.GetInt64(0), reader.GetInt64(1), reader.GetInt64(2));
    }
}
