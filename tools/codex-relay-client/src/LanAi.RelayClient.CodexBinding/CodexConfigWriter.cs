using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace LanAi.RelayClient.CodexBinding;

/// <summary>
/// Points Codex at the relay by editing its two configuration files (F3).
/// </summary>
/// <remarks>
/// <para>
/// Both files belong to the user, not to this client, and both may hold things
/// this client knows nothing about — a ChatGPT session in <c>auth.json</c>, MCP
/// servers and model preferences in <c>config.toml</c>. So neither is replaced:
/// the writer changes the few settings that route traffic and leaves the rest
/// byte-for-byte where it can.
/// </para>
/// <para>
/// The most recent write wins; no coordination with other tools that manage the
/// same files is attempted. That is a deliberate product decision, not an
/// oversight.
/// </para>
/// </remarks>
public sealed class CodexConfigWriter
{
    /// <summary>The provider name this client owns inside <c>config.toml</c>.</summary>
    internal const string ProviderName = "gongfei";

    private const string ApiKeyField = "OPENAI_API_KEY";

    private readonly CodexPaths _paths;
    private readonly CodexAuthSnapshot _snapshot;
    private readonly CodexFileSnapshot _fileSnapshot;

    /// <param name="snapshotRoot">
    /// Where the copy of the user's own Codex configuration is kept. Required, not
    /// defaulted — see the note on <see cref="CodexAuthSnapshot"/>'s constructor.
    /// Getting this wrong does not throw; it means the client can no longer restore
    /// the user to their own ChatGPT account.
    /// </param>
    public CodexConfigWriter(
        CodexPaths paths,
        ISnapshotProtector protector,
        string snapshotRoot,
        string legacySnapshotPath)
        : this(
            paths,
            new CodexAuthSnapshot(protector, legacySnapshotPath),
            new CodexFileSnapshot(paths, snapshotRoot, protector))
    {
    }

    public CodexConfigWriter(
        CodexPaths paths,
        CodexAuthSnapshot snapshot,
        CodexFileSnapshot fileSnapshot)
    {
        _paths = paths ?? throw new ArgumentNullException(nameof(paths));
        _snapshot = snapshot ?? throw new ArgumentNullException(nameof(snapshot));
        _fileSnapshot = fileSnapshot ?? throw new ArgumentNullException(nameof(fileSnapshot));
    }

    /// <summary>
    /// Routes Codex through the relay with <paramref name="apiKey"/>.
    /// </summary>
    /// <param name="apiKey">The managed key's secret.</param>
    /// <param name="baseUrl">
    /// The relay's OpenAI-compatible endpoint, taken from the server's
    /// <c>api_base_url</c> rather than derived from the address the client dials.
    /// </param>
    /// <param name="preferredModel">
    /// The Claude model selected for a Claude/Kiro group. Null leaves the user's
    /// existing top-level model setting unchanged.
    /// </param>
    public void Apply(string apiKey, string baseUrl, string? preferredModel = null)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(apiKey);
        ArgumentException.ThrowIfNullOrWhiteSpace(baseUrl);

        _fileSnapshot.CaptureOnce();
        Directory.CreateDirectory(_paths.Home);

        WriteAuth(apiKey);
        WriteConfig(baseUrl, preferredModel);
    }

    /// <summary>
    /// Puts back the credential that was in <c>auth.json</c> before this client
    /// touched it, or removes the field when there was none (F3.2.7).
    /// </summary>
    public void RestoreAuth(string? originalApiKey)
    {
        JsonObject auth = ReadAuth();

        if (string.IsNullOrEmpty(originalApiKey))
        {
            auth.Remove(ApiKeyField);
        }
        else
        {
            auth[ApiKeyField] = originalApiKey;
        }

        WriteAuthObject(auth);
    }

    /// <summary>The credential currently configured, so it can be restored later.</summary>
    public string? ReadCurrentApiKey() => ReadAuth()[ApiKeyField]?.GetValue<string>();

    /// <summary>
    /// Replaces the credential material with the relay key alone.
    /// </summary>
    /// <remarks>
    /// <para>
    /// Not a merge, and this is the whole point. Codex picks its credential by
    /// what the file contains: an OAuth <c>tokens</c> object means "use the
    /// signed-in ChatGPT account" and takes precedence over any
    /// <c>OPENAI_API_KEY</c> left beside it. Adding the key without removing the
    /// account material produces a config that looks correct, reports success,
    /// and quietly keeps billing the user's ChatGPT plan instead of their relay
    /// balance — the failure this whole client exists to avoid.
    /// </para>
    /// <para>
    /// The account material is therefore taken into safekeeping first, and only
    /// then removed. <see cref="RestoreOriginalAuth"/> hands it back.
    /// </para>
    /// </remarks>
    private void WriteAuth(string apiKey)
    {
        JsonObject current = ReadAuth();

        // Captured before anything is discarded, and only when this client has not
        // already replaced the file — otherwise the second run would "preserve"
        // its own key and lose the user's login permanently.
        if (HasAccountMaterial(current))
        {
            _snapshot.CaptureOnce(current);
        }

        WriteAuthObject(new JsonObject { [ApiKeyField] = apiKey });
    }

    /// <summary>
    /// Whether this file still holds the user's own sign-in rather than ours.
    /// </summary>
    /// <remarks>
    /// Judged by the presence of OAuth material, matching how Codex itself decides
    /// — a file carrying only an API key is one this client already wrote.
    /// </remarks>
    internal static bool HasAccountMaterial(JsonObject auth)
    {
        ArgumentNullException.ThrowIfNull(auth);

        if (auth["tokens"] is JsonObject tokens && tokens.Count > 0)
        {
            return true;
        }

        // A recorded auth_mode with no tokens still describes the user's setup and
        // is worth keeping, but on its own it does not make the file theirs.
        return false;
    }

    /// <summary>
    /// Puts the user's original credentials back, exactly as they were (F3.2.7).
    /// </summary>
    /// <remarks>
    /// Returns false when nothing was recorded — in which case <c>auth.json</c> is
    /// left alone rather than being filled with something invented.
    /// </remarks>
    public bool RestoreOriginalAuth()
    {
        JsonObject? original = _snapshot.Read();
        if (original is null)
        {
            return false;
        }

        WriteAuthObject(original);
        _snapshot.Clear();
        return true;
    }

    /// <summary>Restores both Codex files exactly as they were before the first apply.</summary>
    public bool RestoreOriginalFiles()
    {
        bool restored = _fileSnapshot.Restore();
        if (restored)
        {
            _snapshot.Clear();
        }

        return restored;
    }

    /// <summary>Reads the live TOML without changing it and verifies the owned route.</summary>
    public bool IsRelayRoute(string baseUrl, string? expectedApiKey = null)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(baseUrl);
        if (expectedApiKey is not null)
        {
            ArgumentException.ThrowIfNullOrWhiteSpace(expectedApiKey);
        }

        if (!File.Exists(_paths.ConfigPath))
        {
            return false;
        }

        bool providerSelected = false;
        bool baseUrlMatches = false;
        bool inRelaySection = false;
        bool inAnySection = false;
        string relayHeader = $"[model_providers.{ProviderName}]";

        foreach (string rawLine in SplitLines(File.ReadAllText(_paths.ConfigPath)))
        {
            string trimmed = rawLine.Trim();
            if (trimmed.StartsWith('[') && trimmed.EndsWith(']'))
            {
                inAnySection = true;
                inRelaySection = string.Equals(trimmed, relayHeader, StringComparison.Ordinal);
                continue;
            }

            if (!inAnySection && AssignmentEquals(rawLine, "model_provider", ProviderName))
            {
                providerSelected = true;
            }
            else if (inRelaySection && AssignmentEquals(rawLine, "base_url", baseUrl))
            {
                baseUrlMatches = true;
            }
        }

        if (!providerSelected || !baseUrlMatches)
        {
            return false;
        }

        if (expectedApiKey is null || !File.Exists(_paths.AuthPath))
        {
            return expectedApiKey is null;
        }

        try
        {
            JsonObject? auth = JsonNode.Parse(File.ReadAllText(_paths.AuthPath)) as JsonObject;
            if (auth is null || auth.Count != 1 || auth[ApiKeyField] is not JsonValue value)
            {
                return false;
            }

            return value.TryGetValue<string>(out string? actualApiKey) &&
                string.Equals(actualApiKey, expectedApiKey, StringComparison.Ordinal);
        }
        catch (JsonException)
        {
            return false;
        }
    }

    /// <summary>The provider Codex falls back to when <c>config.toml</c> selects none.</summary>
    public const string DefaultProviderId = "openai";

    /// <summary>
    /// The provider <c>config.toml</c> selects at top level, or null when it selects none.
    /// </summary>
    /// <remarks>
    /// Read from the live file, so after a restore it names the user's own provider.
    /// That is the point: conversations created while this client was in charge have no
    /// earlier provider to go back to, and the only sensible home for them is whatever
    /// the restored configuration will now list.
    /// </remarks>
    public string? ReadActiveProvider()
    {
        if (!File.Exists(_paths.ConfigPath))
        {
            return null;
        }

        foreach (string rawLine in SplitLines(File.ReadAllText(_paths.ConfigPath)))
        {
            string trimmed = rawLine.Trim();

            // Top-level keys end at the first table header; a model_provider under a
            // table belongs to that table, not to the file.
            if (trimmed.StartsWith('[') && trimmed.EndsWith(']'))
            {
                return null;
            }

            if (IsAssignmentTo(rawLine, "model_provider"))
            {
                string value = trimmed.Substring(trimmed.IndexOf('=') + 1).TrimStart();
                return ParseTomlString(value);
            }
        }

        return null;
    }

    /// <summary>Reads one quoted TOML string off the front of <paramref name="value"/>; a trailing comment is ignored.</summary>
    private static string? ParseTomlString(string value)
    {
        if (value.Length < 2)
        {
            return null;
        }

        char quote = value[0];
        if (quote == '\'')
        {
            // Literal string: no escapes at all.
            int end = value.IndexOf('\'', 1);
            return end > 0 ? value[1..end] : null;
        }

        if (quote != '"')
        {
            return null;
        }

        var builder = new StringBuilder();
        for (int i = 1; i < value.Length; i++)
        {
            char c = value[i];
            if (c == '\\' && i + 1 < value.Length)
            {
                builder.Append(value[++i]);
            }
            else if (c == '"')
            {
                return builder.ToString();
            }
            else
            {
                builder.Append(c);
            }
        }

        return null;
    }

    private JsonObject ReadAuth()
    {
        if (!File.Exists(_paths.AuthPath))
        {
            return [];
        }

        try
        {
            return JsonNode.Parse(File.ReadAllText(_paths.AuthPath)) as JsonObject ?? [];
        }
        catch (JsonException)
        {
            // A damaged file cannot be merged into safely. Starting from empty
            // loses whatever was unreadable, but preserving nothing is better than
            // writing a file that is half one thing and half another.
            return [];
        }
    }

    private void WriteAuthObject(JsonObject auth) =>
        AtomicWrite(
            _paths.AuthPath,
            auth.ToJsonString(new JsonSerializerOptions { WriteIndented = true }));

    /// <remarks>
    /// Edited line by line rather than parsed: a TOML library would be a
    /// dependency the installer budget does not want, and a round-trip through one
    /// would reformat the whole file and lose the user's comments.
    /// </remarks>
    private void WriteConfig(string baseUrl, string? preferredModel)
    {
        string existing = File.Exists(_paths.ConfigPath)
            ? File.ReadAllText(_paths.ConfigPath)
            : string.Empty;

        AtomicWrite(_paths.ConfigPath, MergeConfig(existing, baseUrl, preferredModel));
    }

    /// <summary>
    /// Produces a <c>config.toml</c> routing through the relay while keeping
    /// everything the user had that is not this client's to change.
    /// </summary>
    internal static string MergeConfig(string existing, string baseUrl, string? preferredModel = null)
    {
        var preamble = new List<string>();
        var sections = new List<(string Header, List<string> Body)>();

        foreach (string rawLine in SplitLines(existing))
        {
            string trimmed = rawLine.Trim();
            if (trimmed.StartsWith('[') && trimmed.EndsWith(']'))
            {
                sections.Add((trimmed, []));
                continue;
            }

            if (sections.Count == 0)
            {
                preamble.Add(rawLine);
            }
            else
            {
                sections[^1].Body.Add(rawLine);
            }
        }

        bool replaceModel = !string.IsNullOrWhiteSpace(preferredModel);
        bool wroteModel = false;
        bool hasReasoningEffort = false;
        var topLevel = new List<string>();
        foreach (string line in preamble)
        {
            if (IsAssignmentTo(line, "model_provider"))
            {
                continue;
            }

            if (replaceModel && IsAssignmentTo(line, "model"))
            {
                if (!wroteModel)
                {
                    topLevel.Add($"model = \"{EscapeToml(preferredModel!)}\"");
                    wroteModel = true;
                }

                continue;
            }

            if (IsAssignmentTo(line, "model_reasoning_effort"))
            {
                hasReasoningEffort = true;
            }

            topLevel.Add(line);
        }

        TrimTrailingBlank(topLevel);
        if (replaceModel && !wroteModel)
        {
            topLevel.Add($"model = \"{EscapeToml(preferredModel!)}\"");
        }

        if (!hasReasoningEffort)
        {
            topLevel.Add("model_reasoning_effort = \"medium\"");
        }

        topLevel.Add($"model_provider = \"{ProviderName}\"");

        string ourHeader = $"[model_providers.{ProviderName}]";
        var builder = new StringBuilder();

        foreach (string line in topLevel)
        {
            builder.AppendLine(line);
        }

        builder.AppendLine();
        builder.AppendLine(ourHeader);
        builder.AppendLine("name = \"共飞\"");
        builder.AppendLine($"base_url = \"{EscapeToml(baseUrl)}\"");
        builder.AppendLine("wire_api = \"responses\"");
        builder.AppendLine("requires_openai_auth = true");

        foreach ((string header, List<string> body) in sections)
        {
            // Our own section is regenerated above; every other one is copied
            // through untouched, comments and all.
            if (string.Equals(header, ourHeader, StringComparison.Ordinal))
            {
                continue;
            }

            builder.AppendLine();
            builder.AppendLine(header);

            List<string> trimmedBody = [.. body];
            TrimTrailingBlank(trimmedBody);
            foreach (string line in trimmedBody)
            {
                builder.AppendLine(line);
            }
        }

        return builder.ToString();
    }

    private static IEnumerable<string> SplitLines(string text) =>
        text.Replace("\r\n", "\n", StringComparison.Ordinal)
            .Replace('\r', '\n')
            .Split('\n');

    private static bool IsAssignmentTo(string line, string key)
    {
        string trimmed = line.TrimStart();
        if (!trimmed.StartsWith(key, StringComparison.Ordinal))
        {
            return false;
        }

        // Guards against matching a longer key that merely starts the same way,
        // for example "model_provider_extra".
        string rest = trimmed[key.Length..].TrimStart();
        return rest.StartsWith('=');
    }

    private static bool AssignmentEquals(string line, string key, string expected)
    {
        if (!IsAssignmentTo(line, key))
        {
            return false;
        }

        string trimmed = line.TrimStart();
        string value = trimmed[key.Length..].TrimStart()[1..].Trim();
        return string.Equals(value, $"\"{EscapeToml(expected)}\"", StringComparison.Ordinal);
    }

    private static void TrimTrailingBlank(List<string> lines)
    {
        while (lines.Count > 0 && string.IsNullOrWhiteSpace(lines[^1]))
        {
            lines.RemoveAt(lines.Count - 1);
        }
    }

    private static string EscapeToml(string value) =>
        value.Replace("\\", "\\\\", StringComparison.Ordinal)
            .Replace("\"", "\\\"", StringComparison.Ordinal);

    /// <remarks>
    /// Written to a temporary file and moved into place: Codex may read these at
    /// any moment, and a half-written config is worse than an out-of-date one.
    /// </remarks>
    private static void AtomicWrite(string path, string contents)
    {
        string temporaryPath = path + ".tmp";
        File.WriteAllText(temporaryPath, contents, new UTF8Encoding(encoderShouldEmitUTF8Identifier: false));
        File.Move(temporaryPath, path, overwrite: true);
    }
}
