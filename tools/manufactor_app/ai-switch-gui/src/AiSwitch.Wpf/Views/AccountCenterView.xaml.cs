using System.Windows;
using System.Windows.Controls;
using System.Windows.Controls.Primitives;
using System.Windows.Input;
using System.Windows.Media;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Views;

public partial class AccountCenterView : UserControl
{
    private Point _accountDragStart;
    private AccountCenterAccountViewModel? _draggedAccount;

    public AccountCenterView() => InitializeComponent();

    private async void AccountCenterView_OnLoaded(object sender, RoutedEventArgs e)
    {
        if (DataContext is not AccountCenterViewModel viewModel) return;
        try
        {
            await viewModel.ActivateAsync();
        }
        catch (OperationCanceledException)
        {
        }
    }

    private void AddAccountSecretInput_OnPasswordChanged(object sender, RoutedEventArgs e)
    {
        if (DataContext is AccountCenterViewModel viewModel && sender is PasswordBox passwordBox)
        {
            viewModel.AddApiKey = passwordBox.Password;
        }
    }

    private void AddProxyPasswordInput_OnPasswordChanged(object sender, RoutedEventArgs e)
    {
        if (DataContext is AccountCenterViewModel viewModel && sender is PasswordBox passwordBox)
        {
            viewModel.AddProxyPassword = passwordBox.Password;
        }
    }

    private void AddAccountOverlay_OnIsVisibleChanged(object sender, DependencyPropertyChangedEventArgs e)
    {
        if (AddAccountSecretInput is not null)
        {
            AddAccountSecretInput.Clear();
        }
        if (AddProxyPasswordInput is not null)
        {
            AddProxyPasswordInput.Clear();
        }
    }

    private void AccountMoreButton_OnClick(object sender, RoutedEventArgs e)
    {
        if (sender is not Button button ||
            button.Tag is not AccountCenterAccountViewModel account ||
            DataContext is not AccountCenterViewModel viewModel)
        {
            return;
        }

        var menu = new ContextMenu
        {
            PlacementTarget = button,
            Placement = PlacementMode.Bottom,
            HorizontalOffset = -8,
        };
        menu.Items.Add(CreateMenuItem("刷新用量", viewModel.RefreshAccountUsageCommand, account));
        menu.Items.Add(CreateMenuItem(account.ToggleLabel, viewModel.ToggleAccountCommand, account));
        menu.Items.Add(new Separator());

        if (account.SupportsCredentialRefresh)
        {
            menu.Items.Add(CreateAdminMenuItem("刷新凭据", viewModel, account, AccountCenterAdminAction.RefreshCredentials));
        }
        if (account.NeedsAttention)
        {
            menu.Items.Add(CreateAdminMenuItem("恢复运行状态", viewModel, account, AccountCenterAdminAction.RecoverState));
        }
        if (account.HasError)
        {
            menu.Items.Add(CreateAdminMenuItem("清除错误状态", viewModel, account, AccountCenterAdminAction.ClearError));
        }
        if (account.SupportsPrivacy)
        {
            menu.Items.Add(CreateAdminMenuItem("重新设置隐私保护", viewModel, account, AccountCenterAdminAction.SetPrivacy));
        }
        if (account.SupportsQuotaReset)
        {
            menu.Items.Add(CreateAdminMenuItem("重置额度状态", viewModel, account, AccountCenterAdminAction.ResetQuota));
        }
        if (account.SupportsModelSync)
        {
            menu.Items.Add(CreateAdminMenuItem("同步上游模型", viewModel, account, AccountCenterAdminAction.SyncUpstreamModels));
        }

        menu.IsOpen = true;
    }

    private void AccountDragHandle_OnPreviewMouseLeftButtonDown(object sender, MouseButtonEventArgs e)
    {
        _accountDragStart = e.GetPosition(this);
        _draggedAccount = (sender as FrameworkElement)?.Tag as AccountCenterAccountViewModel;
        if (DataContext is AccountCenterViewModel viewModel)
        {
            foreach (AccountCenterAccountViewModel account in viewModel.Accounts)
            {
                account.IsDetailsExpanded = false;
            }
        }
    }

    private void AccountDragHandle_OnPreviewMouseMove(object sender, MouseEventArgs e)
    {
        if (e.LeftButton != MouseButtonState.Pressed || _draggedAccount is null) return;
        Point current = e.GetPosition(this);
        if (Math.Abs(current.X - _accountDragStart.X) < SystemParameters.MinimumHorizontalDragDistance &&
            Math.Abs(current.Y - _accountDragStart.Y) < SystemParameters.MinimumVerticalDragDistance)
        {
            return;
        }

        AccountCenterAccountViewModel source = _draggedAccount;
        try
        {
            DragDrop.DoDragDrop((DependencyObject)sender, source, DragDropEffects.Move);
        }
        finally
        {
            _draggedAccount = null;
            ClearAccountDropTarget();
        }
    }

    private void AccountItems_OnDragOver(object sender, DragEventArgs e)
    {
        AccountCenterAccountViewModel? source = e.Data.GetData(typeof(AccountCenterAccountViewModel)) as AccountCenterAccountViewModel;
        (AccountCenterAccountViewModel? target, FrameworkElement? container) = FindDropTarget<AccountCenterAccountViewModel>(e.OriginalSource);
        ClearAccountDropTarget();
        if (source is null || target is null || container is null || ReferenceEquals(source, target))
        {
            e.Effects = DragDropEffects.None;
            e.Handled = true;
            return;
        }
        target.IsDropTarget = true;
        target.DropInsertAfter = e.GetPosition(container).Y > container.ActualHeight / 2;
        e.Effects = DragDropEffects.Move;
        e.Handled = true;
    }

    private void AccountItems_OnDragLeave(object sender, DragEventArgs e)
    {
        Point position = e.GetPosition(AccountItems);
        if (position.X < 0 || position.Y < 0 || position.X > AccountItems.ActualWidth || position.Y > AccountItems.ActualHeight)
        {
            ClearAccountDropTarget();
        }
    }

    private async void AccountItems_OnDrop(object sender, DragEventArgs e)
    {
        AccountCenterAccountViewModel? source = e.Data.GetData(typeof(AccountCenterAccountViewModel)) as AccountCenterAccountViewModel;
        (AccountCenterAccountViewModel? target, FrameworkElement? container) = FindDropTarget<AccountCenterAccountViewModel>(e.OriginalSource);
        ClearAccountDropTarget();
        if (source is null || target is null || container is null || ReferenceEquals(source, target) ||
            DataContext is not AccountCenterViewModel viewModel)
        {
            return;
        }
        bool insertAfter = e.GetPosition(container).Y > container.ActualHeight / 2;
        await viewModel.ReorderAccountAsync(source, target, insertAfter);
    }

    private void ClearAccountDropTarget()
    {
        if (DataContext is not AccountCenterViewModel viewModel) return;
        foreach (AccountCenterAccountViewModel account in viewModel.Accounts)
        {
            account.IsDropTarget = false;
            account.DropInsertAfter = false;
        }
    }

    private void AccountDetailsButton_OnClick(object sender, RoutedEventArgs e)
    {
        if (sender is not FrameworkElement { Tag: AccountCenterAccountViewModel selected } ||
            DataContext is not AccountCenterViewModel viewModel)
        {
            return;
        }

        bool expand = !selected.IsDetailsExpanded;
        foreach (AccountCenterAccountViewModel account in viewModel.Accounts)
        {
            account.IsDetailsExpanded = expand && ReferenceEquals(account, selected);
        }
    }

    private static (T? Item, FrameworkElement? Container) FindDropTarget<T>(object originalSource) where T : class
    {
        DependencyObject? current = originalSource as DependencyObject;
        T? item = null;
        FrameworkElement? container = null;
        while (current is not null && current is not ItemsControl)
        {
            if (current is FrameworkElement element && element.DataContext is T candidate)
            {
                item ??= candidate;
                if (ReferenceEquals(item, candidate)) container = element;
            }
            current = VisualTreeHelper.GetParent(current);
        }
        return (item, container);
    }

    private static MenuItem CreateAdminMenuItem(
        string header,
        AccountCenterViewModel viewModel,
        AccountCenterAccountViewModel account,
        AccountCenterAdminAction action)
        => CreateMenuItem(
            header,
            viewModel.ManageAccountCommand,
            new AccountCenterAccountActionRequest(account, action));

    private static MenuItem CreateMenuItem(string header, ICommand command, object parameter)
        => new()
        {
            Header = header,
            Command = command,
            CommandParameter = parameter,
            MinWidth = 180,
        };
}
