namespace LanAi.Workspace.Core;

/// <summary>
/// A read-only index entry for a conversation owned by an official CLI.
/// </summary>
public sealed record ConversationRecord
{
    public required string Id { get; init; }

    public required string ProjectId { get; init; }

    public required CliKind NativeClient { get; init; }

    public required string NativeSessionId { get; init; }

    public string? Title { get; init; }

    public required string OriginalWorkingDirectory { get; init; }

    public DateTimeOffset CreatedAt { get; init; }

    public DateTimeOffset UpdatedAt { get; init; }

    public string? SourceProfileIdAtStart { get; init; }

    public string? LastSourceProfileId { get; init; }

    public ResumePolicy ResumePolicy { get; init; } = ResumePolicy.CurrentConnection;

    public ConversationStorageMode StorageMode { get; init; } = ConversationStorageMode.NativeIndex;

    public string? NativeFileFingerprint { get; init; }

    public ConversationStatus Status { get; init; } = ConversationStatus.Available;
}
