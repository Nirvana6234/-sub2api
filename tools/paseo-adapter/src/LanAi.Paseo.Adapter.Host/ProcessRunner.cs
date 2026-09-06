using System.Diagnostics;

namespace LanAi.Paseo.Adapter.Host;

/// <summary>A process the host started, reduced to what supervision needs.</summary>
/// <remarks>
/// An interface rather than <see cref="Process"/> directly so the state machine —
/// backoff, timeouts, ordered stop — is testable without spawning anything. The
/// parts that genuinely need a real process (the cage) are tested separately.
/// </remarks>
public interface IHostedProcess : IDisposable
{
    int Id { get; }

    bool HasExited { get; }

    /// <summary>Completes when the process exits.</summary>
    Task WaitForExitAsync(CancellationToken cancellationToken);

    /// <summary>Terminates the process and its children.</summary>
    void Kill();
}

/// <summary>Starts processes for the host.</summary>
public interface IProcessRunner
{
    IHostedProcess Start(
        string fileName,
        IReadOnlyList<string> arguments,
        IReadOnlyDictionary<string, string> environment,
        Action<string>? onStdErrLine = null);
}

/// <summary>Real process runner. Redirects stderr so daemon failures reach our log, not a void.</summary>
public sealed class ProcessRunner : IProcessRunner
{
    private readonly IProcessCage _cage;

    public ProcessRunner(IProcessCage cage)
    {
        _cage = cage ?? throw new ArgumentNullException(nameof(cage));
    }

    public IHostedProcess Start(
        string fileName,
        IReadOnlyList<string> arguments,
        IReadOnlyDictionary<string, string> environment,
        Action<string>? onStdErrLine = null)
    {
        var startInfo = new ProcessStartInfo(fileName)
        {
            UseShellExecute = false,
            RedirectStandardError = true,
            RedirectStandardOutput = true,
            CreateNoWindow = true,
        };

        foreach (var argument in arguments)
        {
            startInfo.ArgumentList.Add(argument);
        }

        foreach (var (key, value) in environment)
        {
            startInfo.Environment[key] = value;
        }

        var process = Process.Start(startInfo)
            ?? throw new InvalidOperationException($"Failed to start {fileName}");

        // Caged before anything else: a crash between start and Hold would leave an
        // orphan, which is the exact failure the cage exists to prevent.
        _cage.Hold(process);

        process.ErrorDataReceived += (_, e) =>
        {
            if (!string.IsNullOrEmpty(e.Data)) onStdErrLine?.Invoke(e.Data);
        };
        process.BeginErrorReadLine();
        // stdout is drained but discarded: the daemon writes its real log to the
        // private home, and an unread pipe eventually blocks the child.
        process.OutputDataReceived += (_, _) => { };
        process.BeginOutputReadLine();

        return new HostedProcess(process);
    }

    private sealed class HostedProcess : IHostedProcess
    {
        private readonly Process _process;

        public HostedProcess(Process process) => _process = process;

        public int Id => _process.Id;

        public bool HasExited => _process.HasExited;

        public Task WaitForExitAsync(CancellationToken cancellationToken) =>
            _process.WaitForExitAsync(cancellationToken);

        public void Kill()
        {
            try
            {
                if (!_process.HasExited)
                {
                    _process.Kill(entireProcessTree: true);
                }
            }
            catch (InvalidOperationException)
            {
                // Already gone between the check and the call.
            }
        }

        public void Dispose() => _process.Dispose();
    }
}
