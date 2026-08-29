using System.Text;
using XTerm;
using XTerm.Input;
using XTerm.Options;

namespace LanAi.Workspace.Terminal;

public sealed class TerminalSession : IAsyncDisposable
{
    private readonly object _gate = new();
    private readonly Decoder _decoder = Encoding.UTF8.GetDecoder();
    private readonly XTerm.Terminal _terminal;
    private readonly KeyboardInputGenerator _keyboard;
    private PseudoConsoleProcess? _process;
    private bool _disposed;

    public TerminalSession(int columns = 120, int rows = 36)
    {
        _terminal = new XTerm.Terminal(new TerminalOptions
        {
            Cols = Math.Clamp(columns, 20, 300),
            Rows = Math.Clamp(rows, 8, 120),
            Scrollback = 5000,
            TermName = "xterm-256color"
        });
        _keyboard = new KeyboardInputGenerator(_terminal);
    }

    public event EventHandler? FrameChanged;
    public event EventHandler? Exited;

    public bool IsRunning => _process?.IsRunning == true;

    public async Task StartAsync(TerminalCommand command, CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        cancellationToken.ThrowIfCancellationRequested();
        if (_process is not null)
        {
            throw new InvalidOperationException("当前终端已经有正在运行的进程。 ");
        }

        _process = await PseudoConsoleProcess.StartAsync(
            command,
            _terminal.Cols,
            _terminal.Rows,
            cancellationToken);
        _process.OutputReceived += OnOutputReceived;
        _process.Exited += OnExited;
        _process.StartReading();
        FrameChanged?.Invoke(this, EventArgs.Empty);
    }

    public TerminalFrame CaptureFrame()
    {
        lock (_gate)
        {
            var buffer = _terminal.Buffer;
            var lines = new string[_terminal.Rows];
            for (var row = 0; row < _terminal.Rows; row++)
            {
                lines[row] = buffer.Lines[buffer.ViewportY + row]?.TranslateToString() ?? string.Empty;
            }

            var cursorRow = buffer.BaseY + buffer.Y - buffer.ViewportY;
            return new TerminalFrame(
                _terminal.Cols,
                _terminal.Rows,
                lines,
                buffer.X,
                cursorRow,
                _terminal.CursorVisible,
                _terminal.IsAlternateBufferActive,
                _terminal.Title,
                IsRunning);
        }
    }

    public ValueTask SendTextAsync(string text, CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrEmpty(text) || _process is null)
        {
            return ValueTask.CompletedTask;
        }

        return _process.WriteAsync(Encoding.UTF8.GetBytes(text), cancellationToken);
    }

    public ValueTask SendKeyAsync(
        TerminalInputKey key,
        TerminalInputModifiers modifiers = TerminalInputModifiers.None,
        CancellationToken cancellationToken = default)
    {
        string sequence;
        lock (_gate)
        {
            sequence = _keyboard.GenerateKeySequence(MapKey(key), MapModifiers(modifiers));
        }

        return SendTextAsync(sequence, cancellationToken);
    }

    public ValueTask SendCharacterAsync(
        char character,
        TerminalInputModifiers modifiers = TerminalInputModifiers.None,
        CancellationToken cancellationToken = default)
    {
        string sequence;
        lock (_gate)
        {
            sequence = _keyboard.GenerateCharSequence(character, MapModifiers(modifiers));
        }

        return SendTextAsync(sequence, cancellationToken);
    }

    public void Resize(int columns, int rows)
    {
        columns = Math.Clamp(columns, 20, 300);
        rows = Math.Clamp(rows, 8, 120);
        lock (_gate)
        {
            if (columns == _terminal.Cols && rows == _terminal.Rows)
            {
                return;
            }

            _terminal.Resize(columns, rows);
            _process?.Resize(columns, rows);
        }

        FrameChanged?.Invoke(this, EventArgs.Empty);
    }

    public void Scroll(int lines)
    {
        lock (_gate)
        {
            _terminal.Buffer.ScrollLines(lines);
        }

        FrameChanged?.Invoke(this, EventArgs.Empty);
    }

    public async Task StopAsync()
    {
        if (_process is null)
        {
            return;
        }

        await _process.StopAsync(TimeSpan.FromSeconds(1));
    }

    private void OnOutputReceived(object? sender, ReadOnlyMemory<byte> bytes)
    {
        lock (_gate)
        {
            var charCount = _decoder.GetCharCount(bytes.Span, flush: false);
            var characters = new char[charCount];
            _decoder.GetChars(bytes.Span, characters, flush: false);
            _terminal.Write(new string(characters));
        }

        FrameChanged?.Invoke(this, EventArgs.Empty);
    }

    private void OnExited(object? sender, EventArgs e)
    {
        FrameChanged?.Invoke(this, EventArgs.Empty);
        Exited?.Invoke(this, EventArgs.Empty);
    }

    private static Key MapKey(TerminalInputKey key) => key switch
    {
        TerminalInputKey.Enter => Key.Enter,
        TerminalInputKey.Tab => Key.Tab,
        TerminalInputKey.Backspace => Key.Backspace,
        TerminalInputKey.Escape => Key.Escape,
        TerminalInputKey.Up => Key.UpArrow,
        TerminalInputKey.Down => Key.DownArrow,
        TerminalInputKey.Left => Key.LeftArrow,
        TerminalInputKey.Right => Key.RightArrow,
        TerminalInputKey.Home => Key.Home,
        TerminalInputKey.End => Key.End,
        TerminalInputKey.PageUp => Key.PageUp,
        TerminalInputKey.PageDown => Key.PageDown,
        TerminalInputKey.Insert => Key.Insert,
        TerminalInputKey.Delete => Key.Delete,
        TerminalInputKey.F1 => Key.F1,
        TerminalInputKey.F2 => Key.F2,
        TerminalInputKey.F3 => Key.F3,
        TerminalInputKey.F4 => Key.F4,
        TerminalInputKey.F5 => Key.F5,
        TerminalInputKey.F6 => Key.F6,
        TerminalInputKey.F7 => Key.F7,
        TerminalInputKey.F8 => Key.F8,
        TerminalInputKey.F9 => Key.F9,
        TerminalInputKey.F10 => Key.F10,
        TerminalInputKey.F11 => Key.F11,
        TerminalInputKey.F12 => Key.F12,
        _ => Key.Escape
    };

    private static KeyModifiers MapModifiers(TerminalInputModifiers modifiers)
    {
        var result = KeyModifiers.None;
        if (modifiers.HasFlag(TerminalInputModifiers.Shift)) result |= KeyModifiers.Shift;
        if (modifiers.HasFlag(TerminalInputModifiers.Alt)) result |= KeyModifiers.Alt;
        if (modifiers.HasFlag(TerminalInputModifiers.Control)) result |= KeyModifiers.Control;
        return result;
    }

    public async ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        if (_process is not null)
        {
            _process.OutputReceived -= OnOutputReceived;
            _process.Exited -= OnExited;
            await _process.DisposeAsync();
            _process = null;
        }
    }
}
