using System.Data;
using System.Globalization;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;
using Microsoft.Data.Sqlite;

namespace LanAi.RelayClient.CodexBinding;

/// <summary>What a migration did, so the caller can say so without parsing anything.</summary>
public enum SessionMigrationOutcome
{
    /// <summary>Every conversation already belonged where it should. Nothing was written.</summary>
    NothingToDo,

    /// <summary>Some conversations were moved, after their database was backed up.</summary>
    Migrated,

    /// <summary>Not attempted, or not possible right now. Nothing was written.</summary>
    Skipped,

    /// <summary>Attempted and failed. The transaction was rolled back; nothing changed.</summary>
    Failed,
}

/// <param name="Changed">How many conversations were re-tagged.</param>
/// <param name="BackupDirectory">Where the pre-change copy of the database went, when one was made.</param>
/// <param name="Detail">A reason, for the log. Never shown to the user as is.</param>
public sealed record SessionMigrationResult(
    SessionMigrationOutcome Outcome,
    int Changed = 0,
    string? BackupDirectory = null,
    string? Detail = null);

/// <summary>
/// Keeps the user's existing Codex conversations reachable after this client changes
/// which model provider Codex talks to.
/// </summary>
/// <remarks>
/// <para>
/// <b>Why this exists.</b> Codex tags every conversation with the provider id it was
/// created under (<c>threads.model_provider</c> in its state database), and treats
/// that tag as load-bearing in two ways, both measured against a real Codex build:
/// the conversation list only shows conversations whose tag matches the provider
/// currently selected in <c>config.toml</c>, and resuming a conversation uses the
/// tag's provider rather than the current one — sending through whatever base URL
/// that provider's own section names, or failing outright with "Model provider not
/// found" when the section is gone. Switching the selected provider to
/// <c>gongfei</c> therefore hides every earlier conversation, and the ones that stay
/// reachable keep talking to their old endpoint instead of this client.
/// </para>
/// <para>
/// <b>What it does.</b> Before Codex is launched, conversations tagged with some other
/// provider are re-tagged <c>gongfei</c>; when the client hands Codex back, they are
/// tagged as they were. Only the database is touched: resume was measured to trust
/// the database's tag over the one repeated in the rollout file, so rewriting the
/// rollout files buys nothing here and would widen the change to every file on disk.
/// </para>
/// <para>
/// <b>Why it can be undone exactly.</b> The original tag of every conversation it
/// moves is written to a journal <i>before</i> the change. Handing back therefore
/// restores each conversation to its own provider rather than to a guess, and a crash
/// between the two steps loses nothing. The database itself is also backed up first,
/// and only the newest backup is kept.
/// </para>
/// <para>
/// <b>What it never does.</b> It does not throw for the situations that are normal on
/// a real machine — no Codex install yet, a database layout it does not recognise,
/// Codex holding the database — and reports them as a result instead, because none of
/// them may stop Codex from launching. It leaves archived conversations, sub-agent
/// threads and internal threads alone, the same set the sidebar hides. It does not
/// change a row's timestamps, so the list order does not move.
/// </para>
/// </remarks>
public sealed class CodexSessionProviderMigrator
{
    private const string JournalFileName = "journal.json";
    private const int BackupsToKeep = 1;

    private static readonly Regex StateDatabaseName =
        new(@"^state_(\d+)\.sqlite$", RegexOptions.CultureInvariant);

    private readonly CodexPaths _paths;
    private readonly string _backupRoot;
    private readonly int _busyTimeoutSeconds;
    private readonly object _gate = new();

    /// <param name="backupRoot">
    /// Where backups and the journal live. Must not be the file snapshot's directory:
    /// that one is cleared when the snapshot is restored, and the journal has to
    /// outlive it long enough to be read.
    /// </param>
    /// <param name="busyTimeout">
    /// How long to wait for Codex to release the database before giving up and
    /// skipping. Whole seconds, at least one — the underlying setting has that grain.
    /// </param>
    public CodexSessionProviderMigrator(CodexPaths paths, string backupRoot, TimeSpan? busyTimeout = null)
    {
        _paths = paths ?? throw new ArgumentNullException(nameof(paths));
        _backupRoot = string.IsNullOrWhiteSpace(backupRoot)
            ? throw new ArgumentException("Backup directory cannot be empty.", nameof(backupRoot))
            : Path.GetFullPath(backupRoot);
        _busyTimeoutSeconds = Math.Max(1, (int)Math.Ceiling((busyTimeout ?? TimeSpan.FromSeconds(3)).TotalSeconds));
    }

    private static string OwnedProvider => CodexConfigWriter.ProviderName;

    /// <summary>
    /// Re-tags the user's other conversations as this client's, so Codex lists them
    /// and resumes them through the relay.
    /// </summary>
    public SessionMigrationResult MoveSessionsToRelay()
    {
        lock (_gate)
        {
            return Execute(
                operation: "归入共飞",
                select: SelectConversationsBelongingElsewhere,
                target: (_, _) => OwnedProvider,
                beforeWrite: RecordOriginalProviders,
                afterCommit: null,
                onNothingToDo: null);
        }
    }

    /// <summary>
    /// Puts conversations back the way they were before this client took over.
    /// </summary>
    /// <param name="fallbackProvider">
    /// The provider to give a conversation this client did not move — one created
    /// while it was in charge. It has no earlier provider to return to, and would be
    /// stranded under a provider that no longer exists once Codex is handed back.
    /// The caller passes the provider the restored <c>config.toml</c> selects.
    /// </param>
    public SessionMigrationResult ReturnSessionsFromRelay(string fallbackProvider)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(fallbackProvider);
        if (string.Equals(fallbackProvider, OwnedProvider, StringComparison.Ordinal))
        {
            return new SessionMigrationResult(
                SessionMigrationOutcome.Skipped,
                Detail: "还原后的配置仍然指向共飞，没有可以还给的提供方。");
        }

        lock (_gate)
        {
            Dictionary<string, string> original = ReadJournal();
            return Execute(
                operation: "还给原提供方",
                select: SelectConversationsOwnedByRelay,
                target: (id, _) => original.TryGetValue(id, out string? previous) &&
                                   !string.Equals(previous, OwnedProvider, StringComparison.Ordinal)
                    ? previous
                    : fallbackProvider,
                beforeWrite: null,
                afterCommit: DeleteJournal,
                // Nothing is tagged as ours any more, so whatever the journal still
                // remembers describes conversations that are already back where they
                // belong. Left in place it would be read by the next hand-back.
                onNothingToDo: DeleteJournal);
        }
    }

    private SessionMigrationResult Execute(
        string operation,
        Func<SqliteConnection, HashSet<string>, List<Change>> select,
        Func<string, string, string> target,
        Action<IReadOnlyList<Change>>? beforeWrite,
        Action? afterCommit,
        Action? onNothingToDo)
    {
        string? database = FindStateDatabase();
        if (database is null)
        {
            return new SessionMigrationResult(
                SessionMigrationOutcome.Skipped,
                Detail: "没有找到 Codex 的会话库，可能还没有创建过会话。");
        }

        string? backupDirectory = null;
        bool committed = false;
        try
        {
            using SqliteConnection connection = Open(database);
            HashSet<string> columns = ReadColumns(connection, "threads");
            if (!columns.Contains("id") || !columns.Contains("model_provider"))
            {
                return new SessionMigrationResult(
                    SessionMigrationOutcome.Skipped,
                    Detail: $"会话库 {Path.GetFileName(database)} 的结构不是预期的，未做任何修改。");
            }

            // A look before anything is copied or locked: on almost every launch the
            // answer is "nothing to move", and that path must cost no backup and no
            // write lock.
            if (select(connection, columns).Count == 0)
            {
                onNothingToDo?.Invoke();
                return new SessionMigrationResult(SessionMigrationOutcome.NothingToDo);
            }

            backupDirectory = BackUp(connection, database);

            // The immediate transaction takes the write lock now, and the rows are read
            // again under it. A deferred one would read first and upgrade later, and in
            // WAL mode a writer committing in between makes that upgrade fail even
            // after the busy wait — so the list acted on must be the one read while
            // holding the lock, not the one from the look above.
            using SqliteTransaction transaction =
                connection.BeginTransaction(IsolationLevel.Serializable, deferred: false);
            List<Change> changes = select(connection, columns);
            if (changes.Count == 0)
            {
                transaction.Rollback();
                onNothingToDo?.Invoke();
                return new SessionMigrationResult(SessionMigrationOutcome.NothingToDo);
            }

            beforeWrite?.Invoke(changes);

            using SqliteCommand update = connection.CreateCommand();
            update.Transaction = transaction;
            update.CommandText = "UPDATE threads SET model_provider = $provider WHERE id = $id";
            SqliteParameter provider = update.Parameters.Add("$provider", SqliteType.Text);
            SqliteParameter id = update.Parameters.Add("$id", SqliteType.Text);
            foreach (Change change in changes)
            {
                provider.Value = target(change.Id, change.From);
                id.Value = change.Id;
                update.ExecuteNonQuery();
            }

            transaction.Commit();
            committed = true;

            // Only now is an older backup allowed to go. Pruning any earlier would trade
            // the last known-good copy for one that may end up describing a change that
            // never happened.
            PruneOldBackups(backupDirectory);
            afterCommit?.Invoke();

            return new SessionMigrationResult(
                SessionMigrationOutcome.Migrated,
                changes.Count,
                backupDirectory,
                operation);
        }
        catch (SqliteException ex) when (ex.SqliteErrorCode is 5 or 6)
        {
            // SQLITE_BUSY / SQLITE_LOCKED: Codex has the database and would not let go
            // within the wait. Not an error — the next launch tries again — but also
            // not something to retry here: the caller is about to launch Codex.
            return new SessionMigrationResult(
                SessionMigrationOutcome.Skipped,
                Detail: "Codex 正在使用会话库，这次没有整理，下次启动时会再试。");
        }
        catch (Exception ex) when (ex is SqliteException or IOException or UnauthorizedAccessException)
        {
            return new SessionMigrationResult(SessionMigrationOutcome.Failed, Detail: ex.Message);
        }
        finally
        {
            // Runs after the connection is closed, so the copy is not held open. A backup
            // of a change that was never made is clutter that also looks like a record
            // of something that happened.
            if (!committed && backupDirectory is not null)
            {
                DiscardBackup(backupDirectory);
            }
        }
    }

    private static void DiscardBackup(string directory)
    {
        try
        {
            Directory.Delete(directory, recursive: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Left behind, and pruned by the next successful run.
        }
    }

    private static List<Change> SelectConversationsBelongingElsewhere(SqliteConnection connection, HashSet<string> columns)
    {
        var where = new StringBuilder("COALESCE(model_provider, '') <> $owned");

        // The same set the sidebar shows, and no wider. An archived conversation is
        // reachable only by un-archiving it, sub-agent and internal threads belong to
        // a parent conversation, and ambient suggestions are not conversations at
        // all; moving any of them changes data the user cannot see for no benefit.
        if (columns.Contains("archived"))
        {
            where.Append(" AND COALESCE(archived, 0) = 0");
        }

        if (columns.Contains("source"))
        {
            where.Append(" AND LOWER(COALESCE(source, '')) NOT LIKE '%subagent%'");
            where.Append(" AND LOWER(COALESCE(source, '')) NOT LIKE '%internal%'");
        }

        if (columns.Contains("thread_source"))
        {
            where.Append(" AND COALESCE(thread_source, '') <> 'ambient_suggestions'");
        }

        return ReadChanges(connection, where.ToString());
    }

    private static List<Change> SelectConversationsOwnedByRelay(SqliteConnection connection, HashSet<string> columns) =>
        // No visibility filter here, deliberately. Everything tagged as ours has to
        // leave with us, archived or not: once the provider section is gone, an
        // archived conversation the user later restores would fail to open.
        ReadChanges(connection, "model_provider = $owned");

    private static List<Change> ReadChanges(SqliteConnection connection, string where)
    {
        using SqliteCommand command = connection.CreateCommand();
        command.CommandText = $"SELECT id, COALESCE(model_provider, '') FROM threads WHERE {where}";
        command.Parameters.AddWithValue("$owned", OwnedProvider);

        var changes = new List<Change>();
        using SqliteDataReader reader = command.ExecuteReader();
        while (reader.Read())
        {
            changes.Add(new Change(reader.GetString(0), reader.GetString(1)));
        }

        return changes;
    }

    /// <summary>
    /// The newest <c>state_N.sqlite</c>, or null.
    /// </summary>
    /// <remarks>
    /// Codex numbers the file by schema generation and leaves older ones behind when it
    /// moves on, so the highest number is the live one. Editing a stale one would be
    /// harmless but pointless, and matching by shape rather than by the literal
    /// <c>state_5</c> keeps this working across Codex updates.
    /// </remarks>
    private string? FindStateDatabase()
    {
        if (!Directory.Exists(_paths.Home))
        {
            return null;
        }

        return Directory
            .EnumerateFiles(_paths.Home, "state_*.sqlite")
            .Select(path => (Path: path, Match: StateDatabaseName.Match(Path.GetFileName(path))))
            .Where(candidate => candidate.Match.Success)
            .OrderByDescending(candidate => int.Parse(candidate.Match.Groups[1].Value, CultureInfo.InvariantCulture))
            .Select(candidate => candidate.Path)
            .FirstOrDefault();
    }

    private SqliteConnection Open(string database)
    {
        var builder = new SqliteConnectionStringBuilder
        {
            DataSource = database,

            // Never create. A missing file means Codex has not run here, and creating
            // an empty database in its directory would confuse it more than help.
            Mode = SqliteOpenMode.ReadWrite,

            // The wait for a lock held by Codex. This is the only knob the provider
            // offers for it; without it a busy database fails on the first attempt.
            DefaultTimeout = _busyTimeoutSeconds,

            // A pooled connection keeps the file open after Dispose, which would leave
            // the WAL un-checkpointed and the file locked for the next writer.
            Pooling = false,
        };

        var connection = new SqliteConnection(builder.ToString());
        try
        {
            connection.Open();
            return connection;
        }
        catch
        {
            connection.Dispose();
            throw;
        }
    }

    private static HashSet<string> ReadColumns(SqliteConnection connection, string table)
    {
        using SqliteCommand command = connection.CreateCommand();

        // The table name is a literal from this file, never input.
        command.CommandText = $"PRAGMA table_info({table})";

        var columns = new HashSet<string>(StringComparer.Ordinal);
        using SqliteDataReader reader = command.ExecuteReader();
        while (reader.Read())
        {
            columns.Add(reader.GetString(1));
        }

        return columns;
    }

    /// <summary>
    /// A consistent copy of the database, taken with SQLite's own backup routine.
    /// </summary>
    /// <remarks>
    /// Not a file copy: while Codex is running the live data is split between the main
    /// file and its write-ahead log, and copying only the former yields a database
    /// that is silently behind. Only the newest copy is kept — this is a safety net
    /// under the journal, not a history.
    /// </remarks>
    private string BackUp(SqliteConnection source, string database)
    {
        Directory.CreateDirectory(_backupRoot);
        string directory = Path.Combine(
            _backupRoot,
            DateTime.Now.ToString("yyyyMMdd-HHmmssfff", CultureInfo.InvariantCulture));
        Directory.CreateDirectory(directory);

        string destinationPath = Path.Combine(directory, Path.GetFileName(database));
        var destinationString = new SqliteConnectionStringBuilder
        {
            DataSource = destinationPath,
            Pooling = false,
        }.ToString();

        using (var destination = new SqliteConnection(destinationString))
        {
            destination.Open();
            source.BackupDatabase(destination);
        }

        if (!File.Exists(destinationPath) || new FileInfo(destinationPath).Length == 0)
        {
            throw new IOException("会话库备份没有生成有效文件，已放弃修改。");
        }

        return directory;
    }

    private void PruneOldBackups(string keep)
    {
        try
        {
            IEnumerable<string> stale = Directory
                .EnumerateDirectories(_backupRoot)
                .Where(path => !string.Equals(path, keep, StringComparison.Ordinal))
                .OrderByDescending(Path.GetFileName, StringComparer.Ordinal)
                .Skip(BackupsToKeep - 1);
            foreach (string path in stale)
            {
                Directory.Delete(path, recursive: true);
            }
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // An old backup that cannot be deleted costs disk, not correctness.
        }
    }

    // ---- The journal -----------------------------------------------------------------

    private string JournalPath => Path.Combine(_backupRoot, JournalFileName);

    /// <summary>
    /// Writes down where each conversation is about to move from.
    /// </summary>
    /// <remarks>
    /// <para>
    /// Merged into what is already there rather than replacing it, because a launch only
    /// sees the conversations that are not yet the relay's. Starting from an empty
    /// journal each time would forget every conversation moved on an earlier launch that
    /// was never handed back, and they would come home to the fallback provider instead
    /// of their own.
    /// </para>
    /// <para>
    /// A conversation can only appear again if something else re-tagged it since — another
    /// tool, or the user. Its entry is then replaced: what it was immediately before this
    /// move is where it should go back to.
    /// </para>
    /// </remarks>
    private void RecordOriginalProviders(IReadOnlyList<Change> changes)
    {
        Dictionary<string, string> journal = ReadJournal();
        foreach (Change change in changes)
        {
            journal[change.Id] = change.From;
        }

        Directory.CreateDirectory(_backupRoot);

        using var stream = new MemoryStream();
        using (var writer = new Utf8JsonWriter(stream, new JsonWriterOptions { Indented = true }))
        {
            writer.WriteStartObject();
            writer.WriteString("provider", OwnedProvider);
            writer.WriteStartObject("original");
            foreach ((string id, string from) in journal)
            {
                writer.WriteString(id, from);
            }

            writer.WriteEndObject();
            writer.WriteEndObject();
        }

        // Temporary file then move, like every other write here: a half-written journal
        // is worse than a stale one, because it would be read as authoritative.
        string temporaryPath = JournalPath + ".tmp";
        File.WriteAllBytes(temporaryPath, stream.ToArray());
        File.Move(temporaryPath, JournalPath, overwrite: true);
    }

    /// <remarks>
    /// Read with <see cref="JsonDocument"/> rather than bound to a type. This project
    /// is trimmable and reflection-based JSON binds to defaults without complaint when
    /// trimmed; a journal that read as empty would mean conversations that cannot be
    /// returned to their own provider, with nothing to say so.
    /// </remarks>
    private Dictionary<string, string> ReadJournal()
    {
        var journal = new Dictionary<string, string>(StringComparer.Ordinal);
        if (!File.Exists(JournalPath))
        {
            return journal;
        }

        try
        {
            using JsonDocument document = JsonDocument.Parse(File.ReadAllBytes(JournalPath));
            if (document.RootElement.TryGetProperty("original", out JsonElement original) &&
                original.ValueKind == JsonValueKind.Object)
            {
                foreach (JsonProperty entry in original.EnumerateObject())
                {
                    journal[entry.Name] = entry.Value.GetString() ?? string.Empty;
                }
            }
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            // Unreadable means empty. The consequence is bounded: conversations with no
            // recorded origin are returned to the fallback provider instead of their
            // own, which keeps them openable, just not under their old name.
        }

        return journal;
    }

    private void DeleteJournal()
    {
        try
        {
            File.Delete(JournalPath);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Stale entries are harmless: a hand-back only consults the journal for
            // conversations currently tagged as ours.
        }
    }

    private readonly record struct Change(string Id, string From);
}
