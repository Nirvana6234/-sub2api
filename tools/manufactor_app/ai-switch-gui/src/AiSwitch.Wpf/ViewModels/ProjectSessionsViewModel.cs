using System.Collections.ObjectModel;
using System.IO;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Wpf.ViewModels;

/// <summary>
/// The second level of the project hierarchy:
/// 项目中心 → 项目会话 → AI 对话。
/// It is intentionally a separate page so the chat surface is never used as
/// a session-picker and cannot accidentally start a new CLI session.
/// </summary>
public partial class ProjectSessionsViewModel : PageViewModel
{
    private readonly Func<ProjectCardViewModel, Task> _startNewConversation;
    private readonly Func<ProjectCardViewModel, HistorySessionViewModel, Task> _continueConversation;
    private readonly Action _returnToProjects;
    private readonly Func<ProjectCardViewModel, Task<string>>? _captureProfile;
    private readonly Func<ProjectCardViewModel, Task<string>>? _applyProfile;
    private ProjectCardViewModel? _project;

    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(StartNewConversationCommand))]
    [NotifyCanExecuteChangedFor(nameof(ContinueConversationCommand))]
    [NotifyCanExecuteChangedFor(nameof(CaptureProjectProfileCommand))]
    [NotifyCanExecuteChangedFor(nameof(ApplyProjectProfileCommand))]
    private bool isBusy;

    [ObservableProperty]
    private bool hasSessions;

    [ObservableProperty]
    private string statusNotice = "请先从项目中心选择一个项目。";

    public ProjectSessionsViewModel(
        Func<ProjectCardViewModel, Task> startNewConversation,
        Func<ProjectCardViewModel, HistorySessionViewModel, Task> continueConversation,
        Action returnToProjects,
        Func<ProjectCardViewModel, Task<string>>? captureProfile = null,
        Func<ProjectCardViewModel, Task<string>>? applyProfile = null)
        : base("项目会话", "先选择已有历史或创建新会话，然后才进入 AI 对话。")
    {
        _startNewConversation = startNewConversation ?? throw new ArgumentNullException(nameof(startNewConversation));
        _continueConversation = continueConversation ?? throw new ArgumentNullException(nameof(continueConversation));
        _returnToProjects = returnToProjects ?? throw new ArgumentNullException(nameof(returnToProjects));
        _captureProfile = captureProfile;
        _applyProfile = applyProfile;
        Sessions = new ObservableCollection<HistorySessionViewModel>();
    }

    public ObservableCollection<HistorySessionViewModel> Sessions { get; }

    internal ProjectCardViewModel? CurrentProject => _project;

    public string ProjectName => _project?.Name ?? "尚未选择项目";

    public string ProjectPath => _project?.Path ?? "请返回项目中心选择项目";

    public string ProjectStatus => _project?.Status ?? "等待项目选择";

    public bool IsProjectPathAvailable => _project?.PathAvailable == true;

    internal void OpenProject(
        ProjectCardViewModel project,
        IReadOnlyList<HistorySessionViewModel> sessions)
    {
        ArgumentNullException.ThrowIfNull(project);
        ArgumentNullException.ThrowIfNull(sessions);

        _project = project;
        OnPropertyChanged(nameof(ProjectName));
        OnPropertyChanged(nameof(ProjectPath));
        OnPropertyChanged(nameof(ProjectStatus));
        OnPropertyChanged(nameof(IsProjectPathAvailable));
        ReplaceSessions(project, sessions);
        StatusNotice = project.PathAvailable
            ? HasSessions
                ? $"已找到 {Sessions.Count} 条属于“{project.Name}”的官方会话。"
                : $"“{project.Name}”暂无历史会话，可以从空白上下文开始。"
            : $"项目目录当前不可用；仍可查看历史，但无法继续或创建会话。";
        StartNewConversationCommand.NotifyCanExecuteChanged();
        ContinueConversationCommand.NotifyCanExecuteChanged();
        CaptureProjectProfileCommand.NotifyCanExecuteChanged();
        ApplyProjectProfileCommand.NotifyCanExecuteChanged();
    }

    /// <summary>
    /// Refreshes an already-open project page from the global, unfiltered
    /// history index. The second exact-identity filter keeps a nested project
    /// directory from being treated as its parent project.
    /// </summary>
    internal void RefreshSessions(IReadOnlyList<HistorySessionViewModel> sessions)
    {
        ArgumentNullException.ThrowIfNull(sessions);
        if (_project is null)
        {
            return;
        }

        ReplaceSessions(_project, sessions);
        if (!IsBusy)
        {
            StatusNotice = _project.PathAvailable
                ? HasSessions
                    ? $"已刷新：本项目共有 {Sessions.Count} 条历史会话。"
                    : "本项目暂无历史会话。"
                : "项目目录当前不可用；仍可查看已索引历史。";
        }
    }

    [RelayCommand]
    private void BackToProjects() => _returnToProjects();

    [RelayCommand(CanExecute = nameof(CanOpenConversation))]
    private async Task StartNewConversationAsync()
    {
        if (_project is null)
        {
            return;
        }

        await RunNavigationAsync(
            () => _startNewConversation(_project),
            "正在创建新的项目会话…",
            "无法创建新会话");
    }

    [RelayCommand(CanExecute = nameof(CanContinueConversation))]
    private async Task ContinueConversationAsync(HistorySessionViewModel? session)
    {
        if (_project is null || session is null)
        {
            return;
        }

        if (!MatchesProject(_project, session.Record))
        {
            StatusNotice = "这条历史不属于当前项目，已拒绝打开。";
            return;
        }

        await RunNavigationAsync(
            () => _continueConversation(_project, session),
            "正在进入命令行并恢复官方会话…",
            "无法继续这条会话");
    }

    private bool CanOpenConversation() => !IsBusy && _project?.PathAvailable == true;

    private bool CanContinueConversation(HistorySessionViewModel? session) =>
        CanOpenConversation() && session is not null;

    [RelayCommand(CanExecute = nameof(CanManageProjectProfile))]
    private async Task CaptureProjectProfileAsync()
    {
        if (_project is null || _captureProfile is null) return;
        await RunProfileOperationAsync(() => _captureProfile(_project), "正在保存项目工作配置…", "保存项目配置失败");
    }

    [RelayCommand(CanExecute = nameof(CanManageProjectProfile))]
    private async Task ApplyProjectProfileAsync()
    {
        if (_project is null || _applyProfile is null) return;
        await RunProfileOperationAsync(() => _applyProfile(_project), "正在应用项目工作配置…", "应用项目配置失败");
    }

    private bool CanManageProjectProfile() => !IsBusy && _project is not null && _captureProfile is not null && _applyProfile is not null;

    private async Task RunProfileOperationAsync(Func<Task<string>> operation, string progress, string failurePrefix)
    {
        IsBusy = true;
        StatusNotice = progress;
        try
        {
            StatusNotice = await operation().ConfigureAwait(true);
        }
        catch (Exception exception) when (exception is IOException or InvalidDataException or InvalidOperationException or KeyNotFoundException)
        {
            StatusNotice = $"{failurePrefix}：{exception.Message}";
        }
        finally
        {
            IsBusy = false;
            CaptureProjectProfileCommand.NotifyCanExecuteChanged();
            ApplyProjectProfileCommand.NotifyCanExecuteChanged();
        }
    }

    private async Task RunNavigationAsync(
        Func<Task> navigation,
        string progress,
        string failurePrefix)
    {
        IsBusy = true;
        StatusNotice = progress;
        try
        {
            await navigation();
        }
        catch (OperationCanceledException)
        {
            StatusNotice = "打开会话已取消。";
        }
        catch (Exception exception) when (
            exception is InvalidOperationException or ArgumentException or
            NotSupportedException or PathTooLongException or DirectoryNotFoundException or IOException)
        {
            StatusNotice = $"{failurePrefix}：{exception.Message}";
        }
        finally
        {
            IsBusy = false;
        }
    }

    private void ReplaceSessions(
        ProjectCardViewModel project,
        IReadOnlyList<HistorySessionViewModel> sessions)
    {
        Sessions.Clear();
        foreach (HistorySessionViewModel session in sessions
                     .Where(session => MatchesProject(project, session.Record))
                     .OrderByDescending(session => session.Record.UpdatedAt))
        {
            Sessions.Add(session);
        }

        HasSessions = Sessions.Count > 0;
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
}
