namespace LanAi.RelayClient.WeChatReader;

/// <summary>
/// Diagnostic lines on stderr. The client copies each into its own log (<c>logs\client.log</c>),
/// so a problem on someone else's machine can be read back from the file they send.
/// </summary>
/// <remarks>
/// Counts, durations, sizes, colours, window class names and error codes only — never recognised
/// text, not even a conversation title (docs §3: the log carries no chat content).
/// </remarks>
internal static class Diag
{
    public static void Log(string message)
    {
        lock (Console.Error)
        {
            Console.Error.WriteLine(message);
            Console.Error.Flush();
        }
    }

    /// <summary>The exception's type and HRESULT; its message can quote nothing of ours, but is long and localised.</summary>
    public static string Describe(Exception ex) =>
        ex.InnerException is { } inner
            ? $"{ex.GetType().Name} 0x{ex.HResult:X8}（内层 {inner.GetType().Name} 0x{inner.HResult:X8}）"
            : $"{ex.GetType().Name} 0x{ex.HResult:X8}";
}

/// <summary>
/// Which step the reader is in, and for how long. A step that does not come back — a capture call
/// the system never answers — otherwise shows only as the reader going quiet until the client's
/// 30-second watchdog kills it, with nothing saying where it stopped.
/// </summary>
internal sealed class StageClock : IDisposable
{
    private static readonly TimeSpan FirstReport = TimeSpan.FromSeconds(5);

    private readonly object _gate = new();
    private readonly Timer _timer;
    private string? _stage;
    private DateTime _since;
    private TimeSpan _reported;

    public StageClock() => _timer = new Timer(_ => Check(), null, TimeSpan.FromSeconds(1), TimeSpan.FromSeconds(1));

    public void Enter(string stage)
    {
        lock (_gate)
        {
            EndLocked();
            _stage = stage;
            _since = DateTime.UtcNow;
            _reported = TimeSpan.Zero;
        }
    }

    public void Leave()
    {
        lock (_gate)
        {
            EndLocked();
            _stage = null;
        }
    }

    public void Dispose() => _timer.Dispose();

    private void EndLocked()
    {
        if (_stage is not null && _reported > TimeSpan.Zero)
        {
            Diag.Log($"「{_stage}」结束，共用时 {(DateTime.UtcNow - _since).TotalMilliseconds:0}ms");
        }
    }

    /// <summary>At 5 s, then each time the wait has doubled.</summary>
    private void Check()
    {
        lock (_gate)
        {
            if (_stage is null)
            {
                return;
            }

            TimeSpan waited = DateTime.UtcNow - _since;
            TimeSpan next = _reported == TimeSpan.Zero ? FirstReport : _reported * 2;
            if (waited >= next)
            {
                _reported = waited;
                Diag.Log($"卡住：「{_stage}」已 {waited.TotalSeconds:0} 秒没有返回");
            }
        }
    }
}
