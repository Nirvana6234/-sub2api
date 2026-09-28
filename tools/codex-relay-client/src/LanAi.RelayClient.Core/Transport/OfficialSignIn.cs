using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// Where the official sign-ins are made. The same authorize / token endpoints, client ids,
/// redirect addresses and scopes the relay server uses when an account is added there
/// (<c>internal/pkg/openai/oauth.go</c>, <c>internal/pkg/oauth/oauth.go</c>). Settable so tests
/// can point everything at a loopback server.
/// </summary>
internal sealed record OfficialSignInEndpoints(
    string OpenAiAuthorizeUrl,
    string OpenAiTokenUrl,
    string ChatGptSubscriptionsUrl,
    string ClaudeAuthorizeUrl,
    string ClaudeTokenUrl,
    int OpenAiCallbackPort)
{
    public static OfficialSignInEndpoints Official { get; } = new(
        "https://auth.openai.com/oauth/authorize",
        OfficialTokenEndpoints.Official.CodexTokenUrl,
        "https://chatgpt.com/backend-api/subscriptions",
        "https://claude.com/cai/oauth/authorize",
        OfficialTokenEndpoints.Official.ClaudeTokenUrl,
        1455);

    public const string OpenAiScopes = "openid profile email offline_access";

    public const string ClaudeRedirectUri = "https://platform.claude.com/oauth/code/callback";

    public const string ClaudeScopes = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload";

    /// <summary>The address registered for the Codex CLI client: <c>localhost</c>, port 1455, not 127.0.0.1.</summary>
    public string OpenAiRedirectUri => $"http://localhost:{OpenAiCallbackPort}/auth/callback";
}

/// <summary>A sign-in step failed; <see cref="Exception.Message"/> is for the user.</summary>
internal sealed class OfficialSignInException(string userMessage, Exception? innerException = null)
    : Exception(userMessage, innerException)
{
    public string UserMessage => Message;
}

/// <summary>PKCE and state, the way the relay server makes them.</summary>
internal static class Pkce
{
    /// <summary>64 random bytes as hex, as <c>openai.GenerateCodeVerifier</c>.</summary>
    public static string NewVerifier() => Convert.ToHexString(RandomNumberGenerator.GetBytes(64)).ToLowerInvariant();

    public static string NewState() => Convert.ToHexString(RandomNumberGenerator.GetBytes(32)).ToLowerInvariant();

    /// <summary>base64url(SHA-256(verifier)), no padding — the S256 method.</summary>
    public static string Challenge(string verifier) =>
        Convert.ToBase64String(SHA256.HashData(Encoding.ASCII.GetBytes(verifier))).TrimEnd('=').Replace('+', '-').Replace('/', '_');

    /// <summary>Compared in constant time: the state is what ties a callback to this sign-in.</summary>
    public static bool SameState(string? received, string expected) =>
        received is not null &&
        CryptographicOperations.FixedTimeEquals(Encoding.UTF8.GetBytes(received), Encoding.UTF8.GetBytes(expected));
}

/// <summary>Authorization links, and reading back what the user pastes.</summary>
internal static class OfficialAuthorization
{
    public static Uri OpenAiUrl(OfficialSignInEndpoints endpoints, string state, string challenge) =>
        new(endpoints.OpenAiAuthorizeUrl + "?" + Query(
            ("response_type", "code"),
            ("client_id", OfficialTokenEndpoints.CodexClientId),
            ("redirect_uri", endpoints.OpenAiRedirectUri),
            ("scope", OfficialSignInEndpoints.OpenAiScopes),
            ("state", state),
            ("code_challenge", challenge),
            ("code_challenge_method", "S256"),
            ("id_token_add_organizations", "true"),
            ("codex_cli_simplified_flow", "true")));

    public static Uri ClaudeUrl(OfficialSignInEndpoints endpoints, string state, string challenge) =>
        new(endpoints.ClaudeAuthorizeUrl + "?" + Query(
            ("code", "true"),
            ("client_id", OfficialTokenEndpoints.ClaudeClientId),
            ("response_type", "code"),
            ("redirect_uri", OfficialSignInEndpoints.ClaudeRedirectUri),
            ("scope", OfficialSignInEndpoints.ClaudeScopes),
            ("code_challenge", challenge),
            ("code_challenge_method", "S256"),
            ("state", state)));

    /// <summary>
    /// The code (and state) from what the user pasted: the whole address from the browser
    /// (<c>http://localhost:1455/auth/callback?code=…&amp;state=…</c>), just its query
    /// (<c>code=…&amp;state=…</c>), or Claude's <c>code#state</c>. Null when there is no code.
    /// </summary>
    public static (string Code, string? State)? ParsePasted(string? text)
    {
        string trimmed = (text ?? string.Empty).Trim().Trim('"', '\'');
        if (trimmed.Length == 0)
        {
            return null;
        }

        if (trimmed.Contains("code=", StringComparison.Ordinal))
        {
            int query = trimmed.IndexOf('?');
            string parameters = query >= 0 ? trimmed[(query + 1)..] : trimmed.TrimStart('?');
            int fragment = parameters.IndexOf('#');
            if (fragment >= 0)
            {
                parameters = parameters[..fragment];
            }

            Dictionary<string, string> values = ParseQuery(parameters);
            return values.TryGetValue("code", out string? code) && code.Length > 0
                ? (code, values.GetValueOrDefault("state"))
                : null;
        }

        if (trimmed.Contains(' ', StringComparison.Ordinal) || trimmed.Contains('/', StringComparison.Ordinal))
        {
            return null;
        }

        int hash = trimmed.IndexOf('#');
        return hash < 0
            ? (trimmed, null)
            : hash == 0 ? null : (trimmed[..hash], trimmed[(hash + 1)..] is { Length: > 0 } state ? state : null);
    }

    internal static Dictionary<string, string> ParseQuery(string query)
    {
        var values = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (string pair in query.Split('&', StringSplitOptions.RemoveEmptyEntries))
        {
            int equals = pair.IndexOf('=');
            string name = Uri.UnescapeDataString((equals < 0 ? pair : pair[..equals]).Replace('+', ' '));
            string value = equals < 0 ? string.Empty : Uri.UnescapeDataString(pair[(equals + 1)..].Replace('+', ' '));
            values.TryAdd(name, value);
        }

        return values;
    }

    private static string Query(params (string Name, string Value)[] parameters) =>
        string.Join("&", parameters.Select(p => $"{Uri.EscapeDataString(p.Name)}={Uri.EscapeDataString(p.Value)}"));
}

/// <summary>
/// Trades an authorization code for tokens, straight with the official side (§3.3 / §3.4).
/// Nothing about the request or response is logged beyond the status: both carry live credentials.
/// </summary>
internal sealed class OfficialTokenExchanger
{
    private static readonly TimeSpan RequestTimeout = TimeSpan.FromSeconds(30);
    private static readonly TimeSpan SubscriptionTimeout = TimeSpan.FromSeconds(10);

    private readonly OfficialSignInEndpoints _endpoints;
    private readonly Func<Uri, HttpMessageHandler> _handler;
    private readonly Func<DateTimeOffset> _clock;

    public OfficialTokenExchanger(
        OfficialSignInEndpoints? endpoints = null,
        Func<Uri, HttpMessageHandler>? handler = null,
        Func<DateTimeOffset>? clock = null)
    {
        _endpoints = endpoints ?? OfficialSignInEndpoints.Official;
        _handler = handler ?? OfficialTokenRefresher.CreateHandler;
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    public OfficialSignInEndpoints Endpoints => _endpoints;

    public async Task<OfficialAccount> ExchangeOpenAiAsync(string code, string verifier, CancellationToken cancellationToken)
    {
        var request = new HttpRequestMessage(HttpMethod.Post, _endpoints.OpenAiTokenUrl)
        {
            Content = new FormUrlEncodedContent(new Dictionary<string, string>
            {
                ["grant_type"] = "authorization_code",
                ["client_id"] = OfficialTokenEndpoints.CodexClientId,
                ["code"] = code,
                ["redirect_uri"] = _endpoints.OpenAiRedirectUri,
                ["code_verifier"] = verifier,
            }),
        };
        request.Headers.TryAddWithoutValidation("originator", "codex_cli_rs");
        request.Headers.TryAddWithoutValidation("User-Agent", "codex_cli_rs");

        JsonObject body = await SendAsync(request, "ChatGPT", cancellationToken).ConfigureAwait(false);
        string access = JwtPayload.String(body, "access_token");
        string refresh = JwtPayload.String(body, "refresh_token");
        string idToken = JwtPayload.String(body, "id_token");
        if (access.Length == 0 || refresh.Length == 0)
        {
            throw new OfficialSignInException("ChatGPT 没有返回完整的授权（缺少长期授权），请重新登录一次。");
        }

        var account = new OfficialAccount
        {
            Platform = OfficialAccount.OpenAi,
            AccessToken = access,
            RefreshToken = refresh,
            IdToken = idToken,
            ExpiresAt = JwtPayload.ExpiresAt(access) ?? ExpiresIn(body),
            Email = ChatGptIdToken.Email(idToken),
            PlanType = ChatGptIdToken.PlanType(idToken),
            ChatGptAccountId = ChatGptIdToken.AccountId(idToken),
            FedRamp = ChatGptIdToken.FedRamp(idToken),
            LastRefreshAt = _clock(),
        };

        return account with { SubscriptionExpiresAt = await SubscriptionExpiresAtAsync(account, cancellationToken).ConfigureAwait(false) };
    }

    public async Task<OfficialAccount> ExchangeClaudeAsync(string code, string? state, string verifier, CancellationToken cancellationToken)
    {
        var payload = new JsonObject
        {
            ["code"] = code,
            ["grant_type"] = "authorization_code",
            ["client_id"] = OfficialTokenEndpoints.ClaudeClientId,
            ["redirect_uri"] = OfficialSignInEndpoints.ClaudeRedirectUri,
            ["code_verifier"] = verifier,
        };
        if (!string.IsNullOrEmpty(state))
        {
            payload["state"] = state;
        }

        var request = new HttpRequestMessage(HttpMethod.Post, _endpoints.ClaudeTokenUrl)
        {
            Content = new StringContent(payload.ToJsonString(), Encoding.UTF8, "application/json"),
        };
        request.Headers.TryAddWithoutValidation("Accept", "application/json, text/plain, */*");
        request.Headers.TryAddWithoutValidation("User-Agent", "axios/1.13.6");

        JsonObject body = await SendAsync(request, "Claude", cancellationToken).ConfigureAwait(false);
        string access = JwtPayload.String(body, "access_token");
        string refresh = JwtPayload.String(body, "refresh_token");
        if (access.Length == 0 || refresh.Length == 0)
        {
            throw new OfficialSignInException("Claude 没有返回完整的授权（缺少长期授权），请重新登录一次。");
        }

        JsonObject? accountInfo = body["account"] as JsonObject;
        return new OfficialAccount
        {
            Platform = OfficialAccount.Anthropic,
            AccessToken = access,
            RefreshToken = refresh,
            ExpiresAt = ExpiresIn(body),
            Scope = JwtPayload.String(body, "scope"),
            Email = JwtPayload.String(accountInfo, "email_address"),
            AccountUuid = JwtPayload.String(accountInfo, "uuid"),
            OrgUuid = JwtPayload.String(body["organization"] as JsonObject, "uuid"),
            LastRefreshAt = _clock(),
        };
    }

    /// <summary>
    /// When the ChatGPT subscription ends — the read-only half of what the relay server does after
    /// a sign-in (D6). Best effort: nothing here stops the account being added.
    /// </summary>
    private async Task<string> SubscriptionExpiresAtAsync(OfficialAccount account, CancellationToken cancellationToken)
    {
        if (account.ChatGptAccountId.Length == 0)
        {
            return string.Empty;
        }

        try
        {
            var uri = new Uri($"{_endpoints.ChatGptSubscriptionsUrl}?account_id={Uri.EscapeDataString(account.ChatGptAccountId)}");
            using var request = new HttpRequestMessage(HttpMethod.Get, uri);
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", account.AccessToken);
            request.Headers.TryAddWithoutValidation("Origin", "https://chatgpt.com");
            request.Headers.TryAddWithoutValidation("Referer", "https://chatgpt.com/");
            request.Headers.TryAddWithoutValidation("Accept", "application/json");
            using var http = new HttpClient(_handler(uri)) { Timeout = Timeout.InfiniteTimeSpan };
            using CancellationTokenSource timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(SubscriptionTimeout);
            using HttpResponseMessage response = await http.SendAsync(request, timeout.Token).ConfigureAwait(false);
            if (!response.IsSuccessStatusCode)
            {
                ClientLog.Info($"查询 ChatGPT 订阅到期时间：HTTP {(int)response.StatusCode}");
                return string.Empty;
            }

            JsonObject? body = JsonNode.Parse(await response.Content.ReadAsStringAsync(timeout.Token).ConfigureAwait(false)) as JsonObject;
            return JwtPayload.String(body, "active_until");
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException or JsonException or UriFormatException)
        {
            if (cancellationToken.IsCancellationRequested)
            {
                throw;
            }

            ClientLog.Info($"查询 ChatGPT 订阅到期时间失败：{ex.GetType().Name}");
            return string.Empty;
        }
    }

    private DateTimeOffset? ExpiresIn(JsonObject body) =>
        body["expires_in"] is JsonValue value && value.TryGetValue(out long seconds) ? _clock().AddSeconds(seconds) : null;

    private async Task<JsonObject> SendAsync(HttpRequestMessage request, string product, CancellationToken cancellationToken)
    {
        using (request)
        using (var http = new HttpClient(_handler(request.RequestUri!)) { Timeout = Timeout.InfiniteTimeSpan })
        {
            using CancellationTokenSource timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(RequestTimeout);
            HttpResponseMessage response;
            try
            {
                response = await http.SendAsync(request, timeout.Token).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is HttpRequestException ||
                                       (ex is TaskCanceledException && !cancellationToken.IsCancellationRequested))
            {
                ClientLog.Warning($"换取 {product} 授权失败：{ex.GetType().Name}");
                throw new OfficialSignInException(
                    $"连不上 {request.RequestUri!.Host}，请检查代理/VPN 是否开启后重新登录。", ex);
            }

            using (response)
            {
                string text = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
                int status = (int)response.StatusCode;
                ClientLog.Info($"换取 {product} 授权：HTTP {status}");
                if (!response.IsSuccessStatusCode)
                {
                    throw new OfficialSignInException(status is 400 or 401 or 403
                        ? $"{product} 没有接受这次授权（授权码可能已过期或已用过，HTTP {status}），请重新登录。"
                        : $"换取 {product} 授权失败（HTTP {status}），请稍后重新登录。");
                }

                try
                {
                    return JsonNode.Parse(text) as JsonObject
                        ?? throw new OfficialSignInException($"{product} 返回了无法识别的授权内容，请重新登录。");
                }
                catch (JsonException ex)
                {
                    throw new OfficialSignInException($"{product} 返回了无法识别的授权内容，请重新登录。", ex);
                }
            }
        }
    }
}

/// <summary>
/// Receives the one OAuth callback of a sign-in on <c>http://localhost:{port}/auth/callback</c>,
/// the address registered for the Codex CLI client (D2).
/// </summary>
/// <remarks>
/// <para>
/// <c>localhost</c>, not 127.0.0.1 like <see cref="LoopbackHttpListener"/>: the browser comes
/// back with <c>Host: localhost:1455</c>, and http.sys matches registrations by host name.
/// </para>
/// <para>
/// The port is tried as a plain socket first. Another program holding it — Codex's own login
/// server, typically — does not stop http.sys from registering, only from ever receiving; so
/// a held port means "paste instead", decided before the browser is sent anywhere.
/// </para>
/// <para>
/// Only <c>/auth/callback</c> with the sign-in's own state is taken; anything else is answered
/// and ignored, so a stray or forged request cannot end the sign-in.
/// </para>
/// </remarks>
internal sealed class OAuthCallbackListener : IDisposable
{
    private readonly HttpListener _listener;

    private OAuthCallbackListener(HttpListener listener) => _listener = listener;

    /// <summary>Null when the port is taken; the sign-in then goes by pasting.</summary>
    public static OAuthCallbackListener? TryStart(int port)
    {
        try
        {
            using var probe = new System.Net.Sockets.TcpListener(IPAddress.Loopback, port);
            probe.Start();
        }
        catch (System.Net.Sockets.SocketException)
        {
            return null;
        }

        var listener = new HttpListener();
        listener.Prefixes.Add($"http://localhost:{port}/");
        try
        {
            listener.Start();
            return new OAuthCallbackListener(listener);
        }
        catch (Exception ex) when (ex is HttpListenerException or PlatformNotSupportedException)
        {
            listener.Close();
            ClientLog.Info($"授权回调端口 {port} 无法监听（{ex.GetType().Name}），改用粘贴");
            return null;
        }
    }

    /// <summary>Waits for the callback carrying <paramref name="expectedState"/>; returns its code.</summary>
    /// <exception cref="OfficialSignInException">The user declined in the browser.</exception>
    public async Task<string> WaitAsync(string expectedState, CancellationToken cancellationToken)
    {
        using CancellationTokenRegistration stop = cancellationToken.Register(() => _listener.Stop());
        while (true)
        {
            HttpListenerContext context;
            try
            {
                context = await _listener.GetContextAsync().ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is HttpListenerException or ObjectDisposedException or InvalidOperationException)
            {
                cancellationToken.ThrowIfCancellationRequested();
                throw new OfficialSignInException("授权回调意外中断，请把浏览器地址栏里的网址粘贴到客户端。", ex);
            }

            if (!string.Equals(context.Request.Url?.AbsolutePath, "/auth/callback", StringComparison.Ordinal))
            {
                Answer(context, 404, "Not found");
                continue;
            }

            Dictionary<string, string> query = OfficialAuthorization.ParseQuery(context.Request.Url?.Query.TrimStart('?') ?? string.Empty);
            if (!Pkce.SameState(query.GetValueOrDefault("state"), expectedState))
            {
                Answer(context, 400, "这个授权回调不属于当前的登录，已忽略。");
                continue;
            }

            if (query.GetValueOrDefault("error") is { Length: > 0 })
            {
                Answer(context, 200, "已取消授权。可以关闭这个页面，回到共飞助手。");
                throw new OfficialSignInException("你在浏览器里取消了授权。");
            }

            if (query.GetValueOrDefault("code") is not { Length: > 0 } code)
            {
                Answer(context, 400, "回调里没有授权码。");
                continue;
            }

            Answer(context, 200, "授权完成，可以关闭这个页面，回到共飞助手。");
            return code;
        }
    }

    public void Dispose() => _listener.Close();

    private static void Answer(HttpListenerContext context, int status, string message)
    {
        try
        {
            byte[] body = Encoding.UTF8.GetBytes(
                $"<!doctype html><html><head><meta charset=\"utf-8\"><title>共飞助手</title></head>" +
                $"<body style=\"font-family:sans-serif;padding:48px\"><p>{WebUtility.HtmlEncode(message)}</p></body></html>");
            context.Response.StatusCode = status;
            context.Response.ContentType = "text/html; charset=utf-8";
            context.Response.ContentLength64 = body.Length;
            context.Response.OutputStream.Write(body);
            context.Response.Close();
        }
        catch (Exception ex) when (ex is HttpListenerException or IOException or ObjectDisposedException)
        {
            // The browser went away; the sign-in does not depend on the page.
        }
    }
}

/// <summary>
/// One sign-in, from the authorization link to an account ready to keep (D2): back by itself
/// through <see cref="OAuthCallbackListener"/> where it can (ChatGPT), otherwise — and always
/// as well — by what the user pastes.
/// </summary>
internal sealed class OfficialSignInSession : IDisposable
{
    /// <summary>How long a sign-in waits for the user before giving up.</summary>
    public static readonly TimeSpan Timeout = TimeSpan.FromMinutes(10);

    private readonly OfficialTokenExchanger _exchanger;
    private readonly string _verifier = Pkce.NewVerifier();
    private readonly string _state = Pkce.NewState();
    private readonly CancellationTokenSource _cancel = new(Timeout);
    private readonly TaskCompletionSource<OfficialAccount> _done = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly OAuthCallbackListener? _callback;
    private int _exchanging;

    private OfficialSignInSession(LocalProxyKind kind, OfficialTokenExchanger exchanger)
    {
        Kind = kind;
        _exchanger = exchanger;
        string challenge = Pkce.Challenge(_verifier);
        if (kind == LocalProxyKind.Codex)
        {
            AuthorizeUrl = OfficialAuthorization.OpenAiUrl(exchanger.Endpoints, _state, challenge);
            _callback = OAuthCallbackListener.TryStart(exchanger.Endpoints.OpenAiCallbackPort);
        }
        else
        {
            AuthorizeUrl = OfficialAuthorization.ClaudeUrl(exchanger.Endpoints, _state, challenge);
        }

        _cancel.Token.Register(() => _done.TrySetException(new OfficialSignInException(
            _cancelledByUser ? "已取消登录。" : "等了 10 分钟没有完成授权，已放弃。需要时再点「登录」。")));
        if (_callback is { } callback)
        {
            _ = ListenAsync(callback);
        }

        ClientLog.Info($"开始在共飞里登录官方账号（{(kind == LocalProxyKind.Codex ? "ChatGPT" : "Claude")}，{(IsAutomatic ? "自动回调" : "粘贴")}）");
    }

    private volatile bool _cancelledByUser;

    public static OfficialSignInSession Start(LocalProxyKind kind, OfficialTokenExchanger exchanger)
    {
        ArgumentNullException.ThrowIfNull(exchanger);
        if (kind is not (LocalProxyKind.Codex or LocalProxyKind.ClaudeCode))
        {
            throw new ArgumentOutOfRangeException(nameof(kind));
        }

        return new OfficialSignInSession(kind, exchanger);
    }

    public LocalProxyKind Kind { get; }

    public Uri AuthorizeUrl { get; }

    /// <summary>The browser comes back by itself; pasting is still accepted.</summary>
    public bool IsAutomatic => _callback is not null;

    /// <summary>The signed-in account, not yet kept; faults with <see cref="OfficialSignInException"/>.</summary>
    public Task<OfficialAccount> Completion => _done.Task;

    /// <summary>
    /// What the user pasted: checked at once — a text with no code, or another sign-in's state,
    /// is refused here and the sign-in goes on waiting — then traded for the account.
    /// </summary>
    /// <exception cref="OfficialSignInException">Nothing usable was pasted.</exception>
    public void SubmitPasted(string text)
    {
        if (OfficialAuthorization.ParsePasted(text) is not { } parsed)
        {
            throw new OfficialSignInException(Kind == LocalProxyKind.Codex
                ? "没有认出授权码：请粘贴浏览器地址栏里以 http://localhost:1455/auth/callback 开头的完整网址。"
                : "没有认出授权码：请粘贴 Claude 授权页面上显示的那一串授权码。");
        }

        if (parsed.State is not null && !Pkce.SameState(parsed.State, _state))
        {
            throw new OfficialSignInException("这个授权码不属于当前这次登录（可能是之前那次的），请用这次打开的授权页面重新登录。");
        }

        if (Kind == LocalProxyKind.Codex && parsed.State is null)
        {
            throw new OfficialSignInException("粘贴的内容不完整：请粘贴浏览器地址栏里的完整网址（要包含 state=…）。");
        }

        _ = ExchangeAsync(parsed.Code, parsed.State);
    }

    public void Cancel()
    {
        _cancelledByUser = true;
        _cancel.Cancel();
    }

    public void Dispose()
    {
        Cancel();
        _callback?.Dispose();
        _cancel.Dispose();
    }

    private async Task ListenAsync(OAuthCallbackListener callback)
    {
        try
        {
            string code = await callback.WaitAsync(_state, _cancel.Token).ConfigureAwait(false);
            await ExchangeAsync(code, _state).ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
            // Cancelled or timed out; the registration above has said which.
        }
        catch (OfficialSignInException ex)
        {
            _done.TrySetException(ex);
        }
        finally
        {
            callback.Dispose();
        }
    }

    private async Task ExchangeAsync(string code, string? state)
    {
        // The code works once: a paste arriving after the callback (or a second paste) must not spend it again.
        if (Interlocked.Exchange(ref _exchanging, 1) == 1)
        {
            return;
        }

        try
        {
            OfficialAccount account = Kind == LocalProxyKind.Codex
                ? await _exchanger.ExchangeOpenAiAsync(code, _verifier, _cancel.Token).ConfigureAwait(false)
                : await _exchanger.ExchangeClaudeAsync(code, state, _verifier, _cancel.Token).ConfigureAwait(false);
            ClientLog.Info($"在共飞里登录官方账号成功（{account.Platform}）");
            _done.TrySetResult(account);
        }
        catch (OfficialSignInException ex)
        {
            ClientLog.Warning("在共飞里登录官方账号失败：换取授权未成功");
            _done.TrySetException(ex);
        }
        catch (OperationCanceledException)
        {
            // Said by the cancellation registration.
        }
    }
}
