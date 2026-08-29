using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Services;

/// <summary>Why the client stopped being signed in.</summary>
internal enum SignOutReason
{
    /// <summary>Not signed in yet this run.</summary>
    None,

    /// <summary>The user asked to sign out.</summary>
    UserRequested,

    /// <summary>The stored session could no longer be renewed.</summary>
    SessionExpired,
}

/// <summary>
/// Owns the signed-in session: acquiring it, renewing it, and giving it up.
/// </summary>
/// <remarks>
/// <para>
/// Kept free of UI types and constructed with its clock injected, so the renewal
/// rules — the part with real edge cases — are testable without a window or a
/// wall-clock wait.
/// </para>
/// <para>
/// Passwords are accepted as method arguments and never stored on this object.
/// </para>
/// </remarks>
internal sealed class RelaySessionManager
{
    /// <summary>
    /// How far ahead of expiry the access token is renewed.
    /// </summary>
    /// <remarks>
    /// Wide enough that a call cannot be issued with a token that expires while
    /// it is in flight, which would surface as a spurious "session expired".
    /// </remarks>
    private static readonly TimeSpan RenewalMargin = TimeSpan.FromMinutes(2);

    private readonly IRelayServerClient _client;
    private readonly ISessionStore _store;
    private readonly Func<DateTimeOffset> _clock;
    private readonly string _serverAddress;

    private readonly SemaphoreSlim _renewalGate = new(1, 1);

    private StoredSession? _session;

    public RelaySessionManager(
        IRelayServerClient client,
        ISessionStore store,
        string serverAddress,
        Func<DateTimeOffset>? clock = null)
    {
        _client = client ?? throw new ArgumentNullException(nameof(client));
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _serverAddress = serverAddress ?? throw new ArgumentNullException(nameof(serverAddress));
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    /// <summary>Raised whenever the signed-in state changes, on the calling thread.</summary>
    public event EventHandler? StateChanged;

    public bool IsSignedIn => _session is not null;

    public string UserDisplayName => _session?.UserName ?? string.Empty;

    public string UserEmail => _session?.UserEmail ?? string.Empty;

    public SignOutReason LastSignOutReason { get; private set; } = SignOutReason.None;

    /// <summary>
    /// Restores a session persisted by an earlier run, if it is still usable.
    /// </summary>
    /// <remarks>
    /// A session belonging to a different server is discarded: an address is not
    /// an identity, and tokens issued by one relay mean nothing to another.
    /// </remarks>
    public async Task<bool> RestoreAsync(CancellationToken cancellationToken = default)
    {
        StoredSession? stored = _store.Load();
        if (stored is null)
        {
            return false;
        }

        if (!string.Equals(stored.ServerAddress, _serverAddress, StringComparison.OrdinalIgnoreCase))
        {
            _store.Clear();
            return false;
        }

        _session = stored;

        try
        {
            await GetAccessTokenAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (RelayApiException)
        {
            // Includes the offline case. Renewal already dropped the session when
            // the server rejected it; a network failure leaves it in place so the
            // user is not signed out merely for starting up without a connection.
            if (_session is null)
            {
                return false;
            }
        }

        RaiseStateChanged();
        return _session is not null;
    }

    /// <summary>Signs in. A returned two-factor demand must be completed with <see cref="CompleteTwoFactorAsync"/>.</summary>
    public async Task<LoginOutcome> SignInAsync(
        string email,
        string password,
        CancellationToken cancellationToken = default)
    {
        LoginOutcome outcome = await _client.LoginAsync(email, password, cancellationToken).ConfigureAwait(false);

        if (!outcome.RequiresTwoFactor)
        {
            Adopt(outcome.Tokens!);
        }

        return outcome;
    }

    public async Task CompleteTwoFactorAsync(
        string tempToken,
        string totpCode,
        CancellationToken cancellationToken = default)
    {
        AuthTokens tokens = await _client
            .CompleteTwoFactorAsync(tempToken, totpCode, cancellationToken)
            .ConfigureAwait(false);

        Adopt(tokens);
    }

    /// <summary>Creates an account and adopts the session it returns, so the user is not asked to sign in again.</summary>
    public async Task RegisterAsync(RegistrationRequest request, CancellationToken cancellationToken = default)
    {
        AuthTokens tokens = await _client.RegisterAsync(request, cancellationToken).ConfigureAwait(false);
        Adopt(tokens);
    }

    /// <summary>
    /// Returns an access token good for an immediate call, renewing it if needed.
    /// </summary>
    /// <exception cref="RelayApiException">
    /// The session could not be renewed. When the server rejected the refresh
    /// token the session is dropped first, so callers observe a signed-out client.
    /// </exception>
    public async Task<string> GetAccessTokenAsync(CancellationToken cancellationToken = default)
    {
        StoredSession session = _session
            ?? throw new RelayApiException(RelayFailure.Unauthenticated, "尚未登录。");

        if (!session.NeedsRenewal(_clock(), RenewalMargin))
        {
            return session.AccessToken;
        }

        await _renewalGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            // Re-checked inside the gate: several callers can arrive at once and
            // only the first should spend a refresh token on renewal.
            session = _session
                ?? throw new RelayApiException(RelayFailure.Unauthenticated, "尚未登录。");
            if (!session.NeedsRenewal(_clock(), RenewalMargin))
            {
                return session.AccessToken;
            }

            if (!session.CanRenew)
            {
                // Nothing to renew with. The token has run out and the user must
                // sign in again — expected, not a malfunction.
                SignOutLocally(SignOutReason.SessionExpired);
                throw new RelayApiException(RelayFailure.Unauthenticated, "登录已过期，请重新登录。");
            }

            AuthTokens renewed;
            try
            {
                renewed = await _client.RefreshAsync(session.RefreshToken, cancellationToken).ConfigureAwait(false);
            }
            catch (RelayApiException ex) when (ex.Failure == RelayFailure.NetworkUnreachable)
            {
                // Being offline is not grounds for discarding a session; the token
                // may well still be valid once the network returns.
                throw;
            }
            catch (RelayApiException)
            {
                SignOutLocally(SignOutReason.SessionExpired);
                throw;
            }

            Adopt(renewed);
            return renewed.AccessToken;
        }
        finally
        {
            _renewalGate.Release();
        }
    }

    /// <summary>
    /// Signs out: revokes the refresh token server-side, then clears local state.
    /// </summary>
    /// <remarks>
    /// Revocation is best-effort. A user who has decided to sign out must end up
    /// signed out locally even if the server cannot be reached, so a failed
    /// revocation is swallowed rather than blocking the operation.
    /// </remarks>
    public async Task SignOutAsync(CancellationToken cancellationToken = default)
    {
        string? refreshToken = _session?.RefreshToken;

        if (!string.IsNullOrWhiteSpace(refreshToken))
        {
            try
            {
                await _client.LogoutAsync(refreshToken, cancellationToken).ConfigureAwait(false);
            }
            catch (RelayApiException)
            {
            }
        }

        SignOutLocally(SignOutReason.UserRequested);
    }

    private void Adopt(AuthTokens tokens)
    {
        StoredSession session = StoredSession.FromTokens(_serverAddress, tokens, _clock());

        // A refresh reply may legitimately omit the user block; keeping the name
        // already on screen beats blanking the identity area mid-session.
        if (string.IsNullOrWhiteSpace(session.UserEmail) && _session is not null)
        {
            session = session with
            {
                UserEmail = _session.UserEmail,
                UserName = _session.UserName,
            };
        }

        // A refresh that returns no new refresh token must not silently drop the
        // one still in hand, or the session would become unrenewable.
        if (string.IsNullOrWhiteSpace(session.RefreshToken) && _session is not null)
        {
            session = session with { RefreshToken = _session.RefreshToken };
        }

        _session = session;
        LastSignOutReason = SignOutReason.None;
        _store.Save(session);
        RaiseStateChanged();
    }

    private void SignOutLocally(SignOutReason reason)
    {
        _session = null;
        LastSignOutReason = reason;
        _store.Clear();
        RaiseStateChanged();
    }

    private void RaiseStateChanged() => StateChanged?.Invoke(this, EventArgs.Empty);
}
