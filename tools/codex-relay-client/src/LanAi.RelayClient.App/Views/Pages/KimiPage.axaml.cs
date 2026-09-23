using Avalonia.Controls;
using Avalonia.Markup.Xaml;

namespace LanAi.RelayClient.App.Views.Pages;

public partial class KimiPage : UserControl
{
    public KimiPage()
    {
        InitializeComponent();
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);
}
