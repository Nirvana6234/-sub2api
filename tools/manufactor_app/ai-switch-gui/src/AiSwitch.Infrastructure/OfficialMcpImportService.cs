using System.Text.Json.Nodes;
using System.Text.RegularExpressions;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

public sealed record McpImportResult(WorkspaceFeatureState State, int ImportedCount, IReadOnlyList<string> Warnings);

/// <summary>
/// Read-only MCP import translated from CC Switch's import_from_* services.
/// Import never writes back to live client files. Existing definitions only
/// gain the imported client target and are otherwise left unchanged.
/// </summary>
public sealed partial class OfficialMcpImportService
{
    private readonly AppDataPaths _paths;

    public OfficialMcpImportService(AppDataPaths paths) => _paths = paths ?? throw new ArgumentNullException(nameof(paths));

    public async Task<McpImportResult> ImportAllAsync(
        WorkspaceFeatureState state,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(state);
        var definitions = state.McpServers.ToDictionary(item => item.Id, StringComparer.OrdinalIgnoreCase);
        var warnings = new List<string>();
        int imported = 0;
        await ImportJsonAsync(_paths.ClaudeConfigPath, ManagedClientTargets.Claude, "Claude", definitions, warnings, cancellationToken).ConfigureAwait(false);
        await ImportCodexAsync(definitions, warnings, cancellationToken).ConfigureAwait(false);
        await ImportJsonAsync(_paths.GeminiConfigPath, ManagedClientTargets.Gemini, "Gemini", definitions, warnings, cancellationToken).ConfigureAwait(false);

        foreach (McpServerDefinition definition in definitions.Values)
        {
            if (!state.McpServers.Any(existing => string.Equals(existing.Id, definition.Id, StringComparison.OrdinalIgnoreCase))) imported++;
        }

        return new McpImportResult(
            state with { McpServers = definitions.Values.OrderBy(item => item.Name, StringComparer.CurrentCultureIgnoreCase).ToArray() },
            imported,
            warnings);
    }

    private static async Task ImportJsonAsync(
        string path,
        ManagedClientTargets target,
        string label,
        IDictionary<string, McpServerDefinition> definitions,
        ICollection<string> warnings,
        CancellationToken cancellationToken)
    {
        if (!File.Exists(path)) return;
        try
        {
            JsonObject? root = JsonNode.Parse(await File.ReadAllTextAsync(path, cancellationToken).ConfigureAwait(false)) as JsonObject;
            JsonObject? servers = root?["mcpServers"] as JsonObject;
            if (servers is null) return;
            foreach ((string id, JsonNode? node) in servers)
            {
                if (node is not JsonObject server || string.IsNullOrWhiteSpace(id)) continue;
                McpServerDefinition parsed = ParseJsonServer(id, server, target, label, warnings);
                Merge(definitions, parsed, target);
            }
        }
        catch (Exception exception) when (exception is IOException or System.Text.Json.JsonException or InvalidDataException)
        {
            warnings.Add($"{label}: {exception.Message}");
        }
    }

    private async Task ImportCodexAsync(
        IDictionary<string, McpServerDefinition> definitions,
        ICollection<string> warnings,
        CancellationToken cancellationToken)
    {
        if (!File.Exists(_paths.CodexConfigPath)) return;
        try
        {
            string text = await File.ReadAllTextAsync(_paths.CodexConfigPath, cancellationToken).ConfigureAwait(false);
            string[] lines = text.Replace("\r\n", "\n", StringComparison.Ordinal).Split('\n');
            string? id = null;
            var values = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            void Flush()
            {
                if (id is null) return;
                string? command = values.GetValueOrDefault("command");
                string? url = values.GetValueOrDefault("url");
                var parsed = new McpServerDefinition
                {
                    Id = id,
                    Name = id,
                    Transport = string.IsNullOrWhiteSpace(command) ? McpTransportKind.Http : McpTransportKind.Stdio,
                    Command = Unquote(command),
                    Url = Unquote(url),
                    Arguments = ParseTomlArray(values.GetValueOrDefault("args")),
                    Environment = ParseTomlMap(values.GetValueOrDefault("env"), "Codex", id, warnings),
                    Headers = ParseTomlMap(values.GetValueOrDefault("http_headers"), "Codex", id, warnings),
                    Targets = ManagedClientTargets.Codex,
                };
                Merge(definitions, parsed, ManagedClientTargets.Codex);
            }

            foreach (string line in lines)
            {
                Match section = CodexSectionPattern().Match(line);
                if (section.Success)
                {
                    Flush();
                    id = section.Groups[1].Success ? section.Groups[1].Value : section.Groups[2].Value;
                    values.Clear();
                    continue;
                }

                if (id is null) continue;
                if (line.TrimStart().StartsWith("[", StringComparison.Ordinal))
                {
                    Flush();
                    id = null;
                    values.Clear();
                    continue;
                }

                int separator = line.IndexOf('=');
                if (separator > 0) values[line[..separator].Trim()] = line[(separator + 1)..].Trim();
            }

            Flush();
        }
        catch (Exception exception) when (exception is IOException or InvalidDataException)
        {
            warnings.Add($"Codex: {exception.Message}");
        }
    }

    private static McpServerDefinition ParseJsonServer(
        string id,
        JsonObject node,
        ManagedClientTargets target,
        string label,
        ICollection<string> warnings)
    {
        string? command = node["command"]?.GetValue<string>();
        string? url = node["url"]?.GetValue<string>();
        McpTransportKind transport = !string.IsNullOrWhiteSpace(command)
            ? McpTransportKind.Stdio
            : string.Equals(node["type"]?.GetValue<string>(), "sse", StringComparison.OrdinalIgnoreCase)
                ? McpTransportKind.Sse
                : McpTransportKind.Http;
        return new McpServerDefinition
        {
            Id = id,
            Name = id,
            Transport = transport,
            Command = command,
            Url = url,
            Arguments = node["args"] is JsonArray args
                ? args.Select(item => item?.GetValue<string>()).Where(item => !string.IsNullOrWhiteSpace(item)).Cast<string>().ToArray()
                : Array.Empty<string>(),
            Environment = ParseJsonMap(node["env"] as JsonObject, label, id, warnings),
            Headers = ParseJsonMap(node["headers"] as JsonObject, label, id, warnings),
            Targets = target,
        };
    }

    private static IReadOnlyDictionary<string, string> ParseJsonMap(
        JsonObject? source,
        string label,
        string id,
        ICollection<string> warnings)
    {
        var result = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        if (source is null) return result;
        foreach ((string key, JsonNode? node) in source)
        {
            string? value = node?.GetValue<string>();
            AddSafeValue(result, key, value, label, id, warnings);
        }

        return result;
    }

    private static IReadOnlyDictionary<string, string> ParseTomlMap(
        string? value,
        string label,
        string id,
        ICollection<string> warnings)
    {
        var result = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        if (string.IsNullOrWhiteSpace(value)) return result;
        string inner = value.Trim().TrimStart('{').TrimEnd('}');
        foreach (string pair in SplitComma(inner))
        {
            int separator = pair.IndexOf('=');
            if (separator <= 0) continue;
            AddSafeValue(result, Unquote(pair[..separator].Trim()) ?? string.Empty, Unquote(pair[(separator + 1)..].Trim()), label, id, warnings);
        }

        return result;
    }

    private static void AddSafeValue(
        IDictionary<string, string> result,
        string key,
        string? value,
        string label,
        string id,
        ICollection<string> warnings)
    {
        if (string.IsNullOrWhiteSpace(key) || value is null) return;
        if (!SensitiveKeyPattern().IsMatch(key))
        {
            result[key] = value;
            return;
        }

        string trimmed = value.Trim();
        Match env = EnvironmentReferencePattern().Match(trimmed);
        if (env.Success)
        {
            result[key] = $"env:{env.Groups[1].Value}";
        }
        else
        {
            warnings.Add($"{label}/{id}: {key} 含明文凭据，已跳过；请在工作台中改用 env:环境变量名");
        }
    }

    private static void Merge(IDictionary<string, McpServerDefinition> definitions, McpServerDefinition parsed, ManagedClientTargets target)
    {
        if (definitions.TryGetValue(parsed.Id, out McpServerDefinition? existing))
            definitions[parsed.Id] = existing with { Targets = existing.Targets | target };
        else definitions[parsed.Id] = parsed;
    }

    private static IReadOnlyList<string> ParseTomlArray(string? value)
    {
        if (string.IsNullOrWhiteSpace(value)) return Array.Empty<string>();
        return SplitComma(value.Trim().TrimStart('[').TrimEnd(']'))
            .Select(Unquote).Where(item => !string.IsNullOrWhiteSpace(item)).Cast<string>().ToArray();
    }

    private static IEnumerable<string> SplitComma(string value) =>
        value.Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);

    private static string? Unquote(string? value)
    {
        if (string.IsNullOrWhiteSpace(value)) return null;
        string trimmed = value.Trim();
        if (trimmed.Length >= 2 && trimmed[0] is '\'' or '"' && trimmed[^1] == trimmed[0])
            return trimmed[1..^1].Replace("\\\"", "\"", StringComparison.Ordinal).Replace("\\\\", "\\", StringComparison.Ordinal);
        return trimmed;
    }

    [GeneratedRegex("^\\s*\\[mcp_servers\\.(?:\\\"([^\\\"]+)\\\"|([^]\\s]+))\\]\\s*$", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant)]
    private static partial Regex CodexSectionPattern();

    [GeneratedRegex("(token|api[_-]?key|secret|password|authorization|cookie)", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant)]
    private static partial Regex SensitiveKeyPattern();

    [GeneratedRegex("^(?:env:|\\$\\{|\\$)([A-Za-z_][A-Za-z0-9_]*)(?:\\})?$", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant)]
    private static partial Regex EnvironmentReferencePattern();
}
