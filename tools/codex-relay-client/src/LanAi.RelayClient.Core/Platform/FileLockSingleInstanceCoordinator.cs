using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform;

/// <summary>
/// Enforces one instance by holding an exclusive handle on a lock file.
/// </summary>
/// <remarks>
/// <para>
/// The portable half of single-instance. A named <see cref="Mutex"/> does not mean the
/// same thing on Unix — there it is emulated with a shared-memory file whose lifetime
/// and cleanup differ from a Windows kernel object — so macOS gets exclusion from a
/// file handle opened with <see cref="FileShare.None"/> instead, which behaves the
/// same way on every platform .NET runs on.
/// </para>
/// <para>
/// <b>Activation is not implemented.</b> <see cref="TryActivateExistingInstance"/>
/// always returns false: the "show the running window" signal needs a Unix domain
/// socket, and inventing half of one here would be worse than declining. The
/// consequence is bounded and visible — a second launch exits quietly instead of
/// raising the first window — whereas failing at exclusion would let two clients
/// write <c>~/.codex</c> at once.
/// </para>
/// <para>
/// The lock is released by the operating system when the process dies, so a crash or
/// a kill does not strand the slot. That is the main reason this is a held handle
/// rather than a pid file, which would need liveness checks and could be stale.
/// </para>
/// </remarks>
internal sealed class FileLockSingleInstanceCoordinator : ISingleInstanceCoordinator
{
    private readonly FileStream? _lock;
    private bool _disposed;

    /// <param name="lockFilePath">
    /// Absolute path to the lock file. It is created if missing and deliberately left
    /// behind on exit — the file's existence means nothing, only an open exclusive
    /// handle on it does.
    /// </param>
    public FileLockSingleInstanceCoordinator(string lockFilePath)
    {
        if (string.IsNullOrWhiteSpace(lockFilePath))
        {
            throw new ArgumentException("Value cannot be empty.", nameof(lockFilePath));
        }

        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(lockFilePath)!);
            _lock = new FileStream(
                lockFilePath,
                FileMode.OpenOrCreate,
                FileAccess.ReadWrite,
                FileShare.None);
            IsPrimary = true;
        }
        catch (IOException)
        {
            // Someone else holds it. This is the expected path for a second launch,
            // not an error worth logging as one.
            IsPrimary = false;
        }
        catch (UnauthorizedAccessException ex)
        {
            // A directory we cannot write to is not proof another instance is running.
            // Claiming primary is the safer failure: the user gets a working client
            // rather than one that refuses to start for a reason it cannot explain.
            ClientLog.Warning("无法建立单实例锁，按首个实例继续运行", ex);
            IsPrimary = true;
        }
    }

    public bool IsPrimary { get; }

    /// <remarks>Nothing to listen for while activation is unimplemented.</remarks>
    public void StartListening()
    {
    }

    /// <inheritdoc />
    /// <remarks>Always false — see the note on the class.</remarks>
    public bool TryActivateExistingInstance() => false;

    public Task StopListeningAsync() => Task.CompletedTask;

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _lock?.Dispose();
    }
}
