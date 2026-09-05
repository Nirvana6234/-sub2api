using System.IO;
using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class ChatProjectDeletionTests
{
    [Fact]
    public async Task DeleteActiveChatProject_StopsChatBeforeDeletingOfficialHistoryAndProjectRecord()
    {
        using var fixture = new TemporaryWorkspace();
        var events = new List<string>();
        ProjectRecord project = fixture.CreateProject();
        var repository = new RecordingProjectRepository(project, events);
        using var dataService = CreateDataService(fixture, repository, events);
        var controller = new RecordingChatController(project.PathFingerprint, events);
        var viewModel = new MainWindowViewModel(dataService, controller);

        try
        {
            ProjectRemovalOutcome outcome = await viewModel.DeleteProjectAsync(CreateCard(project));

            Assert.True(outcome.Succeeded, outcome.Message);
            Assert.Equal(
                ["chat-cancel", "chat-reset", "official-history-delete", "project-record-delete"],
                events.Take(4));
            Assert.Null(await repository.GetByIdAsync(project.Id));
            Assert.True(Directory.Exists(project.RootPath));
        }
        finally
        {
            await viewModel.ShutdownAsync(TimeSpan.FromSeconds(1));
            viewModel.Dispose();
        }
    }

    [Fact]
    public async Task DeleteActiveChatProject_WhenChatCannotStop_DoesNotDeleteAnything()
    {
        using var fixture = new TemporaryWorkspace();
        var events = new List<string>();
        ProjectRecord project = fixture.CreateProject();
        var repository = new RecordingProjectRepository(project, events);
        using var dataService = CreateDataService(fixture, repository, events);
        var controller = new RecordingChatController(project.PathFingerprint, events)
        {
            ResetError = new InvalidOperationException("模拟停止失败"),
        };
        var viewModel = new MainWindowViewModel(dataService, controller);

        try
        {
            ProjectRemovalOutcome outcome = await viewModel.DeleteProjectAsync(CreateCard(project));

            Assert.False(outcome.Succeeded);
            Assert.Contains("停止该项目的图形 AI 会话失败", outcome.Message, StringComparison.Ordinal);
            Assert.Equal(["chat-cancel", "chat-reset"], events);
            Assert.NotNull(await repository.GetByIdAsync(project.Id));
            Assert.True(Directory.Exists(project.RootPath));
        }
        finally
        {
            controller.ResetError = null;
            await viewModel.ShutdownAsync(TimeSpan.FromSeconds(1));
            viewModel.Dispose();
        }
    }

    private static WorkspaceDataService CreateDataService(
        TemporaryWorkspace fixture,
        IProjectRepository repository,
        List<string> events)
        => new(
            repository,
            new EmptyConversationIndexer(),
            new RecordingConversationDeletionService(events),
            new EmptyCliDetector(),
            new LegacyProfileReader(fixture.ProfilesPath));

    private static ProjectCardViewModel CreateCard(ProjectRecord project)
        => new(
            project,
            "Codex",
            "启动时选择",
            "测试项目",
            "刚刚",
            "T",
            conversationCount: 1,
            codexConversationCount: 1,
            claudeConversationCount: 0,
            geminiConversationCount: 0,
            pathAvailable: true);

    private sealed class RecordingChatController : IChatSessionController
    {
        private readonly List<string> _events;

        public RecordingChatController(string activeProjectFingerprint, List<string> events)
        {
            ActiveProjectFingerprint = activeProjectFingerprint;
            _events = events;
        }

        public Exception? ResetError { get; set; }

        public ChatEngineState State { get; private set; } = ChatEngineState.RunningTurn;

        public string? NativeSessionId => "active-session";

        public string? ActiveProjectFingerprint { get; private set; }

        public event EventHandler<ChatEvent>? EventReceived
        {
            add { }
            remove { }
        }

        public Task ConnectAsync(
            ChatLaunchIntent intent,
            CancellationToken cancellationToken = default)
        {
            ActiveProjectFingerprint = intent.Project.PathFingerprint;
            State = ChatEngineState.Ready;
            return Task.CompletedTask;
        }

        public Task SendAsync(
            ChatLaunchIntent intent,
            string message,
            CancellationToken cancellationToken = default)
            => throw new NotSupportedException();

        public Task RespondToApprovalAsync(
            string requestId,
            ChatApprovalDecision decision,
            CancellationToken cancellationToken = default)
            => throw new NotSupportedException();

        public Task RespondToUserInputAsync(
            string requestId,
            string response,
            CancellationToken cancellationToken = default)
            => throw new NotSupportedException();

        public Task CancelTurnAsync(CancellationToken cancellationToken = default)
        {
            _events.Add("chat-cancel");
            return Task.CompletedTask;
        }

        public Task ResetAsync(CancellationToken cancellationToken = default)
        {
            _events.Add("chat-reset");
            if (ResetError is not null)
            {
                return Task.FromException(ResetError);
            }

            State = ChatEngineState.Created;
            ActiveProjectFingerprint = null;
            return Task.CompletedTask;
        }

        public ValueTask DisposeAsync()
        {
            State = ChatEngineState.Stopped;
            ActiveProjectFingerprint = null;
            return ValueTask.CompletedTask;
        }
    }

    private sealed class RecordingConversationDeletionService : IConversationDeletionService
    {
        private readonly List<string> _events;

        public RecordingConversationDeletionService(List<string> events) => _events = events;

        public Task<ConversationDeletionResult> DeleteProjectConversationsAsync(
            ProjectRecord project,
            CancellationToken cancellationToken = default)
        {
            _events.Add("official-history-delete");
            return Task.FromResult(new ConversationDeletionResult(
            [
                EmptyResult(CliKind.Codex),
                EmptyResult(CliKind.ClaudeCode),
                EmptyResult(CliKind.GeminiCli),
            ]));
        }

        private static CliConversationDeletionResult EmptyResult(CliKind client) =>
            new(client, 0, 0, Array.Empty<ConversationDeletionIssue>());
    }

    private sealed class RecordingProjectRepository : IProjectRepository
    {
        private readonly List<string> _events;
        private ProjectRecord? _project;

        public RecordingProjectRepository(ProjectRecord project, List<string> events)
        {
            _project = project;
            _events = events;
        }

        public Task<IReadOnlyList<ProjectRecord>> GetAllAsync(
            bool includeArchived = false,
            CancellationToken cancellationToken = default)
            => Task.FromResult<IReadOnlyList<ProjectRecord>>(
                _project is null ? Array.Empty<ProjectRecord>() : [_project]);

        public Task<ProjectRecord?> GetByIdAsync(
            string id,
            CancellationToken cancellationToken = default)
            => Task.FromResult(
                string.Equals(_project?.Id, id, StringComparison.OrdinalIgnoreCase) ? _project : null);

        public Task UpsertAsync(
            ProjectRecord project,
            CancellationToken cancellationToken = default)
        {
            _project = project;
            return Task.CompletedTask;
        }

        public Task<bool> DeleteAsync(
            string id,
            CancellationToken cancellationToken = default)
        {
            _events.Add("project-record-delete");
            bool deleted = string.Equals(_project?.Id, id, StringComparison.OrdinalIgnoreCase);
            if (deleted)
            {
                _project = null;
            }

            return Task.FromResult(deleted);
        }
    }

    private sealed class EmptyConversationIndexer : IConversationIndexer
    {
        public Task<IReadOnlyList<ConversationRecord>> ScanAsync(
            ProjectRecord? project = null,
            CliKind? client = null,
            CancellationToken cancellationToken = default)
            => Task.FromResult<IReadOnlyList<ConversationRecord>>(Array.Empty<ConversationRecord>());
    }

    private sealed class EmptyCliDetector : ICliDetector
    {
        public Task<IReadOnlyList<CliInstallation>> DetectAsync(
            CliKind? cli = null,
            CancellationToken cancellationToken = default)
            => Task.FromResult<IReadOnlyList<CliInstallation>>(Array.Empty<CliInstallation>());
    }

    private sealed class TemporaryWorkspace : IDisposable
    {
        public TemporaryWorkspace()
        {
            Root = Path.Combine(
                Path.GetTempPath(),
                "LanAi.ChatDeletion.Tests",
                Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(Root);
            ProfilesPath = Path.Combine(Root, "profiles.json");
        }

        public string Root { get; }

        public string ProfilesPath { get; }

        public ProjectRecord CreateProject()
        {
            string projectRoot = Directory.CreateDirectory(Path.Combine(Root, "source-project")).FullName;
            string fingerprint = PathIdentity.CreateStableId(projectRoot);
            return new ProjectRecord
            {
                Id = fingerprint,
                DisplayName = "测试项目",
                RootPath = projectRoot,
                PathFingerprint = fingerprint,
                DefaultCli = CliKind.Codex,
                CreatedAt = DateTimeOffset.UtcNow,
            };
        }

        public void Dispose()
        {
            string fullRoot = Path.GetFullPath(Root);
            string safeParent = Path.GetFullPath(
                Path.Combine(Path.GetTempPath(), "LanAi.ChatDeletion.Tests"));
            if (fullRoot.StartsWith(safeParent, StringComparison.OrdinalIgnoreCase) && Directory.Exists(fullRoot))
            {
                Directory.Delete(fullRoot, recursive: true);
            }
        }
    }
}
