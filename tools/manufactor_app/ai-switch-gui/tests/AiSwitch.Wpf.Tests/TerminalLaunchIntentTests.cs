using System.Collections.ObjectModel;
using System.IO;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.ViewModels;
using LanAi.Workspace.Wpf.Views;

namespace AiSwitch.Wpf.Tests;

public sealed class TerminalLaunchIntentTests
{
    [Fact]
    public void Capture_FreezesProjectCliConnectionAndResumeBeforeAsyncWork()
    {
        ProjectCardViewModel first = CreateProject(
            "project-a",
            Environment.CurrentDirectory,
            CliKind.Codex);
        ProjectCardViewModel second = CreateProject(
            "project-b",
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            CliKind.ClaudeCode);
        var viewModel = new TerminalViewModel(
            new ObservableCollection<ProjectCardViewModel> { first, second });
        ConnectionProfile[] connections =
        [
            new ConnectionProfile
            {
                Id = "connection-a",
                Name = "连接 A",
                BaseUrl = "https://a.example.test",
            },
            new ConnectionProfile
            {
                Id = "connection-b",
                Name = "连接 B",
                BaseUrl = "https://b.example.test",
            },
        ];
        viewModel.ApplyConnections(
            connections,
            new ConnectionProfileSelection(null, "connection-a", "connection-a"));
        viewModel.SelectedProject = first;
        viewModel.SelectedCli = "Codex";
        var conversation = new ConversationRecord
        {
            Id = "codex:session-a",
            ProjectId = first.PathFingerprint,
            NativeClient = CliKind.Codex,
            NativeSessionId = "session-a",
            Title = "原会话",
            OriginalWorkingDirectory = first.Path,
            CreatedAt = DateTimeOffset.UtcNow.AddHours(-1),
            UpdatedAt = DateTimeOffset.UtcNow,
            ResumePolicy = ResumePolicy.CurrentConnection,
        };
        viewModel.PrepareResume(first, conversation);

        TerminalLaunchIntent intent = TerminalLaunchIntent.Capture(viewModel);

        viewModel.PrepareProject(second);
        viewModel.SelectedCli = "Claude Code";
        viewModel.ApplyConnections(
            connections,
            new ConnectionProfileSelection(null, "connection-b", "connection-b"));

        Assert.Equal(first.Id, intent.ProjectId);
        Assert.Equal(PathIdentity.Normalize(first.Path), intent.WorkingDirectory);
        Assert.Equal(CliKind.Codex, intent.Cli);
        Assert.Equal("connection-a", intent.ConnectionProfileId);
        Assert.Equal("连接中心当前来源 · 连接 A", intent.ConnectionLabel);
        Assert.Equal("session-a", intent.Conversation?.NativeSessionId);
    }

    [Fact]
    public void Capture_KeepsResumeWhenOnlyOriginalWorkingDirectoryMatches()
    {
        ProjectCardViewModel project = CreateProject(
            "migrated-project-id",
            Environment.CurrentDirectory,
            CliKind.Codex);
        var viewModel = new TerminalViewModel(
            new ObservableCollection<ProjectCardViewModel> { project });
        viewModel.ApplyConnections(
        [
            new ConnectionProfile
            {
                Id = "local-machine",
                Name = "本机中转",
                BaseUrl = "http://127.0.0.1:8080/v1",
            },
        ],
        new ConnectionProfileSelection(null, "local-machine", "local-machine"));
        var conversation = new ConversationRecord
        {
            Id = "codex:migrated-session",
            ProjectId = "legacy-unrelated-project-id",
            NativeClient = CliKind.Codex,
            NativeSessionId = "migrated-session",
            OriginalWorkingDirectory = project.Path + Path.DirectorySeparatorChar,
            CreatedAt = DateTimeOffset.UtcNow.AddHours(-1),
            UpdatedAt = DateTimeOffset.UtcNow,
            ResumePolicy = ResumePolicy.CurrentConnection,
        };
        viewModel.PrepareResume(project, conversation);

        TerminalLaunchIntent intent = TerminalLaunchIntent.Capture(viewModel);

        Assert.Equal("migrated-session", intent.Conversation?.NativeSessionId);
        Assert.Equal(CliLaunchMode.Resume, intent.CreateRequest(connection: null).Mode);
    }

    [Fact]
    public void Capture_RejectsLaunchingWithoutAnActiveConnectionCenterSource()
    {
        ProjectCardViewModel project = CreateProject(
            "project-a",
            Environment.CurrentDirectory,
            CliKind.Codex);
        var viewModel = new TerminalViewModel(
            new ObservableCollection<ProjectCardViewModel> { project });
        viewModel.ApplyConnections(
        [
            new ConnectionProfile
            {
                Id = "local-machine",
                Name = "本机中转",
                BaseUrl = "http://127.0.0.1:8080/v1",
            },
        ],
        new ConnectionProfileSelection(null, "local-machine", "missing-source"));

        InvalidOperationException exception = Assert.Throws<InvalidOperationException>(
            () => TerminalLaunchIntent.Capture(viewModel));

        Assert.Contains("连接中心", exception.Message, StringComparison.Ordinal);
    }

    private static ProjectCardViewModel CreateProject(
        string id,
        string path,
        CliKind cli)
    {
        var record = new ProjectRecord
        {
            Id = id,
            DisplayName = id,
            RootPath = path,
            PathFingerprint = PathIdentity.CreateStableId(path),
            DefaultCli = cli,
            CreatedAt = DateTimeOffset.UtcNow,
        };

        return new ProjectCardViewModel(
            record,
            cli == CliKind.Codex ? "Codex" : "Claude Code",
            "启动时选择",
            "测试项目",
            "刚刚",
            "T",
            conversationCount: 1,
            codexConversationCount: cli == CliKind.Codex ? 1 : 0,
            claudeConversationCount: cli == CliKind.ClaudeCode ? 1 : 0,
            geminiConversationCount: cli == CliKind.GeminiCli ? 1 : 0,
            pathAvailable: true);
    }
}
