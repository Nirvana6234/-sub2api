using System.IO;
using System.Buffers;
using System.Net;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// A loopback-only Responses relay: the only network address Codex ever sees.
/// </summary>
/// <remarks>
/// <para>
/// Codex is handed <c>http://127.0.0.1:&lt;random port&gt;/v1</c> and a random local
/// token. The token still lands in <c>auth.json</c> in the clear — that cannot be
/// helped, Codex owns that file — but it is worthless off this machine, and the
/// account session (the JWT) never enters Codex's address space at all.
/// </para>
/// <para>
/// <b>Loopback is not private.</b> Every process on this machine, and every page in
/// the user's browser, can reach this port. They cannot read the response (no CORS
/// headers are sent), but a request that merely goes out has already spent the
/// user's balance. Hence the guards in <see cref="Reject"/> — the token alone is
/// not the whole defence.
/// </para>
/// </remarks>
internal sealed class LocalPawRelay : IAsyncDisposable
{
    /// <summary>The single path Codex calls: <c>base_url</c> is <c>.../v1</c> and it appends this.</summary>
    private const string CodexPath = "/v1/responses";

    /// <summary>
    /// The account-session route on the server.
    /// </summary>
    /// <remarks>
    /// This constant and <see cref="GroupHeader"/> are a third copy of a wire contract
    /// that also exists in Go (<c>backend/internal/server/routes/paw.go</c>) and Rust
    /// (<c>codex-host/src/proxy.rs</c>). Renaming one side leaves every test green and
    /// the product 404ing, so the literals are pinned by a test here too.
    /// </remarks>
    private const string UpstreamPath = "/api/v1/paw/responses";

    /// <summary>The group travels as a header because the body must stay a verbatim Responses payload.</summary>
    internal const string GroupHeader = "X-Paw-Group-Id";

    private readonly HttpListener _listener = new();
    private readonly HttpClient _http;
    private readonly bool _ownsHttp;
    private readonly string _upstream;
    private readonly Func<CancellationToken, Task<string>> _accessToken;
    private readonly object _gate = new();
    private long? _groupId;
    private int _port;
    private CancellationTokenSource? _stop;
    private Task? _serve;
    private bool _disposed;

    /// <param name="accessTokenProvider">
    /// Asked for the account session on <em>every</em> forwarded request rather than
    /// once at startup. Access tokens rotate (see <c>RelaySessionManager</c>), and a
    /// snapshot taken when Codex launched is dead within the hour — at which point
    /// every Codex request 401s while the dashboard, which re-fetches on each poll,
    /// still looks perfectly healthy. Pulling also means signing out takes effect
    /// here immediately, with no extra plumbing.
    /// </param>
    public LocalPawRelay(
        string upstreamBaseUrl,
        Func<CancellationToken, Task<string>> accessTokenProvider,
        HttpClient? http = null)
    {
        if (!Uri.TryCreate(upstreamBaseUrl, UriKind.Absolute, out Uri? uri) ||
            uri.Scheme is not ("http" or "https"))
            throw new ArgumentException("upstreamBaseUrl must be an absolute HTTP URL", nameof(upstreamBaseUrl));
        _upstream = upstreamBaseUrl.TrimEnd('/');
        _accessToken = accessTokenProvider ?? throw new ArgumentNullException(nameof(accessTokenProvider));
        _ownsHttp = http is null;
        _http = http ?? new HttpClient(
            // Never follow a redirect: that would carry the account session to
            // whatever host the redirect names.
            new HttpClientHandler { AllowAutoRedirect = false })
        {
            // No timeout: a single Responses turn legitimately streams for minutes.
            Timeout = Timeout.InfiniteTimeSpan,
        };
        Token = CreateToken();
    }

    /// <summary>The "API key" handed to Codex — meaningless anywhere but this machine.</summary>
    public string Token { get; }

    public Uri? BaseAddress { get; private set; }

    /// <summary>
    /// Binds forwarded traffic to a billing group.
    /// </summary>
    /// <remarks>
    /// Pushed rather than pulled, because the group is a decision the user makes in
    /// the dashboard and nothing else can derive it. It must be pushed again on every
    /// switch: a relay left on the previous group bills traffic somewhere the user
    /// was told it would not go, and neither end says a word about it.
    /// </remarks>
    public void SetGroup(long? groupId)
    {
        lock (_gate) { _groupId = groupId; }
    }

    public Task StartAsync(CancellationToken cancellationToken = default)
    {
        if (_disposed) throw new ObjectDisposedException(nameof(LocalPawRelay));
        if (_listener.IsListening) return Task.CompletedTask;
        _serve = null;
        BaseAddress = null;
        _stop = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        // HttpListener cannot bind port 0 portably; reserve an OS port first.
        using var probe = new System.Net.Sockets.TcpListener(IPAddress.Loopback, 0);
        probe.Start();
        int port = ((IPEndPoint)probe.LocalEndpoint).Port;
        probe.Stop();
        _port = port;
        BaseAddress = new Uri($"http://127.0.0.1:{port}/v1/");
        _listener.Prefixes.Clear();
        _listener.Prefixes.Add($"http://127.0.0.1:{port}/");
        _listener.Start();
        _serve = ServeAsync(_stop.Token);
        return Task.CompletedTask;
    }

    private async Task ServeAsync(CancellationToken cancellationToken)
    {
        while (!cancellationToken.IsCancellationRequested)
        {
            HttpListenerContext context;
            try { context = await _listener.GetContextAsync().WaitAsync(cancellationToken); }
            catch (OperationCanceledException) { break; }
            catch (HttpListenerException) when (cancellationToken.IsCancellationRequested) { break; }
            catch (ObjectDisposedException) { break; }
            _ = HandleAsync(context, cancellationToken);
        }
    }

    /// <summary>Why a request was turned away, or null when it may proceed.</summary>
    /// <remarks>
    /// Split out from the IO so each gate can be tested on its own. Every one of them
    /// blocks a request that would otherwise work — which is exactly why none of them
    /// would be noticed if it silently stopped doing its job.
    /// </remarks>
    internal static (int Status, string Message)? Reject(
        string method,
        string? path,
        string? host,
        string? origin,
        string? authorization,
        string expectedToken,
        int port)
    {
        if (method != "POST" || path != CodexPath)
            return (404, "no such endpoint");

        // Codex never sends Origin (measured), so its presence means a web page is
        // calling. The page cannot read the answer, but the request alone already
        // costs the user money. Presence alone is the test — an empty Origin is still
        // an Origin, and treating it as absent would be a quieter gate than the Rust
        // implementation this mirrors.
        if (origin is not null)
            return (403, "browser-originated request refused");

        // Blocks DNS rebinding: a name that first resolves to a real address and then
        // to 127.0.0.1 walks straight past the browser's same-origin rule.
        if (!string.Equals(host, $"127.0.0.1:{port}", StringComparison.Ordinal))
            return (403, "unexpected Host header");

        string presented = authorization?.StartsWith("Bearer ", StringComparison.OrdinalIgnoreCase) == true
            ? authorization["Bearer ".Length..].Trim()
            : string.Empty;
        if (!FixedTimeEquals(presented, expectedToken))
            return (401, "invalid local relay token");

        return null;
    }

    /// <summary>
    /// One line saying what the context filter did to this request, from the headers
    /// it stamps on what it forwards.
    /// </summary>
    /// <remarks>
    /// <para>
    /// The switch's own log line only says what the client <em>asked</em> for. This
    /// says what actually reached the wire, which is the question worth being able to
    /// answer later: a filter that is running but not compressing, and a filter that
    /// is not in the chain at all, both look identical from the settings screen.
    /// </para>
    /// <para>
    /// Measured on the shipped binary: with compression on it stamps all five headers;
    /// with compression off it stamps <em>none</em>. So their absence is itself the
    /// signal, and it cannot distinguish "filter off" from "no filter in the chain" —
    /// the startup line above is what separates those two.
    /// </para>
    /// </remarks>
    internal static string DescribeContextFilter(
        string? enabled,
        string? changed,
        string? bytesBefore,
        string? bytesAfter,
        string? bytesSaved)
    {
        if (enabled is null && bytesBefore is null && bytesAfter is null)
        {
            return "本轮未经过上下文压缩";
        }

        if (!string.Equals(enabled, "true", StringComparison.OrdinalIgnoreCase))
        {
            return "本轮经过 Context Filter，但压缩未启用";
        }

        bool parsedBefore = long.TryParse(bytesBefore, out long before);
        bool parsedAfter = long.TryParse(bytesAfter, out long after);
        if (!parsedBefore || !parsedAfter)
        {
            return "本轮上下文压缩已启用（过滤器未报告大小）";
        }

        if (!long.TryParse(bytesSaved, out long saved))
        {
            saved = before - after;
        }

        if (saved <= 0 || string.Equals(changed, "false", StringComparison.OrdinalIgnoreCase))
        {
            // Enabled and did nothing is a normal outcome on a short turn, not a fault.
            return $"本轮上下文压缩已启用，未压缩（{Bytes(before)}，无可压缩内容）";
        }

        double ratio = before > 0 ? (double)saved / before * 100 : 0;
        return $"本轮上下文压缩生效：{Bytes(before)} → {Bytes(after)}（省 {Bytes(saved)}，{ratio:F1}%）";
    }

    /// <summary>
    /// Whether this failure is just the caller hanging up rather than something wrong.
    /// </summary>
    /// <remarks>
    /// Windows reports a peer that disappeared mid-write as an <see cref="HttpListenerException"/>
    /// with 1229 (WSAENOTCONN, "操作在不存在的网络连接上") or 64 (ERROR_NETNAME_DELETED,
    /// "指定的网络名不再可用"). Measured in a real session: pressing stop in ChatGPT
    /// produces exactly these, and they were being logged as warnings complete with a
    /// stack trace.
    /// </remarks>
    internal static bool ClientWentAway(Exception exception) => exception switch
    {
        HttpListenerException { ErrorCode: 1229 or 64 } => true,
        ObjectDisposedException => true,
        IOException { InnerException: HttpListenerException { ErrorCode: 1229 or 64 } } => true,
        _ => false,
    };

    /// <summary>A one-line, length-capped form of an upstream error body for the log.</summary>
    /// <remarks>
    /// Capped because the log is something a user is asked to send in for support, and an
    /// unbounded upstream body would be both unreadable and a place for content to leak
    /// into it. The backend's refusals are short JSON envelopes, so the cap rarely bites.
    /// </remarks>
    internal static string Summarize(string? detail)
    {
        if (string.IsNullOrWhiteSpace(detail)) return "(无响应内容)";
        string flat = detail.ReplaceLineEndings(" ").Trim();
        return flat.Length <= 300 ? flat : flat[..300] + "…";
    }

    private static string Bytes(long value) => value switch
    {
        >= 1024 * 1024 => $"{value / 1024d / 1024d:F1} MB",
        >= 1024 => $"{value / 1024d:F1} KB",
        _ => $"{value} B",
    };

    private async Task HandleAsync(HttpListenerContext context, CancellationToken cancellationToken)
    {
        bool responseStarted = false;
        try
        {
            if (Reject(
                    context.Request.HttpMethod,
                    context.Request.Url?.AbsolutePath,
                    context.Request.Headers["Host"],
                    context.Request.Headers["Origin"],
                    context.Request.Headers["Authorization"],
                    Token,
                    _port) is { } rejection)
            {
                await WriteErrorAsync(context, rejection.Status, rejection.Message).ConfigureAwait(false);
                return;
            }

            long? group;
            lock (_gate) { group = _groupId; }
            if (group is null)
            {
                await WriteErrorAsync(context, 400, "no group is bound to this client").ConfigureAwait(false);
                return;
            }

            string jwt;
            try
            {
                jwt = await _accessToken(cancellationToken).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is RelayApiException or InvalidOperationException)
            {
                ClientLog.Warning("本机转发取账号会话失败", ex);
                await WriteErrorAsync(context, 401, "not signed in: no account session available").ConfigureAwait(false);
                return;
            }

            // Logged per request, not per session: this is the only place that can see
            // whether compression actually happened, and one line per model turn is a
            // volume the log can carry.
            string filterWarnings = context.Request.Headers["X-Context-Filter-Warnings"] ?? string.Empty;
            ClientLog.Info($"本轮转发（分组 {group}）：" + DescribeContextFilter(
                context.Request.Headers["X-Context-Filter-Enabled"],
                context.Request.Headers["X-Context-Filter-Changed"],
                context.Request.Headers["X-Context-Filter-Bytes-Before"],
                context.Request.Headers["X-Context-Filter-Bytes-After"],
                context.Request.Headers["X-Context-Filter-Bytes-Saved"])
                + (filterWarnings.Length > 0 ? $"（过滤器提示：{filterWarnings}）" : string.Empty));

            byte[] body;
            using (var buffer = new MemoryStream())
            {
                await context.Request.InputStream.CopyToAsync(buffer, cancellationToken).ConfigureAwait(false);
                body = buffer.ToArray();
            }

            // A whitelist, not a blacklist. Codex's own request carries installation_id,
            // window_id and friends that mean nothing upstream; building a fresh request
            // means never having to decide, header by header, which ones may travel.
            using var request = new HttpRequestMessage(HttpMethod.Post, _upstream + UpstreamPath)
            {
                Content = new ByteArrayContent(body),
            };
            request.Content.Headers.ContentType =
                MediaTypeHeaderValue.Parse(context.Request.ContentType ?? "application/json");
            // Replaced, not appended: forwarding Codex's local token upstream is the
            // easiest mistake to make on this path.
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", jwt);
            request.Headers.Add(GroupHeader, group.Value.ToString(System.Globalization.CultureInfo.InvariantCulture));
            request.Headers.Accept.ParseAdd("text/event-stream");

            using HttpResponseMessage response = await _http
                .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                .ConfigureAwait(false);

            if (!response.IsSuccessStatusCode)
            {
                // Buffered, logged and passed through. Only errors are buffered: they are
                // small and never streamed, whereas a success is a live SSE body that must
                // not be held. Without this an upstream refusal — a rejected group, an
                // expired session, a 502 — reached Codex and left nothing whatsoever in
                // the log, so "Codex stopped working" had no visible cause on this side.
                string detail = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
                ClientLog.Warning($"中转拒绝本轮请求：HTTP {(int)response.StatusCode} {Summarize(detail)}");

                byte[] payload = Encoding.UTF8.GetBytes(detail);
                context.Response.StatusCode = (int)response.StatusCode;
                context.Response.ContentType =
                    response.Content.Headers.ContentType?.ToString() ?? "application/json";
                context.Response.ContentLength64 = payload.Length;
                responseStarted = true;
                await context.Response.OutputStream.WriteAsync(payload, cancellationToken).ConfigureAwait(false);
                return;
            }

            context.Response.StatusCode = (int)response.StatusCode;
            context.Response.ContentType =
                response.Content.Headers.ContentType?.ToString() ?? "text/event-stream";
            // Chunked, and flushed per read: buffering the stream would break two
            // things at once — the answer stops appearing word by word, and the stop
            // button does not take effect until the turn has finished anyway.
            context.Response.SendChunked = true;
            responseStarted = true;

            await using Stream upstream = await response.Content
                .ReadAsStreamAsync(cancellationToken)
                .ConfigureAwait(false);
            byte[] chunk = ArrayPool<byte>.Shared.Rent(16 * 1024);
            try
            {
                int read;
                while ((read = await upstream.ReadAsync(chunk, cancellationToken).ConfigureAwait(false)) > 0)
                {
                    await context.Response.OutputStream
                        .WriteAsync(chunk.AsMemory(0, read), cancellationToken)
                        .ConfigureAwait(false);
                    await context.Response.OutputStream.FlushAsync(cancellationToken).ConfigureAwait(false);
                }
            }
            finally
            {
                ArrayPool<byte>.Shared.Return(chunk);
            }
        }
        catch (Exception ex)
        {
            // Once bytes are on the wire the status is already sent; all that is left
            // is to drop the connection so Codex sees a truncated stream rather than
            // a response that looks complete.
            if (!responseStarted)
            {
                try
                {
                    await WriteErrorAsync(context, 502, "relay unreachable: " + ex.Message).ConfigureAwait(false);
                }
                catch (Exception inner) when (inner is HttpListenerException or ObjectDisposedException or IOException)
                {
                }
            }

            if (ClientWentAway(ex))
            {
                // Codex hung up mid-answer: the user pressed stop, closed the thread, or
                // the app abandoned the turn. Normal, and frequent. Logged as one quiet
                // line because a stack trace here trains the reader to skim past
                // warnings — which is exactly what the removed CDP overlay used to do.
                ClientLog.Info("本轮被 ChatGPT 中断（连接已断开）");
            }
            else if (ex is not OperationCanceledException)
            {
                ClientLog.Warning("本机转发请求失败", ex);
            }
        }
        finally
        {
            try { context.Response.Close(); }
            catch (Exception ex) when (ex is HttpListenerException or ObjectDisposedException or IOException) { }
        }
    }

    /// <remarks>
    /// Shaped like an OpenAI error because Codex recognises that shape and files it as
    /// an upstream failure. A bare status code with an empty body surfaces to the user
    /// as an unexplained protocol error instead.
    /// </remarks>
    private static async Task WriteErrorAsync(HttpListenerContext context, int status, string message)
    {
        // Written field by field rather than serialized from an object: this assembly
        // is trim-analysed, and reflection-based serialization is an IL2026 error here.
        using var buffer = new MemoryStream();
        using (var writer = new System.Text.Json.Utf8JsonWriter(buffer))
        {
            writer.WriteStartObject();
            writer.WriteStartObject("error");
            writer.WriteString("message", message);
            writer.WriteString("type", "cofly_local_relay");
            writer.WriteEndObject();
            writer.WriteEndObject();
        }

        byte[] payload = buffer.ToArray();
        context.Response.StatusCode = status;
        context.Response.ContentType = "application/json";
        context.Response.ContentLength64 = payload.Length;
        await context.Response.OutputStream.WriteAsync(payload).ConfigureAwait(false);
    }

    public async ValueTask DisposeAsync()
    {
        if (_disposed) return;
        _disposed = true;
        await StopAsync().ConfigureAwait(false);
        _listener.Close();
        if (_ownsHttp) _http.Dispose();
    }

    /// <summary>
    /// Stops the current session while keeping the relay reusable for a later
    /// sign-in in the same client process.
    /// </summary>
    public async Task StopAsync()
    {
        _stop?.Cancel();
        if (_listener.IsListening) _listener.Stop();
        if (_serve is not null) { try { await _serve.ConfigureAwait(false); } catch (OperationCanceledException) { } }
        _stop?.Dispose();
        _stop = null;
        _serve = null;
        BaseAddress = null;
        SetGroup(null);
    }

    private static string CreateToken()
    {
        Span<byte> bytes = stackalloc byte[32];
        RandomNumberGenerator.Fill(bytes);
        return Convert.ToHexString(bytes).ToLowerInvariant();
    }

    /// <remarks>
    /// The length is not a secret (it is fixed and public), so returning early on a
    /// length mismatch is fine; the byte comparison itself must not leak which byte
    /// differed.
    /// </remarks>
    private static bool FixedTimeEquals(string presented, string expected) =>
        CryptographicOperations.FixedTimeEquals(
            Encoding.UTF8.GetBytes(presented),
            Encoding.UTF8.GetBytes(expected));
}
