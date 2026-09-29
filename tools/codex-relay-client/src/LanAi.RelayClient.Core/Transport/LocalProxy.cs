using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Transport;

/// <summary>The account one tool's traffic goes to while its local proxy is on.</summary>
public sealed record LocalProxyTarget(long AccountId, string Name);

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

/// <summary>Supplies the access token for one local-proxy account: this machine's sign-in, or one signed in within the client.</summary>
internal interface ILocalProxyCredentialSource
{
    /// <param name="forceRefresh">After the official API refused the cached token.</param>
    Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken);

    /// <summary>Drops everything cached, for the next account to sign in.</summary>
    void Clear();
}

/// <summary>A credential source with no accounts: the default when the host supplies none.</summary>
internal sealed class NoLocalProxyAccounts : ILocalProxyCredentialSource
{
    public static NoLocalProxyAccounts Instance { get; } = new();

    public Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken) =>
        Task.FromException<LocalProxyCredential>(new LocalProxyCredentialException("本地代理没有可用的账号。"));

    public void Clear()
    {
    }
}
