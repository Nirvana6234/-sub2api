using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class ProjectSessionsViewModelTests
{
    [Fact]
    public async Task OpenProject_ListsOnlyExactProjectHistoryUntilUserChoosesAnAction()
    {
        using var fixture = new ProjectFixture();
        ProjectCardViewModel project = fixture.CreateProject("project-a");
        ProjectCardViewModel otherProject = fixture.CreateProject("project-b");
        var calls = new List<string>();
        var viewModel = new ProjectSessionsViewModel(
            selected =>
            {
                calls.Add($"new:{selected.Name}");
                return Task.CompletedTask;
            },
            (selected, session) =>
            {
                calls.Add($"continue:{selected.Name}:{session.Record.NativeSessionId}");
                return Task.CompletedTask;
            },
            () => calls.Add("back"));

        DateTimeOffset now = DateTimeOffset.UtcNow;
        HistorySessionViewModel first = fixture.Session(project, "first", now.AddMinutes(-2));
        HistorySessionViewModel latest = fixture.Session(project, "latest", now);
        HistorySessionViewModel other = fixture.Session(otherProject, "other", now.AddMinutes(2));
        HistorySessionViewModel nested = fixture.Session(
            project,
            "nested",
            now.AddMinutes(3),
            Path.Combine(project.Path, "nested"),
            projectId: "legacy-unrelated");

        viewModel.OpenProject(project, [other, nested, first, latest]);

        Assert.Equal("project-a", viewModel.ProjectName);
        Assert.Equal(["latest", "first"], viewModel.Sessions.Select(item => item.Record.NativeSessionId));
        Assert.True(viewModel.HasSessions);
        Assert.Empty(calls);

        await viewModel.ContinueConversationCommand.ExecuteAsync(latest);

        Assert.Equal(["continue:project-a:latest"], calls);
    }

    [Fact]
    public async Task NewConversation_IsExplicitAndDoesNotReuseAHistorySession()
    {
        using var fixture = new ProjectFixture();
        ProjectCardViewModel project = fixture.CreateProject("project-a");
        ProjectCardViewModel other = fixture.CreateProject("project-b");
        var calls = new List<string>();
        var viewModel = new ProjectSessionsViewModel(
            selected =>
            {
                calls.Add($"new:{selected.Name}");
                return Task.CompletedTask;
            },
            (_, session) =>
            {
                calls.Add($"continue:{session.Record.NativeSessionId}");
                return Task.CompletedTask;
            },
            () => calls.Add("back"));

        viewModel.OpenProject(project, [fixture.Session(other, "other", DateTimeOffset.UtcNow)]);

        Assert.Empty(viewModel.Sessions);
        Assert.Empty(calls);
        await viewModel.StartNewConversationCommand.ExecuteAsync(null);

        Assert.Equal(["new:project-a"], calls);
    }

    [Fact]
    public async Task ContinueConversation_RejectsSessionInjectedFromAnotherProject()
    {
        using var fixture = new ProjectFixture();
        ProjectCardViewModel project = fixture.CreateProject("project-a");
        ProjectCardViewModel other = fixture.CreateProject("project-b");
        var calls = new List<string>();
        var viewModel = new ProjectSessionsViewModel(
            _ => Task.CompletedTask,
            (_, session) =>
            {
                calls.Add(session.Record.NativeSessionId);
                return Task.CompletedTask;
            },
            () => { });
        HistorySessionViewModel otherSession = fixture.Session(other, "other", DateTimeOffset.UtcNow);

        viewModel.OpenProject(project, Array.Empty<HistorySessionViewModel>());
        await viewModel.ContinueConversationCommand.ExecuteAsync(otherSession);

        Assert.Empty(calls);
        Assert.Equal("这条历史不属于当前项目，已拒绝打开。", viewModel.StatusNotice);
    }

    [Fact]
    public void RefreshSessions_UsesProjectIdentityRatherThanTheGlobalHistoryFilter()
    {
        using var fixture = new ProjectFixture();
        ProjectCardViewModel project = fixture.CreateProject("project-a");
        ProjectCardViewModel other = fixture.CreateProject("project-b");
        var viewModel = new ProjectSessionsViewModel(
            _ => Task.CompletedTask,
            (_, _) => Task.CompletedTask,
            () => { });
        DateTimeOffset now = DateTimeOffset.UtcNow;
        HistorySessionViewModel old = fixture.Session(project, "old", now.AddMinutes(-3));
        HistorySessionViewModel refreshed = fixture.Session(project, "refreshed", now);
        HistorySessionViewModel unrelated = fixture.Session(other, "other", now.AddMinutes(1));

        viewModel.OpenProject(project, [old]);
        viewModel.RefreshSessions([unrelated, old, refreshed]);

        Assert.Equal(["refreshed", "old"], viewModel.Sessions.Select(item => item.Record.NativeSessionId));
    }

    private sealed class ProjectFixture : IDisposable
    {
        private readonly string _root = Path.Combine(
            Path.GetTempPath(),
            "LanAi.ProjectSessionsViewModel.Tests",
            Guid.NewGuid().ToString("N"));

        public ProjectFixture() => Directory.CreateDirectory(_root);

        public ProjectCardViewModel CreateProject(string name)
        {
            string path = Directory.CreateDirectory(Path.Combine(_root, name)).FullName;
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
                "T",
                conversationCount: 0,
                codexConversationCount: 0,
                claudeConversationCount: 0,
                geminiConversationCount: 0,
                pathAvailable: true);
        }

        public HistorySessionViewModel Session(
            ProjectCardViewModel project,
            string nativeSessionId,
            DateTimeOffset updatedAt,
            string? workingDirectory = null,
            string? projectId = null)
        {
            string cwd = workingDirectory ?? project.Path;
            var record = new ConversationRecord
            {
                Id = $"codex:{nativeSessionId}",
                ProjectId = projectId ?? project.PathFingerprint,
                NativeClient = CliKind.Codex,
                NativeSessionId = nativeSessionId,
                OriginalWorkingDirectory = cwd,
                CreatedAt = updatedAt.AddMinutes(-1),
                UpdatedAt = updatedAt,
                ResumePolicy = ResumePolicy.CurrentConnection,
            };
            return new HistorySessionViewModel(
                record,
                nativeSessionId,
                project.Name,
                "Codex",
                "本机中转",
                "刚刚",
                "可继续",
                $"工作目录 · {cwd}");
        }

        public void Dispose()
        {
            if (Directory.Exists(_root))
            {
                Directory.Delete(_root, recursive: true);
            }
        }
    }
}
