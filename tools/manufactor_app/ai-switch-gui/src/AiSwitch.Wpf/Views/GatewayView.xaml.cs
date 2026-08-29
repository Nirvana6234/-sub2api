using System.Windows;
using System.Windows.Controls;
using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Views;

public partial class GatewayView : UserControl
{
    public GatewayView() => InitializeComponent();

    private async void GatewayView_OnLoaded(object sender, RoutedEventArgs e)
    {
        if (DataContext is not GatewayViewModel viewModel)
        {
            return;
        }

        try
        {
            await viewModel.InitializeAsync();
        }
        catch (OperationCanceledException)
        {
            // Navigation or application shutdown can end an in-flight probe.
        }
    }

    private void OperationLogTextBox_OnTextChanged(object sender, TextChangedEventArgs e)
    {
        if (sender is TextBox textBox)
        {
            textBox.ScrollToEnd();
        }
    }

    private async void LoginLocalAccountButton_OnClick(object sender, RoutedEventArgs e)
    {
        if (DataContext is not GatewayViewModel viewModel)
        {
            return;
        }

        bool succeeded = await viewModel.LoginLocalAccountAsync(LocalAccountPasswordBox.Password);
        if (succeeded)
        {
            LocalAccountPasswordBox.Clear();
        }
    }

}
