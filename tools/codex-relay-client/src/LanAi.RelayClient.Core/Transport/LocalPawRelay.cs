using System.IO;
using System.Buffers;
using System.Net;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>Which wire protocol a client speaks to the relay.</summary>
internal enum RelayProtocol
{
    /// <summary>OpenAI Responses — Codex.</summary>
    Responses,

    /// <summary>Anthropic Messages — Claude Code and the editor extensions built on it.</summary>
    Messages,
}

/// <summary>One path the relay serves, and where it goes upstream.</summary>
internal sealed record RelayRoute(string ClientPath, string UpstreamPath, RelayProtocol Protocol);

/// <summary>
/// A loopback-only relay: the only network address Codex, and Claude Code with it, ever see.
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

    /// <summary>
    /// Carries the editor's own User-Agent to the server on the Messages routes.
    /// </summary>
    /// <remarks>
    /// Another copy of a wire contract that lives in Go too
    /// (<c>PawClientUserAgentHeader</c> in <c>paw.go</c>). It has to be a separate header
    /// because the account session is bound to the fingerprint it was issued under —
    /// IP and User-Agent together — and the server revokes the whole session family on
    /// a mismatch. The relay's own requests keep one User-Agent for that reason, so the
    /// editor's cannot be the request's; the server puts it back after that check, where
    /// groups restricted to Claude Code look for it.
    /// </remarks>
    internal const string ClientUserAgentHeader = "X-Paw-Client-User-Agent";

    /// <summary>
    /// Everything the relay serves. Anything else is refused: a path that is not listed
    /// is one nothing was written to handle.
    /// </summary>
    private static readonly RelayRoute[] Routes =
    [
        new(CodexPath, UpstreamPath, RelayProtocol.Responses),
        new("/v1/messages", "/api/v1/paw/messages", RelayProtocol.Messages),
        new("/v1/messages/count_tokens", "/api/v1/paw/messages/count_tokens", RelayProtocol.Messages),
    ];

    /// <summary>
    /// The request headers a Messages call may carry upstream, beyond content type.
    /// </summary>
    /// <remarks>
    /// A whitelist for the same reason as the Responses one. These are the ones the
    /// server reads: <c>anthropic-version</c> and <c>anthropic-beta</c> select the API
    /// behaviour, and <c>x-app</c> and the session id feed the Claude Code check and
    /// session stickiness. The server builds its own headers for the real upstream, so
    /// the SDK's x-stainless-* set is deliberately not forwarded.
    /// </remarks>
    private static readonly string[] MessagesForwardedRequestHeaders =
        ["anthropic-version", "anthropic-beta", "x-app", "x-claude-code-session-id"];

    internal static RelayRoute? FindRoute(string? path) =>
        Routes.FirstOrDefault(route => string.Equals(route.ClientPath, path, StringComparison.Ordinal));

    private readonly HttpListener _listener = new();
    private readonly HttpClient _http;
    private readonly bool _ownsHttp;
    private readonly string _upstream;
    private readonly Func<CancellationToken, Task<string>> _accessToken;
    private readonly Func<string, CancellationToken, Task>? _onAccessTokenRejected;

    /// <summary>
    /// Told what one request's filter measurement was, whenever there was one to
    /// report. Not the same question as the log line: this is for a running total
    /// (see <c>ContextFilterUsageStore</c>) rather than for a human reading one line
    /// at a time, so it fires only when there are real numbers to add — never for a
    /// disabled or absent filter, where there is nothing to accumulate.
    /// </summary>
    private readonly Action<long, long>? _onCompressionMeasured;
    private readonly IRelayEndpointStore? _endpointStore;
    private readonly int? _preferredPort;
    private readonly object _gate = new();
    private long? _codexGroupId;
    private string? _codexGroupName;
    private long? _claudeGroupId;
    private string? _claudeGroupName;
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
        Action<long, long>? onCompressionMeasured = null,
        HttpClient? http = null,
        Func<string, CancellationToken, Task>? onAccessTokenRejected = null,
        IRelayEndpointStore? endpointStore = null)
    {
        if (!Uri.TryCreate(upstreamBaseUrl, UriKind.Absolute, out Uri? uri) ||
            uri.Scheme is not ("http" or "https"))
            throw new ArgumentException("upstreamBaseUrl must be an absolute HTTP URL", nameof(upstreamBaseUrl));
        _upstream = upstreamBaseUrl.TrimEnd('/');
        _accessToken = accessTokenProvider ?? throw new ArgumentNullException(nameof(accessTokenProvider));
        _onCompressionMeasured = onCompressionMeasured;
        _onAccessTokenRejected = onAccessTokenRejected;
        _ownsHttp = http is null;
        _http = http ?? new HttpClient(
            // Never follow a redirect: that would carry the account session to
            // whatever host the redirect names.
            new HttpClientHandler { AllowAutoRedirect = false })
        {
            // No timeout: a single Responses turn legitimately streams for minutes.
            Timeout = Timeout.InfiniteTimeSpan,
        };

        // The port and token from last time, when there is somewhere they were kept. An
        // editor's configuration outlives this process, so both have to survive it; see
        // RelayEndpointStore. A remembered token that is not the shape this class
        // generates is not trusted: it is compared against every request, and one that
        // was edited down to something short would quietly weaken that.
        _endpointStore = endpointStore;
        RelayEndpoint remembered = endpointStore?.Load() ?? new RelayEndpoint(null, null);
        _preferredPort = remembered.Port is > 1023 and <= 65535 ? remembered.Port : null;
        Token = IsWellFormedToken(remembered.Token) ? remembered.Token! : CreateToken();
    }

    /// <summary>The "API key" handed to Codex — meaningless anywhere but this machine.</summary>
    public string Token { get; }

    public Uri? BaseAddress { get; private set; }

    /// <summary>
    /// The scheme, host and port alone — no <c>/v1</c>. What Anthropic clients want as
    /// their base URL: they append <c>/v1/messages</c> themselves, and a base that
    /// already ends in <c>/v1</c> would produce <c>/v1/v1/messages</c>.
    /// </summary>
    public string? Origin => BaseAddress is null ? null : $"http://127.0.0.1:{_port}";

    /// <summary>
    /// Binds Codex's (<c>/v1/responses</c>) forwarded traffic to a billing group.
    /// </summary>
    /// <remarks>
    /// Pushed rather than pulled, because the group is a decision the user makes in
    /// the dashboard and nothing else can derive it. It must be pushed again on every
    /// switch: a relay left on the previous group bills traffic somewhere the user
    /// was told it would not go, and neither end says a word about it.
    /// </remarks>
    /// <remarks>
    /// Independent of <see cref="SetClaudeGroup"/>: Codex and the editor plug-ins are
    /// two callers of the same relay, on two different routes, and each names its own
    /// group. Sharing one slot between them would re-bill whichever caller set it last
    /// to a group it never chose.
    /// </remarks>
    public void SetGroup(long? groupId, string? groupName = null)
    {
        lock (_gate)
        {
            _codexGroupId = groupId;
            _codexGroupName = string.IsNullOrWhiteSpace(groupName) ? null : groupName.Trim();
        }

        ClientLog.Info(groupId is null
            ? "本机 Relay（ChatGPT）已切换到自动分组"
            : $"本机 Relay（ChatGPT）已切换{FormatGroup(groupId, groupName)}");
    }

    /// <summary>
    /// Binds Claude Code's (<c>/v1/messages</c>) forwarded traffic to a billing group.
    /// </summary>
    /// <remarks>See <see cref="SetGroup"/> for why this is a slot of its own.</remarks>
    public void SetClaudeGroup(long? groupId, string? groupName = null)
    {
        lock (_gate)
        {
            _claudeGroupId = groupId;
            _claudeGroupName = string.IsNullOrWhiteSpace(groupName) ? null : groupName.Trim();
        }

        ClientLog.Info(groupId is null
            ? "本机 Relay（Claude Code）已清除分组"
            : $"本机 Relay（Claude Code）已切换{FormatGroup(groupId, groupName)}");
    }

    public Task StartAsync(CancellationToken cancellationToken = default)
    {
        if (_disposed) throw new ObjectDisposedException(nameof(LocalPawRelay));
        if (_listener.IsListening) return Task.CompletedTask;
        _serve = null;
        BaseAddress = null;
        _stop = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        int port = ReservePort();
        _port = port;
        BaseAddress = new Uri($"http://127.0.0.1:{port}/v1/");
        _listener.Prefixes.Clear();
        _listener.Prefixes.Add($"http://127.0.0.1:{port}/");
        _listener.Start();
        _serve = ServeAsync(_stop.Token);

        // Kept only once it is actually bound, so what is remembered is what worked.
        _endpointStore?.Save(port, Token);
        return Task.CompletedTask;
    }

    /// <summary>
    /// The remembered port if it is free, otherwise any free one.
    /// </summary>
    /// <remarks>
    /// Falling back is the normal outcome when something else took the port, not a
    /// failure: the relay starts on a different one, and whoever holds a configuration
    /// naming the old port is told through <see cref="Origin"/> and rewrites it.
    /// </remarks>
    private int ReservePort()
    {
        if (_preferredPort is int preferred && TryReserve(preferred, out int reserved))
        {
            return reserved;
        }

        // HttpListener cannot bind port 0 portably; reserve an OS port first.
        return TryReserve(0, out int any)
            ? any
            : throw new InvalidOperationException("No free loopback port is available for the local relay.");
    }

    private static bool TryReserve(int port, out int bound)
    {
        try
        {
            using var probe = new System.Net.Sockets.TcpListener(IPAddress.Loopback, port);
            probe.Start();
            bound = ((IPEndPoint)probe.LocalEndpoint).Port;
            probe.Stop();
            return true;
        }
        catch (System.Net.Sockets.SocketException)
        {
            bound = 0;
            return false;
        }
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
        if (method != "POST" || FindRoute(path) is null)
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

        if (TryParseFilterMetrics(enabled, bytesBefore, bytesAfter, bytesSaved) is not (long before, long after, long saved))
        {
            return "本轮上下文压缩已启用（过滤器未报告大小）";
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
    /// Pulls (before, after, saved) out of the filter's headers, or null when there is
    /// nothing usable — the filter is absent, disabled, or the numbers do not parse.
    /// </summary>
    /// <remarks>
    /// The one place this logic lives. <see cref="DescribeContextFilter"/> uses it to
    /// decide what to print; <c>HandleAsync</c> uses it to decide what to add to the
    /// running total in <c>ContextFilterUsageStore</c>. Duplicating the parsing between
    /// the two would let them drift — one counting a request the other does not.
    /// </remarks>
    internal static (long Before, long After, long Saved)? TryParseFilterMetrics(
        string? enabled,
        string? bytesBefore,
        string? bytesAfter,
        string? bytesSaved)
    {
        if (!string.Equals(enabled, "true", StringComparison.OrdinalIgnoreCase))
        {
            return null;
        }

        if (!long.TryParse(bytesBefore, out long before))
        {
            return null;
        }

        bool haveAfter = long.TryParse(bytesAfter, out long after);
        bool haveSaved = long.TryParse(bytesSaved, out long saved);
        if (!haveAfter && !haveSaved)
        {
            return null;
        }

        if (!haveSaved)
        {
            saved = before - after;
        }
        else if (!haveAfter)
        {
            after = before - saved;
        }

        return (before, after, Math.Max(saved, 0));
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

        // Known before anything can go wrong, because how a failure is worded depends on
        // who asked: Codex and Claude Code each parse a different error envelope. A path
        // no route matches is answered in Codex's, which is the one this class started with.
        RelayRoute? route = FindRoute(context.Request.Url?.AbsolutePath);
        RelayProtocol protocol = route?.Protocol ?? RelayProtocol.Responses;
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
                // Refusals were silent until now, which is why "does Codex ever ask us
                // for /v1/models?" had no answer: every path but POST /v1/responses is
                // turned away without a trace. The query string is kept because that is
                // where Codex puts client_version when it refreshes its model picker.
                // This also gives the security gates above a voice — a browser probing
                // the port used to leave nothing behind either.
                ClientLog.Info(
                    $"本机 Relay 拒绝 {Sanitize(context.Request.HttpMethod)} " +
                    $"{Sanitize(context.Request.Url?.PathAndQuery)}" +
                    $"（HTTP {rejection.Status} {rejection.Message}）");
                await WriteErrorAsync(context, rejection.Status, rejection.Message, protocol).ConfigureAwait(false);
                return;
            }

            // Reject has passed, and it refuses any path FindRoute does not know, so this is
            // never null. Stated as a value rather than assumed with a bang so the two
            // places below cannot drift from that.
            RelayRoute served = route ?? throw new InvalidOperationException("Reject let an unrouted path through.");

            long? group;
            string? groupName;
            lock (_gate)
            {
                if (protocol == RelayProtocol.Messages)
                {
                    group = _claudeGroupId;
                    groupName = _claudeGroupName;
                }
                else
                {
                    group = _codexGroupId;
                    groupName = _codexGroupName;
                }
            }
            if (protocol == RelayProtocol.Messages && group is null)
            {
                // The Claude Code binding is its own selection, independent of whatever
                // group Codex is on — said here, in words, rather than by letting the
                // server answer a request that names no group with a bare 400.
                ClientLog.Warning("Claude 请求没有可用的分组，已拒绝（尚未选择 Claude 分组）");
                await WriteErrorAsync(
                    context,
                    400,
                    "no Claude group is selected in the client",
                    protocol).ConfigureAwait(false);
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
                await WriteErrorAsync(context, 401, "not signed in: no account session available", protocol).ConfigureAwait(false);
                return;
            }

            if (protocol == RelayProtocol.Messages)
            {
                // One line per call: the path says whether it was a turn or a token count,
                // and the group is the one thing that can be wrong that the caller cannot see.
                ClientLog.Info($"本轮转发（Claude，{FormatGroup(group, groupName)}）：{Sanitize(served.ClientPath)}");
            }
            else
            {
                // Logged per request, not per session: this is the only place that can see
                // whether compression actually happened, and one line per model turn is a
                // volume the log can carry.
                string? filterEnabledHeader = context.Request.Headers["X-Context-Filter-Enabled"];
                string? filterChangedHeader = context.Request.Headers["X-Context-Filter-Changed"];
                string? filterBeforeHeader = context.Request.Headers["X-Context-Filter-Bytes-Before"];
                string? filterAfterHeader = context.Request.Headers["X-Context-Filter-Bytes-After"];
                string? filterSavedHeader = context.Request.Headers["X-Context-Filter-Bytes-Saved"];
                string filterWarnings = context.Request.Headers["X-Context-Filter-Warnings"] ?? string.Empty;
                ClientLog.Info($"本轮转发（{FormatGroup(group, groupName)}）：" + DescribeContextFilter(
                    filterEnabledHeader, filterChangedHeader, filterBeforeHeader, filterAfterHeader, filterSavedHeader)
                    + (filterWarnings.Length > 0 ? $"（过滤器提示：{filterWarnings}）" : string.Empty));

                // Fed to the running total (ContextFilterUsageStore) rather than only the
                // log: a number a user can watch grow is what answers "is this actually
                // doing anything for me", where a log line only answers it one turn at a
                // time. Fires only when there is something real to add — see
                // TryParseFilterMetrics.
                if (TryParseFilterMetrics(filterEnabledHeader, filterBeforeHeader, filterAfterHeader, filterSavedHeader)
                    is (long measuredBefore, _, long measuredSaved))
                {
                    _onCompressionMeasured?.Invoke(measuredBefore, measuredSaved);
                }
            }

            byte[] body;
            using (var buffer = new MemoryStream())
            {
                await context.Request.InputStream.CopyToAsync(buffer, cancellationToken).ConfigureAwait(false);
                body = buffer.ToArray();
            }

            using HttpRequestMessage request = BuildUpstreamRequest(context, served, body, jwt, group);

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

                // The account-session middleware answers 401 when the access token
                // was revoked before its local expiry. Give the session manager the
                // exact token that was rejected so it can renew it, and only show the
                // login surface if that renewal is rejected too. A gateway error may
                // also be 401; in that case renewal succeeds and the session stays.
                if (response.StatusCode == HttpStatusCode.Unauthorized &&
                    _onAccessTokenRejected is not null)
                {
                    try
                    {
                        await _onAccessTokenRejected(jwt, cancellationToken).ConfigureAwait(false);
                    }
                    catch (Exception callbackException) when (
                        callbackException is not (OutOfMemoryException or StackOverflowException or ThreadAbortException))
                    {
                        ClientLog.Warning("处理中转鉴权拒绝时续期失败", callbackException);
                    }
                }

                byte[] payload = Encoding.UTF8.GetBytes(detail);
                CopyResponseHeaders(response, context.Response, protocol);
                context.Response.StatusCode = (int)response.StatusCode;
                context.Response.ContentType =
                    response.Content.Headers.ContentType?.ToString() ?? "application/json";
                context.Response.ContentLength64 = payload.Length;
                responseStarted = true;
                await context.Response.OutputStream.WriteAsync(payload, cancellationToken).ConfigureAwait(false);
                return;
            }

            CopyResponseHeaders(response, context.Response, protocol);
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
                    await WriteErrorAsync(context, 502, "relay unreachable: " + ex.Message, protocol).ConfigureAwait(false);
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
                ClientLog.Info(protocol == RelayProtocol.Messages
                    ? "本轮被 Claude 客户端中断（连接已断开）"
                    : "本轮被 ChatGPT 中断（连接已断开）");
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

    /// <summary>
    /// The request sent upstream, built fresh rather than copied from the client's.
    /// </summary>
    /// <remarks>
    /// A whitelist, not a blacklist. Codex's own request carries installation_id,
    /// window_id and friends that mean nothing upstream, and Claude Code's carries the
    /// SDK's whole x-stainless-* set; building a request means never having to decide,
    /// header by header, which ones may travel.
    /// </remarks>
    private HttpRequestMessage BuildUpstreamRequest(
        HttpListenerContext context,
        RelayRoute route,
        byte[] body,
        string jwt,
        long? group)
    {
        // The query string travels for Messages: Claude Code calls /v1/messages?beta=true,
        // and dropping it changes which API surface answers.
        string query = route.Protocol == RelayProtocol.Messages ? context.Request.Url?.Query ?? string.Empty : string.Empty;
        var request = new HttpRequestMessage(HttpMethod.Post, _upstream + route.UpstreamPath + query)
        {
            Content = new ByteArrayContent(body),
        };
        request.Content.Headers.ContentType =
            MediaTypeHeaderValue.Parse(context.Request.ContentType ?? "application/json");

        // Replaced, not appended: forwarding the local token upstream is the easiest
        // mistake to make on this path.
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", jwt);
        request.Headers.Add(
            GroupHeader,
            group?.ToString(System.Globalization.CultureInfo.InvariantCulture) ?? "auto");

        if (route.Protocol == RelayProtocol.Responses)
        {
            request.Headers.Accept.ParseAdd("text/event-stream");
            return request;
        }

        // Not forced to an event stream: Claude Code asks for application/json even when
        // the body says stream:true (measured against a real 2.1.258 CLI), and a
        // count_tokens reply is plain JSON. The client's own Accept is what it can read.
        string? accept = context.Request.Headers["Accept"];
        if (!string.IsNullOrWhiteSpace(accept))
        {
            request.Headers.TryAddWithoutValidation("Accept", accept);
        }

        foreach (string name in MessagesForwardedRequestHeaders)
        {
            string? value = context.Request.Headers[name];
            if (!string.IsNullOrWhiteSpace(value))
            {
                request.Headers.TryAddWithoutValidation(name, value);
            }
        }

        // The editor's User-Agent, in a header of its own. See ClientUserAgentHeader for
        // why it cannot be this request's User-Agent.
        string? clientUserAgent = context.Request.Headers["User-Agent"];
        if (!string.IsNullOrWhiteSpace(clientUserAgent))
        {
            request.Headers.TryAddWithoutValidation(ClientUserAgentHeader, clientUserAgent);
        }

        return request;
    }

    /// <summary>
    /// The upstream response headers an Anthropic client acts on.
    /// </summary>
    /// <remarks>
    /// Codex is content with status and body. The Anthropic SDK is not: it decides whether
    /// and when to retry from <c>retry-after</c>, <c>retry-after-ms</c> and
    /// <c>x-should-retry</c>, and reads the rate-limit state from <c>anthropic-ratelimit-*</c>.
    /// Without them it falls back to its own guesses, which turns a 429 that said "wait ten
    /// seconds" into an immediate retry.
    /// </remarks>
    private static void CopyResponseHeaders(HttpResponseMessage from, HttpListenerResponse to, RelayProtocol protocol)
    {
        if (protocol != RelayProtocol.Messages)
        {
            return;
        }

        foreach (KeyValuePair<string, IEnumerable<string>> header in from.Headers)
        {
            bool wanted =
                header.Key.StartsWith("anthropic-", StringComparison.OrdinalIgnoreCase) ||
                header.Key.Equals("retry-after", StringComparison.OrdinalIgnoreCase) ||
                header.Key.Equals("retry-after-ms", StringComparison.OrdinalIgnoreCase) ||
                header.Key.Equals("x-should-retry", StringComparison.OrdinalIgnoreCase) ||
                header.Key.Equals("request-id", StringComparison.OrdinalIgnoreCase) ||
                header.Key.Equals("x-request-id", StringComparison.OrdinalIgnoreCase);
            if (!wanted)
            {
                continue;
            }

            foreach (string value in header.Value)
            {
                try
                {
                    to.AddHeader(header.Key, value);
                }
                catch (ArgumentException)
                {
                    // A value the listener will not put on the wire is dropped rather than
                    // allowed to fail a response that is otherwise fine.
                }
            }
        }
    }

    /// <summary>Keeps a caller-controlled string on one log line.</summary>
    /// <remarks>
    /// Anything on this port can be reached by any process on the machine, so the
    /// method and path are untrusted input. Without this a crafted request could
    /// forge extra log lines.
    /// </remarks>
    private static string Sanitize(string? value) =>
        string.IsNullOrEmpty(value) ? "-" : value.Replace("\r", " ").Replace("\n", " ");

    private static string FormatGroup(long? groupId, string? groupName) =>
        groupId is null
            ? "自动分组"
            : string.IsNullOrWhiteSpace(groupName)
            ? $"分组 {groupId}"
            : $"分组 {groupId}「{groupName.Replace("\r", " ").Replace("\n", " ")}」";

    /// <remarks>
    /// Shaped like an OpenAI error because Codex recognises that shape and files it as
    /// an upstream failure. A bare status code with an empty body surfaces to the user
    /// as an unexplained protocol error instead.
    /// </remarks>
    private static async Task WriteErrorAsync(
        HttpListenerContext context,
        int status,
        string message,
        RelayProtocol protocol = RelayProtocol.Responses)
    {
        // Written field by field rather than serialized from an object: this assembly
        // is trim-analysed, and reflection-based serialization is an IL2026 error here.
        using var buffer = new MemoryStream();
        using (var writer = new System.Text.Json.Utf8JsonWriter(buffer))
        {
            writer.WriteStartObject();
            if (protocol == RelayProtocol.Messages)
            {
                // The envelope Claude Code parses. Anything else it prints as raw JSON.
                writer.WriteString("type", "error");
                writer.WriteStartObject("error");
                writer.WriteString("type", AnthropicErrorType(status));
                writer.WriteString("message", message);
                writer.WriteEndObject();
            }
            else
            {
                writer.WriteStartObject("error");
                writer.WriteString("message", message);
                writer.WriteString("type", "cofly_local_relay");
                writer.WriteEndObject();
            }

            writer.WriteEndObject();
        }

        byte[] payload = buffer.ToArray();
        context.Response.StatusCode = status;
        context.Response.ContentType = "application/json";
        context.Response.ContentLength64 = payload.Length;
        await context.Response.OutputStream.WriteAsync(payload).ConfigureAwait(false);
    }

    /// <summary>The error type Anthropic's API uses for a status, as the server's own Messages errors do.</summary>
    internal static string AnthropicErrorType(int status) => status switch
    {
        400 => "invalid_request_error",
        401 => "authentication_error",
        403 => "permission_error",
        404 => "not_found_error",
        413 => "request_too_large",
        429 => "rate_limit_error",
        _ => "api_error",
    };

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
        SetClaudeGroup(null);
    }

    private static bool IsWellFormedToken(string? token) =>
        token is { Length: 64 } && token.All(c => c is (>= '0' and <= '9') or (>= 'a' and <= 'f'));

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
