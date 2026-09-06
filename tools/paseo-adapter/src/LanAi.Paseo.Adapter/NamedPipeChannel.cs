using System.IO.Pipes;
using System.Text;

namespace LanAi.Paseo.Adapter;

/// <summary>
/// Named-pipe channel where <b>this side is the server</b> and the bridge dials in.
/// </summary>
/// <remarks>
/// <para>
/// The direction is a security decision, not an arbitrary one.
/// <see cref="PipeOptions.CurrentUserOnly"/> makes the operating system refuse
/// any connection from a different user, and it is available on the pipe
/// <i>server</i>. A pipe created from Node would not give us the same guarantee
/// without extra native work, and loopback TCP would give us none at all — any
/// local process could dial it.
/// </para>
/// <para>
/// The handshake token in <c>hello</c> is the second gate, not the first. It
/// exists to stop a same-user process that stumbles onto the pipe name, and to
/// make a stale bridge from a previous run fail loudly instead of half-working.
/// </para>
/// </remarks>
public sealed class NamedPipeChannel : IPaseoChannel
{
    private readonly string _pipeName;
    private readonly NamedPipeServerStream _stream;
    private readonly SemaphoreSlim _writeLock = new(1, 1);
    private CancellationTokenSource? _readLoopCancellation;
    private Task? _readLoop;
    private int _closedRaised;
    private int _disposeStarted;

    public NamedPipeChannel(string pipeName)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(pipeName);
        _pipeName = pipeName;
        _stream = new NamedPipeServerStream(
            pipeName,
            PipeDirection.InOut,
            maxNumberOfServerInstances: 1,
            PipeTransmissionMode.Byte,
            PipeOptions.Asynchronous | PipeOptions.CurrentUserOnly);
    }

    public event EventHandler<string>? LineReceived;

    public event EventHandler? Closed;

    /// <summary>Full pipe path to hand to the bridge process.</summary>
    public string PipePath => OperatingSystem.IsWindows() ? $@"\\.\pipe\{_pipeName}" : _pipeName;

    public async Task OpenAsync(CancellationToken cancellationToken)
    {
        await _stream.WaitForConnectionAsync(cancellationToken).ConfigureAwait(false);
        _readLoopCancellation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        _readLoop = Task.Run(() => ReadLoopAsync(_readLoopCancellation.Token), CancellationToken.None);
    }

    public async Task SendLineAsync(string line, CancellationToken cancellationToken)
    {
        var payload = Encoding.UTF8.GetBytes(line + "\n");
        await _writeLock.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await _stream.WriteAsync(payload, cancellationToken).ConfigureAwait(false);
            await _stream.FlushAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (Exception ex) when (ex is IOException or ObjectDisposedException)
        {
            RaiseClosedOnce();
            throw new PaseoAdapterException(
                PaseoErrorCode.TransportDown,
                "The bridge connection is gone",
                ex.Message);
        }
        finally
        {
            _writeLock.Release();
        }
    }

    private async Task ReadLoopAsync(CancellationToken cancellationToken)
    {
        var buffer = new byte[8192];
        var pending = new StringBuilder();
        try
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                var read = await _stream.ReadAsync(buffer, cancellationToken).ConfigureAwait(false);
                if (read == 0)
                {
                    break;
                }

                pending.Append(Encoding.UTF8.GetString(buffer, 0, read));
                DispatchCompleteLines(pending);
            }
        }
        catch (OperationCanceledException)
        {
            // Disposal path; not a failure.
        }
        catch (Exception ex) when (ex is IOException or ObjectDisposedException)
        {
            // The peer vanished. Closed below is what turns this into
            // TransportDown for every in-flight request.
        }
        finally
        {
            RaiseClosedOnce();
        }
    }

    private void DispatchCompleteLines(StringBuilder pending)
    {
        while (true)
        {
            var text = pending.ToString();
            var newline = text.IndexOf('\n');
            if (newline < 0)
            {
                return;
            }

            var line = text[..newline].Trim();
            pending.Clear();
            pending.Append(text[(newline + 1)..]);
            if (line.Length > 0)
            {
                LineReceived?.Invoke(this, line);
            }
        }
    }

    private void RaiseClosedOnce()
    {
        if (Interlocked.Exchange(ref _closedRaised, 1) == 0)
        {
            Closed?.Invoke(this, EventArgs.Empty);
        }
    }

    /// <remarks>
    /// Idempotent on purpose. <see cref="PaseoAdapterClient"/> takes ownership of
    /// the channel and disposes it, and callers routinely wrap the channel in
    /// <c>await using</c> as well — so a second dispose is a normal path, not a
    /// bug. Without the guard, the second call cancels an already-disposed
    /// <see cref="CancellationTokenSource"/> and throws during teardown, which is
    /// exactly where an exception is least useful.
    /// </remarks>
    public async ValueTask DisposeAsync()
    {
        if (Interlocked.Exchange(ref _disposeStarted, 1) == 1)
        {
            return;
        }

        _readLoopCancellation?.Cancel();
        try
        {
            if (_readLoop is not null)
            {
                await _readLoop.ConfigureAwait(false);
            }
        }
        catch
        {
            // Disposal must not throw over a read loop that is already unwinding.
        }

        _readLoopCancellation?.Dispose();
        await _stream.DisposeAsync().ConfigureAwait(false);
        _writeLock.Dispose();
        RaiseClosedOnce();
    }
}
