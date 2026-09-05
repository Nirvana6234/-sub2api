using System.Windows.Threading;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Services;

/// <summary>WPF's <see cref="DispatcherTimer"/> behind <see cref="IUiTimer"/>.</summary>
/// <remarks>
/// The behaviour is unchanged from what <c>RegistrationViewModel</c> constructed
/// inline before the view model moved to the shared project; only ownership moved.
/// </remarks>
internal sealed class DispatcherUiTimer : IUiTimer
{
    private readonly DispatcherTimer _timer;

    public DispatcherUiTimer(TimeSpan interval, Action onTick)
    {
        ArgumentNullException.ThrowIfNull(onTick);

        _timer = new DispatcherTimer { Interval = interval };
        _timer.Tick += (_, _) => onTick();
    }

    public void Start() => _timer.Start();

    public void Stop() => _timer.Stop();

    public void Dispose() => _timer.Stop();
}
