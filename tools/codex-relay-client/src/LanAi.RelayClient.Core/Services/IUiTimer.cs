namespace LanAi.RelayClient.Services;

/// <summary>A repeating timer whose callback is delivered on the UI thread.</summary>
/// <remarks>
/// <para>
/// Exists so a view model can run a countdown without naming a UI framework.
/// <c>System.Windows.Threading.DispatcherTimer</c> was the last WPF type left in the
/// view-model layer, and it is the reason <see cref="ViewModels.RegistrationViewModel"/>
/// could not move to this project with the other six.
/// </para>
/// <para>
/// <b>The UI thread guarantee is the point, not a detail.</b> The callback sets
/// observable properties, and a property change raised from a threadpool thread
/// pushes a binding update off-thread — which WPF and Avalonia both answer with an
/// exception from somewhere unrelated, or worse, a torn read. A bare
/// <see cref="System.Threading.Timer"/> or <c>PeriodicTimer</c> does not satisfy this
/// contract; each head supplies its own dispatcher-backed implementation.
/// </para>
/// </remarks>
internal interface IUiTimer : IDisposable
{
    void Start();

    void Stop();
}

/// <summary>Creates a UI-thread timer that runs <paramref name="onTick"/> repeatedly.</summary>
/// <remarks>
/// A delegate rather than an interface: there is exactly one method, and every head
/// supplies it as a lambda over its own dispatcher.
/// </remarks>
/// <param name="interval">How often to fire, once started.</param>
/// <param name="onTick">Invoked on the UI thread on each tick.</param>
internal delegate IUiTimer UiTimerFactory(TimeSpan interval, Action onTick);
