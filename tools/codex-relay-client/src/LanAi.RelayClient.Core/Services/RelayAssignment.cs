using System.Net;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Services;

/// <summary>
/// Where a forwarded request goes and what it carries as credentials.
/// </summary>
/// <param name="BaseUrl">The address requests are sent to (no trailing slash).</param>
/// <param name="Bearer">The credential: the account session when the server forwards itself, a relay ticket when a relay node does.</param>
/// <param name="IsRelay">True for a relay node. The account session must never be sent there — only the ticket.</param>
/// <param name="NodeId">The assigned node (0 for the server itself).</param>
internal sealed record RelayTarget(string BaseUrl, string Bearer, bool IsRelay, long NodeId);

/// <summary>The server has no relay for this user right now (every relay node is down and the server takes no forwarding).</summary>
internal sealed class RelayUnavailableException : Exception
{
    public RelayUnavailableException(string message) : base(message) { }
}

/// <summary>
/// Chooses where each forwarded request goes: the server itself (the account session, as before) or the relay
/// node the server assigned to this user (a short-lived ticket on that node's address).
/// </summary>
internal interface IRelayTargetProvider
{
    /// <summary>The target for the next request. Throws <see cref="RelayUnavailableException"/> when no relay is available.</summary>
    Task<RelayTarget> GetTargetAsync(CancellationToken cancellationToken);

    /// <summary>
    /// The node could not be reached: tell the server and get a new assignment. Null when nothing else is available.
    /// </summary>
    Task<RelayTarget?> ReportUnreachableAsync(RelayTarget failed, CancellationToken cancellationToken);

    /// <summary>The node refused the ticket (expired or revoked): ask the server for a fresh one. Null when nothing changed.</summary>
    Task<RelayTarget?> RefreshAsync(RelayTarget rejected, CancellationToken cancellationToken);
}

/// <summary>
/// The server's relay assignment for the signed-in user (<c>GET/POST /api/v1/paw/relay/assignment</c>).
/// </summary>
/// <remarks>
/// <para>
/// The server answers with a role. <c>relay</c> carries a node address and a ticket; <c>master</c> means the
/// server itself forwards, as it always did, so this client keeps using <see cref="ClientOptions.ServerAddress"/>
/// and the account session — whatever address the answer carries is ignored for that role, so the session can
/// only ever travel to the address compiled into the client. A 404 means the server has no relay switched on:
/// the same thing.
/// </para>
/// <para>
/// A failure to reach the assignment endpoint itself never blocks the user: the last good assignment is kept
/// while its ticket is valid, otherwise requests go to the server as before.
/// </para>
/// </remarks>
internal sealed class RelayAssignmentClient : IRelayTargetProvider
{
    private const string AssignmentPath = "api/v1/paw/relay/assignment";

    /// <summary>The ticket is renewed this long before it expires.</summary>
    private static readonly TimeSpan TicketRenewMargin = TimeSpan.FromSeconds(60);

    /// <summary>How long a "no relay on this server" answer is trusted before asking again.</summary>
    private static readonly TimeSpan NotEnabledRecheck = TimeSpan.FromMinutes(5);

    /// <summary>The assignment is asked for again after this when the server gave no interval.</summary>
    private static readonly TimeSpan DefaultRefresh = TimeSpan.FromSeconds(60);

    /// <summary>Retry the assignment endpoint no sooner than this after it failed.</summary>
    private static readonly TimeSpan FailureBackoff = TimeSpan.FromSeconds(30);

    private readonly HttpClient _http;
    private readonly string _serverAddress;
    private readonly string? _relayNodeAddressOverride;
    private readonly Func<CancellationToken, Task<string>> _accessToken;
    private readonly TimeProvider _clock;
    private readonly SemaphoreSlim _gate = new(1, 1);

    private Assignment? _current;
    private DateTimeOffset _refreshAt = DateTimeOffset.MinValue;

    /// <summary>
    /// The account session the cached assignment was fetched with. A different one - another user signed in, or this
    /// one's access token rotated - means the cached ticket may belong to someone else, so it is asked for again.
    /// </summary>
    private string? _fetchedWith;

    private sealed record Assignment(bool IsRelay, long NodeId, string BaseUrl, string? Ticket, DateTimeOffset? TicketExpiresAt);

    public RelayAssignmentClient(
        HttpClient http,
        string serverAddress,
        Func<CancellationToken, Task<string>> accessToken,
        TimeProvider? clock = null,
        string? relayNodeAddressOverride = null)
    {
        _http = http ?? throw new ArgumentNullException(nameof(http));
        _serverAddress = (serverAddress ?? throw new ArgumentNullException(nameof(serverAddress))).TrimEnd('/');
        _relayNodeAddressOverride = relayNodeAddressOverride?.TrimEnd('/');
        _accessToken = accessToken ?? throw new ArgumentNullException(nameof(accessToken));
        _clock = clock ?? TimeProvider.System;
    }

    /// <summary>The node the next request goes to, or null when the server forwards itself (for the status display).</summary>
    public long? CurrentNodeId => _current is { IsRelay: true } c ? c.NodeId : null;

    public async Task<RelayTarget> GetTargetAsync(CancellationToken cancellationToken)
    {
        string jwt = await _accessToken(cancellationToken).ConfigureAwait(false);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            DateTimeOffset now = _clock.GetUtcNow();
            if (_current is null || now >= _refreshAt || TicketNeedsRenewal(_current, now) || _fetchedWith != jwt)
            {
                await FetchAsync(jwt, unreachableNodeId: 0, cancellationToken).ConfigureAwait(false);
            }

            return ToTarget(_current!, jwt);
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task<RelayTarget?> ReportUnreachableAsync(RelayTarget failed, CancellationToken cancellationToken)
    {
        string jwt = await _accessToken(cancellationToken).ConfigureAwait(false);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            // Another request may already have moved this user: only report the node that is still assigned.
            if (_current is { IsRelay: true } c && c.NodeId == failed.NodeId)
            {
                try
                {
                    await FetchAsync(jwt, unreachableNodeId: failed.NodeId, cancellationToken).ConfigureAwait(false);
                }
                catch (RelayUnavailableException)
                {
                    return null;
                }
            }

            RelayTarget next = ToTarget(_current!, jwt);
            return next.NodeId == failed.NodeId && next.IsRelay == failed.IsRelay ? null : next;
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task<RelayTarget?> RefreshAsync(RelayTarget rejected, CancellationToken cancellationToken)
    {
        string jwt = await _accessToken(cancellationToken).ConfigureAwait(false);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            // A concurrent request may have renewed it already.
            if (_current is { IsRelay: true, Ticket: { } ticket } && ticket != rejected.Bearer)
            {
                return ToTarget(_current, jwt);
            }

            await FetchAsync(jwt, unreachableNodeId: 0, cancellationToken).ConfigureAwait(false);
            RelayTarget next = ToTarget(_current!, jwt);
            return next.Bearer == rejected.Bearer && next.BaseUrl == rejected.BaseUrl ? null : next;
        }
        catch (RelayUnavailableException)
        {
            return null;
        }
        finally
        {
            _gate.Release();
        }
    }

    private RelayTarget ToTarget(Assignment a, string jwt) =>
        a.IsRelay && a.Ticket is not null
            ? new RelayTarget(_relayNodeAddressOverride ?? a.BaseUrl, a.Ticket, IsRelay: true, a.NodeId)
            : new RelayTarget(_serverAddress, jwt, IsRelay: false, NodeId: 0);

    private static bool TicketNeedsRenewal(Assignment a, DateTimeOffset now) =>
        a.IsRelay && a.TicketExpiresAt is { } exp && now >= exp - TicketRenewMargin;

    /// <summary>Asks the server; updates the cached assignment. Called with the gate held.</summary>
    private async Task FetchAsync(string jwt, long unreachableNodeId, CancellationToken cancellationToken)
    {
        DateTimeOffset now = _clock.GetUtcNow();
        if (_fetchedWith != jwt)
        {
            // Never keep another session's ticket around, even if this fetch then fails.
            _current = null;
            _fetchedWith = jwt;
        }

        using var request = new HttpRequestMessage(
            unreachableNodeId > 0 ? HttpMethod.Post : HttpMethod.Get,
            _serverAddress + "/" + AssignmentPath);
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", jwt);
        if (unreachableNodeId > 0)
        {
            request.Content = new StringContent(
                "{\"unreachable_node_id\":" + unreachableNodeId.ToString(System.Globalization.CultureInfo.InvariantCulture) + "}",
                Encoding.UTF8,
                "application/json");
        }

        HttpResponseMessage response;
        try
        {
            response = await _http.SendAsync(request, cancellationToken).ConfigureAwait(false);
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException && !cancellationToken.IsCancellationRequested)
        {
            ClientLog.Warning("查询中转分配失败，沿用上一次的分配", ex);
            KeepOrFallBack(now);
            return;
        }

        using (response)
        {
            string body = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
            switch ((int)response.StatusCode)
            {
                case 200:
                    try
                    {
                        _current = Parse(body, now, out TimeSpan refresh);
                        _refreshAt = now + refresh;
                    }
                    catch (Exception ex) when (ex is JsonException or InvalidOperationException)
                    {
                        ClientLog.Warning("中转分配的回复格式不对，沿用上一次的分配", ex);
                        KeepOrFallBack(now);
                        return;
                    }

                    ClientLog.Info(_current.IsRelay
                        ? $"中转分配：节点 {_current.NodeId}（{Sanitize(_current.BaseUrl)}）"
                        : "中转分配：主节点");
                    return;
                case 404:
                    // The server has no relay switched on: it forwards itself, as it always did.
                    _current = new Assignment(false, 0, _serverAddress, null, null);
                    _refreshAt = now + NotEnabledRecheck;
                    return;
                case 503 when body.Contains("RELAY_UNAVAILABLE", StringComparison.Ordinal):
                    _current = null;
                    _refreshAt = now + FailureBackoff;
                    throw new RelayUnavailableException("no relay is available right now");
                case 401:
                    throw new RelayApiException(RelayFailure.Unauthenticated, "the account session was refused by the relay assignment endpoint", statusCode: 401);
                default:
                    ClientLog.Warning($"查询中转分配被拒绝：HTTP {(int)response.StatusCode}，沿用上一次的分配");
                    KeepOrFallBack(now);
                    return;
            }
        }
    }

    /// <summary>The assignment endpoint was unreachable or odd: keep a still-valid relay ticket, otherwise use the server.</summary>
    private void KeepOrFallBack(DateTimeOffset now)
    {
        if (_current is { IsRelay: true } c && !TicketNeedsRenewal(c, now))
        {
            _refreshAt = now + FailureBackoff;
            return;
        }

        _current = new Assignment(false, 0, _serverAddress, null, null);
        _refreshAt = now + FailureBackoff;
    }

    private Assignment Parse(string body, DateTimeOffset now, out TimeSpan refresh)
    {
        refresh = DefaultRefresh;
        using JsonDocument doc = JsonDocument.Parse(body);
        JsonElement root = doc.RootElement;
        if (root.TryGetProperty("refresh_after", out JsonElement ra) && ra.ValueKind == JsonValueKind.Number && ra.TryGetInt32(out int seconds) && seconds > 0)
        {
            refresh = TimeSpan.FromSeconds(Math.Max(seconds, 15));
        }

        string role = root.TryGetProperty("role", out JsonElement r) ? r.GetString() ?? string.Empty : string.Empty;
        if (!string.Equals(role, "relay", StringComparison.Ordinal))
        {
            // The server forwards itself: this client's own address and the account session, whatever address the answer carries.
            return new Assignment(false, 0, _serverAddress, null, null);
        }

        string baseUrl = (root.TryGetProperty("base_url", out JsonElement b) ? b.GetString() : null) ?? string.Empty;
        string ticket = (root.TryGetProperty("ticket", out JsonElement t) ? t.GetString() : null) ?? string.Empty;
        long nodeId = root.TryGetProperty("node_id", out JsonElement n) && n.TryGetInt64(out long id) ? id : 0;
        if (!Uri.TryCreate(baseUrl, UriKind.Absolute, out Uri? uri) || uri.Scheme is not ("https" or "http") || ticket.Length == 0)
        {
            throw new InvalidOperationException("the relay assignment is malformed");
        }

        DateTimeOffset? expires = null;
        if (root.TryGetProperty("ticket_expires_at", out JsonElement e) && e.ValueKind == JsonValueKind.String &&
            DateTimeOffset.TryParse(e.GetString(), System.Globalization.CultureInfo.InvariantCulture, System.Globalization.DateTimeStyles.AssumeUniversal, out DateTimeOffset parsed))
        {
            expires = parsed;
        }

        return new Assignment(true, nodeId, baseUrl.TrimEnd('/'), ticket, expires);
    }

    private static string Sanitize(string value) => value.Replace('\r', ' ').Replace('\n', ' ');
}
