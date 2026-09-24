using System.Diagnostics;
using System.Text;
using LanAi.Workspace.Terminal;

namespace LanAi.Workspace.Chat;

internal interface IStructuredCliProcess : IAsyncDisposable
{
    event EventHandler<string>? OutputLineReceived;

    event EventHandler<string>? ErrorLineReceived;

    event EventHandler<int>? Exited;

    bool IsRunning { get; }

    Task StartAsync(TerminalCommand command, CancellationToken cancellationToken = default);

    Task WriteLineAsync(string line, CancellationToken cancellationToken = default);

    Task StopAsync(TimeSpan timeout, CancellationToken cancellationToken = default);
}

internal sealed class StructuredCliProcess : IStructuredCliProcess
{
    private readonly object _gate = new();
    private Process? _process;
    private Task? _stdoutTask;
    private Task? _stderrTask;
    private bool _disposed;

    public event EventHandler<string>? OutputLineReceived;

    public event EventHandler<string>? ErrorLineReceived;

    public event EventHandler<int>? Exited;

    public bool IsRunning
    {
        get
        {
            lock (_gate)
            {
                return _process is { HasExited: false };
            }
        }
    }

    public Task StartAsync(TerminalCommand command, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(command);
        ObjectDisposedException.ThrowIf(_disposed, this);
        cancellationToken.ThrowIfCancellationRequested();

        lock (_gate)
        {
            if (_process is not null)
            {
                throw new InvalidOperationException("结构化 CLI 进程已经启动。");
            }

            var startInfo = new ProcessStartInfo
            {
                FileName = command.FileName,
                WorkingDirectory = command.WorkingDirectory,
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardInput = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                StandardInputEncoding = new UTF8Encoding(encoderShouldEmitUTF8Identifier: false),
                StandardOutputEncoding = Encoding.UTF8,
                StandardErrorEncoding = Encoding.UTF8,
            };

            foreach (string argument in command.Arguments)
            {
                startInfo.ArgumentList.Add(argument);
            }

            if (command.Environment is not null)
            {
                foreach ((string key, string? value) in command.Environment)
                {
                    if (value is null)
                    {
                        startInfo.Environment.Remove(key);
                    }
                    else
                    {
                        startInfo.Environment[key] = value;
                    }
                }
            }

            var process = new Process
            {
                StartInfo = startInfo,
                EnableRaisingEvents = true,
            };
            process.Exited += Process_OnExited;

            try
            {
                if (!process.Start())
                {
                    throw new InvalidOperationException("无法启动结构化 CLI 进程。");
                }
            }
            catch
            {
                process.Exited -= Process_OnExited;
                process.Dispose();
                throw;
            }

            _process = process;
            _stdoutTask = ReadLinesAsync(
                process.StandardOutput,
                line => OutputLineReceived?.Invoke(this, line));
            _stderrTask = ReadLinesAsync(
                process.StandardError,
                line => ErrorLineReceived?.Invoke(this, line));
        }

        return Task.CompletedTask;
    }

    public async Task WriteLineAsync(string line, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(line);
        Process process;
        lock (_gate)
        {
            process = _process is { HasExited: false } running
                ? running
                : throw new InvalidOperationException("结构化 CLI 进程未运行。");
        }

        await process.StandardInput.WriteLineAsync(line.AsMemory(), cancellationToken).ConfigureAwait(false);
        await process.StandardInput.FlushAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task StopAsync(TimeSpan timeout, CancellationToken cancellationToken = default)
    {
        if (timeout <= TimeSpan.Zero)
        {
            throw new ArgumentOutOfRangeException(nameof(timeout));
        }

        Process? process;
        lock (_gate)
        {
            process = _process;
        }

        if (process is null || process.HasExited)
        {
            await ObserveReadersAsync().ConfigureAwait(false);
            return;
        }

        try
        {
            process.StandardInput.Close();
            using var timeoutSource = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeoutSource.CancelAfter(timeout);
            try
            {
                await process.WaitForExitAsync(timeoutSource.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
            {
                process.Kill(entireProcessTree: true);
                await process.WaitForExitAsync(CancellationToken.None).ConfigureAwait(false);
            }
        }
        catch (InvalidOperationException)
        {
            // The process exited between the state check and shutdown request.
        }

        await ObserveReadersAsync().ConfigureAwait(false);
    }

    private async Task ObserveReadersAsync()
    {
        Task[] readers;
        lock (_gate)
        {
            readers = new[] { _stdoutTask, _stderrTask }
                .Where(task => task is not null)
                .Cast<Task>()
                .ToArray();
        }

        if (readers.Length > 0)
        {
            await Task.WhenAll(readers).ConfigureAwait(false);
        }
    }

    private static async Task ReadLinesAsync(StreamReader reader, Action<string> publish)
    {
        while (await reader.ReadLineAsync().ConfigureAwait(false) is { } line)
        {
            publish(line);
        }
    }

    private void Process_OnExited(object? sender, EventArgs e)
    {
        if (sender is Process process)
        {
            Exited?.Invoke(this, process.ExitCode);
        }
    }

    public async ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        await StopAsync(TimeSpan.FromSeconds(2)).ConfigureAwait(false);

        lock (_gate)
        {
            if (_process is not null)
            {
                _process.Exited -= Process_OnExited;
                _process.Dispose();
                _process = null;
            }
        }
    }
}
