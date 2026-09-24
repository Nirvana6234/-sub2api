using System.IO;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;

namespace LanAi.Workspace.Wpf.Services;

/// <summary>
/// Coordinates the read-only native indexes with the workspace-owned project database.
/// Long-running probes are independent and execute concurrently; one failed source does
/// not hide data from the remaining sources.
/// </summary>
internal sealed class WorkspaceDataService : IDisposable
{
    private readonly IProjectRepository _projectRepository;
    private readonly IConversationIndexer _conversationIndexer;
    private readonly IConversationDeletionService _conversationDeletionService;
    private readonly ICliDetector _cliDetector;
    private readonly LegacyProfileReader _legacyProfileReader;
    private readonly LegacyProfileEditor _legacyProfileEditor;
    private readonly SemaphoreSlim _loadGate = new(1, 1);
    private bool _disposed;

    public WorkspaceDataService()
        : this(AppDataPaths.CreateDefault())
    {
    }

    public WorkspaceDataService(AppDataPaths paths)
        : this(
            new SqliteProjectRepository(paths ?? throw new ArgumentNullException(nameof(paths))),
            new CompositeConversationIndexer(paths),
            new OfficialConversationDeletionService(paths),
            new CliDetector(),
            new LegacyProfileReader(paths))
    {
    }

    internal WorkspaceDataService(
        IProjectRepository projectRepository,
        IConversationIndexer conversationIndexer,
        IConversationDeletionService conversationDeletionService,
        ICliDetector cliDetector,
        LegacyProfileReader legacyProfileReader)
    {
        _projectRepository = projectRepository ?? throw new ArgumentNullException(nameof(projectRepository));
        _conversationIndexer = conversationIndexer ?? throw new ArgumentNullException(nameof(conversationIndexer));
        _conversationDeletionService = conversationDeletionService
            ?? throw new ArgumentNullException(nameof(conversationDeletionService));
        _cliDetector = cliDetector ?? throw new ArgumentNullException(nameof(cliDetector));
        _legacyProfileReader = legacyProfileReader ?? throw new ArgumentNullException(nameof(legacyProfileReader));
        _legacyProfileEditor = new LegacyProfileEditor(_legacyProfileReader);
    }

    public IConnectionCredentialProvider CredentialProvider => _legacyProfileReader;

    public IConnectionProfileReader ConnectionProfileReader => _legacyProfileReader;

    /// <summary>
    /// Mutates the same legacy document read by the workspace, terminal and
    /// chat engines, preventing the still-supported WinForms manager from
    /// drifting onto a second configuration store.
    /// </summary>
    public IConnectionProfileEditor ConnectionProfileEditor => _legacyProfileEditor;

    public async Task<WorkspaceDataSnapshot> LoadAsync(
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        await _loadGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            Task<LoadPart<IReadOnlyList<ProjectRecord>>> projectsTask = CaptureAsync(
                "项目数据库",
                () => _projectRepository.GetAllAsync(cancellationToken: cancellationToken),
                cancellationToken);
            Task<LoadPart<IReadOnlyList<ConversationRecord>>> conversationsTask = CaptureAsync(
                "历史索引",
                () => _conversationIndexer.ScanAsync(cancellationToken: cancellationToken),
                cancellationToken);
            Task<LoadPart<IReadOnlyList<CliInstallation>>> installationsTask = CaptureAsync(
                "CLI 检测",
                () => _cliDetector.DetectAsync(cancellationToken: cancellationToken),
                cancellationToken);
            Task<LoadPart<IReadOnlyList<ConnectionProfile>>> connectionsTask = CaptureAsync(
                "旧连接配置",
                () => _legacyProfileReader.GetAllAsync(cancellationToken),
                cancellationToken);
            Task<LoadPart<ConnectionProfileSelection>> selectionTask = CaptureAsync(
                "连接选择",
                () => _legacyProfileEditor.GetSelectionAsync(cancellationToken),
                cancellationToken);
            Task<LoadPart<ConnectionProfileRouting>> routingTask = CaptureAsync(
                "客户端分流",
                () => _legacyProfileEditor.GetRoutingAsync(cancellationToken),
                cancellationToken);

            await Task.WhenAll(
                    new Task[] { projectsTask, conversationsTask, installationsTask, connectionsTask, selectionTask, routingTask })
                .ConfigureAwait(false);

            LoadPart<IReadOnlyList<ProjectRecord>> projectsPart = await projectsTask.ConfigureAwait(false);
            LoadPart<IReadOnlyList<ConversationRecord>> conversationsPart = await conversationsTask.ConfigureAwait(false);
            LoadPart<IReadOnlyList<CliInstallation>> installationsPart = await installationsTask.ConfigureAwait(false);
            LoadPart<IReadOnlyList<ConnectionProfile>> connectionsPart = await connectionsTask.ConfigureAwait(false);
            LoadPart<ConnectionProfileSelection> selectionPart = await selectionTask.ConfigureAwait(false);
            LoadPart<ConnectionProfileRouting> routingPart = await routingTask.ConfigureAwait(false);

            var errors = new List<WorkspaceLoadError>();
            AddError(projectsPart, errors);
            AddError(conversationsPart, errors);
            AddError(installationsPart, errors);
            AddError(connectionsPart, errors);
            AddError(selectionPart, errors);
            AddError(routingPart, errors);

            IReadOnlyList<ProjectRecord> projects = projectsPart.Value ?? Array.Empty<ProjectRecord>();
            IReadOnlyList<ConversationRecord> conversations = conversationsPart.Value ?? Array.Empty<ConversationRecord>();
            int discoveredProjects = 0;

            if (projectsPart.Error is null && conversationsPart.Error is null)
            {
                LoadPart<int> discoveryPart = await CaptureAsync(
                        "项目自动发现",
                        () => DiscoverProjectsAsync(projects, conversations, cancellationToken),
                        cancellationToken)
                    .ConfigureAwait(false);
                AddError(discoveryPart, errors);
                discoveredProjects = discoveryPart.Value;

                if (discoveredProjects > 0)
                {
                    LoadPart<IReadOnlyList<ProjectRecord>> reloadedProjects = await CaptureAsync(
                            "项目数据库刷新",
                            () => _projectRepository.GetAllAsync(cancellationToken: cancellationToken),
                            cancellationToken)
                        .ConfigureAwait(false);
                    AddError(reloadedProjects, errors);
                    projects = reloadedProjects.Value ?? projects;
                }
            }

            return new WorkspaceDataSnapshot(
                projects,
                conversations,
                installationsPart.Value ?? Array.Empty<CliInstallation>(),
                connectionsPart.Value ?? Array.Empty<ConnectionProfile>(),
                errors,
                discoveredProjects,
                DateTimeOffset.Now,
                selectionPart.Value ?? new ConnectionProfileSelection(null, null),
                routingPart.Value);
        }
        finally
        {
            _loadGate.Release();
        }
    }

    public async Task<ProjectRecord> AddProjectAsync(
        string rootPath,
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        ArgumentException.ThrowIfNullOrWhiteSpace(rootPath);

        string normalizedRoot = PathIdentity.Normalize(rootPath);
        if (!Directory.Exists(normalizedRoot))
        {
            throw new DirectoryNotFoundException($"项目目录不存在：{normalizedRoot}");
        }

        string fingerprint = PathIdentity.CreateStableId(normalizedRoot);
        DateTimeOffset now = DateTimeOffset.UtcNow;
        var project = new ProjectRecord
        {
            Id = fingerprint,
            DisplayName = CreateDisplayName(normalizedRoot),
            RootPath = normalizedRoot,
            PathFingerprint = fingerprint,
            DefaultCli = CliKind.Codex,
            CreatedAt = now,
            LastOpenedAt = now,
        };

        await _projectRepository.UpsertAsync(project, cancellationToken).ConfigureAwait(false);
        IReadOnlyList<ProjectRecord> projects = await _projectRepository
            .GetAllAsync(includeArchived: true, cancellationToken)
            .ConfigureAwait(false);

        return projects.FirstOrDefault(candidate =>
                   string.Equals(candidate.PathFingerprint, fingerprint, StringComparison.OrdinalIgnoreCase))
               ?? project;
    }

    public async Task<ProjectDeletionResult> DeleteProjectAsync(
        ProjectRecord project,
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        ArgumentNullException.ThrowIfNull(project);

        await _loadGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ConversationDeletionResult conversations = await _conversationDeletionService
                .DeleteProjectConversationsAsync(project, cancellationToken)
                .ConfigureAwait(false);
            if (!conversations.Succeeded)
            {
                return new ProjectDeletionResult(project, conversations, ProjectRecordDeleted: false);
            }

            try
            {
                bool deleted = await _projectRepository.DeleteAsync(project.Id, cancellationToken)
                    .ConfigureAwait(false);
                if (!deleted)
                {
                    deleted = await _projectRepository.GetByIdAsync(project.Id, cancellationToken)
                        .ConfigureAwait(false) is null;
                }

                return new ProjectDeletionResult(project, conversations, deleted);
            }
            catch (Exception exception) when (exception is not OperationCanceledException)
            {
                return new ProjectDeletionResult(
                    project,
                    conversations,
                    ProjectRecordDeleted: false,
                    ProjectRecordError: exception.Message);
            }
        }
        finally
        {
            _loadGate.Release();
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        if (_projectRepository is IDisposable disposableRepository)
        {
            disposableRepository.Dispose();
        }

        _legacyProfileEditor.Dispose();
        _legacyProfileReader.Dispose();

        _loadGate.Dispose();
    }

    private async Task<int> DiscoverProjectsAsync(
        IReadOnlyList<ProjectRecord> storedProjects,
        IReadOnlyList<ConversationRecord> conversations,
        CancellationToken cancellationToken)
    {
        var knownFingerprints = storedProjects
            .Select(project => project.PathFingerprint)
            .Where(fingerprint => !string.IsNullOrWhiteSpace(fingerprint))
            .ToHashSet(StringComparer.OrdinalIgnoreCase);

        ConversationRecord[][] candidates = conversations
            .Where(conversation =>
                !string.IsNullOrWhiteSpace(conversation.ProjectId) &&
                !string.IsNullOrWhiteSpace(conversation.OriginalWorkingDirectory) &&
                !knownFingerprints.Contains(conversation.ProjectId))
            .GroupBy(conversation => conversation.ProjectId, StringComparer.OrdinalIgnoreCase)
            .Select(group => group.OrderByDescending(conversation => conversation.UpdatedAt).ToArray())
            .ToArray();

        int discovered = 0;
        foreach (ConversationRecord[] projectConversations in candidates)
        {
            cancellationToken.ThrowIfCancellationRequested();
            ConversationRecord latest = projectConversations[0];
            string normalizedRoot;
            try
            {
                normalizedRoot = PathIdentity.Normalize(latest.OriginalWorkingDirectory);
            }
            catch (Exception exception) when (exception is ArgumentException or NotSupportedException)
            {
                continue;
            }

            if (!Directory.Exists(normalizedRoot))
            {
                // Native history can outlive a deleted source folder. Such a
                // conversation remains visible in History, but it must not
                // recreate a project card that can no longer be launched.
                continue;
            }

            string fingerprint = PathIdentity.CreateStableId(normalizedRoot);
            if (!knownFingerprints.Add(fingerprint))
            {
                continue;
            }

            var project = new ProjectRecord
            {
                Id = fingerprint,
                DisplayName = CreateDisplayName(normalizedRoot),
                RootPath = normalizedRoot,
                PathFingerprint = fingerprint,
                DefaultCli = latest.NativeClient,
                CreatedAt = projectConversations.Min(conversation => conversation.CreatedAt),
                LastOpenedAt = projectConversations.Max(conversation => conversation.UpdatedAt),
            };

            await _projectRepository.UpsertAsync(project, cancellationToken).ConfigureAwait(false);
            discovered++;
        }

        return discovered;
    }

    private static string CreateDisplayName(string rootPath)
    {
        string trimmed = Path.TrimEndingDirectorySeparator(rootPath);
        string name = Path.GetFileName(trimmed);
        if (!string.IsNullOrWhiteSpace(name))
        {
            return name;
        }

        string? root = Path.GetPathRoot(trimmed);
        return string.IsNullOrWhiteSpace(root) ? trimmed : root.TrimEnd(Path.DirectorySeparatorChar);
    }

    private static async Task<LoadPart<T>> CaptureAsync<T>(
        string source,
        Func<Task<T>> operation,
        CancellationToken cancellationToken)
    {
        try
        {
            return LoadPart<T>.Succeeded(await operation().ConfigureAwait(false));
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (Exception exception)
        {
            return LoadPart<T>.Failed(new WorkspaceLoadError(source, exception.Message));
        }
    }

    private static void AddError<T>(LoadPart<T> part, ICollection<WorkspaceLoadError> errors)
    {
        if (part.Error is not null)
        {
            errors.Add(part.Error);
        }
    }

    private sealed record LoadPart<T>(T? Value, WorkspaceLoadError? Error)
    {
        public static LoadPart<T> Succeeded(T value) => new(value, null);

        public static LoadPart<T> Failed(WorkspaceLoadError error) => new(default, error);
    }
}

internal sealed record WorkspaceDataSnapshot(
    IReadOnlyList<ProjectRecord> Projects,
    IReadOnlyList<ConversationRecord> Conversations,
    IReadOnlyList<CliInstallation> CliInstallations,
    IReadOnlyList<ConnectionProfile> Connections,
    IReadOnlyList<WorkspaceLoadError> Errors,
    int DiscoveredProjectCount,
    DateTimeOffset LoadedAt,
    ConnectionProfileSelection? ConnectionSelection = null,
    ConnectionProfileRouting? ConnectionRouting = null);

internal sealed record ProjectDeletionResult(
    ProjectRecord Project,
    ConversationDeletionResult Conversations,
    bool ProjectRecordDeleted,
    string? ProjectRecordError = null)
{
    public bool Succeeded => Conversations.Succeeded && ProjectRecordDeleted;
}

internal sealed record WorkspaceLoadError(string Source, string Message);
