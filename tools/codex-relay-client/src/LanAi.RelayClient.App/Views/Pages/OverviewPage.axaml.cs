using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views.Pages;

public partial class OverviewPage : UserControl
{
    private readonly IDashboardActions? _actions;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public OverviewPage()
    {
        InitializeComponent();
    }

    internal OverviewPage(IDashboardActions actions)
        : this()
    {
        _actions = actions ?? throw new ArgumentNullException(nameof(actions));
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    private void StartCodex_OnClick(object? sender, RoutedEventArgs e) => _actions?.StartOrInstallCodex();

    private void Recharge_OnClick(object? sender, RoutedEventArgs e) => _actions?.Recharge();

    private void OpenCodex_OnClick(object? sender, RoutedEventArgs e) => _actions?.Navigate(ClientPage.Codex);

    private void OpenClaude_OnClick(object? sender, RoutedEventArgs e) => _actions?.Navigate(ClientPage.Claude);

    private void OpenKimi_OnClick(object? sender, RoutedEventArgs e) => _actions?.Navigate(ClientPage.Kimi);
}
