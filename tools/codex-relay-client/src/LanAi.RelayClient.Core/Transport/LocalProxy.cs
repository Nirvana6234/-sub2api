using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>The account one tool's traffic goes to while its local proxy is on.</summary>
internal sealed record LocalProxyTarget(long AccountId, string Name);

/// <summary>Where the local proxy sends each tool's traffic.</summary>
/// <remarks>Settable so tests can point both at a loopback server.</remarks>
internal sealed record LocalProxyEndpoints(string CodexResponsesUrl, string ClaudeBaseUrl)
{
    /// <summary>The official endpoints, as the relay's server uses them (<c>chatgptCodexURL</c>, <c>claudeAPIURL</c>).</summary>
    public static LocalProxyEndpoints Official { get; } = new(
        "https://chatgpt.com/backend-api/codex/responses",
        "https://api.anthropic.com");
}

/// <summary>What happened on one local-proxy request, for the client to show.</summary>
/// <param name="Succeeded">False for anything the user should hear about.</param>
/// <param name="Message">Why, in Chinese, when it failed.</param>
internal sealed record LocalProxyOutcome(LocalProxyKind Kind, long AccountId, bool Succeeded, string? Message);

/// <summary>Usage the official API reported for one local-proxy request.</summary>
internal sealed record LocalProxyUsage(
    LocalProxyKind Kind,
    long AccountId,
    long InputTokens,
    long OutputTokens,
    long CachedTokens);

/// <summary>Supplies the short-lived access token for one of the user's own accounts.</summary>
internal interface ILocalProxyCredentialSource
{
    /// <param name="forceRefresh">After the official API refused the cached token.</param>
    Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken);

    /// <summary>Drops everything cached, for the next account to sign in.</summary>
    void Clear();
}

/// <summary>
/// Caches each account's short-lived access token from the relay server, in memory only.
/// </summary>
/// <remarks>
/// <para>
/// The server is the account's only refresher; this cache never sees a refresh token and
/// never tries to renew one. It asks the server again shortly before the token's own
/// expiry, after an official 401, and at least every half hour so a token the server has
/// meanwhile rotated is picked up.
/// </para>
/// <para>
/// Pulled per request, like the account session in <see cref="LocalPawRelay"/>: a token
/// captured once at switch-on would be dead within its lifetime, and the failure would
/// look like the official API being down.
/// </para>
/// </remarks>
internal sealed class LocalProxyCredentialCache : ILocalProxyCredentialSource
{
    private static readonly TimeSpan ExpirySkew = TimeSpan.FromMinutes(2);
    private static readonly TimeSpan MaxAge = TimeSpan.FromMinutes(30);

    private readonly IRelayServerClient _client;
    private readonly Func<CancellationToken, Task<string>> _sessionToken;
    private readonly Func<DateTimeOffset> _clock;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private readonly Dictionary<long, (LocalProxyCredential Credential, DateTimeOffset UsableUntil)> _cached = [];

    public LocalProxyCredentialCache(
        IRelayServerClient client,
        Func<CancellationToken, Task<string>> sessionToken,
        Func<DateTimeOffset>? clock = null)
    {
        _client = client ?? throw new ArgumentNullException(nameof(client));
        _sessionToken = sessionToken ?? throw new ArgumentNullException(nameof(sessionToken));
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    public async Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken)
    {
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            DateTimeOffset now = _clock();
            if (!forceRefresh &&
                _cached.TryGetValue(accountId, out var entry) &&
                entry.UsableUntil > now)
            {
                return entry.Credential;
            }

            string session = await _sessionToken(cancellationToken).ConfigureAwait(false);
            LocalProxyCredential fresh = await _client
                .GetLocalProxyCredentialAsync(session, accountId, cancellationToken)
                .ConfigureAwait(false);
            if (string.IsNullOrWhiteSpace(fresh.AccessToken))
            {
                throw new RelayApiException(RelayFailure.MalformedResponse, "服务器没有返回本地代理凭据。");
            }

            DateTimeOffset usableUntil = now + MaxAge;
            if (fresh.ExpiresAt is { } expiresAt && expiresAt - ExpirySkew < usableUntil)
            {
                usableUntil = expiresAt - ExpirySkew;
            }

            _cached[accountId] = (fresh, usableUntil);
            return fresh;
        }
        finally
        {
            _gate.Release();
        }
    }

    public void Clear()
    {
        _gate.Wait();
        try
        {
            _cached.Clear();
        }
        finally
        {
            _gate.Release();
        }
    }
}
