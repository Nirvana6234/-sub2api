namespace LanAi.RelayClient.CodexBinding.DesktopSync;

/// <summary>Signals when a rollout grows, so its new lines can be read.</summary>
/// <remarks>
/// <para>
/// <see cref="FileSystemWatcher"/> gives the prompt signal but merges bursts: measured,
/// eleven appends raised seventeen events that saw only nine distinct lengths. That is
/// harmless because every signal is answered by reading to the end of the file. A slow
/// poll backs it up for the events a watcher can lose outright (buffer overflow, a
/// network or redirected profile).
/// </para>
/// <para>
/// The callback carries no data and may run on any thread, more than once per append,
/// and concurrently with itself; callers read from their own cursor and serialise.
/// </para>
/// </remarks>
public sealed class RolloutFollower : IDisposable
{
    public static readonly TimeSpan DefaultPollInterval = TimeSpan.FromSeconds(2);

    private readonly string _path;
    private readonly Action _changed;
    private readonly FileSystemWatcher? _watcher;
    private readonly Timer _poll;
    private long _lastLength;
    private int _disposed;

    public RolloutFollower(string path, Action changed, TimeSpan? pollInterval = null)
    {
        _path = path;
        _changed = changed;
        _lastLength = RolloutFile.LengthOf(path);

        try
        {
            _watcher = new FileSystemWatcher(Path.GetDirectoryName(path)!, Path.GetFileName(path))
            {
                NotifyFilter = NotifyFilters.Size | NotifyFilters.LastWrite,
            };
            _watcher.Changed += (_, _) => Signal();
            _watcher.Error += (_, _) => Signal();
            _watcher.EnableRaisingEvents = true;
        }
        catch (Exception exception) when (exception is ArgumentException or IOException or PlatformNotSupportedException)
        {
            // The poll alone still works, only slower.
            _watcher?.Dispose();
            _watcher = null;
        }

        TimeSpan interval = pollInterval ?? DefaultPollInterval;
        _poll = new Timer(_ => Poll(), null, interval, interval);
    }

    private void Poll()
    {
        long length = RolloutFile.LengthOf(_path);
        if (length != Interlocked.Exchange(ref _lastLength, length))
        {
            Signal();
        }
    }

    private void Signal()
    {
        if (Volatile.Read(ref _disposed) == 0)
        {
            _changed();
        }
    }

    public void Dispose()
    {
        if (Interlocked.Exchange(ref _disposed, 1) != 0)
        {
            return;
        }

        _watcher?.Dispose();
        _poll.Dispose();
    }
}
