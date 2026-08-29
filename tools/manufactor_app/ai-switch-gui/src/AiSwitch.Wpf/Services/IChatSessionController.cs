using LanAi.Workspace.Chat;
using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Services;

internal interface IChatSessionController : IAsyncDisposable
{
    ChatEngineState State { get; }

    string? NativeSessionId { get; }

    string? ActiveProjectFingerprint { get; }

    event EventHandler<ChatEvent>? EventReceived;

    Task ConnectAsync(
        ChatLaunchIntent intent,
        CancellationToken cancellationToken = default);

    Task SendAsync(
        ChatLaunchIntent intent,
        string message,
        CancellationToken cancellationToken = default);

    Task RespondToApprovalAsync(
        string requestId,
        ChatApprovalDecision decision,
        CancellationToken cancellationToken = default);

    Task RespondToUserInputAsync(
        string requestId,
        string response,
        CancellationToken cancellationToken = default);

    Task CancelTurnAsync(CancellationToken cancellationToken = default);

    Task ResetAsync(CancellationToken cancellationToken = default);
}
