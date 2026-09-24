using System.Net;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace LanAi.Workspace.Wpf.Services;

internal interface ISub2ApiAccountCenterClient
{
    Task<AccountCenterPage> ListAsync(Sub2ApiSessionAccess access, int page, int pageSize, CancellationToken cancellationToken);
    Task<AccountCenterUsageSummary?> GetUsageAsync(Sub2ApiSessionAccess access, long accountId, bool force, CancellationToken cancellationToken);
    Task<AccountCenterEditOptions> GetEditOptionsAsync(Sub2ApiSessionAccess access, CancellationToken cancellationToken);
    Task<AccountCenterProxy> CreateProxyAsync(Sub2ApiSessionAccess access, AccountCenterProxyCreateRequest request, CancellationToken cancellationToken);
    Task<AccountCenterCreateResult> CreateAsync(Sub2ApiSessionAccess access, AccountCenterCreateRequest request, CancellationToken cancellationToken);
    Task<AccountCenterOpenAiAuthSession> GenerateOpenAiAuthAsync(Sub2ApiSessionAccess access, long proxyId, CancellationToken cancellationToken);
    Task<AccountCenterCreateResult> CreateOpenAiFromCodeAsync(Sub2ApiSessionAccess access, AccountCenterOpenAiCreateRequest request, string sessionId, string code, string state, CancellationToken cancellationToken);
    Task<AccountCenterCreateResult> CreateOpenAiFromRefreshTokenAsync(Sub2ApiSessionAccess access, AccountCenterOpenAiCreateRequest request, string refreshToken, bool mobile, CancellationToken cancellationToken);
    Task<AccountCenterCreateResult> CreateOpenAiFromCodexPatAsync(Sub2ApiSessionAccess access, AccountCenterOpenAiCreateRequest request, string accessToken, CancellationToken cancellationToken);
    Task UpdateAsync(Sub2ApiSessionAccess access, long accountId, AccountCenterUpdateRequest update, CancellationToken cancellationToken);
    Task<AccountCenterTestResult> TestAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken);
    Task<IReadOnlyList<AccountCenterTestModel>> GetAvailableModelsAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken);
    Task<AccountCenterDetailedTestResult> RunDetailedTestAsync(
        Sub2ApiSessionAccess access,
        long accountId,
        AccountCenterDetailedTestRequest request,
        IProgress<AccountCenterTestEvent>? progress,
        CancellationToken cancellationToken);
    Task RunAdminActionAsync(Sub2ApiSessionAccess access, long accountId, AccountCenterAdminAction action, CancellationToken cancellationToken);
    Task DeleteAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken);
}

internal sealed record AccountCenterPage(
    IReadOnlyList<AccountCenterAccount> Items,
    int Total,
    int Page,
    int Limit);

internal sealed record AccountCenterAccount(
    long Id,
    string Name,
    string Platform,
    string Type,
    int Concurrency,
    int LoadFactor,
    int Priority,
    string Status,
    string ErrorMessage,
    DateTimeOffset? LastUsedAt,
    DateTimeOffset CreatedAt,
    bool Schedulable,
    DateTimeOffset? RateLimitResetAt,
    DateTimeOffset? TempUnschedulableUntil,
    long? ProxyId,
    string? ProxyName,
    IReadOnlyList<long> GroupIds,
    IReadOnlyList<string> GroupNames,
    string TempUnschedulableReason = "",
    DateTimeOffset? RateLimitedAt = null,
    DateTimeOffset? OverloadUntil = null);

internal sealed record AccountCenterUsageSummary(
    AccountCenterUsageWindow? FiveHour,
    AccountCenterUsageWindow? SevenDay,
    bool IsLocalRollup,
    long ThirtyDayRequests,
    long ThirtyDayTokens,
    double ThirtyDayCost);

internal sealed record AccountCenterUsageWindow(double Utilization, DateTimeOffset? ResetsAt, long Requests, long Tokens, double Cost);
internal sealed record AccountCenterGroup(long Id, string Name, string Platform, double RateMultiplier);
internal sealed record AccountCenterProxy(long Id, string Name, string Protocol, string Host, int Port, string Status);
internal sealed record AccountCenterEditOptions(IReadOnlyList<AccountCenterGroup> PrivateGroups, IReadOnlyList<AccountCenterProxy> Proxies);
internal sealed record AccountCenterProxyCreateRequest(string Name, string Protocol, string Host, int Port, string Username, string Password);
internal sealed record AccountCenterCreateRequest(
    string Mode,
    string Name,
    string Platform,
    string ApiKey,
    string BaseUrl,
    IReadOnlyList<string> Contents,
    int Concurrency,
    int LoadFactor,
    int Priority,
    IReadOnlyList<long> GroupIds,
    long ProxyId,
    string TestModelId);
internal sealed record AccountCenterOpenAiCreateRequest(
    string Name,
    int Concurrency,
    int LoadFactor,
    int Priority,
    IReadOnlyList<long> GroupIds,
    long ProxyId,
    string TestModelId);
internal sealed record AccountCenterCreateResult(int Total, int Created, int Failed, IReadOnlyList<AccountCenterCreateResultItem> Items, int Skipped = 0);
internal sealed record AccountCenterCreateResultItem(int Index, string Name, long? AccountId, string Status, string Message);
internal sealed record AccountCenterOpenAiAuthSession(string AuthUrl, string SessionId);
internal sealed record AccountCenterUpdateRequest(string Name, int Concurrency, int LoadFactor, IReadOnlyList<long> GroupIds, long ProxyId, int? Priority = null, string? Status = null);
internal sealed record AccountCenterTestResult(string Status, string? ErrorMessage, int? LatencyMilliseconds);
public sealed record AccountCenterTestModel(string Id, string DisplayName);
internal sealed record AccountCenterDetailedTestRequest(string ModelId, string Prompt, string Mode);
internal sealed record AccountCenterDetailedTestResult(bool Success, string? ErrorMessage);
internal sealed record AccountCenterTestEvent(
    string Type,
    string Text,
    string Model,
    string Error,
    bool Success,
    string ImageUrl,
    string MimeType);

public enum AccountCenterAdminAction
{
    RefreshCredentials,
    RecoverState,
    ClearError,
    SetPrivacy,
    ResetQuota,
    SyncUpstreamModels,
}

internal enum AccountCenterClientFailure
{
    Unauthorized,
    Forbidden,
    NotFound,
    InvalidRequest,
    RequestTimedOut,
    GatewayUnavailable,
    ProtocolMismatch,
}

internal sealed class AccountCenterClientException : Exception
{
    internal AccountCenterClientException(AccountCenterClientFailure failure, string? serverMessage = null)
        : base(serverMessage ?? failure.ToString())
    {
        Failure = failure;
        ServerMessage = serverMessage;
    }

    internal AccountCenterClientFailure Failure { get; }
    internal string? ServerMessage { get; }
}

/// <summary>
/// Calls the local account-management endpoints. DTOs deliberately omit credentials,
/// contribution accounting, sharing metadata, and arbitrary fields from the desktop UI.
/// </summary>
internal sealed class Sub2ApiAccountCenterClient : ISub2ApiAccountCenterClient, IDisposable
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
        NumberHandling = JsonNumberHandling.AllowReadingFromString,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    };

    private readonly HttpClient _httpClient;
    private readonly bool _ownsHttpClient;

    internal Sub2ApiAccountCenterClient()
        : this(new HttpClient(new HttpClientHandler { UseProxy = false, AllowAutoRedirect = false })
        {
            Timeout = TimeSpan.FromSeconds(60),
        }, ownsHttpClient: true)
    {
    }

    internal Sub2ApiAccountCenterClient(HttpClient httpClient, bool ownsHttpClient = false)
    {
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        _ownsHttpClient = ownsHttpClient;
    }

    public async Task<AccountCenterPage> ListAsync(Sub2ApiSessionAccess access, int page, int pageSize, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(access);
        page = Math.Max(page, 1);
        pageSize = Math.Clamp(pageSize, 1, 100);
        AccountListData data = await SendAsync<AccountListData>(
            access, HttpMethod.Get, $"api/v1/account-contributions?page={page}&limit={pageSize}", null, cancellationToken).ConfigureAwait(false);
        IReadOnlyList<AccountCenterAccount> accounts = (data.Items ?? [])
            .Where(item => item.Id > 0)
            .Select(MapAccount)
            .ToArray();
        return new AccountCenterPage(
            accounts,
            Math.Max(data.Total, accounts.Count),
            Math.Max(data.Page, 1),
            data.Limit > 0 ? data.Limit : pageSize);
    }

    public async Task<AccountCenterUsageSummary?> GetUsageAsync(Sub2ApiSessionAccess access, long accountId, bool force, CancellationToken cancellationToken)
    {
        if (accountId <= 0) throw new ArgumentOutOfRangeException(nameof(accountId));
        UsageSummaryData data = await SendAsync<UsageSummaryData>(
            access, HttpMethod.Get,
            $"api/v1/account-contributions/{accountId}/usage-summary" + (force ? "?force=true" : string.Empty),
            null, cancellationToken).ConfigureAwait(false);
        return new AccountCenterUsageSummary(
            MapWindow(data.Upstream?.FiveHour),
            MapWindow(data.Upstream?.SevenDay),
            string.Equals(data.Upstream?.Source, "local", StringComparison.OrdinalIgnoreCase),
            data.Stats?.Summary?.TotalRequests ?? 0,
            data.Stats?.Summary?.TotalTokens ?? 0,
            data.Stats?.Summary?.TotalCost ?? 0d);
    }

    public async Task<AccountCenterEditOptions> GetEditOptionsAsync(Sub2ApiSessionAccess access, CancellationToken cancellationToken)
    {
        ProxyData[] proxies = await SendAsync<ProxyData[]>(
            access,
            HttpMethod.Get,
            "api/v1/account-contributions/proxies",
            null,
            cancellationToken).ConfigureAwait(false);
        return new AccountCenterEditOptions(
            [],
            proxies
                .Where(proxy => proxy.Id > 0)
                .Select(proxy => new AccountCenterProxy(proxy.Id, NormalizeLabel(proxy.Name, $"代理 {proxy.Id}"), NormalizeLabel(proxy.Protocol, "http"), proxy.Host ?? string.Empty, proxy.Port, proxy.Status ?? string.Empty))
                .ToArray());
    }

    public async Task<AccountCenterProxy> CreateProxyAsync(
        Sub2ApiSessionAccess access,
        AccountCenterProxyCreateRequest request,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(request);
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new
        {
            name = request.Name.Trim(),
            protocol = request.Protocol.Trim().ToLowerInvariant(),
            host = request.Host.Trim(),
            port = request.Port,
            username = EmptyToNull(request.Username),
            password = EmptyToNull(request.Password),
        }, JsonOptions);
        ProxyData proxy = await SendAsync<ProxyData>(
            access,
            HttpMethod.Post,
            "api/v1/account-contributions/proxies",
            body,
            cancellationToken).ConfigureAwait(false);
        return new AccountCenterProxy(
            proxy.Id,
            NormalizeLabel(proxy.Name, $"代理 {proxy.Id}"),
            NormalizeLabel(proxy.Protocol, "http"),
            proxy.Host ?? string.Empty,
            proxy.Port,
            proxy.Status ?? string.Empty);
    }

    public async Task<AccountCenterCreateResult> CreateAsync(
        Sub2ApiSessionAccess access,
        AccountCenterCreateRequest request,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(request);
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new
        {
            mode = request.Mode,
            name = EmptyToNull(request.Name),
            platform = EmptyToNull(request.Platform),
            api_key = EmptyToNull(request.ApiKey),
            base_url = EmptyToNull(request.BaseUrl),
            contents = request.Contents.Count == 0 ? null : request.Contents,
            concurrency = request.Concurrency,
            load_factor = request.LoadFactor,
            priority = request.Priority,
            group_ids = request.GroupIds,
            proxy_id = request.ProxyId > 0 ? (long?)request.ProxyId : null,
            test_model_id = EmptyToNull(request.TestModelId),
        }, JsonOptions);
        CreateResultData data = await SendAsync<CreateResultData>(
            access, HttpMethod.Post, "api/v1/account-contributions", body, cancellationToken).ConfigureAwait(false);
        return MapCreateResult(data);
    }

    public async Task<AccountCenterOpenAiAuthSession> GenerateOpenAiAuthAsync(
        Sub2ApiSessionAccess access,
        long proxyId,
        CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new
        {
            proxy_id = proxyId > 0 ? (long?)proxyId : null,
        }, JsonOptions);
        OpenAiAuthSessionData data = await SendAsync<OpenAiAuthSessionData>(
            access, HttpMethod.Post, "api/v1/account-contributions/openai/generate-auth-url", body, cancellationToken).ConfigureAwait(false);
        if (!TryNormalizeExternalAuthorizationUri(data.AuthUrl, out string? authUrl) ||
            string.IsNullOrWhiteSpace(data.SessionId))
        {
            throw new AccountCenterClientException(AccountCenterClientFailure.ProtocolMismatch);
        }
        return new AccountCenterOpenAiAuthSession(authUrl!, data.SessionId);
    }

    private static bool TryNormalizeExternalAuthorizationUri(string? value, out string? normalized)
    {
        normalized = null;
        if (!Uri.TryCreate(value?.Trim(), UriKind.Absolute, out Uri? uri) ||
            !string.Equals(uri.Scheme, Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase) ||
            string.IsNullOrWhiteSpace(uri.Host) ||
            !string.IsNullOrWhiteSpace(uri.UserInfo))
        {
            return false;
        }

        normalized = uri.AbsoluteUri;
        return true;
    }

    public Task<AccountCenterCreateResult> CreateOpenAiFromCodeAsync(
        Sub2ApiSessionAccess access,
        AccountCenterOpenAiCreateRequest request,
        string sessionId,
        string code,
        string state,
        CancellationToken cancellationToken)
        => CreateOpenAiAsync(access, request, "create-from-code", new
        {
            session_id = sessionId,
            code,
            state,
        }, cancellationToken);

    public Task<AccountCenterCreateResult> CreateOpenAiFromRefreshTokenAsync(
        Sub2ApiSessionAccess access,
        AccountCenterOpenAiCreateRequest request,
        string refreshToken,
        bool mobile,
        CancellationToken cancellationToken)
        => CreateOpenAiAsync(
            access,
            request,
            mobile ? "create-from-mobile-refresh-token" : "create-from-refresh-token",
            new { refresh_token = refreshToken },
            cancellationToken);

    public Task<AccountCenterCreateResult> CreateOpenAiFromCodexPatAsync(
        Sub2ApiSessionAccess access,
        AccountCenterOpenAiCreateRequest request,
        string accessToken,
        CancellationToken cancellationToken)
        => CreateOpenAiAsync(access, request, "create-from-codex-pat", new { access_token = accessToken }, cancellationToken);

    private async Task<AccountCenterCreateResult> CreateOpenAiAsync(
        Sub2ApiSessionAccess access,
        AccountCenterOpenAiCreateRequest request,
        string endpoint,
        object credential,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(request);
        using JsonDocument credentialDocument = JsonDocument.Parse(JsonSerializer.Serialize(credential, JsonOptions));
        var payload = new Dictionary<string, object?>
        {
            ["name"] = EmptyToNull(request.Name),
            ["concurrency"] = request.Concurrency,
            ["load_factor"] = request.LoadFactor,
            ["priority"] = request.Priority,
            ["group_ids"] = request.GroupIds,
            ["proxy_id"] = request.ProxyId > 0 ? request.ProxyId : null,
            ["test_model_id"] = EmptyToNull(request.TestModelId),
        };
        foreach (JsonProperty property in credentialDocument.RootElement.EnumerateObject())
        {
            payload[property.Name] = property.Value.GetString();
        }
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(payload, JsonOptions);
        CreateResultData data = await SendAsync<CreateResultData>(
            access, HttpMethod.Post, $"api/v1/account-contributions/openai/{endpoint}", body, cancellationToken).ConfigureAwait(false);
        return MapCreateResult(data);
    }

    public async Task UpdateAsync(Sub2ApiSessionAccess access, long accountId, AccountCenterUpdateRequest update, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(update);
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new
        {
            name = update.Name,
            concurrency = update.Concurrency,
            load_factor = update.LoadFactor,
            group_ids = update.GroupIds,
            proxy_id = update.ProxyId,
            priority = update.Priority,
            status = update.Status,
        }, JsonOptions);
        _ = await SendAsync<JsonElement>(access, HttpMethod.Put, $"api/v1/account-contributions/{accountId}", body, cancellationToken).ConfigureAwait(false);
    }

    public async Task<AccountCenterTestResult> TestAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new { model_id = string.Empty }, JsonOptions);
        TestData data = await SendAsync<TestData>(access, HttpMethod.Post, $"api/v1/account-contributions/{accountId}/test", body, cancellationToken).ConfigureAwait(false);
        return new AccountCenterTestResult(data.Status ?? "failed", data.ErrorMessage, data.LatencyMilliseconds);
    }

    public async Task<IReadOnlyList<AccountCenterTestModel>> GetAvailableModelsAsync(
        Sub2ApiSessionAccess access,
        long accountId,
        CancellationToken cancellationToken)
    {
        ModelData[] models = await SendAsync<ModelData[]>(
            access,
            HttpMethod.Get,
            $"api/v1/account-contributions/{accountId}/models",
            null,
            cancellationToken).ConfigureAwait(false);
        return models
            .Where(model => !string.IsNullOrWhiteSpace(model.Id))
            .Select(model => new AccountCenterTestModel(
                model.Id!.Trim(),
                NormalizeLabel(model.DisplayName, model.Id!)))
            .DistinctBy(model => model.Id, StringComparer.OrdinalIgnoreCase)
            .ToArray();
    }

    public async Task<AccountCenterDetailedTestResult> RunDetailedTestAsync(
        Sub2ApiSessionAccess access,
        long accountId,
        AccountCenterDetailedTestRequest request,
        IProgress<AccountCenterTestEvent>? progress,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(access);
        ArgumentNullException.ThrowIfNull(request);
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new
        {
            model_id = request.ModelId,
            prompt = EmptyToNull(request.Prompt),
            mode = EmptyToNull(request.Mode),
        }, JsonOptions);
        try
        {
            using var message = new HttpRequestMessage(
                HttpMethod.Post,
                new Uri(access.ApiBaseUri, $"api/v1/account-contributions/{accountId}/test-stream"));
            message.Headers.Authorization = new AuthenticationHeaderValue("Bearer", access.AccessToken);
            message.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("text/event-stream"));
            message.Headers.TryAddWithoutValidation("X-Admin-UI-Request", "1");
            message.Content = new ByteArrayContent(body);
            message.Content.Headers.ContentType = new MediaTypeHeaderValue("application/json") { CharSet = "utf-8" };

            using HttpResponseMessage response = await _httpClient
                .SendAsync(message, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                .ConfigureAwait(false);
            if (!response.IsSuccessStatusCode)
            {
                byte[] errorBytes = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
                try
                {
                    ApiEnvelope<JsonElement>? envelope = null;
                    try
                    {
                        envelope = JsonSerializer.Deserialize<ApiEnvelope<JsonElement>>(errorBytes, JsonOptions);
                    }
                    catch (JsonException)
                    {
                    }
                    string? raw = errorBytes.Length == 0 ? null : Encoding.UTF8.GetString(errorBytes);
                    throw new AccountCenterClientException(
                        MapFailure(response.StatusCode),
                        NormalizeServerMessage(envelope?.Message ?? raw));
                }
                finally
                {
                    CryptographicOperations.ZeroMemory(errorBytes);
                }
            }

            await using Stream stream = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
            using var reader = new StreamReader(stream, Encoding.UTF8, true, 4096, leaveOpen: false);
            bool completed = false;
            string? error = null;
            while (!reader.EndOfStream)
            {
                cancellationToken.ThrowIfCancellationRequested();
                string? line = await reader.ReadLineAsync(cancellationToken).ConfigureAwait(false);
                if (string.IsNullOrWhiteSpace(line) || !line.StartsWith("data:", StringComparison.OrdinalIgnoreCase)) continue;
                string payload = line[5..].TrimStart();
                if (payload.Length == 0 || string.Equals(payload, "[DONE]", StringComparison.OrdinalIgnoreCase)) continue;

                TestEventData? data;
                try
                {
                    data = JsonSerializer.Deserialize<TestEventData>(payload, JsonOptions);
                }
                catch (JsonException)
                {
                    progress?.Report(new AccountCenterTestEvent("status", payload, string.Empty, string.Empty, false, string.Empty, string.Empty));
                    continue;
                }
                if (data is null) continue;

                var item = new AccountCenterTestEvent(
                    data.Type ?? "status",
                    data.Text ?? string.Empty,
                    data.Model ?? string.Empty,
                    data.Error ?? string.Empty,
                    data.Success,
                    data.ImageUrl ?? string.Empty,
                    data.MimeType ?? string.Empty);
                progress?.Report(item);
                if (string.Equals(item.Type, "error", StringComparison.OrdinalIgnoreCase)) error = item.Error;
                if (string.Equals(item.Type, "test_complete", StringComparison.OrdinalIgnoreCase)) completed = item.Success;
            }

            return completed
                ? new AccountCenterDetailedTestResult(true, null)
                : new AccountCenterDetailedTestResult(false, string.IsNullOrWhiteSpace(error) ? "测试连接提前结束，后台未返回完成状态。" : error);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (AccountCenterClientException)
        {
            throw;
        }
        catch (TaskCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            throw new AccountCenterClientException(
                AccountCenterClientFailure.RequestTimedOut,
                $"等待本机后台测试响应超过 {_httpClient.Timeout.TotalSeconds:0} 秒。");
        }
        catch (HttpRequestException exception)
        {
            throw new AccountCenterClientException(AccountCenterClientFailure.GatewayUnavailable, exception.Message);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(body);
        }
    }

    public async Task RunAdminActionAsync(
        Sub2ApiSessionAccess access,
        long accountId,
        AccountCenterAdminAction action,
        CancellationToken cancellationToken)
    {
        string path = action switch
        {
            AccountCenterAdminAction.RefreshCredentials => "refresh",
            AccountCenterAdminAction.RecoverState => "recover-state",
            AccountCenterAdminAction.ClearError => "clear-error",
            AccountCenterAdminAction.SetPrivacy => "set-privacy",
            AccountCenterAdminAction.ResetQuota => "reset-quota",
            AccountCenterAdminAction.SyncUpstreamModels => "models/sync-upstream",
            _ => throw new ArgumentOutOfRangeException(nameof(action), action, null),
        };
        _ = await SendAsync<JsonElement>(
            access,
            HttpMethod.Post,
            $"api/v1/admin/accounts/{accountId}/{path}",
            JsonSerializer.SerializeToUtf8Bytes(new { }, JsonOptions),
            cancellationToken).ConfigureAwait(false);
    }

    public async Task DeleteAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken)
        => _ = await SendAsync<JsonElement>(access, HttpMethod.Delete, $"api/v1/account-contributions/{accountId}", null, cancellationToken).ConfigureAwait(false);

    public void Dispose()
    {
        if (_ownsHttpClient) _httpClient.Dispose();
    }

    private async Task<T> SendAsync<T>(Sub2ApiSessionAccess access, HttpMethod method, string relativePath, byte[]? body, CancellationToken cancellationToken)
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

            using HttpResponseMessage response = await _httpClient.SendAsync(request, HttpCompletionOption.ResponseContentRead, cancellationToken).ConfigureAwait(false);
            byte[] bytes = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
            try
            {
                ApiEnvelope<T>? envelope = null;
                try
                {
                    envelope = JsonSerializer.Deserialize<ApiEnvelope<T>>(bytes, JsonOptions);
                }
                catch (JsonException) when (!response.IsSuccessStatusCode)
                {
                }

                if (!response.IsSuccessStatusCode)
                {
                    throw new AccountCenterClientException(MapFailure(response.StatusCode), NormalizeServerMessage(envelope?.Message));
                }
                if (envelope is null || envelope.Code != 0 || envelope.Data is null)
                {
                    throw new AccountCenterClientException(AccountCenterClientFailure.ProtocolMismatch, NormalizeServerMessage(envelope?.Message));
                }
                return envelope.Data;
            }
            finally
            {
                CryptographicOperations.ZeroMemory(bytes);
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (AccountCenterClientException)
        {
            throw;
        }
        catch (TaskCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            string timeout = _httpClient.Timeout.TotalSeconds >= 1
                ? $"{_httpClient.Timeout.TotalSeconds:0} 秒"
                : $"{_httpClient.Timeout.TotalMilliseconds:0} 毫秒";
            throw new AccountCenterClientException(
                AccountCenterClientFailure.RequestTimedOut,
                $"等待本机后台响应超过 {timeout}。");
        }
        catch (HttpRequestException exception)
        {
            throw new AccountCenterClientException(
                AccountCenterClientFailure.GatewayUnavailable,
                exception.Message);
        }
        finally
        {
            if (body is not null) CryptographicOperations.ZeroMemory(body);
        }
    }

    private static AccountCenterAccount MapAccount(AccountData source)
    {
        return new AccountCenterAccount(
            source.Id,
            NormalizeLabel(source.Name, $"账号 {source.Id}"),
            NormalizeLabel(source.Platform, "未知平台"),
            NormalizeLabel(source.Type, "未知类型"),
            Math.Max(source.Concurrency, 0),
            source.LoadFactor is > 0 ? source.LoadFactor.Value : Math.Max(source.Concurrency, 0),
            source.Priority,
            source.Status ?? string.Empty,
            source.ErrorMessage ?? string.Empty,
            source.LastUsedAt,
            source.CreatedAt,
            source.Schedulable,
            source.RateLimitResetAt,
            source.TempUnschedulableUntil,
            source.ProxyId,
            source.Proxy?.Name,
            source.GroupIds ?? [],
            (source.Groups ?? []).Select(group => group.Name).Where(name => !string.IsNullOrWhiteSpace(name)).ToArray(),
            source.TempUnschedulableReason ?? string.Empty,
            source.RateLimitedAt,
            source.OverloadUntil);
    }

    private static AccountCenterUsageWindow? MapWindow(UsageProgressData? source)
        => source is null
            ? null
            : new AccountCenterUsageWindow(
                Math.Max(source.Utilization, 0d),
                source.ResetsAt,
                source.WindowStats?.Requests ?? 0,
                source.WindowStats?.Tokens ?? 0,
                source.WindowStats?.Cost ?? 0d);

    private static IReadOnlyList<AccountCenterGroup> MapGroups(IEnumerable<GroupData> groups)
        => groups.Where(group => group.Id > 0)
            .Select(group => new AccountCenterGroup(
                group.Id,
                NormalizeLabel(group.Name, $"分组 {group.Id}"),
                NormalizeLabel(group.Platform, "通用"),
                group.RateMultiplier))
            .ToArray();

    private static AccountCenterCreateResult MapCreateResult(CreateResultData data)
    {
        IReadOnlyList<AccountCenterCreateResultItem> items = (data.Items ?? [])
            .Select(item => new AccountCenterCreateResultItem(
                item.Index,
                item.Name ?? string.Empty,
                item.AccountId,
                item.Status ?? string.Empty,
                item.Message ?? string.Empty))
            .ToArray();
        return new AccountCenterCreateResult(data.Total, data.Created, data.Failed, items, data.Skipped);
    }

    private static string NormalizeLabel(string? value, string fallback)
        => string.IsNullOrWhiteSpace(value) ? fallback : value.Trim();

    private static string? EmptyToNull(string? value)
        => string.IsNullOrWhiteSpace(value) ? null : value.Trim();

    private static string? NormalizeServerMessage(string? value)
    {
        if (string.IsNullOrWhiteSpace(value)) return null;
        string normalized = value.Trim();
        return normalized.Length <= 300 ? normalized : normalized[..300];
    }

    private static AccountCenterClientFailure MapFailure(HttpStatusCode statusCode) => statusCode switch
    {
        HttpStatusCode.Unauthorized => AccountCenterClientFailure.Unauthorized,
        HttpStatusCode.Forbidden => AccountCenterClientFailure.Forbidden,
        HttpStatusCode.NotFound => AccountCenterClientFailure.NotFound,
        HttpStatusCode.BadRequest => AccountCenterClientFailure.InvalidRequest,
        _ => AccountCenterClientFailure.GatewayUnavailable,
    };

    private sealed class ApiEnvelope<T>
    {
        [JsonPropertyName("code")] public int Code { get; set; }
        [JsonPropertyName("message")] public string? Message { get; set; }
        [JsonPropertyName("data")] public T? Data { get; set; }
    }

    private sealed class AccountListData
    {
        [JsonPropertyName("items")] public AccountData[]? Items { get; set; }
        [JsonPropertyName("total")] public int Total { get; set; }
        [JsonPropertyName("page")] public int Page { get; set; }
        [JsonPropertyName("limit")] public int Limit { get; set; }
    }

    private sealed class AccountData
    {
        [JsonPropertyName("id")] public long Id { get; set; }
        [JsonPropertyName("name")] public string? Name { get; set; }
        [JsonPropertyName("platform")] public string? Platform { get; set; }
        [JsonPropertyName("type")] public string? Type { get; set; }
        [JsonPropertyName("proxy_id")] public long? ProxyId { get; set; }
        [JsonPropertyName("proxy")] public ProxyData? Proxy { get; set; }
        [JsonPropertyName("concurrency")] public int Concurrency { get; set; }
        [JsonPropertyName("load_factor")] public int? LoadFactor { get; set; }
        [JsonPropertyName("priority")] public int Priority { get; set; }
        [JsonPropertyName("status")] public string? Status { get; set; }
        [JsonPropertyName("error_message")] public string? ErrorMessage { get; set; }
        [JsonPropertyName("last_used_at")] public DateTimeOffset? LastUsedAt { get; set; }
        [JsonPropertyName("created_at")] public DateTimeOffset CreatedAt { get; set; }
        [JsonPropertyName("schedulable")] public bool Schedulable { get; set; }
        [JsonPropertyName("rate_limit_reset_at")] public DateTimeOffset? RateLimitResetAt { get; set; }
        [JsonPropertyName("rate_limited_at")] public DateTimeOffset? RateLimitedAt { get; set; }
        [JsonPropertyName("overload_until")] public DateTimeOffset? OverloadUntil { get; set; }
        [JsonPropertyName("temp_unschedulable_until")] public DateTimeOffset? TempUnschedulableUntil { get; set; }
        [JsonPropertyName("temp_unschedulable_reason")] public string? TempUnschedulableReason { get; set; }
        [JsonPropertyName("group_ids")] public long[]? GroupIds { get; set; }
        [JsonPropertyName("groups")] public GroupData[]? Groups { get; set; }
    }

    private sealed class GroupData
    {
        [JsonPropertyName("id")] public long Id { get; set; }
        [JsonPropertyName("name")] public string Name { get; set; } = string.Empty;
        [JsonPropertyName("platform")] public string? Platform { get; set; }
        [JsonPropertyName("rate_multiplier")] public double RateMultiplier { get; set; }
    }

    private sealed class ProxyData
    {
        [JsonPropertyName("id")] public long Id { get; set; }
        [JsonPropertyName("name")] public string? Name { get; set; }
        [JsonPropertyName("protocol")] public string? Protocol { get; set; }
        [JsonPropertyName("host")] public string? Host { get; set; }
        [JsonPropertyName("port")] public int Port { get; set; }
        [JsonPropertyName("status")] public string? Status { get; set; }
    }

    private sealed class UsageSummaryData
    {
        [JsonPropertyName("upstream")] public UsageInfoData? Upstream { get; set; }
        [JsonPropertyName("stats")] public UsageStatsData? Stats { get; set; }
    }

    private sealed class UsageInfoData
    {
        [JsonPropertyName("source")] public string? Source { get; set; }
        [JsonPropertyName("five_hour")] public UsageProgressData? FiveHour { get; set; }
        [JsonPropertyName("seven_day")] public UsageProgressData? SevenDay { get; set; }
    }

    private sealed class UsageProgressData
    {
        [JsonPropertyName("utilization")] public double Utilization { get; set; }
        [JsonPropertyName("resets_at")] public DateTimeOffset? ResetsAt { get; set; }
        [JsonPropertyName("window_stats")] public WindowStatsData? WindowStats { get; set; }
    }

    private sealed class WindowStatsData
    {
        [JsonPropertyName("requests")] public long Requests { get; set; }
        [JsonPropertyName("tokens")] public long Tokens { get; set; }
        [JsonPropertyName("cost")] public double Cost { get; set; }
    }

    private sealed class UsageStatsData
    {
        [JsonPropertyName("summary")] public UsageRollupData? Summary { get; set; }
    }

    private sealed class UsageRollupData
    {
        [JsonPropertyName("total_requests")] public long TotalRequests { get; set; }
        [JsonPropertyName("total_tokens")] public long TotalTokens { get; set; }
        [JsonPropertyName("total_cost")] public double TotalCost { get; set; }
    }

    private sealed class TestData
    {
        [JsonPropertyName("status")] public string? Status { get; set; }
        [JsonPropertyName("error_message")] public string? ErrorMessage { get; set; }
        [JsonPropertyName("latency_ms")] public int? LatencyMilliseconds { get; set; }
    }

    private sealed class ModelData
    {
        [JsonPropertyName("id")] public string? Id { get; set; }
        [JsonPropertyName("display_name")] public string? DisplayName { get; set; }
    }

    private sealed class TestEventData
    {
        [JsonPropertyName("type")] public string? Type { get; set; }
        [JsonPropertyName("text")] public string? Text { get; set; }
        [JsonPropertyName("model")] public string? Model { get; set; }
        [JsonPropertyName("error")] public string? Error { get; set; }
        [JsonPropertyName("success")] public bool Success { get; set; }
        [JsonPropertyName("image_url")] public string? ImageUrl { get; set; }
        [JsonPropertyName("mime_type")] public string? MimeType { get; set; }
    }

    private sealed class OpenAiAuthSessionData
    {
        [JsonPropertyName("auth_url")] public string? AuthUrl { get; set; }
        [JsonPropertyName("session_id")] public string? SessionId { get; set; }
    }

    private sealed class CreateResultData
    {
        [JsonPropertyName("total")] public int Total { get; set; }
        [JsonPropertyName("created")] public int Created { get; set; }
        [JsonPropertyName("failed")] public int Failed { get; set; }
        [JsonPropertyName("skipped")] public int Skipped { get; set; }
        [JsonPropertyName("items")] public CreateResultItemData[]? Items { get; set; }
    }

    private sealed class CreateResultItemData
    {
        [JsonPropertyName("index")] public int Index { get; set; }
        [JsonPropertyName("name")] public string? Name { get; set; }
        [JsonPropertyName("account_id")] public long? AccountId { get; set; }
        [JsonPropertyName("status")] public string? Status { get; set; }
        [JsonPropertyName("message")] public string? Message { get; set; }
    }
}
