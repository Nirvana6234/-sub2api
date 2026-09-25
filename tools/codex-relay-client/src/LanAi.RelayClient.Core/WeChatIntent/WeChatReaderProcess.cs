using System.Diagnostics;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;

namespace LanAi.RelayClient.WeChatIntent;

/// <summary>The screen reader, as the view model sees it. Faked in tests.</summary>
internal interface IWeChatReader : IDisposable
{
    /// <summary>An event from the reader. Raised on a background thread.</summary>
    event Action<ReaderEvent>? EventReceived;

    /// <summary>The reader stopped for good after repeated crashes. Raised on a background thread.</summary>
    event Action<string>? Failed;

    bool IsRunning { get; }

    void Start();

    void Stop();

    /// <summary>「分析」: read the screen now, settled or not.</summary>
    void Snapshot();
}

/// <summary>Runs <c>wechat-reader.exe</c> (docs §4.6).</summary>
/// <remarks>
/// <list type="bullet">
/// <item>Put in the same kill-on-close job object as the context filter, so a crash of the client
/// cannot leave it running — which would also make the self-updater's copy over the install
/// directory fail on the locked exe. It also exits by itself when its stdin closes.</item>
/// <item>Restarted after an unexpected exit, 1 s, then 5 s, then 30 s later; a fourth failure in
/// a row gives up and reports <see cref="Failed"/>. A run that lasted a minute resets the count.</item>
/// <item>Declared hung when nothing, not even its 10-second <c>alive</c>, arrives for 30 seconds.</item>
/// </list>
/// Nothing the reader prints is logged except event types and error codes: its frames are the
/// user's conversation.
/// </remarks>
internal sealed class WeChatReaderProcess : IWeChatReader
{
    private static readonly TimeSpan[] Backoff = [TimeSpan.FromSeconds(1), TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(30)];
    private static readonly TimeSpan HealthyRun = TimeSpan.FromMinutes(1);
    private static readonly TimeSpan HangAfter = TimeSpan.FromSeconds(30);
    /// <summary>
    /// How often a still window is looked at again. 600 ms, not the 1.5 s of the first version:
    /// the inline cards are pinned to bubbles, and a scroll has to be noticed quickly enough that
    /// they are cleared before they point at the wrong messages.
    /// </summary>
    private const int IntervalMs = 600;

    private readonly string _exePath;
    private readonly object _gate = new();
    private readonly Timer _watchdog;
    private Process? _process;
    private bool _wanted;
    private int _failures;
    private DateTime _startedAt;
    private DateTime _lastHeard;
    private Timer? _restart;

    public WeChatReaderProcess(string exePath)
    {
        _exePath = exePath ?? throw new ArgumentNullException(nameof(exePath));
        _watchdog = new Timer(_ => CheckHung(), null, Timeout.Infinite, Timeout.Infinite);
    }

    public event Action<ReaderEvent>? EventReceived;

    public event Action<string>? Failed;

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

    public void Start()
    {
        lock (_gate)
        {
            // Already running, or waiting out a restart back-off: leave it alone. Resetting the
            // failure count here would let repeated calls defeat the give-up after three crashes.
            if (_wanted && (_process is not null || _restart is not null))
            {
                return;
            }

            _wanted = true;
            _failures = 0;
            Launch();
        }
    }

    public void Stop()
    {
        Process? process;
        lock (_gate)
        {
            _wanted = false;
            _restart?.Dispose();
            _restart = null;
            _watchdog.Change(Timeout.Infinite, Timeout.Infinite);

            // Taken out under the lock, ended outside it: waiting for the exit while holding the
            // gate could deadlock against OnExited, which takes the gate from the Exited callback.
            process = _process;
            _process = null;
        }

        if (process is not null)
        {
            End(process);
            process.Dispose();
        }
    }

    public void Snapshot() => Send(new ReaderCommand { Cmd = ReaderCommand.Snapshot });

    public void Dispose()
    {
        Stop();
        _watchdog.Dispose();
    }

    private void Launch()
    {
        var info = new ProcessStartInfo(_exePath)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            StandardOutputEncoding = new UTF8Encoding(false),
            StandardErrorEncoding = new UTF8Encoding(false),
            StandardInputEncoding = new UTF8Encoding(false),
            WorkingDirectory = Path.GetDirectoryName(_exePath) ?? AppContext.BaseDirectory,
        };

        Process process;
        try
        {
            process = Process.Start(info) ?? throw new InvalidOperationException("进程未启动");
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException or IOException)
        {
            ClientLog.Warning("微信读取组件启动失败", ex);
            ScheduleRestart();
            return;
        }

        ChildProcessJob.TryAdopt(process, "微信读取组件");
        process.EnableRaisingEvents = true;
        process.Exited += (_, _) => OnExited(process);
        _process = process;
        _startedAt = DateTime.UtcNow;
        _lastHeard = _startedAt;
        _watchdog.Change(TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(5));

        new Thread(() => ReadEvents(process)) { IsBackground = true, Name = "wechat-reader stdout" }.Start();
        new Thread(() => DrainErrors(process)) { IsBackground = true, Name = "wechat-reader stderr" }.Start();

        WriteLine(process, new ReaderCommand { Cmd = ReaderCommand.Start, IntervalMs = IntervalMs });
        ClientLog.Info("微信读取组件已启动");
    }

    private void Send(ReaderCommand command)
    {
        lock (_gate)
        {
            if (_process is { HasExited: false } process)
            {
                WriteLine(process, command);
            }
        }
    }

    private static void WriteLine(Process process, ReaderCommand command)
    {
        try
        {
            process.StandardInput.WriteLine(JsonSerializer.Serialize(command, ReaderJsonContext.Default.ReaderCommand));
            process.StandardInput.Flush();
        }
        catch (Exception ex) when (ex is IOException or InvalidOperationException or ObjectDisposedException)
        {
            // It is exiting; OnExited decides what happens next.
        }
    }

    private void ReadEvents(Process process)
    {
        try
        {
            string? line;
            while ((line = process.StandardOutput.ReadLine()) is not null)
            {
                ReaderEvent? e;
                try
                {
                    e = JsonSerializer.Deserialize(line, ReaderJsonContext.Default.ReaderEvent);
                }
                catch (JsonException)
                {
                    continue;
                }

                if (e is null)
                {
                    continue;
                }

                lock (_gate)
                {
                    _lastHeard = DateTime.UtcNow;
                }

                if (e.Type == ReaderEvent.Error)
                {
                    // The code and the reader's own message, which never contains recognised text.
                    ClientLog.Warning($"微信读取组件：{e.Code} {e.Message}");
                }

                EventReceived?.Invoke(e);
            }
        }
        catch (Exception ex) when (ex is IOException or InvalidOperationException or ObjectDisposedException)
        {
        }
    }

    private static void DrainErrors(Process process)
    {
        try
        {
            string? line;
            while ((line = process.StandardError.ReadLine()) is not null)
            {
                // The reader writes only exception type names here (see its Program.cs).
                ClientLog.Warning($"微信读取组件 stderr：{(line.Length > 120 ? line[..120] : line)}");
            }
        }
        catch (Exception ex) when (ex is IOException or InvalidOperationException or ObjectDisposedException)
        {
        }
    }

    private void OnExited(Process process)
    {
        lock (_gate)
        {
            if (!ReferenceEquals(process, _process))
            {
                return;
            }

            int code = SafeExitCode(process);
            process.Dispose();
            _process = null;
            _watchdog.Change(Timeout.Infinite, Timeout.Infinite);
            if (!_wanted)
            {
                return;
            }

            ClientLog.Warning($"微信读取组件意外退出（{code}）");
            if (DateTime.UtcNow - _startedAt >= HealthyRun)
            {
                _failures = 0;
            }

            ScheduleRestart();
        }
    }

    /// <summary>Called with the gate held.</summary>
    private void ScheduleRestart()
    {
        if (_failures >= Backoff.Length)
        {
            _wanted = false;
            ClientLog.Warning("微信读取组件连续失败，已停止");
            ThreadPool.QueueUserWorkItem(_ => Failed?.Invoke("微信读取组件异常，已停止。重新打开开关可以再试一次。"));
            return;
        }

        TimeSpan wait = Backoff[_failures++];
        _restart?.Dispose();
        _restart = new Timer(_ =>
        {
            lock (_gate)
            {
                _restart?.Dispose();
                _restart = null;
                if (_wanted && _process is null)
                {
                    Launch();
                }
            }
        }, null, wait, Timeout.InfiniteTimeSpan);
    }

    private void CheckHung()
    {
        Process? hung = null;
        lock (_gate)
        {
            if (_process is { HasExited: false } && DateTime.UtcNow - _lastHeard > HangAfter)
            {
                hung = _process;
            }
        }

        if (hung is not null)
        {
            // Still the current process, so its exit goes through OnExited and is restarted.
            ClientLog.Warning("微信读取组件无响应，重启");
            End(hung);
        }
    }

    /// <summary>Closing stdin asks it to leave; the kill makes sure. Never called with the gate held.</summary>
    private static void End(Process process)
    {
        try
        {
            process.StandardInput.Close();
            if (!process.WaitForExit(500))
            {
                process.Kill(entireProcessTree: true);
            }
        }
        catch (Exception ex) when (ex is InvalidOperationException or System.ComponentModel.Win32Exception or IOException)
        {
        }
    }

    private static int SafeExitCode(Process process)
    {
        try
        {
            return process.ExitCode;
        }
        catch (InvalidOperationException)
        {
            return -1;
        }
    }
}
