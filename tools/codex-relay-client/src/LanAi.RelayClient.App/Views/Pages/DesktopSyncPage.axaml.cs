using Avalonia;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views.Pages;

public partial class DesktopSyncPage : UserControl
{
    private readonly DesktopSyncViewModel? _viewModel;
    private readonly SafeAsyncRunner? _safeAsync;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public DesktopSyncPage()
    {
        InitializeComponent();
    }

    internal DesktopSyncPage(DesktopSyncViewModel viewModel, SafeAsyncRunner safeAsync)
        : this()
    {
        _viewModel = viewModel ?? throw new ArgumentNullException(nameof(viewModel));
        _safeAsync = safeAsync ?? throw new ArgumentNullException(nameof(safeAsync));
        DataContext = viewModel;
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    /// <summary>The list is re-read each time the page is shown: the desktop app changes under it.</summary>
    protected override void OnAttachedToVisualTree(VisualTreeAttachmentEventArgs e)
    {
        base.OnAttachedToVisualTree(e);
        Run(vm => vm.RefreshSessionsAsync());
    }

    private void Refresh_OnClick(object? sender, RoutedEventArgs e) => Run(vm => vm.RefreshSessionsAsync());

    private void Pair_OnClick(object? sender, RoutedEventArgs e) => Run(vm => vm.StartPairingAsync());

    private void Toggle_OnClick(object? sender, RoutedEventArgs e)
    {
        if ((sender as Control)?.DataContext is SyncSessionItem item)
        {
            // The box shows the stored choice, not the click: a refused or cancelled
            // selection must not leave it ticked.
            if (sender is CheckBox box)
            {
                box.IsChecked = item.IsSelected;
            }

            Run(vm => vm.ToggleAsync(item));
        }
    }

    private void Revoke_OnClick(object? sender, RoutedEventArgs e)
    {
        if ((sender as Control)?.DataContext is SyncPhoneItem phone)
        {
            Run(vm => vm.RevokeAsync(phone));
        }
    }

    private void Run(Func<DesktopSyncViewModel, Task> action)
    {
        if (_viewModel is not null && _safeAsync is not null)
        {
            _ = _safeAsync.RunAsync(() => action(_viewModel));
        }
    }
}
