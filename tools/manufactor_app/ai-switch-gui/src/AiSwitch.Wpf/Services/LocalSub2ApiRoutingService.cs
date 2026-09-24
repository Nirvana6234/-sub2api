using System.Net;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using AiSwitchGui;

namespace LanAi.Workspace.Wpf.Services;

internal interface ILocalSub2ApiRoutingService
{
    Task<LocalSub2ApiRoutingResult> ApplySourceAsync(
        ProfileStore store,
        string profileId,
        CancellationToken cancellationToken);

    Task<LocalSub2ApiRoutingResult> ApplyRoutingAsync(
        ProfileStore store,
        CancellationToken cancellationToken);

    Task<IReadOnlySet<string>> GetActiveBackupSourceIdsAsync(
        ProfileStore store,
        CancellationToken cancellationToken);
}

internal sealed record LocalSub2ApiRoutingIssue(string Platform, string Summary);

internal sealed record LocalSub2ApiRoutingResult(
    ProfileStore ClientStore,
    IReadOnlyList<string> UpdatedPlatforms)
{
    public IReadOnlyList<LocalSub2ApiRoutingIssue> Issues { get; init; } = [];
}

internal sealed record LocalSub2ApiRoutingTarget(string ProfileId, ClientProfile Client, int Order = 0);

/// <summary>
/// Migrates legacy direct-connect profiles into local Sub2API accounts and
/// switches the local client API keys between Sub2API groups. Sub2API remains
/// the only scheduler; this service only maintains its native entities.
/// </summary>
internal sealed class LocalSub2ApiRoutingService : ILocalSub2ApiRoutingService, IDisposable
{
    private const string ManagedPrefix = "共飞工作台";
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
        NumberHandling = JsonNumberHandling.AllowReadingFromString,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    };

    private readonly ISub2ApiSessionManager _sessionManager;
    private readonly Func<string?> _localControlTokenProvider;
    private readonly HttpClient _httpClient;
    private readonly bool _ownsHttpClient;

    internal LocalSub2ApiRoutingService(
        ISub2ApiSessionManager sessionManager,
        Func<string?>? localControlTokenProvider = null)
        : this(
            sessionManager,
            new HttpClient(new HttpClientHandler { UseProxy = false, AllowAutoRedirect = false })
            {
                Timeout = TimeSpan.FromSeconds(30),
            },
            ownsHttpClient: true,
            localControlTokenProvider)
    {
    }

    internal LocalSub2ApiRoutingService(
        ISub2ApiSessionManager sessionManager,
        HttpClient httpClient,
        bool ownsHttpClient = false,
        Func<string?>? localControlTokenProvider = null)
    {
        _sessionManager = sessionManager ?? throw new ArgumentNullException(nameof(sessionManager));
        _localControlTokenProvider = localControlTokenProvider ?? (() => null);
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        _ownsHttpClient = ownsHttpClient;
    }

    public async Task<LocalSub2ApiRoutingResult> ApplySourceAsync(
        ProfileStore store,
        string profileId,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(store);
        ArgumentException.ThrowIfNullOrWhiteSpace(profileId);

        ProfileDefinition source = FindProfile(store, profileId);
        if (string.Equals(source.Id, ProfileSourceIds.LocalMachine, StringComparison.OrdinalIgnoreCase))
        {
            return new LocalSub2ApiRoutingResult(store, Array.Empty<string>());
        }

        var requested = new Dictionary<string, IReadOnlyList<LocalSub2ApiRoutingTarget>>(StringComparer.OrdinalIgnoreCase)
        {
            ["openai"] = IsFullyConfigured(source.Codex) ? [new(source.Id, source.Codex)] : [],
            ["anthropic"] = IsFullyConfigured(source.Claude) ? [new(source.Id, source.Claude)] : [],
            ["gemini"] = IsFullyConfigured(source.Gemini) ? [new(source.Id, source.Gemini)] : [],
            ["grok"] = IsFullyConfigured(source.Grok) ? [new(source.Id, source.Grok)] : [],
        };
        return await ApplyCoreAsync(store, requested, ensureEmptyPools: false, cancellationToken).ConfigureAwait(false);
    }

    public async Task<LocalSub2ApiRoutingResult> ApplyRoutingAsync(
        ProfileStore store,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(store);
        IReadOnlyList<string> enabledSourceIds = ResolveEnabledSourceIds(store);
        ProfileDefinition[] enabledSources = enabledSourceIds
            .Select(id => FindProfile(store, id))
            .Where(source => !string.Equals(source.Id, ProfileSourceIds.LocalMachine, StringComparison.OrdinalIgnoreCase))
            .DistinctBy(source => source.Id, StringComparer.OrdinalIgnoreCase)
            .ToArray();
        var requested = new Dictionary<string, IReadOnlyList<LocalSub2ApiRoutingTarget>>(StringComparer.OrdinalIgnoreCase)
        {
            ["openai"] = BuildTargets(enabledSources, source => source.Codex),
            ["anthropic"] = BuildTargets(enabledSources, source => source.Claude),
            ["gemini"] = BuildTargets(enabledSources, source => source.Gemini),
            ["grok"] = BuildTargets(enabledSources, source => source.Grok),
        };
        return await ApplyCoreAsync(store, requested, ensureEmptyPools: true, cancellationToken).ConfigureAwait(false);
    }

    public async Task<IReadOnlySet<string>> GetActiveBackupSourceIdsAsync(
        ProfileStore store,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(store);
        if (store.BackupUpstreamEnabled != true)
        {
            return new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        }

        ProfileDefinition local = FindProfile(store, ProfileSourceIds.LocalMachine);
        Uri localApiBaseUri = RequireLocalApiBaseUri(local);
        Sub2ApiSessionAccess access = await _sessionManager
            .GetAccessAsync(localApiBaseUri, cancellationToken)
            .ConfigureAwait(false);
        if (!access.IsAdministrator)
        {
            return new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        }

        PagedData<AdminAccountData> accounts = await SendAsync<PagedData<AdminAccountData>>(
            access,
            HttpMethod.Get,
            "api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc",
            body: null,
            cancellationToken).ConfigureAwait(false);
        DateTimeOffset activeSince = DateTimeOffset.UtcNow.AddMinutes(-2);
        return (accounts.Items ?? [])
            .Where(account => account.LastUsedAt >= activeSince)
            .Select(GetWorkspaceSourceId)
            .Where(sourceId => !string.IsNullOrWhiteSpace(sourceId))
            .Select(sourceId => sourceId!)
            .ToHashSet(StringComparer.OrdinalIgnoreCase);
    }

    public void Dispose()
    {
        if (_ownsHttpClient)
        {
            _httpClient.Dispose();
        }
    }

    private async Task<LocalSub2ApiRoutingResult> ApplyCoreAsync(
        ProfileStore store,
        IReadOnlyDictionary<string, IReadOnlyList<LocalSub2ApiRoutingTarget>> requested,
        bool ensureEmptyPools,
        CancellationToken cancellationToken)
    {
        ProfileDefinition local = FindProfile(store, ProfileSourceIds.LocalMachine);
        Uri localApiBaseUri = RequireLocalApiBaseUri(local);
        Sub2ApiSessionAccess access = await GetAdministratorAccessAsync(localApiBaseUri, cancellationToken)
            .ConfigureAwait(false);

        if (!access.IsAdministrator)
        {
            throw new InvalidOperationException("本机中转中心需要使用唯一的本机管理员账号登录后才能修改调度。 ");
        }

        await EnsureLocalAdminBalanceAsync(access, cancellationToken).ConfigureAwait(false);

        List<GroupData> groups = (await SendAsync<GroupData[]>(
            access,
            HttpMethod.Get,
            "api/v1/admin/groups/all?include_inactive=true",
            body: null,
            cancellationToken).ConfigureAwait(false)).ToList();
        AccountListData accountList = await SendAsync<AccountListData>(
            access,
            HttpMethod.Get,
            "api/v1/account-contributions?page=1&limit=500",
            body: null,
            cancellationToken).ConfigureAwait(false);
        PagedData<AdminAccountData> adminAccountList = await SendAsync<PagedData<AdminAccountData>>(
            access,
            HttpMethod.Get,
            "api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc",
            body: null,
            cancellationToken).ConfigureAwait(false);
        PagedData<ApiKeyData> keyList = await SendAsync<PagedData<ApiKeyData>>(
            access,
            HttpMethod.Get,
            "api/v1/keys?page=1&page_size=1000",
            body: null,
            cancellationToken).ConfigureAwait(false);

        var localKeys = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        var updatedPlatforms = new List<string>();
        var issues = new List<LocalSub2ApiRoutingIssue>();
        foreach ((string platform, IReadOnlyList<LocalSub2ApiRoutingTarget> targets) in requested)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (targets.Count == 0 && !ensureEmptyPools)
            {
                continue;
            }

            try
            {
                localKeys[platform] = await ApplyTargetAsync(
                    access,
                    groups,
                    accountList,
                    adminAccountList,
                    keyList,
                    platform,
                    targets,
                    cancellationToken).ConfigureAwait(false);
                updatedPlatforms.Add(PlatformLabel(platform));
            }
            catch (Exception exception) when (exception is InvalidOperationException or JsonException)
            {
                issues.Add(new LocalSub2ApiRoutingIssue(
                    PlatformLabel(platform),
                    SafeMessage(exception.Message, "切换失败，已保持原路由不变。")));
            }
        }

        if (updatedPlatforms.Count == 0)
        {
            return new LocalSub2ApiRoutingResult(store, Array.Empty<string>()) { Issues = issues };
        }

        ProfileStore clientStore = BuildClientStore(store, local, localKeys);
        return new LocalSub2ApiRoutingResult(clientStore, updatedPlatforms) { Issues = issues };
    }

    private async Task<string> ApplyTargetAsync(
        Sub2ApiSessionAccess access,
        List<GroupData> groups,
        AccountListData accountList,
        PagedData<AdminAccountData> adminAccountList,
        PagedData<ApiKeyData> keyList,
        string platform,
        IReadOnlyList<LocalSub2ApiRoutingTarget> targets,
        CancellationToken cancellationToken)
    {
        string groupName = $"{ManagedPrefix}-{platform}-备用上游";
        GroupData group = groups.FirstOrDefault(item =>
                              string.Equals(item.Name, groupName, StringComparison.OrdinalIgnoreCase) &&
                              string.Equals(item.Platform, platform, StringComparison.OrdinalIgnoreCase))
                          ?? groups.FirstOrDefault(item =>
                              item.Name?.StartsWith(ManagedPrefix + "-", StringComparison.OrdinalIgnoreCase) == true &&
                              string.Equals(item.Platform, platform, StringComparison.OrdinalIgnoreCase))
                          ?? await CreateGroupAsync(access, groupName, platform, cancellationToken).ConfigureAwait(false);
        if (group.RateMultiplier != 0)
        {
            group = await UpdateManagedGroupAsync(access, group, cancellationToken).ConfigureAwait(false);
        }
        if (!groups.Any(item => item.Id == group.Id))
        {
            groups.Add(group);
        }

        var activeAccountIds = new HashSet<long>();
        foreach (LocalSub2ApiRoutingTarget target in targets.OrderBy(target => target.Order))
        {
            ClientProfile upstream = target.Client;
            string sourceMarker = StableMarker(target.ProfileId, platform);
            string accountName = $"{ManagedPrefix}-{sourceMarker}-上游-{CredentialMarker(upstream)}";
            AdminAccountData? existingAccount = adminAccountList.Items?.FirstOrDefault(item =>
                string.Equals(item.Name, accountName, StringComparison.OrdinalIgnoreCase) &&
                string.Equals(item.Platform, platform, StringComparison.OrdinalIgnoreCase));
            long accountId;
            if (existingAccount is null)
            {
                accountId = await CreateExternalAccountAsync(
                    access,
                    accountName,
                    platform,
                    upstream,
                    group.Id,
                    target.ProfileId,
                    target.Order,
                    cancellationToken).ConfigureAwait(false);
            }
            else
            {
                accountId = existingAccount.Id;
                await UpdateExternalAccountAsync(
                    access,
                    existingAccount.Id,
                    group.Id,
                    target.Order,
                    cancellationToken).ConfigureAwait(false);
            }
            activeAccountIds.Add(accountId);
        }

        await RemoveSupersededExternalAccountsAsync(
            access,
            adminAccountList.Items ?? [],
            platform,
            activeAccountIds,
            cancellationToken).ConfigureAwait(false);
        await UnbindPersonalAccountsAsync(
            access,
            accountList.Items ?? [],
            platform,
            cancellationToken).ConfigureAwait(false);

        string keyName = $"{ManagedPrefix}-{PlatformLabel(platform)}-客户端";
        ApiKeyData? apiKey = keyList.Items?.FirstOrDefault(item =>
            string.Equals(item.Name, keyName, StringComparison.OrdinalIgnoreCase));
        if (apiKey is null)
        {
            apiKey = await CreateClientKeyAsync(access, keyName, group.Id, cancellationToken).ConfigureAwait(false);
        }
        else
        {
            await UpdateClientKeyAsync(access, apiKey.Id, keyName, group.Id, cancellationToken).ConfigureAwait(false);
        }

        if (string.IsNullOrWhiteSpace(apiKey.Key))
        {
            throw new InvalidOperationException($"本机后台没有返回 {PlatformLabel(platform)} 客户端密钥。");
        }
        return apiKey.Key;
    }

    private async Task EnsureLocalAdminBalanceAsync(
        Sub2ApiSessionAccess access,
        CancellationToken cancellationToken)
    {
        if (access.Balance > 0)
        {
            return;
        }

        UserData user = await SendAsync<UserData>(
            access,
            HttpMethod.Get,
            $"api/v1/admin/users/{access.UserId}",
            body: null,
            cancellationToken).ConfigureAwait(false);
        if (user.Balance > 0)
        {
            return;
        }

        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new
        {
            balance = 1.0,
            operation = "add",
            notes = "共飞AI工作台本机零费率路由占位余额",
        }, JsonOptions);
        _ = await SendAsync<UserData>(
            access,
            HttpMethod.Post,
            $"api/v1/admin/users/{access.UserId}/balance",
            body,
            cancellationToken).ConfigureAwait(false);
    }

    private async Task<Sub2ApiSessionAccess> GetAdministratorAccessAsync(
        Uri localApiBaseUri,
        CancellationToken cancellationToken)
    {
        string? token = _localControlTokenProvider();
        if (!string.IsNullOrWhiteSpace(token))
        {
            try
            {
                return await _sessionManager
                    .LoginLocalControlAsync(localApiBaseUri, token, cancellationToken)
                    .ConfigureAwait(false);
            }
            catch (Sub2ApiSessionException exception)
            {
                throw new InvalidOperationException(DescribeSessionFailure(exception.Failure), exception);
            }
        }

        try
        {
            Sub2ApiSessionAccess access = await _sessionManager
                .GetAccessAsync(localApiBaseUri, cancellationToken)
                .ConfigureAwait(false);
            if (access.IsAdministrator)
            {
                return access;
            }
        }
        catch (Sub2ApiSessionException exception) when (
            exception.Failure is Sub2ApiSessionFailure.AuthorizationUnavailable or
                Sub2ApiSessionFailure.InvalidCredentials or
                Sub2ApiSessionFailure.Forbidden)
        {
            // Continue with the installation-owned local control token below.
        }
        catch (Sub2ApiSessionException exception)
        {
            throw new InvalidOperationException(DescribeSessionFailure(exception.Failure), exception);
        }

        throw new InvalidOperationException(DescribeSessionFailure(Sub2ApiSessionFailure.AuthorizationUnavailable));
    }

    private async Task<GroupData> CreateGroupAsync(
        Sub2ApiSessionAccess access,
        string name,
        string platform,
        CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new
        {
            name,
            description = "由共飞AI工作台管理的本机来源分组",
            platform,
            rate_multiplier = 0.0,
            allow_contribution_pool = false,
            is_exclusive = false,
            subscription_type = "standard",
            allow_messages_dispatch = platform == "openai",
        }, JsonOptions);
        return await SendAsync<GroupData>(
            access,
            HttpMethod.Post,
            "api/v1/admin/groups",
            body,
            cancellationToken).ConfigureAwait(false);
    }

    private async Task<GroupData> UpdateManagedGroupAsync(
        Sub2ApiSessionAccess access,
        GroupData group,
        CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new
        {
            rate_multiplier = 0.0,
            status = "active",
        }, JsonOptions);
        return await SendAsync<GroupData>(
            access,
            HttpMethod.Put,
            $"api/v1/admin/groups/{group.Id}",
            body,
            cancellationToken).ConfigureAwait(false);
    }

    private async Task<long> CreateExternalAccountAsync(
        Sub2ApiSessionAccess access,
        string name,
        string platform,
        ClientProfile upstream,
        long groupId,
        string profileId,
        int order,
        CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new
        {
            name,
            notes = "由共飞AI工作台管理的外部来源",
            platform,
            type = "apikey",
            credentials = new Dictionary<string, string>
            {
                ["api_key"] = upstream.Secret,
                ["base_url"] = NormalizeUpstreamBaseUrl(upstream.BaseUrl),
            },
            extra = new Dictionary<string, object>
            {
                ["workspace_external_source"] = true,
                ["workspace_source_id"] = profileId,
                ["workspace_fallback_order"] = order,
            },
            concurrency = 30,
            priority = 1000 + order,
            load_factor = 3,
            group_ids = new[] { groupId },
            confirm_mixed_channel_risk = true,
        }, JsonOptions);
        AdminAccountData created = await SendAsync<AdminAccountData>(
            access,
            HttpMethod.Post,
            "api/v1/admin/accounts",
            body,
            cancellationToken).ConfigureAwait(false);
        if (created.Id <= 0)
        {
            throw new InvalidOperationException($"{PlatformLabel(platform)} 外部来源创建后没有返回账号标识。");
        }
        return created.Id;
    }

    private async Task UpdateExternalAccountAsync(
        Sub2ApiSessionAccess access,
        long accountId,
        long groupId,
        int order,
        CancellationToken cancellationToken)
    {
        byte[] updateBody = JsonSerializer.SerializeToUtf8Bytes(new
        {
            group_ids = new[] { groupId },
            priority = 1000 + order,
            status = "active",
        }, JsonOptions);
        _ = await SendAsync<JsonElement>(
            access,
            HttpMethod.Put,
            $"api/v1/admin/accounts/{accountId}",
            updateBody,
            cancellationToken).ConfigureAwait(false);
    }

    private async Task RemoveSupersededExternalAccountsAsync(
        Sub2ApiSessionAccess access,
        IReadOnlyList<AdminAccountData> accounts,
        string platform,
        IReadOnlySet<long> activeAccountIds,
        CancellationToken cancellationToken)
    {
        foreach (AdminAccountData oldAccount in accounts.Where(item =>
                     !activeAccountIds.Contains(item.Id) &&
                     string.Equals(item.Platform, platform, StringComparison.OrdinalIgnoreCase) &&
                     item.Name?.StartsWith(ManagedPrefix + "-", StringComparison.OrdinalIgnoreCase) == true &&
                     item.Name.Contains("-上游-", StringComparison.OrdinalIgnoreCase)))
        {
            _ = await SendAsync<JsonElement>(
                access,
                HttpMethod.Delete,
                $"api/v1/admin/accounts/{oldAccount.Id}",
                body: null,
                cancellationToken).ConfigureAwait(false);
        }
    }

    private async Task UnbindPersonalAccountsAsync(
        Sub2ApiSessionAccess access,
        IReadOnlyList<AccountData> accounts,
        string platform,
        CancellationToken cancellationToken)
    {
        foreach (AccountData account in accounts.Where(item =>
                     item.Id > 0 && string.Equals(item.Platform, platform, StringComparison.OrdinalIgnoreCase)))
        {
            if ((account.GroupIds?.Length ?? 0) == 0 && account.Priority == 0)
            {
                continue;
            }
            byte[] body = JsonSerializer.SerializeToUtf8Bytes(new { group_ids = Array.Empty<long>(), priority = 0 }, JsonOptions);
            _ = await SendAsync<JsonElement>(
                access,
                HttpMethod.Put,
                $"api/v1/account-contributions/{account.Id}",
                body,
                cancellationToken).ConfigureAwait(false);
        }
    }

    private async Task<ApiKeyData> CreateClientKeyAsync(
        Sub2ApiSessionAccess access,
        string name,
        long groupId,
        CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new { name, group_id = groupId }, JsonOptions);
        return await SendAsync<ApiKeyData>(
            access,
            HttpMethod.Post,
            "api/v1/keys",
            body,
            cancellationToken).ConfigureAwait(false);
    }

    private async Task UpdateClientKeyAsync(
        Sub2ApiSessionAccess access,
        long keyId,
        string name,
        long groupId,
        CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new { name, group_id = groupId, status = "active" }, JsonOptions);
        _ = await SendAsync<ApiKeyData>(
            access,
            HttpMethod.Put,
            $"api/v1/keys/{keyId}",
            body,
            cancellationToken).ConfigureAwait(false);
    }

    private async Task<T> SendAsync<T>(
        Sub2ApiSessionAccess access,
        HttpMethod method,
        string relativePath,
        byte[]? body,
        CancellationToken cancellationToken)
    {
        try
        {
            using var request = new HttpRequestMessage(method, new Uri(access.ApiBaseUri, relativePath));
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", access.AccessToken);
            if (body is not null)
            {
                request.Content = new ByteArrayContent(body);
                request.Content.Headers.ContentType = new MediaTypeHeaderValue("application/json") { CharSet = "utf-8" };
            }

            using HttpResponseMessage response = await _httpClient
                .SendAsync(request, HttpCompletionOption.ResponseContentRead, cancellationToken)
                .ConfigureAwait(false);
            byte[] responseBytes = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
            try
            {
                ApiEnvelope<T>? envelope = null;
                try
                {
                    envelope = JsonSerializer.Deserialize<ApiEnvelope<T>>(responseBytes, JsonOptions);
                }
                catch (JsonException) when (!response.IsSuccessStatusCode)
                {
                }

                if (!response.IsSuccessStatusCode || envelope is null || envelope.Code != 0 || envelope.Data is null)
                {
                    string fallback = response.StatusCode switch
                    {
                        HttpStatusCode.Unauthorized => "本机登录已失效，请重新登录。",
                        HttpStatusCode.Forbidden => "当前本机账号没有修改中转调度的管理员权限。",
                        _ => $"本机后台请求失败（{(int)response.StatusCode}）。",
                    };
                    throw new InvalidOperationException(SafeMessage(envelope?.Message, fallback));
                }
                return envelope.Data;
            }
            finally
            {
                CryptographicOperations.ZeroMemory(responseBytes);
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (TaskCanceledException exception)
        {
            throw new InvalidOperationException("连接本机后台超时。", exception);
        }
        catch (HttpRequestException exception)
        {
            throw new InvalidOperationException("无法连接本机后台，请先确认本机服务可用。", exception);
        }
        finally
        {
            if (body is not null)
            {
                CryptographicOperations.ZeroMemory(body);
            }
        }
    }

    private static ProfileStore BuildClientStore(
        ProfileStore source,
        ProfileDefinition local,
        IReadOnlyDictionary<string, string> keys)
    {
        ProfileDefinition defaults = ProfileDefinition.CreateLocalDefaults();
        var effectiveLocal = new ProfileDefinition
        {
            Id = local.Id,
            Name = local.Name,
            Notes = local.Notes,
            DashboardUrl = local.DashboardUrl,
            Codex = WithSecret(local.Codex, defaults.Codex.BaseUrl, keys.GetValueOrDefault("openai")),
            Claude = WithSecret(local.Claude, defaults.Claude.BaseUrl, keys.GetValueOrDefault("anthropic")),
            Gemini = WithSecret(local.Gemini, defaults.Gemini.BaseUrl, keys.GetValueOrDefault("gemini")),
            Grok = WithSecret(local.Grok, defaults.Grok.BaseUrl, keys.GetValueOrDefault("grok")),
        };
        source.Local = effectiveLocal;
        source.SelectedLocalSourceId = ProfileSourceIds.LocalMachine;
        return source;
    }

    private static ClientProfile WithSecret(ClientProfile source, string fallbackBaseUrl, string? secret) => new()
    {
        BaseUrl = string.IsNullOrWhiteSpace(source.BaseUrl) ? fallbackBaseUrl : source.BaseUrl,
        Secret = string.IsNullOrWhiteSpace(secret) ? source.Secret : secret,
    };

    private static ProfileDefinition FindProfile(ProfileStore store, string profileId)
        => store.CloudSources.Concat(store.LocalSources).FirstOrDefault(profile =>
               string.Equals(profile.Id, profileId, StringComparison.OrdinalIgnoreCase))
           ?? throw new InvalidOperationException("所选来源已经不存在。");

    private static Uri RequireLocalApiBaseUri(ProfileDefinition local)
    {
        string candidate = !string.IsNullOrWhiteSpace(local.Codex.BaseUrl)
            ? local.Codex.BaseUrl
            : local.Claude.BaseUrl;
        if (!Uri.TryCreate(candidate, UriKind.Absolute, out Uri? uri) ||
            uri.Scheme is not ("http" or "https") ||
            !(uri.IsLoopback || string.Equals(uri.Host, "localhost", StringComparison.OrdinalIgnoreCase)))
        {
            throw new InvalidOperationException("请先在设置中选择正确的本机中转目录和本机地址。");
        }
        return new UriBuilder(uri) { Path = "/", Query = string.Empty, Fragment = string.Empty }.Uri;
    }

    private static bool IsFullyConfigured(ClientProfile profile)
        => !string.IsNullOrWhiteSpace(profile.BaseUrl) && !string.IsNullOrWhiteSpace(profile.Secret);

    private static IReadOnlyList<LocalSub2ApiRoutingTarget> BuildTargets(
        IReadOnlyList<ProfileDefinition> sources,
        Func<ProfileDefinition, ClientProfile> selectClient)
        => sources
            .Select((source, index) => new LocalSub2ApiRoutingTarget(source.Id, selectClient(source), index))
            .Where(target => IsFullyConfigured(target.Client))
            .ToArray();

    private static IReadOnlyList<string> ResolveEnabledSourceIds(ProfileStore store)
    {
        if (store.BackupUpstreamEnabled != true)
        {
            return [];
        }

        HashSet<string> externalSourceIds = store.CloudSources
            .Select(source => source.Id)
            .ToHashSet(StringComparer.OrdinalIgnoreCase);
        return (store.BackupSourceIds ?? [])
            .Where(id => !string.IsNullOrWhiteSpace(id))
            .Where(externalSourceIds.Contains)
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray();
    }

    private static string NormalizeUpstreamBaseUrl(string value)
    {
        string normalized = value.Trim().TrimEnd('/');
        return normalized.EndsWith("/v1", StringComparison.OrdinalIgnoreCase)
            ? normalized[..^3].TrimEnd('/')
            : normalized;
    }

    private static string StableMarker(string identity, string platform)
    {
        byte[] hash = SHA256.HashData(Encoding.UTF8.GetBytes($"{identity.Trim()}::{platform}"));
        return $"{platform}-{Convert.ToHexString(hash.AsSpan(0, 6)).ToLowerInvariant()}";
    }

    private static string CredentialMarker(ClientProfile profile)
    {
        byte[] hash = SHA256.HashData(Encoding.UTF8.GetBytes(
            $"{NormalizeUpstreamBaseUrl(profile.BaseUrl)}\n{profile.Secret}"));
        return Convert.ToHexString(hash.AsSpan(0, 6)).ToLowerInvariant();
    }

    private static string? GetWorkspaceSourceId(AdminAccountData account)
    {
        if (account.Extra is null ||
            !account.Extra.TryGetValue("workspace_external_source", out JsonElement managed) ||
            managed.ValueKind != JsonValueKind.True ||
            !account.Extra.TryGetValue("workspace_source_id", out JsonElement sourceId) ||
            sourceId.ValueKind != JsonValueKind.String)
        {
            return null;
        }
        return sourceId.GetString();
    }

    private static string PlatformLabel(string platform) => platform switch
    {
        "openai" => "Codex",
        "anthropic" => "Claude",
        "gemini" => "Gemini",
        "grok" => "Grok",
        _ => platform,
    };

    private static string DescribeSessionFailure(Sub2ApiSessionFailure failure) => failure switch
    {
        Sub2ApiSessionFailure.AuthorizationUnavailable => "请先在中转中心登录本机管理员账号。",
        Sub2ApiSessionFailure.GatewayUnavailable => "无法连接本机后台，请先确认本机服务可用。",
        _ => "本机登录已失效，请重新登录后再切换来源。",
    };

    private static string SafeMessage(string? value, string fallback)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return fallback;
        }
        string normalized = value.Trim();
        return normalized.Length <= 240 ? normalized : normalized[..240];
    }

    private sealed class ApiEnvelope<T>
    {
        [JsonPropertyName("code")] public int Code { get; set; }
        [JsonPropertyName("message")] public string? Message { get; set; }
        [JsonPropertyName("data")] public T? Data { get; set; }
    }

    private sealed class PagedData<T>
    {
        [JsonPropertyName("items")] public T[]? Items { get; set; }
    }

    private sealed class UserData
    {
        [JsonPropertyName("id")] public long Id { get; set; }
        [JsonPropertyName("balance")] public decimal Balance { get; set; }
    }

    private sealed class GroupData
    {
        [JsonPropertyName("id")] public long Id { get; set; }
        [JsonPropertyName("name")] public string? Name { get; set; }
        [JsonPropertyName("platform")] public string? Platform { get; set; }
        [JsonPropertyName("rate_multiplier")] public double RateMultiplier { get; set; }
    }

    private sealed class AccountListData
    {
        [JsonPropertyName("items")] public AccountData[]? Items { get; set; }
    }

    private sealed class AccountData
    {
        [JsonPropertyName("id")] public long Id { get; set; }
        [JsonPropertyName("name")] public string? Name { get; set; }
        [JsonPropertyName("platform")] public string? Platform { get; set; }
        [JsonPropertyName("priority")] public int Priority { get; set; }
        [JsonPropertyName("group_ids")] public long[]? GroupIds { get; set; }
    }

    private sealed class AdminAccountData
    {
        [JsonPropertyName("id")] public long Id { get; set; }
        [JsonPropertyName("name")] public string? Name { get; set; }
        [JsonPropertyName("platform")] public string? Platform { get; set; }
        [JsonPropertyName("last_used_at")] public DateTimeOffset? LastUsedAt { get; set; }
        [JsonPropertyName("extra")] public Dictionary<string, JsonElement>? Extra { get; set; }
    }

    private sealed class ApiKeyData
    {
        [JsonPropertyName("id")] public long Id { get; set; }
        [JsonPropertyName("key")] public string? Key { get; set; }
        [JsonPropertyName("name")] public string? Name { get; set; }
    }

}
