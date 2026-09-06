using System.Net;
using System.Net.Sockets;

namespace LanAi.Paseo.Adapter.Host;

/// <summary>Lifecycle states a consumer has to be able to render.</summary>
public enum RuntimeState
{
    Stopped,
    Starting,
    Running,
    Stopping,
    /// <summary>Exited unexpectedly; a restart is scheduled.</summary>
    Crashed,
    /// <summary>Restarts gave up. No further attempts without an explicit start.</summary>
    Faulted,
}

/// <summary>
/// A state change, with everything the UI needs to act on it.
/// </summary>
/// <param name="LogPath">
/// The daemon's own log inside the private home. Carried here so a consumer can
/// offer "export logs" on a fault without reconstructing the path — the moment a
/// user is stuck is the wrong moment to make them find a file.
/// </param>
public sealed record RuntimeStateChanged(RuntimeState State, string? Detail, string LogPath);

/// <summary>Readiness check for a started daemon.</summary>
public interface IDaemonHealthProbe
{
    Task<bool> IsHealthyAsync(int port, CancellationToken cancellationToken);
}

/// <summary>
/// Probes <c>GET /api/health</c> on loopback.
/// </summary>
/// <remarks>
/// That route is exempt from the daemon's bearer middleware, so readiness can be
/// established before any password has been handed to anything — which is what
/// lets the host treat "is it up" and "can I talk to it" as separate questions.
/// </remarks>
public sealed class HttpDaemonHealthProbe : IDaemonHealthProbe, IDisposable
{
    private readonly HttpClient _client = new() { Timeout = TimeSpan.FromSeconds(2) };

    public async Task<bool> IsHealthyAsync(int port, CancellationToken cancellationToken)
    {
        try
        {
            using var response = await _client
                .GetAsync($"http://127.0.0.1:{port}/api/health", cancellationToken)
                .ConfigureAwait(false);
            return response.IsSuccessStatusCode;
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException)
        {
            return false;
        }
    }

    public void Dispose() => _client.Dispose();
}

/// <summary>
/// Owns the daemon process: start, readiness, crash restart with backoff, and an
/// ordered stop.
/// </summary>
/// <remarks>
/// <para>
/// The supervisor is what makes "hosted by the client" true in the ordinary case;
/// <see cref="IProcessCage"/> is the backstop for the case where this code never
/// gets to run.
/// </para>
/// <para>
/// Stopping is deliberately observable: <see cref="StopAsync"/> returns only once
/// the process is gone or the timeout elapsed, and says which. A consumer that
/// restores <c>~/.codex</c> after shutdown depends on that ordering — restoring
/// while a daemon is still running would hand a live codex process a half-old
/// configuration.
/// </para>
/// </remarks>
public sealed class DaemonSupervisor : IAsyncDisposable
{
    private readonly PaseoRuntimeOptions _options;
    private readonly IProcessRunner _runner;
    private readonly IDaemonHealthProbe _healthProbe;
    private readonly Func<TimeSpan, CancellationToken, Task> _delay;
    private readonly object _gate = new();

    private IHostedProcess? _process;
    private CancellationTokenSource? _watchdog;
    private int _consecutiveFailures;
    private bool _stopRequested;

    public DaemonSupervisor(
        PaseoRuntimeOptions options,
        IProcessRunner runner,
        IDaemonHealthProbe healthProbe,
        Func<TimeSpan, CancellationToken, Task>? delay = null)
    {
        _options = options ?? throw new ArgumentNullException(nameof(options));
        _runner = runner ?? throw new ArgumentNullException(nameof(runner));
        _healthProbe = healthProbe ?? throw new ArgumentNullException(nameof(healthProbe));
        _delay = delay ?? ((duration, token) => Task.Delay(duration, token));
    }

    /// <summary>Raised on every state transition. Handlers must not throw.</summary>
    public event EventHandler<RuntimeStateChanged>? StateChanged;

    public RuntimeState State { get; private set; } = RuntimeState.Stopped;

    /// <summary>The loopback port the daemon is listening on, once it is running.</summary>
    public int Port { get; private set; }

    /// <summary>Password handed to the daemon for this run. Regenerated on every start.</summary>
    public string Password { get; private set; } = string.Empty;

    /// <summary>Starts the daemon and waits until it answers its health endpoint.</summary>
    /// <exception cref="InvalidOperationException">The daemon never became healthy.</exception>
    public async Task StartAsync(CancellationToken cancellationToken = default)
    {
        _stopRequested = false;
        _consecutiveFailures = 0;
        await StartOnceAsync(cancellationToken).ConfigureAwait(false);
    }

    private async Task StartOnceAsync(CancellationToken cancellationToken)
    {
        Transition(RuntimeState.Starting, null);

        Port = _options.Port ?? PickFreePort();
        Password = GeneratePassword();

        // Rewritten before every start, never merged: the daemon edits this file
        // itself (it will happily add back a CORS origin and an app base URL), so
        // the only way our switches stay true is to lay them down again each time.
        DaemonConfigComposer.Write(_options.PaseoHomePath, Port, _options.RelayEndpoint, _options.RelayUseTls);

        var environment = new Dictionary<string, string>
        {
            ["PASEO_HOME"] = _options.PaseoHomePath,
            // Env rather than config.json: the file wants a bcrypt digest, and
            // hashing one would mean a NuGet dependency this assembly refuses.
            ["PASEO_PASSWORD"] = Password,
        };

        var lastStdErr = string.Empty;
        var process = _runner.Start(
            _options.NodeExecutablePath,
            [_options.DaemonEntryPath, "daemon", "start", "--foreground", "--home", _options.PaseoHomePath],
            environment,
            line => lastStdErr = line);

        lock (_gate)
        {
            _process = process;
        }

        var healthy = await WaitForHealthAsync(process, cancellationToken).ConfigureAwait(false);
        if (!healthy)
        {
            process.Kill();
            // The port is in the message on purpose: "start timed out" hides the
            // most common real cause, which is that something else took the port.
            var detail = process.HasExited
                ? $"daemon exited during startup on port {Port}: {lastStdErr}"
                : $"daemon did not answer /api/health on port {Port} within {_options.StartTimeout.TotalSeconds:N0}s";
            Transition(RuntimeState.Crashed, detail);
            throw new InvalidOperationException(detail);
        }

        _consecutiveFailures = 0;
        Transition(RuntimeState.Running, null);
        BeginWatching(process);
    }

    private async Task<bool> WaitForHealthAsync(IHostedProcess process, CancellationToken cancellationToken)
    {
        using var deadline = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        deadline.CancelAfter(_options.StartTimeout);
        try
        {
            while (!deadline.Token.IsCancellationRequested)
            {
                if (process.HasExited)
                {
                    return false;
                }

                if (await _healthProbe.IsHealthyAsync(Port, deadline.Token).ConfigureAwait(false))
                {
                    return true;
                }

                await _delay(TimeSpan.FromMilliseconds(200), deadline.Token).ConfigureAwait(false);
            }
        }
        catch (OperationCanceledException)
        {
            // Falls through to the timeout path below.
        }

        return false;
    }

    private void BeginWatching(IHostedProcess process)
    {
        var watchdog = new CancellationTokenSource();
        _watchdog = watchdog;
        _ = Task.Run(async () =>
        {
            try
            {
                await process.WaitForExitAsync(watchdog.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                return;
            }

            if (_stopRequested || watchdog.IsCancellationRequested)
            {
                return;
            }

            await HandleCrashAsync(watchdog.Token).ConfigureAwait(false);
        });
    }

    private async Task HandleCrashAsync(CancellationToken cancellationToken)
    {
        _consecutiveFailures++;
        if (_consecutiveFailures >= _options.MaxRestartAttempts)
        {
            // Faulted is a dead end on purpose. Restarting forever hides a
            // permanent problem (a broken install, a port that never frees) behind
            // a busy spinner; a consumer can offer an explicit retry.
            Transition(RuntimeState.Faulted, $"daemon failed {_consecutiveFailures} times; not restarting");
            return;
        }

        var backoff = BackoffFor(_consecutiveFailures);
        Transition(RuntimeState.Crashed, $"attempt {_consecutiveFailures}, retrying in {backoff.TotalSeconds:N0}s");
        try
        {
            await _delay(backoff, cancellationToken).ConfigureAwait(false);
            await StartOnceAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
            // Shutting down mid-backoff is not a failure.
        }
        catch (Exception ex)
        {
            if (State != RuntimeState.Faulted)
            {
                await HandleCrashAsync(cancellationToken).ConfigureAwait(false);
            }
            else
            {
                Transition(RuntimeState.Faulted, ex.Message);
            }
        }
    }

    internal static TimeSpan BackoffFor(int attempt) => attempt switch
    {
        <= 1 => TimeSpan.FromSeconds(1),
        2 => TimeSpan.FromSeconds(2),
        3 => TimeSpan.FromSeconds(5),
        4 => TimeSpan.FromSeconds(15),
        _ => TimeSpan.FromSeconds(30),
    };

    /// <summary>
    /// Stops the daemon and waits for it to be gone.
    /// </summary>
    /// <returns>
    /// <c>true</c> if the process exited within the stop timeout; <c>false</c> if
    /// it had to be killed. Callers that restore user configuration afterwards
    /// need to know which, because "stopped" and "killed while writing" are not
    /// the same state to restore over.
    /// </returns>
    public async Task<bool> StopAsync(CancellationToken cancellationToken = default)
    {
        _stopRequested = true;
        IHostedProcess? process;
        lock (_gate)
        {
            process = _process;
            _process = null;
        }

        _watchdog?.Cancel();

        if (process is null || process.HasExited)
        {
            Transition(RuntimeState.Stopped, null);
            return true;
        }

        Transition(RuntimeState.Stopping, null);
        process.Kill();

        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(_options.StopTimeout);
        var exited = true;
        try
        {
            await process.WaitForExitAsync(timeout.Token).ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
            exited = false;
        }

        process.Dispose();
        Transition(RuntimeState.Stopped, exited ? null : "daemon did not exit within the stop timeout");
        return exited;
    }

    private void Transition(RuntimeState state, string? detail)
    {
        State = state;
        StateChanged?.Invoke(
            this,
            new RuntimeStateChanged(state, detail, DaemonConfigComposer.LogPath(_options.PaseoHomePath)));
    }

    /// <summary>
    /// Picks a free loopback port.
    /// </summary>
    /// <remarks>
    /// Bind-to-zero then release has a well-known race: another process can take
    /// the port between here and the daemon binding it. It is accepted rather than
    /// papered over — the failure surfaces as a start failure that names the port,
    /// and a retry picks a different one. A fixed port would be worse: 6767 is
    /// exactly where the user's own Paseo would be.
    /// </remarks>
    internal static int PickFreePort()
    {
        using var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        var port = ((IPEndPoint)listener.LocalEndpoint).Port;
        listener.Stop();
        return port;
    }

    private static string GeneratePassword() =>
        Convert.ToHexString(System.Security.Cryptography.RandomNumberGenerator.GetBytes(24));

    public async ValueTask DisposeAsync()
    {
        await StopAsync().ConfigureAwait(false);
        _watchdog?.Dispose();
        (_healthProbe as IDisposable)?.Dispose();
    }
}
