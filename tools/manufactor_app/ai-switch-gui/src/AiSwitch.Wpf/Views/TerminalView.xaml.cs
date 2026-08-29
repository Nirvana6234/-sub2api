using System.IO;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Terminal;
using LanAi.Workspace.Wpf.Controls;
using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Views;

public partial class TerminalView : UserControl
{
    private static readonly Brush IdleDotBrush = CreateBrush(0x8E, 0x8E, 0x93);
    private static readonly Brush IdleBadgeBrush = CreateBrush(0xF1, 0xF1, 0xF4);
    private static readonly Brush StartingDotBrush = CreateBrush(0xFF, 0x9F, 0x0A);
    private static readonly Brush StartingBadgeBrush = CreateBrush(0xFF, 0xF4, 0xDF);
    private static readonly Brush RunningDotBrush = CreateBrush(0x34, 0xC7, 0x59);
    private static readonly Brush RunningBadgeBrush = CreateBrush(0xE9, 0xF8, 0xED);
    private static readonly Brush ErrorDotBrush = CreateBrush(0xFF, 0x45, 0x3A);
    private static readonly Brush ErrorBadgeBrush = CreateBrush(0xFF, 0xEB, 0xEA);
    private static readonly Brush NoticeBrush = CreateBrush(0xB9, 0xB9, 0xBF);
    private static readonly object ActiveLaunchGate = new();
    private static CancellationTokenSource? _activeLaunchCancellation;
    private static Task? _activeLaunchTask;

    private readonly CliDetector _cliDetector = new();
    private CancellationTokenSource? _launchCancellation;
    private Window? _ownerWindow;
    private bool _hostEventsAttached;
    private bool _resolvingLaunch;
    private bool _returning;
    private string? _activeLaunchSummary;

    public TerminalView()
    {
        InitializeComponent();
    }

    private async void TerminalView_OnLoaded(object sender, RoutedEventArgs e)
    {
        Window? owner = Window.GetWindow(this);
        if (!ReferenceEquals(_ownerWindow, owner))
        {
            if (_ownerWindow is not null)
            {
                _ownerWindow.Closed -= OwnerWindow_OnClosed;
            }

            _ownerWindow = owner;
            if (_ownerWindow is not null)
            {
                _ownerWindow.Closed += OwnerWindow_OnClosed;
            }
        }

        AttachHostEvents();
        ApplyHostState(TerminalSurface.Host.State, TerminalSurface.Host.StatusMessage);
        UpdateTerminalMetadata();

        if (DataContext is TerminalViewModel viewModel && viewModel.ConsumeAutoStartRequest())
        {
            await StartTerminalAsync();
        }
    }

    private void TerminalView_OnUnloaded(object sender, RoutedEventArgs e)
    {
        DetachHostEvents();
        // The shared host intentionally remains alive. Returning to this page
        // reconnects to the same official CLI session and its current frame.
    }

    private void OwnerWindow_OnClosed(object? sender, EventArgs e)
    {
        if (_ownerWindow is not null)
        {
            _ownerWindow.Closed -= OwnerWindow_OnClosed;
            _ownerWindow = null;
        }

    }

    private async void StartTerminal_OnClick(object sender, RoutedEventArgs e)
        => await StartTerminalAsync();

    private async Task StartTerminalAsync()
    {
        if (_resolvingLaunch)
        {
            return;
        }

        TerminalViewModel launchOptions;
        TerminalLaunchIntent intent;
        try
        {
            launchOptions = DataContext as TerminalViewModel
                ?? throw new InvalidOperationException("终端页面尚未完成初始化。 ");
            intent = TerminalLaunchIntent.Capture(launchOptions);
        }
        catch (Exception exception) when (
            exception is InvalidOperationException or
                ArgumentException or
                NotSupportedException or
                PathTooLongException or
                DirectoryNotFoundException)
        {
            SetNotice(exception.Message, isError: true);
            return;
        }

        Task? previousLaunch = CancelActiveLaunch();
        if (previousLaunch is not null)
        {
            _ = ObserveLaunchCompletionAsync(previousLaunch);
        }
        _launchCancellation?.Dispose();
        var cancellation = new CancellationTokenSource();
        _launchCancellation = cancellation;
        _resolvingLaunch = true;
        UpdateControlAvailability();
        SetBadge(TerminalHostState.Starting, "正在检测 CLI");
        SetNotice($"正在检测 {GetCliDisplayName(intent.Cli)} 并准备项目环境……");

        Task launchTask = LaunchTerminalAsync(intent, launchOptions, cancellation.Token);
        RegisterActiveLaunch(cancellation, launchTask);

        try
        {
            await launchTask;
            TerminalSurface.Focus();
        }
        catch (OperationCanceledException) when (cancellation.IsCancellationRequested)
        {
            SetNotice("终端启动已取消。");
        }
        catch (Exception exception)
        {
            SetNotice($"启动失败：{exception.Message}", isError: true);
            SetBadge(TerminalHostState.Faulted, "启动失败");
        }
        finally
        {
            ClearActiveLaunch(cancellation, launchTask);
            if (ReferenceEquals(_launchCancellation, cancellation))
            {
                _launchCancellation = null;
            }

            cancellation.Dispose();
            _resolvingLaunch = false;
            UpdateControlAvailability();
        }
    }

    private async Task LaunchTerminalAsync(
        TerminalLaunchIntent intent,
        TerminalViewModel launchOptions,
        CancellationToken cancellationToken)
    {
        IConnectionProfileReader profileReader = launchOptions.ConnectionProfileReader
            ?? throw new InvalidOperationException("终端连接服务尚未初始化。请先完成工作台加载。 ");
        IConnectionCredentialProvider credentialProvider = launchOptions.CredentialProvider
            ?? throw new InvalidOperationException("终端凭据服务尚未初始化。请先完成工作台加载。 ");
        IReadOnlyList<CliInstallation> detected = await _cliDetector
            .DetectAsync(intent.Cli, cancellationToken);
        CliInstallation? installation = detected.FirstOrDefault();
        if (installation?.IsInstalled != true || string.IsNullOrWhiteSpace(installation.ExecutablePath))
        {
            throw new InvalidOperationException(
                $"未检测到 {GetCliDisplayName(intent.Cli)}。请先安装官方 CLI，并确认命令已加入 PATH。");
        }

        IReadOnlyList<ConnectionProfile> profiles = await profileReader
            .GetAllAsync(cancellationToken);
        ConnectionProfile? connection = ResolveConnection(
            profiles,
            intent.ConnectionProfileId,
            intent.ConnectionLabel,
            intent.Cli);
        if (!string.IsNullOrWhiteSpace(intent.ConnectionProfileId) && connection is null)
        {
            throw new InvalidOperationException(
                $"连接“{intent.ConnectionLabel}”尚未配置 {GetCliDisplayName(intent.Cli)}。请前往连接中心选择或完善该来源。");
        }

        CliLaunchRequest request = intent.CreateRequest(connection);
        var commandFactory = new CliTerminalCommandFactory(credentialProvider);
        TerminalCommand command = await commandFactory.CreateAsync(
            request,
            installation,
            connection,
            cancellationToken);

        string connectionLabel = connection?.Name ?? "沿用官方 CLI 当前配置";
        string versionLabel = string.IsNullOrWhiteSpace(installation.Version)
            ? GetCliDisplayName(intent.Cli)
            : installation.Version!;
        _activeLaunchSummary = intent.Conversation is null
            ? $"{versionLabel} · {connectionLabel} · {intent.WorkingDirectory}"
            : $"恢复“{intent.Conversation.Title ?? intent.Conversation.NativeSessionId}” · {versionLabel} · {connectionLabel}";

        await TerminalSurface.StartAsync(command, cancellationToken);
    }

    private async void StopTerminal_OnClick(object sender, RoutedEventArgs e)
    {
        await StopTerminalAsync();
    }

    private async void Return_OnClick(object sender, RoutedEventArgs e)
    {
        if (_returning)
        {
            return;
        }

        _returning = true;
        UpdateControlAvailability();
        try
        {
            if (!await StopTerminalAsync())
            {
                return;
            }

            if (DataContext is not TerminalViewModel viewModel)
            {
                SetNotice("返回失败：终端页面尚未完成初始化。", isError: true);
                return;
            }

            viewModel.ReturnToPreviousPage();
        }
        finally
        {
            _returning = false;
            UpdateControlAvailability();
        }
    }

    private async Task<bool> StopTerminalAsync()
    {
        Task? pendingLaunch = CancelActiveLaunch();
        SetNotice("正在停止终端进程……");

        try
        {
            if (pendingLaunch is not null)
            {
                await ObserveLaunchCompletionAsync(pendingLaunch);
            }

            await TerminalSurface.StopAsync();
            return true;
        }
        catch (Exception exception) when (
            exception is IOException or
                InvalidOperationException or
                ObjectDisposedException or
                OperationCanceledException)
        {
            SetNotice($"停止终端失败：{exception.Message}", isError: true);
            return false;
        }
    }

    private void TerminalSurface_OnDimensionsChanged(
        object? sender,
        TerminalDimensionsChangedEventArgs e)
    {
        TerminalSizeText.Text = $"{e.Columns} × {e.Rows}";
    }

    private void TerminalSurface_OnInputFailed(object? sender, TerminalInputFailedEventArgs e)
        => SetNotice($"终端输入失败：{e.Exception.Message}", isError: true);

    private void AttachHostEvents()
    {
        if (_hostEventsAttached)
        {
            return;
        }

        TerminalSurface.Host.FrameChanged += Host_OnFrameChanged;
        TerminalSurface.Host.StateChanged += Host_OnStateChanged;
        _hostEventsAttached = true;
    }

    private void DetachHostEvents()
    {
        if (!_hostEventsAttached)
        {
            return;
        }

        TerminalSurface.Host.FrameChanged -= Host_OnFrameChanged;
        TerminalSurface.Host.StateChanged -= Host_OnStateChanged;
        _hostEventsAttached = false;
    }

    private void Host_OnFrameChanged(object? sender, EventArgs e)
        => RunOnUi(UpdateTerminalMetadata);

    private void Host_OnStateChanged(object? sender, TerminalHostStateChangedEventArgs e)
        => RunOnUi(() => ApplyHostState(e.State, e.Message));

    private void ApplyHostState(TerminalHostState state, string message)
    {
        SetBadge(state, message);
        UpdateTerminalMetadata();
        UpdateControlAvailability();

        switch (state)
        {
            case TerminalHostState.Running:
                SetNotice(_activeLaunchSummary is null
                    ? "官方 CLI 已在内嵌终端中运行。"
                    : $"正在运行：{_activeLaunchSummary}");
                TerminalSurface.Focus();
                break;
            case TerminalHostState.Stopping:
                SetNotice("正在停止终端进程……");
                break;
            case TerminalHostState.Exited:
                SetNotice("终端进程已退出；输出仍保留在当前页面，可重新启动。 ");
                break;
            case TerminalHostState.Faulted:
                SetNotice($"终端错误：{message}", isError: true);
                break;
        }
    }

    private void UpdateTerminalMetadata()
    {
        TerminalFrame? frame = TerminalSurface.Host.CurrentFrame;
        string? title = frame?.Title;
        if (string.IsNullOrWhiteSpace(title))
        {
            title = TerminalSurface.Host.ActiveMetadata?.DisplayName;
        }

        TerminalTitleText.Text = string.IsNullOrWhiteSpace(title)
            ? "共飞AI工作台"
            : SanitizeTerminalTitle(title);
        TerminalSizeText.Text = $"{TerminalSurface.Columns} × {TerminalSurface.Rows}";
    }

    private void UpdateControlAvailability()
    {
        TerminalHostState state = TerminalSurface.Host.State;
        bool terminalBusy = state is TerminalHostState.Starting or TerminalHostState.Running or TerminalHostState.Stopping;
        bool selectionsEnabled = !_resolvingLaunch && !terminalBusy;

        ProjectComboBox.IsEnabled = selectionsEnabled;
        CliComboBox.IsEnabled = selectionsEnabled;
        StartButton.IsEnabled = selectionsEnabled;
        StopButton.IsEnabled = _resolvingLaunch || state is TerminalHostState.Starting or TerminalHostState.Running;
        BackButton.IsEnabled = !_returning;
    }

    private void SetBadge(TerminalHostState state, string message)
    {
        (Brush dot, Brush badge, string label) = state switch
        {
            TerminalHostState.Starting => (StartingDotBrush, StartingBadgeBrush, "正在启动"),
            TerminalHostState.Running => (RunningDotBrush, RunningBadgeBrush, "终端运行中"),
            TerminalHostState.Stopping => (StartingDotBrush, StartingBadgeBrush, "正在停止"),
            TerminalHostState.Exited => (IdleDotBrush, IdleBadgeBrush, "进程已退出"),
            TerminalHostState.Faulted => (ErrorDotBrush, ErrorBadgeBrush, "终端错误"),
            _ => (IdleDotBrush, IdleBadgeBrush, "等待启动"),
        };

        RuntimeDot.Fill = dot;
        RuntimeBadge.Background = badge;
        RuntimeStatusText.Text = label;
        RuntimeBadge.ToolTip = message;
    }

    private void SetNotice(string message, bool isError = false)
    {
        TerminalNoticeText.Text = message;
        TerminalNoticeText.Foreground = isError
            ? ErrorDotBrush
            : NoticeBrush;
    }

    internal static ConnectionProfile? ResolveConnection(
        IReadOnlyList<ConnectionProfile> profiles,
        string? selectedId,
        string? selectedName,
        CliKind cli)
    {
        if (string.IsNullOrWhiteSpace(selectedId) && string.IsNullOrWhiteSpace(selectedName))
        {
            return null;
        }

        ConnectionProfile? profile;
        if (!string.IsNullOrWhiteSpace(selectedId))
        {
            profile = profiles.FirstOrDefault(item =>
                string.Equals(item.Id, selectedId, StringComparison.OrdinalIgnoreCase));
            if (profile is null)
            {
                return null;
            }
        }
        else
        {
            profile = profiles.FirstOrDefault(item =>
                string.Equals(item.Name, selectedName, StringComparison.OrdinalIgnoreCase));
        }

        if (profile is null && selectedName?.Contains("局域网", StringComparison.OrdinalIgnoreCase) == true)
        {
            profile = profiles.FirstOrDefault(item =>
                string.Equals(item.Id, "lan-default", StringComparison.OrdinalIgnoreCase) ||
                item.Kind == ConnectionProfileKind.Lan);
        }

        if (profile is null &&
            (selectedName?.Contains("本地", StringComparison.OrdinalIgnoreCase) == true ||
             selectedName?.Contains("本机", StringComparison.OrdinalIgnoreCase) == true))
        {
            profile = profiles.FirstOrDefault(item =>
                string.Equals(item.Id, "local-machine", StringComparison.OrdinalIgnoreCase) ||
                item.Kind == ConnectionProfileKind.Local);
        }

        if (profile is null)
        {
            return null;
        }

        bool hasClientConfiguration = profile.EnabledClients.Count == 0 ||
                                      profile.EnabledClients.Contains(cli) ||
                                      profile.ClientBaseUrls.ContainsKey(cli);
        return hasClientConfiguration ? profile : null;
    }

    private static string GetCliDisplayName(CliKind cli) => cli switch
    {
        CliKind.Codex => "Codex",
        CliKind.ClaudeCode => "Claude Code",
        CliKind.GeminiCli => "Gemini CLI",
        _ => cli.ToString(),
    };

    private static string SanitizeTerminalTitle(string title)
    {
        string value = new(title.Where(character => !char.IsControl(character)).ToArray());
        value = value.Trim();
        return value.Length <= 96 ? value : value[..96] + "…";
    }

    private void RunOnUi(Action action)
    {
        if (Dispatcher.CheckAccess())
        {
            action();
            return;
        }

        if (!Dispatcher.HasShutdownStarted)
        {
            _ = Dispatcher.BeginInvoke(action);
        }
    }

    private static void RegisterActiveLaunch(
        CancellationTokenSource cancellation,
        Task launchTask)
    {
        lock (ActiveLaunchGate)
        {
            _activeLaunchCancellation = cancellation;
            _activeLaunchTask = launchTask;
        }
    }

    private static Task? CancelActiveLaunch()
    {
        lock (ActiveLaunchGate)
        {
            try
            {
                _activeLaunchCancellation?.Cancel();
            }
            catch (ObjectDisposedException)
            {
            }

            return _activeLaunchTask;
        }
    }

    private static void ClearActiveLaunch(
        CancellationTokenSource cancellation,
        Task launchTask)
    {
        lock (ActiveLaunchGate)
        {
            if (!ReferenceEquals(_activeLaunchCancellation, cancellation) ||
                !ReferenceEquals(_activeLaunchTask, launchTask))
            {
                return;
            }

            _activeLaunchCancellation = null;
            _activeLaunchTask = null;
        }
    }

    private static async Task ObserveLaunchCompletionAsync(Task launchTask)
    {
        try
        {
            await launchTask.ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
        }
        catch
        {
            // The interactive view reports launch errors. Shutdown and restart
            // paths only need to observe completion without duplicating UI.
        }
    }

    internal static async Task ShutdownForApplicationAsync(TimeSpan shutdownLimit)
    {
        if (shutdownLimit <= TimeSpan.Zero)
        {
            throw new ArgumentOutOfRangeException(nameof(shutdownLimit));
        }

        Task? pendingLaunch = CancelActiveLaunch();
        Task observeLaunch = pendingLaunch is null
            ? Task.CompletedTask
            : ObserveLaunchCompletionAsync(pendingLaunch);
        Task stopTerminal = TerminalHost.Shared.ShutdownAsync(shutdownLimit);

        try
        {
            await Task.WhenAll(observeLaunch, stopTerminal).WaitAsync(shutdownLimit);
        }
        catch (TimeoutException)
        {
            // The ConPTY job object is released by process shutdown even if a
            // child ignores Ctrl+C beyond this bounded grace period.
        }
        catch
        {
            // The central window shutdown coordinator must remain able to close.
        }
    }

    private static Brush CreateBrush(byte red, byte green, byte blue)
    {
        var brush = new SolidColorBrush(Color.FromRgb(red, green, blue));
        brush.Freeze();
        return brush;
    }
}
