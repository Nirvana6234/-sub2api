using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;

namespace LanAi.RelayClient.App.Views;

/// <summary>Asks a yes/no question and returns the answer.</summary>
/// <remarks>
/// <para>
/// Avalonia has no <c>MessageBox</c>, so the WPF head's <c>MessageBoxButton.YesNo</c>
/// prompts need a real window here. The sibling of <see cref="NoticeDialog"/>, which
/// only reports.
/// </para>
/// <para>
/// Closing by the title bar counts as "no". A prompt that is dismissed rather than
/// answered must never be read as consent — the caller that matters here restarts
/// ChatGPT and discards whatever the user had in flight.
/// </para>
/// </remarks>
public partial class ConfirmDialog : Window
{
    private bool _confirmed;

    public ConfirmDialog()
    {
        InitializeComponent();
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    /// <summary>Shows the question over <paramref name="owner"/> and waits for an answer.</summary>
    internal static async Task<bool> AskAsync(Window owner, string message, string confirmLabel = "确定")
    {
        var dialog = new ConfirmDialog();
        dialog.FindControl<TextBlock>("MessageText")!.Text = message;
        dialog.FindControl<Button>("ConfirmButton")!.Content = confirmLabel;

        await dialog.ShowDialog(owner);
        return dialog._confirmed;
    }

    private void Confirm_OnClick(object? sender, RoutedEventArgs e)
    {
        _confirmed = true;
        Close();
    }

    private void Cancel_OnClick(object? sender, RoutedEventArgs e) => Close();
}
