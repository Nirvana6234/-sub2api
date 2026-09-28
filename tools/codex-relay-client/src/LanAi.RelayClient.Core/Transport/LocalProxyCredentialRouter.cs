using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// One credential source for the relay and the page alike: the user's accounts on the relay
/// server by id, this machine's own official sign-ins by their reserved ids
/// (<see cref="LocalMachineAccounts"/>), and the official accounts signed in within the client
/// (<see cref="OfficialAccountIds"/>).
/// </summary>
internal sealed class LocalProxyCredentialRouter : ILocalProxyCredentialSource
{
    private readonly ILocalProxyCredentialSource _relayAccounts;
    private readonly ILocalProxyCredentialSource? _officialAccounts;

    public LocalProxyCredentialRouter(
        ILocalProxyCredentialSource relayAccounts,
        ILocalMachineAccount localCodex,
        ILocalMachineAccount localClaude,
        ILocalProxyCredentialSource? officialAccounts = null)
    {
        _relayAccounts = relayAccounts ?? throw new ArgumentNullException(nameof(relayAccounts));
        LocalCodex = localCodex ?? throw new ArgumentNullException(nameof(localCodex));
        LocalClaude = localClaude ?? throw new ArgumentNullException(nameof(localClaude));
        _officialAccounts = officialAccounts;
    }

    public ILocalMachineAccount LocalCodex { get; }

    public ILocalMachineAccount LocalClaude { get; }

    public ILocalMachineAccount LocalFor(LocalProxyKind kind) => kind == LocalProxyKind.ClaudeCode ? LocalClaude : LocalCodex;

    public Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken) =>
        accountId switch
        {
            LocalMachineAccounts.CodexId => LocalCodex.GetAsync(accountId, forceRefresh, cancellationToken),
            LocalMachineAccounts.ClaudeId => LocalClaude.GetAsync(accountId, forceRefresh, cancellationToken),
            _ when OfficialAccountIds.IsOfficial(accountId) => _officialAccounts is { } official
                ? official.GetAsync(accountId, forceRefresh, cancellationToken)
                : Task.FromException<LocalProxyCredential>(new LocalProxyCredentialException("这个客户端没有启用「在共飞里登录的账号」。")),
            _ => _relayAccounts.GetAsync(accountId, forceRefresh, cancellationToken),
        };

    /// <summary>
    /// Drops the relay tokens, which belong to the account signing out. This machine's own
    /// sign-ins do not: they are the computer user's, whoever is signed in to the client.
    /// </summary>
    public void Clear() => _relayAccounts.Clear();
}
