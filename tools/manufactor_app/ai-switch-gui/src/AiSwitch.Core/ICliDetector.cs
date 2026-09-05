namespace LanAi.Workspace.Core;

public interface ICliDetector
{
    Task<IReadOnlyList<CliInstallation>> DetectAsync(
        CliKind? cli = null,
        CancellationToken cancellationToken = default);
}
