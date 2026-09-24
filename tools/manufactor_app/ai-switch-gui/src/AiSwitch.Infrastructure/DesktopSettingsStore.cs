using System.Text.Json;
using System.Text.Json.Serialization;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

public interface IDesktopSettingsStore
{
    Task<WorkspaceDesktopSettings> LoadAsync(CancellationToken cancellationToken = default);

    Task SaveAsync(WorkspaceDesktopSettings settings, CancellationToken cancellationToken = default);
}

/// <summary>
/// Atomic settings persistence following the same temp-file/replace pattern
/// used by CC Switch configuration writes.
/// </summary>
public sealed class DesktopSettingsStore : IDesktopSettingsStore, IDisposable
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

    public DesktopSettingsStore(AppDataPaths paths)
    {
        ArgumentNullException.ThrowIfNull(paths);
        _path = paths.DesktopSettingsPath;
        _backups = new RollingBackupService(paths.BackupsDirectory);
    }

    public async Task<WorkspaceDesktopSettings> LoadAsync(CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (!File.Exists(_path))
            {
                return new WorkspaceDesktopSettings();
            }

            await using FileStream stream = File.OpenRead(_path);
            return await JsonSerializer.DeserializeAsync<WorkspaceDesktopSettings>(stream, JsonOptions, cancellationToken)
                       .ConfigureAwait(false)
                   ?? new WorkspaceDesktopSettings();
        }
        catch (JsonException exception)
        {
            throw new InvalidDataException("工作台设置不是有效的 JSON。", exception);
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task SaveAsync(WorkspaceDesktopSettings settings, CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        ArgumentNullException.ThrowIfNull(settings);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            string directory = Path.GetDirectoryName(_path)
                               ?? throw new InvalidOperationException("设置文件路径缺少目录。");
            Directory.CreateDirectory(directory);
            await _backups.BackupFileAsync(_path, "desktop-settings", cancellationToken).ConfigureAwait(false);
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
                    await JsonSerializer.SerializeAsync(stream, settings with { SchemaVersion = 1 }, JsonOptions, cancellationToken)
                        .ConfigureAwait(false);
                    await stream.FlushAsync(cancellationToken).ConfigureAwait(false);
                }

                if (File.Exists(_path))
                {
                    File.Replace(temporaryPath, _path, null, true);
                }
                else
                {
                    File.Move(temporaryPath, _path);
                }
            }
            finally
            {
                if (File.Exists(temporaryPath)) File.Delete(temporaryPath);
            }
        }
        finally
        {
            _gate.Release();
        }
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _gate.Dispose();
    }
}
