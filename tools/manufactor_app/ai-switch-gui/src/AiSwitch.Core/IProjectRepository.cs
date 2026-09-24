namespace LanAi.Workspace.Core;

public interface IProjectRepository
{
    Task<IReadOnlyList<ProjectRecord>> GetAllAsync(
        bool includeArchived = false,
        CancellationToken cancellationToken = default);

    Task<ProjectRecord?> GetByIdAsync(
        string id,
        CancellationToken cancellationToken = default);

    Task UpsertAsync(
        ProjectRecord project,
        CancellationToken cancellationToken = default);

    Task<bool> DeleteAsync(
        string id,
        CancellationToken cancellationToken = default);
}
