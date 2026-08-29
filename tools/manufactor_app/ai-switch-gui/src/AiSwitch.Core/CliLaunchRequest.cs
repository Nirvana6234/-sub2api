namespace LanAi.Workspace.Core;

/// <summary>
/// Describes a CLI process launch without carrying plaintext credentials.
/// </summary>
public sealed record CliLaunchRequest
{
    public required string ProjectId { get; init; }

    public required CliKind Cli { get; init; }

    public required string WorkingDirectory { get; init; }

    public CliLaunchMode Mode { get; init; } = CliLaunchMode.New;

    public string? ConnectionProfileId { get; init; }

    public string? Model { get; init; }

    public string? ConversationId { get; init; }

    public string? NativeSessionId { get; init; }

    public ResumePolicy ResumePolicy { get; init; } = ResumePolicy.CurrentConnection;

    public IReadOnlyList<string> AdditionalArguments { get; init; } = Array.Empty<string>();
}
