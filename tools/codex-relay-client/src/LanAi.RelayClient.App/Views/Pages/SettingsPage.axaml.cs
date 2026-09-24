using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;

namespace LanAi.RelayClient.App.Views.Pages;

public partial class SettingsPage : UserControl
{
    private readonly IDashboardActions? _actions;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public SettingsPage()
    {
        InitializeComponent();
    }

    internal SettingsPage(IDashboardActions actions)
        : this()
    {
        _actions = actions ?? throw new ArgumentNullException(nameof(actions));
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    private void CheckUpdate_OnClick(object? sender, RoutedEventArgs e) => _actions?.CheckUpdate();

    private void ShowAnnouncements_OnClick(object? sender, RoutedEventArgs e) => _actions?.ShowAnnouncements();

    private void OpenContactPage_OnClick(object? sender, RoutedEventArgs e) => _actions?.OpenContactPage();
}
