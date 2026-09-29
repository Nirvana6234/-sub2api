using System.Text;
using System.Text.Json;
using LanAi.RelayClient.WeChatIntent;
using LanAi.RelayClient.WeChatReader;

// wechat-reader.exe — reads the open WeChat conversation off the screen for the client.
//
// Protocol (docs/WECHAT_INTENT_ASSISTANT.md §4.2): one JSON command per line on stdin, one
// JSON event per line on stdout. stderr carries diagnostics only (Diag.Log) — the client copies
// each line into logs\client.log — and must never contain recognised text: counts, durations,
// sizes, colours, window classes and error codes. The process ends when stdin closes, so it
// cannot outlive the client even where the job object (§4.6) is unavailable.
//
// `wechat-reader --probe` captures once and prints the detected layout and each line's
// geometry and character count — never the text — for tuning the layout rules.

Console.OutputEncoding = new UTF8Encoding(false);
Console.InputEncoding = new UTF8Encoding(false);

Diag.Log($"启动：pid {Environment.ProcessId}，系统 {Environment.OSVersion.Version}");
if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 19041))
{
    Diag.Log("系统版本低于 Windows 10 2004，退出");
    Emit(new ReaderEvent { Type = ReaderEvent.Error, Code = ReaderEvent.ErrorOsUnsupported, Message = "需要 Windows 10 2004 或更高版本" });
    return 2;
}

TextRecognizer? ocr = TextRecognizer.TryCreate();
if (ocr is null)
{
    Diag.Log("没有简体中文文字识别，退出");
    Emit(new ReaderEvent { Type = ReaderEvent.Error, Code = ReaderEvent.ErrorOcrLanguageMissing, Message = "Windows 没有安装简体中文文字识别" });
    return 3;
}

Diag.Log($"文字识别语言：{ocr.LanguageTag}");

WindowCapture capture;
try
{
    capture = new WindowCapture();
}
catch (Exception ex) when (ex is not OutOfMemoryException)
{
    Diag.Log($"创建截图设备失败：{Diag.Describe(ex)}");
    Emit(new ReaderEvent { Type = ReaderEvent.Error, Code = ReaderEvent.ErrorCaptureFailed, Message = $"创建截图设备失败：{ex.GetType().Name}" });
    return 4;
}

using var captureScope = capture;
var window = new WeChatWindow();

if (args.Contains("--probe"))
{
    return await Probe.RunAsync(window, capture, ocr);
}

var reader = new Reader(window, capture, ocr, Emit);
var input = new Thread(() =>
{
    try
    {
        string? line;
        while ((line = Console.In.ReadLine()) is not null)
        {
            ReaderCommand? command = null;
            try
            {
                command = JsonSerializer.Deserialize(line, ReaderJsonContext.Default.ReaderCommand);
            }
            catch (JsonException)
            {
            }

            if (command is not null)
            {
                reader.Accept(command);
            }
        }
    }
    catch (IOException)
    {
    }

    Diag.Log("标准输入已关闭，退出");
    reader.Quit();
})
{ IsBackground = true, Name = "stdin" };
input.Start();

Emit(new ReaderEvent { Type = ReaderEvent.Ready });
await reader.RunAsync();
return 0;

static void Emit(ReaderEvent e)
{
    string json = JsonSerializer.Serialize(e, ReaderJsonContext.Default.ReaderEvent);
    lock (Console.Out)
    {
        Console.Out.Write(json);
        Console.Out.Write('\n');
        Console.Out.Flush();
    }
}

namespace LanAi.RelayClient.WeChatReader
{
    /// <summary>The polling loop: window state every 200 ms, a capture whenever one is due.</summary>
    internal sealed class Reader(WeChatWindow window, WindowCapture capture, TextRecognizer ocr, Action<ReaderEvent> emit)
    {
        private static readonly TimeSpan Tick = TimeSpan.FromMilliseconds(200);
        private static readonly TimeSpan SettleInterval = TimeSpan.FromMilliseconds(250);

        /// <summary>
        /// An animated sticker keeps the picture from ever settling. After this long, read it
        /// anyway rather than never reading that conversation.
        /// </summary>
        private static readonly TimeSpan SettleGiveUp = TimeSpan.FromSeconds(3);

        private static readonly TimeSpan AliveInterval = TimeSpan.FromSeconds(10);
        private static readonly TimeSpan CaptureTimeout = TimeSpan.FromSeconds(3);
        private static readonly TimeSpan SummaryInterval = TimeSpan.FromSeconds(60);

        private readonly object _gate = new();
        private readonly StageClock _stage = new();
        private readonly Counters _counts = new();
        private DateTime _lastSummary = DateTime.UtcNow;
        private string? _loggedState;
        private IntPtr _loggedHwnd = new(-1);
        private string? _loggedLayoutProblem;
        private ChatLayout? _loggedLayout;
        private int _failuresInRow;
        private readonly CancellationTokenSource _quit = new();
        private bool _started;
        private bool _snapshotRequested;
        private TimeSpan _interval = TimeSpan.FromMilliseconds(1500);

        private string? _lastState;
        private (int X, int Y, int W, int H)? _lastBounds;
        private double _lastScale;
        private int? _lastFront;

        private DateTime _nextCapture = DateTime.MinValue;
        private ulong? _emittedHash;
        private ulong? _pendingHash;
        private DateTime _pendingSince;
        private DateTime _lastAlive = DateTime.MinValue;
        private string? _lastErrorCode;
        private long _seq;

        public void Accept(ReaderCommand command)
        {
            Diag.Log($"收到命令：{command.Cmd}{(command.IntervalMs is int every ? $"（间隔 {every}ms）" : string.Empty)}");
            lock (_gate)
            {
                switch (command.Cmd)
                {
                    case ReaderCommand.Start:
                        _started = true;
                        if (command.IntervalMs is int ms)
                        {
                            _interval = TimeSpan.FromMilliseconds(Math.Clamp(ms, 250, 60_000));
                        }

                        _nextCapture = DateTime.MinValue;
                        _emittedHash = null;
                        _pendingHash = null;
                        break;
                    case ReaderCommand.Snapshot:
                        _snapshotRequested = true;
                        break;
                    case ReaderCommand.Stop:
                        _started = false;
                        _pendingHash = null;
                        break;
                }
            }
        }

        public void Quit() => _quit.Cancel();

        public async Task RunAsync()
        {
            while (!_quit.IsCancellationRequested)
            {
                try
                {
                    await StepAsync().ConfigureAwait(false);
                }
                catch (Exception ex) when (ex is not OutOfMemoryException)
                {
                    // Never the text: only the exception's type reaches stderr.
                    _stage.Leave();
                    Diag.Log($"本轮出错：{Diag.Describe(ex)}");
                }

                try
                {
                    await Task.Delay(Tick, _quit.Token).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    break;
                }
            }

            _stage.Dispose();
        }

        private async Task StepAsync()
        {
            DateTime now = DateTime.UtcNow;
            if (now - _lastAlive >= AliveInterval)
            {
                _lastAlive = now;
                emit(new ReaderEvent { Type = ReaderEvent.Alive });
            }

            if (now - _lastSummary >= SummaryInterval)
            {
                LogSummary(now);
            }

            _stage.Enter("查找微信窗口");
            window.Refresh();
            string state = window.State();
            (int X, int Y, int W, int H)? bounds = window.Bounds();
            double scale = window.Scale();
            int? front = WeChatWindow.FrontPid();
            _stage.Leave();
            _counts.Steps++;
            if (state != _loggedState || window.Handle != _loggedHwnd)
            {
                _loggedState = state;
                _loggedHwnd = window.Handle;
                Diag.Log($"窗口状态：{state}，缩放 {scale:0.##}，微信窗口 {WeChatWindow.Describe(window.Handle)}"
                    + (state == ReaderEvent.StateForeground ? string.Empty : $"；前台是 {WeChatWindow.DescribeForeground()}"));
            }

            if (state != _lastState || bounds != _lastBounds || Math.Abs(scale - _lastScale) > 0.001 || front != _lastFront)
            {
                _lastState = state;
                _lastBounds = bounds;
                _lastScale = scale;
                _lastFront = front;
                emit(new ReaderEvent
                {
                    Type = ReaderEvent.Window,
                    State = state,
                    Rect = bounds is { } b ? new ReaderRect { X = b.X, Y = b.Y, W = b.W, H = b.H } : null,
                    Scale = scale,
                    ImageScale = 1.0,
                    FrontPid = front,
                });
            }

            bool started, snapshot;
            lock (_gate)
            {
                started = _started;
                snapshot = _snapshotRequested;
            }

            if (!started || state != ReaderEvent.StateForeground || (!snapshot && now < _nextCapture))
            {
                if (state != ReaderEvent.StateForeground)
                {
                    _pendingHash = null;
                    if (started)
                    {
                        _counts.NotInFront++;
                    }
                }

                return;
            }

            Pixels pixels;
            var watch = System.Diagnostics.Stopwatch.StartNew();
            try
            {
                pixels = await capture.CaptureAsync(window.Handle, CaptureTimeout, _stage).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is not OutOfMemoryException)
            {
                _stage.Leave();
                _counts.CaptureFailed++;
                _failuresInRow++;
                // Every one at first, then one in fifty: a capture that never works again would
                // otherwise write a line every 0.6 seconds.
                if (_failuresInRow <= 5 || _failuresInRow % 50 == 0)
                {
                    Diag.Log($"截图失败（连续第 {_failuresInRow} 次）：{Diag.Describe(ex)}，用时 {watch.ElapsedMilliseconds}ms，"
                        + $"{capture.LastAttempt}；微信窗口 {WeChatWindow.Describe(window.Handle)}");
                }

                ReportError(ReaderEvent.ErrorCaptureFailed, $"截图失败：{ex.GetType().Name}");
                _nextCapture = now + _interval;
                return;
            }

            _counts.Captured(watch.ElapsedMilliseconds);
            if (_failuresInRow > 0)
            {
                Diag.Log($"截图恢复：此前连续失败 {_failuresInRow} 次，这次 {watch.ElapsedMilliseconds}ms");
                _failuresInRow = 0;
            }

            _stage.Enter("识别聊天区域");
            ChatLayout? layout = ChatLayout.Detect(pixels, scale, out string problem);
            _stage.Leave();
            if (layout is null)
            {
                _counts.NoChatArea++;
                if (problem != _loggedLayoutProblem)
                {
                    _loggedLayoutProblem = problem;
                    _loggedLayout = null;
                    Diag.Log($"没有找到聊天区域：{problem}（截图 {pixels.Width}x{pixels.Height}，缩放 {scale:0.##}）");
                }

                ReportError(ReaderEvent.ErrorNoChatArea, "没有找到聊天区域");
                _nextCapture = now + _interval;
                lock (_gate)
                {
                    _snapshotRequested = false;
                }

                return;
            }

            _lastErrorCode = null;
            _loggedLayoutProblem = null;
            if (!layout.SameAs(_loggedLayout, scale))
            {
                _loggedLayout = layout;
                Diag.Log($"聊天区域：x={layout.Left}..{layout.Right}，标题 y={layout.HeaderTop}，消息区 y={layout.MessagesTop}..{layout.MessagesBottom}，"
                    + $"底色 {layout.Background}（截图 {pixels.Width}x{pixels.Height}，缩放 {scale:0.##}）");
            }

            ulong hash = layout.Hash(pixels, scale);

            if (snapshot)
            {
                lock (_gate)
                {
                    _snapshotRequested = false;
                }

                await EmitFrameAsync(pixels, layout, hash, scale).ConfigureAwait(false);
                _nextCapture = now + _interval;
                return;
            }

            if (hash == _emittedHash)
            {
                _counts.Unchanged++;
                _pendingHash = null;
                _nextCapture = now + _interval;
                return;
            }

            // Settled: the same picture twice in a row, or moving for longer than any scroll.
            if (hash == _pendingHash || (_pendingHash is not null && now - _pendingSince >= SettleGiveUp))
            {
                _pendingHash = null;
                await EmitFrameAsync(pixels, layout, hash, scale).ConfigureAwait(false);
                _nextCapture = now + _interval;
                return;
            }

            _counts.Settling++;
            if (_pendingHash is null)
            {
                _pendingSince = now;
                if (_emittedHash is not null)
                {
                    emit(new ReaderEvent { Type = ReaderEvent.Scrolling });
                }
            }

            _pendingHash = hash;
            _nextCapture = now + SettleInterval;
        }

        private async Task EmitFrameAsync(Pixels pixels, ChatLayout layout, ulong hash, double scale)
        {
            int width = layout.Right - layout.Left;
            var watch = System.Diagnostics.Stopwatch.StartNew();
            _stage.Enter("识别文字");
            (string title, string how) = await ReadTitleAsync(ocr, pixels, layout, scale).ConfigureAwait(false);
            List<ReaderLine> lines = await ocr.ReadAsync(pixels, layout.Left, layout.MessagesTop, width,
                layout.MessagesBottom - layout.MessagesTop + 1, scale).ConfigureAwait(false);
            _stage.Leave();

            _counts.Frames++;
            Diag.Log($"发出画面 #{_seq + 1}：标题 {title.Length} 字（{how}），消息区 {lines.Count} 行，识别 {watch.ElapsedMilliseconds}ms");
            _emittedHash = hash;
            emit(new ReaderEvent
            {
                Type = ReaderEvent.Frame,
                Seq = ++_seq,
                Hash = hash.ToString("x16"),
                Size = new ReaderRect { W = pixels.Width, H = pixels.Height },
                Area = new ReaderRect { X = layout.Left, Y = layout.MessagesTop, W = width, H = layout.MessagesBottom - layout.MessagesTop + 1 },
                Background = [layout.Background.R & ~3, layout.Background.G & ~3, layout.Background.B & ~3],
                Title = title,
                Lines = [.. lines],
            });
        }

        /// <summary>
        /// The title, read from its own text block (see <see cref="ChatLayout.FindTitle"/>), and
        /// read again at twice the size when that finds nothing. The second string says which, for
        /// the log.
        /// </summary>
        internal static async Task<(string Title, string How)> ReadTitleAsync(TextRecognizer ocr, Pixels pixels, ChatLayout layout, double scale)
        {
            if (layout.FindTitle(pixels, scale) is not { } area)
            {
                return (string.Empty, "标题栏里没有文字");
            }

            int pad = area.H + 4;
            int x = area.X - pad, w = area.W + (2 * pad);
            int y = Math.Max(area.ClearTop, area.Y - pad);
            int h = Math.Min(area.ClearBottom, area.Y + area.H - 1 + pad) - y + 1;
            List<ReaderLine> read = await ocr.ReadAsync(pixels, x, y, w, h, scale).ConfigureAwait(false);
            if (read.Count > 0)
            {
                return (read[0].Text, $"文字块 {area.W}x{area.H}");
            }

            read = await ocr.ReadAsync(pixels, x, y, w, h, scale, upscale: 2).ConfigureAwait(false);
            return read.Count > 0
                ? (read[0].Text, $"文字块 {area.W}x{area.H}，放大后认出")
                : (string.Empty, $"文字块 {area.W}x{area.H}，放大后也没认出");
        }

        /// <summary>What the last minute amounted to, so a quiet reader can be told from a stuck or a blind one.</summary>
        private void LogSummary(DateTime now)
        {
            bool started;
            lock (_gate)
            {
                started = _started;
            }

            Counters c = _counts;
            Diag.Log($"近 {(now - _lastSummary).TotalSeconds:0} 秒{(started ? string.Empty : "（未开始）")}：轮询 {c.Steps} 次，微信不在前台 {c.NotInFront} 次，"
                + $"截图成功 {c.CaptureOk} 次{(c.CaptureOk > 0 ? $"（平均 {c.CaptureMsTotal / c.CaptureOk}ms，最长 {c.CaptureMsMax}ms）" : string.Empty)}、失败 {c.CaptureFailed} 次，"
                + $"没找到聊天区域 {c.NoChatArea} 次，画面没变 {c.Unchanged} 次，等画面稳定 {c.Settling} 次，发出画面 {c.Frames} 个");
            _counts.Reset();
            _lastSummary = now;
        }

        private sealed class Counters
        {
            public int Steps;
            public int NotInFront;
            public int CaptureOk;
            public long CaptureMsTotal;
            public long CaptureMsMax;
            public int CaptureFailed;
            public int NoChatArea;
            public int Unchanged;
            public int Settling;
            public int Frames;

            public void Captured(long ms)
            {
                CaptureOk++;
                CaptureMsTotal += ms;
                CaptureMsMax = Math.Max(CaptureMsMax, ms);
            }

            public void Reset()
            {
                Steps = NotInFront = CaptureOk = CaptureFailed = NoChatArea = Unchanged = Settling = Frames = 0;
                CaptureMsTotal = CaptureMsMax = 0;
            }
        }

        /// <summary>Once per distinct problem, not once per tick.</summary>
        private void ReportError(string code, string message)
        {
            if (code == _lastErrorCode)
            {
                return;
            }

            _lastErrorCode = code;
            emit(new ReaderEvent { Type = ReaderEvent.Error, Code = code, Message = message });
        }
    }

    /// <summary>One capture, geometry only. For tuning <see cref="ChatLayout"/> without reading anyone's chat.</summary>
    internal static class Probe
    {
        public static async Task<int> RunAsync(WeChatWindow window, WindowCapture capture, TextRecognizer ocr)
        {
            for (int i = 0; i < 20 && window.Handle == IntPtr.Zero; i++)
            {
                window.Refresh();
                await Task.Delay(150).ConfigureAwait(false);
            }

            if (window.Handle == IntPtr.Zero)
            {
                Console.WriteLine("未找到微信主窗口");
                return 1;
            }

            double scale = window.Scale();
            var sw = System.Diagnostics.Stopwatch.StartNew();
            Pixels p = await capture.CaptureAsync(window.Handle, TimeSpan.FromSeconds(3)).ConfigureAwait(false);
            long captureMs = sw.ElapsedMilliseconds;
            Console.WriteLine($"状态 {window.State()}  窗口 {window.Bounds()}  缩放 {scale:0.##}  截图 {p.Width}x{p.Height} {captureMs}ms");

            ChatLayout? layout = ChatLayout.Detect(p, scale);
            if (layout is null)
            {
                Console.WriteLine("未识别到聊天区域");
                return 1;
            }

            Console.WriteLine($"聊天区 x={layout.Left}..{layout.Right}  标题 y={layout.HeaderTop}  消息区 y={layout.MessagesTop}..{layout.MessagesBottom}  底色 {layout.Background}");
            sw.Restart();
            (string title, string how) = await Reader.ReadTitleAsync(ocr, p, layout, scale).ConfigureAwait(false);
            List<ReaderLine> lines = await ocr.ReadAsync(p, layout.Left, layout.MessagesTop, layout.Right - layout.Left, layout.MessagesBottom - layout.MessagesTop + 1, scale).ConfigureAwait(false);
            Console.WriteLine($"识别 {sw.ElapsedMilliseconds}ms，标题 {title.Length} 字（{how}），消息区 {lines.Count} 行");
            foreach (ReaderLine line in lines)
            {
                Console.WriteLine($"  y={line.Y,5} x=[{line.X,5},{line.X + line.W,5}] h={line.H,3} 字数={line.Text.Length,3} 底色=({string.Join(",", line.Bg)})");
            }

            return 0;
        }
    }
}
