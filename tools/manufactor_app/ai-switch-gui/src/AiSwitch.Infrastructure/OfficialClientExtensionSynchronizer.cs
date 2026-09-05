using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

public interface IOfficialClientExtensionSynchronizer
{
    Task SynchronizeAsync(
        WorkspaceFeatureState previous,
        WorkspaceFeatureState current,
        CancellationToken cancellationToken = default);
}

public sealed partial class OfficialClientExtensionSynchronizer : IOfficialClientExtensionSynchronizer, IDisposable
{
    private const string McpBlockStart = "# BEGIN LANAI WORKSPACE MCP";
    private const string McpBlockEnd = "# END LANAI WORKSPACE MCP";
    private const string SkillMarkerFileName = ".lanai-managed.json";

    private readonly AppDataPaths _paths;
    private readonly RollingBackupService _backups;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private bool _disposed;

    public OfficialClientExtensionSynchronizer(AppDataPaths paths)
    {
        _paths = paths ?? throw new ArgumentNullException(nameof(paths));
        _backups = new RollingBackupService(paths.BackupsDirectory);
    }

    public async Task SynchronizeAsync(
        WorkspaceFeatureState previous,
        WorkspaceFeatureState current,
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        ArgumentNullException.ThrowIfNull(previous);
        ArgumentNullException.ThrowIfNull(current);

        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            var failures = new List<string>();
            await CollectFailureAsync(failures, "MCP", () => SynchronizeMcpAsync(previous, current, cancellationToken)).ConfigureAwait(false);
            await CollectFailureAsync(failures, "提示词", () => SynchronizePromptsAsync(current, cancellationToken)).ConfigureAwait(false);
            await CollectFailureAsync(failures, "Skills", () => SynchronizeSkillsAsync(previous, current, cancellationToken)).ConfigureAwait(false);
            if (failures.Count > 0)
            {
                throw new InvalidOperationException($"部分客户端扩展同步失败：{string.Join("；", failures)}");
            }
        }
        finally
        {
            _gate.Release();
        }
    }

    private async Task SynchronizeMcpAsync(
        WorkspaceFeatureState previous,
        WorkspaceFeatureState current,
        CancellationToken cancellationToken)
    {
        string[] allManagedIds = previous.McpServers
            .Concat(current.McpServers)
            .Select(server => server.Id)
            .Where(id => !string.IsNullOrWhiteSpace(id))
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray();

        var failures = new List<string>();
        await CollectFailureAsync(failures, "Claude", () => SynchronizeJsonMcpAsync(
                _paths.ClaudeConfigPath,
                current.McpServers.Where(server => server.Targets.HasFlag(ManagedClientTargets.Claude)),
                allManagedIds,
                "claude-mcp",
                cancellationToken)).ConfigureAwait(false);
        await CollectFailureAsync(failures, "Gemini", () => SynchronizeJsonMcpAsync(
                _paths.GeminiConfigPath,
                current.McpServers.Where(server => server.Targets.HasFlag(ManagedClientTargets.Gemini)),
                allManagedIds,
                "gemini-mcp",
                cancellationToken)).ConfigureAwait(false);
        await CollectFailureAsync(failures, "Codex", () => SynchronizeCodexMcpAsync(
                current.McpServers.Where(server => server.Targets.HasFlag(ManagedClientTargets.Codex)),
                allManagedIds,
                cancellationToken)).ConfigureAwait(false);
        if (failures.Count > 0) throw new InvalidOperationException(string.Join("；", failures));
    }

    private async Task SynchronizeJsonMcpAsync(
        string path,
        IEnumerable<McpServerDefinition> enabledServers,
        IReadOnlyCollection<string> allManagedIds,
        string backupCategory,
        CancellationToken cancellationToken)
    {
        JsonObject root = await ReadJsonObjectAsync(path, cancellationToken).ConfigureAwait(false);
        JsonObject servers = root["mcpServers"] as JsonObject ?? new JsonObject();
        foreach (string id in allManagedIds)
        {
            RemovePropertyIgnoreCase(servers, id);
        }

        foreach (McpServerDefinition server in enabledServers)
        {
            ValidateMcp(server);
            servers[server.Id] = CreateJsonMcpNode(server);
        }

        if (servers.Count == 0)
        {
            RemovePropertyIgnoreCase(root, "mcpServers");
        }
        else
        {
            root["mcpServers"] = servers;
        }

        await WriteTextAtomicallyAsync(
                path,
                root.ToJsonString(new JsonSerializerOptions { WriteIndented = true }),
                backupCategory,
                cancellationToken)
            .ConfigureAwait(false);
    }

    private async Task SynchronizeCodexMcpAsync(
        IEnumerable<McpServerDefinition> enabledServers,
        IReadOnlyCollection<string> allManagedIds,
        CancellationToken cancellationToken)
    {
        string existing = File.Exists(_paths.CodexConfigPath)
            ? await File.ReadAllTextAsync(_paths.CodexConfigPath, cancellationToken).ConfigureAwait(false)
            : string.Empty;
        string withoutManagedBlock = RemoveMarkedBlock(existing, McpBlockStart, McpBlockEnd);
        string cleaned = RemoveCodexMcpSections(withoutManagedBlock, allManagedIds);

        var builder = new StringBuilder(cleaned.TrimEnd());
        McpServerDefinition[] servers = enabledServers.ToArray();
        if (servers.Length > 0)
        {
            if (builder.Length > 0)
            {
                builder.AppendLine().AppendLine();
            }

            builder.AppendLine(McpBlockStart);
            foreach (McpServerDefinition server in servers)
            {
                ValidateMcp(server);
                builder.Append("[mcp_servers.").Append(QuoteTomlKey(server.Id)).AppendLine("]");
                if (server.Transport == McpTransportKind.Stdio)
                {
                    builder.Append("command = ").AppendLine(QuoteToml(server.Command!));
                    if (server.Arguments.Count > 0)
                    {
                        builder.Append("args = [")
                            .Append(string.Join(", ", server.Arguments.Select(QuoteToml)))
                            .AppendLine("]");
                    }

                    AppendTomlMap(builder, "env", server.Environment);
                }
                else
                {
                    builder.Append("url = ").AppendLine(QuoteToml(server.Url!));
                    AppendTomlMap(builder, "http_headers", server.Headers);
                }

                builder.AppendLine();
            }

            builder.AppendLine(McpBlockEnd);
        }

        await WriteTextAtomicallyAsync(
                _paths.CodexConfigPath,
                builder.ToString().TrimEnd() + Environment.NewLine,
                "codex-config",
                cancellationToken)
            .ConfigureAwait(false);
    }

    private async Task SynchronizePromptsAsync(
        WorkspaceFeatureState state,
        CancellationToken cancellationToken)
    {
        PromptPresetDefinition? codex = state.PromptPresets.LastOrDefault(prompt => prompt.Targets.HasFlag(ManagedClientTargets.Codex));
        PromptPresetDefinition? claude = state.PromptPresets.LastOrDefault(prompt => prompt.Targets.HasFlag(ManagedClientTargets.Claude));
        PromptPresetDefinition? gemini = state.PromptPresets.LastOrDefault(prompt => prompt.Targets.HasFlag(ManagedClientTargets.Gemini));
        var failures = new List<string>();
        await CollectFailureAsync(failures, "Codex", () => SynchronizePromptFileAsync(_paths.CodexPromptPath, codex, "codex-prompt", cancellationToken)).ConfigureAwait(false);
        await CollectFailureAsync(failures, "Claude", () => SynchronizePromptFileAsync(_paths.ClaudePromptPath, claude, "claude-prompt", cancellationToken)).ConfigureAwait(false);
        await CollectFailureAsync(failures, "Gemini", () => SynchronizePromptFileAsync(_paths.GeminiPromptPath, gemini, "gemini-prompt", cancellationToken)).ConfigureAwait(false);
        if (failures.Count > 0) throw new InvalidOperationException(string.Join("；", failures));
    }

    private async Task SynchronizePromptFileAsync(
        string path,
        PromptPresetDefinition? prompt,
        string backupCategory,
        CancellationToken cancellationToken)
    {
        if (prompt is null && !File.Exists(path))
        {
            return;
        }

        await WriteTextAtomicallyAsync(
                path,
                prompt is null ? string.Empty : prompt.Markdown.TrimEnd() + Environment.NewLine,
                backupCategory,
                cancellationToken)
            .ConfigureAwait(false);
    }

    private async Task SynchronizeSkillsAsync(
        WorkspaceFeatureState previous,
        WorkspaceFeatureState current,
        CancellationToken cancellationToken)
    {
        ManagedSkillDefinition[] union = previous.Skills
            .Concat(current.Skills)
            .GroupBy(skill => skill.Id, StringComparer.OrdinalIgnoreCase)
            .Select(group => group.Last())
            .ToArray();
        foreach (ManagedSkillDefinition skill in union)
        {
            ManagedSkillDefinition? active = current.Skills.FirstOrDefault(item =>
                string.Equals(item.Id, skill.Id, StringComparison.OrdinalIgnoreCase));
            ManagedSkillDefinition effective = active ?? skill with { Targets = ManagedClientTargets.None };
            var failures = new List<string>();
            await CollectFailureAsync(failures, $"Codex/{skill.Name}", () => SynchronizeSkillTargetAsync(effective, _paths.CodexSkillsDirectory, ManagedClientTargets.Codex, cancellationToken)).ConfigureAwait(false);
            await CollectFailureAsync(failures, $"Claude/{skill.Name}", () => SynchronizeSkillTargetAsync(effective, _paths.ClaudeSkillsDirectory, ManagedClientTargets.Claude, cancellationToken)).ConfigureAwait(false);
            await CollectFailureAsync(failures, $"Gemini/{skill.Name}", () => SynchronizeSkillTargetAsync(effective, _paths.GeminiSkillsDirectory, ManagedClientTargets.Gemini, cancellationToken)).ConfigureAwait(false);
            if (failures.Count > 0) throw new InvalidOperationException(string.Join("；", failures));
        }
    }

    private async Task SynchronizeSkillTargetAsync(
        ManagedSkillDefinition skill,
        string clientSkillsRoot,
        ManagedClientTargets target,
        CancellationToken cancellationToken)
    {
        string targetPath = SafeChildPath(clientSkillsRoot, skill.Name);
        if (!skill.Targets.HasFlag(target))
        {
            DeleteManagedSkillDirectory(targetPath, skill.Id);
            return;
        }

        string sourcePath = SafeChildPath(_paths.ManagedSkillsDirectory, skill.StorageDirectoryName);
        if (!Directory.Exists(sourcePath))
        {
            throw new DirectoryNotFoundException($"技能源目录不存在：{skill.Name}");
        }

        if (Directory.Exists(targetPath) && !IsManagedSkillDirectory(targetPath, skill.Id))
        {
            throw new InvalidOperationException($"{skill.Name} 已存在于客户端目录且不受工作台管理。请先改名或导入现有技能。");
        }

        Directory.CreateDirectory(clientSkillsRoot);
        await File.WriteAllTextAsync(
                Path.Combine(sourcePath, SkillMarkerFileName),
                JsonSerializer.Serialize(new { skill_id = skill.Id }),
                cancellationToken)
            .ConfigureAwait(false);
        if (Directory.Exists(targetPath))
        {
            Directory.Delete(targetPath, recursive: true);
        }

        try
        {
            Directory.CreateSymbolicLink(targetPath, sourcePath);
            return;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or NotSupportedException)
        {
            // CC Switch uses symlink first and falls back to a managed copy on
            // Windows machines where developer mode/privilege is unavailable.
        }

        string temporaryPath = $"{targetPath}.lanai-{Guid.NewGuid():N}.tmp";
        try
        {
            CopyDirectory(sourcePath, temporaryPath, cancellationToken);
            await File.WriteAllTextAsync(
                    Path.Combine(temporaryPath, SkillMarkerFileName),
                    JsonSerializer.Serialize(new { skill_id = skill.Id }),
                    cancellationToken)
                .ConfigureAwait(false);
            Directory.Move(temporaryPath, targetPath);
        }
        finally
        {
            if (Directory.Exists(temporaryPath))
            {
                Directory.Delete(temporaryPath, recursive: true);
            }
        }
    }

    private async Task WriteTextAtomicallyAsync(
        string path,
        string content,
        string backupCategory,
        CancellationToken cancellationToken)
    {
        string? directory = Path.GetDirectoryName(path);
        if (string.IsNullOrWhiteSpace(directory))
        {
            throw new InvalidOperationException("配置文件路径缺少目录。 ");
        }

        Directory.CreateDirectory(directory);
        await _backups.BackupFileAsync(path, backupCategory, cancellationToken).ConfigureAwait(false);
        string temporaryPath = $"{path}.{Guid.NewGuid():N}.tmp";
        try
        {
            await File.WriteAllTextAsync(temporaryPath, content, new UTF8Encoding(false), cancellationToken).ConfigureAwait(false);
            if (File.Exists(path))
            {
                File.Replace(temporaryPath, path, destinationBackupFileName: null, ignoreMetadataErrors: true);
            }
            else
            {
                File.Move(temporaryPath, path);
            }
        }
        finally
        {
            if (File.Exists(temporaryPath))
            {
                File.Delete(temporaryPath);
            }
        }
    }

    private static async Task<JsonObject> ReadJsonObjectAsync(string path, CancellationToken cancellationToken)
    {
        if (!File.Exists(path))
        {
            return new JsonObject();
        }

        string json = await File.ReadAllTextAsync(path, cancellationToken).ConfigureAwait(false);
        if (string.IsNullOrWhiteSpace(json))
        {
            return new JsonObject();
        }

        try
        {
            return JsonNode.Parse(json) as JsonObject
                ?? throw new InvalidDataException($"配置文件根节点必须是 JSON 对象：{path}");
        }
        catch (JsonException exception)
        {
            throw new InvalidDataException($"配置文件不是有效 JSON：{path}", exception);
        }
    }

    private static JsonObject CreateJsonMcpNode(McpServerDefinition server)
    {
        var node = new JsonObject();
        if (server.Transport == McpTransportKind.Stdio)
        {
            node["command"] = server.Command;
            if (server.Arguments.Count > 0)
            {
                node["args"] = new JsonArray(server.Arguments.Select(argument => JsonValue.Create(argument)).ToArray());
            }

            AddJsonMap(node, "env", server.Environment);
        }
        else
        {
            node["url"] = server.Url;
            if (server.Transport == McpTransportKind.Sse)
            {
                node["type"] = "sse";
            }

            AddJsonMap(node, "headers", server.Headers);
        }

        return node;
    }

    private static void AddJsonMap(JsonObject node, string propertyName, IReadOnlyDictionary<string, string> values)
    {
        if (values.Count == 0)
        {
            return;
        }

        var map = new JsonObject();
        foreach ((string key, string value) in values)
        {
            map[key] = ResolveConfiguredValue(key, value);
        }

        node[propertyName] = map;
    }

    private static void AppendTomlMap(StringBuilder builder, string propertyName, IReadOnlyDictionary<string, string> values)
    {
        if (values.Count == 0)
        {
            return;
        }

        builder.Append(propertyName).Append(" = { ")
            .Append(string.Join(", ", values.Select(pair =>
                $"{QuoteTomlKey(pair.Key)} = {QuoteToml(ResolveConfiguredValue(pair.Key, pair.Value))}")))
            .AppendLine(" }");
    }

    private static string ResolveConfiguredValue(string key, string value)
    {
        string trimmed = value.Trim();
        if (trimmed.StartsWith("env:", StringComparison.OrdinalIgnoreCase))
        {
            string variable = trimmed[4..].Trim();
            if (string.IsNullOrWhiteSpace(variable))
            {
                throw new InvalidOperationException($"{key} 的环境变量引用为空。 ");
            }

            return Environment.GetEnvironmentVariable(variable)
                ?? throw new InvalidOperationException($"环境变量 {variable} 尚未设置。 ");
        }

        if (SensitiveKeyPattern().IsMatch(key) && !string.IsNullOrEmpty(trimmed))
        {
            throw new InvalidOperationException($"{key} 可能包含凭据，请填写 env:环境变量名，工作台不会明文保存该值。 ");
        }

        return value;
    }

    private static void ValidateMcp(McpServerDefinition server)
    {
        if (!Regex.IsMatch(server.Id, "^[A-Za-z0-9._-]{1,80}$"))
        {
            throw new InvalidOperationException("MCP ID 只能包含字母、数字、点、下划线和短横线。 ");
        }

        if (server.Transport == McpTransportKind.Stdio && string.IsNullOrWhiteSpace(server.Command))
        {
            throw new InvalidOperationException($"MCP {server.Name} 缺少启动命令。 ");
        }

        if (server.Transport != McpTransportKind.Stdio &&
            (!Uri.TryCreate(server.Url, UriKind.Absolute, out Uri? uri) || uri.Scheme is not ("http" or "https")))
        {
            throw new InvalidOperationException($"MCP {server.Name} 的 URL 必须是 HTTP 或 HTTPS 地址。 ");
        }
    }

    private static string RemoveMarkedBlock(string text, string start, string end)
    {
        int startIndex = text.IndexOf(start, StringComparison.Ordinal);
        while (startIndex >= 0)
        {
            int endIndex = text.IndexOf(end, startIndex + start.Length, StringComparison.Ordinal);
            int removeEnd = endIndex < 0 ? text.Length : endIndex + end.Length;
            if (removeEnd < text.Length && text[removeEnd] == '\r') removeEnd++;
            if (removeEnd < text.Length && text[removeEnd] == '\n') removeEnd++;
            text = text.Remove(startIndex, removeEnd - startIndex);
            startIndex = text.IndexOf(start, StringComparison.Ordinal);
        }

        return text;
    }

    private static string RemoveCodexMcpSections(string text, IReadOnlyCollection<string> ids)
    {
        if (ids.Count == 0 || string.IsNullOrWhiteSpace(text))
        {
            return text;
        }

        string[] lines = text.Replace("\r\n", "\n", StringComparison.Ordinal).Split('\n');
        var output = new List<string>(lines.Length);
        bool skip = false;
        foreach (string line in lines)
        {
            Match section = TomlSectionPattern().Match(line);
            if (section.Success)
            {
                string name = section.Groups.Cast<Group>()
                    .Skip(1)
                    .First(group => group.Success)
                    .Value;
                skip = ids.Any(id => string.Equals(id, name, StringComparison.OrdinalIgnoreCase));
            }

            if (!skip)
            {
                output.Add(line);
            }
        }

        return string.Join(Environment.NewLine, output).TrimEnd();
    }

    private static string QuoteToml(string value) =>
        $"\"{value.Replace("\\", "\\\\", StringComparison.Ordinal).Replace("\"", "\\\"", StringComparison.Ordinal).Replace("\r", "\\r", StringComparison.Ordinal).Replace("\n", "\\n", StringComparison.Ordinal)}\"";

    private static string QuoteTomlKey(string value) => QuoteToml(value);

    private static void RemovePropertyIgnoreCase(JsonObject node, string propertyName)
    {
        string? key = node.Select(pair => pair.Key).FirstOrDefault(key =>
            string.Equals(key, propertyName, StringComparison.OrdinalIgnoreCase));
        if (key is not null)
        {
            node.Remove(key);
        }
    }

    private static async Task RestoreFilesAsync(
        IReadOnlyDictionary<string, byte[]?> snapshots,
        CancellationToken cancellationToken)
    {
        foreach ((string path, byte[]? bytes) in snapshots)
        {
            string? directory = Path.GetDirectoryName(path);
            if (!string.IsNullOrWhiteSpace(directory))
            {
                Directory.CreateDirectory(directory);
            }

            if (bytes is null)
            {
                if (File.Exists(path)) File.Delete(path);
            }
            else
            {
                await File.WriteAllBytesAsync(path, bytes, cancellationToken).ConfigureAwait(false);
            }
        }
    }

    private static async Task CollectFailureAsync(List<string> failures, string label, Func<Task> operation)
    {
        try
        {
            await operation().ConfigureAwait(false);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidDataException or InvalidOperationException or NotSupportedException)
        {
            failures.Add($"{label}: {exception.Message}");
        }
    }

    private static string SafeChildPath(string parent, string child)
    {
        string parentFull = Path.GetFullPath(parent).TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar;
        string childFull = Path.GetFullPath(Path.Combine(parentFull, child));
        if (!childFull.StartsWith(parentFull, StringComparison.OrdinalIgnoreCase))
        {
            throw new InvalidOperationException("技能目录超出允许的存储范围。 ");
        }

        return childFull;
    }

    private static void CopyDirectory(string source, string destination, CancellationToken cancellationToken)
    {
        Directory.CreateDirectory(destination);
        foreach (string file in Directory.EnumerateFiles(source, "*", SearchOption.TopDirectoryOnly))
        {
            cancellationToken.ThrowIfCancellationRequested();
            File.Copy(file, Path.Combine(destination, Path.GetFileName(file)), overwrite: true);
        }

        foreach (string directory in Directory.EnumerateDirectories(source, "*", SearchOption.TopDirectoryOnly))
        {
            cancellationToken.ThrowIfCancellationRequested();
            CopyDirectory(directory, Path.Combine(destination, Path.GetFileName(directory)), cancellationToken);
        }
    }

    private static bool IsManagedSkillDirectory(string path, string skillId)
    {
        string marker = Path.Combine(path, SkillMarkerFileName);
        if (!File.Exists(marker)) return false;
        try
        {
            JsonObject? data = JsonNode.Parse(File.ReadAllText(marker)) as JsonObject;
            return string.Equals(data?["skill_id"]?.GetValue<string>(), skillId, StringComparison.OrdinalIgnoreCase);
        }
        catch
        {
            return false;
        }
    }

    private static void DeleteManagedSkillDirectory(string path, string skillId)
    {
        if (Directory.Exists(path) && IsManagedSkillDirectory(path, skillId))
        {
            Directory.Delete(path, recursive: true);
        }
    }

    [GeneratedRegex("(token|api[_-]?key|secret|password|authorization|cookie)", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant)]
    private static partial Regex SensitiveKeyPattern();

    private static Regex TomlSectionPattern() => new("^\\s*\\[mcp_servers\\.(?:\\\"([^\\\"]+)\\\"|'([^']+)'|([^]\\s]+))(?:\\.[^]]+)?\\]\\s*$", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _gate.Dispose();
    }
}
