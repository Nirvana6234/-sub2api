using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// One credential source for the relay and the page alike: this machine's own official
/// sign-ins by their reserved ids (<see cref="LocalMachineAccounts"/>), and the official
/// accounts signed in within the client (<see cref="OfficialAccountIds"/>).
/// </summary>
/// <remarks>
/// Positive ids were the user's accounts on the relay server. They are no longer used by the
/// local proxy (任务计划 D7); one arriving here — a choice saved by an older client — is refused
/// in words rather than sent anywhere.
/// </remarks>
internal sealed class LocalProxyCredentialRouter : ILocalProxyCredentialSource
{
    internal const string RelayAccountsRetired =
        "本地代理不再使用中转站上的账号。请在「本地代理」页改用这台电脑上已登录的账号，或授权共飞AI助手本地代理使用你的官方账号。";

    public LocalProxyCredentialRouter(
        ILocalMachineAccount localCodex,
        ILocalMachineAccount localClaude,
        OfficialAccountSource? official = null)
    {
        LocalCodex = localCodex ?? throw new ArgumentNullException(nameof(localCodex));
        LocalClaude = localClaude ?? throw new ArgumentNullException(nameof(localClaude));
        Official = official;
    }

    public ILocalMachineAccount LocalCodex { get; }

    public ILocalMachineAccount LocalClaude { get; }

    /// <summary>The accounts signed in within the client; null when the host supplied none.</summary>
    public OfficialAccountSource? Official { get; }

    public ILocalMachineAccount LocalFor(LocalProxyKind kind) => kind == LocalProxyKind.ClaudeCode ? LocalClaude : LocalCodex;

    public Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken) =>
        accountId switch
        {
            LocalMachineAccounts.CodexId => LocalCodex.GetAsync(accountId, forceRefresh, cancellationToken),
            LocalMachineAccounts.ClaudeId => LocalClaude.GetAsync(accountId, forceRefresh, cancellationToken),
            _ when OfficialAccountIds.IsOfficial(accountId) && Official is { } official =>
                official.GetAsync(accountId, forceRefresh, cancellationToken),
            _ when OfficialAccountIds.IsOfficial(accountId) =>
                Task.FromException<LocalProxyCredential>(new LocalProxyCredentialException("这个客户端没有启用「已授权给共飞AI助手的账号」。")),
            _ => Task.FromException<LocalProxyCredential>(new LocalProxyCredentialException(RelayAccountsRetired)),
        };

    /// <summary>
    /// Nothing to drop: this machine's sign-ins are the computer user's, whoever is signed in to
    /// the client, and the client's own are hidden by the store's scope, not by this.
    /// </summary>
    public void Clear()
    {
    }
}
