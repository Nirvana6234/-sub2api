using System.Text.Json;
using System.Text.Json.Serialization;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

public interface IWorkspaceFeatureStore
{
    Task<WorkspaceFeatureState> LoadAsync(CancellationToken cancellationToken = default);

    Task SaveAsync(WorkspaceFeatureState state, CancellationToken cancellationToken = default);
}

public sealed class WorkspaceFeatureStore : IWorkspaceFeatureStore, IDisposable
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        WriteIndented = true,
        Converters = { new JsonStringEnumConverter(JsonNamingPolicy.SnakeCaseLower) },
    };

    private readonly string _path;
    private readonly RollingBackupService _backups;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private bool _disposed;

    public WorkspaceFeatureStore(AppDataPaths paths)
    {
        ArgumentNullException.ThrowIfNull(paths);
        _path = paths.FeatureStatePath;
        _backups = new RollingBackupService(paths.BackupsDirectory);
    }

    public async Task<WorkspaceFeatureState> LoadAsync(CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (!File.Exists(_path))
            {
                return new WorkspaceFeatureState();
            }

            await using FileStream stream = new(
                _path,
                FileMode.Open,
                FileAccess.Read,
                FileShare.Read,
                16 * 1024,
                FileOptions.Asynchronous | FileOptions.SequentialScan);
            WorkspaceFeatureState? state = await JsonSerializer
                .DeserializeAsync<WorkspaceFeatureState>(stream, JsonOptions, cancellationToken)
                .ConfigureAwait(false);
            return Normalize(state ?? new WorkspaceFeatureState());
        }
        catch (JsonException exception)
        {
            throw new InvalidDataException("工作台扩展配置不是有效的 JSON。", exception);
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task SaveAsync(WorkspaceFeatureState state, CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        ArgumentNullException.ThrowIfNull(state);
        WorkspaceFeatureState normalized = Normalize(state);

        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            string? directory = Path.GetDirectoryName(_path);
            if (string.IsNullOrWhiteSpace(directory))
            {
                throw new InvalidOperationException("扩展配置路径缺少目录。 ");
            }

            Directory.CreateDirectory(directory);
            await _backups.BackupFileAsync(_path, "workspace-features", cancellationToken).ConfigureAwait(false);

            string temporaryPath = $"{_path}.{Guid.NewGuid():N}.tmp";
            try
            {
                await using (FileStream stream = new(
                                 temporaryPath,
                                 FileMode.CreateNew,
                                 FileAccess.Write,
                                 FileShare.None,
                                 16 * 1024,
                                 FileOptions.Asynchronous | FileOptions.WriteThrough))
                {
                    await JsonSerializer.SerializeAsync(stream, normalized, JsonOptions, cancellationToken)
                        .ConfigureAwait(false);
                    await stream.FlushAsync(cancellationToken).ConfigureAwait(false);
                }

                if (File.Exists(_path))
                {
                    File.Replace(temporaryPath, _path, destinationBackupFileName: null, ignoreMetadataErrors: true);
                }
                else
                {
                    File.Move(temporaryPath, _path);
                }
            }
            finally
            {
                TryDelete(temporaryPath);
            }
        }
        finally
        {
            _gate.Release();
        }
    }

    private static WorkspaceFeatureState Normalize(WorkspaceFeatureState state)
    {
        return state with
        {
            SchemaVersion = 1,
            McpServers = state.McpServers
                .Where(item => !string.IsNullOrWhiteSpace(item.Id) && !string.IsNullOrWhiteSpace(item.Name))
                .GroupBy(item => item.Id.Trim(), StringComparer.OrdinalIgnoreCase)
                .Select(group => group.Last() with { Id = group.Key, Name = group.Last().Name.Trim() })
                .OrderBy(item => item.Name, StringComparer.CurrentCultureIgnoreCase)
                .ToArray(),
            PromptPresets = state.PromptPresets
                .Where(item => !string.IsNullOrWhiteSpace(item.Id) && !string.IsNullOrWhiteSpace(item.Name))
                .GroupBy(item => item.Id.Trim(), StringComparer.OrdinalIgnoreCase)
                .Select(group => group.Last() with { Id = group.Key, Name = group.Last().Name.Trim() })
                .OrderBy(item => item.Name, StringComparer.CurrentCultureIgnoreCase)
                .ToArray(),
            Skills = state.Skills
                .Where(item => !string.IsNullOrWhiteSpace(item.Id) && !string.IsNullOrWhiteSpace(item.Name))
                .GroupBy(item => item.Id.Trim(), StringComparer.OrdinalIgnoreCase)
                .Select(group => group.Last() with { Id = group.Key, Name = group.Last().Name.Trim() })
                .OrderBy(item => item.Name, StringComparer.CurrentCultureIgnoreCase)
                .ToArray(),
            ProjectProfiles = state.ProjectProfiles
                .Where(item => !string.IsNullOrWhiteSpace(item.ProjectId))
                .GroupBy(item => item.ProjectId.Trim(), StringComparer.OrdinalIgnoreCase)
                .Select(group => group.OrderBy(item => item.UpdatedAt).Last() with { ProjectId = group.Key })
                .ToArray(),
        };
    }

    private static void TryDelete(string path)
    {
        try
        {
            if (File.Exists(path))
            {
                File.Delete(path);
            }
        }
        catch
        {
            // A stale temporary file must not mask the original result.
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _gate.Dispose();
    }
}

public sealed class RollingBackupService
{
    private const int DefaultRetentionCount = 20;
    private readonly string _backupDirectory;
    private readonly int _retentionCount;

    public RollingBackupService(string backupDirectory, int retentionCount = DefaultRetentionCount)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(backupDirectory);
        ArgumentOutOfRangeException.ThrowIfLessThan(retentionCount, 1);
        _backupDirectory = Path.GetFullPath(backupDirectory);
        _retentionCount = retentionCount;
    }

    public async Task<string?> BackupFileAsync(
        string sourcePath,
        string category,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(sourcePath);
        ArgumentException.ThrowIfNullOrWhiteSpace(category);
        if (!File.Exists(sourcePath))
        {
            return null;
        }

        Directory.CreateDirectory(_backupDirectory);
        string safeCategory = string.Concat(category.Select(character =>
            char.IsLetterOrDigit(character) || character is '-' or '_' ? character : '_'));
        string destination = Path.Combine(
            _backupDirectory,
            $"{safeCategory}-{DateTime.UtcNow:yyyyMMdd-HHmmssfff}-{Guid.NewGuid():N}.bak");

        await using (FileStream source = new(sourcePath, FileMode.Open, FileAccess.Read, FileShare.ReadWrite, 16 * 1024, true))
        await using (FileStream target = new(destination, FileMode.CreateNew, FileAccess.Write, FileShare.None, 16 * 1024, true))
        {
            await source.CopyToAsync(target, cancellationToken).ConfigureAwait(false);
            await target.FlushAsync(cancellationToken).ConfigureAwait(false);
        }

        foreach (FileInfo stale in new DirectoryInfo(_backupDirectory)
                     .EnumerateFiles($"{safeCategory}-*.bak")
                     .OrderByDescending(file => file.CreationTimeUtc)
                     .Skip(_retentionCount))
        {
            try
            {
                stale.Delete();
            }
            catch
            {
                // Retention cleanup is best effort.
            }
        }

        return destination;
    }
}
