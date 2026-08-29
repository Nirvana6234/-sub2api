using LanAi.Workspace.Core;

namespace LanAi.Workspace.Chat;

public enum ChatEngineState
{
    Created,
    Starting,
    Ready,
    RunningTurn,
    WaitingForApproval,
    Stopping,
    Stopped,
    Faulted,
}

public enum ChatPermissionMode
{
    ReadOnly,
    WorkspaceWrite,
    FullAccess,
}

public enum ChatApprovalDecision
{
    Deny,
    AllowOnce,
    AllowForSession,
}

public sealed record ChatEngineContext
{
    public required CliLaunchRequest LaunchRequest { get; init; }

    public required CliInstallation Installation { get; init; }

    public ConnectionProfile? Connection { get; init; }

    public ChatPermissionMode PermissionMode { get; init; } = ChatPermissionMode.WorkspaceWrite;
}

public abstract record ChatEvent(DateTimeOffset Timestamp);

public sealed record ChatEngineStateEvent(
    ChatEngineState State,
    string Message,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public sealed record ChatSessionStartedEvent(
    string NativeSessionId,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public sealed record ChatAssistantDeltaEvent(
    string Text,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public sealed record ChatAssistantMessageEvent(
    string Text,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public sealed record ChatToolStartedEvent(
    string ToolCallId,
    string ToolName,
    string? Summary,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public sealed record ChatToolProgressEvent(
    string ToolCallId,
    string Message,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public sealed record ChatToolCompletedEvent(
    string ToolCallId,
    string ToolName,
    bool Succeeded,
    string? Summary,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public sealed record ChatApprovalRequestedEvent(
    string RequestId,
    string Title,
    string Detail,
    IReadOnlyList<ChatApprovalDecision> AllowedDecisions,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public sealed record ChatUserInputRequestedEvent(
    string RequestId,
    string Prompt,
    IReadOnlyList<string> Options,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public sealed record ChatUsageEvent(
    long? InputTokens,
    long? OutputTokens,
    long? CachedInputTokens,
    DateTimeOffset Timestamp,
    long? CacheCreationTokens = null) : ChatEvent(Timestamp);

public sealed record ChatTurnCompletedEvent(
    bool Succeeded,
    string? ErrorMessage,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public sealed record ChatErrorEvent(
    string Code,
    string Message,
    DateTimeOffset Timestamp) : ChatEvent(Timestamp);

public interface IChatEngine : IAsyncDisposable
{
    CliKind Kind { get; }

    ChatEngineState State { get; }

    string? NativeSessionId { get; }

    event EventHandler<ChatEvent>? EventReceived;

    Task StartAsync(ChatEngineContext context, CancellationToken cancellationToken = default);

    Task SendMessageAsync(string message, CancellationToken cancellationToken = default);

    Task RespondToApprovalAsync(
        string requestId,
        ChatApprovalDecision decision,
        CancellationToken cancellationToken = default);

    Task RespondToUserInputAsync(
        string requestId,
        string response,
        CancellationToken cancellationToken = default);

    Task CancelTurnAsync(CancellationToken cancellationToken = default);

    Task StopAsync(CancellationToken cancellationToken = default);
}
