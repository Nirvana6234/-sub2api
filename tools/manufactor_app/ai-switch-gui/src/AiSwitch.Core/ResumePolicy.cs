namespace LanAi.Workspace.Core;

/// <summary>
/// Determines which connection should be used when an existing conversation is resumed.
/// </summary>
public enum ResumePolicy
{
    CurrentConnection,
    PinnedConnection,
}
