using System.IO;
using System.Security.Cryptography;
using System.Text.Json.Nodes;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// Codex's own ChatGPT sign-in on this machine, as a local-proxy account.
/// </summary>
/// <remarks>
/// <para>
/// <b>Who refreshes.</b> While Codex points at the relay its sign-in sits in the client's
/// snapshot, out of Codex's sight — so this is the only thing that refreshes it, and there is
/// nothing to race. OpenAI refresh tokens work once: a new pair is written back into every
/// copy (<see cref="ICodexLoginStore.UpdateLoginTokens"/>) before it is used, so the sign-in
/// handed back to Codex on exit is the current one.
/// </para>
/// <para>
/// <b>When saving fails.</b> The old refresh token is already spent by then, so the new pair
/// is kept in memory, used, and written again on every later request until it lands.
/// </para>
/// </remarks>
internal sealed class LocalCodexAccount : ILocalMachineAccount
{
    private static readonly TimeSpan ExpirySkew = TimeSpan.FromMinutes(5);

    /// <summary>
    /// Several turns can be refused with the same expired token at once; each asks for a
    /// forced refresh. One refresh answers them all, instead of spending a token per turn.
    /// </summary>
    private static readonly TimeSpan ForcedRefreshQuietPeriod = TimeSpan.FromSeconds(60);

    internal const string NotSignedInDetail =
        "没有找到本机 Codex 的 ChatGPT 登录。请在终端执行 codex login 登录 ChatGPT（客户端开着也可以），登录后这里会自动识别。";

    private readonly ICodexLoginStore _store;
    private readonly IOfficialTokenRefresher _refresher;
    private readonly Func<DateTimeOffset> _clock;
    private readonly SemaphoreSlim _gate = new(1, 1);

    private (string PreviousRefreshToken, CodexRefreshedTokens Tokens)? _unsaved;
    private DateTimeOffset? _lastRefreshAt;

    public LocalCodexAccount(ICodexLoginStore store, IOfficialTokenRefresher refresher, Func<DateTimeOffset>? clock = null)
    {
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _refresher = refresher ?? throw new ArgumentNullException(nameof(refresher));
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    public LocalProxyKind Kind => LocalProxyKind.Codex;

    public LocalMachineAccountStatus Probe()
    {
        CodexLogin? login;
        try
        {
            login = _store.ReadLogin();
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or InvalidDataException or CryptographicException)
        {
            ClientLog.Warning("读取本机 Codex 登录失败", ex);
            return new LocalMachineAccountStatus(LocalMachineAccountState.NotSignedIn, "本机 ChatGPT 登录", "读取本机 Codex 登录失败，请稍后再试。");
        }

        return login is null || login.RefreshToken.Length == 0
            ? new LocalMachineAccountStatus(LocalMachineAccountState.NotSignedIn, "本机 ChatGPT 登录", NotSignedInDetail)
            : new LocalMachineAccountStatus(LocalMachineAccountState.SignedIn, DisplayName(login.IdToken), string.Empty);
    }

    public async Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken)
    {
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            SaveUnsaved();
            CodexLogin login = Current();

            bool expiring = login.AccessToken.Length == 0 ||
                            JwtPayload.ExpiresAt(login.AccessToken) is { } expires && expires - ExpirySkew <= _clock();
            bool forced = forceRefresh && !(_lastRefreshAt is { } last && _clock() - last < ForcedRefreshQuietPeriod);
            if (expiring || forced)
            {
                login = await RefreshAsync(login, cancellationToken).ConfigureAwait(false);
            }

            return ToCredential(login);
        }
        finally
        {
            _gate.Release();
        }
    }

    /// <summary>Nothing is cached but a pair that could not be saved, and that must outlive a sign-out of the relay.</summary>
    public void Clear()
    {
    }

    /// <summary>The stored sign-in, with a refreshed pair that has not reached the disk yet laid over it.</summary>
    private CodexLogin Current()
    {
        CodexLogin login;
        try
        {
            login = _store.ReadLogin() ?? throw new LocalProxyCredentialException(NotSignedInDetail);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or InvalidDataException or CryptographicException)
        {
            throw new LocalProxyCredentialException("读取本机 Codex 登录失败，请稍后再试。", ex);
        }

        if (_unsaved is { } pending && login.RefreshToken == pending.PreviousRefreshToken)
        {
            login = Apply(login, pending.Tokens);
        }

        return login;
    }

    private async Task<CodexLogin> RefreshAsync(CodexLogin login, CancellationToken cancellationToken)
    {
        if (login.RefreshToken.Length == 0)
        {
            throw new LocalProxyCredentialException(NotSignedInDetail);
        }

        CodexRefreshedTokens tokens = await _refresher.RefreshCodexAsync(login.RefreshToken, cancellationToken).ConfigureAwait(false);
        DateTimeOffset now = _clock();
        _lastRefreshAt = now;
        CodexLogin refreshed = Apply(login, tokens);

        // The pair being replaced may itself be one that never reached the disk.
        string stored = _unsaved is { } pending && pending.Tokens.RefreshToken == login.RefreshToken
            ? pending.PreviousRefreshToken
            : login.RefreshToken;
        try
        {
            if (!_store.UpdateLoginTokens(stored, Merge(_unsaved, stored, tokens), now))
            {
                // Signed in again meanwhile: the new sign-in is the user's choice now.
                _unsaved = null;
                ClientLog.Info("本机 Codex 登录在刷新期间已更换，改用新的登录");
                return _store.ReadLogin() ?? refreshed;
            }

            _unsaved = null;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or InvalidDataException or CryptographicException)
        {
            _unsaved = (stored, Merge(_unsaved, stored, tokens));
            ClientLog.Warning("刷新后的本机 Codex 登录暂时没能保存，下一次请求会再试", ex);
        }

        return refreshed;
    }

    private void SaveUnsaved()
    {
        if (_unsaved is not { } pending)
        {
            return;
        }

        try
        {
            _store.UpdateLoginTokens(pending.PreviousRefreshToken, pending.Tokens, _clock());
            _unsaved = null;
            ClientLog.Info("刷新后的本机 Codex 登录已补存");
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or InvalidDataException or CryptographicException)
        {
            ClientLog.Warning("刷新后的本机 Codex 登录仍未能保存", ex);
        }
    }

    /// <summary>A second refresh over an unsaved first keeps the first's id token when the second sends none.</summary>
    private static CodexRefreshedTokens Merge(
        (string PreviousRefreshToken, CodexRefreshedTokens Tokens)? unsaved,
        string stored,
        CodexRefreshedTokens latest) =>
        unsaved is { } pending && pending.PreviousRefreshToken == stored
            ? latest with
            {
                RefreshToken = latest.RefreshToken ?? pending.Tokens.RefreshToken,
                IdToken = latest.IdToken ?? pending.Tokens.IdToken,
            }
            : latest;

    private static CodexLogin Apply(CodexLogin login, CodexRefreshedTokens tokens) => login with
    {
        AccessToken = tokens.AccessToken,
        RefreshToken = tokens.RefreshToken ?? login.RefreshToken,
        IdToken = tokens.IdToken ?? login.IdToken,
    };

    private static LocalProxyCredential ToCredential(CodexLogin login) => new(
        accountId: LocalMachineAccounts.CodexId,
        name: DisplayName(login.IdToken),
        platform: "openai",
        accessToken: login.AccessToken,
        expiresAt: JwtPayload.ExpiresAt(login.AccessToken),
        chatgptAccountId: login.AccountId.Length > 0 ? login.AccountId : ChatGptIdToken.AccountId(login.IdToken),
        fedramp: ChatGptIdToken.FedRamp(login.IdToken));

    /// <summary>本机 ChatGPT 登录（email · Plus）, from the id token's claims.</summary>
    internal static string DisplayName(string idToken)
    {
        string label = string.Join(" · ", new[] { ChatGptIdToken.Email(idToken), ChatGptIdToken.PlanLabel(ChatGptIdToken.PlanType(idToken)) }
            .Where(part => part.Length > 0));
        return label.Length > 0 ? $"本机 ChatGPT 登录（{label}）" : "本机 ChatGPT 登录";
    }
}
