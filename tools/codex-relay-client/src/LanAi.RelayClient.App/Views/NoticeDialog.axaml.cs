using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.App.Views;

/// <summary>
/// Tells the user an operation failed and where the details went.
/// </summary>
/// <remarks>
/// <para>
/// Avalonia has no <c>MessageBox</c>, so the WPF head's failure notice needs an actual
/// window here. It is worth the thirty lines: every click handler runs through
/// <see cref="SafeAsyncRunner"/>, which logs but does not surface, and without this
/// an unexpected fault leaves the user pressing 登录 with nothing happening at all —
/// the "点了没反应" report that carries no information back.
/// </para>
/// <para>
/// States plainly that a log exists and where, because the user is the one who will
/// have to retrieve it. The exception text is included but secondary: it means
/// nothing to them and everything to whoever reads the report.
/// </para>
/// </remarks>
public partial class NoticeDialog : Window
{
    public NoticeDialog()
    {
        InitializeComponent();
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    /// <summary>Shows the failure notice over <paramref name="owner"/>.</summary>
    internal static Task ShowFailureAsync(Window owner, Exception exception)
    {
        var dialog = new NoticeDialog();
        dialog.FindControl<TextBlock>("MessageText")!.Text =
            $"操作出错了，客户端仍在运行。\n\n{exception.Message}\n\n详细信息已记录到：\n{ClientLog.FilePath}";

        return dialog.ShowDialog(owner);
    }

    /// <summary>Shows a plain message over <paramref name="owner"/>.</summary>
    internal static Task ShowNoticeAsync(Window owner, string message)
    {
        var dialog = new NoticeDialog();
        dialog.FindControl<TextBlock>("MessageText")!.Text = message;
        return dialog.ShowDialog(owner);
    }

    private void Close_OnClick(object? sender, RoutedEventArgs e) => Close();
}
