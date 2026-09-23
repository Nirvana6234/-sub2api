using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views.Pages;

public partial class LocalProxyPage : UserControl
{
    private readonly IDashboardActions? _actions;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public LocalProxyPage()
    {
        InitializeComponent();
    }

    internal LocalProxyPage(IDashboardActions actions)
        : this()
    {
        _actions = actions ?? throw new ArgumentNullException(nameof(actions));
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    private void Toggle_OnClick(object? sender, RoutedEventArgs e)
    {
        if ((sender as Control)?.DataContext is LocalProxyAccountItem item)
        {
            _actions?.ToggleLocalProxy(item);
        }
    }
}
