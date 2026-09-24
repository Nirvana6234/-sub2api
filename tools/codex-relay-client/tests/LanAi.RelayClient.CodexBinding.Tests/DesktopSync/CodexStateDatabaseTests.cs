using LanAi.RelayClient.CodexBinding.DesktopSync;
using Microsoft.Data.Sqlite;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests.DesktopSync;

public sealed class CodexStateDatabaseTests : IDisposable
{
    private readonly string _home = Path.Combine(Path.GetTempPath(), $"codex-state-{Guid.NewGuid():N}");
    private readonly CodexStateDatabase _database;

    public CodexStateDatabaseTests()
    {
        Directory.CreateDirectory(_home);
        _database = new CodexStateDatabase(new CodexPaths(_home));
    }

    public void Dispose()
    {
        SqliteConnection.ClearAllPools();
        Directory.Delete(_home, recursive: true);
    }

    /// <summary>
    /// An archived conversation is out of the user's sight, which is where any stray
    /// answer to the caller should land. The target itself is never the caller.
    /// </summary>
    [Fact]
    public void TheCallerIsAnArchivedConversationWhenThereIsOne()
    {
        GivenThreads(
            ("recent", Archived: false, UpdatedAt: 300),
            ("target", Archived: true, UpdatedAt: 400),
            ("old-archived", Archived: true, UpdatedAt: 100));

        Assert.Equal("old-archived", _database.FindCallerThread("target"));
    }

    [Fact]
    public void WithoutArchivedConversationsTheCallerIsTheMostRecentOtherOne()
    {
        GivenThreads(("target", false, 400), ("recent", false, 300), ("older", false, 100));

        Assert.Equal("recent", _database.FindCallerThread("target"));
    }

    [Fact]
    public void AConversationAloneHasNoCaller()
    {
        GivenThreads(("target", false, 400));

        Assert.Null(_database.FindCallerThread("target"));
    }

    [Fact]
    public void NoDatabaseMeansNothingFound()
    {
        Assert.Null(_database.FindCallerThread("target"));
        Assert.Null(_database.FindThread("target"));
    }

    /// <summary>Codex stores some paths as <c>\\?\C:\…</c>; file APIs are handed them plain.</summary>
    [Fact]
    public void PathsLoseTheLongPathPrefix()
    {
        GivenThreads(("t", false, 1));

        CodexThreadRecord record = _database.FindThread("t")!;

        Assert.Equal(@"C:\Users\tester\.codex\sessions\rollout-t.jsonl", record.RolloutPath);
        Assert.Equal(@"C:\Work\project", record.Cwd);
    }

    /// <summary>Only the highest generation is live; Codex leaves older ones behind.</summary>
    [Fact]
    public void TheNewestStateDatabaseIsTheOneRead()
    {
        GivenThreads("state_4.sqlite", ("stale", false, 1));
        GivenThreads("state_5.sqlite", ("live", false, 1));

        Assert.NotNull(_database.FindThread("live"));
        Assert.Null(_database.FindThread("stale"));
    }

    [Theory]
    [InlineData("""{"type":"disabled"}""", "never", PermissionMode.FullAccess)]
    [InlineData("""{"type":"managed","file_system":{"type":"restricted"},"network":"restricted"}""", "on-request", PermissionMode.Auto)]
    [InlineData("""{"type":"managed"}""", "never", PermissionMode.SandboxedNoApproval)]
    [InlineData("""{"type":"disabled"}""", "on-request", PermissionMode.Unknown)]
    [InlineData("not json", "never", PermissionMode.Unknown)]
    [InlineData(null, null, PermissionMode.Unknown)]
    public void PermissionModesAreClassifiedFromTheTwoColumns(string? sandbox, string? approval, PermissionMode expected) =>
        Assert.Equal(expected, PermissionModes.Classify(sandbox, approval));

    private void GivenThreads(params (string Id, bool Archived, long UpdatedAt)[] threads) =>
        GivenThreads("state_5.sqlite", threads);

    private void GivenThreads(string file, params (string Id, bool Archived, long UpdatedAt)[] threads)
    {
        using var connection = new SqliteConnection($"Data Source={Path.Combine(_home, file)};Pooling=False");
        connection.Open();
        using SqliteCommand create = connection.CreateCommand();
        create.CommandText =
            "CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, created_at INTEGER, updated_at INTEGER, " +
            "source TEXT, model_provider TEXT, cwd TEXT, title TEXT, sandbox_policy TEXT, approval_mode TEXT, " +
            "archived INTEGER NOT NULL DEFAULT 0, model TEXT)";
        create.ExecuteNonQuery();

        foreach ((string id, bool archived, long updatedAt) in threads)
        {
            using SqliteCommand insert = connection.CreateCommand();
            insert.CommandText =
                "INSERT INTO threads (id, rollout_path, updated_at, cwd, title, sandbox_policy, approval_mode, archived, model) " +
                "VALUES ($id, $path, $updated, $cwd, 'hi', '{\"type\":\"disabled\"}', 'never', $archived, 'gpt-5.5')";
            insert.Parameters.AddWithValue("$id", id);
            insert.Parameters.AddWithValue("$path", $@"\\?\C:\Users\tester\.codex\sessions\rollout-{id}.jsonl");
            insert.Parameters.AddWithValue("$updated", updatedAt);
            insert.Parameters.AddWithValue("$cwd", @"\\?\C:\Work\project");
            insert.Parameters.AddWithValue("$archived", archived ? 1 : 0);
            insert.ExecuteNonQuery();
        }
    }
}
