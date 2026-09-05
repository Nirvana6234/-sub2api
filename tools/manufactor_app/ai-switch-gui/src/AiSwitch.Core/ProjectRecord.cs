namespace LanAi.Workspace.Core;

/// <summary>
/// Describes one source-code project independently of its active CLI connection.
/// </summary>
public sealed record ProjectRecord
{
    public required string Id { get; init; }

    public required string DisplayName { get; init; }

    public required string RootPath { get; init; }

    public required string PathFingerprint { get; init; }

    public CliKind DefaultCli { get; init; } = CliKind.Codex;

    public string? DefaultConnectionProfileId { get; init; }

    public string? DefaultModel { get; init; }

    public ResumePolicy ResumePolicy { get; init; } = ResumePolicy.CurrentConnection;

    public DateTimeOffset CreatedAt { get; init; }

    public DateTimeOffset? LastOpenedAt { get; init; }

    public bool IsArchived { get; init; }
}
