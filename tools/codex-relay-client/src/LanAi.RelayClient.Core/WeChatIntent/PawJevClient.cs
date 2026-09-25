using System.Diagnostics;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text.Json;
using System.Text.Json.Serialization;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.WeChatIntent;

/// <summary>
/// Asks Jev through the 共飞 relay's Jev group (docs §7.4): <c>POST /api/v1/paw/systemone</c>
/// with the signed-in session and <c>X-Paw-Group-Id</c>. The request body is TypeSafe's, unchanged;
/// so is a successful response. The relay bills the account; the user needs no key of their own.
/// </summary>
/// <remarks>
/// <para>
/// The same requests as <see cref="DirectJevClient"/> — the view model picks one or the other —
/// and the same rules: nothing about the conversation is logged, and the model falls back to
/// <c>jev-latest</c> only on TypeSafe's "Unknown model" answer, which the relay passes through.
/// </para>
/// <para>
/// Like the client's other relay requests it sets no User-Agent: the account session is bound to
/// the network fingerprint it was issued under, and a different one would revoke it. A 401 is
/// handed to the session manager to renew, and the request is tried once more.
/// </para>
/// <para>
/// Always the main server's address, never a relay node's: this route is a control request in the
/// relay-nodes plan (§7.8), answered by the main server whatever share of traffic it takes.
/// </para>
/// </remarks>
internal sealed class PawJevClient : IJevClient
{
    public const string Route = "api/v1/paw/systemone";
    public const string GroupHeader = "X-Paw-Group-Id";
    private static readonly TimeSpan RequestTimeout = TimeSpan.FromSeconds(15);

    private readonly Uri _endpoint;
    private readonly Func<CancellationToken, Task<string>> _accessToken;
    private readonly Func<string, CancellationToken, Task>? _onAccessTokenRejected;
    private readonly Func<long?> _groupId;
    private readonly HttpClient _http;
    private readonly Func<TimeSpan, CancellationToken, Task> _delay;

    /// <param name="serverAddress">The main server, e.g. https://gongfeiai.com/.</param>
    /// <param name="accessToken">The session's current access token (renewed as needed).</param>
    /// <param name="onAccessTokenRejected">Told the exact token the server refused, to renew it.</param>
    /// <param name="groupId">The chosen Jev group, read per request.</param>
    public PawJevClient(
        Uri serverAddress,
        Func<CancellationToken, Task<string>> accessToken,
        Func<string, CancellationToken, Task>? onAccessTokenRejected,
        Func<long?> groupId,
        HttpMessageHandler? handler = null,
        Func<TimeSpan, CancellationToken, Task>? delay = null)
    {
        ArgumentNullException.ThrowIfNull(serverAddress);
        _endpoint = new Uri(serverAddress, Route);
        _accessToken = accessToken ?? throw new ArgumentNullException(nameof(accessToken));
        _onAccessTokenRejected = onAccessTokenRejected;
        _groupId = groupId ?? throw new ArgumentNullException(nameof(groupId));
        _http = new HttpClient(handler ?? new HttpClientHandler { AllowAutoRedirect = false })
        {
            Timeout = Timeout.InfiniteTimeSpan,
        };
        _delay = delay ?? Task.Delay;
    }

    public async Task<JevOutcome> EvaluateAsync(JevState state, CancellationToken cancellationToken)
    {
        if (_groupId() is not long group)
        {
            return new JevOutcome(null, JevFailure.GroupUnavailable);
        }

        (JevOutcome outcome, bool unknownModel) = await PostAsync(WeChatIntentQuestions.Model, state, group, cancellationToken).ConfigureAwait(false);
        if (unknownModel)
        {
            ClientLog.Warning($"Jev 模型 {WeChatIntentQuestions.Model} 已下线，改用 {WeChatIntentQuestions.FallbackModel}（共飞分组）");
            (outcome, _) = await PostAsync(WeChatIntentQuestions.FallbackModel, state, group, cancellationToken).ConfigureAwait(false);
            return outcome with { FellBack = true };
        }

        return outcome;
    }

    private async Task<(JevOutcome Outcome, bool UnknownModel)> PostAsync(string model, JevState state, long group, CancellationToken cancellationToken)
    {
        byte[] body = JevRequestBody.Build(model, state);
        bool renewed = false;
        for (int attempt = 0; ; attempt++)
        {
            string token;
            try
            {
                token = await _accessToken(cancellationToken).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is not OperationCanceledException and not OutOfMemoryException)
            {
                ClientLog.Warning($"Jev（共飞分组）取登录态失败：{ex.GetType().Name}");
                return (new JevOutcome(null, JevFailure.SignedOut), false);
            }

            var stopwatch = Stopwatch.StartNew();
            using var request = new HttpRequestMessage(HttpMethod.Post, _endpoint) { Content = new ByteArrayContent(body) };
            request.Content.Headers.ContentType = new MediaTypeHeaderValue("application/json");
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token);
            request.Headers.Add(GroupHeader, group.ToString(System.Globalization.CultureInfo.InvariantCulture));

            HttpResponseMessage response;
            try
            {
                using CancellationTokenSource timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
                timeout.CancelAfter(RequestTimeout);
                response = await _http.SendAsync(request, timeout.Token).ConfigureAwait(false);
            }
            catch (TaskCanceledException) when (!cancellationToken.IsCancellationRequested)
            {
                ClientLog.Warning($"Jev（共飞分组）请求超时（{stopwatch.ElapsedMilliseconds}ms）");
                return (new JevOutcome(null, JevFailure.Timeout), false);
            }
            catch (HttpRequestException ex)
            {
                ClientLog.Warning($"Jev（共飞分组）请求失败：{ex.GetType().Name} {ex.HttpRequestError}");
                return (new JevOutcome(null, JevFailure.Network), false);
            }

            using (response)
            {
                int status = (int)response.StatusCode;
                if (response.IsSuccessStatusCode)
                {
                    byte[] content = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
                    JevResponse? parsed;
                    try
                    {
                        parsed = JsonSerializer.Deserialize(content, JevJsonContext.Default.JevResponse);
                    }
                    catch (JsonException)
                    {
                        parsed = null;
                    }

                    if (parsed is null)
                    {
                        ClientLog.Warning($"Jev（共飞分组）返回无法解析（{status}）");
                        return (new JevOutcome(null, JevFailure.ServerError, status), false);
                    }

                    ClientLog.Info($"Jev（共飞分组）{status} {stopwatch.ElapsedMilliseconds}ms 输入 {parsed.Usage?.InputTokens ?? 0} token，模型 {parsed.Model}");
                    return (new JevOutcome(parsed, JevFailure.None, status), false);
                }

                string code = await PawErrorCodeAsync(response, cancellationToken).ConfigureAwait(false);
                ClientLog.Warning($"Jev（共飞分组）返回 {status} {code}（{stopwatch.ElapsedMilliseconds}ms）");
                switch (status)
                {
                    case 401 when !renewed && _onAccessTokenRejected is not null:
                        // Revoked before its local expiry: renew with the exact token refused, try once more.
                        renewed = true;
                        try
                        {
                            await _onAccessTokenRejected(token, cancellationToken).ConfigureAwait(false);
                        }
                        catch (Exception ex) when (ex is not OperationCanceledException and not OutOfMemoryException)
                        {
                            ClientLog.Warning($"Jev（共飞分组）续期登录态失败：{ex.GetType().Name}");
                            return (new JevOutcome(null, JevFailure.SignedOut, status), false);
                        }

                        continue;
                    case 401:
                        return (new JevOutcome(null, JevFailure.SignedOut, status), false);
                    case 402:
                        return (new JevOutcome(null, JevFailure.Quota, status), false);

                    // Two different 403s, told apart only by the code: BILLING_ERROR is the
                    // account's balance or subscription, GROUP_FORBIDDEN the group itself.
                    case 403 when code == "BILLING_ERROR":
                        return (new JevOutcome(null, JevFailure.Quota, status), false);
                    case 403:
                        return (new JevOutcome(null, JevFailure.GroupUnavailable, status), false);

                    // The group's model allowlist lacks the model: an admin setting, not a passing fault.
                    case 404:
                        return (new JevOutcome(null, JevFailure.GroupUnavailable, status), false);

                    // QUOTA_EXCEEDED: the internal key's quota. RATE_LIMIT_EXCEEDED and
                    // UPSTREAM_BUSY are windows that pass, and are retried once after Retry-After.
                    case 429 when IsQuotaCode(code):
                        return (new JevOutcome(null, JevFailure.Quota, status), false);
                    case 429:
                    case 529:
                        if (attempt == 0)
                        {
                            await _delay(DirectJevClient.RetryWait(response), cancellationToken).ConfigureAwait(false);
                            continue;
                        }

                        return (new JevOutcome(null, JevFailure.Busy, status), false);
                    case 503:
                        return (new JevOutcome(null, JevFailure.ServiceUnavailable, status), false);
                    case >= 500:
                        return (new JevOutcome(null, JevFailure.ServerError, status), false);
                    default:
                        bool unknownModel = status == 400 && await DirectJevClient.IsUnknownModelAsync(response, cancellationToken).ConfigureAwait(false);
                        return (new JevOutcome(null, JevFailure.BadRequest, status), unknownModel && model != WeChatIntentQuestions.FallbackModel);
                }
            }
        }
    }

    /// <summary>Balance or quota exhausted, as the relay's own error codes put it.</summary>
    private static bool IsQuotaCode(string code) =>
        code.Contains("QUOTA", StringComparison.OrdinalIgnoreCase) || code.Contains("BALANCE", StringComparison.OrdinalIgnoreCase);

    /// <summary>
    /// The relay's own error code, or empty. The relay answers in three shapes (agreed with the
    /// server side, 2026-09-25): paw's <c>{"error":{"code","message"}}</c> for its checks, the
    /// gateway's <c>{"error":{"type","code","message"}}</c> for forwarding and billing, and the
    /// model allowlist's OpenAI-style 404 without a code. TypeSafe's own 4xx pass through as
    /// <c>{"detail":{…}}</c>, which carries no code either.
    /// </summary>
    private static async Task<string> PawErrorCodeAsync(HttpResponseMessage response, CancellationToken cancellationToken)
    {
        try
        {
            byte[] content = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
            return JsonSerializer.Deserialize(content, PawJevJsonContext.Default.PawErrorEnvelope)?.Error?.Code ?? string.Empty;
        }
        catch (JsonException)
        {
            return string.Empty;
        }
    }
}

/// <summary>The relay's error body: <c>{"error":{"code":"GROUP_FORBIDDEN","message":"…"}}</c>.</summary>
internal sealed record PawErrorEnvelope
{
    [JsonPropertyName("error")]
    public PawErrorBody? Error { get; init; }
}

internal sealed record PawErrorBody
{
    [JsonPropertyName("code")]
    public string? Code { get; init; }

    [JsonPropertyName("message")]
    public string? Message { get; init; }
}

[JsonSerializable(typeof(PawErrorEnvelope))]
internal sealed partial class PawJevJsonContext : JsonSerializerContext;
