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
    private readonly IDashboardActions? _actions;

    private static readonly TimeSpan DesktopStartTimeout = TimeSpan.FromSeconds(60);
    private static readonly TimeSpan DesktopPollInterval = TimeSpan.FromSeconds(3);

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public DesktopSyncPage()
    {
        InitializeComponent();
    }

    internal DesktopSyncPage(DesktopSyncViewModel viewModel, SafeAsyncRunner safeAsync, IDashboardActions actions)
        : this()
    {
        _viewModel = viewModel ?? throw new ArgumentNullException(nameof(viewModel));
        _safeAsync = safeAsync ?? throw new ArgumentNullException(nameof(safeAsync));
        _actions = actions ?? throw new ArgumentNullException(nameof(actions));
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

    /// <summary>The same start as the overview's button, then watch for the app to come up.</summary>
    private void LaunchDesktop_OnClick(object? sender, RoutedEventArgs e)
    {
        _actions?.StartOrInstallCodex();
        Run(vm => vm.WaitForDesktopAsync(DesktopStartTimeout, DesktopPollInterval));
    }

    private void Pair_OnClick(object? sender, RoutedEventArgs e) => Run(vm => vm.StartPairingAsync());

    private void Toggle_OnClick(object? sender, RoutedEventArgs e)
    {
        if ((sender as Control)?.DataContext is SyncSessionItem item)
        {
            // The box shows the stored choice, not the click: a refused or cancelled
            // selection must not leave it ticked.
            // SetCurrentValue, not assignment: assigning sets a local value that replaces the
            // binding for good, and the box would stop following the stored choice.
            if (sender is CheckBox box)
            {
                box.SetCurrentValue(CheckBox.IsCheckedProperty, item.IsSelected);
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
