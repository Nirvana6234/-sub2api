using Avalonia.Threading;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.App.Services;

/// <summary>Avalonia's <see cref="DispatcherTimer"/> behind <see cref="IUiTimer"/>.</summary>
/// <remarks>
/// Deliberately the same shape as the WPF implementation. Both frameworks name the
/// type <c>DispatcherTimer</c> and both deliver ticks on the UI thread, so this is a
/// namespace swap rather than a reimplementation — which is the whole reason the
/// abstraction is worth its size.
/// </remarks>
internal sealed class AvaloniaUiTimer : IUiTimer
{
    private readonly DispatcherTimer _timer;

    public AvaloniaUiTimer(TimeSpan interval, Action onTick)
    {
        ArgumentNullException.ThrowIfNull(onTick);

        _timer = new DispatcherTimer { Interval = interval };
        _timer.Tick += (_, _) => onTick();
    }

    public void Start() => _timer.Start();

    public void Stop() => _timer.Stop();

    public void Dispose() => _timer.Stop();
}
