using System.Windows;
using System.Windows.Controls;
using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Views;

public partial class StatsView : UserControl
{
    public StatsView() => InitializeComponent();

    private async void StatsView_OnLoaded(object sender, RoutedEventArgs e)
    {
        if (DataContext is StatsViewModel viewModel)
        {
            await viewModel.ActivateAsync();
        }
    }

    private async void LocalCloudRefreshButton_OnClick(object sender, RoutedEventArgs e)
    {
        if (DataContext is StatsViewModel viewModel && viewModel.CanRefresh)
        {
            await viewModel.RefreshAsync(null);
        }
    }

    private async void SaveLocalAdministratorAuthorizationButton_OnClick(object sender, RoutedEventArgs e)
    {
        if (DataContext is not StatsViewModel viewModel)
        {
            return;
        }

        string submittedKey = LocalAdministratorApiKeyInput.Password;
        try
        {
            await viewModel.SaveLocalAdministratorAuthorizationAsync(submittedKey);
        }
        finally
        {
            submittedKey = string.Empty;
            LocalAdministratorApiKeyInput.Clear();
        }
    }

    private async void AuthorizeLocalUserButton_OnClick(object sender, RoutedEventArgs e)
    {
        if (DataContext is not StatsViewModel viewModel)
        {
            return;
        }

        string submittedPassword = LocalUserPasswordInput.Password;
        try
        {
            await viewModel.AuthorizeLocalUserAsync(submittedPassword);
        }
        finally
        {
            submittedPassword = string.Empty;
            LocalUserPasswordInput.Clear();
        }
    }
}
