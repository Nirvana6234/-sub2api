using System.Globalization;
using System.IO;
using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Input;
using System.Windows.Media;
using System.Windows.Threading;
using LanAi.Workspace.Terminal;

namespace LanAi.Workspace.Wpf.Controls;

public sealed class TerminalDimensionsChangedEventArgs : EventArgs
{
    public TerminalDimensionsChangedEventArgs(int columns, int rows)
    {
        Columns = columns;
        Rows = rows;
    }

    public int Columns { get; }

    public int Rows { get; }
}

public sealed class TerminalInputFailedEventArgs : EventArgs
{
    public TerminalInputFailedEventArgs(Exception exception) => Exception = exception;

    public Exception Exception { get; }
}

/// <summary>
/// Lightweight WPF renderer and input surface for TerminalSession. The PTY and
/// ANSI state remain in AiSwitch.Terminal; this control only draws immutable
/// frames and translates WPF keyboard, IME, resize and wheel events.
/// </summary>
public sealed class TerminalControl : FrameworkElement
{
    private const double HorizontalPadding = 18;
    private const double VerticalPadding = 15;
    private const double TerminalFontSize = 12.5;

    private static readonly FontFamily TerminalFontFamily = new(
        "Cascadia Mono, Cascadia Code, Consolas, Microsoft YaHei UI");
    private static readonly Typeface TerminalTypeface = new(
        TerminalFontFamily,
        FontStyles.Normal,
        FontWeights.Normal,
        FontStretches.Normal);
    private static readonly Brush ForegroundBrush = CreateBrush(0xE7, 0xE7, 0xEA);
    private static readonly Brush MutedBrush = CreateBrush(0x88, 0x88, 0x90);
    private static readonly Brush PromptBrush = CreateBrush(0x65, 0xD4, 0x86);
    private static readonly Brush CursorBrush = CreateBrush(0x64, 0xB5, 0xFF);

    private readonly DispatcherTimer _cursorTimer;
    private TerminalHost _host = TerminalHost.Shared;
    private TerminalFrame? _frame;
    private int _renderPending;
    private bool _eventsAttached;
    private bool _cursorVisible = true;
    private CellMetrics _lastMetrics;

    public TerminalControl()
    {
        Focusable = true;
        FocusVisualStyle = null;
        Cursor = Cursors.IBeam;
        SnapsToDevicePixels = true;
        ClipToBounds = true;
        InputMethod.SetIsInputMethodEnabled(this, true);

        _cursorTimer = new DispatcherTimer(DispatcherPriority.Background, Dispatcher)
        {
            Interval = TimeSpan.FromMilliseconds(530),
        };
        _cursorTimer.Tick += CursorTimer_OnTick;

        Loaded += TerminalControl_OnLoaded;
        Unloaded += TerminalControl_OnUnloaded;
        IsKeyboardFocusWithinChanged += TerminalControl_OnIsKeyboardFocusWithinChanged;
    }

    public event EventHandler<TerminalDimensionsChangedEventArgs>? DimensionsChanged;

    public event EventHandler<TerminalInputFailedEventArgs>? InputFailed;

    public TerminalHost Host
    {
        get => _host;
        set
        {
            ArgumentNullException.ThrowIfNull(value);
            if (ReferenceEquals(_host, value))
            {
                return;
            }

            DetachHostEvents();
            _host = value;
            _frame = value.CurrentFrame;
            if (IsLoaded)
            {
                AttachHostEvents();
                UpdateTerminalDimensions();
            }

            InvalidateVisual();
        }
    }

    public int Columns { get; private set; } = 120;

    public int Rows { get; private set; } = 36;

    public Task StartAsync(TerminalCommand command, CancellationToken cancellationToken = default)
    {
        UpdateTerminalDimensions();
        return Host.StartAsync(command, Columns, Rows, cancellationToken);
    }

    public Task StopAsync(CancellationToken cancellationToken = default)
        => Host.StopAsync(cancellationToken);

    protected override Size MeasureOverride(Size availableSize)
    {
        var width = double.IsInfinity(availableSize.Width) ? 720 : availableSize.Width;
        var height = double.IsInfinity(availableSize.Height) ? 360 : availableSize.Height;
        return new Size(Math.Max(320, width), Math.Max(180, height));
    }

    protected override void OnRender(DrawingContext drawingContext)
    {
        base.OnRender(drawingContext);

        var metrics = GetCellMetrics();
        _lastMetrics = metrics;
        TerminalFrame? frame = _frame ?? Host.CurrentFrame;
        if (frame is null)
        {
            DrawEmptyState(drawingContext, metrics.PixelsPerDip);
            return;
        }

        var lineCount = Math.Min(frame.Rows, frame.Lines.Count);
        for (var row = 0; row < lineCount; row++)
        {
            var line = frame.Lines[row] ?? string.Empty;
            if (line.Length == 0)
            {
                continue;
            }

            var formatted = CreateText(line, ForegroundBrush, metrics.PixelsPerDip);
            drawingContext.DrawText(
                formatted,
                new Point(HorizontalPadding, VerticalPadding + row * metrics.Height));
        }

        if (frame.CursorVisible &&
            _cursorVisible &&
            frame.CursorColumn >= 0 &&
            frame.CursorColumn < frame.Columns &&
            frame.CursorRow >= 0 &&
            frame.CursorRow < frame.Rows)
        {
            var cursorX = HorizontalPadding + frame.CursorColumn * metrics.Width;
            var cursorY = VerticalPadding + frame.CursorRow * metrics.Height + 1;
            drawingContext.DrawRoundedRectangle(
                CursorBrush,
                null,
                new Rect(cursorX, cursorY, 2, Math.Max(8, metrics.Height - 2)),
                1,
                1);
        }
    }

    protected override void OnRenderSizeChanged(SizeChangedInfo sizeInfo)
    {
        base.OnRenderSizeChanged(sizeInfo);
        UpdateTerminalDimensions();
    }

    protected override void OnMouseDown(MouseButtonEventArgs e)
    {
        base.OnMouseDown(e);
        Focus();
    }

    protected override void OnMouseWheel(MouseWheelEventArgs e)
    {
        base.OnMouseWheel(e);
        if ((_frame ?? Host.CurrentFrame) is null)
        {
            return;
        }

        var notches = e.Delta / Mouse.MouseWheelDeltaForOneLine;
        Host.Scroll(-notches * 3);
        e.Handled = true;
    }

    protected override async void OnPreviewKeyDown(KeyEventArgs e)
    {
        base.OnPreviewKeyDown(e);
        if (e.Handled || !Host.IsRunning)
        {
            return;
        }

        var key = e.Key switch
        {
            Key.System => e.SystemKey,
            Key.ImeProcessed => e.ImeProcessedKey,
            _ => e.Key,
        };
        var modifiers = GetTerminalModifiers();

        if (IsPasteGesture(key))
        {
            e.Handled = true;
            try
            {
                if (Clipboard.ContainsText())
                {
                    await SendInputAsync(() => Host.SendTextAsync(Clipboard.GetText())).ConfigureAwait(true);
                }
            }
            catch (ExternalException exception)
            {
                InputFailed?.Invoke(this, new TerminalInputFailedEventArgs(exception));
            }

            return;
        }

        TerminalInputKey? terminalKey = MapSpecialKey(key);
        if (terminalKey is not null)
        {
            e.Handled = true;
            await SendInputAsync(() => Host.SendKeyAsync(terminalKey.Value, modifiers)).ConfigureAwait(true);
            return;
        }

        var hasControl = Keyboard.Modifiers.HasFlag(ModifierKeys.Control);
        var hasAlt = Keyboard.Modifiers.HasFlag(ModifierKeys.Alt);
        if (hasControl == hasAlt || !TryGetShortcutCharacter(key, hasControl, out var character))
        {
            return;
        }

        e.Handled = true;
        await SendInputAsync(() => Host.SendCharacterAsync(character, modifiers)).ConfigureAwait(true);
    }

    protected override async void OnTextInput(TextCompositionEventArgs e)
    {
        base.OnTextInput(e);
        if (!Host.IsRunning || string.IsNullOrEmpty(e.Text))
        {
            return;
        }

        // WPF delivers completed IME compositions here as one Unicode string,
        // which lets Chinese input reach the UTF-8 PTY without per-char damage.
        e.Handled = true;
        await SendInputAsync(() => Host.SendTextAsync(e.Text)).ConfigureAwait(true);
    }

    private void TerminalControl_OnLoaded(object sender, RoutedEventArgs e)
    {
        AttachHostEvents();
        _frame = Host.CurrentFrame;
        UpdateTerminalDimensions();
        UpdateCursorTimer();
        InvalidateVisual();
    }

    private void TerminalControl_OnUnloaded(object sender, RoutedEventArgs e)
    {
        DetachHostEvents();
        _cursorTimer.Stop();
    }

    private void TerminalControl_OnIsKeyboardFocusWithinChanged(
        object sender,
        DependencyPropertyChangedEventArgs e)
    {
        _cursorVisible = true;
        UpdateCursorTimer();
        InvalidateVisual();
    }

    private void AttachHostEvents()
    {
        if (_eventsAttached)
        {
            return;
        }

        Host.FrameChanged += Host_OnFrameChanged;
        Host.StateChanged += Host_OnStateChanged;
        _eventsAttached = true;
    }

    private void DetachHostEvents()
    {
        if (!_eventsAttached)
        {
            return;
        }

        Host.FrameChanged -= Host_OnFrameChanged;
        Host.StateChanged -= Host_OnStateChanged;
        _eventsAttached = false;
    }

    private void Host_OnFrameChanged(object? sender, EventArgs e) => ScheduleFrameRefresh();

    private void Host_OnStateChanged(object? sender, TerminalHostStateChangedEventArgs e)
    {
        ScheduleFrameRefresh();
        if (!Dispatcher.HasShutdownStarted)
        {
            _ = Dispatcher.InvokeAsync(UpdateCursorTimer, DispatcherPriority.Background);
        }
    }

    private void ScheduleFrameRefresh()
    {
        if (Interlocked.Exchange(ref _renderPending, 1) != 0)
        {
            return;
        }

        try
        {
            _ = Dispatcher.InvokeAsync(() =>
            {
                Interlocked.Exchange(ref _renderPending, 0);
                _frame = Host.CurrentFrame;
                _cursorVisible = true;
                InvalidateVisual();
            }, DispatcherPriority.Render);
        }
        catch (InvalidOperationException)
        {
            Interlocked.Exchange(ref _renderPending, 0);
        }
    }

    private void CursorTimer_OnTick(object? sender, EventArgs e)
    {
        _cursorVisible = !_cursorVisible;
        InvalidateVisual();
    }

    private void UpdateCursorTimer()
    {
        if (IsKeyboardFocusWithin && Host.IsRunning && IsLoaded)
        {
            if (!_cursorTimer.IsEnabled)
            {
                _cursorTimer.Start();
            }
        }
        else
        {
            _cursorTimer.Stop();
            _cursorVisible = true;
        }
    }

    private void UpdateTerminalDimensions()
    {
        if (ActualWidth <= 0 || ActualHeight <= 0)
        {
            return;
        }

        var metrics = GetCellMetrics();
        var columns = Math.Clamp(
            (int)Math.Floor((ActualWidth - HorizontalPadding * 2) / metrics.Width),
            20,
            300);
        var rows = Math.Clamp(
            (int)Math.Floor((ActualHeight - VerticalPadding * 2) / metrics.Height),
            8,
            120);

        if (columns == Columns && rows == Rows)
        {
            return;
        }

        Columns = columns;
        Rows = rows;
        Host.Resize(columns, rows);
        DimensionsChanged?.Invoke(this, new TerminalDimensionsChangedEventArgs(columns, rows));
    }

    private async Task SendInputAsync(Func<ValueTask> send)
    {
        try
        {
            await send().ConfigureAwait(true);
        }
        catch (Exception exception) when (
            exception is IOException or ObjectDisposedException or InvalidOperationException)
        {
            InputFailed?.Invoke(this, new TerminalInputFailedEventArgs(exception));
        }
    }

    private CellMetrics GetCellMetrics()
    {
        var pixelsPerDip = VisualTreeHelper.GetDpi(this).PixelsPerDip;
        if (_lastMetrics.Width > 0 && Math.Abs(_lastMetrics.PixelsPerDip - pixelsPerDip) < 0.001)
        {
            return _lastMetrics;
        }

        var sample = CreateText("M", ForegroundBrush, pixelsPerDip);
        return new CellMetrics(
            Math.Max(6, sample.WidthIncludingTrailingWhitespace),
            Math.Ceiling(sample.Height + 2),
            pixelsPerDip);
    }

    private static FormattedText CreateText(string text, Brush brush, double pixelsPerDip)
        => new(
            text,
            CultureInfo.CurrentUICulture,
            FlowDirection.LeftToRight,
            TerminalTypeface,
            TerminalFontSize,
            brush,
            pixelsPerDip);

    private static void DrawEmptyState(DrawingContext drawingContext, double pixelsPerDip)
    {
        drawingContext.DrawText(
            CreateText("lan-ai workspace", PromptBrush, pixelsPerDip),
            new Point(28, 26));
        drawingContext.DrawText(
            CreateText("选择项目与官方 CLI 后启动终端", ForegroundBrush, pixelsPerDip),
            new Point(28, 54));
        drawingContext.DrawText(
            CreateText("支持中文输入、快捷键、滚轮回看与窗口缩放", MutedBrush, pixelsPerDip),
            new Point(28, 80));
    }

    private static TerminalInputKey? MapSpecialKey(Key key) => key switch
    {
        Key.Enter => TerminalInputKey.Enter,
        Key.Tab => TerminalInputKey.Tab,
        Key.Back => TerminalInputKey.Backspace,
        Key.Escape => TerminalInputKey.Escape,
        Key.Up => TerminalInputKey.Up,
        Key.Down => TerminalInputKey.Down,
        Key.Left => TerminalInputKey.Left,
        Key.Right => TerminalInputKey.Right,
        Key.Home => TerminalInputKey.Home,
        Key.End => TerminalInputKey.End,
        Key.PageUp => TerminalInputKey.PageUp,
        Key.PageDown => TerminalInputKey.PageDown,
        Key.Insert => TerminalInputKey.Insert,
        Key.Delete => TerminalInputKey.Delete,
        Key.F1 => TerminalInputKey.F1,
        Key.F2 => TerminalInputKey.F2,
        Key.F3 => TerminalInputKey.F3,
        Key.F4 => TerminalInputKey.F4,
        Key.F5 => TerminalInputKey.F5,
        Key.F6 => TerminalInputKey.F6,
        Key.F7 => TerminalInputKey.F7,
        Key.F8 => TerminalInputKey.F8,
        Key.F9 => TerminalInputKey.F9,
        Key.F10 => TerminalInputKey.F10,
        Key.F11 => TerminalInputKey.F11,
        Key.F12 => TerminalInputKey.F12,
        _ => null,
    };

    private static TerminalInputModifiers GetTerminalModifiers()
    {
        var result = TerminalInputModifiers.None;
        if (Keyboard.Modifiers.HasFlag(ModifierKeys.Shift)) result |= TerminalInputModifiers.Shift;
        if (Keyboard.Modifiers.HasFlag(ModifierKeys.Alt)) result |= TerminalInputModifiers.Alt;
        if (Keyboard.Modifiers.HasFlag(ModifierKeys.Control)) result |= TerminalInputModifiers.Control;
        return result;
    }

    private static bool IsPasteGesture(Key key)
    {
        var modifiers = Keyboard.Modifiers;
        return (key == Key.V &&
                modifiers.HasFlag(ModifierKeys.Control) &&
                modifiers.HasFlag(ModifierKeys.Shift)) ||
               (key == Key.Insert && modifiers.HasFlag(ModifierKeys.Shift));
    }

    private static bool TryGetShortcutCharacter(Key key, bool control, out char character)
    {
        if (key is >= Key.A and <= Key.Z)
        {
            character = (char)('a' + ((int)key - (int)Key.A));
            if (!control && Keyboard.Modifiers.HasFlag(ModifierKeys.Shift))
            {
                character = char.ToUpperInvariant(character);
            }

            return true;
        }

        if (key == Key.Space)
        {
            character = ' ';
            return true;
        }

        character = key switch
        {
            Key.OemOpenBrackets => '[',
            Key.OemCloseBrackets => ']',
            Key.OemBackslash => '\\',
            Key.OemMinus => '-',
            Key.OemPlus => '=',
            _ => '\0',
        };
        return character != '\0';
    }

    private static Brush CreateBrush(byte red, byte green, byte blue)
    {
        var brush = new SolidColorBrush(Color.FromRgb(red, green, blue));
        brush.Freeze();
        return brush;
    }

    private readonly record struct CellMetrics(double Width, double Height, double PixelsPerDip);
}
