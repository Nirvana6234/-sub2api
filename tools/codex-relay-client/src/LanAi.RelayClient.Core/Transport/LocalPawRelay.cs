using System.IO;
using System.Buffers;
using System.Net;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using LanAi.RelayClient.Platform;
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
/// <param name="LocalProxyOnly">
/// Served only while that tool's local proxy is on. The account-session endpoint on the
/// server has no such sub-path, so in relay mode it is refused exactly as before.
/// </param>
internal sealed record RelayRoute(string ClientPath, string UpstreamPath, RelayProtocol Protocol, bool LocalProxyOnly = false);

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
        new(CodexPath + "/compact", string.Empty, RelayProtocol.Responses, LocalProxyOnly: true),
        new(CodexPath + "/input_tokens", string.Empty, RelayProtocol.Responses, LocalProxyOnly: true),
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

    // Replaced on each start: a listener whose Start failed cannot be started again.
    private HttpListener _listener = new();
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

    // The local proxy: straight to the official API with one of the user's own accounts.
    private readonly ILocalProxyCredentialSource? _localProxyCredentials;
    private readonly LocalProxyEndpoints _localProxyEndpoints;
    private HttpClient _directHttp;
    private readonly bool _ownsDirectHttp;

    /// <summary>
    /// Clients replaced by <see cref="RefreshDirectConnection"/>. Not disposed on the spot:
    /// a turn still streaming through one would be cut off. Disposed with the relay.
    /// </summary>
    private readonly List<HttpClient> _retiredDirectHttp = [];

    /// <summary>Encrypted Codex history each local-proxy account has refused. See <see cref="EncryptedContentRecovery"/>.</summary>
    private readonly RejectedEncryptedContent _rejectedEncrypted = new();
    private readonly Action<LocalProxyOutcome>? _onLocalProxyOutcome;
    private readonly Action<LocalProxyUsage>? _onLocalProxyUsage;
    private LocalProxyTarget? _codexLocalProxy;
    private LocalProxyTarget? _claudeLocalProxy;

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
        IRelayEndpointStore? endpointStore = null,
        ILocalProxyCredentialSource? localProxyCredentials = null,
        LocalProxyEndpoints? localProxyEndpoints = null,
        HttpClient? directHttp = null,
        Action<LocalProxyOutcome>? onLocalProxyOutcome = null,
        Action<LocalProxyUsage>? onLocalProxyUsage = null)
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

        _localProxyCredentials = localProxyCredentials;
        _localProxyEndpoints = localProxyEndpoints ?? LocalProxyEndpoints.Official;
        _onLocalProxyOutcome = onLocalProxyOutcome;
        _onLocalProxyUsage = onLocalProxyUsage;
        _ownsDirectHttp = directHttp is null;
        _directHttp = directHttp ?? CreateDirectHttp(_localProxyEndpoints);

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

    /// <summary>
    /// Sends one tool's traffic straight to the official API with one of the user's own
    /// accounts, or — with null — back to the relay server. Takes effect on the next request.
    /// </summary>
    /// <remarks>
    /// Independent per tool, like the groups: Codex on a local proxy does not move Claude Code,
    /// and neither touches the tool's own configuration (it keeps pointing at this relay).
    /// </remarks>
    public void SetLocalProxy(LocalProxyKind kind, LocalProxyTarget? target)
    {
        lock (_gate)
        {
            switch (kind)
            {
                case LocalProxyKind.Codex:
                    _codexLocalProxy = target;
                    break;
                case LocalProxyKind.ClaudeCode:
                    _claudeLocalProxy = target;
                    break;
                default:
                    throw new ArgumentOutOfRangeException(nameof(kind), kind, "Not a local proxy kind.");
            }
        }

        string tool = kind == LocalProxyKind.Codex ? "ChatGPT" : "Claude Code";
        ClientLog.Info(target is null
            ? $"本机 Relay（{tool}）改回经中转站转发"
            : $"本机 Relay（{tool}）改为本地代理：账号 {target.AccountId}「{Sanitize(target.Name)}」直连官方");

        // Switching on is when the user has just been told to have their proxy on; pick up
        // the proxy as it is now, not as it was when the client started.
        if (target is not null)
        {
            RefreshDirectConnection();
        }
    }

    /// <summary>
    /// The client for the official API: its own, so nothing about it can disturb the account
    /// session's fingerprint on the server side.
    /// </summary>
    /// <remarks>
    /// Uses the proxy as it is set <em>now</em> (<see cref="SystemProxyReader"/>) — .NET's own
    /// default is a snapshot from process start, and the official hosts are often reachable
    /// only through a proxy the user turns on later. Never follows a redirect, which would
    /// carry the account's token to whatever host it names. Decompresses itself so the stream
    /// can be read for its usage; accept-encoding is therefore not forwarded, and no
    /// content-encoding goes back to the tool.
    /// </remarks>
    private static HttpClient CreateDirectHttp(LocalProxyEndpoints endpoints)
    {
        SystemProxy proxy = SystemProxyReader.Current(new Uri(endpoints.CodexResponsesUrl));
        return new HttpClient(new HttpClientHandler
        {
            AllowAutoRedirect = false,
            AutomaticDecompression = DecompressionMethods.All,
            UseProxy = proxy.Proxy is not null,
            Proxy = proxy.Proxy,
        })
        {
            Timeout = Timeout.InfiniteTimeSpan,
        };
    }

    /// <summary>
    /// Rebuilds the connection to the official API with the proxy as it is set now. Called
    /// when a local proxy is switched on and after a connection failure, so a proxy turned on
    /// afterwards is used from the next turn without restarting the client.
    /// </summary>
    internal void RefreshDirectConnection()
    {
        if (!_ownsDirectHttp)
        {
            return;
        }

        HttpClient fresh = CreateDirectHttp(_localProxyEndpoints);
        ClientLog.Info($"本地代理连接官方改用当前网络设置：{SystemProxyReader.Current(new Uri(_localProxyEndpoints.CodexResponsesUrl)).Description}");
        lock (_gate)
        {
            _retiredDirectHttp.Add(_directHttp);
            _directHttp = fresh;
        }
    }

    /// <summary>The local-proxy target currently in force for <paramref name="kind"/>, if any.</summary>
    internal LocalProxyTarget? LocalProxyFor(LocalProxyKind kind)
    {
        lock (_gate)
        {
            return kind == LocalProxyKind.ClaudeCode ? _claudeLocalProxy : _codexLocalProxy;
        }
    }

    public Task StartAsync(CancellationToken cancellationToken = default)
    {
        if (_disposed) throw new ObjectDisposedException(nameof(LocalPawRelay));
        if (_listener.IsListening) return Task.CompletedTask;
        _serve = null;
        BaseAddress = null;
        _stop = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        // The remembered port if it is free, otherwise any free one. Falling back is the
        // normal outcome when something else took the port, not a failure: whoever holds
        // a configuration naming the old port is told through Origin and rewrites it.
        HttpListener listener = LoopbackHttpListener.Start(_preferredPort, out int port);
        _listener.Close();
        _listener = listener;
        _port = port;
        BaseAddress = new Uri($"http://127.0.0.1:{port}/v1/");
        _serve = ServeAsync(listener, _stop.Token);

        // Kept only once it is actually bound, so what is remembered is what worked.
        _endpointStore?.Save(port, Token);
        return Task.CompletedTask;
    }

    private async Task ServeAsync(HttpListener listener, CancellationToken cancellationToken)
    {
        while (!cancellationToken.IsCancellationRequested)
        {
            HttpListenerContext context;
            try { context = await listener.GetContextAsync().WaitAsync(cancellationToken); }
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

            LocalProxyTarget? localProxy;
            lock (_gate)
            {
                localProxy = protocol == RelayProtocol.Messages ? _claudeLocalProxy : _codexLocalProxy;
            }

            if (localProxy is not null)
            {
                responseStarted = await HandleLocalProxyAsync(context, served, localProxy, cancellationToken).ConfigureAwait(false);
                return;
            }

            if (served.LocalProxyOnly)
            {
                ClientLog.Info(
                    $"本机 Relay 拒绝 {Sanitize(context.Request.HttpMethod)} {Sanitize(context.Request.Url?.PathAndQuery)}" +
                    "（HTTP 404 仅本地代理模式提供）");
                await WriteErrorAsync(context, 404, "no such endpoint", protocol).ConfigureAwait(false);
                return;
            }

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
                ObserveContextFilter(context, FormatGroup(group, groupName));
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
            responseStarted = true;
            await PumpAsync(response, context, observe: null, cancellationToken).ConfigureAwait(false);
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
    /// Writes a successful upstream answer through to the tool as it arrives.
    /// </summary>
    /// <remarks>
    /// Chunked, and flushed per read: buffering the stream would break two things at
    /// once — the answer stops appearing word by word, and the stop button does not take
    /// effect until the turn has finished anyway. <paramref name="observe"/> sees every
    /// chunk as it passes, never the whole body.
    /// </remarks>
    private static async Task PumpAsync(
        HttpResponseMessage response,
        HttpListenerContext context,
        Action<ReadOnlyMemory<byte>>? observe,
        CancellationToken cancellationToken)
    {
        context.Response.StatusCode = (int)response.StatusCode;
        context.Response.ContentType =
            response.Content.Headers.ContentType?.ToString() ?? "text/event-stream";
        context.Response.SendChunked = true;

        await using Stream upstream = await response.Content
            .ReadAsStreamAsync(cancellationToken)
            .ConfigureAwait(false);
        byte[] chunk = ArrayPool<byte>.Shared.Rent(16 * 1024);
        try
        {
            int read;
            while ((read = await upstream.ReadAsync(chunk, cancellationToken).ConfigureAwait(false)) > 0)
            {
                observe?.Invoke(chunk.AsMemory(0, read));
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

    /// <summary>
    /// Logs what the context filter did to this request and feeds the running total.
    /// </summary>
    /// <remarks>
    /// Logged per request, not per session: this is the only place that can see whether
    /// compression actually happened, and one line per model turn is a volume the log can
    /// carry. Fed to the running total (ContextFilterUsageStore) rather than only the log:
    /// a number a user can watch grow is what answers "is this actually doing anything for
    /// me". Fires only when there is something real to add — see TryParseFilterMetrics.
    /// </remarks>
    private void ObserveContextFilter(HttpListenerContext context, string route)
    {
        string? filterEnabledHeader = context.Request.Headers["X-Context-Filter-Enabled"];
        string? filterChangedHeader = context.Request.Headers["X-Context-Filter-Changed"];
        string? filterBeforeHeader = context.Request.Headers["X-Context-Filter-Bytes-Before"];
        string? filterAfterHeader = context.Request.Headers["X-Context-Filter-Bytes-After"];
        string? filterSavedHeader = context.Request.Headers["X-Context-Filter-Bytes-Saved"];
        string filterWarnings = context.Request.Headers["X-Context-Filter-Warnings"] ?? string.Empty;
        ClientLog.Info($"本轮转发（{route}）：" + DescribeContextFilter(
            filterEnabledHeader, filterChangedHeader, filterBeforeHeader, filterAfterHeader, filterSavedHeader)
            + (filterWarnings.Length > 0 ? $"（过滤器提示：{Sanitize(filterWarnings)}）" : string.Empty));

        if (TryParseFilterMetrics(filterEnabledHeader, filterBeforeHeader, filterAfterHeader, filterSavedHeader)
            is (long measuredBefore, _, long measuredSaved))
        {
            _onCompressionMeasured?.Invoke(measuredBefore, measuredSaved);
        }
    }

    /// <summary>
    /// Serves one request straight from the official API with one of the user's own
    /// accounts. Returns whether anything was written to the tool.
    /// </summary>
    /// <remarks>
    /// <para>
    /// No fallback to the relay server, ever: the local proxy is the user's own choice, and
    /// quietly sending the turn to the server instead would start spending their balance
    /// without their knowing. A failure is answered to the tool as the official API gave it
    /// and reported through <see cref="LocalProxyOutcome"/> for the client to show.
    /// </para>
    /// <para>
    /// One retry, only for an official 401 and only before anything reached the tool: the
    /// cached token may have been rotated server-side, so a fresh one is asked for once.
    /// </para>
    /// <para>
    /// And one for Codex history this account cannot decrypt (400
    /// <c>invalid_encrypted_content</c>), with the encrypted items stripped — see
    /// <see cref="EncryptedContentRecovery"/>. Items already refused are stripped before the
    /// first attempt.
    /// </para>
    /// </remarks>
    private async Task<bool> HandleLocalProxyAsync(
        HttpListenerContext context,
        RelayRoute route,
        LocalProxyTarget target,
        CancellationToken cancellationToken)
    {
        RelayProtocol protocol = route.Protocol;
        LocalProxyKind kind = protocol == RelayProtocol.Messages ? LocalProxyKind.ClaudeCode : LocalProxyKind.Codex;
        string label = $"本地代理「{Sanitize(target.Name)}」";

        if (protocol == RelayProtocol.Messages)
        {
            ClientLog.Info($"本轮转发（Claude，{label}）：{Sanitize(route.ClientPath)}");
        }
        else
        {
            ObserveContextFilter(context, label);
        }

        if (_localProxyCredentials is null)
        {
            Report(kind, target, false, "本地代理未就绪。");
            await WriteErrorAsync(context, 503, "local proxy is not available", protocol).ConfigureAwait(false);
            return true;
        }

        byte[] body;
        using (var buffer = new MemoryStream())
        {
            await context.Request.InputStream.CopyToAsync(buffer, cancellationToken).ConfigureAwait(false);
            body = buffer.ToArray();
        }

        bool codex = protocol != RelayProtocol.Messages;
        if (codex && EncryptedContentRecovery.StripKnown(body, _rejectedEncrypted.For(target.AccountId)) is { } known)
        {
            ClientLog.Info($"{label}：剥离该账号已拒收过的加密历史");
            body = known;
        }

        HttpResponseMessage? response = null;
        string? refusal = null;
        try
        {
            bool tokenRetried = false;
            bool encryptedRetried = false;
            while (true)
            {
                LocalProxyCredential credential;
                try
                {
                    credential = await _localProxyCredentials
                        .GetAsync(target.AccountId, forceRefresh: tokenRetried, cancellationToken)
                        .ConfigureAwait(false);
                }
                catch (RelayApiException ex)
                {
                    ClientLog.Warning($"取本地代理凭据失败（账号 {target.AccountId}）", ex);
                    Report(kind, target, false, "无法从中转站取得该账号的授权：" + ex.UserMessage);
                    await WriteErrorAsync(context, 502, "local proxy: could not obtain the account's token", protocol)
                        .ConfigureAwait(false);
                    return true;
                }

                using HttpRequestMessage request = BuildLocalProxyRequest(context, route, body, credential);
                try
                {
                    HttpClient direct;
                    lock (_gate)
                    {
                        direct = _directHttp;
                    }
                    response = await direct
                        .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                        .ConfigureAwait(false);
                }
                catch (HttpRequestException ex)
                {
                    ClientLog.Warning("本地代理连接官方失败", ex);
                    // The next turn reads the proxy settings afresh: the user may be turning
                    // their proxy on right now.
                    RefreshDirectConnection();
                    Report(kind, target, false,
                        "无法连接官方服务器，请检查代理/VPN 是否开启、能否访问官方（" + ex.Message + "）。" +
                        "开启代理后下一轮对话会自动使用，无需重启客户端。");
                    await WriteErrorAsync(context, 502, "local proxy: official API unreachable", protocol)
                        .ConfigureAwait(false);
                    return true;
                }

                if (response.StatusCode == HttpStatusCode.Unauthorized && !tokenRetried)
                {
                    tokenRetried = true;
                    response.Dispose();
                    response = null;
                    continue;
                }

                if (codex && !encryptedRetried && response.StatusCode == HttpStatusCode.BadRequest)
                {
                    refusal = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
                    if (EncryptedContentRecovery.IsInvalidEncryptedContent(400, refusal) &&
                        EncryptedContentRecovery.StripAll(body) is { } stripped)
                    {
                        // Collected before stripping: these are what this account refused.
                        _rejectedEncrypted.Remember(target.AccountId, EncryptedContentRecovery.CollectDigests(body));
                        ClientLog.Info($"{label}：官方解不开对话里的加密历史（多半来自中转站的账号），剥离后重试一次");
                        encryptedRetried = true;
                        body = stripped;
                        refusal = null;
                        response.Dispose();
                        response = null;
                        continue;
                    }
                }

                break;
            }

            if (!response.IsSuccessStatusCode)
            {
                string detail = refusal ?? await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
                ClientLog.Warning($"官方拒绝本轮请求（{label}）：HTTP {(int)response.StatusCode} {Summarize(detail)}");
                Report(kind, target, false, DescribeOfficialRefusal((int)response.StatusCode));

                byte[] payload = Encoding.UTF8.GetBytes(detail);
                CopyResponseHeaders(response, context.Response, protocol, localProxy: true);
                context.Response.StatusCode = (int)response.StatusCode;
                context.Response.ContentType = response.Content.Headers.ContentType?.ToString() ?? "application/json";
                context.Response.ContentLength64 = payload.Length;
                await context.Response.OutputStream.WriteAsync(payload, cancellationToken).ConfigureAwait(false);
                return true;
            }

            Report(kind, target, true, null);
            CopyResponseHeaders(response, context.Response, protocol, localProxy: true);
            var meter = new LocalProxyUsageMeter(protocol);
            try
            {
                await PumpAsync(response, context, meter.Observe, cancellationToken).ConfigureAwait(false);
            }
            finally
            {
                if (meter.Finish() is { } usage)
                {
                    _onLocalProxyUsage?.Invoke(new LocalProxyUsage(
                        kind, target.AccountId, usage.InputTokens, usage.OutputTokens, usage.CachedTokens));
                }
            }
            return true;
        }
        finally
        {
            response?.Dispose();
        }
    }

    private HttpRequestMessage BuildLocalProxyRequest(
        HttpListenerContext context,
        RelayRoute route,
        byte[] body,
        LocalProxyCredential credential)
    {
        if (route.Protocol == RelayProtocol.Messages)
        {
            // Claude Code calls /v1/messages?beta=true; the query selects the API surface.
            string query = context.Request.Url?.Query is { Length: > 0 } q ? q : "?beta=true";
            return LocalProxyRequests.BuildClaude(
                _localProxyEndpoints.ClaudeBaseUrl.TrimEnd('/') + route.ClientPath + query,
                context.Request.Headers,
                body,
                context.Request.ContentType,
                credential,
                countTokens: route.ClientPath.EndsWith("/count_tokens", StringComparison.Ordinal));
        }

        string suffix = route.ClientPath[CodexPath.Length..];
        return LocalProxyRequests.BuildCodex(
            _localProxyEndpoints.CodexResponsesUrl.TrimEnd('/') + suffix,
            context.Request.Headers,
            body,
            context.Request.ContentType,
            credential,
            compact: suffix == "/compact");
    }

    private void Report(LocalProxyKind kind, LocalProxyTarget target, bool succeeded, string? message)
    {
        try
        {
            _onLocalProxyOutcome?.Invoke(new LocalProxyOutcome(kind, target.AccountId, succeeded, message));
        }
        catch (Exception ex) when (ex is not (OutOfMemoryException or StackOverflowException or ThreadAbortException))
        {
            ClientLog.Warning("通知本地代理状态失败", ex);
        }
    }

    /// <summary>What an official refusal means, in the words the user is shown.</summary>
    internal static string DescribeOfficialRefusal(int status) => status switch
    {
        401 => "官方拒绝了该账号的授权（HTTP 401），可能已失效，请在中转站检查这个账号。",
        403 => "官方不允许该账号这样使用（HTTP 403），可能被限制或订阅不支持。",
        404 => "官方找不到这个接口或模型（HTTP 404）。",
        429 => "该账号的额度已用完或请求过快（HTTP 429），请稍后再试。",
        >= 500 => $"官方服务暂时不可用（HTTP {status}）。",
        _ => $"官方拒绝了本轮请求（HTTP {status}）。",
    };

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
    /// <remarks>
    /// Codex straight from the official API also reads headers: its rate-limit meter comes
    /// from <c>x-codex-*</c>, so in local-proxy mode those travel back as well.
    /// </remarks>
    private static void CopyResponseHeaders(
        HttpResponseMessage from,
        HttpListenerResponse to,
        RelayProtocol protocol,
        bool localProxy = false)
    {
        if (protocol != RelayProtocol.Messages && !localProxy)
        {
            return;
        }

        foreach (KeyValuePair<string, IEnumerable<string>> header in from.Headers)
        {
            bool wanted = protocol == RelayProtocol.Messages
                ? IsWantedAnthropicHeader(header.Key)
                : header.Key.StartsWith("x-codex-", StringComparison.OrdinalIgnoreCase) ||
                  header.Key.StartsWith("openai-", StringComparison.OrdinalIgnoreCase) ||
                  header.Key.Equals("retry-after", StringComparison.OrdinalIgnoreCase) ||
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

    private static bool IsWantedAnthropicHeader(string name) =>
        name.StartsWith("anthropic-", StringComparison.OrdinalIgnoreCase) ||
        name.Equals("retry-after", StringComparison.OrdinalIgnoreCase) ||
        name.Equals("retry-after-ms", StringComparison.OrdinalIgnoreCase) ||
        name.Equals("x-should-retry", StringComparison.OrdinalIgnoreCase) ||
        name.Equals("request-id", StringComparison.OrdinalIgnoreCase) ||
        name.Equals("x-request-id", StringComparison.OrdinalIgnoreCase);

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
        if (_ownsDirectHttp)
        {
            _directHttp.Dispose();
            foreach (HttpClient retired in _retiredDirectHttp)
            {
                retired.Dispose();
            }
        }
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

        // Per account, like the groups: the next person to sign in chooses their own.
        lock (_gate)
        {
            _codexLocalProxy = null;
            _claudeLocalProxy = null;
        }
        _localProxyCredentials?.Clear();
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
