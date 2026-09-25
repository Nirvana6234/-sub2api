using System.Diagnostics;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text.Json;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.WeChatIntent;

internal enum JevFailure
{
    None,

    /// <summary>No key saved on this computer.</summary>
    NoKey,

    /// <summary>401: the key is wrong or was revoked. Pauses the feature.</summary>
    InvalidKey,

    /// <summary>402/403: the account cannot pay or may not use the model. Pauses the feature.</summary>
    Quota,

    /// <summary>429/529 twice: TypeSafe is busy. This judgement is dropped.</summary>
    Busy,

    /// <summary>Another 4xx: the request itself is wrong, which is this client's bug.</summary>
    BadRequest,

    /// <summary>A 5xx other than 529.</summary>
    ServerError,

    /// <summary>No connection, DNS, TLS, proxy.</summary>
    Network,

    Timeout,

    /// <summary>Relay only: the 共飞 session was rejected even after renewing it.</summary>
    SignedOut,

    /// <summary>Relay only: the Jev group is gone, or this account may not use it (403).</summary>
    GroupUnavailable,

    /// <summary>Relay only: the group has no price configured or no working TypeSafe account (503).</summary>
    ServiceUnavailable,
}

/// <summary>A judgement's result: the response, or why there is none.</summary>
internal sealed record JevOutcome(JevResponse? Response, JevFailure Failure, int? Status = null)
{
    /// <summary>The pinned model was gone and <see cref="WeChatIntentQuestions.FallbackModel"/> answered.</summary>
    public bool FellBack { get; init; }

    public bool Succeeded => Response is not null;
}

internal enum JevKeyCheck
{
    Valid,
    Invalid,
    Unreachable,
}

/// <summary>
/// Asks Jev for a judgement. Phase 1 talks to TypeSafe directly with the user's own key
/// (<see cref="DirectJevClient"/>); phase 2 adds a client for the relay's Jev group without
/// touching anything that calls this (docs §5.6).
/// </summary>
internal interface IJevClient
{
    Task<JevOutcome> EvaluateAsync(JevState state, CancellationToken cancellationToken);
}

/// <summary>Calls <c>api.typesafe.ai</c> with the key saved on this computer (docs §5.6).</summary>
/// <remarks>
/// <para>
/// <b>Nothing about the conversation is logged</b>: not the body, not the response, not the
/// headers. The log gets the status, the time taken, the input tokens and the model version.
/// </para>
/// <para>
/// The model falls back to <c>jev-latest</c> only on TypeSafe's "Unknown model" answer — a
/// 400 whose message says so, which is what a retired version returns (measured, and not in
/// their documentation). A 429, a 529 or a timeout never moves the user off the version the
/// thresholds were tuned on.
/// </para>
/// </remarks>
internal sealed class DirectJevClient : IJevClient
{
    public static readonly Uri BaseAddress = new("https://api.typesafe.ai/v1/");
    private static readonly TimeSpan RequestTimeout = TimeSpan.FromSeconds(10);
    private static readonly TimeSpan MaxRetryWait = TimeSpan.FromSeconds(5);

    private readonly Func<string?> _key;
    private readonly Func<HttpMessageHandler> _handler;
    private readonly Func<TimeSpan, CancellationToken, Task> _delay;
    private HttpClient? _http;

    /// <param name="key">Reads the saved key. Called per request, so a changed key applies at once.</param>
    /// <param name="handler">For tests; by default the proxy as it is set now (<see cref="SystemProxyReader"/>).</param>
    /// <param name="delay">For tests; the wait before a retry.</param>
    public DirectJevClient(Func<string?> key, Func<HttpMessageHandler>? handler = null, Func<TimeSpan, CancellationToken, Task>? delay = null)
    {
        _key = key ?? throw new ArgumentNullException(nameof(key));
        _handler = handler ?? CreateHandler;
        _delay = delay ?? Task.Delay;
    }

    public async Task<JevOutcome> EvaluateAsync(JevState state, CancellationToken cancellationToken)
    {
        string? key = _key();
        if (string.IsNullOrWhiteSpace(key))
        {
            return new JevOutcome(null, JevFailure.NoKey);
        }

        JevOutcome outcome = await PostAsync(key, WeChatIntentQuestions.Model, state, cancellationToken).ConfigureAwait(false);
        if (outcome.Failure == JevFailure.BadRequest && outcome.Status == 400 && _lastWasUnknownModel)
        {
            ClientLog.Warning($"Jev 模型 {WeChatIntentQuestions.Model} 已下线，改用 {WeChatIntentQuestions.FallbackModel}");
            outcome = await PostAsync(key, WeChatIntentQuestions.FallbackModel, state, cancellationToken).ConfigureAwait(false);
            return outcome with { FellBack = true };
        }

        return outcome;
    }

    /// <summary>
    /// Checks a key before it is saved, with <c>GET /v1/models</c> — which spends no judgement
    /// tokens. Valid when it answers 200 and lists <c>jev-latest</c>.
    /// </summary>
    public async Task<JevKeyCheck> CheckKeyAsync(string key, CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, new Uri(BaseAddress, "models"));
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", key.Trim());
        try
        {
            using CancellationTokenSource timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(RequestTimeout);
            using HttpResponseMessage response = await Http().SendAsync(request, timeout.Token).ConfigureAwait(false);
            ClientLog.Info($"TypeSafe 验证 key：{(int)response.StatusCode}");
            if (response.StatusCode is HttpStatusCode.Unauthorized or HttpStatusCode.Forbidden)
            {
                return JevKeyCheck.Invalid;
            }

            if (!response.IsSuccessStatusCode)
            {
                return JevKeyCheck.Unreachable;
            }

            byte[] body = await response.Content.ReadAsByteArrayAsync(timeout.Token).ConfigureAwait(false);
            JevModelList? list = JsonSerializer.Deserialize(body, JevJsonContext.Default.JevModelList);
            return list?.Models.Any(m => m.Name.StartsWith("jev", StringComparison.OrdinalIgnoreCase)) == true
                ? JevKeyCheck.Valid
                : JevKeyCheck.Invalid;
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException or JsonException && !cancellationToken.IsCancellationRequested)
        {
            ResetConnection();
            ClientLog.Warning($"TypeSafe 验证 key 失败：{ex.GetType().Name}");
            return JevKeyCheck.Unreachable;
        }
    }

    private bool _lastWasUnknownModel;

    private async Task<JevOutcome> PostAsync(string key, string model, JevState state, CancellationToken cancellationToken)
    {
        _lastWasUnknownModel = false;
        byte[] body = JevRequestBody.Build(model, state);
        for (int attempt = 0; ; attempt++)
        {
            var stopwatch = Stopwatch.StartNew();
            using var request = new HttpRequestMessage(HttpMethod.Post, new Uri(BaseAddress, "systemone"))
            {
                Content = new ByteArrayContent(body),
            };
            request.Content.Headers.ContentType = new MediaTypeHeaderValue("application/json");
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", key.Trim());

            HttpResponseMessage response;
            try
            {
                using CancellationTokenSource timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
                timeout.CancelAfter(RequestTimeout);
                response = await Http().SendAsync(request, timeout.Token).ConfigureAwait(false);
            }
            catch (TaskCanceledException) when (!cancellationToken.IsCancellationRequested)
            {
                ClientLog.Warning($"Jev 请求超时（{stopwatch.ElapsedMilliseconds}ms）");
                return new JevOutcome(null, JevFailure.Timeout);
            }
            catch (HttpRequestException ex)
            {
                // The proxy may have been switched on or off since; the next request reads it again.
                ResetConnection();
                ClientLog.Warning($"Jev 请求失败：{ex.GetType().Name} {ex.HttpRequestError}");
                return new JevOutcome(null, JevFailure.Network);
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
                        ClientLog.Warning($"Jev 返回无法解析（{status}）");
                        return new JevOutcome(null, JevFailure.ServerError, status);
                    }

                    ClientLog.Info($"Jev {status} {stopwatch.ElapsedMilliseconds}ms 输入 {parsed.Usage?.InputTokens ?? 0} token，模型 {parsed.Model}");
                    return new JevOutcome(parsed, JevFailure.None, status);
                }

                ClientLog.Warning($"Jev 返回 {status}（{stopwatch.ElapsedMilliseconds}ms）");
                switch (status)
                {
                    case 401:
                        return new JevOutcome(null, JevFailure.InvalidKey, status);
                    case 402:
                    case 403:
                        return new JevOutcome(null, JevFailure.Quota, status);
                    case 429:
                    case 529:
                        if (attempt == 0)
                        {
                            await _delay(RetryWait(response), cancellationToken).ConfigureAwait(false);
                            continue;
                        }

                        return new JevOutcome(null, JevFailure.Busy, status);
                    case >= 500:
                        return new JevOutcome(null, JevFailure.ServerError, status);
                    default:
                        if (status == 400)
                        {
                            _lastWasUnknownModel = await IsUnknownModelAsync(response, cancellationToken).ConfigureAwait(false);
                        }

                        return new JevOutcome(null, JevFailure.BadRequest, status);
                }
            }
        }
    }

    internal static async Task<bool> IsUnknownModelAsync(HttpResponseMessage response, CancellationToken cancellationToken)
    {
        try
        {
            byte[] content = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
            JevErrorBody? error = JsonSerializer.Deserialize(content, JevJsonContext.Default.JevErrorBody);
            return error?.Detail?.Message?.StartsWith("Unknown model", StringComparison.OrdinalIgnoreCase) == true;
        }
        catch (JsonException)
        {
            return false;
        }
    }

    internal static TimeSpan RetryWait(HttpResponseMessage response)
    {
        TimeSpan? after = response.Headers.RetryAfter?.Delta
            ?? (response.Headers.RetryAfter?.Date is DateTimeOffset at ? at - DateTimeOffset.UtcNow : null);
        if (after is not { } wait || wait <= TimeSpan.Zero)
        {
            return TimeSpan.FromSeconds(1);
        }

        return wait > MaxRetryWait ? MaxRetryWait : wait;
    }

    private HttpClient Http() => _http ??= new HttpClient(_handler()) { Timeout = Timeout.InfiniteTimeSpan };

    private void ResetConnection()
    {
        _http?.Dispose();
        _http = null;
    }

    /// <summary>The proxy as it is set now, never following a redirect — which would carry the key elsewhere.</summary>
    private static HttpMessageHandler CreateHandler()
    {
        SystemProxy proxy = SystemProxyReader.Current(BaseAddress);
        return new HttpClientHandler
        {
            AllowAutoRedirect = false,
            AutomaticDecompression = DecompressionMethods.All,
            UseProxy = proxy.Proxy is not null,
            Proxy = proxy.Proxy,
        };
    }
}
