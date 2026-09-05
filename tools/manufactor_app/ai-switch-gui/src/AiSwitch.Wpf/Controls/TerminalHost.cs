using LanAi.Workspace.Terminal;

namespace LanAi.Workspace.Wpf.Controls;

public enum TerminalHostState
{
    Idle,
    Starting,
    Running,
    Stopping,
    Exited,
    Faulted,
}

public sealed class TerminalHostStateChangedEventArgs : EventArgs
{
    public TerminalHostStateChangedEventArgs(TerminalHostState state, string message)
    {
        State = state;
        Message = message;
    }

    public TerminalHostState State { get; }

    public string Message { get; }
}

public sealed record TerminalDisplayMetadata(string DisplayName, string WorkingDirectory);

/// <summary>
/// Owns the single interactive CLI session shown by the workspace. The host is
/// deliberately independent from the WPF visual tree, so navigating away from
/// the terminal page does not orphan or terminate the official CLI process.
/// </summary>
public sealed class TerminalHost : IAsyncDisposable
{
    private readonly SemaphoreSlim _transitionGate = new(1, 1);
    private readonly object _stateGate = new();
    private readonly CancellationTokenSource _shutdownCancellation = new();
    private TerminalSession? _session;
    private TerminalFrame? _currentFrame;
    private TerminalDisplayMetadata? _activeMetadata;
    private TerminalHostState _state = TerminalHostState.Idle;
    private string _statusMessage = "等待启动";
    private bool _disposed;

    public static TerminalHost Shared { get; } = new();

    public event EventHandler? FrameChanged;

    public event EventHandler<TerminalHostStateChangedEventArgs>? StateChanged;

    public TerminalFrame? CurrentFrame => Volatile.Read(ref _currentFrame);

    public TerminalDisplayMetadata? ActiveMetadata
    {
        get
        {
            lock (_stateGate)
            {
                return _activeMetadata;
            }
        }
    }

    public TerminalHostState State
    {
        get
        {
            lock (_stateGate)
            {
                return _state;
            }
        }
    }

    public string StatusMessage
    {
        get
        {
            lock (_stateGate)
            {
                return _statusMessage;
            }
        }
    }

    public bool IsRunning => State == TerminalHostState.Running && _session?.IsRunning == true;

    public bool IsShutdownRequested => _shutdownCancellation.IsCancellationRequested;

    public async Task StartAsync(
        TerminalCommand command,
        int columns,
        int rows,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(command);
        ObjectDisposedException.ThrowIf(_disposed, this);

        using var linkedCancellation = CancellationTokenSource.CreateLinkedTokenSource(
            cancellationToken,
            _shutdownCancellation.Token);
        CancellationToken effectiveCancellation = linkedCancellation.Token;

        await _transitionGate.WaitAsync(effectiveCancellation).ConfigureAwait(false);
        try
        {
            ObjectDisposedException.ThrowIf(_disposed, this);
            effectiveCancellation.ThrowIfCancellationRequested();
            await StopCurrentSessionAsync(publishStoppingState: _session is not null).ConfigureAwait(false);

            effectiveCancellation.ThrowIfCancellationRequested();
            var session = new TerminalSession(columns, rows);
            session.FrameChanged += Session_OnFrameChanged;
            session.Exited += Session_OnExited;

            lock (_stateGate)
            {
                _session = session;
                _activeMetadata = new TerminalDisplayMetadata(
                    command.DisplayName ?? "终端",
                    command.WorkingDirectory);
            }

            Volatile.Write(ref _currentFrame, null);
            FrameChanged?.Invoke(this, EventArgs.Empty);
            PublishState(TerminalHostState.Starting, $"正在启动 {command.DisplayName ?? "终端"}");

            try
            {
                await session.StartAsync(command, effectiveCancellation).ConfigureAwait(false);
                CaptureFrame(session);
                PublishState(
                    session.IsRunning ? TerminalHostState.Running : TerminalHostState.Exited,
                    session.IsRunning ? "官方 CLI 已连接" : "进程已退出");
            }
            catch (Exception exception)
            {
                session.FrameChanged -= Session_OnFrameChanged;
                session.Exited -= Session_OnExited;

                lock (_stateGate)
                {
                    if (ReferenceEquals(_session, session))
                    {
                        _session = null;
                        _activeMetadata = null;
                    }
                }

                await session.DisposeAsync().ConfigureAwait(false);
                PublishState(
                    exception is OperationCanceledException && effectiveCancellation.IsCancellationRequested
                        ? TerminalHostState.Idle
                        : TerminalHostState.Faulted,
                    exception is OperationCanceledException && effectiveCancellation.IsCancellationRequested
                        ? (_shutdownCancellation.IsCancellationRequested ? "应用正在退出" : "启动已取消")
                        : exception.Message);
                throw;
            }
        }
        finally
        {
            _transitionGate.Release();
        }
    }

    public async Task StopAsync(CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        await _transitionGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await StopCurrentSessionAsync(publishStoppingState: true).ConfigureAwait(false);
        }
        finally
        {
            _transitionGate.Release();
        }
    }

    public ValueTask SendTextAsync(string text, CancellationToken cancellationToken = default)
    {
        TerminalSession? session = _session;
        return session?.IsRunning == true
            ? session.SendTextAsync(text, cancellationToken)
            : ValueTask.CompletedTask;
    }

    public ValueTask SendKeyAsync(
        TerminalInputKey key,
        TerminalInputModifiers modifiers = TerminalInputModifiers.None,
        CancellationToken cancellationToken = default)
    {
        TerminalSession? session = _session;
        return session?.IsRunning == true
            ? session.SendKeyAsync(key, modifiers, cancellationToken)
            : ValueTask.CompletedTask;
    }

    public ValueTask SendCharacterAsync(
        char character,
        TerminalInputModifiers modifiers = TerminalInputModifiers.None,
        CancellationToken cancellationToken = default)
    {
        TerminalSession? session = _session;
        return session?.IsRunning == true
            ? session.SendCharacterAsync(character, modifiers, cancellationToken)
            : ValueTask.CompletedTask;
    }

    public void Resize(int columns, int rows)
    {
        TerminalSession? session = _session;
        if (session is null)
        {
            return;
        }

        try
        {
            session.Resize(columns, rows);
        }
        catch (ObjectDisposedException)
        {
            // A resize can race with a user-requested stop during navigation.
        }
    }

    public void Scroll(int lines)
    {
        TerminalSession? session = _session;
        if (session is null || lines == 0)
        {
            return;
        }

        try
        {
            session.Scroll(lines);
        }
        catch (ObjectDisposedException)
        {
            // The last wheel event may arrive while the session is closing.
        }
    }

    private async Task StopCurrentSessionAsync(bool publishStoppingState)
    {
        TerminalSession? session;
        lock (_stateGate)
        {
            session = _session;
        }

        if (session is null)
        {
            if (publishStoppingState)
            {
                PublishState(TerminalHostState.Idle, "等待启动");
            }

            return;
        }

        if (publishStoppingState)
        {
            PublishState(TerminalHostState.Stopping, "正在停止终端进程");
        }

        try
        {
            await session.StopAsync().ConfigureAwait(false);
        }
        finally
        {
            CaptureFrame(session);
            session.FrameChanged -= Session_OnFrameChanged;
            session.Exited -= Session_OnExited;
            await session.DisposeAsync().ConfigureAwait(false);

            lock (_stateGate)
            {
                if (ReferenceEquals(_session, session))
                {
                    _session = null;
                    _activeMetadata = null;
                }
            }

            PublishState(TerminalHostState.Exited, "终端已停止");
        }
    }

    private void Session_OnFrameChanged(object? sender, EventArgs e)
    {
        if (sender is TerminalSession session)
        {
            CaptureFrame(session);
        }
    }

    private void Session_OnExited(object? sender, EventArgs e)
    {
        if (sender is not TerminalSession session || !ReferenceEquals(session, _session))
        {
            return;
        }

        CaptureFrame(session);
        lock (_stateGate)
        {
            _activeMetadata = null;
        }
        PublishState(TerminalHostState.Exited, "官方 CLI 进程已退出");
    }

    private void CaptureFrame(TerminalSession session)
    {
        if (!ReferenceEquals(session, _session))
        {
            return;
        }

        try
        {
            Volatile.Write(ref _currentFrame, session.CaptureFrame());
            FrameChanged?.Invoke(this, EventArgs.Empty);
        }
        catch (ObjectDisposedException)
        {
            // The final process event may be delivered during async disposal.
        }
    }

    private void PublishState(TerminalHostState state, string message)
    {
        lock (_stateGate)
        {
            _state = state;
            _statusMessage = message;
        }

        StateChanged?.Invoke(this, new TerminalHostStateChangedEventArgs(state, message));
    }

    public void RequestShutdown() => _shutdownCancellation.Cancel();

    public async Task ShutdownAsync(TimeSpan timeout)
    {
        if (timeout <= TimeSpan.Zero)
        {
            throw new ArgumentOutOfRangeException(nameof(timeout), "退出等待时间必须大于零。");
        }

        RequestShutdown();
        Task stopTask = StopForShutdownAsync();
        try
        {
            await stopTask.WaitAsync(timeout).ConfigureAwait(false);
        }
        catch (TimeoutException)
        {
            _ = ObserveCompletionAsync(stopTask);
        }
    }

    private async Task StopForShutdownAsync()
    {
        await _transitionGate.WaitAsync().ConfigureAwait(false);
        try
        {
            if (_disposed)
            {
                return;
            }

            await StopCurrentSessionAsync(publishStoppingState: _session is not null).ConfigureAwait(false);
            _disposed = true;
        }
        finally
        {
            _transitionGate.Release();
        }
    }

    private static async Task ObserveCompletionAsync(Task task)
    {
        try
        {
            await task.ConfigureAwait(false);
        }
        catch
        {
            // Application shutdown is already in progress. Observing the task
            // prevents an abandoned stop failure from becoming unobserved.
        }
    }

    public async ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return;
        }

        RequestShutdown();
        await _transitionGate.WaitAsync().ConfigureAwait(false);
        try
        {
            if (_disposed)
            {
                return;
            }

            await StopCurrentSessionAsync(publishStoppingState: false).ConfigureAwait(false);
            _disposed = true;
        }
        finally
        {
            _transitionGate.Release();
        }
    }
}
