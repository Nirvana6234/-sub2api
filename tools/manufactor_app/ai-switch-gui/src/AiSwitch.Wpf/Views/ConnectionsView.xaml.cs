using System.ComponentModel;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Media;
using System.Windows.Threading;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Views;

public partial class ConnectionsView : UserControl
{
    private ConnectionsViewModel? _viewModel;
    private Point _backupDragStart;
    private ConnectionCardViewModel? _draggedBackup;

    public ConnectionsView()
    {
        InitializeComponent();
        DataContextChanged += ConnectionsView_OnDataContextChanged;
    }

    private void ConnectionsView_OnDataContextChanged(object sender, System.Windows.DependencyPropertyChangedEventArgs e)
    {
        if (_viewModel is not null)
        {
            _viewModel.PropertyChanged -= ViewModel_OnPropertyChanged;
        }

        _viewModel = e.NewValue as ConnectionsViewModel;
        if (_viewModel is not null)
        {
            _viewModel.PropertyChanged += ViewModel_OnPropertyChanged;
        }
    }

    private void ViewModel_OnPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (e.PropertyName != nameof(ConnectionsViewModel.ConnectionEditor))
        {
            return;
        }

        Dispatcher.BeginInvoke(DispatcherPriority.Loaded, new Action(() =>
        {
            if (_viewModel?.ConnectionEditor is not null)
            {
                ConnectionEditorPanel.BringIntoView();
            }
        }));
    }

    private void CodexSecret_OnPasswordChanged(object sender, System.Windows.RoutedEventArgs e) =>
        SetEnteredSecret(CliKind.Codex, sender);

    private void ClaudeSecret_OnPasswordChanged(object sender, System.Windows.RoutedEventArgs e) =>
        SetEnteredSecret(CliKind.ClaudeCode, sender);

    private void GeminiSecret_OnPasswordChanged(object sender, System.Windows.RoutedEventArgs e) =>
        SetEnteredSecret(CliKind.GeminiCli, sender);

    private void GrokSecret_OnPasswordChanged(object sender, System.Windows.RoutedEventArgs e) =>
        SetEnteredSecret(CliKind.GrokCli, sender);

    private void SetEnteredSecret(CliKind client, object sender)
    {
        if (DataContext is ConnectionsViewModel viewModel && sender is PasswordBox passwordBox)
        {
            viewModel.SetEnteredSecret(client, passwordBox.Password);
        }
    }

    private void BackupDragHandle_OnPreviewMouseLeftButtonDown(object sender, MouseButtonEventArgs e)
    {
        _backupDragStart = e.GetPosition(this);
        _draggedBackup = (sender as FrameworkElement)?.Tag as ConnectionCardViewModel;
        if (DataContext is ConnectionsViewModel viewModel)
        {
            foreach (ConnectionCardViewModel source in viewModel.BackupConnections)
            {
                source.IsExpanded = false;
            }
        }
    }

    private void BackupDragHandle_OnPreviewMouseMove(object sender, MouseEventArgs e)
    {
        if (e.LeftButton != MouseButtonState.Pressed || _draggedBackup is null) return;
        Point current = e.GetPosition(this);
        if (Math.Abs(current.X - _backupDragStart.X) < SystemParameters.MinimumHorizontalDragDistance &&
            Math.Abs(current.Y - _backupDragStart.Y) < SystemParameters.MinimumVerticalDragDistance)
        {
            return;
        }

        ConnectionCardViewModel source = _draggedBackup;
        try
        {
            DragDrop.DoDragDrop((DependencyObject)sender, source, DragDropEffects.Move);
        }
        finally
        {
            _draggedBackup = null;
            ClearBackupDropTarget();
        }
    }

    private void BackupItems_OnDragOver(object sender, DragEventArgs e)
    {
        ConnectionCardViewModel? source = e.Data.GetData(typeof(ConnectionCardViewModel)) as ConnectionCardViewModel;
        (ConnectionCardViewModel? target, FrameworkElement? container) = FindDropTarget<ConnectionCardViewModel>(e.OriginalSource);
        ClearBackupDropTarget();
        if (source is null || target is null || container is null || !target.IsBackupEnabled || ReferenceEquals(source, target))
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

    private void BackupItems_OnDragLeave(object sender, DragEventArgs e)
    {
        Point position = e.GetPosition(BackupItems);
        if (position.X < 0 || position.Y < 0 || position.X > BackupItems.ActualWidth || position.Y > BackupItems.ActualHeight)
        {
            ClearBackupDropTarget();
        }
    }

    private async void BackupItems_OnDrop(object sender, DragEventArgs e)
    {
        ConnectionCardViewModel? source = e.Data.GetData(typeof(ConnectionCardViewModel)) as ConnectionCardViewModel;
        (ConnectionCardViewModel? target, FrameworkElement? container) = FindDropTarget<ConnectionCardViewModel>(e.OriginalSource);
        ClearBackupDropTarget();
        if (source is null || target is null || container is null || !target.IsBackupEnabled || ReferenceEquals(source, target) ||
            DataContext is not ConnectionsViewModel viewModel)
        {
            return;
        }
        bool insertAfter = e.GetPosition(container).Y > container.ActualHeight / 2;
        await viewModel.ReorderBackupAsync(source, target, insertAfter);
    }

    private void ClearBackupDropTarget()
    {
        if (DataContext is not ConnectionsViewModel viewModel) return;
        foreach (ConnectionCardViewModel source in viewModel.BackupConnections)
        {
            source.IsDropTarget = false;
            source.DropInsertAfter = false;
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
}




