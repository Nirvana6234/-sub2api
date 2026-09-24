using System.Globalization;
using LanAi.Workspace.Core;
using Microsoft.Data.Sqlite;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Persists project-level settings only. Official CLI conversation bodies and
/// credentials are deliberately outside this database.
/// </summary>
public sealed class SqliteProjectRepository : IProjectRepository, IDisposable
{
    private readonly string _connectionString;
    private readonly string _databaseDirectory;
    private readonly SemaphoreSlim _initializationGate = new(1, 1);
    private volatile bool _initialized;

    public SqliteProjectRepository(AppDataPaths paths)
        : this(paths?.DatabasePath ?? throw new ArgumentNullException(nameof(paths)))
    {
    }

    public SqliteProjectRepository(string databasePath)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(databasePath);

        var fullPath = Path.GetFullPath(Environment.ExpandEnvironmentVariables(databasePath));
        _databaseDirectory = Path.GetDirectoryName(fullPath)
            ?? throw new ArgumentException("The database path must include a directory.", nameof(databasePath));

        _connectionString = new SqliteConnectionStringBuilder
        {
            DataSource = fullPath,
            Mode = SqliteOpenMode.ReadWriteCreate,
            Cache = SqliteCacheMode.Shared,
            Pooling = true,
        }.ToString();
    }

    public async Task<IReadOnlyList<ProjectRecord>> GetAllAsync(
        bool includeArchived = false,
        CancellationToken cancellationToken = default)
    {
        await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);

        await using var connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
        await using var command = connection.CreateCommand();
        command.CommandText = """
            SELECT id, display_name, root_path, path_fingerprint, default_cli,
                   default_connection_profile_id, default_model, resume_policy,
                   created_at, last_opened_at, is_archived
            FROM projects
            WHERE $include_archived = 1 OR is_archived = 0
            ORDER BY is_archived ASC,
                     COALESCE(last_opened_at, created_at) DESC,
                     display_name COLLATE NOCASE ASC;
            """;
        command.Parameters.AddWithValue("$include_archived", includeArchived ? 1 : 0);

        var projects = new List<ProjectRecord>();
        await using var reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
        while (await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
        {
            projects.Add(ReadProject(reader));
        }

        return projects;
    }

    public async Task<ProjectRecord?> GetByIdAsync(
        string id,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(id);
        await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);

        await using var connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
        await using var command = connection.CreateCommand();
        command.CommandText = """
            SELECT id, display_name, root_path, path_fingerprint, default_cli,
                   default_connection_profile_id, default_model, resume_policy,
                   created_at, last_opened_at, is_archived
            FROM projects
            WHERE id = $id
            LIMIT 1;
            """;
        command.Parameters.AddWithValue("$id", id);

        await using var reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
        return await reader.ReadAsync(cancellationToken).ConfigureAwait(false)
            ? ReadProject(reader)
            : null;
    }

    public async Task UpsertAsync(
        ProjectRecord project,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(project);
        ArgumentException.ThrowIfNullOrWhiteSpace(project.Id);
        ArgumentException.ThrowIfNullOrWhiteSpace(project.DisplayName);
        ArgumentException.ThrowIfNullOrWhiteSpace(project.RootPath);

        await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);

        string normalizedRoot = PathIdentity.Normalize(project.RootPath);
        string fingerprint = PathIdentity.CreateStableId(normalizedRoot);
        DateTimeOffset createdAt = project.CreatedAt == default ? DateTimeOffset.UtcNow : project.CreatedAt;

        await using var connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
        await using var command = connection.CreateCommand();
        command.CommandText = """
            INSERT INTO projects (
                id, display_name, root_path, path_fingerprint, default_cli,
                default_connection_profile_id, default_model, resume_policy,
                created_at, last_opened_at, is_archived)
            VALUES (
                $id, $display_name, $root_path, $path_fingerprint, $default_cli,
                $default_connection_profile_id, $default_model, $resume_policy,
                $created_at, $last_opened_at, $is_archived)
            ON CONFLICT(id) DO UPDATE SET
                display_name = excluded.display_name,
                root_path = excluded.root_path,
                path_fingerprint = excluded.path_fingerprint,
                default_cli = excluded.default_cli,
                default_connection_profile_id = excluded.default_connection_profile_id,
                default_model = excluded.default_model,
                resume_policy = excluded.resume_policy,
                last_opened_at = excluded.last_opened_at,
                is_archived = excluded.is_archived
            ON CONFLICT(path_fingerprint) DO UPDATE SET
                display_name = excluded.display_name,
                root_path = excluded.root_path,
                default_cli = excluded.default_cli,
                default_connection_profile_id = excluded.default_connection_profile_id,
                default_model = excluded.default_model,
                resume_policy = excluded.resume_policy,
                last_opened_at = excluded.last_opened_at,
                is_archived = excluded.is_archived;
            """;

        command.Parameters.AddWithValue("$id", project.Id);
        command.Parameters.AddWithValue("$display_name", project.DisplayName.Trim());
        command.Parameters.AddWithValue("$root_path", normalizedRoot);
        command.Parameters.AddWithValue("$path_fingerprint", fingerprint);
        command.Parameters.AddWithValue("$default_cli", (int)project.DefaultCli);
        command.Parameters.AddWithValue(
            "$default_connection_profile_id",
            (object?)NullIfWhiteSpace(project.DefaultConnectionProfileId) ?? DBNull.Value);
        command.Parameters.AddWithValue(
            "$default_model",
            (object?)NullIfWhiteSpace(project.DefaultModel) ?? DBNull.Value);
        command.Parameters.AddWithValue("$resume_policy", (int)project.ResumePolicy);
        command.Parameters.AddWithValue("$created_at", FormatTimestamp(createdAt));
        command.Parameters.AddWithValue(
            "$last_opened_at",
            project.LastOpenedAt is { } lastOpenedAt
                ? FormatTimestamp(lastOpenedAt)
                : DBNull.Value);
        command.Parameters.AddWithValue("$is_archived", project.IsArchived ? 1 : 0);

        await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task<bool> DeleteAsync(
        string id,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(id);
        await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);

        await using var connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
        await using var command = connection.CreateCommand();
        command.CommandText = "DELETE FROM projects WHERE id = $id;";
        command.Parameters.AddWithValue("$id", id);
        return await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false) > 0;
    }

    public void Dispose() => _initializationGate.Dispose();

    private async Task EnsureInitializedAsync(CancellationToken cancellationToken)
    {
        if (_initialized)
        {
            return;
        }

        await _initializationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (_initialized)
            {
                return;
            }

            Directory.CreateDirectory(_databaseDirectory);

            await using var connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
            await using var command = connection.CreateCommand();
            command.CommandText = """
                PRAGMA journal_mode = WAL;
                PRAGMA foreign_keys = ON;
                PRAGMA busy_timeout = 5000;

                CREATE TABLE IF NOT EXISTS projects (
                    id TEXT NOT NULL PRIMARY KEY,
                    display_name TEXT NOT NULL,
                    root_path TEXT NOT NULL,
                    path_fingerprint TEXT NOT NULL UNIQUE,
                    default_cli INTEGER NOT NULL,
                    default_connection_profile_id TEXT NULL,
                    default_model TEXT NULL,
                    resume_policy INTEGER NOT NULL,
                    created_at TEXT NOT NULL,
                    last_opened_at TEXT NULL,
                    is_archived INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0, 1))
                );

                CREATE INDEX IF NOT EXISTS ix_projects_recent
                    ON projects(is_archived, last_opened_at, created_at);

                PRAGMA user_version = 1;
                """;
            await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
            _initialized = true;
        }
        finally
        {
            _initializationGate.Release();
        }
    }

    private async Task<SqliteConnection> OpenConnectionAsync(CancellationToken cancellationToken)
    {
        var connection = new SqliteConnection(_connectionString);
        try
        {
            await connection.OpenAsync(cancellationToken).ConfigureAwait(false);

            await using var command = connection.CreateCommand();
            command.CommandText = "PRAGMA busy_timeout = 5000; PRAGMA foreign_keys = ON;";
            await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
            return connection;
        }
        catch
        {
            await connection.DisposeAsync().ConfigureAwait(false);
            throw;
        }
    }

    private static ProjectRecord ReadProject(SqliteDataReader reader)
    {
        return new ProjectRecord
        {
            Id = reader.GetString(0),
            DisplayName = reader.GetString(1),
            RootPath = reader.GetString(2),
            PathFingerprint = reader.GetString(3),
            DefaultCli = ReadEnum<CliKind>(reader.GetInt32(4), CliKind.Codex),
            DefaultConnectionProfileId = reader.IsDBNull(5) ? null : reader.GetString(5),
            DefaultModel = reader.IsDBNull(6) ? null : reader.GetString(6),
            ResumePolicy = ReadEnum<ResumePolicy>(reader.GetInt32(7), ResumePolicy.CurrentConnection),
            CreatedAt = ParseTimestamp(reader.GetString(8)),
            LastOpenedAt = reader.IsDBNull(9) ? null : ParseTimestamp(reader.GetString(9)),
            IsArchived = reader.GetInt32(10) != 0,
        };
    }

    private static TEnum ReadEnum<TEnum>(int value, TEnum fallback)
        where TEnum : struct, Enum
        => Enum.IsDefined(typeof(TEnum), value) ? (TEnum)(object)value : fallback;

    private static string FormatTimestamp(DateTimeOffset value)
        => value.ToUniversalTime().ToString("O", CultureInfo.InvariantCulture);

    private static DateTimeOffset ParseTimestamp(string value)
        => DateTimeOffset.Parse(value, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind);

    private static string? NullIfWhiteSpace(string? value)
        => string.IsNullOrWhiteSpace(value) ? null : value.Trim();
}
