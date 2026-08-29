using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.App.Views;

/// <summary>What the user chose when they pressed 退出.</summary>
internal enum ExitChoice
{
    /// <summary>Dismissed without choosing. Nothing happens.</summary>
    None,

    /// <summary>Quit the client entirely and end the process.</summary>
    FullExit,

    /// <summary>Hide the window; the client keeps relaying.</summary>
    MinimizeToTray,

    /// <summary>Sign out of the account but keep the client running.</summary>
    SignOut,
}

/// <summary>
/// Asks what 退出 should mean.
/// </summary>
/// <remarks>
/// <para>
/// The button has three plausible meanings and they are not interchangeable: quitting
/// the client stops ChatGPT using 共飞 credit, minimising keeps everything running, and
/// signing out changes account. Guessing wrong in either direction is costly — a user
/// who meant "get this window out of the way" and got a sign-out has to find their
/// password again; one who meant "stop this" and got a minimise is still being billed.
/// </para>
/// <para>
/// The consequence line is written from the live state rather than fixed, because
/// whether Codex is running is exactly what changes the cost of the choice.
/// </para>
/// </remarks>
public partial class ExitConfirmationDialog : Window
{
    private ExitChoice _choice = ExitChoice.None;

    public ExitConfirmationDialog()
    {
        InitializeComponent();
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    /// <summary>Asks the user and returns what they picked.</summary>
    internal static async Task<ExitChoice> AskAsync(Window owner, bool isCodexRunning)
    {
        var dialog = new ExitConfirmationDialog();
        string area = PlatformWords.NotificationArea;

        dialog.FindControl<Button>("MinimizeButton")!.Content = $"最小化到{area}";
        dialog.FindControl<TextBlock>("ConsequenceText")!.Text = isCodexRunning
            ? $"ChatGPT 正在运行。完全退出会关闭中转并释放当前授权，ChatGPT 将不再使用共飞额度；最小化到{area}可以继续保持连接。"
            : $"完全退出会释放当前授权并结束助手进程。最小化到{area}可以让助手继续在后台运行。";

        await dialog.ShowDialog(owner);
        return dialog._choice;
    }

    private void FullExit_OnClick(object? sender, RoutedEventArgs e) => Pick(ExitChoice.FullExit);

    private void Minimize_OnClick(object? sender, RoutedEventArgs e) => Pick(ExitChoice.MinimizeToTray);

    private void SignOut_OnClick(object? sender, RoutedEventArgs e) => Pick(ExitChoice.SignOut);

    private void Pick(ExitChoice choice)
    {
        _choice = choice;
        Close();
    }
}
