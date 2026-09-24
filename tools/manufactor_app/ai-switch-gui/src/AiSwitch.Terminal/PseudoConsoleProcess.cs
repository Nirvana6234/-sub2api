using Porta.Pty;

namespace LanAi.Workspace.Terminal;

internal sealed class PseudoConsoleProcess : IAsyncDisposable
{
    private readonly IPtyConnection? _connection;
    private readonly CancellationTokenSource _readCancellation = new();
    private readonly bool _exitedDuringStartup;
    private Task _readTask = Task.CompletedTask;
    private bool _readStarted;
    private bool _exited;
    private bool _disposed;

    private PseudoConsoleProcess(
        IPtyConnection? connection,
        bool exitedDuringStartup = false)
    {
        _connection = connection;
        _exitedDuringStartup = exitedDuringStartup;
        if (_connection is not null)
        {
            _connection.ProcessExited += OnProcessExited;
        }
    }

    public event EventHandler<ReadOnlyMemory<byte>>? OutputReceived;
    public event EventHandler? Exited;

    public bool IsRunning => !_disposed && !_exited && _connection is not null;
    public int ProcessId => _connection?.Pid ?? 0;

    public static async Task<PseudoConsoleProcess> StartAsync(
        TerminalCommand command,
        int columns,
        int rows,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(command);
        if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 17763))
        {
            throw new PlatformNotSupportedException("内嵌终端需要 Windows 10 1809 或更高版本。 ");
        }

        if (!Directory.Exists(command.WorkingDirectory))
        {
            throw new DirectoryNotFoundException($"项目目录不存在：{command.WorkingDirectory}");
        }

        var environment = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase)
        {
            ["TERM"] = "xterm-256color",
            ["COLORTERM"] = "truecolor"
        };
        var windowsDirectory = Environment.GetEnvironmentVariable("WINDIR");
        if (string.IsNullOrWhiteSpace(windowsDirectory))
        {
            windowsDirectory = Environment.GetFolderPath(Environment.SpecialFolder.Windows);
        }
        if (string.IsNullOrWhiteSpace(windowsDirectory))
        {
            windowsDirectory = Directory.GetParent(Environment.SystemDirectory)?.FullName
                ?? @"C:\Windows";
        }
        environment["WINDIR"] = windowsDirectory;
        environment["SystemRoot"] = windowsDirectory;
        if (string.IsNullOrWhiteSpace(Environment.GetEnvironmentVariable("WINDIR")))
        {
            Environment.SetEnvironmentVariable("WINDIR", windowsDirectory, EnvironmentVariableTarget.Process);
        }
        if (string.IsNullOrWhiteSpace(Environment.GetEnvironmentVariable("SystemRoot")))
        {
            Environment.SetEnvironmentVariable("SystemRoot", windowsDirectory, EnvironmentVariableTarget.Process);
        }
        if (command.Environment is not null)
        {
            foreach (var pair in command.Environment)
            {
                environment[pair.Key] = pair.Value ?? string.Empty;
            }
        }

        var options = new PtyOptions
        {
            Name = command.DisplayName,
            Cols = columns,
            Rows = rows,
            Cwd = command.WorkingDirectory,
            App = command.FileName,
            CommandLine = command.Arguments.ToArray(),
            Environment = environment
        };

        try
        {
            IPtyConnection connection = await PtyProvider.SpawnAsync(options, cancellationToken);
            return new PseudoConsoleProcess(connection);
        }
        catch (ArgumentException exception) when (ExitedBeforePortaPtyAttached(exception))
        {
            // Porta.Pty 1.0.7 creates the child process before it constructs
            // PseudoConsoleConnection. A command such as "cmd /c echo x" may
            // legitimately exit in that tiny window; the library then calls
            // Process.GetProcessById for an already-gone PID and throws. Treat
            // this exact condition as a natural exit, not a failed terminal
            // startup. No command is delayed, wrapped or retried.
            return new PseudoConsoleProcess(connection: null, exitedDuringStartup: true);
        }
    }

    public void StartReading()
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        if (_readStarted)
        {
            return;
        }

        _readStarted = true;
        if (_exitedDuringStartup)
        {
            MarkExited();
            return;
        }

        _readTask = ReadLoopAsync(_readCancellation.Token);
        if (_connection!.WaitForExit(0))
        {
            MarkExited();
        }
    }

    public async ValueTask WriteAsync(
        ReadOnlyMemory<byte> data,
        CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        if (_connection is null || _exited)
        {
            return;
        }

        await _connection.WriterStream.WriteAsync(data, cancellationToken);
        await _connection.WriterStream.FlushAsync(cancellationToken);
    }

    public void Resize(int columns, int rows)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        if (_connection is not null && !_exited)
        {
            _connection.Resize(columns, rows);
        }
    }

    public async Task StopAsync(TimeSpan gracefulTimeout)
    {
        if (_disposed || _exited || _connection is null)
        {
            return;
        }

        try
        {
            await WriteAsync(new byte[] { 0x03 });
            var exited = await Task.Run(
                () => _connection!.WaitForExit(Math.Max(1, (int)gracefulTimeout.TotalMilliseconds)));
            if (!exited)
            {
                _connection!.Kill();
                _ = await Task.Run(() => _connection.WaitForExit(3000));
            }
        }
        catch (ObjectDisposedException)
        {
        }
        finally
        {
            MarkExited();
        }
    }

    private async Task ReadLoopAsync(CancellationToken cancellationToken)
    {
        var buffer = new byte[16 * 1024];
        try
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                var read = await _connection!.ReaderStream.ReadAsync(buffer, cancellationToken);
                if (read == 0)
                {
                    break;
                }

                var copy = new byte[read];
                Buffer.BlockCopy(buffer, 0, copy, 0, read);
                OutputReceived?.Invoke(this, copy);
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
        }
        catch (ObjectDisposedException) when (_disposed)
        {
        }
    }

    private void OnProcessExited(object? sender, PtyExitedEventArgs e) => MarkExited();

    private void MarkExited()
    {
        if (_exited)
        {
            return;
        }

        _exited = true;
        Exited?.Invoke(this, EventArgs.Empty);
    }

    private static bool ExitedBeforePortaPtyAttached(ArgumentException exception)
    {
        string stackTrace = exception.StackTrace ?? string.Empty;
        return exception.Message.StartsWith("Process with an Id", StringComparison.Ordinal) &&
               stackTrace.Contains(
                   "System.Diagnostics.Process.GetProcessById",
                   StringComparison.Ordinal) &&
               stackTrace.Contains(
                   "Porta.Pty.Windows.PseudoConsoleConnection",
                   StringComparison.Ordinal);
    }

    public async ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return;
        }

        try
        {
            await StopAsync(TimeSpan.FromMilliseconds(800));
        }
        finally
        {
            _disposed = true;
            _readCancellation.Cancel();
            if (_connection is not null)
            {
                _connection.ProcessExited -= OnProcessExited;
                _connection.Dispose();
            }
            try
            {
                await _readTask;
            }
            catch (OperationCanceledException)
            {
            }

            _readCancellation.Dispose();
        }
    }
}
