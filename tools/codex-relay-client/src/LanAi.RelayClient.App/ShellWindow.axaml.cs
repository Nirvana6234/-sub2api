using Avalonia.Controls;
using Avalonia.Markup.Xaml;

namespace LanAi.RelayClient.App;

/// <summary>Hosts whichever surface the client is currently showing.</summary>
/// <remarks>
/// <para>
/// Closing hides rather than exits, matching the WPF window. That is not a nicety: the
/// panel tells the user 保持客户端运行，ChatGPT 才能继续使用共飞额度, and with Avalonia's
/// default shutdown mode the close button would end the process and take the relay with
/// it — the window contradicting its own instruction.
/// </para>
/// <para>
/// The close button therefore never exits: the tray's 退出 entry (which sets
/// <see cref="ExitRequested"/>) and the panel's 退出 do. A quit asked of the application
/// rather than of this window — ⌘Q, Dock ▸ Quit, the OS shutting down — is let through,
/// since it is not the close button and nothing else would honour it. If no tray could be
/// created the close button falls back to a real exit, because a window that cannot be
/// closed and has nowhere to hide is worse than one that quits.
/// </para>
/// </remarks>
public partial class ShellWindow : Window
{
    public ShellWindow()
    {
        InitializeComponent();
        Closing += ShellWindow_OnClosing;
    }

    /// <summary>Set by the tray's exit entry to allow the window to close for real.</summary>
    internal bool ExitRequested { get; set; }

    /// <summary>Whether a tray exists to hide into.</summary>
    internal bool HasTray { get; set; }

    /// <summary>Invoked the first time the window hides, to explain that it is still running.</summary>
    internal Action? OnFirstHide { get; set; }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    /// <summary>Replaces the visible surface.</summary>
    internal void Show(Control surface) =>
        this.FindControl<ContentControl>("Surface")!.Content = surface;

    /// <summary>Brings the window back from the tray.</summary>
    internal void RestoreFromTray()
    {
        Show();
        WindowState = WindowState.Normal;
        Activate();
    }

    private void ShellWindow_OnClosing(object? sender, WindowClosingEventArgs e)
    {
        // The application itself is being asked to quit — ⌘Q, Dock ▸ Quit, an Apple Event quit (the
        // installer's way of replacing a running copy), or the OS shutting down — not the user
        // pressing this window's close button. Hiding then leaves a process that cannot be quit
        // from anywhere but the tray. The cleanup (config restored, key released) runs in
        // App.OnExit, which this lets happen.
        if (e.CloseReason is WindowCloseReason.ApplicationShutdown or WindowCloseReason.OSShutdown)
        {
            return;
        }

        if (ExitRequested || !HasTray)
        {
            return;
        }

        e.Cancel = true;
        Hide();
        OnFirstHide?.Invoke();
    }
}
