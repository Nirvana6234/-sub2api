using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views.Pages;

public partial class CodexPage : UserControl
{
    private readonly IDashboardActions? _actions;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public CodexPage()
    {
        InitializeComponent();
    }

    internal CodexPage(IDashboardActions actions)
        : this()
    {
        _actions = actions ?? throw new ArgumentNullException(nameof(actions));
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    private void StartCodex_OnClick(object? sender, RoutedEventArgs e) => _actions?.StartOrInstallCodex();

    private void RepairCodexStartup_OnClick(object? sender, RoutedEventArgs e) => _actions?.RepairCodexStartup();

    private void ConfigureAutoGroup_OnClick(object? sender, RoutedEventArgs e) => _actions?.ConfigureAutoGroup();

    private void ShowGroupModels_OnClick(object? sender, RoutedEventArgs e)
    {
        if ((sender as Control)?.DataContext is GroupItemViewModel group)
        {
            _actions?.ShowGroupModels(group);
        }
    }
}
