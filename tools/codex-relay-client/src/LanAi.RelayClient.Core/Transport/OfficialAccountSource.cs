using System.Collections.Concurrent;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// The official accounts signed in within the client, as local-proxy credentials (任务计划 §3.5, A4).
/// </summary>
/// <remarks>
/// <para>
/// <b>The only refresher.</b> These sign-ins were made by the client itself, so nothing else
/// holds their refresh tokens — unlike this machine's own sign-ins, there is nobody to race.
/// Both official refresh tokens work once, so a refresh is done at most once at a time per
/// account, and at most once a minute when the official side keeps refusing the token.
/// </para>
/// <para>
/// <b>Written before it is used.</b> A refreshed pair goes into the store first; only if the
/// account still holds the refresh token that was spent (<see cref="OfficialAccountStore.Update"/>)
/// — a sign-in made again meanwhile wins. When the write fails the old refresh token is already
/// spent, so the new pair is kept in memory, used, and written again on every later request
/// until it lands.
/// </para>
/// <para>
/// <b>When the sign-in is gone</b> (refused for good: used, revoked, expired) the account is
/// marked so on the page and the user is asked to sign in again; nothing falls back to the
/// relay server.
/// </para>
/// </remarks>
internal sealed class OfficialAccountSource : ILocalProxyCredentialSource
{
    private static readonly TimeSpan ExpirySkew = TimeSpan.FromMinutes(5);
    private static readonly TimeSpan ForcedRefreshQuietPeriod = TimeSpan.FromSeconds(60);

    private readonly OfficialAccountStore _store;
    private readonly IOfficialTokenRefresher _refresher;
    private readonly Func<DateTimeOffset> _clock;
    private readonly ConcurrentDictionary<long, SemaphoreSlim> _gates = new();
    private readonly ConcurrentDictionary<long, DateTimeOffset> _lastRefreshAt = new();

    /// <summary>Refreshed accounts not yet written, with the refresh token the store still holds for each.</summary>
    private readonly ConcurrentDictionary<long, (string StoredRefreshToken, OfficialAccount Refreshed)> _unsaved = new();

    public OfficialAccountSource(OfficialAccountStore store, IOfficialTokenRefresher refresher, Func<DateTimeOffset>? clock = null)
    {
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _refresher = refresher ?? throw new ArgumentNullException(nameof(refresher));
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    public OfficialAccountStore Store => _store;

    public async Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken)
    {
        SemaphoreSlim gate = _gates.GetOrAdd(accountId, _ => new SemaphoreSlim(1, 1));
        await gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            SaveUnsaved(accountId);
            (OfficialAccount account, string stored) = Current(accountId);
            if (!account.IsValid)
            {
                throw new LocalProxyCredentialException(GoneMessage(account));
            }

            DateTimeOffset now = _clock();
            DateTimeOffset? expiresAt = account.ExpiresAt ?? JwtPayload.ExpiresAt(account.AccessToken);
            bool expiring = account.AccessToken.Length == 0 || (expiresAt is { } expires && expires - ExpirySkew <= now);
            bool forced = forceRefresh &&
                          !(_lastRefreshAt.TryGetValue(accountId, out DateTimeOffset last) && now - last < ForcedRefreshQuietPeriod);
            if (expiring || forced)
            {
                account = await RefreshAsync(account, stored, cancellationToken).ConfigureAwait(false);
            }

            return ToCredential(account);
        }
        finally
        {
            gate.Release();
        }
    }

    /// <summary>
    /// Nothing is dropped: a pair that could not be written must outlive a sign-out of the
    /// relay, or the account it belongs to is lost for good.
    /// </summary>
    public void Clear()
    {
    }

    /// <summary>The stored account, with a refreshed pair that has not reached the disk yet laid over it.</summary>
    private (OfficialAccount Account, string StoredRefreshToken) Current(long accountId)
    {
        OfficialAccount stored = _store.Get(accountId)
            ?? throw new LocalProxyCredentialException("这个在共飞里登录的账号已被删除，请在本地代理页另选一个账号或改回经中转站。");
        return _unsaved.TryGetValue(accountId, out var pending) && pending.StoredRefreshToken == stored.RefreshToken
            ? (pending.Refreshed, stored.RefreshToken)
            : (stored, stored.RefreshToken);
    }

    private async Task<OfficialAccount> RefreshAsync(OfficialAccount account, string storedRefreshToken, CancellationToken cancellationToken)
    {
        OfficialAccount refreshed;
        try
        {
            refreshed = account.Kind == LocalProxyKind.ClaudeCode
                ? Apply(account, await _refresher.RefreshClaudeAsync(account.RefreshToken, cancellationToken).ConfigureAwait(false))
                : Apply(account, await _refresher.RefreshCodexAsync(account.RefreshToken, cancellationToken).ConfigureAwait(false));
        }
        catch (LocalProxyCredentialException ex) when (ex.SignInGone)
        {
            OfficialAccount gone = account with { InvalidReason = "登录已失效（可能在别处退出、修改了密码或被官方收回）" };
            TryWrite(account.Id, storedRefreshToken, gone);
            _unsaved.TryRemove(account.Id, out _);
            ClientLog.Warning($"在共飞里登录的官方账号 {account.Id} 登录已失效");
            throw new LocalProxyCredentialException(GoneMessage(gone), ex);
        }
        catch (LocalProxyCredentialException ex)
        {
            // The refresher speaks of "本机" sign-ins; these are the client's own.
            throw new LocalProxyCredentialException(ex.UserMessage.Replace("本机 ", " ", StringComparison.Ordinal).Trim(), ex);
        }
        finally
        {
            _lastRefreshAt[account.Id] = _clock();
        }

        try
        {
            if (!_store.Update(account.Id, storedRefreshToken, _ => refreshed))
            {
                // Signed in again (or removed) while this refresh ran: that sign-in is the user's choice now.
                _unsaved.TryRemove(account.Id, out _);
                ClientLog.Info($"在共飞里登录的官方账号 {account.Id} 在刷新期间已重新登录，改用新的登录");
                return _store.Get(account.Id)
                    ?? throw new LocalProxyCredentialException("这个在共飞里登录的账号已被删除，请在本地代理页另选一个账号或改回经中转站。");
            }

            _unsaved.TryRemove(account.Id, out _);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            _unsaved[account.Id] = (storedRefreshToken, refreshed);
            ClientLog.Warning($"在共飞里登录的官方账号 {account.Id} 刷新后暂时没能保存，下一次请求会再试", ex);
        }

        return refreshed;
    }

    private void SaveUnsaved(long accountId)
    {
        if (!_unsaved.TryGetValue(accountId, out var pending))
        {
            return;
        }

        try
        {
            if (!_store.Update(accountId, pending.StoredRefreshToken, _ => pending.Refreshed))
            {
                // Signed in again meanwhile: the pair in memory belongs to a sign-in that no longer matters.
                _unsaved.TryRemove(accountId, out _);
                return;
            }

            _unsaved.TryRemove(accountId, out _);
            ClientLog.Info($"在共飞里登录的官方账号 {accountId} 刷新后的登录已补存");
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning($"在共飞里登录的官方账号 {accountId} 刷新后的登录仍未能保存", ex);
        }
    }

    private void TryWrite(long accountId, string storedRefreshToken, OfficialAccount account)
    {
        try
        {
            _store.Update(accountId, storedRefreshToken, _ => account);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning($"记录官方账号 {accountId} 的状态失败", ex);
        }
    }

    private OfficialAccount Apply(OfficialAccount account, CodexRefreshedTokens tokens)
    {
        string idToken = tokens.IdToken ?? account.IdToken;
        return account with
        {
            AccessToken = tokens.AccessToken,
            RefreshToken = tokens.RefreshToken ?? account.RefreshToken,
            IdToken = idToken,
            ExpiresAt = JwtPayload.ExpiresAt(tokens.AccessToken),
            Email = ChatGptIdToken.Email(idToken) is { Length: > 0 } email ? email : account.Email,
            PlanType = ChatGptIdToken.PlanType(idToken) is { Length: > 0 } plan ? plan : account.PlanType,
            ChatGptAccountId = ChatGptIdToken.AccountId(idToken) is { Length: > 0 } id ? id : account.ChatGptAccountId,
            FedRamp = tokens.IdToken is null ? account.FedRamp : ChatGptIdToken.FedRamp(idToken),
            LastRefreshAt = _clock(),
        };
    }

    private OfficialAccount Apply(OfficialAccount account, ClaudeRefreshedTokens tokens) => account with
    {
        AccessToken = tokens.AccessToken,
        RefreshToken = tokens.RefreshToken ?? account.RefreshToken,
        ExpiresAt = tokens.ExpiresInSeconds is { } seconds ? _clock().AddSeconds(seconds) : null,
        Scope = tokens.Scope ?? account.Scope,
        LastRefreshAt = _clock(),
    };

    private static LocalProxyCredential ToCredential(OfficialAccount account) => new(
        accountId: account.Id,
        name: account.DisplayName,
        platform: account.Platform,
        accessToken: account.AccessToken,
        expiresAt: account.ExpiresAt ?? JwtPayload.ExpiresAt(account.AccessToken),
        chatgptAccountId: account.ChatGptAccountId,
        fedramp: account.FedRamp);

    private static string GoneMessage(OfficialAccount account)
    {
        string product = account.Kind == LocalProxyKind.ClaudeCode ? "Claude" : "ChatGPT";
        string reason = account.InvalidReason.Length > 0 ? account.InvalidReason : "登录已失效";
        return $"在共飞里登录的 {product} 账号「{account.DisplayName}」{reason}，请在本地代理页点「重新登录」。未切回中转站。";
    }
}
