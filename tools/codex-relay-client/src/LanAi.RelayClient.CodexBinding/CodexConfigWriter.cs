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
public sealed class CodexConfigWriter : ICodexLoginStore
{
    /// <summary>The provider name this client owns inside <c>config.toml</c>.</summary>
    internal const string ProviderName = "gongfei";

    private const string FeaturesHeader = "[features]";

    /// <summary>
    /// The switch that lets Codex fetch a model list when it signs in with an API key. Still
    /// marked as under development in Codex, hence a constant to find in one place if it moves.
    /// </summary>
    private const string DiscoveryFeature = "api_key_model_discovery";

    /// <summary>
    /// Codex warns in every conversation when a feature still under development is on
    /// ("Under-development features enabled: api_key_model_discovery", measured). The switch is
    /// ours, not something the user opted into, so the warning is silenced along with it.
    /// </summary>
    private const string SuppressUnstableWarning = "suppress_unstable_features_warning";

    private const string ApiKeyField = "OPENAI_API_KEY";

    /// <summary>
    /// Serialises every read and write of the user's sign-in, across instances: the local
    /// proxy refreshes it from relay threads while launch, the route guard and release move
    /// it between <c>auth.json</c> and the snapshots. A refresh written into a copy that a
    /// restore is about to replace would be lost — and OpenAI refresh tokens are single-use.
    /// </summary>
    private static readonly object LoginGate = new();

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
    /// <param name="catalogUrl">
    /// Where Codex should ask for its model list. Written to the provider together with the
    /// feature switch Codex requires before it will ask at all (measured: without the switch
    /// the address is ignored). Null writes neither.
    /// </param>
    /// <param name="keepModelIfIn">
    /// When given, <paramref name="preferredModel"/> only replaces the user's model if the
    /// user's is not among these — a model the user picked and the group serves is theirs to
    /// keep. Null replaces unconditionally.
    /// </param>
    public void Apply(
        string apiKey,
        string baseUrl,
        string? preferredModel = null,
        string? catalogUrl = null,
        IReadOnlyCollection<string>? keepModelIfIn = null)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(apiKey);
        ArgumentException.ThrowIfNullOrWhiteSpace(baseUrl);

        lock (LoginGate)
        {
            bool firstCapture = _fileSnapshot.CaptureOnce();
            Directory.CreateDirectory(_paths.Home);

            WriteAuth(apiKey, firstCapture);
            WriteConfig(baseUrl, preferredModel, catalogUrl, keepModelIfIn);

            if (catalogUrl is not null)
            {
                ForgetCachedModelList();
            }
        }
    }

    /// <summary>
    /// Codex's on-disk copy of the last model list it fetched, for up to five minutes.
    /// </summary>
    internal const string ModelsCacheFile = "models_cache.json";

    /// <summary>
    /// Deletes Codex's cached model list so the next start asks for it again.
    /// </summary>
    /// <remarks>
    /// Codex answers its first request from that copy and refreshes it in the background, so a
    /// restart right after a group switch raced the refresh and often showed the previous
    /// group's models (measured, against the real binary). Best-effort: a copy that cannot be
    /// removed only means the old list may show once more.
    /// </remarks>
    private void ForgetCachedModelList()
    {
        try
        {
            File.Delete(Path.Combine(_paths.Home, ModelsCacheFile));
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
        }
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
    /// <para>
    /// Account material found when a snapshot already exists is a sign-in the user made
    /// while Codex pointed at the relay (<c>codex login</c> rewrites the file, the route
    /// guard then calls here). It is newer than what the snapshot holds, so it replaces
    /// it: keeping the old one would hand back a sign-in the user had already replaced,
    /// and drop the one they just made.
    /// </para>
    /// </remarks>
    private void WriteAuth(string apiKey, bool firstCapture)
    {
        JsonObject current = ReadAuth();

        // Captured before anything is discarded. Only account material is ever captured —
        // a file carrying just a key is one this client wrote, and "preserving" it would
        // lose the user's login permanently.
        if (HasAccountMaterial(current))
        {
            if (!firstCapture)
            {
                ReplaceSnapshotAuth(current, File.ReadAllBytes(_paths.AuthPath));
            }

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
        lock (LoginGate)
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
    }

    /// <summary>Restores both Codex files exactly as they were before the first apply.</summary>
    /// <remarks>
    /// "As they were" except for the sign-in: a newer one the user made meanwhile, or the
    /// same one with tokens the local proxy refreshed, is what goes back.
    /// </remarks>
    public bool RestoreOriginalFiles()
    {
        lock (LoginGate)
        {
            bool restored = _fileSnapshot.Restore();
            if (restored)
            {
                _snapshot.Clear();
            }

            return restored;
        }
    }

    /// <inheritdoc />
    public CodexLogin? ReadLogin()
    {
        lock (LoginGate)
        {
            // Newest first: a sign-in in the file itself is either the only copy (Codex not on
            // the relay) or one made since the snapshot was taken.
            foreach (Func<JsonObject?> source in LoginSources())
            {
                if (ParseLogin(source()) is { } login)
                {
                    return login;
                }
            }

            return null;
        }
    }

    /// <inheritdoc />
    public bool UpdateLoginTokens(string previousRefreshToken, CodexRefreshedTokens refreshed, DateTimeOffset refreshedAt)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(previousRefreshToken);
        ArgumentNullException.ThrowIfNull(refreshed);
        ArgumentException.ThrowIfNullOrWhiteSpace(refreshed.AccessToken);

        lock (LoginGate)
        {
            bool updated = false;

            JsonObject live = ReadAuth();
            if (HasRefreshToken(live, previousRefreshToken))
            {
                WriteAuthObject(ApplyRefresh(live, refreshed, refreshedAt));
                updated = true;
            }

            if (ReadFileSnapshotAuth() is { } recorded && HasRefreshToken(recorded, previousRefreshToken))
            {
                byte[] plaintext = Encoding.UTF8.GetBytes(
                    ApplyRefresh(recorded, refreshed, refreshedAt).ToJsonString(new JsonSerializerOptions { WriteIndented = true }));
                try
                {
                    _fileSnapshot.ReplaceAuth(plaintext);
                }
                finally
                {
                    Array.Clear(plaintext, 0, plaintext.Length);
                }

                updated = true;
            }

            if (_snapshot.Read() is { } legacy && HasRefreshToken(legacy, previousRefreshToken))
            {
                _snapshot.ReplaceIfExists(ApplyRefresh(legacy, refreshed, refreshedAt));
                updated = true;
            }

            return updated;
        }
    }

    private IEnumerable<Func<JsonObject?>> LoginSources()
    {
        yield return () => ReadAuth() is { } live && HasAccountMaterial(live) ? live : null;
        yield return ReadFileSnapshotAuth;
        yield return _snapshot.Read;
    }

    private JsonObject? ReadFileSnapshotAuth()
    {
        byte[]? plaintext = _fileSnapshot.ReadAuth();
        if (plaintext is null)
        {
            return null;
        }

        try
        {
            return JsonNode.Parse(plaintext) as JsonObject;
        }
        catch (JsonException)
        {
            return null;
        }
        finally
        {
            Array.Clear(plaintext, 0, plaintext.Length);
        }
    }

    /// <summary>Puts a newer sign-in into both snapshots, in place of the one they hold.</summary>
    private void ReplaceSnapshotAuth(JsonObject auth, byte[] raw)
    {
        try
        {
            _fileSnapshot.ReplaceAuth(raw);
        }
        finally
        {
            Array.Clear(raw, 0, raw.Length);
        }

        _snapshot.ReplaceIfExists(auth);
    }

    private static CodexLogin? ParseLogin(JsonObject? auth)
    {
        if (auth?["tokens"] is not JsonObject tokens)
        {
            return null;
        }

        string refresh = StringOf(tokens, "refresh_token");
        string access = StringOf(tokens, "access_token");
        if (refresh.Length == 0 && access.Length == 0)
        {
            return null;
        }

        return new CodexLogin(access, refresh, StringOf(tokens, "id_token"), StringOf(tokens, "account_id"));
    }

    private static bool HasRefreshToken(JsonObject auth, string refreshToken) =>
        auth["tokens"] is JsonObject tokens &&
        string.Equals(StringOf(tokens, "refresh_token"), refreshToken, StringComparison.Ordinal);

    /// <summary>The same shape Codex itself writes after a refresh: the three tokens and <c>last_refresh</c>.</summary>
    private static JsonObject ApplyRefresh(JsonObject auth, CodexRefreshedTokens refreshed, DateTimeOffset refreshedAt)
    {
        var tokens = (JsonObject)auth["tokens"]!;
        tokens["access_token"] = refreshed.AccessToken;
        if (!string.IsNullOrWhiteSpace(refreshed.RefreshToken))
        {
            tokens["refresh_token"] = refreshed.RefreshToken;
        }
        if (!string.IsNullOrWhiteSpace(refreshed.IdToken))
        {
            tokens["id_token"] = refreshed.IdToken;
        }

        auth["last_refresh"] = refreshedAt.UtcDateTime.ToString(
            "yyyy-MM-dd'T'HH:mm:ss.fffffff'Z'", System.Globalization.CultureInfo.InvariantCulture);
        return auth;
    }

    private static string StringOf(JsonObject obj, string name) =>
        obj[name] is JsonValue value && value.TryGetValue(out string? text) ? text ?? string.Empty : string.Empty;

    /// <summary>Reads the live TOML without changing it and verifies the owned route.</summary>
    public bool IsRelayRoute(string baseUrl, string? expectedApiKey = null, string? catalogUrl = null)
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
        bool catalogMatches = catalogUrl is null;
        bool discoveryOn = catalogUrl is null;
        bool inRelaySection = false;
        bool inFeatures = false;
        bool inAnySection = false;
        string relayHeader = $"[model_providers.{ProviderName}]";

        foreach (string rawLine in SplitLines(File.ReadAllText(_paths.ConfigPath)))
        {
            string trimmed = rawLine.Trim();
            if (trimmed.StartsWith('[') && trimmed.EndsWith(']'))
            {
                inAnySection = true;
                inRelaySection = string.Equals(trimmed, relayHeader, StringComparison.Ordinal);
                inFeatures = string.Equals(trimmed, FeaturesHeader, StringComparison.Ordinal);
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
            else if (inRelaySection && catalogUrl is not null && AssignmentEquals(rawLine, "model_catalog_url", catalogUrl))
            {
                catalogMatches = true;
            }
            else if (inFeatures && IsAssignmentTo(rawLine, DiscoveryFeature) &&
                     rawLine.Split('=', 2)[1].Trim().StartsWith("true", StringComparison.Ordinal))
            {
                discoveryOn = true;
            }
        }

        if (!providerSelected || !baseUrlMatches || !catalogMatches || !discoveryOn)
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
    private void WriteConfig(
        string baseUrl,
        string? preferredModel,
        string? catalogUrl = null,
        IReadOnlyCollection<string>? keepModelIfIn = null)
    {
        string existing = File.Exists(_paths.ConfigPath)
            ? File.ReadAllText(_paths.ConfigPath)
            : string.Empty;

        AtomicWrite(_paths.ConfigPath, MergeConfig(existing, baseUrl, preferredModel, catalogUrl, keepModelIfIn));
    }

    /// <summary>
    /// Produces a <c>config.toml</c> routing through the relay while keeping
    /// everything the user had that is not this client's to change.
    /// </summary>
    internal static string MergeConfig(
        string existing,
        string baseUrl,
        string? preferredModel = null,
        string? catalogUrl = null,
        IReadOnlyCollection<string>? keepModelIfIn = null)
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

        string? existingModel = preamble
            .Select(line => IsAssignmentTo(line, "model") ? ReadQuotedValue(line) : null)
            .FirstOrDefault(value => value is not null);
        bool replaceModel = !string.IsNullOrWhiteSpace(preferredModel) &&
            (keepModelIfIn is null ||
             existingModel is null ||
             !keepModelIfIn.Contains(existingModel, StringComparer.OrdinalIgnoreCase));
        bool wroteModel = false;
        bool hasReasoningEffort = false;
        var topLevel = new List<string>();
        foreach (string line in preamble)
        {
            if (IsAssignmentTo(line, "model_provider"))
            {
                continue;
            }

            if (catalogUrl is not null && IsAssignmentTo(line, SuppressUnstableWarning))
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

        if (catalogUrl is not null)
        {
            topLevel.Add($"{SuppressUnstableWarning} = true");
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
        if (catalogUrl is not null)
        {
            builder.AppendLine($"model_catalog_url = \"{EscapeToml(catalogUrl)}\"");
        }

        if (catalogUrl is not null)
        {
            EnsureDiscoveryFeature(sections);
        }

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

    /// <summary>
    /// Turns the model-list switch on inside the user's own <c>[features]</c> table, or adds
    /// the table. Two tables of the same name are a TOML error that stops Codex reading the
    /// file at all, so an existing one is edited rather than appended to.
    /// </summary>
    private static void EnsureDiscoveryFeature(List<(string Header, List<string> Body)> sections)
    {
        string line = $"{DiscoveryFeature} = true";
        int at = sections.FindIndex(s => string.Equals(s.Header, FeaturesHeader, StringComparison.Ordinal));
        if (at < 0)
        {
            sections.Add((FeaturesHeader, [line]));
            return;
        }

        List<string> body = sections[at].Body;
        int existing = body.FindIndex(l => IsAssignmentTo(l, DiscoveryFeature));
        if (existing >= 0)
        {
            body[existing] = line;
        }
        else
        {
            body.Insert(0, line);
        }
    }

    /// <summary>The string in <c>key = "value"</c>, or null when the value is not a plain quoted string.</summary>
    private static string? ReadQuotedValue(string line)
    {
        int equals = line.IndexOf('=', StringComparison.Ordinal);
        if (equals < 0)
        {
            return null;
        }

        string value = line[(equals + 1)..].Trim();
        if (value.Length < 2 || value[0] != '"')
        {
            return null;
        }

        int close = value.IndexOf('"', 1);
        return close < 0 ? null : value[1..close];
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
