using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Serialization.Metadata;

namespace LanAi.RelayClient.Server;

/// <summary>
/// Talks to a remote relay over its versioned REST API.
/// </summary>
/// <remarks>
/// <para>
/// Deliberately stateless with respect to credentials: tokens arrive as method
/// arguments. That keeps refresh scheduling, secure storage and sign-out in one
/// place higher up instead of being split across this type.
/// </para>
/// <para>
/// The API paths here are all pre-existing server endpoints; this client
/// requires no backend change.
/// </para>
/// </remarks>
public sealed class RelayServerClient : IRelayServerClient
{
    private const string ApiPrefix = "api/v1/";

    /// <summary>
    /// Page size requested when walking the key list.
    /// </summary>
    /// <remarks>
    /// The server honours anything up to 1000 and otherwise falls back to its own
    /// default (<c>response.ParsePagination</c>), so this is a request, not a
    /// guarantee — the paging loop must not assume it was granted.
    /// </remarks>
    private const int KeyPageSize = 100;

    /// <summary>Upper bound on pages walked, so bad pagination metadata cannot hang the client.</summary>
    private const int MaxKeyPages = 100;

    /// <remarks>
    /// Resolves through the source-generated <see cref="RelayJsonContext"/> instead of
    /// reflection, which is what lets the client be published trimmed. See that type
    /// for why every contract here carries an explicit <c>[JsonConstructor]</c>.
    /// </remarks>
    private static readonly JsonSerializerOptions SerializerOptions = new(JsonSerializerDefaults.Web)
    {
        TypeInfoResolver = RelayJsonContext.Default,
    };

    /// <summary>Source-generated metadata for <typeparamref name="T"/>.</summary>
    /// <exception cref="RelayApiException">
    /// Thrown when the type was never registered in <see cref="RelayJsonContext"/>.
    /// Failing loudly here is deliberate: an unregistered contract is a build-time
    /// omission, and this turns it into a failure the endpoint's own test catches.
    /// </exception>
    private static JsonTypeInfo<T> TypeInfoFor<T>()
    {
        if (RelayJsonContext.Default.GetTypeInfo(typeof(T)) is JsonTypeInfo<T> typeInfo)
        {
            return typeInfo;
        }

        throw new RelayApiException(
            RelayFailure.MalformedResponse,
            $"{typeof(T).Name} 未注册到 {nameof(RelayJsonContext)}，无法解析服务器响应。");
    }

    private readonly HttpClient _http;

    /// <param name="httpClient">
    /// Transport to use. Its <see cref="HttpClient.BaseAddress"/> must be the relay's
    /// root (for example <c>https://relay.example.com/</c>) and must end in a slash,
    /// otherwise the last path segment would be dropped during URI resolution.
    /// </param>
    public RelayServerClient(HttpClient httpClient)
    {
        _http = httpClient ?? throw new ArgumentNullException(nameof(httpClient));

        Uri? baseAddress = _http.BaseAddress;
        if (baseAddress is null)
        {
            throw new ArgumentException("HttpClient.BaseAddress must be set to the relay root.", nameof(httpClient));
        }

        if (!baseAddress.AbsoluteUri.EndsWith('/'))
        {
            throw new ArgumentException(
                "HttpClient.BaseAddress must end with '/', otherwise relative paths lose their last segment.",
                nameof(httpClient));
        }
    }

    public Task<PublicSettings> GetPublicSettingsAsync(CancellationToken cancellationToken = default) =>
        SendAsync<PublicSettings>(HttpMethod.Get, "settings/public", body: null, accessToken: null, cancellationToken);

    public async Task<LoginOutcome> LoginAsync(
        string email,
        string password,
        CancellationToken cancellationToken = default)
    {
        // The 2FA branch and the success branch share one endpoint and are told
        // apart only by the payload shape, so the raw element is inspected before
        // being bound to either contract.
        JsonElement payload = await SendAsync<JsonElement>(
            HttpMethod.Post,
            "auth/login",
            new LoginBody(email, password),
            accessToken: null,
            cancellationToken,
            verifiesCredentials: true).ConfigureAwait(false);

        if (TryReadTwoFactorChallenge(payload, out string tempToken, out string? maskedEmail))
        {
            return LoginOutcome.TwoFactorRequired(tempToken, maskedEmail);
        }

        return LoginOutcome.Authenticated(RequireUsableTokens(Bind<AuthTokens>(payload)));
    }

    public async Task<AuthTokens> CompleteTwoFactorAsync(
        string tempToken,
        string totpCode,
        CancellationToken cancellationToken = default) =>
        RequireUsableTokens(await SendAsync<AuthTokens>(
            HttpMethod.Post,
            "auth/login/2fa",
            new TwoFactorBody(tempToken, totpCode),
            accessToken: null,
            cancellationToken,
            verifiesCredentials: true).ConfigureAwait(false));

    public async Task<AuthTokens> RegisterAsync(
        RegistrationRequest request,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(request);

        // Optional fields are omitted rather than sent empty: the server treats
        // an empty invitation code as "not supplied", but sending "" for a field
        // the operator disabled is a needless way to trip validation.
        var body = new Dictionary<string, object?>
        {
            ["email"] = request.Email,
            ["password"] = request.Password,
        };
        AddIfPresent(body, "verify_code", request.VerifyCode);
        AddIfPresent(body, "invitation_code", request.InvitationCode);
        AddIfPresent(body, "promo_code", request.PromoCode);
        AddIfPresent(body, "turnstile_token", request.TurnstileToken);

        return RequireUsableTokens(await SendAsync<AuthTokens>(
            HttpMethod.Post,
            "auth/register",
            body,
            accessToken: null,
            cancellationToken).ConfigureAwait(false));
    }

    public Task<VerifyCodeDispatch> SendVerifyCodeAsync(
        string email,
        string? turnstileToken,
        CancellationToken cancellationToken = default)
    {
        var body = new Dictionary<string, object?> { ["email"] = email };
        AddIfPresent(body, "turnstile_token", turnstileToken);

        return SendAsync<VerifyCodeDispatch>(
            HttpMethod.Post,
            "auth/send-verify-code",
            body,
            accessToken: null,
            cancellationToken);
    }

    public async Task<AuthTokens> RefreshAsync(string refreshToken, CancellationToken cancellationToken = default) =>
        RequireUsableTokens(await SendAsync<AuthTokens>(
            HttpMethod.Post,
            "auth/refresh",
            new RefreshTokenBody(refreshToken),
            accessToken: null,
            cancellationToken).ConfigureAwait(false));

    public async Task LogoutAsync(string refreshToken, CancellationToken cancellationToken = default) =>
        await SendAsync<JsonElement>(
            HttpMethod.Post,
            "auth/logout",
            new RefreshTokenBody(refreshToken),
            accessToken: null,
            cancellationToken).ConfigureAwait(false);

    public Task<RelayUser> GetCurrentUserAsync(string accessToken, CancellationToken cancellationToken = default) =>
        SendAsync<RelayUser>(HttpMethod.Get, "auth/me", body: null, accessToken, cancellationToken);

    public Task<DashboardStats> GetDashboardStatsAsync(
        string accessToken,
        CancellationToken cancellationToken = default) =>
        SendAsync<DashboardStats>(
            HttpMethod.Get,
            "usage/dashboard/stats",
            body: null,
            accessToken,
            cancellationToken);

    public async Task<IReadOnlyList<SubscriptionSummaryItem>> GetSubscriptionSummaryAsync(
        string accessToken,
        CancellationToken cancellationToken = default)
    {
        SubscriptionSummaryResponse response = await SendAsync<SubscriptionSummaryResponse>(
            HttpMethod.Get,
            "subscriptions/summary",
            body: null,
            accessToken,
            cancellationToken).ConfigureAwait(false);

        return response.Subscriptions ?? [];
    }

    public async Task<IReadOnlyList<RelayGroup>> GetAvailableGroupsAsync(
        string accessToken,
        CancellationToken cancellationToken = default) =>
        await SendAsync<RelayGroup[]>(
            HttpMethod.Get,
            "groups/available",
            body: null,
            accessToken,
            cancellationToken).ConfigureAwait(false);

    /// <remarks>
    /// Tolerates a null payload. The service returns a nil map when the rate
    /// repository is not wired (<c>api_key_service.go:1000</c>), and Go marshals
    /// that as <c>null</c> rather than <c>{}</c>. "No overrides" is the ordinary
    /// case for most users, so treating it as a malformed response would fail the
    /// whole group surface for exactly the people who have nothing special
    /// configured.
    /// </remarks>
    public async Task<IReadOnlyDictionary<long, double>> GetUserGroupRatesAsync(
        string accessToken,
        CancellationToken cancellationToken = default)
    {
        JsonElement payload = await SendAsync<JsonElement>(
            HttpMethod.Get,
            "groups/rates",
            body: null,
            accessToken,
            cancellationToken).ConfigureAwait(false);

        if (payload.ValueKind is JsonValueKind.Null or JsonValueKind.Undefined)
        {
            return new Dictionary<long, double>();
        }

        return Bind<Dictionary<long, double>>(payload);
    }

    /// <remarks>
    /// The date range is sent explicitly rather than relying on a server default,
    /// so the chart's axis and the window actually queried cannot drift apart.
    /// </remarks>
    public async Task<IReadOnlyList<UsageTrendPoint>> GetUsageTrendAsync(
        string accessToken,
        int days,
        CancellationToken cancellationToken = default)
    {
        (string start, string end) = WindowEndingToday(days);

        UsageSnapshot snapshot = await SendAsync<UsageSnapshot>(
            HttpMethod.Get,
            $"usage/dashboard/snapshot-v2?granularity=day&include_trend=true&include_model_stats=false&start_date={start}&end_date={end}",
            body: null,
            accessToken,
            cancellationToken).ConfigureAwait(false);

        return snapshot.Trend ?? [];
    }

    public async Task<IReadOnlyList<ModelUsage>> GetModelUsageAsync(
        string accessToken,
        int days,
        CancellationToken cancellationToken = default)
    {
        (string start, string end) = WindowEndingToday(days);

        ModelUsageResponse response = await SendAsync<ModelUsageResponse>(
            HttpMethod.Get,
            $"usage/dashboard/models?start_date={start}&end_date={end}",
            body: null,
            accessToken,
            cancellationToken).ConfigureAwait(false);

        return response.Models ?? [];
    }

    /// <remarks>
    /// Local dates, not UTC: the server buckets by the user's own timezone, so a
    /// UTC window would shift the days and make "today" appear half empty.
    /// </remarks>
    private static (string Start, string End) WindowEndingToday(int days)
    {
        DateTime today = DateTime.Now.Date;
        return (today.AddDays(-(Math.Max(days, 1) - 1)).ToString("yyyy-MM-dd"), today.ToString("yyyy-MM-dd"));
    }

    public async Task<IReadOnlyList<RelayApiKey>> ListApiKeysAsync(
        string accessToken,
        CancellationToken cancellationToken = default)
    {
        // The list is paginated. A managed-key lookup that only ever saw the first
        // page would decide "no key exists" for a user with many keys and issue a
        // duplicate, so the pages are walked to exhaustion.
        var all = new List<RelayApiKey>();
        int page = 1;

        while (true)
        {
            PagedResult<RelayApiKey> batch = await SendAsync<PagedResult<RelayApiKey>>(
                HttpMethod.Get,
                $"keys?page={page}&page_size={KeyPageSize}",
                body: null,
                accessToken,
                cancellationToken).ConfigureAwait(false);

            all.AddRange(batch.Items);

            // Driven by what the server reports, never by the page size that was
            // asked for: the server is free to serve a smaller page than requested,
            // and treating a short page as the last one would stop early — the very
            // under-fetch this loop exists to avoid. Pages is computed from the size
            // the server actually used, so it stays correct either way.
            if (batch.Items.Count == 0 || page >= batch.Pages)
            {
                break;
            }

            // A server whose metadata never terminates the walk must not hang the
            // client; the cap is far above any realistic key count.
            if (page >= MaxKeyPages)
            {
                break;
            }

            page++;
        }

        return all;
    }

    /// <remarks>
    /// <para>
    /// Carries a fresh <c>Idempotency-Key</c> on every attempt. The server derives
    /// its replay key from that header alone, never from the body
    /// (<c>idempotency.go</c>), and currently runs in observe-only mode where a
    /// missing header is tolerated — but an operator turning that off would make
    /// every header-less write fail outright. Sending one is therefore the only
    /// form that works on both settings.
    /// </para>
    /// <para>
    /// The value is new per attempt rather than stable per installation. A stable
    /// key would replay the cached response for up to a day, so a client reissuing
    /// after the user deleted the key in the web panel would be handed the deleted
    /// key's details and write a dead credential into <c>auth.json</c>, reporting
    /// success. Retry-safety for a single attempt is not worth that.
    /// </para>
    /// </remarks>
    public Task<RelayApiKey> CreateApiKeyAsync(
        string accessToken,
        string name,
        long? groupId,
        int expiresInDays,
        CancellationToken cancellationToken = default)
    {
        var body = new Dictionary<string, object?>
        {
            ["name"] = name,
            ["expires_in_days"] = expiresInDays,
        };

        // Omitted rather than sent as null, so the server applies its own default
        // instead of being told to clear the binding.
        if (groupId is { } id)
        {
            body["group_id"] = id;
        }

        return SendAsync<RelayApiKey>(
            HttpMethod.Post,
            "keys",
            body,
            accessToken,
            cancellationToken,
            idempotencyKey: Guid.NewGuid().ToString("N"));
    }

    /// <remarks>
    /// Carries <c>expires_at</c> alone. The same three-state rule that makes the
    /// group switch dangerous applies here in reverse: an RFC3339 value sets the
    /// expiry, which is exactly what a renewal wants — but any empty string would
    /// clear it and turn the lease into a permanent key.
    /// </remarks>
    public Task<RelayApiKey> RenewApiKeyAsync(
        string accessToken,
        long keyId,
        DateTimeOffset expiresAt,
        CancellationToken cancellationToken = default) =>
        SendAsync<RelayApiKey>(
            HttpMethod.Put,
            $"keys/{keyId}",
            new Dictionary<string, object?>
            {
                ["expires_at"] = expiresAt.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'"),
            },
            accessToken,
            cancellationToken);

    public Task<PaymentCheckoutInfo> GetCheckoutInfoAsync(
        string accessToken,
        CancellationToken cancellationToken = default) =>
        SendAsync<PaymentCheckoutInfo>(
            HttpMethod.Get,
            "payment/checkout-info",
            body: null,
            accessToken,
            cancellationToken);

    public Task<PaymentOrderCreateResult> CreateBalanceOrderAsync(
        string accessToken,
        decimal amount,
        string paymentType,
        CancellationToken cancellationToken = default)
    {
        if (amount <= 0)
        {
            throw new ArgumentOutOfRangeException(nameof(amount));
        }

        if (string.IsNullOrWhiteSpace(paymentType))
        {
            throw new ArgumentException("Payment type is required.", nameof(paymentType));
        }

        return SendAsync<PaymentOrderCreateResult>(
            HttpMethod.Post,
            "payment/orders",
            new BalanceOrderBody(amount, paymentType, "balance", IsMobile: false),
            accessToken,
            cancellationToken,
            idempotencyKey: Guid.NewGuid().ToString("N"));
    }

    public Task<PaymentOrder> GetPaymentOrderAsync(
        string accessToken,
        long orderId,
        CancellationToken cancellationToken = default) =>
        SendAsync<PaymentOrder>(
            HttpMethod.Get,
            $"payment/orders/{orderId}",
            body: null,
            accessToken,
            cancellationToken);

    public Task<PaymentOrder> VerifyPaymentOrderAsync(
        string accessToken,
        string outTradeNo,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(outTradeNo))
        {
            throw new ArgumentException("Out trade number is required.", nameof(outTradeNo));
        }

        return SendAsync<PaymentOrder>(
            HttpMethod.Post,
            "payment/orders/verify",
            new VerifyOrderBody(outTradeNo),
            accessToken,
            cancellationToken);
    }

    public async Task CancelPaymentOrderAsync(
        string accessToken,
        long orderId,
        CancellationToken cancellationToken = default) =>
        await SendAsync<JsonElement>(
            HttpMethod.Post,
            $"payment/orders/{orderId}/cancel",
            body: null,
            accessToken,
            cancellationToken).ConfigureAwait(false);

    public async Task DeleteApiKeyAsync(
        string accessToken,
        long keyId,
        CancellationToken cancellationToken = default) =>
        await SendAsync<JsonElement>(
            HttpMethod.Delete,
            $"keys/{keyId}",
            body: null,
            accessToken,
            cancellationToken).ConfigureAwait(false);

    /// <remarks>
    /// The body carries <c>group_id</c> alone, deliberately.
    /// <para>
    /// The update handler reads an <em>empty</em> <c>expires_at</c> as "clear the
    /// expiry" while an <em>absent</em> one means "leave it alone"
    /// (<c>api_key_handler.go:225-238</c>). Serialising a request record whose
    /// <c>ExpiresAt</c> defaulted to <c>""</c> would therefore turn a one-day lease
    /// into a key that never expires — silently, on an action the user thinks is
    /// just a group switch, and defeating the whole point of the F3.2 lease model.
    /// A dictionary with one entry cannot grow that default by accident.
    /// </para>
    /// </remarks>
    public Task<RelayApiKey> UpdateApiKeyGroupAsync(
        string accessToken,
        long keyId,
        long groupId,
        CancellationToken cancellationToken = default) =>
        SendAsync<RelayApiKey>(
            HttpMethod.Put,
            $"keys/{keyId}",
            new Dictionary<string, object?> { ["group_id"] = groupId },
            accessToken,
            cancellationToken);

    /// <summary>
    /// Rejects a token payload that cannot actually authenticate anything.
    /// </summary>
    /// <remarks>
    /// <para>
    /// Every field of <see cref="AuthTokens"/> has a default, so a reply that is
    /// merely <em>shaped</em> wrong — say <c>{"requires_2fa":true}</c> with no
    /// temp token — deserializes happily into an instance with an empty access
    /// token. Without this guard the client would report a successful sign-in and
    /// then fail every subsequent call with 401, which is far harder to diagnose
    /// than failing here.
    /// </para>
    /// <para>
    /// Only the access token is required. A missing refresh token is legitimate:
    /// the server falls back to issuing an access token alone when pair
    /// generation fails (see <c>respondWithTokenPair</c>). Sessions obtained that
    /// way simply cannot be silently renewed and will need a fresh sign-in.
    /// </para>
    /// </remarks>
    private static AuthTokens RequireUsableTokens(AuthTokens tokens)
    {
        if (string.IsNullOrWhiteSpace(tokens.AccessToken))
        {
            throw new RelayApiException(
                RelayFailure.MalformedResponse,
                "服务器返回的登录结果中没有访问令牌。");
        }

        return tokens;
    }

    private static void AddIfPresent(IDictionary<string, object?> body, string key, string? value)
    {
        if (!string.IsNullOrWhiteSpace(value))
        {
            body[key] = value;
        }
    }

    /// <summary>
    /// Detects the "second factor required" reply.
    /// </summary>
    /// <remarks>
    /// Requires both the flag and a usable temp token. A reply claiming 2FA
    /// without a token cannot be acted on, so it is treated as not-a-challenge
    /// and falls through to normal binding, which then fails loudly — better
    /// than stranding the user on a code prompt that can never succeed.
    /// </remarks>
    private static bool TryReadTwoFactorChallenge(
        JsonElement payload,
        out string tempToken,
        out string? maskedEmail)
    {
        tempToken = string.Empty;
        maskedEmail = null;

        if (payload.ValueKind != JsonValueKind.Object)
        {
            return false;
        }

        TotpChallengePayload? challenge = payload.Deserialize(RelayJsonContext.Default.TotpChallengePayload);
        if (challenge is null || !challenge.Requires2Fa || string.IsNullOrWhiteSpace(challenge.TempToken))
        {
            return false;
        }

        tempToken = challenge.TempToken;
        maskedEmail = challenge.UserEmailMasked;
        return true;
    }

    /// <param name="verifiesCredentials">
    /// True only for endpoints that check a password or code the user just typed.
    /// Decides how a 401 is read: on those endpoints it means "wrong credentials",
    /// everywhere else it means "the session expired". Collapsing the two would
    /// tell a user with a stale token that their password is wrong.
    /// </param>
    private async Task<T> SendAsync<T>(
        HttpMethod method,
        string relativePath,
        object? body,
        string? accessToken,
        CancellationToken cancellationToken,
        bool verifiesCredentials = false,
        string? idempotencyKey = null)
    {
        using var request = new HttpRequestMessage(method, ApiPrefix + relativePath);

        if (!string.IsNullOrWhiteSpace(idempotencyKey))
        {
            request.Headers.TryAddWithoutValidation("Idempotency-Key", idempotencyKey);
        }

        if (body is not null)
        {
            // Serialized against the body's runtime type rather than its declared
            // object?, so the source-generated metadata is actually found.
            JsonTypeInfo bodyTypeInfo = RelayJsonContext.Default.GetTypeInfo(body.GetType())
                ?? throw new RelayApiException(
                    RelayFailure.MalformedResponse,
                    $"{body.GetType().Name} 未注册到 {nameof(RelayJsonContext)}，无法构造请求体。");
            request.Content = JsonContent.Create(body, bodyTypeInfo);
        }

        if (!string.IsNullOrWhiteSpace(accessToken))
        {
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", accessToken);
        }

        HttpResponseMessage response;
        try
        {
            response = await _http.SendAsync(request, cancellationToken).ConfigureAwait(false);
        }
        catch (HttpRequestException ex)
        {
            throw new RelayApiException(
                RelayFailure.NetworkUnreachable,
                $"无法连接到服务器：{ex.Message}",
                innerException: ex);
        }
        catch (TaskCanceledException ex) when (!cancellationToken.IsCancellationRequested)
        {
            // Cancellation the caller did not ask for is a timeout, not an abort.
            throw new RelayApiException(
                RelayFailure.NetworkUnreachable,
                "连接服务器超时。",
                innerException: ex);
        }

        using (response)
        {
            return await ReadEnvelopeAsync<T>(response, verifiesCredentials, cancellationToken).ConfigureAwait(false);
        }
    }

    private static async Task<T> ReadEnvelopeAsync<T>(
        HttpResponseMessage response,
        bool verifiesCredentials,
        CancellationToken cancellationToken)
    {
        ApiEnvelope<JsonElement>? envelope;
        try
        {
            envelope = await response.Content
                .ReadFromJsonAsync(RelayJsonContext.Default.ApiEnvelopeJsonElement, cancellationToken)
                .ConfigureAwait(false);
        }
        catch (Exception ex) when (ex is JsonException or NotSupportedException)
        {
            // A body that is not our envelope usually means something in front of
            // the relay answered instead — a proxy error page, a captive portal,
            // a wrong base address. Saying "malformed" is more honest than
            // inventing an auth or server failure from the status code alone.
            throw new RelayApiException(
                RelayFailure.MalformedResponse,
                $"服务器返回了非预期的内容（HTTP {(int)response.StatusCode}）。",
                statusCode: (int)response.StatusCode,
                innerException: ex);
        }

        if (envelope is null)
        {
            throw new RelayApiException(
                RelayFailure.MalformedResponse,
                $"服务器返回了空响应（HTTP {(int)response.StatusCode}）。",
                statusCode: (int)response.StatusCode);
        }

        if (!response.IsSuccessStatusCode || !envelope.IsSuccess)
        {
            throw Classify(response.StatusCode, envelope, verifiesCredentials);
        }

        return Bind<T>(envelope.Data);
    }

    private static T Bind<T>(JsonElement data)
    {
        if (typeof(T) == typeof(JsonElement))
        {
            return (T)(object)data;
        }

        // An absent data field on an otherwise successful call is a contract
        // violation for every endpoint this client uses, so it is surfaced
        // rather than papered over with a default instance.
        if (data.ValueKind is JsonValueKind.Undefined or JsonValueKind.Null)
        {
            throw new RelayApiException(
                RelayFailure.MalformedResponse,
                $"服务器未返回 {typeof(T).Name} 数据。");
        }

        // Bound in two steps rather than with `??`: T is unconstrained here, and
        // the null-coalescing operator is not available on an unconstrained T?.
        T? bound;
        try
        {
            bound = data.Deserialize(TypeInfoFor<T>());
        }
        catch (JsonException ex)
        {
            throw new RelayApiException(
                RelayFailure.MalformedResponse,
                $"服务器返回的 {typeof(T).Name} 数据无法解析：{ex.Message}",
                innerException: ex);
        }

        if (bound is null)
        {
            throw new RelayApiException(
                RelayFailure.MalformedResponse,
                $"服务器返回的 {typeof(T).Name} 数据无法解析。");
        }

        return bound;
    }

    /// <summary>
    /// Turns a failed envelope into a classified exception.
    /// </summary>
    /// <remarks>
    /// The server's <c>reason</c> code is preferred over the HTTP status where it
    /// is more specific, because status alone collapses distinct situations the
    /// UI needs to separate — most importantly a wrong password versus an account
    /// that never had one (F1'.5).
    /// </remarks>
    private static RelayApiException Classify(
        HttpStatusCode statusCode,
        ApiEnvelope<JsonElement> envelope,
        bool verifiesCredentials)
    {
        string? reason = string.IsNullOrWhiteSpace(envelope.Reason) ? null : envelope.Reason;
        string message = string.IsNullOrWhiteSpace(envelope.Message)
            ? $"请求失败（HTTP {(int)statusCode}）。"
            : envelope.Message!;

        RelayFailure failure = reason switch
        {
            // Provisional: confirmed reason codes for the password-less account
            // case are still pending live verification (requirements risk 20).
            // Unmatched codes fall through to status-based classification, so an
            // unexpected value degrades to the generic path rather than being
            // misreported as something else.
            "PASSWORD_NOT_SET" or "NO_PASSWORD_SET" => RelayFailure.PasswordNotSet,
            _ => ClassifyByStatus(statusCode, verifiesCredentials),
        };

        return new RelayApiException(failure, message, reason, (int)statusCode);
    }

    private static RelayFailure ClassifyByStatus(HttpStatusCode statusCode, bool verifiesCredentials) => statusCode switch
    {
        HttpStatusCode.Unauthorized =>
            verifiesCredentials ? RelayFailure.InvalidCredentials : RelayFailure.Unauthenticated,
        HttpStatusCode.Forbidden => RelayFailure.Forbidden,
        HttpStatusCode.NotFound => RelayFailure.NotFound,
        HttpStatusCode.TooManyRequests => RelayFailure.RateLimited,
        HttpStatusCode.BadRequest or HttpStatusCode.Conflict or HttpStatusCode.UnprocessableEntity =>
            RelayFailure.Rejected,
        _ when (int)statusCode >= 500 => RelayFailure.ServerError,
        _ => RelayFailure.Rejected,
    };

    public Task<ClaudePreferenceDto> GetClaudePreferenceAsync(
        string accessToken,
        CancellationToken cancellationToken = default) =>
        SendAsync<ClaudePreferenceDto>(
            HttpMethod.Get,
            "user/claude-preference",
            null,
            accessToken,
            cancellationToken);

    public Task SetClaudePreferenceAsync(
        string accessToken,
        string model,
        string thinkingLevel,
        CancellationToken cancellationToken = default) =>
        SendAsync<JsonElement>(
            HttpMethod.Put,
            "user/claude-preference",
            new Dictionary<string, object?> { ["model"] = model, ["thinking_level"] = thinkingLevel },
            accessToken,
            cancellationToken);

    public Task<AnnouncementHead> GetAnnouncementHeadAsync(
        string accessToken,
        CancellationToken cancellationToken = default) =>
        SendAsync<AnnouncementHead>(
            HttpMethod.Get,
            "announcements/head",
            body: null,
            accessToken,
            cancellationToken);

    /// <remarks>
    /// <c>unread_only</c> is deliberately not sent. The client needs the read
    /// items too — to fill the list view, and to tell "already read" apart from
    /// "no longer visible" when pruning what it has already notified about.
    /// </remarks>
    public async Task<IReadOnlyList<RelayAnnouncement>> ListAnnouncementsAsync(
        string accessToken,
        CancellationToken cancellationToken = default) =>
        await SendAsync<RelayAnnouncement[]>(
            HttpMethod.Get,
            "announcements",
            body: null,
            accessToken,
            cancellationToken).ConfigureAwait(false);

    public Task MarkAnnouncementReadAsync(
        string accessToken,
        long announcementId,
        CancellationToken cancellationToken = default) =>
        SendAsync<JsonElement>(
            HttpMethod.Post,
            $"announcements/{announcementId}/read",
            body: null,
            accessToken,
            cancellationToken);
}
