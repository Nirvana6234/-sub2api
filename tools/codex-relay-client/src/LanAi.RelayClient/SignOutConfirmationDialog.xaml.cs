using System.Windows;

namespace LanAi.RelayClient;

public enum SignOutChoice
{
    None,
    SignOut,
    MinimizeToTray,
}

public partial class SignOutConfirmationDialog : Window
{
    public SignOutConfirmationDialog(bool isCodexRunning)
    {
        InitializeComponent();
        ConsequenceText.Text = isCodexRunning
            ? "ChatGPT 正在运行。退出账号会释放当前授权；最小化到托盘可继续保持连接。"
            : "退出账号会清除本机登录状态并释放当前授权。也可以最小化到托盘继续保持登录。";
    }

    public SignOutChoice Choice { get; private set; }

    private void SignOut_OnClick(object sender, RoutedEventArgs e)
    {
        Choice = SignOutChoice.SignOut;
        DialogResult = true;
    }

    private void Minimize_OnClick(object sender, RoutedEventArgs e)
    {
        Choice = SignOutChoice.MinimizeToTray;
        DialogResult = true;
    }
}
