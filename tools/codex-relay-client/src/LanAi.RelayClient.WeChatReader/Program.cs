using System.Text;
using System.Text.Json;
using LanAi.RelayClient.WeChatIntent;
using LanAi.RelayClient.WeChatReader;

// wechat-reader.exe — reads the open WeChat conversation off the screen for the client.
//
// Protocol (docs/WECHAT_INTENT_ASSISTANT.md §4.2): one JSON command per line on stdin, one
// JSON event per line on stdout. stderr carries diagnostics only and must never contain
// recognised text. The process ends when stdin closes, so it cannot outlive the client even
// where the job object (§4.6) is unavailable.
//
// `wechat-reader --probe` captures once and prints the detected layout and each line's
// geometry and character count — never the text — for tuning the layout rules.

Console.OutputEncoding = new UTF8Encoding(false);
Console.InputEncoding = new UTF8Encoding(false);

if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 19041))
{
    Emit(new ReaderEvent { Type = ReaderEvent.Error, Code = ReaderEvent.ErrorOsUnsupported, Message = "需要 Windows 10 2004 或更高版本" });
    return 2;
}

TextRecognizer? ocr = TextRecognizer.TryCreate();
if (ocr is null)
{
    Emit(new ReaderEvent { Type = ReaderEvent.Error, Code = ReaderEvent.ErrorOcrLanguageMissing, Message = "Windows 没有安装简体中文文字识别" });
    return 3;
}

using var capture = new WindowCapture();
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

        private readonly object _gate = new();
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
                    Console.Error.WriteLine($"step failed: {ex.GetType().Name}");
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
        }

        private async Task StepAsync()
        {
            DateTime now = DateTime.UtcNow;
            if (now - _lastAlive >= AliveInterval)
            {
                _lastAlive = now;
                emit(new ReaderEvent { Type = ReaderEvent.Alive });
            }

            window.Refresh();
            string state = window.State();
            (int X, int Y, int W, int H)? bounds = window.Bounds();
            double scale = window.Scale();
            int? front = WeChatWindow.FrontPid();
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
                }

                return;
            }

            Pixels pixels;
            try
            {
                pixels = await capture.CaptureAsync(window.Handle, CaptureTimeout).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is not OutOfMemoryException)
            {
                ReportError(ReaderEvent.ErrorCaptureFailed, $"截图失败：{ex.GetType().Name}");
                _nextCapture = now + _interval;
                return;
            }

            ChatLayout? layout = ChatLayout.Detect(pixels, scale);
            if (layout is null)
            {
                ReportError(ReaderEvent.ErrorNoChatArea, "没有找到聊天区域");
                _nextCapture = now + _interval;
                lock (_gate)
                {
                    _snapshotRequested = false;
                }

                return;
            }

            _lastErrorCode = null;
            ulong hash = layout.Hash(pixels);

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
            List<ReaderLine> title = await ocr.ReadAsync(pixels, layout.Left, layout.HeaderTop, width,
                layout.MessagesTop - layout.HeaderTop, scale).ConfigureAwait(false);
            List<ReaderLine> lines = await ocr.ReadAsync(pixels, layout.Left, layout.MessagesTop, width,
                layout.MessagesBottom - layout.MessagesTop + 1, scale).ConfigureAwait(false);

            _emittedHash = hash;
            emit(new ReaderEvent
            {
                Type = ReaderEvent.Frame,
                Seq = ++_seq,
                Hash = hash.ToString("x16"),
                Size = new ReaderRect { W = pixels.Width, H = pixels.Height },
                Area = new ReaderRect { X = layout.Left, Y = layout.MessagesTop, W = width, H = layout.MessagesBottom - layout.MessagesTop + 1 },
                Background = [layout.Background.R & ~3, layout.Background.G & ~3, layout.Background.B & ~3],
                Title = title.Count > 0 ? title[0].Text : string.Empty,
                Lines = [.. lines],
            });
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
            List<ReaderLine> title = await ocr.ReadAsync(p, layout.Left, layout.HeaderTop, layout.Right - layout.Left, layout.MessagesTop - layout.HeaderTop, scale).ConfigureAwait(false);
            List<ReaderLine> lines = await ocr.ReadAsync(p, layout.Left, layout.MessagesTop, layout.Right - layout.Left, layout.MessagesBottom - layout.MessagesTop + 1, scale).ConfigureAwait(false);
            Console.WriteLine($"识别 {sw.ElapsedMilliseconds}ms，标题 {(title.Count > 0 ? title[0].Text.Length : 0)} 字，消息区 {lines.Count} 行");
            foreach (ReaderLine line in lines)
            {
                Console.WriteLine($"  y={line.Y,5} x=[{line.X,5},{line.X + line.W,5}] h={line.H,3} 字数={line.Text.Length,3} 底色=({string.Join(",", line.Bg)})");
            }

            return 0;
        }
    }
}
