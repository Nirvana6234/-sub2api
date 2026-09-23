using Avalonia.Controls;
using Avalonia.Markup.Xaml;

namespace LanAi.RelayClient.App.Views.Pages;

/// <remarks>Everything here is two-way bound; there are no buttons to forward.</remarks>
public partial class ClaudePage : UserControl
{
    public ClaudePage()
    {
        InitializeComponent();
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);
}
