using System.Runtime.Versioning;

namespace LanAi.RelayClient.Services;

/// <summary>Coordinates one client process per Windows device.</summary>
/// <remarks>
/// Marked Windows-only rather than ported. Both halves of this are Windows
/// constructs: a named <see cref="Mutex"/> has different semantics on Unix (it is
/// backed by a shared-memory file rather than a kernel object), and
/// <see cref="EventWaitHandle.OpenExisting(string)"/> — how a second launch tells the
/// first one to show its window — has no Unix equivalent at all. macOS needs an
/// advisory lock on a lockfile plus a Unix domain socket for the activation signal,
/// which is new code rather than a translation, so it is deliberately left for the
/// platform pass instead of being half-solved here.
/// </remarks>
[SupportedOSPlatform("windows")]
internal sealed class SingleInstanceCoordinator : ISingleInstanceCoordinator
{
    private readonly string _eventName;
    private readonly Mutex _mutex;
    private readonly EventWaitHandle? _activationEvent;
    private readonly Action _activate;
    private readonly CancellationTokenSource _stop = new();
    private Task? _listener;
    private bool _disposed;

    public SingleInstanceCoordinator(string mutexName, string eventName, Action activate)
    {
        if (string.IsNullOrWhiteSpace(mutexName))
        {
            throw new ArgumentException("Value cannot be empty.", nameof(mutexName));
        }

        if (string.IsNullOrWhiteSpace(eventName))
        {
            throw new ArgumentException("Value cannot be empty.", nameof(eventName));
        }

        _activate = activate ?? throw new ArgumentNullException(nameof(activate));
        _eventName = eventName;
        _mutex = new Mutex(initiallyOwned: false, mutexName, out bool createdNew);
        IsPrimary = createdNew;

        if (IsPrimary)
        {
            try
            {
                _activationEvent = new EventWaitHandle(
                    initialState: false,
                    EventResetMode.AutoReset,
                    eventName);
            }
            catch
            {
                _mutex.Dispose();
                _stop.Dispose();
                throw;
            }
        }
    }

    public bool IsPrimary { get; }

    public void StartListening()
    {
        if (!IsPrimary || _activationEvent is null || _listener is not null)
        {
            return;
        }

        _listener = Task.Run(ListenAsync);
    }

    public bool TryActivateExistingInstance()
    {
        if (IsPrimary)
        {
            return false;
        }

        try
        {
            using EventWaitHandle activation = EventWaitHandle.OpenExisting(_eventName);
            return activation.Set();
        }
        catch (WaitHandleCannotBeOpenedException)
        {
            return false;
        }
        catch (UnauthorizedAccessException)
        {
            return false;
        }
    }

    private async Task ListenAsync()
    {
        while (!_stop.IsCancellationRequested)
        {
            bool signaled;
            try
            {
                signaled = _activationEvent!.WaitOne(TimeSpan.FromMilliseconds(250));
            }
            catch (ObjectDisposedException)
            {
                return;
            }

            if (signaled)
            {
                try
                {
                    _activate();
                }
                catch (Exception ex)
                {
                    ClientLog.Error("单实例激活回调失败，继续监听", ex);
                }
            }

            await Task.Yield();
        }
    }

    public async Task StopListeningAsync()
    {
        _stop.Cancel();
        if (_listener is not null)
        {
            await _listener.ConfigureAwait(false);
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        try
        {
            StopListeningAsync().GetAwaiter().GetResult();
        }
        finally
        {
            _activationEvent?.Dispose();
            _mutex.Dispose();
            _stop.Dispose();
        }
    }
}
