using Avalonia.Controls;
using Avalonia.Controls.Primitives;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views.Pages;

public partial class ExplorePage : UserControl
{
    private readonly WeChatIntentViewModel? _viewModel;
    private readonly SafeAsyncRunner? _safeAsync;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public ExplorePage()
    {
        InitializeComponent();
    }

    internal ExplorePage(WeChatIntentViewModel viewModel, SafeAsyncRunner safeAsync)
        : this()
    {
        _viewModel = viewModel ?? throw new ArgumentNullException(nameof(viewModel));
        _safeAsync = safeAsync ?? throw new ArgumentNullException(nameof(safeAsync));
        DataContext = viewModel;
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    /// <summary>
    /// The switch shows the view model's state, not the click: turning on may be refused (no
    /// consent, WeChat not running), and the switch must not stay on when it was.
    /// </summary>
    private void Enable_OnClick(object? sender, RoutedEventArgs e)
    {
        if (_viewModel is null || _safeAsync is null || sender is not ToggleButton toggle)
        {
            return;
        }

        bool wanted = toggle.IsChecked == true;
        toggle.SetCurrentValue(ToggleButton.IsCheckedProperty, _viewModel.IsEnabled);
        _ = _safeAsync.RunAsync(async () =>
        {
            await _viewModel.SetEnabledAsync(wanted).ConfigureAwait(true);
            toggle.SetCurrentValue(ToggleButton.IsCheckedProperty, _viewModel.IsEnabled);
        });
    }

    private void SaveKey_OnClick(object? sender, RoutedEventArgs e)
    {
        if (_viewModel is not null && _safeAsync is not null)
        {
            _ = _safeAsync.RunAsync(_viewModel.SaveKeyAsync);
        }
    }

    private void ClearKey_OnClick(object? sender, RoutedEventArgs e) => _viewModel?.ClearKey();

    /// <summary>
    /// Test builds only. The box shows the view model's route, not the click: moving back to the
    /// 共飞 group may ask for its consent first, and a refusal must leave the box as it was.
    /// </summary>
    private void UseOwnKey_OnClick(object? sender, RoutedEventArgs e)
    {
        if (_viewModel is null || _safeAsync is null || sender is not CheckBox box)
        {
            return;
        }

        bool ownKey = box.IsChecked == true;
        box.SetCurrentValue(ToggleButton.IsCheckedProperty, _viewModel.UseOwnKey);
        _ = _safeAsync.RunAsync(async () =>
        {
            await _viewModel.SetUseRelayGroupAsync(!ownKey).ConfigureAwait(true);
            box.SetCurrentValue(ToggleButton.IsCheckedProperty, _viewModel.UseOwnKey);
        });
    }

    private void Pause_OnClick(object? sender, RoutedEventArgs e) => _viewModel?.Pause();

    private void Resume_OnClick(object? sender, RoutedEventArgs e) => _viewModel?.Resume();

    private void Unmute_OnClick(object? sender, RoutedEventArgs e)
    {
        if ((sender as Control)?.DataContext is string chat)
        {
            _viewModel?.Unmute(chat);
        }
    }
}
