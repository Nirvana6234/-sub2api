namespace LanAi.RelayClient.CodexBinding.DesktopSync;

/// <summary>What one entry on the phone is.</summary>
public enum SyncItemKind
{
    /// <summary>A message into the conversation: typed on the desktop, or delegated in (the phone's arrive this way).</summary>
    User,

    /// <summary>The agent saying what it is about to do.</summary>
    Progress,

    /// <summary>The agent's final answer for the turn; rendered as Markdown.</summary>
    Reply,

    /// <summary>A reasoning summary. The encrypted reasoning itself never leaves the machine.</summary>
    Thinking,

    Command,
    FileChange,

    /// <summary>MCP calls, web searches and other tool calls: a one-line summary, no results.</summary>
    Tool,

    Image,

    /// <summary>
    /// A command that has started and not finished. The rollout writes the tool call when
    /// it is issued but the completed item only at the end; this is built from the former.
    /// A client drops it when anything later arrives in the same turn.
    /// </summary>
    Running,

    /// <summary>A divider, e.g. the context was compacted.</summary>
    Notice,

    TurnStarted,
    TurnEnded,

    /// <summary>An item type this client does not know. Shown, never silently dropped.</summary>
    Unknown,
}

public enum UserMessageOrigin
{
    /// <summary>Typed into the desktop app.</summary>
    Desktop,

    /// <summary>
    /// Arrived through <c>send_message_to_thread</c>: from the phone, or from another
    /// agent. The rollout cannot tell which; matching against the send log can.
    /// </summary>
    Delegated,
}

public enum TurnOutcome
{
    Completed,
    Failed,
    Aborted,
}

/// <summary>One file touched by a <see cref="SyncItemKind.FileChange"/>.</summary>
/// <param name="Path">Relative to the conversation's working directory when it lies inside it.</param>
/// <param name="Change"><c>add</c>, <c>update</c> or <c>delete</c>, as Codex writes it.</param>
public sealed record SyncFileChange(string Path, string Change, int Added, int Removed);

/// <summary>
/// One entry of a conversation as the phone sees it: the projection of one rollout line.
/// </summary>
/// <remarks>
/// A flat record rather than a hierarchy so that one serializer covers every kind; the
/// members a kind does not use stay null. <see cref="Seq"/> is the byte offset of the
/// line in the rollout, which is unique, increases, and doubles as the resume cursor.
/// </remarks>
public sealed record SyncItem(long Seq, string? TurnId, string ItemId, SyncItemKind Kind)
{
    /// <summary>Message, reply, reasoning summary, notice or tool summary text; for a failed turn, the error.</summary>
    public string? Text { get; init; }

    public UserMessageOrigin? Origin { get; init; }

    /// <summary>For a delegated message, the conversation named as its caller.</summary>
    public string? SourceThreadId { get; init; }

    /// <summary>Images attached to a user message; fetched one by one through the detail call.</summary>
    public int ImageCount { get; init; }

    public string? Command { get; init; }
    public int? ExitCode { get; init; }

    /// <summary>The item's own status, e.g. <c>completed</c>, <c>failed</c>, <c>declined</c>.</summary>
    public string? Status { get; init; }

    public long? DurationMs { get; init; }

    /// <summary>The tail of a command's output. The rest is fetched on demand.</summary>
    public string? OutputPreview { get; init; }

    public bool OutputTruncated { get; init; }

    public IReadOnlyList<SyncFileChange>? Files { get; init; }

    public TurnOutcome? Outcome { get; init; }
}
