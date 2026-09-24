using System.Text;
using System.Text.Encodings.Web;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace LanAi.RelayClient.CodexBinding;

/// <summary>What a Claude Code configuration change did.</summary>
public enum PluginConfigOutcome
{
    /// <summary>The configuration was changed.</summary>
    Applied,

    /// <summary>What this client had changed was put back.</summary>
    Restored,

    /// <summary>Nothing needed changing.</summary>
    NothingToDo,

    /// <summary>Not attempted, or not safe to attempt. Nothing was written.</summary>
    Skipped,

    /// <summary>Attempted and failed. Nothing was left half written.</summary>
    Failed,
}

/// <param name="Detail">A reason, for the log. Never shown to the user as is.</param>
public sealed record PluginConfigResult(PluginConfigOutcome Outcome, string? Detail = null);

/// <summary>
/// Points Claude Code at the relay by setting a few environment variables in its
/// <c>settings.json</c>, and puts them back afterwards.
/// </summary>
/// <remarks>
/// <para>
/// <b>Where.</b> <c>~/.claude/settings.json</c>, or <c>$CLAUDE_CONFIG_DIR/settings.json</c>,
/// in its <c>env</c> block. The CLI reads that, and so does the editor extension — it
/// launches the same CLI, and its own documentation says to prefer settings.json for
/// environment variables. This is the approach cockpit-tools takes for the same file.
/// </para>
/// <para>
/// <b>What it touches.</b> Only the keys it is asked to set, and only those. The user's
/// other <c>env</c> entries, and everything else in the file, are left as they are.
/// </para>
/// <para>
/// <b>How it undoes it.</b> The state of every key before it was first changed is written
/// to a journal before the file is. Restoring puts each key back to that — a value the user
/// had is returned, a key they did not have is removed — rather than clearing keys and hoping
/// nothing of theirs was among them, which is what makes a plain "remove what we wrote"
/// lossy: someone with their own <c>ANTHROPIC_MODEL</c> would lose it. A key the user has
/// edited since it was set is left alone, because putting back the original would overwrite
/// a change they made on purpose.
/// </para>
/// <para>
/// <b>What it refuses.</b> A file with comments or trailing commas, one that is not valid
/// JSON, or whose <c>env</c> is not an object. Rewriting any of those would either drop the
/// user's comments or replace a file that could not be read, so it reports and does nothing.
/// It never throws for these; none of them may stop the client from starting.
/// </para>
/// </remarks>
public sealed class ClaudeCodeSettingsWriter
{
    private const string SettingsFileName = "settings.json";
    private const string EnvKey = "env";

    /// <summary>
    /// The keys this client ever manages. A value in the request sets the key; a null one
    /// removes it, which is how <c>ANTHROPIC_API_KEY</c> is kept from competing with the
    /// token while the relay is in use.
    /// </summary>
    public static readonly IReadOnlyList<string> ManagedKeys =
    [
        "ANTHROPIC_BASE_URL",
        "ANTHROPIC_AUTH_TOKEN",
        "ANTHROPIC_API_KEY",
        "ANTHROPIC_MODEL",
        "ANTHROPIC_DEFAULT_HAIKU_MODEL",
        "ANTHROPIC_DEFAULT_SONNET_MODEL",
        "ANTHROPIC_DEFAULT_OPUS_MODEL",
        "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
    ];

    private static readonly JsonSerializerOptions WriteOptions = new()
    {
        WriteIndented = true,

        // Readable, and no rewriting of characters the user typed: the default encoder
        // turns non-ASCII text and characters like & and + into \u escapes.
        Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
    };

    private readonly string _settingsPath;
    private readonly string _journalPath;
    private readonly object _gate = new();

    /// <param name="journalPath">
    /// Where the record of original values is kept. Must outlive a restart of the client:
    /// it is the only thing that knows what to put back.
    /// </param>
    /// <param name="configDirectory">
    /// Claude Code's configuration directory. Defaults to <c>$CLAUDE_CONFIG_DIR</c>, then
    /// <c>~/.claude</c>. The editor extension may have been started with a different one;
    /// there is no way to know from here.
    /// </param>
    public ClaudeCodeSettingsWriter(string journalPath, string? configDirectory = null)
    {
        _journalPath = string.IsNullOrWhiteSpace(journalPath)
            ? throw new ArgumentException("Journal path cannot be empty.", nameof(journalPath))
            : Path.GetFullPath(journalPath);
        _settingsPath = Path.Combine(configDirectory ?? DefaultConfigDirectory(), SettingsFileName);
    }

    public string SettingsPath => _settingsPath;

    internal static string DefaultConfigDirectory()
    {
        string? overridden = Environment.GetEnvironmentVariable("CLAUDE_CONFIG_DIR");
        return string.IsNullOrWhiteSpace(overridden)
            ? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".claude")
            : overridden.Trim();
    }

    /// <summary>
    /// Makes <c>env</c> in <c>settings.json</c> carry <paramref name="desired"/> for the
    /// managed keys.
    /// </summary>
    /// <param name="desired">
    /// The managed keys and their values; a null value removes the key. A managed key that is
    /// missing from it is put back to how it was, which is how a model that is no longer
    /// selected stops being forced.
    /// </param>
    public PluginConfigResult Apply(IReadOnlyDictionary<string, string?> desired)
    {
        ArgumentNullException.ThrowIfNull(desired);
        foreach (string key in desired.Keys)
        {
            if (!ManagedKeys.Contains(key, StringComparer.Ordinal))
            {
                throw new ArgumentException($"'{key}' is not a key this writer manages.", nameof(desired));
            }
        }

        lock (_gate)
        {
            try
            {
                Journal journal = ReadJournal();

                // A journal written for another directory belongs to a configuration that
                // is no longer the one in use. Put that one back first, so a change of
                // CLAUDE_CONFIG_DIR does not strand this client's edits in the old place.
                if (journal.SettingsPath is not null &&
                    !string.Equals(journal.SettingsPath, _settingsPath, StringComparison.OrdinalIgnoreCase))
                {
                    RestoreAt(journal.SettingsPath, journal);
                    journal = new Journal();
                }

                (JsonObject? root, bool existed, string? refusal) = LoadSettings();
                if (refusal is not null)
                {
                    return new PluginConfigResult(PluginConfigOutcome.Skipped, refusal);
                }

                // A file the user deleted while this was active has nothing of theirs left
                // to restore. Keeping the old record would put keys back into a file they
                // chose to remove.
                if (!existed && journal.SettingsPath is not null)
                {
                    journal = new Journal();
                }

                root ??= new JsonObject();
                JsonObject env = EnsureEnv(root);
                bool firstTime = journal.SettingsPath is null;
                journal.SettingsPath = _settingsPath;
                if (firstTime)
                {
                    journal.FileExisted = existed;
                }

                bool changed = false;

                foreach ((string key, string? value) in desired)
                {
                    if (!journal.Keys.ContainsKey(key))
                    {
                        // The first time only: on any later call the current value is one
                        // this client wrote, and recording it would lose the real original.
                        journal.Keys[key] = new KeyState(
                            Had: env.ContainsKey(key),
                            OriginalJson: env[key]?.ToJsonString(),
                            Applied: null);
                    }

                    KeyState state = journal.Keys[key];
                    if (SetKey(env, key, value))
                    {
                        changed = true;
                    }

                    journal.Keys[key] = state with { Applied = value };
                }

                foreach (string key in journal.Keys.Keys.ToArray())
                {
                    if (desired.ContainsKey(key))
                    {
                        continue;
                    }

                    if (PutBack(env, key, journal.Keys[key]))
                    {
                        changed = true;
                    }

                    journal.Keys.Remove(key);
                }

                if (!changed && File.Exists(_settingsPath) && File.Exists(_journalPath))
                {
                    WriteJournal(journal);
                    return new PluginConfigResult(PluginConfigOutcome.NothingToDo);
                }

                // The record first, then the file: a crash between the two leaves entries for
                // changes that were not made, which restoring recognises and ignores, and never
                // the reverse.
                WriteJournal(journal);
                Directory.CreateDirectory(Path.GetDirectoryName(_settingsPath)!);
                AtomicWrite(_settingsPath, root.ToJsonString(WriteOptions));
                return new PluginConfigResult(PluginConfigOutcome.Applied);
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or JsonException)
            {
                return new PluginConfigResult(PluginConfigOutcome.Failed, ex.Message);
            }
        }
    }

    /// <summary>Puts every key this client set back to how it was.</summary>
    public PluginConfigResult Restore()
    {
        lock (_gate)
        {
            try
            {
                Journal journal = ReadJournal();
                if (journal.SettingsPath is null)
                {
                    return new PluginConfigResult(PluginConfigOutcome.NothingToDo);
                }

                PluginConfigResult result = RestoreAt(journal.SettingsPath, journal);

                // Kept when the file could not be read. That is a state the user may fix, and
                // the record is the only thing that knows what to put back once they do.
                if (result.Outcome != PluginConfigOutcome.Skipped)
                {
                    DeleteJournal();
                }

                return result;
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or JsonException)
            {
                return new PluginConfigResult(PluginConfigOutcome.Failed, ex.Message);
            }
        }
    }

    private PluginConfigResult RestoreAt(string settingsPath, Journal journal)
    {
        if (!File.Exists(settingsPath))
        {
            return new PluginConfigResult(PluginConfigOutcome.NothingToDo, "settings.json is already gone.");
        }

        JsonObject? root = TryParseStrict(File.ReadAllText(settingsPath));
        if (root is null)
        {
            // Edited into something this cannot read while it was active. Not overwritten:
            // whatever it is now is the user's.
            return new PluginConfigResult(PluginConfigOutcome.Skipped, "settings.json can no longer be read as plain JSON.");
        }

        bool changed = false;
        if (root[EnvKey] is JsonObject env)
        {
            foreach ((string key, KeyState state) in journal.Keys)
            {
                if (PutBack(env, key, state))
                {
                    changed = true;
                }
            }

            if (env.Count == 0)
            {
                root.Remove(EnvKey);
                changed = true;
            }
        }

        // A file this client created holds nothing of the user's once its own keys are gone.
        // Leaving an empty {} behind would be litter in their directory.
        if (!journal.FileExisted && root.Count == 0)
        {
            File.Delete(settingsPath);
            return new PluginConfigResult(PluginConfigOutcome.Restored);
        }

        if (changed)
        {
            AtomicWrite(settingsPath, root.ToJsonString(WriteOptions));
        }

        return new PluginConfigResult(changed ? PluginConfigOutcome.Restored : PluginConfigOutcome.NothingToDo);
    }

    /// <summary>
    /// Sets or removes <paramref name="key"/>. Returns whether that changed anything.
    /// </summary>
    private static bool SetKey(JsonObject env, string key, string? value)
    {
        if (value is null)
        {
            return env.Remove(key);
        }

        if (env[key] is JsonValue current && current.TryGetValue(out string? existing) &&
            string.Equals(existing, value, StringComparison.Ordinal))
        {
            return false;
        }

        env[key] = value;
        return true;
    }

    /// <summary>
    /// Returns a key to how it was, unless the user has since changed it.
    /// </summary>
    private static bool PutBack(JsonObject env, string key, KeyState state)
    {
        string? current = env[key] is JsonValue value && value.TryGetValue(out string? text) ? text : null;
        bool present = env.ContainsKey(key);

        // Not ours any more: what is there is neither what this client wrote nor absent
        // where it removed a key. Restoring over it would erase an edit made on purpose.
        bool unchanged = state.Applied is null ? !present : string.Equals(current, state.Applied, StringComparison.Ordinal);
        if (!unchanged)
        {
            return false;
        }

        if (state.Had)
        {
            // Put back as it was written, whatever type that was: a user who had the value as a
            // number or a boolean gets a number or a boolean, not a string that looks like one.
            env[key] = state.OriginalJson is null ? null : JsonNode.Parse(state.OriginalJson);
            return true;
        }

        return env.Remove(key);
    }

    // ---- Reading and writing the file -------------------------------------------------

    private (JsonObject? Root, bool Existed, string? Refusal) LoadSettings()
    {
        if (!File.Exists(_settingsPath))
        {
            return (null, false, null);
        }

        string text = File.ReadAllText(_settingsPath);
        if (string.IsNullOrWhiteSpace(text))
        {
            return (new JsonObject(), true, null);
        }

        JsonObject? strict = TryParseStrict(text);
        if (strict is null)
        {
            bool readableWithComments = TryParseLenient(text);
            return (null, true, readableWithComments
                ? "settings.json contains comments or trailing commas, which rewriting would lose; left unchanged."
                : "settings.json is not valid JSON; left unchanged.");
        }

        if (strict[EnvKey] is not null && strict[EnvKey] is not JsonObject)
        {
            return (null, true, "settings.json has an env that is not an object; left unchanged.");
        }

        return (strict, true, null);
    }

    private static JsonObject EnsureEnv(JsonObject root)
    {
        if (root[EnvKey] is JsonObject env)
        {
            return env;
        }

        var created = new JsonObject();
        root[EnvKey] = created;
        return created;
    }

    private static JsonObject? TryParseStrict(string text)
    {
        try
        {
            return JsonNode.Parse(text) as JsonObject;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    private static bool TryParseLenient(string text)
    {
        try
        {
            using JsonDocument _ = JsonDocument.Parse(
                text,
                new JsonDocumentOptions { CommentHandling = JsonCommentHandling.Skip, AllowTrailingCommas = true });
            return true;
        }
        catch (JsonException)
        {
            return false;
        }
    }

    private static void AtomicWrite(string path, string contents)
    {
        string temporaryPath = path + ".tmp";
        File.WriteAllText(temporaryPath, contents + Environment.NewLine, new UTF8Encoding(encoderShouldEmitUTF8Identifier: false));
        File.Move(temporaryPath, path, overwrite: true);
    }

    // ---- The journal ------------------------------------------------------------------

    private sealed class Journal
    {
        public string? SettingsPath { get; set; }

        public bool FileExisted { get; set; }

        public Dictionary<string, KeyState> Keys { get; } = new(StringComparer.Ordinal);
    }

    /// <param name="Had">Whether the user had this key before this client first set it.</param>
    /// <param name="OriginalJson">Their value as JSON text, of whatever type it was.</param>
    /// <param name="Applied">What this client last set; null when it removed the key.</param>
    private sealed record KeyState(bool Had, string? OriginalJson, string? Applied);

    /// <remarks>
    /// Read with <see cref="JsonDocument"/> and written with <see cref="Utf8JsonWriter"/>, not
    /// bound to a type: this project is trimmable, and reflection-based JSON binds to defaults
    /// without complaint when trimmed. A journal that read as empty would mean originals that
    /// are never put back.
    /// </remarks>
    private Journal ReadJournal()
    {
        var journal = new Journal();
        if (!File.Exists(_journalPath))
        {
            return journal;
        }

        try
        {
            using JsonDocument document = JsonDocument.Parse(File.ReadAllBytes(_journalPath));
            JsonElement root = document.RootElement;
            if (root.TryGetProperty("settingsPath", out JsonElement path) && path.ValueKind == JsonValueKind.String)
            {
                journal.SettingsPath = path.GetString();
            }

            journal.FileExisted = root.TryGetProperty("fileExisted", out JsonElement existed) &&
                                  existed.ValueKind == JsonValueKind.True;
            if (root.TryGetProperty("keys", out JsonElement keys) && keys.ValueKind == JsonValueKind.Object)
            {
                foreach (JsonProperty entry in keys.EnumerateObject())
                {
                    JsonElement value = entry.Value;
                    journal.Keys[entry.Name] = new KeyState(
                        Had: value.TryGetProperty("had", out JsonElement had) && had.ValueKind == JsonValueKind.True,
                        OriginalJson: ReadNullableString(value, "originalJson"),
                        Applied: ReadNullableString(value, "applied"));
                }
            }
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            // Unreadable means nothing remembered. The consequence is bounded: the next
            // apply treats today's values as the originals, which restores them rather than
            // the user's own — and the file is never overwritten on the strength of a record
            // that could not be read.
            return new Journal();
        }

        return journal;
    }

    private static string? ReadNullableString(JsonElement parent, string name) =>
        parent.TryGetProperty(name, out JsonElement element) && element.ValueKind == JsonValueKind.String
            ? element.GetString()
            : null;

    private void WriteJournal(Journal journal)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(_journalPath)!);

        using var stream = new MemoryStream();
        using (var writer = new Utf8JsonWriter(stream, new JsonWriterOptions { Indented = true }))
        {
            writer.WriteStartObject();
            writer.WriteString("settingsPath", journal.SettingsPath);
            writer.WriteBoolean("fileExisted", journal.FileExisted);
            writer.WriteStartObject("keys");
            foreach ((string key, KeyState state) in journal.Keys)
            {
                writer.WriteStartObject(key);
                writer.WriteBoolean("had", state.Had);
                if (state.OriginalJson is null)
                {
                    writer.WriteNull("originalJson");
                }
                else
                {
                    writer.WriteString("originalJson", state.OriginalJson);
                }

                if (state.Applied is null)
                {
                    writer.WriteNull("applied");
                }
                else
                {
                    writer.WriteString("applied", state.Applied);
                }

                writer.WriteEndObject();
            }

            writer.WriteEndObject();
            writer.WriteEndObject();
        }

        string temporaryPath = _journalPath + ".tmp";
        File.WriteAllBytes(temporaryPath, stream.ToArray());
        File.Move(temporaryPath, _journalPath, overwrite: true);
    }

    private void DeleteJournal()
    {
        try
        {
            File.Delete(_journalPath);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // A stale record only matters if it names a key this client sets again, and then
            // it is replaced.
        }
    }
}
