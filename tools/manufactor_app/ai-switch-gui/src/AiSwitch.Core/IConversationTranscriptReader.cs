namespace LanAi.Workspace.Core;

/// <summary>
/// A display-safe message read from an official CLI transcript. Implementations
/// must not persist message bodies or expose hidden reasoning and raw tool output.
/// </summary>
public sealed record ConversationTranscriptMessage(
    string Id,
    ConversationTranscriptRole Role,
    string Text,
    DateTimeOffset Timestamp,
    string? Title = null);

public enum ConversationTranscriptRole
{
    User,
    Assistant,
    Tool,
    System,
}

public sealed record ConversationTranscript(
    bool SourceFound,
    IReadOnlyList<ConversationTranscriptMessage> Messages,
    IReadOnlyList<string> Warnings)
{
    public static ConversationTranscript NotFound(string warning) =>
        new(false, Array.Empty<ConversationTranscriptMessage>(), [warning]);
}

/// <summary>
/// Reads the visible user/assistant history for one native conversation on
/// demand. Transcript bodies remain in official storage and are never copied
/// into the workspace database.
/// </summary>
public interface IConversationTranscriptReader
{
    Task<ConversationTranscript> ReadAsync(
        ConversationRecord conversation,
        CancellationToken cancellationToken = default);
}
