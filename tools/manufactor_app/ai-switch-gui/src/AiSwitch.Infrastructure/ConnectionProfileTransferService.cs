using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Safe provider import/export translated from CC Switch's provider backup
/// workflow. Exports never contain credentials; imports merge cloud sources
/// while preserving the two fixed local sources and current routing.
/// </summary>
public sealed class ConnectionProfileTransferService
{
    private const int SourceBackupRetentionCount = 5;
    private static readonly JsonSerializerOptions JsonOptions = new() { WriteIndented = true };
    private static readonly string[] SensitiveNames =
    [
        "secret", "token", "api_key", "apikey", "password", "authorization", "cookie",
    ];

    private readonly string _profilesPath;
    private readonly string _backupDirectory;
    private readonly RollingBackupService _backups;
    private readonly SemaphoreSlim _gate = new(1, 1);

    public ConnectionProfileTransferService(AppDataPaths paths)
    {
        ArgumentNullException.ThrowIfNull(paths);
        _profilesPath = paths.LegacyProfilesPath;
        _backupDirectory = paths.BackupsDirectory;
        _backups = new RollingBackupService(paths.BackupsDirectory, SourceBackupRetentionCount);
    }

    public async Task ExportSafeAsync(string destination, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(destination);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            JsonObject root = await ReadObjectAsync(_profilesPath, cancellationToken).ConfigureAwait(false);
            RemoveSensitiveValues(root);
            JsonArray clouds = FindArray(root, "CloudSources") ?? new JsonArray();
            var export = new JsonObject
            {
                ["schema_version"] = 1,
                ["product"] = "LanAi.Workspace",
                ["exported_at"] = DateTimeOffset.UtcNow,
                ["cloud_sources"] = clouds.DeepClone(),
            };
            await WriteAtomicAsync(destination, export.ToJsonString(JsonOptions), cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task<int> ImportSafeAsync(string source, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(source);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            JsonObject imported = await ReadObjectAsync(source, cancellationToken).ConfigureAwait(false);
            RemoveSensitiveValues(imported);
            JsonArray incoming = FindArray(imported, "cloud_sources")
                                 ?? FindArray(imported, "CloudSources")
                                 ?? throw new InvalidDataException("导入文件没有 cloud_sources。");
            JsonObject current = await ReadObjectAsync(_profilesPath, cancellationToken).ConfigureAwait(false);
            JsonArray existing = FindArray(current, "CloudSources") ?? new JsonArray();
            SetNode(current, "CloudSources", existing);

            int importedCount = 0;
            foreach (JsonNode? node in incoming)
            {
                if (node is not JsonObject profile) continue;
                string id = GetString(profile, "Id") ?? GetString(profile, "id") ?? Guid.NewGuid().ToString("N");
                if (string.Equals(id, "local-machine", StringComparison.OrdinalIgnoreCase) ||
                    string.Equals(id, "lan-default", StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }

                SetNode(profile, "Id", JsonValue.Create(id));
                int index = FindProfileIndex(existing, id);
                if (index >= 0) existing[index] = profile.DeepClone();
                else existing.Add(profile.DeepClone());
                importedCount++;
            }

            await _backups.BackupFileAsync(_profilesPath, "connection-profiles", cancellationToken).ConfigureAwait(false);
            await WriteAtomicAsync(_profilesPath, current.ToJsonString(JsonOptions), cancellationToken).ConfigureAwait(false);
            return importedCount;
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task<bool> RestoreLatestAsync(CancellationToken cancellationToken = default)
    {
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            string? backup = Directory.Exists(_backupDirectory)
                ? Directory.EnumerateFiles(_backupDirectory, "connection-profiles-*.bak")
                    .OrderByDescending(File.GetLastWriteTimeUtc)
                    .FirstOrDefault()
                : null;
            if (backup is null) return false;
            await _backups.BackupFileAsync(_profilesPath, "connection-profiles-before-restore", cancellationToken)
                .ConfigureAwait(false);
            string content = await File.ReadAllTextAsync(backup, cancellationToken).ConfigureAwait(false);
            _ = JsonNode.Parse(content) as JsonObject
                ?? throw new InvalidDataException("来源备份不是有效的 JSON 对象。");
            await WriteAtomicAsync(_profilesPath, content, cancellationToken).ConfigureAwait(false);
            return true;
        }
        finally
        {
            _gate.Release();
        }
    }

    private static async Task<JsonObject> ReadObjectAsync(string path, CancellationToken cancellationToken)
    {
        if (!File.Exists(path)) return new JsonObject();
        string json = await File.ReadAllTextAsync(path, cancellationToken).ConfigureAwait(false);
        try
        {
            return JsonNode.Parse(json) as JsonObject
                   ?? throw new InvalidDataException("连接来源文件根节点必须是 JSON 对象。");
        }
        catch (JsonException exception)
        {
            throw new InvalidDataException("连接来源文件不是有效的 JSON。", exception);
        }
    }

    private static void RemoveSensitiveValues(JsonNode node)
    {
        if (node is JsonObject obj)
        {
            foreach (string key in obj.Select(pair => pair.Key).ToArray())
            {
                if (SensitiveNames.Any(name => Normalize(key).Contains(name, StringComparison.Ordinal)))
                {
                    obj.Remove(key);
                }
                else if (obj[key] is { } child)
                {
                    RemoveSensitiveValues(child);
                }
            }
        }
        else if (node is JsonArray array)
        {
            foreach (JsonNode? child in array)
            {
                if (child is not null) RemoveSensitiveValues(child);
            }
        }
    }

    private static string Normalize(string value) => value.Replace("-", "_", StringComparison.Ordinal).ToLowerInvariant();

    private static JsonArray? FindArray(JsonObject parent, string name) =>
        parent.FirstOrDefault(pair => string.Equals(pair.Key, name, StringComparison.OrdinalIgnoreCase)).Value as JsonArray;

    private static string? GetString(JsonObject parent, string name)
    {
        JsonNode? node = parent.FirstOrDefault(pair => string.Equals(pair.Key, name, StringComparison.OrdinalIgnoreCase)).Value;
        return node is JsonValue value && value.TryGetValue(out string? text) ? text : null;
    }

    private static void SetNode(JsonObject parent, string name, JsonNode? value)
    {
        string actual = parent.Select(pair => pair.Key).FirstOrDefault(key =>
                            string.Equals(key, name, StringComparison.OrdinalIgnoreCase)) ?? name;
        parent[actual] = value;
    }

    private static int FindProfileIndex(JsonArray profiles, string id)
    {
        for (int index = 0; index < profiles.Count; index++)
        {
            if (profiles[index] is JsonObject profile &&
                string.Equals(GetString(profile, "Id"), id, StringComparison.OrdinalIgnoreCase)) return index;
        }

        return -1;
    }

    private static async Task WriteAtomicAsync(string path, string content, CancellationToken cancellationToken)
    {
        string directory = Path.GetDirectoryName(path)
                           ?? throw new InvalidOperationException("目标文件缺少目录。");
        Directory.CreateDirectory(directory);
        string temporary = $"{path}.{Guid.NewGuid():N}.tmp";
        try
        {
            await File.WriteAllTextAsync(temporary, content, new UTF8Encoding(false), cancellationToken)
                .ConfigureAwait(false);
            if (File.Exists(path)) File.Replace(temporary, path, null, true);
            else File.Move(temporary, path);
        }
        finally
        {
            if (File.Exists(temporary)) File.Delete(temporary);
        }
    }
}
