using System.Reflection;
using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Terminal;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class ConnectionRuntimeSharingTests
{
    [Fact]
    public async Task MainWindow_UsesWorkspaceDataServiceReaderAndProviderForChatAndTerminal()
    {
        using var fixture = new TemporaryWorkspace();
        var profileReader = new LegacyProfileReader(fixture.Paths);
        var dataService = new WorkspaceDataService(
            new EmptyProjectRepository(),
            new EmptyConversationIndexer(),
            new SuccessfulConversationDeletionService(),
            new EmptyCliDetector(),
            profileReader);
        using var viewModel = new MainWindowViewModel(dataService);

        TerminalViewModel terminal = GetField<TerminalViewModel>(viewModel, "_terminal");
        Assert.Same(viewModel.ConnectionProfileReader, terminal.ConnectionProfileReader);
        Assert.Same(viewModel.CredentialProvider, terminal.CredentialProvider);

        ChatSessionController chatController = GetField<ChatSessionController>(viewModel, "_chatController");
        Assert.Same(viewModel.ConnectionProfileReader, GetField<IConnectionProfileReader>(chatController, "_profileReader"));

        Func<CliKind, IChatEngine> engineFactory = GetField<Func<CliKind, IChatEngine>>(
            chatController,
            "_engineFactory");
        IChatEngine engine = engineFactory(CliKind.Codex);
        try
        {
            CliTerminalCommandFactory commandFactory = GetField<CliTerminalCommandFactory>(engine, "_commandFactory");
            Assert.Same(viewModel.CredentialProvider, GetField<IConnectionCredentialProvider>(commandFactory, "_credentials"));
        }
        finally
        {
            await engine.DisposeAsync();
        }
    }

    [Fact]
    public void TerminalViewModel_RejectsHalfInjectedConnectionRuntime()
    {
        using var fixture = new TemporaryWorkspace();
        var reader = new LegacyProfileReader(fixture.Paths);

        Assert.Throws<ArgumentException>(() => new TerminalViewModel(
            [],
            reader,
            credentialProvider: null));

        reader.Dispose();
    }

    [Fact]
    public async Task MainWindow_KeepsChatAsAnInternalProjectOrHistoryRoute()
    {
        using var fixture = new TemporaryWorkspace();
        var profileReader = new LegacyProfileReader(fixture.Paths);
        var dataService = new WorkspaceDataService(
            new EmptyProjectRepository(),
            new EmptyConversationIndexer(),
            new SuccessfulConversationDeletionService(),
            new EmptyCliDetector(),
            profileReader);
        var viewModel = new MainWindowViewModel(dataService);

        try
        {
            Assert.DoesNotContain(viewModel.NavigationItems, item =>
                string.Equals(item.Id, "chat", StringComparison.OrdinalIgnoreCase));
            ProjectCardViewModel project = CreateProjectCard(fixture, "导航测试项目");
            GetField<ProjectSessionsViewModel>(viewModel, "_projectSessions")
                .OpenProject(project, Array.Empty<HistorySessionViewModel>());

            ConfigureChatNavigation(
                viewModel,
                project,
                returnPageId: "project-sessions",
                returnLabel: "返回项目会话",
                originLabel: "项目中心");
            NavigateTo(viewModel, "chat");

            Assert.IsType<ChatViewModel>(viewModel.CurrentPage);
            Assert.Equal("项目中心 / 导航测试项目 / 项目会话 / AI 对话", viewModel.ChatBreadcrumb);
            Assert.Equal("返回项目会话", viewModel.ChatReturnLabel);
            Assert.Equal("projects", Assert.Single(viewModel.NavigationItems, item => item.IsSelected).Id);

            viewModel.ReturnFromChatCommand.Execute(null);
            Assert.IsType<ProjectSessionsViewModel>(viewModel.CurrentPage);

            ConfigureChatNavigation(
                viewModel,
                project,
                returnPageId: "history",
                returnLabel: "返回历史会话",
                originLabel: "历史会话");
            NavigateTo(viewModel, "chat");

            Assert.IsType<ChatViewModel>(viewModel.CurrentPage);
            Assert.Equal("历史会话 / 导航测试项目 / AI 对话", viewModel.ChatBreadcrumb);
            Assert.Equal("返回历史会话", viewModel.ChatReturnLabel);
            Assert.DoesNotContain(viewModel.NavigationItems, item =>
                string.Equals(item.Id, "history", StringComparison.OrdinalIgnoreCase));
            Assert.DoesNotContain(viewModel.NavigationItems, item => item.IsSelected);

            viewModel.ReturnFromChatCommand.Execute(null);
            Assert.IsType<HistoryViewModel>(viewModel.CurrentPage);
        }
        finally
        {
            await viewModel.ShutdownAsync(TimeSpan.FromSeconds(1));
            viewModel.Dispose();
        }
    }

    [Fact]
    public async Task MainWindow_ProjectSessionContinueOpensTerminalAndQueuesNativeResume()
    {
        using var fixture = new TemporaryWorkspace();
        var profileReader = new LegacyProfileReader(fixture.Paths);
        var dataService = new WorkspaceDataService(
            new EmptyProjectRepository(),
            new EmptyConversationIndexer(),
            new SuccessfulConversationDeletionService(),
            new EmptyCliDetector(),
            profileReader);
        var viewModel = new MainWindowViewModel(dataService);

        try
        {
            ProjectCardViewModel project = CreateProjectCard(fixture, "终端恢复测试项目");
            var record = new ConversationRecord
            {
                Id = "codex:native-resume-session",
                ProjectId = project.PathFingerprint,
                NativeClient = CliKind.Codex,
                NativeSessionId = "native-resume-session",
                OriginalWorkingDirectory = project.Path,
                CreatedAt = DateTimeOffset.UtcNow.AddMinutes(-5),
                UpdatedAt = DateTimeOffset.UtcNow,
                ResumePolicy = ResumePolicy.CurrentConnection,
            };
            var session = new HistorySessionViewModel(
                record,
                "继续原生会话",
                project.Name,
                "Codex",
                "本机中转",
                "刚刚",
                "可继续",
                "原生命令行会话");
            ProjectSessionsViewModel sessions = GetField<ProjectSessionsViewModel>(viewModel, "_projectSessions");
            sessions.OpenProject(project, [session]);

            await sessions.ContinueConversationCommand.ExecuteAsync(session);

            TerminalViewModel terminal = Assert.IsType<TerminalViewModel>(viewModel.CurrentPage);
            Assert.Same(record, terminal.PendingConversation);
            Assert.Equal(CliKind.Codex, terminal.SelectedCliKind);
            Assert.True(terminal.ConsumeAutoStartRequest());
            Assert.False(terminal.ConsumeAutoStartRequest());
            Assert.DoesNotContain(viewModel.NavigationItems, item => item.IsSelected);
        }
        finally
        {
            await viewModel.ShutdownAsync(TimeSpan.FromSeconds(1));
            viewModel.Dispose();
        }
    }

    private static T GetField<T>(object instance, string fieldName)
    {
        FieldInfo field = instance.GetType().GetField(
            fieldName,
            BindingFlags.Instance | BindingFlags.NonPublic)
            ?? throw new InvalidOperationException($"找不到字段 {fieldName}。");
        return Assert.IsAssignableFrom<T>(field.GetValue(instance));
    }

    private static void NavigateTo(MainWindowViewModel viewModel, string pageId)
    {
        MethodInfo method = typeof(MainWindowViewModel).GetMethod(
            "NavigateTo",
            BindingFlags.Instance | BindingFlags.NonPublic)
            ?? throw new InvalidOperationException("找不到内部导航方法。");
        method.Invoke(viewModel, [pageId]);
    }

    private static void ConfigureChatNavigation(
        MainWindowViewModel viewModel,
        ProjectCardViewModel? project,
        string returnPageId,
        string returnLabel,
        string originLabel)
    {
        MethodInfo method = typeof(MainWindowViewModel).GetMethod(
            "ConfigureChatNavigation",
            BindingFlags.Instance | BindingFlags.NonPublic)
            ?? throw new InvalidOperationException("找不到 AI 对话导航配置方法。");
        method.Invoke(viewModel, [project, returnPageId, returnLabel, originLabel]);
    }

    private static ProjectCardViewModel CreateProjectCard(TemporaryWorkspace fixture, string name)
    {
        string path = Directory.CreateDirectory(Path.Combine(fixture.Root, name)).FullName;
        string fingerprint = PathIdentity.CreateStableId(path);
        var record = new ProjectRecord
        {
            Id = fingerprint,
            DisplayName = name,
            RootPath = path,
            PathFingerprint = fingerprint,
            DefaultCli = CliKind.Codex,
            CreatedAt = DateTimeOffset.UtcNow,
        };
        return new ProjectCardViewModel(
            record,
            "Codex",
            "本机中转",
            "可用",
            "刚刚",
            "N",
            conversationCount: 0,
            codexConversationCount: 0,
            claudeConversationCount: 0,
            geminiConversationCount: 0,
            pathAvailable: true);
    }

    private sealed class EmptyProjectRepository : IProjectRepository
    {
        public Task<IReadOnlyList<ProjectRecord>> GetAllAsync(
            bool includeArchived = false,
            CancellationToken cancellationToken = default)
            => Task.FromResult<IReadOnlyList<ProjectRecord>>(Array.Empty<ProjectRecord>());

        public Task<ProjectRecord?> GetByIdAsync(string id, CancellationToken cancellationToken = default)
            => Task.FromResult<ProjectRecord?>(null);

        public Task UpsertAsync(ProjectRecord project, CancellationToken cancellationToken = default)
            => Task.CompletedTask;

        public Task<bool> DeleteAsync(string id, CancellationToken cancellationToken = default)
            => Task.FromResult(false);
    }

    private sealed class EmptyConversationIndexer : IConversationIndexer
    {
        public Task<IReadOnlyList<ConversationRecord>> ScanAsync(
            ProjectRecord? project = null,
            CliKind? client = null,
            CancellationToken cancellationToken = default)
            => Task.FromResult<IReadOnlyList<ConversationRecord>>(Array.Empty<ConversationRecord>());
    }

    private sealed class SuccessfulConversationDeletionService : IConversationDeletionService
    {
        public Task<ConversationDeletionResult> DeleteProjectConversationsAsync(
            ProjectRecord project,
            CancellationToken cancellationToken = default)
            => Task.FromResult(new ConversationDeletionResult(
            [
                new CliConversationDeletionResult(CliKind.Codex, 0, 0, Array.Empty<ConversationDeletionIssue>()),
                new CliConversationDeletionResult(CliKind.ClaudeCode, 0, 0, Array.Empty<ConversationDeletionIssue>()),
                new CliConversationDeletionResult(CliKind.GeminiCli, 0, 0, Array.Empty<ConversationDeletionIssue>()),
            ]));
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
            Root = Path.Combine(Path.GetTempPath(), "LanAi.ConnectionRuntime.Tests", Guid.NewGuid().ToString("N"));
            string user = Directory.CreateDirectory(Path.Combine(Root, "user")).FullName;
            string local = Directory.CreateDirectory(Path.Combine(Root, "local")).FullName;
            Paths = new AppDataPaths(user, local);
        }

        public string Root { get; }

        public AppDataPaths Paths { get; }

        public void Dispose()
        {
            string safeParent = Path.Combine(Path.GetTempPath(), "LanAi.ConnectionRuntime.Tests");
            if (Directory.Exists(Root) &&
                Path.GetFullPath(Root).StartsWith(Path.GetFullPath(safeParent), StringComparison.OrdinalIgnoreCase))
            {
                Directory.Delete(Root, recursive: true);
            }
        }
    }
}
