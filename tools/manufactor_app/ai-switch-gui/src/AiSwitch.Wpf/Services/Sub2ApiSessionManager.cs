using System.Net;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace LanAi.Workspace.Wpf.Services;

internal interface ISub2ApiSessionManager : IDisposable
{
    Sub2ApiSessionState Current { get; }

    event EventHandler? SessionChanged;

    Task RestoreAsync(Uri apiBaseUri, CancellationToken cancellationToken);

    Task<Sub2ApiSessionAccess> LoginAsync(
        Uri apiBaseUri,
        string email,
        string password,
        CancellationToken cancellationToken);

    Task<Sub2ApiSessionAccess> LoginAsync(
        Uri apiBaseUri,
        string email,
        string password,
        bool allowInsecurePublicHttp,
        CancellationToken cancellationToken);

    Task<Sub2ApiSessionAccess> LoginLocalControlAsync(
        Uri apiBaseUri,
        string localControlToken,
        CancellationToken cancellationToken)
        => throw new Sub2ApiSessionException(Sub2ApiSessionFailure.AuthorizationUnavailable);

    Task<Sub2ApiSessionAccess> GetAccessAsync(Uri apiBaseUri, CancellationToken cancellationToken);

    Task LogoutAsync(CancellationToken cancellationToken);
}

internal sealed record Sub2ApiSessionState(
    bool IsAuthenticated,
    bool IsRestoring,
    bool IsAdministrator,
    string RoleLabel,
    decimal Balance,
    decimal FrozenBalance,
    DateTimeOffset? ExpiresAtUtc,
    Uri? ApiBaseUri,
    string Status)
{
    /// <summary>Account name reported by the gateway; empty when signed out.</summary>
    public string Username { get; init; } = string.Empty;

    /// <summary>Account email reported by the gateway; empty when signed out.</summary>
    public string Email { get; init; } = string.Empty;

    /// <summary>
    /// True when the session was opened with the machine-local control token
    /// instead of an account password. The identity badge shows a dedicated
    /// label for it so the workspace is never mistaken for a cloud account.
    /// </summary>
    public bool IsLocalControl { get; init; }

    public static Sub2ApiSessionState SignedOut { get; } = new(
        false,
        false,
        false,
        "未登录",
        0m,
        0m,
        null,
        null,
        "登录后可在用量仪表盘使用同一安全会话。");
}

internal sealed class Sub2ApiSessionAccess
{
    internal Sub2ApiSessionAccess(
        Uri apiBaseUri,
        string accessToken,
        long userId,
        string role,
        decimal balance,
        decimal frozenBalance,
        DateTimeOffset expiresAtUtc)
    {
        ApiBaseUri = apiBaseUri;
        AccessToken = accessToken;
        UserId = userId;
        Role = role;
        Balance = balance;
        FrozenBalance = frozenBalance;
        ExpiresAtUtc = expiresAtUtc;
    }

    public Uri ApiBaseUri { get; }

    internal string AccessToken { get; }

    public long UserId { get; }

    public string Role { get; }

    public decimal Balance { get; }

    public decimal FrozenBalance { get; }

    public DateTimeOffset ExpiresAtUtc { get; }

    /// <summary>Account name reported by the gateway; may be empty.</summary>
    public string Username { get; init; } = string.Empty;

    /// <summary>Account email reported by the gateway; may be empty.</summary>
    public string Email { get; init; } = string.Empty;

    public bool IsAdministrator => string.Equals(Role, "admin", StringComparison.Ordinal);

    public override string ToString() => "Sub2API session access";
}

internal enum Sub2ApiSessionFailure
{
    AuthorizationUnavailable,
    InvalidCredentials,
    RequiresTwoFactor,
    Forbidden,
    ComplianceRequired,
    GatewayUnavailable,
    SecureStorageUnavailable,
    ProtocolMismatch,
}

internal sealed class Sub2ApiSessionException : Exception
{
    public Sub2ApiSessionException(Sub2ApiSessionFailure failure)
        : base(failure.ToString())
        => Failure = failure;

    public Sub2ApiSessionFailure Failure { get; }
}

/// <summary>
/// Owns the reusable Sub2API account session for the desktop app.
/// Passwords are used only for the login request, access tokens stay in
/// memory, and only a rotating refresh token is delegated to the DPAPI store.
/// </summary>
internal sealed class Sub2ApiSessionManager : ISub2ApiSessionManager
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
        NumberHandling = JsonNumberHandling.AllowReadingFromString,
    };

    private static readonly TimeSpan AccessTokenSafetyWindow = TimeSpan.FromMinutes(2);
    private static readonly TimeSpan DefaultAccessTokenLifetime = TimeSpan.FromHours(1);

    private readonly ILocalSub2ApiAccountSessionStore _store;
    private readonly HttpClient _httpClient;
    private readonly bool _ownsHttpClient;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private Sub2ApiSessionAccess? _access;
    private string? _refreshToken;
    private bool _isLocalControlSession;
    private bool _disposed;

    public Sub2ApiSessionManager()
        : this(
            new LocalSub2ApiAccountSessionStore(),
            new HttpClient(new HttpClientHandler
            {
                // This client carries refresh tokens and the local-control
                // capability. It must never hand either to a system proxy.
                UseProxy = false,
                AllowAutoRedirect = false,
            })
            {
                Timeout = TimeSpan.FromSeconds(15),
            },
            ownsHttpClient: true)
    {
    }

    internal Sub2ApiSessionManager(
        ILocalSub2ApiAccountSessionStore store,
        HttpClient httpClient,
        bool ownsHttpClient = false)
    {
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        _ownsHttpClient = ownsHttpClient;
    }

    public Sub2ApiSessionState Current { get; private set; } = Sub2ApiSessionState.SignedOut;

    public event EventHandler? SessionChanged;

    public async Task RestoreAsync(Uri apiBaseUri, CancellationToken cancellationToken)
    {
        // Restore is only attempted for the source already selected by the user.
        // Public HTTP still requires explicit confirmation on the initial login.
        Uri normalizedBaseUri = RequireGateway(apiBaseUri);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ThrowIfDisposed();
            if (CanReuseAccess(normalizedBaseUri))
            {
                return;
            }

            SetState(Current with
            {
                IsRestoring = true,
                Status = "正在恢复已保存的登录…",
            });

            LocalSub2ApiAccountSession? saved = _store.Load(normalizedBaseUri);
            if (saved is null)
            {
                ClearMemory();
                SetState(Sub2ApiSessionState.SignedOut);
                return;
            }

            try
            {
                await RefreshCoreAsync(normalizedBaseUri, saved.RefreshToken, cancellationToken).ConfigureAwait(false);
            }
            catch (Sub2ApiSessionException exception) when (
                exception.Failure is Sub2ApiSessionFailure.InvalidCredentials or
                    Sub2ApiSessionFailure.Forbidden or
                    Sub2ApiSessionFailure.ComplianceRequired or
                    Sub2ApiSessionFailure.ProtocolMismatch)
            {
                _store.Clear(normalizedBaseUri);
                ClearMemory();
                SetState(Sub2ApiSessionState.SignedOut with
                {
                    Status = "已保存的登录已失效，请重新登录。",
                });
            }
        }
        finally
        {
            if (Current.IsRestoring)
            {
                SetState(Current with { IsRestoring = false });
            }

            _gate.Release();
        }
    }

    public async Task<Sub2ApiSessionAccess> LoginAsync(
        Uri apiBaseUri,
        string email,
        string password,
        CancellationToken cancellationToken)
        => await LoginAsync(
                apiBaseUri,
                email,
                password,
                allowInsecurePublicHttp: false,
                cancellationToken)
            .ConfigureAwait(false);

    public async Task<Sub2ApiSessionAccess> LoginAsync(
        Uri apiBaseUri,
        string email,
        string password,
        bool allowInsecurePublicHttp,
        CancellationToken cancellationToken)
    {
        Uri normalizedBaseUri = RequireGateway(apiBaseUri, allowInsecurePublicHttp);
        if (string.IsNullOrWhiteSpace(email) || string.IsNullOrEmpty(password))
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.AuthorizationUnavailable);
        }

        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ThrowIfDisposed();
            AuthenticationData authentication = await LoginCoreAsync(
                    normalizedBaseUri,
                    email.Trim(),
                    password,
                    cancellationToken)
                .ConfigureAwait(false);

            if (authentication.RequiresTwoFactor)
            {
                throw new Sub2ApiSessionException(Sub2ApiSessionFailure.RequiresTwoFactor);
            }

            if (!TryValidateAuthentication(authentication, out string? accessToken, out string? refreshToken) ||
                authentication.User is null)
            {
                throw new Sub2ApiSessionException(Sub2ApiSessionFailure.ProtocolMismatch);
            }

            // A successful login response is not enough to prove that the
            // issued access token can authorize subsequent user APIs. Verify
            // it before publishing an authenticated application state.
            UserData user = await GetCurrentUserAsync(
                    normalizedBaseUri,
                    accessToken!,
                    cancellationToken)
                .ConfigureAwait(false);
            DateTimeOffset expiresAt = ResolveExpiry(authentication.ExpiresIn);
            SaveRotatedSession(normalizedBaseUri, refreshToken!, user);
            _refreshToken = refreshToken;
            _isLocalControlSession = false;
            _access = CreateAccess(normalizedBaseUri, accessToken!, user, expiresAt);
            ApplyAuthenticatedState(_access);
            return _access;
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task<Sub2ApiSessionAccess> LoginLocalControlAsync(
        Uri apiBaseUri,
        string localControlToken,
        CancellationToken cancellationToken)
    {
        Uri normalizedBaseUri = RequireGateway(apiBaseUri);
        if (!IPAddress.TryParse(normalizedBaseUri.Host, out IPAddress? address) ||
            !IPAddress.IsLoopback(address) ||
            string.IsNullOrWhiteSpace(localControlToken))
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.AuthorizationUnavailable);
        }

        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ThrowIfDisposed();
            using var request = CreateJsonRequest(
                HttpMethod.Post,
                normalizedBaseUri,
                "api/v1/auth/local-control",
                "{}"u8.ToArray());
            request.Headers.TryAddWithoutValidation("X-Local-Control-Token", localControlToken.Trim());
            AuthenticationData authentication = await SendAndReadAsync<AuthenticationData>(request, cancellationToken)
                .ConfigureAwait(false);
            if (!TryValidateAuthentication(authentication, out string? accessToken, out string? refreshToken) ||
                authentication.User is null)
            {
                throw new Sub2ApiSessionException(Sub2ApiSessionFailure.ProtocolMismatch);
            }

            UserData user = await GetCurrentUserAsync(normalizedBaseUri, accessToken!, cancellationToken)
                .ConfigureAwait(false);
            if (!string.Equals(user.Role, "admin", StringComparison.Ordinal))
            {
                throw new Sub2ApiSessionException(Sub2ApiSessionFailure.Forbidden);
            }

            DateTimeOffset expiresAt = ResolveExpiry(authentication.ExpiresIn);
            SaveRotatedSession(normalizedBaseUri, refreshToken!, user);
            _refreshToken = refreshToken;
            _isLocalControlSession = true;
            _access = CreateAccess(normalizedBaseUri, accessToken!, user, expiresAt);
            ApplyAuthenticatedState(_access);
            return _access;
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task<Sub2ApiSessionAccess> GetAccessAsync(Uri apiBaseUri, CancellationToken cancellationToken)
    {
        Uri normalizedBaseUri = RequireGateway(apiBaseUri, allowInsecurePublicHttp: true);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ThrowIfDisposed();
            if (CanReuseAccess(normalizedBaseUri))
            {
                return _access!;
            }

            string? refreshToken = _refreshToken;
            if (string.IsNullOrWhiteSpace(refreshToken))
            {
                LocalSub2ApiAccountSession? saved = _store.Load(normalizedBaseUri);
                if (saved is null)
                {
                    throw new Sub2ApiSessionException(Sub2ApiSessionFailure.AuthorizationUnavailable);
                }

                refreshToken = saved.RefreshToken;
            }

            return await RefreshCoreAsync(normalizedBaseUri, refreshToken, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task LogoutAsync(CancellationToken cancellationToken)
    {
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ThrowIfDisposed();
            Uri? baseUri = _access?.ApiBaseUri ?? Current.ApiBaseUri ?? _store.LoadMostRecent()?.ApiBaseUri;
            LocalSub2ApiAccountSession? saved = baseUri is null ? null : _store.Load(baseUri);
            string? refreshToken = _refreshToken ?? saved?.RefreshToken;

            if (baseUri is not null && !string.IsNullOrWhiteSpace(refreshToken))
            {
                byte[] body = JsonSerializer.SerializeToUtf8Bytes(new { refresh_token = refreshToken });
                try
                {
                    using var request = CreateJsonRequest(HttpMethod.Post, baseUri, "api/v1/auth/logout", body);
                    using HttpResponseMessage _ = await _httpClient
                        .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                        .ConfigureAwait(false);
                }
                catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException)
                {
                    // Local logout is authoritative even when the gateway is offline.
                }
                finally
                {
                    CryptographicOperations.ZeroMemory(body);
                }
            }

            if (baseUri is not null)
            {
                _store.Clear(baseUri);
            }
            ClearMemory();
            SetState(Sub2ApiSessionState.SignedOut with { Status = "已退出登录。" });
        }
        finally
        {
            _gate.Release();
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        ClearMemory();
        _gate.Dispose();
        if (_ownsHttpClient)
        {
            _httpClient.Dispose();
        }
    }

    private async Task<Sub2ApiSessionAccess> RefreshCoreAsync(
        Uri baseUri,
        string refreshToken,
        CancellationToken cancellationToken)
    {
        AuthenticationData authentication = await RefreshTokenCoreAsync(baseUri, refreshToken, cancellationToken)
            .ConfigureAwait(false);
        if (!TryValidateAuthentication(authentication, out string? accessToken, out string? rotatedRefreshToken))
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.ProtocolMismatch);
        }

        UserData user = await GetCurrentUserAsync(baseUri, accessToken!, cancellationToken).ConfigureAwait(false);
        DateTimeOffset expiresAt = ResolveExpiry(authentication.ExpiresIn);
        SaveRotatedSession(baseUri, rotatedRefreshToken!, user);
        _refreshToken = rotatedRefreshToken;
        _access = CreateAccess(baseUri, accessToken!, user, expiresAt);
        ApplyAuthenticatedState(_access);
        return _access;
    }

    private async Task<AuthenticationData> LoginCoreAsync(
        Uri baseUri,
        string email,
        string password,
        CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new { email, password });
        try
        {
            using var request = CreateJsonRequest(HttpMethod.Post, baseUri, "api/v1/auth/login", body);
            return await SendAndReadAsync<AuthenticationData>(request, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(body);
        }
    }

    private async Task<AuthenticationData> RefreshTokenCoreAsync(
        Uri baseUri,
        string refreshToken,
        CancellationToken cancellationToken)
    {
        byte[] body = JsonSerializer.SerializeToUtf8Bytes(new { refresh_token = refreshToken });
        try
        {
            using var request = CreateJsonRequest(HttpMethod.Post, baseUri, "api/v1/auth/refresh", body);
            return await SendAndReadAsync<AuthenticationData>(request, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(body);
        }
    }

    private async Task<UserData> GetCurrentUserAsync(
        Uri baseUri,
        string accessToken,
        CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, new Uri(baseUri, "api/v1/auth/me"));
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", accessToken);
        UserData user = await SendAndReadAsync<UserData>(request, cancellationToken).ConfigureAwait(false);
        return RequireUser(user);
    }

    private async Task<T> SendAndReadAsync<T>(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        try
        {
            using HttpResponseMessage response = await _httpClient
                .SendAsync(request, HttpCompletionOption.ResponseContentRead, cancellationToken)
                .ConfigureAwait(false);
            if (!response.IsSuccessStatusCode)
            {
                throw new Sub2ApiSessionException(MapFailure(response.StatusCode));
            }

            byte[] bytes = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
            try
            {
                ApiEnvelope<T>? envelope = JsonSerializer.Deserialize<ApiEnvelope<T>>(bytes, JsonOptions);
                if (envelope?.Code != 0 || envelope.Data is null)
                {
                    throw new Sub2ApiSessionException(Sub2ApiSessionFailure.ProtocolMismatch);
                }

                return envelope.Data;
            }
            catch (JsonException)
            {
                throw new Sub2ApiSessionException(Sub2ApiSessionFailure.ProtocolMismatch);
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
        catch (TaskCanceledException)
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.GatewayUnavailable);
        }
        catch (HttpRequestException)
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.GatewayUnavailable);
        }
    }

    private void SaveRotatedSession(Uri baseUri, string refreshToken, UserData user)
    {
        if (!LocalSub2ApiAccountSession.TryCreate(
                refreshToken,
                baseUri.AbsoluteUri,
                user.Id,
                user.Role,
                DateTimeOffset.UtcNow,
                out LocalSub2ApiAccountSession? session))
        {
            // Explicit public-HTTP login may remain available for one-off
            // troubleshooting, but its refresh token must never be persisted.
            if (string.Equals(baseUri.Scheme, Uri.UriSchemeHttp, StringComparison.OrdinalIgnoreCase) &&
                (!IPAddress.TryParse(baseUri.Host, out IPAddress? address) || !IPAddress.IsLoopback(address)))
            {
                return;
            }

            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.ProtocolMismatch);
        }

        if (_store.Save(session!) != LocalSub2ApiAccountSessionSaveResult.Saved)
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.SecureStorageUnavailable);
        }
    }

    private void ApplyAuthenticatedState(Sub2ApiSessionAccess access)
        => SetState(new Sub2ApiSessionState(
            true,
            false,
            access.IsAdministrator,
            access.IsAdministrator ? "管理员" : "普通用户",
            access.Balance,
            access.FrozenBalance,
            access.ExpiresAtUtc,
            access.ApiBaseUri,
            "已登录；用量仪表盘将使用此安全会话。")
        {
            Username = access.Username,
            Email = access.Email,
            IsLocalControl = _isLocalControlSession,
        });

    private bool CanReuseAccess(Uri baseUri)
        => _access is not null &&
           SameEndpoint(_access.ApiBaseUri, baseUri) &&
           _access.ExpiresAtUtc > DateTimeOffset.UtcNow + AccessTokenSafetyWindow;

    private void SetState(Sub2ApiSessionState state)
    {
        if (Current == state)
        {
            return;
        }

        Current = state;
        SessionChanged?.Invoke(this, EventArgs.Empty);
    }

    private void ClearMemory()
    {
        _access = null;
        _refreshToken = null;
        _isLocalControlSession = false;
    }

    private void ThrowIfDisposed() => ObjectDisposedException.ThrowIf(_disposed, this);

    private static Sub2ApiSessionAccess CreateAccess(
        Uri baseUri,
        string accessToken,
        UserData user,
        DateTimeOffset expiresAtUtc)
        => new(baseUri, accessToken, user.Id, user.Role!, user.Balance, user.FrozenBalance, expiresAtUtc)
        {
            Username = (user.Username ?? string.Empty).Trim(),
            Email = (user.Email ?? string.Empty).Trim(),
        };

    private static UserData RequireUser(UserData user)
    {
        if (user.Id <= 0 || !LocalSub2ApiAccountSession.TryNormalizeRole(user.Role, out string? role))
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.ProtocolMismatch);
        }

        user.Role = role;
        return user;
    }

    private static bool TryValidateAuthentication(
        AuthenticationData authentication,
        out string? accessToken,
        out string? refreshToken)
    {
        accessToken = NormalizeToken(authentication.AccessToken);
        refreshToken = NormalizeToken(authentication.RefreshToken);
        return accessToken is not null && refreshToken is not null;
    }

    private static string? NormalizeToken(string? value)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return null;
        }

        string candidate = value.Trim();
        return candidate.Length <= 8192 && !candidate.Any(char.IsControl) ? candidate : null;
    }

    private static DateTimeOffset ResolveExpiry(int seconds)
    {
        TimeSpan lifetime = seconds is > 0 and <= 86400
            ? TimeSpan.FromSeconds(seconds)
            : DefaultAccessTokenLifetime;
        return DateTimeOffset.UtcNow + lifetime;
    }

    private static Sub2ApiSessionFailure MapFailure(HttpStatusCode statusCode)
        => statusCode switch
        {
            HttpStatusCode.Unauthorized => Sub2ApiSessionFailure.InvalidCredentials,
            HttpStatusCode.Forbidden => Sub2ApiSessionFailure.Forbidden,
            HttpStatusCode.Locked => Sub2ApiSessionFailure.ComplianceRequired,
            _ => Sub2ApiSessionFailure.GatewayUnavailable,
        };

    private static HttpRequestMessage CreateJsonRequest(HttpMethod method, Uri baseUri, string path, byte[] body)
    {
        var request = new HttpRequestMessage(method, new Uri(baseUri, path))
        {
            Content = new ByteArrayContent(body),
        };
        request.Content.Headers.ContentType = new MediaTypeHeaderValue("application/json") { CharSet = "utf-8" };
        return request;
    }

    private static Uri RequireGateway(Uri value, bool allowInsecurePublicHttp = false)
    {
        ArgumentNullException.ThrowIfNull(value);
        if (!Sub2ApiEndpointNormalizer.TryNormalizeApiBaseUri(
                value.AbsoluteUri,
                allowInsecurePublicHttp,
                out Uri? normalized))
        {
            throw new Sub2ApiSessionException(Sub2ApiSessionFailure.GatewayUnavailable);
        }

        return normalized!;
    }

    private static bool SameEndpoint(Uri left, Uri right)
        => Uri.Compare(
            left,
            right,
            UriComponents.SchemeAndServer | UriComponents.Path,
            UriFormat.SafeUnescaped,
            StringComparison.OrdinalIgnoreCase) == 0;

    private sealed class ApiEnvelope<T>
    {
        [JsonPropertyName("code")] public int Code { get; set; }

        [JsonPropertyName("data")] public T? Data { get; set; }
    }

    private sealed class AuthenticationData
    {
        [JsonPropertyName("access_token")] public string? AccessToken { get; set; }

        [JsonPropertyName("refresh_token")] public string? RefreshToken { get; set; }

        [JsonPropertyName("expires_in")] public int ExpiresIn { get; set; }

        [JsonPropertyName("requires_2fa")] public bool RequiresTwoFactor { get; set; }

        [JsonPropertyName("user")] public UserData? User { get; set; }
    }

    private sealed class UserData
    {
        [JsonPropertyName("id")] public long Id { get; set; }

        [JsonPropertyName("username")] public string? Username { get; set; }

        [JsonPropertyName("email")] public string? Email { get; set; }

        [JsonPropertyName("role")] public string? Role { get; set; }

        [JsonPropertyName("balance")] public decimal Balance { get; set; }

        [JsonPropertyName("frozen_balance")] public decimal FrozenBalance { get; set; }
    }
}
