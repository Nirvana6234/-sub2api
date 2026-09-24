namespace LanAi.Workspace.Core;

/// <summary>
/// The result of probing an official CLI installation and its runtime capabilities.
/// </summary>
public sealed record CliInstallation
{
    public required CliKind Kind { get; init; }

    public bool IsInstalled { get; init; }

    public string? ExecutablePath { get; init; }

    public string? Version { get; init; }

    public CliCapability Capabilities { get; init; }

    public DateTimeOffset DetectedAt { get; init; }

    public IReadOnlyList<string> AlternativeExecutablePaths { get; init; } = Array.Empty<string>();

    public bool HasPathConflict => AlternativeExecutablePaths.Count > 0;

    public bool CanRun => IsInstalled && !string.IsNullOrWhiteSpace(Version);
}
