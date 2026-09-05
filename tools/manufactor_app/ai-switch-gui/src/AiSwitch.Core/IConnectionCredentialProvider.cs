namespace LanAi.Workspace.Core;

/// <summary>
/// Resolves a credential only at process launch time. Implementations must not log,
/// serialize, or expose the returned value to the UI.
/// </summary>
public interface IConnectionCredentialProvider
{
    ValueTask<string?> GetSecretAsync(
        string connectionProfileId,
        CliKind client,
        CancellationToken cancellationToken = default);
}
