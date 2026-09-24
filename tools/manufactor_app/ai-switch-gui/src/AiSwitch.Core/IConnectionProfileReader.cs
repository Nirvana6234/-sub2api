namespace LanAi.Workspace.Core;

public interface IConnectionProfileReader
{
    Task<IReadOnlyList<ConnectionProfile>> GetAllAsync(
        CancellationToken cancellationToken = default);

    Task<ConnectionProfile?> GetByIdAsync(
        string id,
        CancellationToken cancellationToken = default);
}
