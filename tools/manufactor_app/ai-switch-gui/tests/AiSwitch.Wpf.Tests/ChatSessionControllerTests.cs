using System.IO;
using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class ChatSessionControllerTests
{
    [Fact]
    public async Task ConnectThenSend_ReusesTheAlreadyRestoredEngine()
    {
        using var workspace = new TemporaryWorkspace();
        ProjectRecord project = workspace.CreateProject("project-a", CliKind.Codex);
        ConversationRecord conversation = CreateConversation(project, "native-resume-session");
        var engines = new List<RecordingChatEngine>();
        await using var controller = new ChatSessionController(
            _ =>
            {
                var engine = new RecordingChatEngine();
                engines.Add(engine);
                return engine;
            },
            new InstalledCliDetector(),
            new EmptyProfileReader());
        ChatLaunchIntent intent = CreateIntent(project, conversation);

        await controller.ConnectAsync(intent);

        RecordingChatEngine engine = Assert.Single(engines);
        Assert.Equal(CliLaunchMode.Resume, engine.StartContext?.LaunchRequest.Mode);
        Assert.Empty(engine.Messages);

        await controller.SendAsync(intent, "继续工作");

        Assert.Single(engines);
        Assert.Equal(["继续工作"], engine.Messages);
    }

    [Fact]
    public async Task Send_RestoresNativeSessionAndReplacesEngineWhenProjectChanges()
    {
        using var workspace = new TemporaryWorkspace();
        ProjectRecord firstProject = workspace.CreateProject("project-a", CliKind.Codex);
        ProjectRecord secondProject = workspace.CreateProject("project-b", CliKind.Codex);
        ConversationRecord conversation = CreateConversation(firstProject, "native-resume-session");
        var engines = new List<RecordingChatEngine>();
        await using var controller = new ChatSessionController(
            _ =>
            {
                var engine = new RecordingChatEngine();
                engines.Add(engine);
                return engine;
            },
            new InstalledCliDetector(),
            new EmptyProfileReader());

        await controller.SendAsync(
            CreateIntent(firstProject, conversation),
            "继续上次工作");
        await controller.SendAsync(
            CreateIntent(firstProject, conversation),
            "继续同一会话");

        RecordingChatEngine firstEngine = Assert.Single(engines);
        Assert.NotNull(firstEngine.StartContext);
        Assert.Equal(CliLaunchMode.Resume, firstEngine.StartContext.LaunchRequest.Mode);
        Assert.Equal("native-resume-session", firstEngine.StartContext.LaunchRequest.NativeSessionId);
        Assert.Equal(["继续上次工作", "继续同一会话"], firstEngine.Messages);
        Assert.Equal(firstProject.PathFingerprint, controller.ActiveProjectFingerprint);

        await controller.SendAsync(
            CreateIntent(secondProject, conversation: null),
            "切换到新项目");

        Assert.Equal(2, engines.Count);
        Assert.True(firstEngine.StopCalled);
        Assert.True(firstEngine.DisposeCalled);
        RecordingChatEngine secondEngine = engines[1];
        Assert.Equal(CliLaunchMode.New, secondEngine.StartContext?.LaunchRequest.Mode);
        Assert.Null(secondEngine.StartContext?.LaunchRequest.NativeSessionId);
        Assert.Equal(["切换到新项目"], secondEngine.Messages);
        Assert.Equal(secondProject.PathFingerprint, controller.ActiveProjectFingerprint);
    }

    private static ChatLaunchIntent CreateIntent(
        ProjectRecord project,
        ConversationRecord? conversation)
        => new(
            project,
            project.DefaultCli,
            ConnectionProfileId: "test-source",
            ConnectionLabel: "测试来源",
            Model: null,
            conversation,
            ChatPermissionMode.WorkspaceWrite);

    private static ConversationRecord CreateConversation(ProjectRecord project, string nativeSessionId)
        => new()
        {
            Id = $"codex:{nativeSessionId}",
            ProjectId = project.PathFingerprint,
            NativeClient = CliKind.Codex,
            NativeSessionId = nativeSessionId,
            OriginalWorkingDirectory = project.RootPath,
            CreatedAt = DateTimeOffset.UtcNow.AddMinutes(-10),
            UpdatedAt = DateTimeOffset.UtcNow,
            ResumePolicy = ResumePolicy.CurrentConnection,
        };

    private sealed class RecordingChatEngine : IChatEngine
    {
        public CliKind Kind => CliKind.Codex;

        public ChatEngineState State { get; private set; } = ChatEngineState.Created;

        public string? NativeSessionId { get; private set; }

        public ChatEngineContext? StartContext { get; private set; }

        public List<string> Messages { get; } = [];

        public bool StopCalled { get; private set; }

        public bool DisposeCalled { get; private set; }

        public event EventHandler<ChatEvent>? EventReceived
        {
            add { }
            remove { }
        }

        public Task StartAsync(
            ChatEngineContext context,
            CancellationToken cancellationToken = default)
        {
            StartContext = context;
            NativeSessionId = context.LaunchRequest.NativeSessionId ?? $"new-{Guid.NewGuid():N}";
            State = ChatEngineState.Ready;
            return Task.CompletedTask;
        }

        public Task SendMessageAsync(
            string message,
            CancellationToken cancellationToken = default)
        {
            Messages.Add(message);
            State = ChatEngineState.Ready;
            return Task.CompletedTask;
        }

        public Task RespondToApprovalAsync(
            string requestId,
            ChatApprovalDecision decision,
            CancellationToken cancellationToken = default)
            => Task.CompletedTask;

        public Task RespondToUserInputAsync(
            string requestId,
            string response,
            CancellationToken cancellationToken = default)
            => Task.CompletedTask;

        public Task CancelTurnAsync(CancellationToken cancellationToken = default)
            => Task.CompletedTask;

        public Task StopAsync(CancellationToken cancellationToken = default)
        {
            StopCalled = true;
            State = ChatEngineState.Stopped;
            return Task.CompletedTask;
        }

        public ValueTask DisposeAsync()
        {
            DisposeCalled = true;
            State = ChatEngineState.Stopped;
            return ValueTask.CompletedTask;
        }
    }

    private sealed class InstalledCliDetector : ICliDetector
    {
        public Task<IReadOnlyList<CliInstallation>> DetectAsync(
            CliKind? cli = null,
            CancellationToken cancellationToken = default)
        {
            CliKind selected = cli ?? CliKind.Codex;
            return Task.FromResult<IReadOnlyList<CliInstallation>>(
            [
                new CliInstallation
                {
                    Kind = selected,
                    IsInstalled = true,
                    ExecutablePath = "official-cli.exe",
                    Version = "test",
                    DetectedAt = DateTimeOffset.UtcNow,
                },
            ]);
        }
    }

    private sealed class EmptyProfileReader : IConnectionProfileReader
    {
        public Task<IReadOnlyList<ConnectionProfile>> GetAllAsync(
            CancellationToken cancellationToken = default)
            => Task.FromResult<IReadOnlyList<ConnectionProfile>>(
            [
                new ConnectionProfile
                {
                    Id = "test-source",
                    Name = "测试来源",
                    BaseUrl = "https://test.example/v1",
                },
            ]);

        public Task<ConnectionProfile?> GetByIdAsync(
            string id,
            CancellationToken cancellationToken = default)
            => Task.FromResult<ConnectionProfile?>(
                string.Equals(id, "test-source", StringComparison.OrdinalIgnoreCase)
                    ? new ConnectionProfile
                    {
                        Id = "test-source",
                        Name = "测试来源",
                        BaseUrl = "https://test.example/v1",
                    }
                    : null);
    }

    private sealed class TemporaryWorkspace : IDisposable
    {
        public TemporaryWorkspace()
        {
            Root = Path.Combine(
                Path.GetTempPath(),
                "LanAi.ChatController.Tests",
                Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(Root);
        }

        public string Root { get; }

        public ProjectRecord CreateProject(string name, CliKind cli)
        {
            string path = Directory.CreateDirectory(Path.Combine(Root, name)).FullName;
            string fingerprint = PathIdentity.CreateStableId(path);
            return new ProjectRecord
            {
                Id = fingerprint,
                DisplayName = name,
                RootPath = path,
                PathFingerprint = fingerprint,
                DefaultCli = cli,
                CreatedAt = DateTimeOffset.UtcNow,
            };
        }

        public void Dispose()
        {
            string fullRoot = Path.GetFullPath(Root);
            string safeParent = Path.GetFullPath(
                Path.Combine(Path.GetTempPath(), "LanAi.ChatController.Tests"));
            if (fullRoot.StartsWith(safeParent, StringComparison.OrdinalIgnoreCase) && Directory.Exists(fullRoot))
            {
                Directory.Delete(fullRoot, recursive: true);
            }
        }
    }
}
