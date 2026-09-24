namespace LanAi.Workspace.Core;

/// <summary>
/// Permanently removes conversations from the official CLI-owned stores.
/// Implementations must never delete the project's source directory.
/// </summary>
public interface IConversationDeletionService
{
    Task<ConversationDeletionResult> DeleteProjectConversationsAsync(
        ProjectRecord project,
        CancellationToken cancellationToken = default);
}

public sealed record ConversationDeletionIssue(
    CliKind Client,
    string Item,
    string Message);

public sealed record CliConversationDeletionResult(
    CliKind Client,
    int MatchedCount,
    int DeletedCount,
    IReadOnlyList<ConversationDeletionIssue> Issues)
{
    public bool Succeeded =>
        MatchedCount >= 0 &&
        DeletedCount >= 0 &&
        DeletedCount == MatchedCount &&
        Issues.Count == 0;
}

public sealed record ConversationDeletionResult(
    IReadOnlyList<CliConversationDeletionResult> Clients)
{
    public static readonly IReadOnlyList<CliKind> RequiredOfficialClients =
    [
        CliKind.Codex,
        CliKind.ClaudeCode,
        CliKind.GeminiCli,
    ];

    public bool Succeeded
    {
        get
        {
            IReadOnlyList<CliKind> requiredClients = RequiredOfficialClients;
            return Clients.Count == requiredClients.Count &&
                   requiredClients.All(required =>
                       Clients.Count(client => client.Client == required) == 1) &&
                   Clients.All(client => client.Succeeded);
        }
    }

    public int MatchedCount => Clients.Sum(client => client.MatchedCount);

    public int DeletedCount => Clients.Sum(client => client.DeletedCount);

    public IReadOnlyList<ConversationDeletionIssue> Issues => Clients
        .SelectMany(client => client.Issues)
        .ToArray();
}
