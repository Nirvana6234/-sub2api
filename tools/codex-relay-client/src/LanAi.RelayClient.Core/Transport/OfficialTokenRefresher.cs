using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>What a Claude refresh returned.</summary>
internal sealed record ClaudeRefreshedTokens(string AccessToken, string? RefreshToken, long? ExpiresInSeconds, string? Scope)
{
    public override string ToString() => "ClaudeRefreshedTokens { … }";
}

/// <summary>Renews the user's own official sign-ins with their refresh tokens.</summary>
internal interface IOfficialTokenRefresher
{
    Task<CodexRefreshedTokens> RefreshCodexAsync(string refreshToken, CancellationToken cancellationToken);

    Task<ClaudeRefreshedTokens> RefreshClaudeAsync(string refreshToken, CancellationToken cancellationToken);
}

/// <summary>Where each sign-in is renewed. Settable so tests can point both at a loopback server.</summary>
internal sealed record OfficialTokenEndpoints(string CodexTokenUrl, string ClaudeTokenUrl)
{
    /// <summary>The same endpoints and client ids the official CLIs use (and the relay server, in internal/pkg).</summary>
    public static OfficialTokenEndpoints Official { get; } = new(
        "https://auth.openai.com/oauth/token",
        "https://platform.claude.com/v1/oauth/token");

    public const string CodexClientId = "app_EMoamEEZ73f0CkXaXp7hrann";

    public const string ClaudeClientId = "9d1c250a-e61b-44d9-88ed-5944d1962f5e";
}

/// <summary>
/// Calls the official token endpoints the way Codex and Claude Code do. Nothing about the
/// request or response is logged beyond the status: both carry live credentials.
/// </summary>
/// <remarks>
/// Built per call with the proxy as it is set now, like the local proxy's own connection:
/// the official hosts are often reachable only through a proxy the user turns on later.
/// </remarks>
internal sealed class OfficialTokenRefresher : IOfficialTokenRefresher
{
    private static readonly TimeSpan RequestTimeout = TimeSpan.FromSeconds(30);

    private readonly OfficialTokenEndpoints _endpoints;
    private readonly Func<Uri, HttpMessageHandler> _handler;

    public OfficialTokenRefresher(OfficialTokenEndpoints? endpoints = null, Func<Uri, HttpMessageHandler>? handler = null)
    {
        _endpoints = endpoints ?? OfficialTokenEndpoints.Official;
        _handler = handler ?? CreateHandler;
    }

    public async Task<CodexRefreshedTokens> RefreshCodexAsync(string refreshToken, CancellationToken cancellationToken)
    {
        var request = new HttpRequestMessage(HttpMethod.Post, _endpoints.CodexTokenUrl)
        {
            Content = new FormUrlEncodedContent(new Dictionary<string, string>
            {
                ["grant_type"] = "refresh_token",
                ["refresh_token"] = refreshToken,
                ["client_id"] = OfficialTokenEndpoints.CodexClientId,
                ["scope"] = "openid profile email",
            }),
        };
        request.Headers.TryAddWithoutValidation("originator", "codex_cli_rs");
        request.Headers.TryAddWithoutValidation("User-Agent", "codex_cli_rs");

        JsonObject body = await SendAsync(request, "ChatGPT", "codex login", cancellationToken).ConfigureAwait(false);
        string access = JwtPayload.String(body, "access_token");
        if (access.Length == 0)
        {
            throw new LocalProxyCredentialException("刷新本机 ChatGPT 登录时，官方没有返回新的授权，稍后会自动重试。");
        }

        return new CodexRefreshedTokens(access, NullIfEmpty(JwtPayload.String(body, "refresh_token")), NullIfEmpty(JwtPayload.String(body, "id_token")));
    }

    public async Task<ClaudeRefreshedTokens> RefreshClaudeAsync(string refreshToken, CancellationToken cancellationToken)
    {
        var payload = new JsonObject
        {
            ["grant_type"] = "refresh_token",
            ["refresh_token"] = refreshToken,
            ["client_id"] = OfficialTokenEndpoints.ClaudeClientId,
        };
        var request = new HttpRequestMessage(HttpMethod.Post, _endpoints.ClaudeTokenUrl)
        {
            Content = new StringContent(payload.ToJsonString(), Encoding.UTF8, "application/json"),
        };
        request.Headers.TryAddWithoutValidation("Accept", "application/json, text/plain, */*");
        request.Headers.TryAddWithoutValidation("User-Agent", "axios/1.13.6");

        JsonObject body = await SendAsync(request, "Claude", "claude 里执行 /login", cancellationToken).ConfigureAwait(false);
        string access = JwtPayload.String(body, "access_token");
        if (access.Length == 0)
        {
            throw new LocalProxyCredentialException("刷新本机 Claude 登录时，官方没有返回新的授权，稍后会自动重试。");
        }

        long? expiresIn = body["expires_in"] is JsonValue value && value.TryGetValue(out long seconds) ? seconds : null;
        return new ClaudeRefreshedTokens(access, NullIfEmpty(JwtPayload.String(body, "refresh_token")), expiresIn, NullIfEmpty(JwtPayload.String(body, "scope")));
    }

    private async Task<JsonObject> SendAsync(HttpRequestMessage request, string product, string signInHint, CancellationToken cancellationToken)
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
                ClientLog.Warning($"刷新本机 {product} 登录失败：{ex.GetType().Name}");
                throw new LocalProxyCredentialException(
                    $"刷新本机 {product} 登录时连不上 {request.RequestUri!.Host}，请检查代理/VPN 是否开启。开启后下一轮对话会自动重试。", ex);
            }

            using (response)
            {
                string text = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
                int status = (int)response.StatusCode;
                ClientLog.Info($"刷新本机 {product} 登录：HTTP {status}");
                if (!response.IsSuccessStatusCode)
                {
                    throw new LocalProxyCredentialException(IsSignInGone(status, text)
                        ? $"本机 {product} 登录已失效（可能在别处退出或被重新登录过），请先重新登录（{signInHint}），下一轮对话会自动使用新登录。"
                        : $"刷新本机 {product} 登录失败（HTTP {status}），稍后会自动重试。");
                }

                try
                {
                    return JsonNode.Parse(text) as JsonObject
                        ?? throw new LocalProxyCredentialException($"刷新本机 {product} 登录时，官方返回了无法识别的内容，稍后会自动重试。");
                }
                catch (JsonException ex)
                {
                    throw new LocalProxyCredentialException($"刷新本机 {product} 登录时，官方返回了无法识别的内容，稍后会自动重试。", ex);
                }
            }
        }
    }

    /// <summary>
    /// A refresh token that will never work again: used already, revoked, or expired. Retrying
    /// cannot help; the user has to sign in again.
    /// </summary>
    internal static bool IsSignInGone(int status, string body) =>
        status is 400 or 401 or 403 &&
        (body.Contains("invalid_grant", StringComparison.OrdinalIgnoreCase) ||
         body.Contains("refresh_token_reused", StringComparison.OrdinalIgnoreCase) ||
         body.Contains("refresh_token_expired", StringComparison.OrdinalIgnoreCase) ||
         body.Contains("refresh_token_invalidated", StringComparison.OrdinalIgnoreCase) ||
         body.Contains("invalid_refresh_token", StringComparison.OrdinalIgnoreCase));

    private static string? NullIfEmpty(string value) => value.Length == 0 ? null : value;

    private static HttpMessageHandler CreateHandler(Uri target)
    {
        SystemProxy proxy = SystemProxyReader.Current(target);
        return new HttpClientHandler
        {
            AllowAutoRedirect = false,
            AutomaticDecompression = DecompressionMethods.All,
            UseProxy = proxy.Proxy is not null,
            Proxy = proxy.Proxy,
        };
    }
}
