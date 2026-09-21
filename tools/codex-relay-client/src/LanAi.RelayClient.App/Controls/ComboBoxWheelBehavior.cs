using Avalonia;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Interactivity;

namespace LanAi.RelayClient.Controls;

/// <summary>
/// Stops a mouse wheel passing over a closed <see cref="ComboBox"/> from changing its
/// selection.
/// </summary>
/// <remarks>
/// <see cref="ComboBox"/> answers <c>PointerWheelChanged</c> itself, on the bubble pass, once
/// it has focus — which it keeps after the user's last click on it, long after the pointer has
/// moved on. From then on, scrolling the page past it (it sits in a tall <c>ScrollViewer</c>)
/// silently changes a billing-relevant choice — the分组 the user is on, or which Claude group
/// the editor plug-ins use — with no click and no warning.
/// </remarks>
/// <example>
/// <c>&lt;ComboBox controls:ComboBoxWheelBehavior.DisableMouseWheel="True" .../&gt;</c>
/// </example>
public static class ComboBoxWheelBehavior
{
    public static readonly AttachedProperty<bool> DisableMouseWheelProperty =
        AvaloniaProperty.RegisterAttached<Control, bool>("DisableMouseWheel", typeof(ComboBoxWheelBehavior));

    static ComboBoxWheelBehavior() =>
        DisableMouseWheelProperty.Changed.AddClassHandler<Control>(OnDisableMouseWheelChanged);

    public static void SetDisableMouseWheel(Control element, bool value) =>
        element.SetValue(DisableMouseWheelProperty, value);

    public static bool GetDisableMouseWheel(Control element) =>
        element.GetValue(DisableMouseWheelProperty);

    private static void OnDisableMouseWheelChanged(Control control, AvaloniaPropertyChangedEventArgs args)
    {
        // Only ever added, never removed: nothing in this app turns the property back off
        // once set, and Avalonia has no RemoveHandler overload keyed by routing strategy to
        // undo it symmetrically.
        if (args.NewValue is true)
        {
            // The tunnel pass runs, target to root, before the bubble pass where ComboBox's
            // own class handler changes the selection — marking the event handled here is
            // what keeps that handler from ever seeing it. handledEventsToo is not needed:
            // this is the first thing to see the event, not something reacting after another
            // handler already claimed it.
            control.AddHandler(InputElement.PointerWheelChangedEvent, OnPointerWheelChanged, RoutingStrategies.Tunnel);
        }
    }

    private static void OnPointerWheelChanged(object? sender, PointerWheelEventArgs e) => e.Handled = true;
}
