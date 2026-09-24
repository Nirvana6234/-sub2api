using System.Collections.ObjectModel;
using System.Text.Json;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Read-only compatibility adapter for the WinForms profiles.json file.
/// Plaintext legacy secrets are never copied into SQLite, logs, profile IDs or
/// returned URLs. They remain in this process only behind transient references.
/// </summary>
public sealed class LegacyProfileReader : IConnectionProfileReader, IConnectionCredentialProvider, IDisposable
{
    private const string CredentialPrefix = "legacy:";

    private static readonly JsonDocumentOptions DocumentOptions = new()
    {
        AllowTrailingCommas = true,
        CommentHandling = JsonCommentHandling.Skip,
    };

    private readonly string _profilesPath;
    private readonly object _cacheGate = new();
    private int _disposeState;
    private IReadOnlyDictionary<string, string> _secrets
        = new ReadOnlyDictionary<string, string>(
            new Dictionary<string, string>(StringComparer.Ordinal));
    private IReadOnlyDictionary<string, string> _legacyBaseUrls
        = new ReadOnlyDictionary<string, string>(
            new Dictionary<string, string>(StringComparer.Ordinal));

    public LegacyProfileReader(AppDataPaths paths)
        : this(paths?.LegacyProfilesPath ?? throw new ArgumentNullException(nameof(paths)))
    {
    }

    public LegacyProfileReader(string profilesPath)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(profilesPath);
        _profilesPath = Path.GetFullPath(Environment.ExpandEnvironmentVariables(profilesPath));
    }

    public string ProfilesPath => _profilesPath;

    public async Task<IReadOnlyList<ConnectionProfile>> GetAllAsync(
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(Volatile.Read(ref _disposeState) != 0, this);

        // A reload invalidates transient references immediately. This prevents
        // an interrupted or failed read from leaving stale plaintext available.
        ClearSecrets();

        if (!File.Exists(_profilesPath))
        {
            return Array.Empty<ConnectionProfile>();
        }

        await using var stream = new FileStream(
            _profilesPath,
            FileMode.Open,
            FileAccess.Read,
            FileShare.ReadWrite | FileShare.Delete,
            bufferSize: 16 * 1024,
            FileOptions.Asynchronous | FileOptions.SequentialScan);

        JsonDocument parsedDocument;
        try
        {
            parsedDocument = await JsonDocument.ParseAsync(stream, DocumentOptions, cancellationToken)
                .ConfigureAwait(false);
        }
        catch (JsonException exception)
        {
            ClearSecrets();
            throw new InvalidDataException("The legacy profiles.json file is not valid JSON.", exception);
        }

        using JsonDocument document = parsedDocument;

        cancellationToken.ThrowIfCancellationRequested();

        var profiles = new List<ConnectionProfile>();
        var seenIds = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        var secrets = new Dictionary<string, string>(StringComparer.Ordinal);
        var legacyBaseUrls = new Dictionary<string, string>(StringComparer.Ordinal);

        if (document.RootElement.ValueKind != JsonValueKind.Object)
        {
            ClearSecrets();
            return Array.Empty<ConnectionProfile>();
        }

        ReadProfileCollection(
            document.RootElement,
            "LocalSources",
            ConnectionProfileKind.Local,
            profiles,
            seenIds,
            secrets,
            legacyBaseUrls,
            cancellationToken);

        ReadProfileCollection(
            document.RootElement,
            "CloudSources",
            ConnectionProfileKind.Cloud,
            profiles,
            seenIds,
            secrets,
            legacyBaseUrls,
            cancellationToken);

        // Older files may predate the source arrays. These fallbacks preserve
        // compatibility without normalizing or writing the legacy document.
        ReadSingleProfile(
            document.RootElement,
            "Local",
            "local-machine",
            "本机中转",
            ConnectionProfileKind.Local,
            profiles,
            seenIds,
            secrets,
            legacyBaseUrls);
        ReadSingleProfile(
            document.RootElement,
            "Lan",
            "lan-default",
            "局域网中转",
            ConnectionProfileKind.Lan,
            profiles,
            seenIds,
            secrets,
            legacyBaseUrls);
        ReadSingleProfile(
            document.RootElement,
            "Cloud",
            "cloud-default",
            "远程来源",
            ConnectionProfileKind.Cloud,
            profiles,
            seenIds,
            secrets,
            legacyBaseUrls);

        // The two fixed local entries have distinct semantics even though both
        // live in LocalSources in the old schema.
        for (int index = 0; index < profiles.Count; index++)
        {
            ConnectionProfile profile = profiles[index];
            if (string.Equals(profile.Id, "lan-default", StringComparison.OrdinalIgnoreCase))
            {
                profiles[index] = profile with { Kind = ConnectionProfileKind.Lan };
            }
            else if (string.Equals(profile.Id, "local-machine", StringComparison.OrdinalIgnoreCase))
            {
                profiles[index] = profile with { Kind = ConnectionProfileKind.Local };
            }
        }

        lock (_cacheGate)
        {
            ObjectDisposedException.ThrowIf(_disposeState != 0, this);
            Volatile.Write(
                ref _secrets,
                new ReadOnlyDictionary<string, string>(secrets));
            Volatile.Write(
                ref _legacyBaseUrls,
                new ReadOnlyDictionary<string, string>(legacyBaseUrls));
        }

        return profiles;
    }

    public async Task<ConnectionProfile?> GetByIdAsync(
        string id,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(id);
        IReadOnlyList<ConnectionProfile> profiles = await GetAllAsync(cancellationToken).ConfigureAwait(false);
        return profiles.FirstOrDefault(profile =>
            string.Equals(profile.Id, id, StringComparison.OrdinalIgnoreCase));
    }

    public ValueTask<string?> GetSecretAsync(
        string connectionProfileId,
        CliKind client,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(connectionProfileId);
        cancellationToken.ThrowIfCancellationRequested();
        if (Volatile.Read(ref _disposeState) != 0)
        {
            return ValueTask.FromResult<string?>(null);
        }

        string reference = CreateCredentialReference(connectionProfileId, ToLegacyClientName(client));
        IReadOnlyDictionary<string, string> snapshot = Volatile.Read(ref _secrets);
        return ValueTask.FromResult(snapshot.TryGetValue(reference, out string? secret) ? secret : null);
    }

    /// <summary>
    /// Resolves a transient credential reference for a specific legacy client.
    /// This method never reads or writes a second file and should only be called
    /// immediately before injecting a child-process environment.
    /// </summary>
    public bool TryGetLegacySecret(string credentialId, CliKind client, out string secret)
        => TryGetLegacySecret(credentialId, ToLegacyClientName(client), out secret);

    /// <summary>
    /// String overload retained for the legacy Grok provider, which is present
    /// in profiles.json even when no standalone Grok CLI exists in Core.
    /// </summary>
    public bool TryGetLegacySecret(string credentialId, string clientName, out string secret)
    {
        secret = string.Empty;
        if (Volatile.Read(ref _disposeState) != 0 ||
            string.IsNullOrWhiteSpace(credentialId) ||
            string.IsNullOrWhiteSpace(clientName) ||
            !credentialId.StartsWith(CredentialPrefix, StringComparison.Ordinal))
        {
            return false;
        }

        IReadOnlyDictionary<string, string> snapshot = Volatile.Read(ref _secrets);
        int clientSeparator = credentialId.LastIndexOf(':');
        string exactReference = clientSeparator > CredentialPrefix.Length
            ? $"{credentialId[..clientSeparator]}:{clientName}"
            : $"{credentialId}:{clientName}";
        return snapshot.TryGetValue(exactReference, out secret!);
    }

    /// <summary>
    /// Preserves all four legacy endpoint variants, including Grok, without
    /// pretending that Grok is a separately launchable official CLI.
    /// </summary>
    public bool TryGetLegacyBaseUrl(string connectionProfileId, string clientName, out string baseUrl)
    {
        baseUrl = string.Empty;
        if (Volatile.Read(ref _disposeState) != 0 ||
            string.IsNullOrWhiteSpace(connectionProfileId) ||
            string.IsNullOrWhiteSpace(clientName))
        {
            return false;
        }

        IReadOnlyDictionary<string, string> snapshot = Volatile.Read(ref _legacyBaseUrls);
        return snapshot.TryGetValue(CreateClientLookupKey(connectionProfileId, clientName), out baseUrl!);
    }

    private static void ReadProfileCollection(
        JsonElement root,
        string propertyName,
        ConnectionProfileKind defaultKind,
        ICollection<ConnectionProfile> profiles,
        ISet<string> seenIds,
        IDictionary<string, string> secrets,
        IDictionary<string, string> legacyBaseUrls,
        CancellationToken cancellationToken)
    {
        if (!TryGetProperty(root, propertyName, out JsonElement collection) ||
            collection.ValueKind != JsonValueKind.Array)
        {
            return;
        }

        foreach (JsonElement item in collection.EnumerateArray())
        {
            cancellationToken.ThrowIfCancellationRequested();
            AddProfile(item, null, null, defaultKind, profiles, seenIds, secrets, legacyBaseUrls);
        }
    }

    private static void ReadSingleProfile(
        JsonElement root,
        string propertyName,
        string fallbackId,
        string fallbackName,
        ConnectionProfileKind kind,
        ICollection<ConnectionProfile> profiles,
        ISet<string> seenIds,
        IDictionary<string, string> secrets,
        IDictionary<string, string> legacyBaseUrls)
    {
        if (TryGetProperty(root, propertyName, out JsonElement profile) &&
            profile.ValueKind == JsonValueKind.Object)
        {
            AddProfile(profile, fallbackId, fallbackName, kind, profiles, seenIds, secrets, legacyBaseUrls);
        }
    }

    private static void AddProfile(
        JsonElement source,
        string? fallbackId,
        string? fallbackName,
        ConnectionProfileKind kind,
        ICollection<ConnectionProfile> profiles,
        ISet<string> seenIds,
        IDictionary<string, string> secrets,
        IDictionary<string, string> legacyBaseUrls)
    {
        if (source.ValueKind != JsonValueKind.Object)
        {
            return;
        }

        string id = GetString(source, "Id")?.Trim() ?? fallbackId ?? string.Empty;
        if (string.IsNullOrWhiteSpace(id))
        {
            // No secret material participates in the generated identifier.
            string identity = string.Join(
                "\n",
                GetString(source, "Name") ?? string.Empty,
                ReadClient(source, "Codex").BaseUrl,
                ReadClient(source, "Claude").BaseUrl,
                ReadClient(source, "Gemini").BaseUrl,
                ReadClient(source, "Grok").BaseUrl);
            id = $"legacy-{Convert.ToHexString(System.Security.Cryptography.SHA256.HashData(System.Text.Encoding.UTF8.GetBytes(identity))).ToLowerInvariant()}";
        }

        if (!seenIds.Add(id))
        {
            return;
        }

        string name = GetString(source, "Name")?.Trim() ?? fallbackName ?? "未命名来源";
        string? notes = NullIfWhiteSpace(GetString(source, "Notes"));

        var clientData = new Dictionary<string, LegacyClientData>(StringComparer.OrdinalIgnoreCase)
        {
            ["Codex"] = ReadClient(source, "Codex"),
            ["Claude"] = ReadClient(source, "Claude"),
            ["Gemini"] = ReadClient(source, "Gemini"),
            ["Grok"] = ReadClient(source, "Grok"),
        };

        var enabledClients = new List<CliKind>(4);
        var clientBaseUrls = new Dictionary<CliKind, string>();
        var credentialHints = new Dictionary<CliKind, ConnectionCredentialHint>();
        if (clientData["Codex"].IsConfigured)
        {
            enabledClients.Add(CliKind.Codex);
            AddBaseUrl(clientBaseUrls, CliKind.Codex, clientData["Codex"].BaseUrl);
        }

        AddCredentialHint(credentialHints, CliKind.Codex, clientData["Codex"].Secret);

        if (clientData["Claude"].IsConfigured)
        {
            enabledClients.Add(CliKind.ClaudeCode);
            AddBaseUrl(clientBaseUrls, CliKind.ClaudeCode, clientData["Claude"].BaseUrl);
        }

        AddCredentialHint(credentialHints, CliKind.ClaudeCode, clientData["Claude"].Secret);

        if (clientData["Gemini"].IsConfigured)
        {
            enabledClients.Add(CliKind.GeminiCli);
            AddBaseUrl(clientBaseUrls, CliKind.GeminiCli, clientData["Gemini"].BaseUrl);
        }

        AddCredentialHint(credentialHints, CliKind.GeminiCli, clientData["Gemini"].Secret);

        if (clientData["Grok"].IsConfigured)
        {
            enabledClients.Add(CliKind.GrokCli);
            AddBaseUrl(clientBaseUrls, CliKind.GrokCli, clientData["Grok"].BaseUrl);
        }

        AddCredentialHint(credentialHints, CliKind.GrokCli, clientData["Grok"].Secret);

        string baseUrl = SelectProfileBaseUrl(
        [
            clientData["Codex"],
            clientData["Claude"],
            clientData["Gemini"],
            clientData["Grok"],
        ]);
        // LAN keeps its explicit dashboard-address migration.  The fixed
        // local profile may also carry an explicit DashboardUrl for a custom
        // native UI port; consumers still validate that it resolves to this
        // computer before opening it.
        string? dashboardUrl = string.Equals(id, ConnectionProfileIds.LanDefault, StringComparison.OrdinalIgnoreCase)
            ? ReadLanDashboardUrl(source, clientData)
            : NullIfWhiteSpace(GetString(source, "DashboardUrl"));
        string? credentialId = null;
        foreach ((string clientName, LegacyClientData data) in clientData)
        {
            if (!string.IsNullOrWhiteSpace(data.BaseUrl))
            {
                CliKind client = clientName switch
                {
                    "Codex" => CliKind.Codex,
                    "Claude" => CliKind.ClaudeCode,
                    "Gemini" => CliKind.GeminiCli,
                    "Grok" => CliKind.GrokCli,
                    _ => throw new InvalidDataException($"不支持的客户端类型：{clientName}"),
                };
                legacyBaseUrls[CreateClientLookupKey(id, clientName)] =
                    ConnectionEndpointNormalizer.Normalize(client, data.BaseUrl);
            }

            if (string.IsNullOrWhiteSpace(data.Secret))
            {
                continue;
            }

            string reference = CreateCredentialReference(id, clientName);
            secrets[reference] = data.Secret!;
            credentialId ??= reference;
        }

        profiles.Add(new ConnectionProfile
        {
            Id = id,
            Name = name,
            Kind = kind,
            BaseUrl = baseUrl,
            ClientBaseUrls = clientBaseUrls,
            ApiKeyCredentialId = credentialId,
            DashboardUrl = dashboardUrl,
            ClientCredentialHints = credentialHints,
            EnabledClients = enabledClients,
            DefaultModels = new Dictionary<CliKind, string>(),
            Notes = notes,
        });
    }

    private static LegacyClientData ReadClient(JsonElement profile, string propertyName)
    {
        if (!TryGetProperty(profile, propertyName, out JsonElement client) ||
            client.ValueKind != JsonValueKind.Object)
        {
            return default;
        }

        return new LegacyClientData(
            NullIfWhiteSpace(GetString(client, "BaseUrl")),
            NullIfWhiteSpace(GetString(client, "Secret")));
    }

    private static string SelectProfileBaseUrl(IEnumerable<LegacyClientData> clients)
    {
        foreach (LegacyClientData client in clients)
        {
            if (!string.IsNullOrWhiteSpace(client.BaseUrl))
            {
                return client.BaseUrl!;
            }
        }

        return string.Empty;
    }

    /// <summary>
    /// The legacy schema used API endpoints as its only URL fields.  Preserve an
    /// explicit dashboard address when present; otherwise provide a safe native
    /// migration for the production endpoint on port 8080.
    /// Other ports deliberately remain unguessed and must be configured by the
    /// user in Connection Center.
    /// </summary>
    private static string? ReadLanDashboardUrl(
        JsonElement profile,
        IReadOnlyDictionary<string, LegacyClientData> clients)
    {
        string? configured = NullIfWhiteSpace(GetString(profile, "DashboardUrl"));
        if (configured is not null)
        {
            return configured;
        }

        foreach (string clientName in new[] { "Codex", "Claude", "Gemini", "Grok" })
        {
            if (clients.TryGetValue(clientName, out LegacyClientData client) &&
                TryCreateNativeDashboardUrl(client.BaseUrl, out string dashboardUrl))
            {
                return dashboardUrl;
            }
        }

        return null;
    }

    private static bool TryCreateNativeDashboardUrl(string? apiUrl, out string dashboardUrl)
    {
        dashboardUrl = string.Empty;
        if (string.IsNullOrWhiteSpace(apiUrl) ||
            !Uri.TryCreate(apiUrl.Trim(), UriKind.Absolute, out Uri? apiUri) ||
            apiUri.Scheme is not ("http" or "https") ||
            string.IsNullOrWhiteSpace(apiUri.Host) ||
            !string.IsNullOrWhiteSpace(apiUri.UserInfo) ||
            apiUri.Port != 8080)
        {
            return false;
        }

        dashboardUrl = new UriBuilder(apiUri.Scheme, apiUri.Host, apiUri.Port)
        {
            Path = "/dashboard",
        }.Uri.AbsoluteUri;
        return true;
    }

    private static void AddBaseUrl(
        IDictionary<CliKind, string> target,
        CliKind client,
        string? baseUrl)
    {
        if (!string.IsNullOrWhiteSpace(baseUrl))
        {
            target[client] = ConnectionEndpointNormalizer.Normalize(client, baseUrl);
        }
    }

    private static void AddCredentialHint(
        IDictionary<CliKind, ConnectionCredentialHint> target,
        CliKind client,
        string? secret)
    {
        if (string.IsNullOrWhiteSpace(secret))
        {
            return;
        }

        string normalized = secret.Trim();
        byte[] digest = System.Security.Cryptography.SHA256.HashData(
            System.Text.Encoding.UTF8.GetBytes(normalized));
        string fingerprint = Convert.ToHexString(digest)[..12];
        string preview = normalized.Length switch
        {
            <= 4 => "••••",
            <= 8 => $"{normalized[..2]}••••",
            _ => $"{normalized[..3]}••••{normalized[^4..]}",
        };
        target[client] = new ConnectionCredentialHint(preview, fingerprint);
    }

    private static string CreateCredentialReference(string profileId, string clientName)
        => $"{CredentialPrefix}{profileId}:{clientName}";

    private static string CreateClientLookupKey(string profileId, string clientName)
        => $"{profileId}\0{clientName}";

    private static string ToLegacyClientName(CliKind client)
        => client switch
        {
            CliKind.Codex => "Codex",
            CliKind.ClaudeCode => "Claude",
            CliKind.GeminiCli => "Gemini",
            CliKind.GrokCli => "Grok",
            _ => client.ToString(),
        };

    private static bool TryGetProperty(JsonElement element, string name, out JsonElement value)
    {
        if (element.TryGetProperty(name, out value))
        {
            return true;
        }

        foreach (JsonProperty property in element.EnumerateObject())
        {
            if (string.Equals(property.Name, name, StringComparison.OrdinalIgnoreCase))
            {
                value = property.Value;
                return true;
            }
        }

        value = default;
        return false;
    }

    private static string? GetString(JsonElement element, string propertyName)
    {
        if (!TryGetProperty(element, propertyName, out JsonElement value) ||
            value.ValueKind is JsonValueKind.Null or JsonValueKind.Undefined)
        {
            return null;
        }

        return value.ValueKind == JsonValueKind.String ? value.GetString() : null;
    }

    private void ClearSecrets()
    {
        lock (_cacheGate)
        {
            Volatile.Write(
                ref _secrets,
                new ReadOnlyDictionary<string, string>(
                    new Dictionary<string, string>(StringComparer.Ordinal)));
            Volatile.Write(
                ref _legacyBaseUrls,
                new ReadOnlyDictionary<string, string>(
                    new Dictionary<string, string>(StringComparer.Ordinal)));
        }
    }

    public void Dispose()
    {
        lock (_cacheGate)
        {
            if (Interlocked.Exchange(ref _disposeState, 1) != 0)
            {
                return;
            }

            Volatile.Write(
                ref _secrets,
                new ReadOnlyDictionary<string, string>(
                    new Dictionary<string, string>(StringComparer.Ordinal)));
            Volatile.Write(
                ref _legacyBaseUrls,
                new ReadOnlyDictionary<string, string>(
                    new Dictionary<string, string>(StringComparer.Ordinal)));
        }
    }

    private static string? NullIfWhiteSpace(string? value)
        => string.IsNullOrWhiteSpace(value) ? null : value.Trim();

    private readonly record struct LegacyClientData(string? BaseUrl, string? Secret)
    {
        public bool IsConfigured =>
            !string.IsNullOrWhiteSpace(BaseUrl) || !string.IsNullOrWhiteSpace(Secret);
    }
}

