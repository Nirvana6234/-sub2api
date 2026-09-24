namespace LanAi.Workspace.Core;

/// <summary>
/// Defines how one CLI should react when a connection is selected.
/// </summary>
public sealed record ClientConnectionSettings
{
    public required CliKind Client { get; init; }

    public ClientConfigurationMode ConfigurationMode { get; init; }

    public string? ConnectionProfileId { get; init; }

    public string? DefaultModel { get; init; }
}
