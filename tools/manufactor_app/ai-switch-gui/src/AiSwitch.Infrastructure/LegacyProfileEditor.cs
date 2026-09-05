using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Performs constrained, compatibility-preserving edits against the legacy
/// profiles.json document. Unknown JSON fields are retained and every write is
/// replaced atomically with the previous document kept as profiles.json.bak.
/// </summary>
public sealed class LegacyProfileEditor : IConnectionProfileEditor, IDisposable
{
    private const string LocalMachineName = "本机中转";
    private const string LanDefaultName = "局域网中转";
    private const string CredentialPrefix = "legacy:";

    private static readonly JsonDocumentOptions DocumentOptions = new()
    {
        AllowTrailingCommas = true,
        CommentHandling = JsonCommentHandling.Skip,
    };

    private static readonly JsonSerializerOptions SerializerOptions = new()
    {
        WriteIndented = true,
    };

    private readonly string _profilesPath;
    private readonly SemaphoreSlim _writeGate = new(1, 1);
    private int _disposeState;

    public LegacyProfileEditor(AppDataPaths paths)
        : this(paths?.LegacyProfilesPath ?? throw new ArgumentNullException(nameof(paths)))
    {
    }

    public LegacyProfileEditor(LegacyProfileReader reader)
        : this(reader?.ProfilesPath ?? throw new ArgumentNullException(nameof(reader)))
    {
    }

    public LegacyProfileEditor(string profilesPath)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(profilesPath);
        _profilesPath = Path.GetFullPath(Environment.ExpandEnvironmentVariables(profilesPath));
    }

    public string ProfilesPath => _profilesPath;

    public Task<ConnectionProfile> AddAsync(
        ConnectionProfileDraft draft,
        CancellationToken cancellationToken = default)
    {
        ValidateDraft(draft);
        if (draft.Kind != ConnectionProfileKind.Cloud)
        {
            throw new InvalidOperationException("只能新增远程连接来源；本机中转和局域网中转是固定来源。");
        }

        string id = Guid.NewGuid().ToString("N");
        return MutateAndReadProfileAsync(
            root =>
            {
                JsonArray cloudSources = GetRequiredArray(root, "CloudSources");
                EnsureUniqueName(root, draft.Name, exceptId: null);
                cloudSources.Add(CreateProfileNode(id, draft));
                return id;
            },
            cancellationToken);
    }

    public Task<ConnectionProfile> UpdateAsync(
        string id,
        ConnectionProfileDraft draft,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(id);
        ValidateDraft(draft);
        string normalizedId = id.Trim();

        return MutateAndReadProfileAsync(
            root =>
            {
                (JsonObject profile, ConnectionProfileKind existingKind) =
                    FindEditableProfile(root, normalizedId);
                if (draft.Kind != existingKind)
                {
                    throw new InvalidOperationException("不能修改连接来源的类别。");
                }

                if ((existingKind is ConnectionProfileKind.Local or ConnectionProfileKind.Lan) &&
                    !string.IsNullOrWhiteSpace(draft.DashboardUrl))
                {
                    ValidateDashboardUrl(draft.DashboardUrl);
                }

                string name = existingKind switch
                {
                    ConnectionProfileKind.Local => LocalMachineName,
                    ConnectionProfileKind.Lan => LanDefaultName,
                    _ => draft.Name.Trim(),
                };
                if (existingKind == ConnectionProfileKind.Cloud)
                {
                    EnsureUniqueName(root, name, normalizedId);
                }

                ApplyProfileDraft(profile, normalizedId, name, draft);
                return normalizedId;
            },
            cancellationToken);
    }

    public async Task DeleteAsync(
        string id,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(id);
        string normalizedId = id.Trim();
        if (ConnectionProfileIds.IsFixed(normalizedId))
        {
            throw new InvalidOperationException("本机中转和局域网中转是固定来源，不能删除。");
        }

        await ExecuteMutationAsync(
            root =>
            {
                JsonArray cloudSources = GetRequiredArray(root, "CloudSources");
                int index = FindProfileIndex(cloudSources, normalizedId);
                if (index < 0)
                {
                    if (FindProfile(GetRequiredArray(root, "LocalSources"), normalizedId) is not null)
                    {
                        throw new InvalidOperationException("只能删除远程连接来源。");
                    }

                    throw new KeyNotFoundException($"未找到连接来源：{normalizedId}");
                }

                cloudSources.RemoveAt(index);
                string? selectedCloudId = NullIfWhiteSpace(GetString(root, "SelectedCloudSourceId"));
                if (string.Equals(selectedCloudId, normalizedId, StringComparison.OrdinalIgnoreCase))
                {
                    SetString(
                        root,
                        "SelectedCloudSourceId",
                        GetFirstProfileId(cloudSources) ?? string.Empty);
                }

                if (cloudSources.Count == 0)
                {
                    RemoveProperty(root, "Cloud");
                }

                NormalizeRouting(root);

                return true;
            },
            cancellationToken).ConfigureAwait(false);
    }

    public async Task<ConnectionProfileSelection> GetSelectionAsync(
        CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        await _writeGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            JsonObject root = await LoadRootAsync(cancellationToken).ConfigureAwait(false);
            EnsureDocumentShape(root);
            return new ConnectionProfileSelection(
                NullIfWhiteSpace(GetString(root, "SelectedCloudSourceId")),
                NullIfWhiteSpace(GetString(root, "SelectedLocalSourceId")),
                NullIfWhiteSpace(GetString(root, "ActiveConnectionProfileId")));
        }
        finally
        {
            _writeGate.Release();
        }
    }

    public async Task SetSelectedAsync(
        ConnectionProfileSelectionGroup group,
        string id,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(id);
        string normalizedId = id.Trim();

        await ExecuteMutationAsync(
            root =>
            {
                switch (group)
                {
                    case ConnectionProfileSelectionGroup.Cloud:
                        if (FindProfile(GetRequiredArray(root, "CloudSources"), normalizedId) is null)
                        {
                            throw new KeyNotFoundException($"未找到远程连接来源：{normalizedId}");
                        }

                        SetString(root, "SelectedCloudSourceId", normalizedId);
                        SetString(root, "ActiveConnectionProfileId", normalizedId);
                        break;

                    case ConnectionProfileSelectionGroup.Local:
                        if (!ConnectionProfileIds.IsFixed(normalizedId) ||
                            FindProfile(GetRequiredArray(root, "LocalSources"), normalizedId) is null)
                        {
                            throw new InvalidOperationException("本地类别只能选择本机中转或局域网中转。");
                        }

                        SetString(root, "SelectedLocalSourceId", normalizedId);
                        SetString(root, "ActiveConnectionProfileId", normalizedId);
                        break;

                    default:
                        throw new ArgumentOutOfRangeException(nameof(group), group, "不支持的连接选择类别。");
                }

                return true;
            },
            cancellationToken).ConfigureAwait(false);
    }

    public async Task<ConnectionProfileRouting> GetRoutingAsync(
        CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        await _writeGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            JsonObject root = await LoadRootAsync(cancellationToken).ConfigureAwait(false);
            EnsureDocumentShape(root);
            JsonObject routing = GetOrCreateObject(root, "Mixed");
            return new ConnectionProfileRouting(
                GetString(routing, "CodexSourceId") ?? ConnectionProfileIds.LocalMachine,
                GetString(routing, "ClaudeSourceId") ?? ConnectionProfileIds.LocalMachine,
                GetString(routing, "GeminiSourceId") ?? ConnectionProfileIds.LocalMachine,
                GetString(routing, "GrokSourceId") ?? ConnectionProfileIds.LocalMachine,
                ReadBackupProfileIds(root),
                GetBoolean(root, "BackupUpstreamEnabled") ?? false);
        }
        finally
        {
            _writeGate.Release();
        }
    }

    public async Task SetRoutingAsync(
        ConnectionProfileRouting routing,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(routing);
        ArgumentException.ThrowIfNullOrWhiteSpace(routing.CodexProfileId);
        ArgumentException.ThrowIfNullOrWhiteSpace(routing.ClaudeCodeProfileId);
        ArgumentException.ThrowIfNullOrWhiteSpace(routing.GeminiCliProfileId);

        await ExecuteMutationAsync(
            root =>
            {
                JsonArray cloudSources = GetRequiredArray(root, "CloudSources");
                JsonArray localSources = GetRequiredArray(root, "LocalSources");
                JsonObject mixed = GetOrCreateObject(root, "Mixed");
                SetRoutingSource(
                    mixed,
                    "CodexSourceId",
                    "CodexSource",
                    routing.CodexProfileId.Trim(),
                    cloudSources,
                    localSources);
                SetRoutingSource(
                    mixed,
                    "ClaudeSourceId",
                    "ClaudeSource",
                    routing.ClaudeCodeProfileId.Trim(),
                    cloudSources,
                    localSources);
                SetRoutingSource(
                    mixed,
                    "GeminiSourceId",
                    "GeminiSource",
                    routing.GeminiCliProfileId.Trim(),
                    cloudSources,
                    localSources);
                SetRoutingSource(
                    mixed,
                    "GrokSourceId",
                    "GrokSource",
                    ResolveOptionalGrokRoutingId(routing).Trim(),
                    cloudSources,
                    localSources);
                root["BackupSourceIds"] = new JsonArray(
                    (routing.BackupProfileIds ?? [])
                    .Where(id => !string.IsNullOrWhiteSpace(id))
                    .Select(id => id.Trim())
                    .Where(id => FindProfile(cloudSources, id) is not null)
                    .Distinct(StringComparer.OrdinalIgnoreCase)
                    .Select(id => JsonValue.Create(id))
                    .ToArray());
                SetBoolean(root, "BackupUpstreamEnabled", routing.BackupUpstreamEnabled);
                return true;
            },
            cancellationToken).ConfigureAwait(false);
    }

    private async Task<ConnectionProfile> MutateAndReadProfileAsync(
        Func<JsonObject, string> mutation,
        CancellationToken cancellationToken)
    {
        string id = await ExecuteMutationAsync(mutation, cancellationToken).ConfigureAwait(false);
        using var reader = new LegacyProfileReader(_profilesPath);
        return await reader.GetByIdAsync(id, cancellationToken).ConfigureAwait(false)
            ?? throw new InvalidDataException($"连接来源写入后无法重新读取：{id}");
    }

    private async Task<TResult> ExecuteMutationAsync<TResult>(
        Func<JsonObject, TResult> mutation,
        CancellationToken cancellationToken)
    {
        ThrowIfDisposed();
        ArgumentNullException.ThrowIfNull(mutation);
        await _writeGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ThrowIfDisposed();
            JsonObject root = await LoadRootAsync(cancellationToken).ConfigureAwait(false);
            EnsureDocumentShape(root);
            TResult result = mutation(root);
            EnsureDocumentShape(root);
            SynchronizeLegacyAliases(root);
            await WriteAtomicAsync(root, cancellationToken).ConfigureAwait(false);
            return result;
        }
        finally
        {
            _writeGate.Release();
        }
    }

    private async Task<JsonObject> LoadRootAsync(CancellationToken cancellationToken)
    {
        if (!File.Exists(_profilesPath))
        {
            return new JsonObject();
        }

        string json = await File.ReadAllTextAsync(_profilesPath, cancellationToken).ConfigureAwait(false);
        try
        {
            JsonNode? node = JsonNode.Parse(json, nodeOptions: null, DocumentOptions);
            return node as JsonObject
                ?? throw new InvalidDataException("旧 profiles.json 的根节点必须是 JSON 对象。");
        }
        catch (JsonException exception)
        {
            throw new InvalidDataException("旧 profiles.json 不是有效的 JSON。", exception);
        }
    }

    private async Task WriteAtomicAsync(JsonObject root, CancellationToken cancellationToken)
    {
        string? directory = Path.GetDirectoryName(_profilesPath);
        if (!string.IsNullOrWhiteSpace(directory))
        {
            Directory.CreateDirectory(directory);
        }

        string tempPath = $"{_profilesPath}.{Guid.NewGuid():N}.tmp";
        string backupPath = $"{_profilesPath}.bak";
        string backupTempPath = $"{backupPath}.{Guid.NewGuid():N}.tmp";
        try
        {
            string json = root.ToJsonString(SerializerOptions);
            await File.WriteAllTextAsync(
                    tempPath,
                    json,
                    new UTF8Encoding(encoderShouldEmitUTF8Identifier: false),
                    cancellationToken)
                .ConfigureAwait(false);
            cancellationToken.ThrowIfCancellationRequested();

            if (File.Exists(_profilesPath))
            {
                File.Replace(tempPath, _profilesPath, backupTempPath, ignoreMetadataErrors: true);
                File.Move(backupTempPath, backupPath, overwrite: true);
            }
            else
            {
                File.Move(tempPath, _profilesPath);
                File.Copy(_profilesPath, backupPath, overwrite: true);
            }
        }
        finally
        {
            DeleteFileIfPresent(tempPath);
            DeleteFileIfPresent(backupTempPath);
        }
    }

    private static void EnsureDocumentShape(JsonObject root)
    {
        JsonArray cloudSources = GetOrCreateArray(root, "CloudSources");
        if (cloudSources.Count == 0 && GetObject(root, "Cloud") is { } legacyCloud)
        {
            JsonObject cloud = (JsonObject)legacyCloud.DeepClone();
            string id = NullIfWhiteSpace(GetString(cloud, "Id")) ?? "cloud-default";
            SetString(cloud, "Id", id);
            SetString(cloud, "Name", NullIfWhiteSpace(GetString(cloud, "Name")) ?? "远程来源");
            cloudSources.Add(cloud);
        }

        JsonArray localSources = GetOrCreateArray(root, "LocalSources");
        EnsureFixedProfile(
            localSources,
            GetObject(root, "Local"),
            ConnectionProfileIds.LocalMachine,
            LocalMachineName,
            "用于当前机器运行的本机中转服务。",
            "http://127.0.0.1:8080/v1",
            "http://127.0.0.1:8080");
        EnsureFixedProfile(
            localSources,
            GetObject(root, "Lan"),
            ConnectionProfileIds.LanDefault,
            LanDefaultName,
            "用于另一台局域网机器上的中转服务。",
            "http://192.168.x.x:8080/v1",
            "http://192.168.x.x:8080");

        JsonObject local = FindProfile(localSources, ConnectionProfileIds.LocalMachine)!;
        JsonObject lan = FindProfile(localSources, ConnectionProfileIds.LanDefault)!;
        SetString(local, "Name", LocalMachineName);
        SetString(local, "Id", ConnectionProfileIds.LocalMachine);
        SetString(lan, "Name", LanDefaultName);
        SetString(lan, "Id", ConnectionProfileIds.LanDefault);
        EnsureLanDashboardUrl(lan);

        string? selectedCloud = NullIfWhiteSpace(GetString(root, "SelectedCloudSourceId"));
        if (selectedCloud is null || FindProfile(cloudSources, selectedCloud) is null)
        {
            SetString(root, "SelectedCloudSourceId", GetFirstProfileId(cloudSources) ?? string.Empty);
        }

        string? selectedLocal = NullIfWhiteSpace(GetString(root, "SelectedLocalSourceId"));
        if (!ConnectionProfileIds.IsFixed(selectedLocal) ||
            FindProfile(localSources, selectedLocal!) is null)
        {
            SetString(root, "SelectedLocalSourceId", ConnectionProfileIds.LocalMachine);
        }

        string? activeProfileId = NullIfWhiteSpace(GetString(root, "ActiveConnectionProfileId"));
        if (activeProfileId is not null && !IsKnownProfile(activeProfileId, cloudSources, localSources))
        {
            RemoveProperty(root, "ActiveConnectionProfileId");
        }

        NormalizeRouting(root);
        root["BackupSourceIds"] = new JsonArray(
            ReadBackupProfileIds(root)
                .Where(id => FindProfile(cloudSources, id) is not null)
                .Distinct(StringComparer.OrdinalIgnoreCase)
                .Select(id => JsonValue.Create(id))
                .ToArray());
        if (GetBoolean(root, "BackupUpstreamEnabled") is null)
        {
            SetBoolean(root, "BackupUpstreamEnabled", ReadBackupProfileIds(root).Count > 0);
        }
    }

    private static IReadOnlyList<string> ReadBackupProfileIds(JsonObject root)
    {
        if (root["BackupSourceIds"] is not JsonArray values)
        {
            return [];
        }

        return values
            .Select(value => value?.GetValue<string>()?.Trim())
            .Where(value => !string.IsNullOrWhiteSpace(value))
            .Cast<string>()
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray();
    }

    private static void NormalizeRouting(JsonObject root)
    {
        JsonArray cloudSources = GetRequiredArray(root, "CloudSources");
        JsonArray localSources = GetRequiredArray(root, "LocalSources");
        string selectedLocal = NullIfWhiteSpace(GetString(root, "SelectedLocalSourceId"))
            ?? ConnectionProfileIds.LocalMachine;
        if (FindProfile(localSources, selectedLocal) is null)
        {
            selectedLocal = ConnectionProfileIds.LocalMachine;
        }

        string cloudFallback = GetFirstProfileId(cloudSources) ?? selectedLocal;
        JsonObject mixed = GetOrCreateObject(root, "Mixed");
        NormalizeRoutingSource(
            mixed,
            "CodexSourceId",
            "CodexSource",
            cloudFallback,
            selectedLocal,
            defaultToCloud: true,
            cloudSources,
            localSources);
        NormalizeRoutingSource(
            mixed,
            "ClaudeSourceId",
            "ClaudeSource",
            cloudFallback,
            selectedLocal,
            defaultToCloud: false,
            cloudSources,
            localSources);
        NormalizeRoutingSource(
            mixed,
            "GeminiSourceId",
            "GeminiSource",
            cloudFallback,
            selectedLocal,
            defaultToCloud: false,
            cloudSources,
            localSources);
        NormalizeOptionalGrokRoutingSource(
            mixed,
            cloudFallback,
            selectedLocal,
            cloudSources,
            localSources);
    }

    private static void NormalizeOptionalGrokRoutingSource(
        JsonObject mixed,
        string cloudFallback,
        string localFallback,
        JsonArray cloudSources,
        JsonArray localSources)
    {
        string? current = NullIfWhiteSpace(GetString(mixed, "GrokSourceId"));
        if (current is not null && IsKnownProfile(current, cloudSources, localSources))
        {
            SetRoutingSource(mixed, "GrokSourceId", "GrokSource", current, cloudSources, localSources);
            return;
        }

        string? geminiSourceId = NullIfWhiteSpace(GetString(mixed, "GeminiSourceId"));
        string resolved = geminiSourceId is not null && IsKnownProfile(geminiSourceId, cloudSources, localSources)
            ? geminiSourceId
            : ReadLegacyCloudMode(mixed, "GrokSource", defaultToCloud: false) &&
              FindProfile(cloudSources, cloudFallback) is not null
                ? cloudFallback
                : localFallback;

        SetRoutingSource(mixed, "GrokSourceId", "GrokSource", resolved, cloudSources, localSources);
    }

    private static void NormalizeRoutingSource(
        JsonObject mixed,
        string idProperty,
        string modeProperty,
        string cloudFallback,
        string localFallback,
        bool defaultToCloud,
        JsonArray cloudSources,
        JsonArray localSources)
    {
        string? current = NullIfWhiteSpace(GetString(mixed, idProperty));
        string resolved;
        if (current is not null && IsKnownProfile(current, cloudSources, localSources))
        {
            resolved = current;
        }
        else
        {
            bool useCloud = ReadLegacyCloudMode(mixed, modeProperty, defaultToCloud);
            resolved = useCloud && FindProfile(cloudSources, cloudFallback) is not null
                ? cloudFallback
                : localFallback;
        }

        SetRoutingSource(mixed, idProperty, modeProperty, resolved, cloudSources, localSources);
    }

    private static void SetRoutingSource(
        JsonObject mixed,
        string idProperty,
        string modeProperty,
        string id,
        JsonArray cloudSources,
        JsonArray localSources)
    {
        if (!IsKnownProfile(id, cloudSources, localSources))
        {
            throw new InvalidOperationException($"混合模式引用的来源不存在：{id}");
        }

        SetString(mixed, idProperty, id);
        // Legacy ClientSourceMode serializes as 0 (Cloud) or 1 (Local).
        SetNode(
            mixed,
            modeProperty,
            JsonValue.Create(FindProfile(cloudSources, id) is not null ? 0 : 1));
    }

    private static string ResolveOptionalGrokRoutingId(ConnectionProfileRouting routing) =>
        string.IsNullOrWhiteSpace(routing.GrokCliProfileId)
            ? routing.GeminiCliProfileId
            : routing.GrokCliProfileId;

    private static bool ReadLegacyCloudMode(
        JsonObject mixed,
        string modeProperty,
        bool defaultToCloud)
    {
        JsonNode? node = GetNode(mixed, modeProperty);
        if (node is not JsonValue value)
        {
            return defaultToCloud;
        }

        if (value.TryGetValue(out int numeric))
        {
            return numeric == 0;
        }

        if (value.TryGetValue(out string? text) && !string.IsNullOrWhiteSpace(text))
        {
            return string.Equals(text, "Cloud", StringComparison.OrdinalIgnoreCase) ||
                   string.Equals(text, "0", StringComparison.OrdinalIgnoreCase);
        }

        return defaultToCloud;
    }

    private static bool IsKnownProfile(string id, JsonArray cloudSources, JsonArray localSources) =>
        FindProfile(cloudSources, id) is not null ||
        FindProfile(localSources, id) is not null;

    private static void SynchronizeLegacyAliases(JsonObject root)
    {
        JsonArray cloudSources = GetRequiredArray(root, "CloudSources");
        JsonArray localSources = GetRequiredArray(root, "LocalSources");

        string? selectedCloudId = NullIfWhiteSpace(GetString(root, "SelectedCloudSourceId"));
        JsonObject? selectedCloud = selectedCloudId is null
            ? null
            : FindProfile(cloudSources, selectedCloudId);
        if (selectedCloud is not null)
        {
            SetNode(root, "Cloud", MergeAlias(selectedCloud, GetObject(root, "Cloud")));
        }
        else
        {
            RemoveProperty(root, "Cloud");
        }

        string selectedLocalId = NullIfWhiteSpace(GetString(root, "SelectedLocalSourceId"))
            ?? ConnectionProfileIds.LocalMachine;
        JsonObject selectedLocal = FindProfile(localSources, selectedLocalId)
            ?? FindProfile(localSources, ConnectionProfileIds.LocalMachine)!;
        JsonObject lan = FindProfile(localSources, ConnectionProfileIds.LanDefault)!;
        SetNode(root, "Local", MergeAlias(selectedLocal, GetObject(root, "Local")));
        SetNode(root, "Lan", MergeAlias(lan, GetObject(root, "Lan")));
    }

    private static void EnsureFixedProfile(
        JsonArray localSources,
        JsonObject? fallback,
        string id,
        string name,
        string notes,
        string codexBaseUrl,
        string otherBaseUrl)
    {
        if (FindProfile(localSources, id) is not null)
        {
            return;
        }

        JsonObject profile;
        if (fallback is not null &&
            (string.Equals(GetString(fallback, "Id"), id, StringComparison.OrdinalIgnoreCase) ||
             string.IsNullOrWhiteSpace(GetString(fallback, "Id"))))
        {
            profile = (JsonObject)fallback.DeepClone();
            SetString(profile, "Id", id);
            SetString(profile, "Name", name);
        }
        else
        {
            profile = CreateFixedProfileNode(id, name, notes, codexBaseUrl, otherBaseUrl);
        }

        localSources.Add(profile);
    }

    /// <summary>
    /// Older profiles only know the API endpoint.  The only automatic dashboard
    /// migration we make uses the production API/UI endpoint on port 8080;
    /// other endpoints need an explicit address in Connection Center.
    /// </summary>
    private static void EnsureLanDashboardUrl(JsonObject profile)
    {
        if (!string.IsNullOrWhiteSpace(GetString(profile, "DashboardUrl")))
        {
            return;
        }

        foreach (string clientName in new[] { "Codex", "Claude", "Gemini", "Grok" })
        {
            string? apiUrl = GetObject(profile, clientName) is { } client
                ? GetString(client, "BaseUrl")
                : null;
            if (TryCreateNativeDashboardUrl(apiUrl, out string dashboardUrl))
            {
                SetString(profile, "DashboardUrl", dashboardUrl);
                return;
            }
        }
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

    private static JsonObject CreateFixedProfileNode(
        string id,
        string name,
        string notes,
        string codexBaseUrl,
        string otherBaseUrl)
    {
        var profile = new JsonObject
        {
            ["Id"] = id,
            ["Name"] = name,
            ["Notes"] = notes,
            ["Codex"] = new JsonObject { ["BaseUrl"] = codexBaseUrl },
            ["Claude"] = new JsonObject { ["BaseUrl"] = otherBaseUrl },
            ["Gemini"] = new JsonObject { ["BaseUrl"] = otherBaseUrl },
            ["Grok"] = new JsonObject { ["BaseUrl"] = codexBaseUrl },
        };
        if (string.Equals(id, ConnectionProfileIds.LanDefault, StringComparison.OrdinalIgnoreCase))
        {
            EnsureLanDashboardUrl(profile);
        }

        return profile;
    }

    private static JsonObject CreateProfileNode(string id, ConnectionProfileDraft draft)
    {
        var profile = new JsonObject();
        ApplyProfileDraft(profile, id, draft.Name.Trim(), draft);
        return profile;
    }

    private static void ApplyProfileDraft(
        JsonObject profile,
        string id,
        string name,
        ConnectionProfileDraft draft)
    {
        SetString(profile, "Id", id);
        SetString(profile, "Name", name);
        SetString(profile, "Notes", draft.Notes?.Trim() ?? string.Empty);
        if (ConnectionProfileIds.IsFixed(id) &&
            !string.IsNullOrWhiteSpace(draft.DashboardUrl))
        {
            SetString(profile, "DashboardUrl", draft.DashboardUrl.Trim());
        }

        ApplyClientDraft(profile, "Codex", CliKind.Codex, draft.Codex);
        ApplyClientDraft(profile, "Claude", CliKind.ClaudeCode, draft.ClaudeCode);
        ApplyClientDraft(profile, "Gemini", CliKind.GeminiCli, draft.GeminiCli);
        ApplyClientDraft(profile, "Grok", CliKind.GrokCli, draft.GrokCli ?? ConnectionClientDraft.Empty);
    }

    private static void ApplyClientDraft(
        JsonObject profile,
        string propertyName,
        CliKind clientKind,
        ConnectionClientDraft draft)
    {
        ArgumentNullException.ThrowIfNull(draft);
        ArgumentNullException.ThrowIfNull(draft.SecretChange);
        JsonObject client = GetOrCreateObject(profile, propertyName);
        SetString(client, "BaseUrl", ConnectionEndpointNormalizer.Normalize(clientKind, draft.BaseUrl));

        switch (draft.SecretChange.Kind)
        {
            case ConnectionSecretChangeKind.Keep:
                break;
            case ConnectionSecretChangeKind.Replace:
                string replacement = draft.SecretChange.Replacement
                    ?? throw new InvalidOperationException("替换密钥时必须提供新密钥。");
                ArgumentException.ThrowIfNullOrWhiteSpace(replacement);
                SetString(client, "Secret", replacement);
                break;
            case ConnectionSecretChangeKind.Clear:
                RemoveProperty(client, "Secret");
                break;
            default:
                throw new ArgumentOutOfRangeException(
                    nameof(draft),
                    draft.SecretChange.Kind,
                    "不支持的密钥变更类型。");
        }
    }

    private static (JsonObject Profile, ConnectionProfileKind Kind) FindEditableProfile(
        JsonObject root,
        string id)
    {
        JsonArray localSources = GetRequiredArray(root, "LocalSources");
        if (FindProfile(localSources, id) is { } localProfile)
        {
            if (string.Equals(id, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase))
            {
                return (localProfile, ConnectionProfileKind.Local);
            }

            if (string.Equals(id, ConnectionProfileIds.LanDefault, StringComparison.OrdinalIgnoreCase))
            {
                return (localProfile, ConnectionProfileKind.Lan);
            }

            throw new InvalidOperationException("只能编辑固定的本机中转和局域网中转来源。");
        }

        return FindProfile(GetRequiredArray(root, "CloudSources"), id) is { } cloudProfile
            ? (cloudProfile, ConnectionProfileKind.Cloud)
            : throw new KeyNotFoundException($"未找到连接来源：{id}");
    }

    private static void EnsureUniqueName(JsonObject root, string name, string? exceptId)
    {
        string normalizedName = name.Trim();
        foreach (JsonObject profile in EnumerateProfiles(root))
        {
            string? id = GetString(profile, "Id");
            if (exceptId is not null && string.Equals(id, exceptId, StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            if (string.Equals(
                    NullIfWhiteSpace(GetString(profile, "Name")),
                    normalizedName,
                    StringComparison.CurrentCultureIgnoreCase))
            {
                throw new InvalidOperationException($"连接来源名称已存在：{normalizedName}");
            }
        }
    }

    private static IEnumerable<JsonObject> EnumerateProfiles(JsonObject root)
    {
        foreach (string collectionName in new[] { "CloudSources", "LocalSources" })
        {
            foreach (JsonNode? item in GetRequiredArray(root, collectionName))
            {
                if (item is JsonObject profile)
                {
                    yield return profile;
                }
            }
        }
    }

    private static JsonObject MergeAlias(JsonObject source, JsonObject? existingAlias)
    {
        var merged = (JsonObject)source.DeepClone();
        if (existingAlias is null)
        {
            return merged;
        }

        foreach ((string key, JsonNode? value) in existingAlias)
        {
            if (FindPropertyName(merged, key) is null)
            {
                merged[key] = value?.DeepClone();
            }
        }

        return merged;
    }

    private static JsonArray GetOrCreateArray(JsonObject parent, string propertyName)
    {
        if (GetNode(parent, propertyName) is JsonArray array)
        {
            return array;
        }

        var created = new JsonArray();
        SetNode(parent, propertyName, created);
        return created;
    }

    private static JsonArray GetRequiredArray(JsonObject parent, string propertyName) =>
        GetNode(parent, propertyName) as JsonArray
        ?? throw new InvalidDataException($"profiles.json 字段 {propertyName} 不是数组。");

    private static JsonObject GetOrCreateObject(JsonObject parent, string propertyName)
    {
        if (GetNode(parent, propertyName) is JsonObject value)
        {
            return value;
        }

        var created = new JsonObject();
        SetNode(parent, propertyName, created);
        return created;
    }

    private static JsonObject? GetObject(JsonObject parent, string propertyName) =>
        GetNode(parent, propertyName) as JsonObject;

    private static JsonNode? GetNode(JsonObject parent, string propertyName)
    {
        string? actualName = FindPropertyName(parent, propertyName);
        return actualName is null ? null : parent[actualName];
    }

    private static string? GetString(JsonObject parent, string propertyName)
    {
        if (GetNode(parent, propertyName) is not JsonValue value ||
            !value.TryGetValue(out string? result))
        {
            return null;
        }

        return result;
    }

    private static bool? GetBoolean(JsonObject parent, string propertyName)
    {
        if (GetNode(parent, propertyName) is not JsonValue value ||
            !value.TryGetValue(out bool result))
        {
            return null;
        }

        return result;
    }

    private static void SetString(JsonObject parent, string propertyName, string value) =>
        SetNode(parent, propertyName, JsonValue.Create(value));

    private static void SetBoolean(JsonObject parent, string propertyName, bool value) =>
        SetNode(parent, propertyName, JsonValue.Create(value));

    private static void SetNode(JsonObject parent, string propertyName, JsonNode? value)
    {
        string actualName = FindPropertyName(parent, propertyName) ?? propertyName;
        parent[actualName] = value;
    }

    private static void RemoveProperty(JsonObject parent, string propertyName)
    {
        string? actualName = FindPropertyName(parent, propertyName);
        if (actualName is not null)
        {
            parent.Remove(actualName);
        }
    }

    private static string? FindPropertyName(JsonObject parent, string propertyName)
    {
        foreach ((string key, _) in parent)
        {
            if (string.Equals(key, propertyName, StringComparison.OrdinalIgnoreCase))
            {
                return key;
            }
        }

        return null;
    }

    private static JsonObject? FindProfile(JsonArray profiles, string id)
    {
        foreach (JsonNode? item in profiles)
        {
            if (item is JsonObject profile &&
                string.Equals(GetString(profile, "Id"), id, StringComparison.OrdinalIgnoreCase))
            {
                return profile;
            }
        }

        return null;
    }

    private static int FindProfileIndex(JsonArray profiles, string id)
    {
        for (int index = 0; index < profiles.Count; index++)
        {
            if (profiles[index] is JsonObject profile &&
                string.Equals(GetString(profile, "Id"), id, StringComparison.OrdinalIgnoreCase))
            {
                return index;
            }
        }

        return -1;
    }

    private static string? GetFirstProfileId(JsonArray profiles)
    {
        foreach (JsonNode? item in profiles)
        {
            if (item is JsonObject profile && NullIfWhiteSpace(GetString(profile, "Id")) is { } id)
            {
                return id;
            }
        }

        return null;
    }

    private static void ValidateDraft(ConnectionProfileDraft draft)
    {
        ArgumentNullException.ThrowIfNull(draft);
        ArgumentException.ThrowIfNullOrWhiteSpace(draft.Name);
        ArgumentNullException.ThrowIfNull(draft.Codex);
        ArgumentNullException.ThrowIfNull(draft.ClaudeCode);
        ArgumentNullException.ThrowIfNull(draft.GeminiCli);
        ArgumentNullException.ThrowIfNull(draft.Codex.SecretChange);
        ArgumentNullException.ThrowIfNull(draft.ClaudeCode.SecretChange);
        ArgumentNullException.ThrowIfNull(draft.GeminiCli.SecretChange);
        ArgumentNullException.ThrowIfNull((draft.GrokCli ?? ConnectionClientDraft.Empty).SecretChange);
    }

    private static void ValidateDashboardUrl(string value)
    {
        if (!Uri.TryCreate(value.Trim(), UriKind.Absolute, out Uri? uri) ||
            uri.Scheme is not ("http" or "https") ||
            string.IsNullOrWhiteSpace(uri.Host) ||
            !string.IsNullOrWhiteSpace(uri.UserInfo) ||
            !string.IsNullOrWhiteSpace(uri.Query) ||
            !string.IsNullOrWhiteSpace(uri.Fragment))
        {
            throw new ArgumentException(
                "后台地址必须是无账号、查询参数或片段的 HTTP(S) 地址。",
                nameof(value));
        }
    }

    private static string? NullIfWhiteSpace(string? value) =>
        string.IsNullOrWhiteSpace(value) ? null : value.Trim();

    private static void DeleteFileIfPresent(string path)
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
            // Cleanup failure must not mask the original atomic-write result.
        }
    }

    private void ThrowIfDisposed() =>
        ObjectDisposedException.ThrowIf(Volatile.Read(ref _disposeState) != 0, this);

    public void Dispose()
    {
        if (Interlocked.Exchange(ref _disposeState, 1) == 0)
        {
            _writeGate.Dispose();
        }
    }
}











