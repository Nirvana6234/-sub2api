namespace LanAi.Workspace.Core;

public interface IConversationIndexer
{
    Task<IReadOnlyList<ConversationRecord>> ScanAsync(
        ProjectRecord? project = null,
        CliKind? client = null,
        CancellationToken cancellationToken = default);
}
