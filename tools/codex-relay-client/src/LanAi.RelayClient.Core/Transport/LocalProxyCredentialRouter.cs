using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// One credential source for the relay and the page alike: the user's accounts on the relay
/// server by id, and this machine's own official sign-ins by their reserved ids
/// (<see cref="LocalMachineAccounts"/>).
/// </summary>
internal sealed class LocalProxyCredentialRouter : ILocalProxyCredentialSource
{
    private readonly ILocalProxyCredentialSource _relayAccounts;

    public LocalProxyCredentialRouter(
        ILocalProxyCredentialSource relayAccounts,
        ILocalMachineAccount localCodex,
        ILocalMachineAccount localClaude)
    {
        _relayAccounts = relayAccounts ?? throw new ArgumentNullException(nameof(relayAccounts));
        LocalCodex = localCodex ?? throw new ArgumentNullException(nameof(localCodex));
        LocalClaude = localClaude ?? throw new ArgumentNullException(nameof(localClaude));
    }

    public ILocalMachineAccount LocalCodex { get; }

    public ILocalMachineAccount LocalClaude { get; }

    public ILocalMachineAccount LocalFor(LocalProxyKind kind) => kind == LocalProxyKind.ClaudeCode ? LocalClaude : LocalCodex;

    public Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken) =>
        accountId switch
        {
            LocalMachineAccounts.CodexId => LocalCodex.GetAsync(accountId, forceRefresh, cancellationToken),
            LocalMachineAccounts.ClaudeId => LocalClaude.GetAsync(accountId, forceRefresh, cancellationToken),
            _ => _relayAccounts.GetAsync(accountId, forceRefresh, cancellationToken),
        };

    /// <summary>
    /// Drops the relay tokens, which belong to the account signing out. This machine's own
    /// sign-ins do not: they are the computer user's, whoever is signed in to the client.
    /// </summary>
    public void Clear() => _relayAccounts.Clear();
}
