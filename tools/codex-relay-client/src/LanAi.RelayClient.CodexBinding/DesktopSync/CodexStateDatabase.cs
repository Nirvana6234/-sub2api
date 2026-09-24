using System.Globalization;
using System.Text.RegularExpressions;
using Microsoft.Data.Sqlite;

namespace LanAi.RelayClient.CodexBinding.DesktopSync;

/// <summary>What the state database says about one conversation.</summary>
/// <param name="RolloutPath">Already stripped of the <c>\\?\</c> prefix Codex stores.</param>
/// <param name="SandboxPolicy">The raw JSON column; see <see cref="PermissionMode"/>.</param>
public sealed record CodexThreadRecord(
    string Id,
    string RolloutPath,
    string Cwd,
    string? Title,
    string? Model,
    string? SandboxPolicy,
    string? ApprovalMode,
    bool Archived);

/// <summary>
/// Read-only access to the <c>threads</c> table in Codex's <c>state_N.sqlite</c>.
/// </summary>
/// <remarks>
/// Sync never writes here. The desktop app keeps the database open in WAL mode, and a
/// read-only, unpooled connection is the way to look without holding anything open
/// afterwards. The migrator next door is the only thing in this client that writes.
/// </remarks>
public sealed class CodexStateDatabase
{
    private static readonly Regex StateDatabaseName =
        new(@"^state_(\d+)\.sqlite$", RegexOptions.CultureInvariant);

    private readonly CodexPaths _paths;

    public CodexStateDatabase(CodexPaths paths) => _paths = paths;

    public CodexThreadRecord? FindThread(string threadId)
    {
        using SqliteConnection? connection = OpenReadOnly();
        if (connection is null)
        {
            return null;
        }

        using SqliteCommand command = connection.CreateCommand();
        command.CommandText =
            "SELECT id, rollout_path, cwd, title, model, sandbox_policy, approval_mode, archived " +
            "FROM threads WHERE id = $id";
        command.Parameters.AddWithValue("$id", threadId);
        using SqliteDataReader reader = command.ExecuteReader();
        return reader.Read() ? ReadRecord(reader) : null;
    }

    /// <summary>
    /// A conversation to name as the caller of an app-tools call, never <paramref name="target"/>.
    /// </summary>
    /// <remarks>
    /// The pipe insists the caller is a real conversation, but writes nothing into it
    /// (measured). An archived one is preferred: it is out of the user's sight, so if a
    /// model ever did answer back to the caller the reply would land somewhere harmless.
    /// A deleted id fails, so this is re-read rather than remembered.
    /// </remarks>
    public string? FindCallerThread(string target)
    {
        using SqliteConnection? connection = OpenReadOnly();
        if (connection is null)
        {
            return null;
        }

        using SqliteCommand command = connection.CreateCommand();
        command.CommandText =
            "SELECT id FROM threads WHERE id <> $target " +
            "ORDER BY archived DESC, updated_at DESC LIMIT 1";
        command.Parameters.AddWithValue("$target", target);
        return command.ExecuteScalar() as string;
    }

    private static CodexThreadRecord ReadRecord(SqliteDataReader reader) => new(
        Id: reader.GetString(0),
        RolloutPath: StripLongPathPrefix(reader.GetString(1)),
        Cwd: StripLongPathPrefix(reader.IsDBNull(2) ? string.Empty : reader.GetString(2)),
        Title: reader.IsDBNull(3) ? null : reader.GetString(3),
        Model: reader.IsDBNull(4) ? null : reader.GetString(4),
        SandboxPolicy: reader.IsDBNull(5) ? null : reader.GetString(5),
        ApprovalMode: reader.IsDBNull(6) ? null : reader.GetString(6),
        Archived: !reader.IsDBNull(7) && reader.GetInt64(7) != 0);

    /// <summary>Codex stores some paths as <c>\\?\C:\...</c>; file APIs want them plain.</summary>
    internal static string StripLongPathPrefix(string path) =>
        path.StartsWith(@"\\?\", StringComparison.Ordinal) ? path[4..] : path;

    private SqliteConnection? OpenReadOnly()
    {
        string? database = FindNewest();
        if (database is null)
        {
            return null;
        }

        var builder = new SqliteConnectionStringBuilder
        {
            DataSource = database,
            Mode = SqliteOpenMode.ReadOnly,
            DefaultTimeout = 2,

            // A pooled connection would keep Codex's database open after we are done.
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

    /// <summary>The highest-numbered <c>state_N.sqlite</c>; older generations are left behind by Codex.</summary>
    private string? FindNewest()
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
}
