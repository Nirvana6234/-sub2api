using System.Collections.ObjectModel;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class TerminalViewModelTests
{
    [Fact]
    public void ReturnToPreviousPage_InvokesConfiguredCallback()
    {
        int callbackCount = 0;
        var viewModel = new TerminalViewModel(
            new ObservableCollection<ProjectCardViewModel>(),
            returnToPreviousPage: () => callbackCount++);

        viewModel.ReturnToPreviousPage();

        Assert.Equal(1, callbackCount);
    }

    [Fact]
    public void ReturnToPreviousPage_WithoutCallback_DoesNotThrow()
    {
        var viewModel = new TerminalViewModel(new ObservableCollection<ProjectCardViewModel>());

        Exception? exception = Record.Exception(viewModel.ReturnToPreviousPage);

        Assert.Null(exception);
    }

    [Fact]
    public void PrepareResume_PinnedConnectionUsesConversationSourceAndLocksSelection()
    {
        ProjectCardViewModel project = CreateProject("project-a", @"C:\work\project-a");
        var viewModel = new TerminalViewModel(new ObservableCollection<ProjectCardViewModel> { project });
        viewModel.ApplyConnections(
        [
            new ConnectionProfile
            {
                Id = "current",
                Name = "当前连接",
                BaseUrl = "https://current.example.test",
            },
            new ConnectionProfile
            {
                Id = "pinned",
                Name = "会话原连接",
                BaseUrl = "https://pinned.example.test",
            },
        ],
        new ConnectionProfileSelection(
            CloudProfileId: null,
            LocalProfileId: "current",
            ActiveProfileId: "current"));
        ConversationRecord conversation = CreateConversation(
            project,
            ResumePolicy.PinnedConnection,
            lastSourceProfileId: "pinned");

        viewModel.PrepareResume(project, conversation);

        Assert.Equal("pinned", viewModel.EffectiveConnectionProfileId);
        Assert.Equal("会话绑定 · 会话原连接", viewModel.SelectedConnection);
        Assert.Same(conversation, viewModel.PendingConversation);
    }

    [Fact]
    public void ApplyConnections_UsesActiveSourceBeforeLegacyLocalFallback()
    {
        var viewModel = new TerminalViewModel(new ObservableCollection<ProjectCardViewModel>());
        viewModel.ApplyConnections(
        [
            new ConnectionProfile { Id = "local-machine", Name = "本机中转", BaseUrl = "http://127.0.0.1:8080/v1" },
            new ConnectionProfile { Id = "cloud-a", Name = "远程中转", BaseUrl = "https://cloud.example.test/v1" },
        ],
        new ConnectionProfileSelection(
            CloudProfileId: "cloud-a",
            LocalProfileId: "local-machine",
            ActiveProfileId: "cloud-a"));

        Assert.Equal("cloud-a", viewModel.EffectiveConnectionProfileId);
        Assert.Equal("连接中心当前来源 · 远程中转", viewModel.SelectedConnection);
    }

    [Fact]
    public void ApplyConnections_UsesLegacyLocalSelectionOnlyWhenActiveSourceIsAbsent()
    {
        var viewModel = new TerminalViewModel(new ObservableCollection<ProjectCardViewModel>());
        viewModel.ApplyConnections(
        [
            new ConnectionProfile { Id = "local-machine", Name = "本机中转", BaseUrl = "http://127.0.0.1:8080/v1" },
            new ConnectionProfile { Id = "cloud-a", Name = "远程中转", BaseUrl = "https://cloud.example.test/v1" },
        ],
        new ConnectionProfileSelection(
            CloudProfileId: "cloud-a",
            LocalProfileId: "local-machine",
            ActiveProfileId: null));

        Assert.Equal("local-machine", viewModel.EffectiveConnectionProfileId);
        Assert.Equal("连接中心当前来源 · 本机中转", viewModel.SelectedConnection);
    }

    [Fact]
    public void ApplyConnections_DoesNotSilentlyReplaceAnInvalidExplicitActiveSource()
    {
        var viewModel = new TerminalViewModel(new ObservableCollection<ProjectCardViewModel>());
        viewModel.ApplyConnections(
        [
            new ConnectionProfile { Id = "local-machine", Name = "本机中转", BaseUrl = "http://127.0.0.1:8080/v1" },
        ],
        new ConnectionProfileSelection(
            CloudProfileId: null,
            LocalProfileId: "local-machine",
            ActiveProfileId: "deleted-cloud"));

        Assert.Null(viewModel.EffectiveConnectionProfileId);
        Assert.Equal("连接中心尚未选择有效来源", viewModel.SelectedConnection);
    }

    [Fact]
    public void PrepareProject_UsesConnectionCenterActiveSourceInsteadOfProjectDefault()
    {
        ProjectCardViewModel project = CreateProject("project-a", @"C:\\work\\project-a");
        ProjectCardViewModel withDifferentDefault = new(
            project.Record with { DefaultConnectionProfileId = "different-source" },
            "Codex",
            "项目旧默认",
            "测试项目",
            "刚刚",
            "T",
            conversationCount: 1,
            codexConversationCount: 1,
            claudeConversationCount: 0,
            geminiConversationCount: 0,
            pathAvailable: true);
        var viewModel = new TerminalViewModel(
            new ObservableCollection<ProjectCardViewModel> { withDifferentDefault });
        viewModel.ApplyConnections(
        [
            new ConnectionProfile { Id = "active-source", Name = "当前来源", BaseUrl = "https://active.example.test/v1" },
            new ConnectionProfile { Id = "different-source", Name = "项目旧默认", BaseUrl = "https://old.example.test/v1" },
        ],
        new ConnectionProfileSelection(null, "active-source", "active-source"));

        viewModel.PrepareProject(withDifferentDefault);

        Assert.Equal("active-source", viewModel.EffectiveConnectionProfileId);
        Assert.Equal("连接中心当前来源 · 当前来源", viewModel.SelectedConnection);
    }

    [Fact]
    public void PrepareResume_WithoutMappedProjectKeepsNativeResumeIntent()
    {
        var viewModel = new TerminalViewModel(new ObservableCollection<ProjectCardViewModel>());
        ProjectCardViewModel original = CreateProject("missing-project", @"C:\missing\project");
        ConversationRecord conversation = CreateConversation(
            original,
            ResumePolicy.CurrentConnection,
            lastSourceProfileId: null);

        viewModel.PrepareResume(project: null, conversation);

        Assert.Null(viewModel.SelectedProject);
        Assert.Same(conversation, viewModel.PendingConversation);
        Assert.Contains("恢复意图", viewModel.TerminalNotice, StringComparison.Ordinal);
    }

    [Fact]
    public void AutoStartRequest_IsConsumedExactlyOnce()
    {
        ProjectCardViewModel project = CreateProject("project-a", @"C:\work\project-a");
        var viewModel = new TerminalViewModel(new ObservableCollection<ProjectCardViewModel> { project });
        ConversationRecord conversation = CreateConversation(
            project,
            ResumePolicy.CurrentConnection,
            lastSourceProfileId: null);
        viewModel.PrepareResume(project, conversation);

        viewModel.RequestAutoStart();

        Assert.True(viewModel.ConsumeAutoStartRequest());
        Assert.False(viewModel.ConsumeAutoStartRequest());
    }

    [Fact]
    public void SelectingDifferentProjectClearsResumeButMatchingPathKeepsIt()
    {
        ProjectCardViewModel indexed = CreateProject("indexed-id", @"C:\work\same-project");
        ProjectCardViewModel samePath = CreateProject("different-id", @"C:\work\same-project");
        ProjectCardViewModel other = CreateProject("other-id", @"C:\work\other-project");
        var viewModel = new TerminalViewModel(
            new ObservableCollection<ProjectCardViewModel> { indexed, samePath, other });
        ConversationRecord conversation = CreateConversation(
            indexed,
            ResumePolicy.CurrentConnection,
            lastSourceProfileId: null);

        viewModel.PrepareResume(indexed, conversation);
        viewModel.SelectedProject = samePath;
        Assert.Same(conversation, viewModel.PendingConversation);

        viewModel.SelectedProject = other;
        Assert.Null(viewModel.PendingConversation);
    }

    [Fact]
    public void ProjectSnapshotRefresh_PreservesSelectedProjectAndPendingConversationByStableIdentity()
    {
        ProjectCardViewModel first = CreateProject("project-a", @"C:\work\project-a");
        ProjectCardViewModel selected = CreateProject("project-b", @"C:\work\project-b");
        var projects = new ObservableCollection<ProjectCardViewModel> { first, selected };
        var viewModel = new TerminalViewModel(projects);
        ConversationRecord conversation = CreateConversation(
            selected,
            ResumePolicy.CurrentConnection,
            lastSourceProfileId: null);
        viewModel.PrepareResume(selected, conversation);
        TerminalProjectRefreshState refreshState = viewModel.BeginProjectSnapshotRefresh();

        projects.Clear();
        ProjectCardViewModel refreshedFirst = CreateProject("project-a", @"C:\work\project-a");
        ProjectCardViewModel refreshedSelected = CreateProject("project-b", @"C:\work\project-b");
        projects.Add(refreshedFirst);
        projects.Add(refreshedSelected);
        viewModel.CompleteProjectSnapshotRefresh(refreshState);

        Assert.Same(refreshedSelected, viewModel.SelectedProject);
        Assert.Same(conversation, viewModel.PendingConversation);
        Assert.Equal(CliKind.Codex, viewModel.SelectedCliKind);
    }

    [Fact]
    public void ProjectSnapshotRefresh_KeepsUnmappedPendingConversationForHistoryOnlyMode()
    {
        var projects = new ObservableCollection<ProjectCardViewModel>();
        var viewModel = new TerminalViewModel(projects);
        ProjectCardViewModel missing = CreateProject("missing", @"C:\missing\project");
        ConversationRecord conversation = CreateConversation(
            missing,
            ResumePolicy.CurrentConnection,
            lastSourceProfileId: null);
        viewModel.PrepareResume(project: null, conversation);
        TerminalProjectRefreshState refreshState = viewModel.BeginProjectSnapshotRefresh();

        projects.Add(CreateProject("other", @"C:\work\other"));
        viewModel.CompleteProjectSnapshotRefresh(refreshState);

        Assert.Null(viewModel.SelectedProject);
        Assert.Same(conversation, viewModel.PendingConversation);
    }

    private static ProjectCardViewModel CreateProject(string id, string path)
    {
        var record = new ProjectRecord
        {
            Id = id,
            DisplayName = id,
            RootPath = path,
            PathFingerprint = id,
            DefaultCli = CliKind.Codex,
            CreatedAt = DateTimeOffset.UtcNow,
        };

        return new ProjectCardViewModel(
            record,
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
    }

    private static ConversationRecord CreateConversation(
        ProjectCardViewModel project,
        ResumePolicy resumePolicy,
        string? lastSourceProfileId)
        => new()
        {
            Id = "codex:native-session",
            ProjectId = project.PathFingerprint,
            NativeClient = CliKind.Codex,
            NativeSessionId = "native-session",
            OriginalWorkingDirectory = project.Path,
            CreatedAt = DateTimeOffset.UtcNow.AddHours(-1),
            UpdatedAt = DateTimeOffset.UtcNow,
            ResumePolicy = resumePolicy,
            SourceProfileIdAtStart = "original",
            LastSourceProfileId = lastSourceProfileId,
        };
}
