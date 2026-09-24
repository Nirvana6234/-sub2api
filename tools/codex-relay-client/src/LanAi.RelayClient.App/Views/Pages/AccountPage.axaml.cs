using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;

namespace LanAi.RelayClient.App.Views.Pages;

public partial class AccountPage : UserControl
{
    private readonly IDashboardActions? _actions;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public AccountPage()
    {
        InitializeComponent();
    }

    internal AccountPage(IDashboardActions actions)
        : this()
    {
        _actions = actions ?? throw new ArgumentNullException(nameof(actions));
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    private void Recharge_OnClick(object? sender, RoutedEventArgs e) => _actions?.Recharge();
}
