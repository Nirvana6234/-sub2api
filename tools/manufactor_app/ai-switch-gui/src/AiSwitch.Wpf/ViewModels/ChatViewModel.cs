using System.Collections.ObjectModel;
using System.ComponentModel;
using System.IO;
using System.Windows;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Wpf.Services;

namespace LanAi.Workspace.Wpf.ViewModels;

internal sealed record ChatLaunchIntent(
    ProjectRecord Project,
    CliKind Cli,
    string? ConnectionProfileId,
    string ConnectionLabel,
    string? Model,
    ConversationRecord? Conversation,
    ChatPermissionMode PermissionMode)
{
    public static ChatLaunchIntent Capture(
        TerminalViewModel launchOptions,
        ChatPermissionMode permissionMode)
    {
        ArgumentNullException.ThrowIfNull(launchOptions);
        ProjectRecord project = launchOptions.SelectedProjectRecord
            ?? throw new InvalidOperationException("请先选择一个项目。");
        string workingDirectory = PathIdentity.Normalize(project.RootPath);
        if (!Directory.Exists(workingDirectory))
        {
            throw new DirectoryNotFoundException($"项目目录不存在：{workingDirectory}");
        }

        ProjectRecord normalizedProject = project with { RootPath = workingDirectory };
        ConversationRecord? conversation = launchOptions.PendingConversation;
        if (conversation is not null &&
            (conversation.NativeClient != launchOptions.SelectedCliKind ||
             !PathsEqual(conversation.OriginalWorkingDirectory, workingDirectory)))
        {
            conversation = null;
        }

        string? connectionProfileId = launchOptions.EffectiveConnectionProfileId;
        if (string.IsNullOrWhiteSpace(connectionProfileId))
        {
            string message = conversation?.ResumePolicy == ResumePolicy.PinnedConnection
                ? "该历史会话没有可用的绑定连接来源，请先在连接中心恢复该来源后再继续。"
                : "请先在连接中心选择一个有效连接来源，再开始对话。";
            throw new InvalidOperationException(message);
        }

        return new ChatLaunchIntent(
            normalizedProject,
            launchOptions.SelectedCliKind,
            connectionProfileId,
            launchOptions.SelectedConnection,
            project.DefaultCli == launchOptions.SelectedCliKind ? project.DefaultModel : null,
            conversation,
            permissionMode);
    }

    private static bool PathsEqual(string left, string right)
    {
        try
        {
            return string.Equals(
                PathIdentity.Normalize(left),
                PathIdentity.Normalize(right),
                OperatingSystem.IsWindows()
                    ? StringComparison.OrdinalIgnoreCase
                    : StringComparison.Ordinal);
        }
        catch (Exception exception) when (
            exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            return false;
        }
    }
}

public enum ChatMessageKind
{
    User,
    Assistant,
    Tool,
    Approval,
    System,
    Error,
}

public partial class ChatMessageViewModel : ObservableObject
{
    public ChatMessageViewModel(
        ChatMessageKind kind,
        string title,
        string text,
        string? correlationId = null,
        DateTimeOffset? createdAt = null)
    {
        Kind = kind;
        Title = title;
        this.text = text;
        CorrelationId = correlationId;
        CreatedAt = createdAt ?? DateTimeOffset.Now;
    }

    public ChatMessageKind Kind { get; }

    public string Title { get; }

    public string? CorrelationId { get; }

    public DateTimeOffset CreatedAt { get; }

    public string TimeLabel => CreatedAt.ToString("HH:mm");

    [ObservableProperty]
    private string text;

    [ObservableProperty]
    private string status = string.Empty;

    [ObservableProperty]
    private bool isCompleted;

    public bool IsApproval => Kind == ChatMessageKind.Approval;
}

public partial class ChatViewModel : PageViewModel, IAsyncDisposable
{
    private readonly IChatSessionController _controller;
    private readonly IConversationTranscriptReader _transcriptReader;
    private readonly Func<Task> _openAdvancedTerminal;
    private readonly ILocalTelemetryRepository? _localTelemetryRepository;
    private readonly IManagedCliSessionRegistry? _managedCliSessionRegistry;
    private readonly Dictionary<string, ChatMessageViewModel> _toolMessages = new(StringComparer.Ordinal);
    private readonly HashSet<string> _loadedTranscriptMessageIds = new(StringComparer.Ordinal);
    private readonly HashSet<string> _historicalMessageFingerprints = new(StringComparer.Ordinal);
    private readonly SemaphoreSlim _activationGate = new(1, 1);
    private readonly CancellationTokenSource _lifetime = new();
    private CancellationTokenSource _contextCancellation = new();
    private ChatMessageViewModel? _streamingAssistant;
    private string? _pendingUserInputRequestId;
    private string? _displayContextKey;
    private string? _loadedTranscriptContextKey;
    private bool _suppressHistoricalReplayDuplicates;
    private volatile bool _acceptControllerEvents = true;
    private LocalTurnTelemetry? _activeTurnTelemetry;
    private bool _disposed;

    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(SendCommand))]
    private string draftText = string.Empty;

    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(SendCommand))]
    private bool isBusy;

    [ObservableProperty]
    private bool hasMessages;

    [ObservableProperty]
    private string runtimeStatus = "准备就绪";

    [ObservableProperty]
    private string nativeSessionId = "尚未创建";

    [ObservableProperty]
    private string selectedPermissionMode = "工作区可编辑";

    internal ChatViewModel(
        TerminalViewModel launchOptions,
        IChatSessionController controller,
        Func<Task> openAdvancedTerminal,
        IConversationTranscriptReader? transcriptReader = null,
        ILocalTelemetryRepository? localTelemetryRepository = null,
        IManagedCliSessionRegistry? managedCliSessionRegistry = null)
        : base("AI 对话", "像使用官方 App 一样交流；真实 CLI 在后台负责项目理解、工具与会话恢复。")
    {
        LaunchOptions = launchOptions ?? throw new ArgumentNullException(nameof(launchOptions));
        _controller = controller ?? throw new ArgumentNullException(nameof(controller));
        _openAdvancedTerminal = openAdvancedTerminal ?? throw new ArgumentNullException(nameof(openAdvancedTerminal));
        _transcriptReader = transcriptReader ?? EmptyConversationTranscriptReader.Instance;
        _localTelemetryRepository = localTelemetryRepository;
        _managedCliSessionRegistry = managedCliSessionRegistry;
        _controller.EventReceived += Controller_OnEventReceived;
        LaunchOptions.PropertyChanged += LaunchOptions_OnPropertyChanged;

        Messages = new ObservableCollection<ChatMessageViewModel>();
        PermissionModes = new ObservableCollection<string>
        {
            "只读分析",
            "工作区可编辑",
            "完全访问",
        };
        Suggestions = new ObservableCollection<string>
        {
            "帮我理解这个项目的结构",
            "检查当前改动有没有明显问题",
            "告诉我下一步应该从哪里开始",
        };
    }

    public TerminalViewModel LaunchOptions { get; }

    public ObservableCollection<ChatMessageViewModel> Messages { get; }

    public ObservableCollection<string> PermissionModes { get; }

    public ObservableCollection<string> Suggestions { get; }

    public string CurrentProjectName => LaunchOptions.SelectedProject?.Name ?? "尚未选择项目";

    public string CurrentProjectPath => LaunchOptions.SelectedProject?.Path ?? "请先从项目中心进入";

    public string CurrentCli => LaunchOptions.SelectedCli;

    public string CurrentConnection => LaunchOptions.SelectedConnection;

    public void RefreshContext()
    {
        OnPropertyChanged(nameof(CurrentProjectName));
        OnPropertyChanged(nameof(CurrentProjectPath));
        OnPropertyChanged(nameof(CurrentCli));
        OnPropertyChanged(nameof(CurrentConnection));

        string contextKey = BuildDisplayContextKey();
        bool contextChanged = !string.Equals(_displayContextKey, contextKey, StringComparison.Ordinal);
        if (contextChanged)
        {
            RenewContextCancellation();
            _displayContextKey = contextKey;
            _loadedTranscriptContextKey = null;
            _acceptControllerEvents = false;
            ClearDisplayedMessages();
        }

        ConversationRecord? pendingConversation = LaunchOptions.PendingConversation;
        if (pendingConversation is not null)
        {
            NativeSessionId = pendingConversation.NativeSessionId;
            if (!IsBusy)
            {
                bool isConnected = string.Equals(
                        _controller.ActiveProjectFingerprint,
                        LaunchOptions.SelectedProject?.PathFingerprint,
                        StringComparison.OrdinalIgnoreCase) &&
                    string.Equals(
                        _controller.NativeSessionId,
                        pendingConversation.NativeSessionId,
                        StringComparison.Ordinal);
                RuntimeStatus = isConnected
                    ? "历史会话已连接，可以继续对话"
                    : "正在准备历史会话…";
            }
            return;
        }

        NativeSessionId = string.Equals(
                _controller.ActiveProjectFingerprint,
                LaunchOptions.SelectedProject?.PathFingerprint,
                StringComparison.OrdinalIgnoreCase) &&
            !string.IsNullOrWhiteSpace(_controller.NativeSessionId)
                ? _controller.NativeSessionId
                : "尚未创建";
        if (contextChanged && !IsBusy)
        {
            RuntimeStatus = "准备就绪";
        }
    }

    public async Task ActivateAsync()
    {
        if (_disposed)
        {
            return;
        }

        RefreshContext();
        ConversationRecord? conversation = LaunchOptions.PendingConversation;
        if (conversation is null)
        {
            return;
        }

        string displayContextKey = BuildDisplayContextKey();
        using var activationCancellation = CancellationTokenSource.CreateLinkedTokenSource(
            _lifetime.Token,
            _contextCancellation.Token);
        CancellationToken cancellationToken = activationCancellation.Token;

        try
        {
            await _activationGate.WaitAsync(cancellationToken);
            try
            {
                if (!IsCurrentDisplayContext(displayContextKey, conversation))
                {
                    return;
                }

                IsBusy = true;
                if (!string.Equals(
                        _loadedTranscriptContextKey,
                        displayContextKey,
                        StringComparison.Ordinal))
                {
                    await LoadTranscriptAsync(conversation, displayContextKey, cancellationToken);
                }

                if (!IsCurrentDisplayContext(displayContextKey, conversation))
                {
                    return;
                }

                // Opening a historical session must never wait for the official
                // CLI process to start.  The transcript is useful immediately,
                // while Codex app-server startup can take several seconds on a
                // cold machine.  The first user message calls controller.SendAsync,
                // which then starts or resumes the exact native session.
                if (IsControllerConnectedTo(conversation))
                {
                    _acceptControllerEvents = true;
                    NativeSessionId = _controller.NativeSessionId ?? conversation.NativeSessionId;
                    RuntimeStatus = "历史会话已连接，可以继续对话";
                    return;
                }

                _acceptControllerEvents = false;
                RuntimeStatus = IsSelectedProjectDirectoryAvailable()
                    ? "历史已载入，可以继续对话"
                    : "历史已载入；项目目录不可用，当前仅可查看";
            }
            finally
            {
                IsBusy = false;
                _activationGate.Release();
            }
        }
        catch (OperationCanceledException) when (
            _lifetime.IsCancellationRequested || cancellationToken.IsCancellationRequested)
        {
            // The page context changed or the workspace is closing.
        }
        catch (Exception exception)
        {
            if (IsCurrentDisplayContext(displayContextKey, conversation))
            {
                RuntimeStatus = $"历史记录读取失败：{exception.Message}";
                IsBusy = false;
            }
        }
    }

    partial void OnSelectedPermissionModeChanged(string value)
    {
        RuntimeStatus = value == "完全访问"
            ? "完全访问会允许官方 CLI 执行更高风险操作"
            : "准备就绪";
    }

    private void LaunchOptions_OnPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (e.PropertyName == nameof(TerminalViewModel.SelectedConnection))
        {
            RunOnUi(() => OnPropertyChanged(nameof(CurrentConnection)));
            return;
        }

        if (e.PropertyName is nameof(TerminalViewModel.SelectedProject) or
            nameof(TerminalViewModel.SelectedCli) or
            nameof(TerminalViewModel.PendingConversation))
        {
            RunOnUi(RefreshContext);
        }
    }

    [RelayCommand(CanExecute = nameof(CanSend))]
    private async Task SendAsync()
    {
        string message = DraftText.Trim();
        if (message.Length == 0 || _disposed)
        {
            return;
        }

        if (_pendingUserInputRequestId is { Length: > 0 } inputRequestId)
        {
            DraftText = string.Empty;
            AddMessage(ChatMessageKind.User, "你", message);
            _pendingUserInputRequestId = null;
            IsBusy = true;
            RuntimeStatus = "正在提交补充信息…";
            try
            {
                await _controller.RespondToUserInputAsync(inputRequestId, message, _lifetime.Token);
                if (IsBusy)
                {
                    RuntimeStatus = "已提交补充信息，正在继续…";
                }
            }
            catch (Exception exception) when (
                exception is InvalidOperationException or ObjectDisposedException or IOException)
            {
                AddMessage(ChatMessageKind.Error, "提交失败", exception.Message);
                RuntimeStatus = "补充信息提交失败";
                IsBusy = false;
            }
            return;
        }

        ChatLaunchIntent intent;
        try
        {
            intent = ChatLaunchIntent.Capture(LaunchOptions, ParsePermissionMode());
        }
        catch (Exception exception) when (
            exception is InvalidOperationException or ArgumentException or
                NotSupportedException or PathTooLongException or DirectoryNotFoundException)
        {
            AddMessage(ChatMessageKind.Error, "无法开始对话", exception.Message);
            return;
        }

        DraftText = string.Empty;
        _suppressHistoricalReplayDuplicates = false;
        _acceptControllerEvents = true;
        AddMessage(ChatMessageKind.User, "你", message);
        BeginLocalTurnTelemetry(intent);
        IsBusy = true;
        RuntimeStatus = "正在思考…";

        try
        {
            await _controller.SendAsync(intent, message, _lifetime.Token);
            if (_controller.State == ChatEngineState.Ready)
            {
                IsBusy = false;
            }
        }
        catch (OperationCanceledException) when (_lifetime.IsCancellationRequested)
        {
            CompleteLocalTurnTelemetry(
                succeeded: false,
                completedAt: DateTimeOffset.UtcNow);
            RuntimeStatus = "工作区正在关闭";
            IsBusy = false;
        }
        catch (OperationCanceledException)
        {
            CompleteLocalTurnTelemetry(
                succeeded: false,
                completedAt: DateTimeOffset.UtcNow);
            RuntimeStatus = "已停止本轮回答";
            IsBusy = false;
        }
        catch (Exception exception)
        {
            CompleteLocalTurnTelemetry(
                succeeded: false,
                completedAt: DateTimeOffset.UtcNow);
            AddMessage(ChatMessageKind.Error, "对话失败", exception.Message);
            RuntimeStatus = "对话发生错误";
            IsBusy = false;
        }
    }

    private bool CanSend() => !IsBusy && !string.IsNullOrWhiteSpace(DraftText);

    [RelayCommand]
    private void UseSuggestion(string? suggestion)
    {
        if (!string.IsNullOrWhiteSpace(suggestion))
        {
            DraftText = suggestion;
        }
    }

    [RelayCommand]
    private async Task StopAsync()
    {
        try
        {
            await _controller.CancelTurnAsync(_lifetime.Token);
            RuntimeStatus = "已请求停止";
        }
        catch (Exception exception) when (
            exception is InvalidOperationException or ObjectDisposedException or IOException)
        {
            AddMessage(ChatMessageKind.Error, "停止失败", exception.Message);
        }
    }

    [RelayCommand]
    private async Task NewConversationAsync()
    {
        RenewContextCancellation();
        await _controller.ResetAsync(_lifetime.Token);
        if (LaunchOptions.SelectedProject is { } project)
        {
            LaunchOptions.PrepareProject(project);
        }
        else
        {
            LaunchOptions.PendingConversation = null;
        }

        _displayContextKey = null;
        _loadedTranscriptContextKey = null;
        ClearDisplayedMessages();
        NativeSessionId = "尚未创建";
        RefreshContext();
        RuntimeStatus = "已准备新会话";
    }

    /// <summary>
    /// Starts a blank conversation after the user has explicitly chosen
    /// "新建会话" on the project-session page.
    /// </summary>
    internal Task StartNewConversationAsync() => NewConversationAsync();

    /// <summary>
    /// Opens a project-owned official session. The project-session page filters
    /// candidates first, and this boundary validates ownership again.
    /// </summary>
    internal async Task ResumeConversationAsync(
        ProjectCardViewModel project,
        ConversationRecord conversation)
    {
        ArgumentNullException.ThrowIfNull(project);
        ArgumentNullException.ThrowIfNull(conversation);
        ObjectDisposedException.ThrowIf(_disposed, this);
        if (!MatchesProject(project, conversation))
        {
            throw new InvalidOperationException("所选历史不属于当前项目。");
        }

        LaunchOptions.PrepareResume(project, conversation);
        RefreshContext();
        await ActivateAsync();
    }

    [RelayCommand]
    private Task OpenAdvancedTerminalAsync() => _openAdvancedTerminal();

    [RelayCommand]
    private Task AllowOnceAsync(ChatMessageViewModel? message) =>
        RespondToApprovalAsync(message, ChatApprovalDecision.AllowOnce);

    [RelayCommand]
    private Task DenyAsync(ChatMessageViewModel? message) =>
        RespondToApprovalAsync(message, ChatApprovalDecision.Deny);

    private async Task RespondToApprovalAsync(
        ChatMessageViewModel? message,
        ChatApprovalDecision decision)
    {
        if (message?.CorrelationId is not { Length: > 0 } requestId)
        {
            return;
        }

        await _controller.RespondToApprovalAsync(requestId, decision, _lifetime.Token);
        message.Status = decision == ChatApprovalDecision.Deny ? "已拒绝" : "本次已允许";
        message.IsCompleted = true;
    }

    private void Controller_OnEventReceived(object? sender, ChatEvent chatEvent)
    {
        RunOnUi(() =>
        {
            if (_acceptControllerEvents)
            {
                ApplyEvent(chatEvent);
            }
        });
    }

    private void ApplyEvent(ChatEvent chatEvent)
    {
        switch (chatEvent)
        {
            case ChatEngineStateEvent state:
                RuntimeStatus = state.Message;
                break;
            case ChatSessionStartedEvent session:
                NativeSessionId = session.NativeSessionId;
                RuntimeStatus = "官方会话已连接";
                RegisterManagedSessionBestEffort(
                    LaunchOptions.PendingConversation?.NativeClient ?? LaunchOptions.SelectedCliKind,
                    session.NativeSessionId);
                break;
            case ChatAssistantDeltaEvent delta:
                CaptureFirstToken(delta.Timestamp, streaming: true);
                _streamingAssistant ??= AddMessage(ChatMessageKind.Assistant, CurrentCli, string.Empty);
                _streamingAssistant.Text += delta.Text;
                break;
            case ChatAssistantMessageEvent message:
                CaptureFirstToken(message.Timestamp, streaming: false);
                if (_suppressHistoricalReplayDuplicates &&
                    _historicalMessageFingerprints.Contains(
                        CreateMessageFingerprint(ChatMessageKind.Assistant, message.Text)))
                {
                    if (_streamingAssistant is not null)
                    {
                        Messages.Remove(_streamingAssistant);
                        HasMessages = Messages.Count > 0;
                        _streamingAssistant = null;
                    }
                    break;
                }

                if (_streamingAssistant is null)
                {
                    AddMessage(ChatMessageKind.Assistant, CurrentCli, message.Text);
                }
                else if (string.IsNullOrWhiteSpace(_streamingAssistant.Text))
                {
                    _streamingAssistant.Text = message.Text;
                }
                break;
            case ChatToolStartedEvent tool:
                _toolMessages[tool.ToolCallId] = AddMessage(
                    ChatMessageKind.Tool,
                    tool.ToolName,
                    tool.Summary ?? "正在执行工具…",
                    tool.ToolCallId);
                break;
            case ChatToolProgressEvent progress when _toolMessages.TryGetValue(progress.ToolCallId, out ChatMessageViewModel? toolMessage):
                toolMessage.Status = progress.Message;
                break;
            case ChatToolCompletedEvent completed:
                if (!_toolMessages.TryGetValue(completed.ToolCallId, out ChatMessageViewModel? completedMessage))
                {
                    completedMessage = AddMessage(
                        ChatMessageKind.Tool,
                        completed.ToolName,
                        completed.Summary ?? string.Empty,
                        completed.ToolCallId);
                }

                completedMessage.Status = completed.Succeeded ? "已完成" : "执行失败";
                completedMessage.IsCompleted = true;
                break;
            case ChatApprovalRequestedEvent approval:
                ChatMessageViewModel approvalMessage = AddMessage(
                    ChatMessageKind.Approval,
                    approval.Title,
                    approval.Detail,
                    approval.RequestId);
                approvalMessage.Status = "等待你的决定";
                RuntimeStatus = "等待权限确认";
                break;
            case ChatUserInputRequestedEvent input:
                _pendingUserInputRequestId = input.RequestId;
                IsBusy = false;
                AddMessage(
                    ChatMessageKind.System,
                    "需要你的补充",
                    input.Options.Count == 0
                        ? input.Prompt
                        : $"{input.Prompt}\n可选：{string.Join(" / ", input.Options)}",
                    input.RequestId);
                RuntimeStatus = "等待你的输入";
                break;
            case ChatUsageEvent usage:
                CaptureLocalTurnUsage(usage);
                RuntimeStatus = usage.InputTokens is null && usage.OutputTokens is null
                    ? "本轮已完成"
                    : $"本轮 Token · 输入 {usage.InputTokens ?? 0:N0} / 输出 {usage.OutputTokens ?? 0:N0}";
                break;
            case ChatTurnCompletedEvent completed:
                CompleteLocalTurnTelemetry(completed.Succeeded, completed.Timestamp);
                _streamingAssistant = null;
                IsBusy = false;
                RuntimeStatus = completed.Succeeded ? "回答完成" : completed.ErrorMessage ?? "回答失败";
                break;
            case ChatErrorEvent error:
                AddMessage(ChatMessageKind.Error, "运行错误", error.Message);
                RuntimeStatus = error.Message;
                break;
        }
    }

    private ChatMessageViewModel AddMessage(
        ChatMessageKind kind,
        string title,
        string text,
        string? correlationId = null,
        DateTimeOffset? createdAt = null)
    {
        var message = new ChatMessageViewModel(kind, title, text, correlationId, createdAt);
        Messages.Add(message);
        HasMessages = Messages.Count > 0;
        return message;
    }

    private async Task LoadTranscriptAsync(
        ConversationRecord conversation,
        string displayContextKey,
        CancellationToken cancellationToken)
    {
        RuntimeStatus = "正在读取官方历史记录…";
        ConversationTranscript transcript = await _transcriptReader
            .ReadAsync(conversation, cancellationToken);
        if (!IsCurrentDisplayContext(displayContextKey, conversation))
        {
            return;
        }

        foreach (ConversationTranscriptMessage entry in transcript.Messages)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (string.IsNullOrWhiteSpace(entry.Text) ||
                (!string.IsNullOrWhiteSpace(entry.Id) &&
                 !_loadedTranscriptMessageIds.Add(entry.Id)))
            {
                continue;
            }

            ChatMessageKind kind = entry.Role switch
            {
                ConversationTranscriptRole.User => ChatMessageKind.User,
                ConversationTranscriptRole.Assistant => ChatMessageKind.Assistant,
                ConversationTranscriptRole.Tool => ChatMessageKind.Tool,
                _ => ChatMessageKind.System,
            };
            string title = !string.IsNullOrWhiteSpace(entry.Title)
                ? entry.Title
                : entry.Role switch
                {
                    ConversationTranscriptRole.User => "你",
                    ConversationTranscriptRole.Assistant => CurrentCli,
                    ConversationTranscriptRole.Tool => "工具",
                    _ => "系统",
                };
            ChatMessageViewModel message = AddMessage(
                kind,
                title,
                entry.Text,
                entry.Id,
                entry.Timestamp);
            if (kind == ChatMessageKind.Tool)
            {
                message.Status = "历史工具记录";
                message.IsCompleted = true;
            }

            _historicalMessageFingerprints.Add(CreateMessageFingerprint(kind, entry.Text));
        }

        if (transcript.Warnings.Count > 0)
        {
            AddMessage(
                ChatMessageKind.System,
                "历史读取提示",
                string.Join(Environment.NewLine, transcript.Warnings));
        }
        else if (!transcript.SourceFound && transcript.Messages.Count == 0)
        {
            AddMessage(
                ChatMessageKind.System,
                "未找到历史正文",
                "官方会话索引仍然存在，但对应的本地历史正文当前不可用。仍会尝试恢复官方会话。");
        }

        _loadedTranscriptContextKey = displayContextKey;
        _suppressHistoricalReplayDuplicates = true;
        RuntimeStatus = transcript.SourceFound
            ? $"已载入 {transcript.Messages.Count} 条历史消息"
            : "官方历史正文当前不可用；发送消息时仍可尝试恢复会话。";
    }

    private void ClearDisplayedMessages()
    {
        Messages.Clear();
        _toolMessages.Clear();
        _loadedTranscriptMessageIds.Clear();
        _historicalMessageFingerprints.Clear();
        _streamingAssistant = null;
        _pendingUserInputRequestId = null;
        _suppressHistoricalReplayDuplicates = false;
        HasMessages = false;
    }

    private string BuildDisplayContextKey()
    {
        ProjectRecord? project = LaunchOptions.SelectedProjectRecord;
        ConversationRecord? conversation = LaunchOptions.PendingConversation;
        return string.Join(
            "\u001f",
            project?.PathFingerprint ?? project?.Id ?? "no-project",
            LaunchOptions.SelectedCli,
            conversation?.NativeClient.ToString() ?? "new",
            conversation?.NativeSessionId ?? "new");
    }

    private bool IsCurrentDisplayContext(
        string displayContextKey,
        ConversationRecord conversation) =>
        string.Equals(_displayContextKey, displayContextKey, StringComparison.Ordinal) &&
        LaunchOptions.PendingConversation is { } current &&
        current.NativeClient == conversation.NativeClient &&
        string.Equals(
            current.NativeSessionId,
            conversation.NativeSessionId,
            StringComparison.Ordinal);

    private bool IsControllerConnectedTo(ConversationRecord conversation) =>
        (_controller.State is ChatEngineState.Ready or ChatEngineState.RunningTurn or ChatEngineState.WaitingForApproval) &&
        string.Equals(
            _controller.ActiveProjectFingerprint,
            LaunchOptions.SelectedProject?.PathFingerprint,
            StringComparison.OrdinalIgnoreCase) &&
        string.Equals(
            _controller.NativeSessionId,
            conversation.NativeSessionId,
            StringComparison.Ordinal);

    private bool IsSelectedProjectDirectoryAvailable()
    {
        string? path = LaunchOptions.SelectedProjectRecord?.RootPath;
        return !string.IsNullOrWhiteSpace(path) && Directory.Exists(path);
    }

    private static string CreateMessageFingerprint(ChatMessageKind kind, string text) =>
        $"{kind}:{text.Replace("\r\n", "\n", StringComparison.Ordinal).Trim()}";

    private void BeginLocalTurnTelemetry(ChatLaunchIntent intent)
    {
        RegisterManagedSessionBestEffort(
            intent.Cli,
            intent.Conversation?.NativeSessionId ?? _controller.NativeSessionId);

        if (_localTelemetryRepository is null)
        {
            return;
        }

        _activeTurnTelemetry = new LocalTurnTelemetry(
            DateTimeOffset.UtcNow,
            intent.Cli,
            intent.ConnectionProfileId,
            intent.ConnectionLabel,
            intent.Model);
    }

    private void RegisterManagedSessionBestEffort(CliKind cliKind, string? nativeSessionId)
    {
        if (_managedCliSessionRegistry is null ||
            cliKind is not (CliKind.Codex or CliKind.ClaudeCode) ||
            string.IsNullOrWhiteSpace(nativeSessionId))
        {
            return;
        }

        _ = RegisterManagedSessionCoreAsync(cliKind, nativeSessionId);
    }

    private async Task RegisterManagedSessionCoreAsync(CliKind cliKind, string nativeSessionId)
    {
        try
        {
            await _managedCliSessionRegistry!
                .RegisterManagedSessionAsync(cliKind, nativeSessionId, _lifetime.Token)
                .ConfigureAwait(false);
        }
        catch (OperationCanceledException) when (_lifetime.IsCancellationRequested)
        {
            // Workspace shutdown deliberately cancels this non-blocking
            // de-duplication marker.
        }
        catch (ObjectDisposedException)
        {
            // The optional importer may be disposed during application shutdown.
        }
        catch
        {
            // The marker must never delay or fail a user chat turn. The importer
            // remains conservative on the next successful registration.
        }
    }

    private void CaptureLocalTurnUsage(ChatUsageEvent usage)
    {
        LocalTurnTelemetry? current = _activeTurnTelemetry;
        if (current is null)
        {
            return;
        }

        current.InputTokens = usage.InputTokens ?? current.InputTokens;
        current.OutputTokens = usage.OutputTokens ?? current.OutputTokens;
        current.CachedInputTokens = usage.CachedInputTokens ?? current.CachedInputTokens;
        current.CacheCreationTokens = usage.CacheCreationTokens ?? current.CacheCreationTokens;
    }

    private void CaptureFirstToken(DateTimeOffset timestamp, bool streaming)
    {
        LocalTurnTelemetry? current = _activeTurnTelemetry;
        if (current is null)
        {
            return;
        }

        current.FirstTokenAt ??= timestamp;
        current.IsStreaming |= streaming;
    }

    private void CompleteLocalTurnTelemetry(bool succeeded, DateTimeOffset completedAt)
    {
        LocalTurnTelemetry? completed = _activeTurnTelemetry;
        _activeTurnTelemetry = null;
        if (completed is null || _localTelemetryRepository is null)
        {
            return;
        }

        TimeSpan elapsed = completedAt - completed.StartedAt;
        int elapsedMilliseconds = (int)Math.Min(
            int.MaxValue,
            Math.Max(0d, elapsed.TotalMilliseconds));
        int? firstTokenMilliseconds = completed.FirstTokenAt is { } firstTokenAt
            ? (int)Math.Min(int.MaxValue, Math.Max(0d, (firstTokenAt - completed.StartedAt).TotalMilliseconds))
            : null;

        LocalUsageTelemetryEvent telemetryEvent;
        try
        {
            telemetryEvent = new LocalUsageTelemetryEvent(
                completedAt,
                completed.Cli,
                completed.SourceId,
                completed.SourceLabel,
                completed.Model,
                completed.InputTokens,
                completed.OutputTokens,
                completed.CachedInputTokens,
                succeeded,
                elapsedMilliseconds,
                cacheCreationTokens: completed.CacheCreationTokens,
                estimatedCost: null,
                firstTokenMilliseconds: firstTokenMilliseconds,
                statusCategory: succeeded ? "success" : "client-error",
                isStreaming: completed.IsStreaming,
                pricingModel: null);
        }
        catch (ArgumentException)
        {
            // A user-controlled connection name or model name may accidentally
            // contain an endpoint or credential-like text.  Keep the aggregate
            // request record, but never persist that metadata.
            telemetryEvent = new LocalUsageTelemetryEvent(
                completedAt,
                completed.Cli,
                sourceId: null,
                sourceLabel: null,
                model: null,
                inputTokens: completed.InputTokens,
                outputTokens: completed.OutputTokens,
                cachedInputTokens: completed.CachedInputTokens,
                succeeded: succeeded,
                elapsedMilliseconds: elapsedMilliseconds,
                cacheCreationTokens: completed.CacheCreationTokens,
                estimatedCost: null,
                firstTokenMilliseconds: firstTokenMilliseconds,
                statusCategory: succeeded ? "success" : "client-error",
                isStreaming: completed.IsStreaming,
                pricingModel: null);
        }

        _ = RecordLocalTelemetrySafelyAsync(telemetryEvent);
    }

    private async Task RecordLocalTelemetrySafelyAsync(LocalUsageTelemetryEvent telemetryEvent)
    {
        try
        {
            await _localTelemetryRepository!
                .RecordUsageAsync(telemetryEvent, CancellationToken.None)
                .ConfigureAwait(false);
        }
        catch (Exception)
        {
            // Local observability is best-effort.  It must not disrupt a chat
            // turn or expose an implementation error in the conversation.
        }
    }

    private static bool MatchesProject(
        ProjectCardViewModel project,
        ConversationRecord conversation) =>
        string.Equals(project.Id, conversation.ProjectId, StringComparison.OrdinalIgnoreCase) ||
        string.Equals(
            project.PathFingerprint,
            conversation.ProjectId,
            StringComparison.OrdinalIgnoreCase) ||
        PathsEqual(project.Path, conversation.OriginalWorkingDirectory);

    private static bool PathsEqual(string left, string right)
    {
        try
        {
            return string.Equals(
                PathIdentity.Normalize(left),
                PathIdentity.Normalize(right),
                OperatingSystem.IsWindows()
                    ? StringComparison.OrdinalIgnoreCase
                    : StringComparison.Ordinal);
        }
        catch (Exception exception) when (
            exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            return false;
        }
    }

    private void RenewContextCancellation()
    {
        CancellationTokenSource previous = _contextCancellation;
        _contextCancellation = new CancellationTokenSource();
        previous.Cancel();
        previous.Dispose();
    }

    private ChatPermissionMode ParsePermissionMode() => SelectedPermissionMode switch
    {
        "只读分析" => ChatPermissionMode.ReadOnly,
        "完全访问" => ChatPermissionMode.FullAccess,
        _ => ChatPermissionMode.WorkspaceWrite,
    };

    private static void RunOnUi(Action action)
    {
        if (Application.Current?.Dispatcher is not { } dispatcher || dispatcher.CheckAccess())
        {
            action();
            return;
        }

        if (!dispatcher.HasShutdownStarted)
        {
            _ = dispatcher.BeginInvoke(action);
        }
    }

    public async ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _controller.EventReceived -= Controller_OnEventReceived;
        LaunchOptions.PropertyChanged -= LaunchOptions_OnPropertyChanged;
        _lifetime.Cancel();
        _contextCancellation.Cancel();
        await _controller.DisposeAsync();
        _contextCancellation.Dispose();
        _lifetime.Dispose();
    }

    private sealed class LocalTurnTelemetry
    {
        public LocalTurnTelemetry(
            DateTimeOffset startedAt,
            CliKind cli,
            string? sourceId,
            string? sourceLabel,
            string? model)
        {
            StartedAt = startedAt;
            Cli = cli;
            SourceId = sourceId;
            SourceLabel = sourceLabel;
            Model = model;
        }

        public DateTimeOffset StartedAt { get; }

        public CliKind Cli { get; }

        public string? SourceId { get; }

        public string? SourceLabel { get; }

        public string? Model { get; }

        public long InputTokens { get; set; }

        public long OutputTokens { get; set; }

        public long CachedInputTokens { get; set; }

        public long CacheCreationTokens { get; set; }

        public DateTimeOffset? FirstTokenAt { get; set; }

        public bool IsStreaming { get; set; }
    }

    private sealed class EmptyConversationTranscriptReader : IConversationTranscriptReader
    {
        public static EmptyConversationTranscriptReader Instance { get; } = new();

        public Task<ConversationTranscript> ReadAsync(
            ConversationRecord conversation,
            CancellationToken cancellationToken = default) =>
            Task.FromResult(new ConversationTranscript(
                SourceFound: false,
                Messages: Array.Empty<ConversationTranscriptMessage>(),
                Warnings: Array.Empty<string>()));
    }
}
