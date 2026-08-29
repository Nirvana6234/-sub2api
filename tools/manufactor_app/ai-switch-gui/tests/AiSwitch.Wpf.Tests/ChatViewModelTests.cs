using System.Collections.ObjectModel;
using System.IO;
using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class ChatViewModelTests
{
    [Fact]
    public async Task Activate_HistoryLoadsTranscriptAndDefersOfficialConnectionUntilFirstSend()
    {
        var lifecycle = new List<string>();
        DateTimeOffset firstAt = DateTimeOffset.UtcNow.AddMinutes(-5);
        var reader = new RecordingTranscriptReader(
            new ConversationTranscript(
                SourceFound: true,
                Messages:
                [
                    new ConversationTranscriptMessage(
                        "history-user",
                        ConversationTranscriptRole.User,
                        "之前的问题",
                        firstAt),
                    new ConversationTranscriptMessage(
                        "history-assistant",
                        ConversationTranscriptRole.Assistant,
                        "之前的回答",
                        firstAt.AddMinutes(1)),
                ],
                Warnings: Array.Empty<string>()),
            lifecycle);
        await using var harness = new ChatViewModelHarness(reader, lifecycle);
        ConversationRecord conversation = CreateConversation(harness.Project, "resume-session");
        harness.Terminal.PrepareResume(harness.Project, conversation);

        harness.ViewModel.RefreshContext();

        Assert.Equal("resume-session", harness.ViewModel.NativeSessionId);
        await harness.ViewModel.ActivateAsync();

        Assert.Equal(["read"], lifecycle);
        Assert.Equal(conversation, Assert.Single(reader.Requests));
        Assert.Empty(harness.Controller.ConnectedIntents);
        Assert.Empty(harness.Controller.SentMessages);
        Assert.Collection(
            harness.ViewModel.Messages,
            message =>
            {
                Assert.Equal(ChatMessageKind.User, message.Kind);
                Assert.Equal("之前的问题", message.Text);
                Assert.Equal(firstAt, message.CreatedAt);
            },
            message =>
            {
                Assert.Equal(ChatMessageKind.Assistant, message.Kind);
                Assert.Equal("之前的回答", message.Text);
            });
        Assert.Equal("历史已载入，可以继续对话", harness.ViewModel.RuntimeStatus);

        await harness.ViewModel.ActivateAsync();
        Assert.Empty(harness.Controller.ConnectedIntents);
        Assert.Single(reader.Requests);

        harness.ViewModel.DraftText = "继续处理这个项目";
        await harness.ViewModel.SendCommand.ExecuteAsync(null);

        SentMessage sent = Assert.Single(harness.Controller.SentMessages);
        Assert.Equal("继续处理这个项目", sent.Message);
        Assert.Equal("resume-session", sent.Intent.Conversation?.NativeSessionId);
        Assert.Empty(harness.Controller.ConnectedIntents);
    }

    [Fact]
    public async Task Activate_MissingProjectStillShowsTranscriptWithoutConnecting()
    {
        var reader = new RecordingTranscriptReader(new ConversationTranscript(
            SourceFound: true,
            Messages:
            [
                new ConversationTranscriptMessage(
                    "history-user",
                    ConversationTranscriptRole.User,
                    "保留下来的历史",
                    DateTimeOffset.UtcNow.AddMinutes(-1)),
            ],
            Warnings: Array.Empty<string>()));
        await using var harness = new ChatViewModelHarness(reader);
        ConversationRecord conversation = CreateConversation(harness.Project, "missing-project-session");
        Directory.Delete(harness.Project.Path, recursive: true);
        harness.Terminal.PrepareResume(harness.Project, conversation);

        await harness.ViewModel.ActivateAsync();

        Assert.Equal("保留下来的历史", Assert.Single(harness.ViewModel.Messages).Text);
        Assert.Empty(harness.Controller.ConnectedIntents);
        Assert.Equal("历史已载入；项目目录不可用，当前仅可查看", harness.ViewModel.RuntimeStatus);
    }

    [Fact]
    public async Task Activate_AlreadyConnectedHistoryKeepsTheExistingOfficialEngine()
    {
        var reader = new RecordingTranscriptReader(new ConversationTranscript(
            SourceFound: true,
            Messages: Array.Empty<ConversationTranscriptMessage>(),
            Warnings: Array.Empty<string>()));
        await using var harness = new ChatViewModelHarness(reader);
        ConversationRecord conversation = CreateConversation(harness.Project, "already-connected-session");
        harness.Terminal.PrepareResume(harness.Project, conversation);
        await harness.Controller.ConnectAsync(new ChatLaunchIntent(
            harness.Project.Record,
            CliKind.Codex,
            ConnectionProfileIds.LocalMachine,
            "本机中转",
            Model: null,
            conversation,
            ChatPermissionMode.WorkspaceWrite));

        await harness.ViewModel.ActivateAsync();

        Assert.Single(harness.Controller.ConnectedIntents);
        Assert.Equal("already-connected-session", harness.ViewModel.NativeSessionId);
        Assert.Equal("历史会话已连接，可以继续对话", harness.ViewModel.RuntimeStatus);
    }

    [Fact]
    public async Task NewConversation_ClearsLoadedHistoryAndResumeIntent()
    {
        var reader = new RecordingTranscriptReader(new ConversationTranscript(
            SourceFound: true,
            Messages:
            [
                new ConversationTranscriptMessage(
                    "history-user",
                    ConversationTranscriptRole.User,
                    "旧消息",
                    DateTimeOffset.UtcNow),
            ],
            Warnings: Array.Empty<string>()));
        await using var harness = new ChatViewModelHarness(reader);
        harness.Terminal.PrepareResume(
            harness.Project,
            CreateConversation(harness.Project, "resume-session"));
        await harness.ViewModel.ActivateAsync();
        Assert.True(harness.ViewModel.HasMessages);

        await harness.ViewModel.NewConversationCommand.ExecuteAsync(null);

        Assert.Empty(harness.ViewModel.Messages);
        Assert.False(harness.ViewModel.HasMessages);
        Assert.Null(harness.Terminal.PendingConversation);
        Assert.Equal("尚未创建", harness.ViewModel.NativeSessionId);
        Assert.Equal("已准备新会话", harness.ViewModel.RuntimeStatus);
    }

    [Fact]
    public async Task ResumeConversation_PreparesResumeLoadsTranscriptAndDefersConnection()
    {
        var lifecycle = new List<string>();
        var reader = new RecordingTranscriptReader(
            new ConversationTranscript(
                SourceFound: true,
                Messages:
                [
                    new ConversationTranscriptMessage(
                        "selected-history-message",
                        ConversationTranscriptRole.User,
                        "选择后立即显示的历史",
                        DateTimeOffset.UtcNow.AddMinutes(-1)),
                ],
                Warnings: Array.Empty<string>()),
            lifecycle);
        await using var harness = new ChatViewModelHarness(reader, lifecycle);
        ConversationRecord conversation = CreateConversation(harness.Project, "selected-session");
        await harness.ViewModel.ResumeConversationAsync(harness.Project, conversation);

        Assert.Equal(conversation, harness.Terminal.PendingConversation);
        Assert.Equal(["read"], lifecycle);
        Assert.Empty(harness.Controller.ConnectedIntents);
        Assert.Equal("选择后立即显示的历史", Assert.Single(harness.ViewModel.Messages).Text);
        Assert.Empty(harness.Controller.SentMessages);
    }

    [Fact]
    public async Task HistoryProjectSessions_UsesUnfilteredIndexAndExactProjectIdentity()
    {
        await using var harness = new ChatViewModelHarness();
        ProjectCardViewModel otherProject = harness.AddProject("project-b", CliKind.Codex);
        DateTimeOffset now = DateTimeOffset.UtcNow;
        ConversationRecord byFingerprint = CreateConversation(harness.Project, "fingerprint-session") with
        {
            Title = "按指纹匹配",
            UpdatedAt = now.AddMinutes(-2),
        };
        ConversationRecord byWorkingDirectory = CreateConversation(harness.Project, "cwd-session") with
        {
            Id = "codex:cwd-session",
            ProjectId = "legacy-project-identity",
            Title = "按目录匹配",
            UpdatedAt = now,
        };
        ConversationRecord other = CreateConversation(otherProject, "other-session") with
        {
            Title = "其他项目",
            UpdatedAt = now.AddMinutes(1),
        };
        string nestedPath = Directory.CreateDirectory(Path.Combine(harness.Project.Path, "nested")).FullName;
        ConversationRecord nestedButNotExact = byWorkingDirectory with
        {
            Id = "codex:nested-session",
            NativeSessionId = "nested-session",
            OriginalWorkingDirectory = nestedPath,
            UpdatedAt = now.AddMinutes(2),
        };
        var history = new HistoryViewModel(() => Task.CompletedTask, _ => { });
        history.ApplySnapshot(new WorkspaceDataSnapshot(
            [harness.Project.Record, otherProject.Record],
            [other, nestedButNotExact, byFingerprint, byWorkingDirectory],
            Array.Empty<CliInstallation>(),
            Array.Empty<ConnectionProfile>(),
            Array.Empty<WorkspaceLoadError>(),
            DiscoveredProjectCount: 0,
            LoadedAt: now));

        history.SearchText = "不会命中任何标题";
        history.SelectedCliFilter = "Gemini CLI";
        Assert.Empty(history.Sessions);

        IReadOnlyList<HistorySessionViewModel> projectSessions =
            history.GetProjectSessions(harness.Project);

        Assert.Equal(2, projectSessions.Count);
        Assert.Equal("cwd-session", projectSessions[0].Record.NativeSessionId);
        Assert.Equal("fingerprint-session", projectSessions[1].Record.NativeSessionId);
        Assert.DoesNotContain(
            projectSessions,
            session => session.Record.NativeSessionId is "other-session" or "nested-session");
    }

    [Fact]
    public async Task Send_RendersStreamingAssistantToolAndSessionEvents()
    {
        await using var harness = new ChatViewModelHarness();
        DateTimeOffset now = DateTimeOffset.UtcNow;

        harness.ViewModel.DraftText = "  请检查项目  ";
        await harness.ViewModel.SendCommand.ExecuteAsync(null);

        SentMessage sent = Assert.Single(harness.Controller.SentMessages);
        Assert.Equal("请检查项目", sent.Message);
        Assert.Equal(harness.Project.Id, sent.Intent.Project.Id);
        Assert.Equal(ConnectionProfileIds.LocalMachine, sent.Intent.ConnectionProfileId);
        Assert.True(harness.ViewModel.IsBusy);
        Assert.Equal("请检查项目", Assert.Single(harness.ViewModel.Messages).Text);

        harness.Controller.Publish(new ChatSessionStartedEvent("native-session", now));
        harness.Controller.Publish(new ChatAssistantDeltaEvent("检查", now));
        harness.Controller.Publish(new ChatAssistantDeltaEvent("完成", now));
        harness.Controller.Publish(new ChatAssistantMessageEvent("检查完成", now));
        harness.Controller.Publish(new ChatToolStartedEvent("tool-1", "读取文件", "正在读取", now));
        harness.Controller.Publish(new ChatToolProgressEvent("tool-1", "已读取 2 个文件", now));
        harness.Controller.Publish(new ChatToolCompletedEvent("tool-1", "读取文件", true, null, now));
        harness.Controller.Publish(new ChatTurnCompletedEvent(true, null, now));

        Assert.Equal("native-session", harness.ViewModel.NativeSessionId);
        ChatMessageViewModel assistant = Assert.Single(
            harness.ViewModel.Messages,
            message => message.Kind == ChatMessageKind.Assistant);
        Assert.Equal("检查完成", assistant.Text);
        ChatMessageViewModel tool = Assert.Single(
            harness.ViewModel.Messages,
            message => message.Kind == ChatMessageKind.Tool);
        Assert.Equal("已完成", tool.Status);
        Assert.True(tool.IsCompleted);
        Assert.False(harness.ViewModel.IsBusy);
        Assert.Equal("回答完成", harness.ViewModel.RuntimeStatus);
    }

    [Fact]
    public async Task Send_RecordsAggregateSafeLocalTelemetryWhenTurnCompletes()
    {
        var telemetry = new RecordingLocalTelemetryRepository();
        await using var harness = new ChatViewModelHarness(localTelemetryRepository: telemetry);

        harness.ViewModel.DraftText = "请只统计本轮，不保存这句话";
        await harness.ViewModel.SendCommand.ExecuteAsync(null);

        DateTimeOffset completedAt = DateTimeOffset.UtcNow.AddMilliseconds(25);
        harness.Controller.Publish(new ChatUsageEvent(
            InputTokens: 120,
            OutputTokens: 48,
            CachedInputTokens: 12,
            Timestamp: completedAt,
            CacheCreationTokens: 7));
        harness.Controller.Publish(new ChatTurnCompletedEvent(true, null, completedAt));

        LocalUsageTelemetryEvent recorded = await telemetry.UsageRecorded.Task
            .WaitAsync(TimeSpan.FromSeconds(2));

        Assert.Equal(CliKind.Codex, recorded.CliKind);
        Assert.Equal(ConnectionProfileIds.LocalMachine, recorded.SourceId);
        Assert.Contains("本机中转", recorded.SourceLabel, StringComparison.Ordinal);
        Assert.Null(recorded.Model);
        Assert.Equal(120, recorded.InputTokens);
        Assert.Equal(48, recorded.OutputTokens);
        Assert.Equal(12, recorded.CachedInputTokens);
        Assert.Equal(7, recorded.CacheCreationTokens);
        Assert.True(recorded.Succeeded);
        Assert.NotNull(recorded.ElapsedMilliseconds);
        Assert.True(recorded.ElapsedMilliseconds >= 0);
    }

    [Fact]
    public async Task Send_RegistersOnlyStartedOrResumedWorkspaceSessionsForHistoryDeDuplication()
    {
        var registry = new RecordingManagedCliSessionRegistry();
        await using var harness = new ChatViewModelHarness(managedCliSessionRegistry: registry);
        ConversationRecord conversation = CreateConversation(harness.Project, "managed-resume-session");
        harness.Terminal.PrepareResume(harness.Project, conversation);

        // Merely opening/reading a history transcript must not hide its
        // existing official usage from the importer.
        await harness.ViewModel.ActivateAsync();
        Assert.Empty(registry.Registrations);

        harness.ViewModel.DraftText = "继续这个已恢复的会话";
        await harness.ViewModel.SendCommand.ExecuteAsync(null);
        await registry.WaitForCountAsync(1);

        ManagedSessionRegistration resumed = Assert.Single(registry.Registrations);
        Assert.Equal(CliKind.Codex, resumed.CliKind);
        Assert.Equal("managed-resume-session", resumed.NativeSessionId);

        harness.Controller.Publish(new ChatSessionStartedEvent("managed-new-session", DateTimeOffset.UtcNow));
        await registry.WaitForCountAsync(2);
        Assert.Contains(
            registry.Registrations,
            registration => registration is { CliKind: CliKind.Codex, NativeSessionId: "managed-new-session" });
    }

    [Fact]
    public async Task Send_RequiresAnActiveConnectionCenterSource()
    {
        await using var harness = new ChatViewModelHarness();
        harness.Terminal.ApplyConnections(
        [
            new ConnectionProfile
            {
                Id = ConnectionProfileIds.LocalMachine,
                Name = "本机中转",
                BaseUrl = "http://127.0.0.1:8080/v1",
            },
        ],
        new ConnectionProfileSelection(
            CloudProfileId: null,
            LocalProfileId: ConnectionProfileIds.LocalMachine,
            ActiveProfileId: "deleted-source"));
        harness.ViewModel.DraftText = "请开始";

        await harness.ViewModel.SendCommand.ExecuteAsync(null);

        Assert.Empty(harness.Controller.SentMessages);
        ChatMessageViewModel error = Assert.Single(harness.ViewModel.Messages);
        Assert.Equal(ChatMessageKind.Error, error.Kind);
        Assert.Contains("连接中心", error.Text, StringComparison.Ordinal);
    }

    [Fact]
    public async Task UserInputRequest_ReEnablesSendAndRoutesReplyToPendingRequest()
    {
        await using var harness = new ChatViewModelHarness();
        DateTimeOffset now = DateTimeOffset.UtcNow;

        harness.ViewModel.DraftText = "开始";
        await harness.ViewModel.SendCommand.ExecuteAsync(null);
        Assert.True(harness.ViewModel.IsBusy);

        harness.Controller.Publish(new ChatUserInputRequestedEvent(
            "input-1",
            "请选择处理范围",
            ["当前文件", "整个项目"],
            now));

        Assert.False(harness.ViewModel.IsBusy);
        Assert.Equal("等待你的输入", harness.ViewModel.RuntimeStatus);
        Assert.Contains("当前文件 / 整个项目", harness.ViewModel.Messages[^1].Text, StringComparison.Ordinal);

        harness.ViewModel.DraftText = "整个项目";
        Assert.True(harness.ViewModel.SendCommand.CanExecute(null));
        await harness.ViewModel.SendCommand.ExecuteAsync(null);

        UserInputReply reply = Assert.Single(harness.Controller.UserInputReplies);
        Assert.Equal("input-1", reply.RequestId);
        Assert.Equal("整个项目", reply.Response);
        Assert.True(harness.ViewModel.IsBusy);
        Assert.Equal("已提交补充信息，正在继续…", harness.ViewModel.RuntimeStatus);

        harness.Controller.Publish(new ChatTurnCompletedEvent(true, null, now));
        Assert.False(harness.ViewModel.IsBusy);
    }

    [Fact]
    public async Task ApprovalCommands_RouteAllowAndDenyAndCompleteTheirCards()
    {
        await using var harness = new ChatViewModelHarness();
        DateTimeOffset now = DateTimeOffset.UtcNow;
        harness.Controller.Publish(new ChatApprovalRequestedEvent(
            "approval-allow",
            "允许写入？",
            "将修改项目文件",
            [ChatApprovalDecision.AllowOnce, ChatApprovalDecision.Deny],
            now));
        harness.Controller.Publish(new ChatApprovalRequestedEvent(
            "approval-deny",
            "允许执行？",
            "将运行命令",
            [ChatApprovalDecision.AllowOnce, ChatApprovalDecision.Deny],
            now));
        ChatMessageViewModel[] approvals = harness.ViewModel.Messages
            .Where(message => message.Kind == ChatMessageKind.Approval)
            .ToArray();

        await harness.ViewModel.AllowOnceCommand.ExecuteAsync(approvals[0]);
        await harness.ViewModel.DenyCommand.ExecuteAsync(approvals[1]);

        Assert.Equal(
            [
                new ApprovalReply("approval-allow", ChatApprovalDecision.AllowOnce),
                new ApprovalReply("approval-deny", ChatApprovalDecision.Deny),
            ],
            harness.Controller.ApprovalReplies);
        Assert.Equal("本次已允许", approvals[0].Status);
        Assert.Equal("已拒绝", approvals[1].Status);
        Assert.All(approvals, approval => Assert.True(approval.IsCompleted));
    }

    [Fact]
    public async Task ProjectSwitch_StartsNewContextWhileMatchingConversationKeepsNativeSessionResume()
    {
        await using var harness = new ChatViewModelHarness();
        ConversationRecord conversation = CreateConversation(harness.Project, "resume-session");
        harness.Terminal.PrepareResume(harness.Project, conversation);
        harness.ViewModel.RefreshContext();

        harness.ViewModel.DraftText = "继续上次工作";
        await harness.ViewModel.SendCommand.ExecuteAsync(null);
        ChatLaunchIntent resumed = harness.Controller.SentMessages[0].Intent;
        Assert.Equal("resume-session", resumed.Conversation?.NativeSessionId);
        Assert.Equal(harness.Project.PathFingerprint, resumed.Project.PathFingerprint);

        harness.Controller.Publish(new ChatTurnCompletedEvent(true, null, DateTimeOffset.UtcNow));
        ProjectCardViewModel secondProject = harness.AddProject("project-b", CliKind.ClaudeCode);
        harness.Terminal.PrepareProject(secondProject);
        harness.ViewModel.RefreshContext();
        harness.Controller.Publish(new ChatAssistantMessageEvent(
            "旧项目迟到的事件",
            DateTimeOffset.UtcNow));
        Assert.Empty(harness.ViewModel.Messages);
        harness.ViewModel.DraftText = "开始新项目";
        await harness.ViewModel.SendCommand.ExecuteAsync(null);

        ChatLaunchIntent switched = harness.Controller.SentMessages[1].Intent;
        Assert.Equal(secondProject.PathFingerprint, switched.Project.PathFingerprint);
        Assert.Equal(CliKind.ClaudeCode, switched.Cli);
        Assert.Null(switched.Conversation);
    }

    private static ConversationRecord CreateConversation(
        ProjectCardViewModel project,
        string nativeSessionId)
        => new()
        {
            Id = $"codex:{nativeSessionId}",
            ProjectId = project.PathFingerprint,
            NativeClient = CliKind.Codex,
            NativeSessionId = nativeSessionId,
            OriginalWorkingDirectory = project.Path,
            CreatedAt = DateTimeOffset.UtcNow.AddMinutes(-10),
            UpdatedAt = DateTimeOffset.UtcNow,
            ResumePolicy = ResumePolicy.CurrentConnection,
        };

    private static HistorySessionViewModel CreateHistorySession(
        ConversationRecord conversation,
        string title) =>
        new(
            conversation,
            title,
            Path.GetFileName(conversation.OriginalWorkingDirectory),
            WorkspaceDisplay.CliName(conversation.NativeClient),
            "与当前连接独立",
            "刚刚",
            "可继续",
            $"工作目录 · {conversation.OriginalWorkingDirectory}");

    private sealed class ChatViewModelHarness : IAsyncDisposable
    {
        private readonly string _root;

        public ChatViewModelHarness(
            IConversationTranscriptReader? transcriptReader = null,
            List<string>? lifecycle = null,
            ILocalTelemetryRepository? localTelemetryRepository = null,
            IManagedCliSessionRegistry? managedCliSessionRegistry = null)
        {
            _root = Path.Combine(
                Path.GetTempPath(),
                "LanAi.ChatViewModel.Tests",
                Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(_root);
            Project = AddProject("project-a", CliKind.Codex);
            Terminal = new TerminalViewModel(new ObservableCollection<ProjectCardViewModel> { Project });
            Terminal.ApplyConnections(
            [
                new ConnectionProfile
                {
                    Id = ConnectionProfileIds.LocalMachine,
                    Name = "本机中转",
                    BaseUrl = "http://127.0.0.1:8080/v1",
                },
            ],
            new ConnectionProfileSelection(
                CloudProfileId: null,
                LocalProfileId: ConnectionProfileIds.LocalMachine,
                ActiveProfileId: ConnectionProfileIds.LocalMachine));
            Terminal.PrepareProject(Project);
            Controller = new RecordingChatSessionController();
            Controller.Lifecycle = lifecycle;
            ViewModel = new ChatViewModel(
                Terminal,
                Controller,
                () => Task.CompletedTask,
                transcriptReader,
                localTelemetryRepository,
                managedCliSessionRegistry);
        }

        public ProjectCardViewModel Project { get; }

        public TerminalViewModel Terminal { get; }

        public RecordingChatSessionController Controller { get; }

        public ChatViewModel ViewModel { get; }

        public ProjectCardViewModel AddProject(string name, CliKind cli)
        {
            string path = Directory.CreateDirectory(Path.Combine(_root, name)).FullName;
            string fingerprint = PathIdentity.CreateStableId(path);
            var record = new ProjectRecord
            {
                Id = fingerprint,
                DisplayName = name,
                RootPath = path,
                PathFingerprint = fingerprint,
                DefaultCli = cli,
                CreatedAt = DateTimeOffset.UtcNow,
            };
            var card = new ProjectCardViewModel(
                record,
                cli switch
                {
                    CliKind.ClaudeCode => "Claude Code",
                    CliKind.GeminiCli => "Gemini CLI",
                    _ => "Codex",
                },
                "启动时选择",
                "测试项目",
                "刚刚",
                "T",
                conversationCount: 0,
                codexConversationCount: 0,
                claudeConversationCount: 0,
                geminiConversationCount: 0,
                pathAvailable: true);
            if (Terminal is not null && !Terminal.Projects.Contains(card))
            {
                Terminal.Projects.Add(card);
            }

            return card;
        }

        public async ValueTask DisposeAsync()
        {
            await ViewModel.DisposeAsync();
            string fullRoot = Path.GetFullPath(_root);
            string safeParent = Path.GetFullPath(
                Path.Combine(Path.GetTempPath(), "LanAi.ChatViewModel.Tests"));
            if (fullRoot.StartsWith(safeParent, StringComparison.OrdinalIgnoreCase) && Directory.Exists(fullRoot))
            {
                Directory.Delete(fullRoot, recursive: true);
            }
        }
    }

    private sealed class RecordingChatSessionController : IChatSessionController
    {
        public ChatEngineState State { get; private set; } = ChatEngineState.Created;

        public string? NativeSessionId { get; private set; }

        public string? ActiveProjectFingerprint { get; private set; }

        public List<SentMessage> SentMessages { get; } = [];

        public List<ChatLaunchIntent> ConnectedIntents { get; } = [];

        public List<ApprovalReply> ApprovalReplies { get; } = [];

        public List<UserInputReply> UserInputReplies { get; } = [];

        public List<string>? Lifecycle { get; set; }

        public event EventHandler<ChatEvent>? EventReceived;

        public Task ConnectAsync(
            ChatLaunchIntent intent,
            CancellationToken cancellationToken = default)
        {
            ConnectedIntents.Add(intent);
            Lifecycle?.Add("connect");
            ActiveProjectFingerprint = intent.Project.PathFingerprint;
            NativeSessionId = intent.Conversation?.NativeSessionId ?? $"new-{Guid.NewGuid():N}";
            State = ChatEngineState.Ready;
            return Task.CompletedTask;
        }

        public Task SendAsync(
            ChatLaunchIntent intent,
            string message,
            CancellationToken cancellationToken = default)
        {
            SentMessages.Add(new SentMessage(intent, message));
            ActiveProjectFingerprint = intent.Project.PathFingerprint;
            State = ChatEngineState.RunningTurn;
            return Task.CompletedTask;
        }

        public Task RespondToApprovalAsync(
            string requestId,
            ChatApprovalDecision decision,
            CancellationToken cancellationToken = default)
        {
            ApprovalReplies.Add(new ApprovalReply(requestId, decision));
            State = ChatEngineState.RunningTurn;
            return Task.CompletedTask;
        }

        public Task RespondToUserInputAsync(
            string requestId,
            string response,
            CancellationToken cancellationToken = default)
        {
            UserInputReplies.Add(new UserInputReply(requestId, response));
            State = ChatEngineState.RunningTurn;
            return Task.CompletedTask;
        }

        public Task CancelTurnAsync(CancellationToken cancellationToken = default)
        {
            State = ChatEngineState.Ready;
            return Task.CompletedTask;
        }

        public Task ResetAsync(CancellationToken cancellationToken = default)
        {
            State = ChatEngineState.Created;
            NativeSessionId = null;
            ActiveProjectFingerprint = null;
            return Task.CompletedTask;
        }

        public void Publish(ChatEvent chatEvent)
        {
            switch (chatEvent)
            {
                case ChatSessionStartedEvent session:
                    NativeSessionId = session.NativeSessionId;
                    break;
                case ChatUserInputRequestedEvent:
                case ChatApprovalRequestedEvent:
                    State = ChatEngineState.WaitingForApproval;
                    break;
                case ChatTurnCompletedEvent:
                    State = ChatEngineState.Ready;
                    break;
            }

            EventReceived?.Invoke(this, chatEvent);
        }

        public ValueTask DisposeAsync()
        {
            State = ChatEngineState.Stopped;
            return ValueTask.CompletedTask;
        }
    }

    private sealed record SentMessage(ChatLaunchIntent Intent, string Message);

    private sealed record ApprovalReply(string RequestId, ChatApprovalDecision Decision);

    private sealed record UserInputReply(string RequestId, string Response);

    private sealed class RecordingLocalTelemetryRepository : ILocalTelemetryRepository
    {
        public TaskCompletionSource<LocalUsageTelemetryEvent> UsageRecorded { get; } = new(
            TaskCreationOptions.RunContinuationsAsynchronously);

        public Task RecordUsageAsync(
            LocalUsageTelemetryEvent telemetryEvent,
            CancellationToken cancellationToken = default)
        {
            UsageRecorded.TrySetResult(telemetryEvent);
            return Task.CompletedTask;
        }

        public Task RecordNetworkProbeAsync(
            LocalNetworkHealthProbe probe,
            CancellationToken cancellationToken = default) => Task.CompletedTask;

        public Task<LocalTelemetrySnapshot> GetSnapshotAsync(
            TimeZoneInfo? timeZone = null,
            CancellationToken cancellationToken = default) =>
            Task.FromResult(new LocalTelemetrySnapshot(
                DateTimeOffset.UtcNow,
                LocalTelemetryUsageSummary.Empty,
                LocalTelemetryUsageSummary.Empty,
                Array.Empty<LocalTelemetryDailyUsage>(),
                LatestNetworkStatus: null));
    }

    private sealed class RecordingManagedCliSessionRegistry : IManagedCliSessionRegistry
    {
        private readonly object _gate = new();
        private readonly List<ManagedSessionRegistration> _registrations = [];

        public IReadOnlyList<ManagedSessionRegistration> Registrations
        {
            get
            {
                lock (_gate)
                {
                    return _registrations.ToArray();
                }
            }
        }

        public Task RegisterManagedSessionAsync(
            CliKind cliKind,
            string nativeSessionId,
            CancellationToken cancellationToken = default)
        {
            lock (_gate)
            {
                _registrations.Add(new ManagedSessionRegistration(cliKind, nativeSessionId));
            }

            return Task.CompletedTask;
        }

        public async Task WaitForCountAsync(int expectedCount)
        {
            DateTimeOffset deadline = DateTimeOffset.UtcNow.AddSeconds(2);
            while (true)
            {
                if (Registrations.Count >= expectedCount)
                {
                    return;
                }

                TimeSpan remaining = deadline - DateTimeOffset.UtcNow;
                if (remaining <= TimeSpan.Zero)
                {
                    throw new TimeoutException($"Expected {expectedCount} managed-session registrations.");
                }

                await Task.Delay(TimeSpan.FromMilliseconds(Math.Min(10, remaining.TotalMilliseconds)));
            }
        }
    }

    private sealed record ManagedSessionRegistration(CliKind CliKind, string NativeSessionId);

    private sealed class RecordingTranscriptReader : IConversationTranscriptReader
    {
        private readonly ConversationTranscript _transcript;
        private readonly List<string>? _lifecycle;

        public RecordingTranscriptReader(
            ConversationTranscript transcript,
            List<string>? lifecycle = null)
        {
            _transcript = transcript;
            _lifecycle = lifecycle;
        }

        public List<ConversationRecord> Requests { get; } = [];

        public Task<ConversationTranscript> ReadAsync(
            ConversationRecord conversation,
            CancellationToken cancellationToken = default)
        {
            Requests.Add(conversation);
            _lifecycle?.Add("read");
            return Task.FromResult(_transcript);
        }
    }
}
