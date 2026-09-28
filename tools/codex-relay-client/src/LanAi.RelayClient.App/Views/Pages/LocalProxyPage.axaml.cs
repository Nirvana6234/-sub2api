using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.Server;
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

    private LocalProxyViewModel? LocalProxy => (DataContext as DashboardPageViewModel)?.Dashboard.LocalProxy;

    private void Toggle_OnClick(object? sender, RoutedEventArgs e)
    {
        if ((sender as Control)?.DataContext is LocalProxyAccountItem item)
        {
            _actions?.ToggleLocalProxy(item);
        }
    }

    /// <summary>「在共飞里登录…」, 「用其他…账号登录」 and 「重新开始」: the tool is in the button's Tag.</summary>
    private void StartSignIn_OnClick(object? sender, RoutedEventArgs e)
    {
        if ((sender as Control)?.Tag is string tag && Enum.TryParse(tag, out LocalProxyKind kind))
        {
            _actions?.StartLocalProxySignIn(kind, reauthorize: null);
        }
    }

    private void Reauthorize_OnClick(object? sender, RoutedEventArgs e)
    {
        if ((sender as Control)?.DataContext is LocalProxyAccountItem item)
        {
            _actions?.StartLocalProxySignIn(item.Kind, item);
        }
    }

    private void Remove_OnClick(object? sender, RoutedEventArgs e)
    {
        if ((sender as Control)?.DataContext is LocalProxyAccountItem item)
        {
            _actions?.RemoveLocalProxyAccount(item);
        }
    }

    private void SubmitSignIn_OnClick(object? sender, RoutedEventArgs e) => LocalProxy?.SubmitSignIn();

    private void CancelSignIn_OnClick(object? sender, RoutedEventArgs e) => LocalProxy?.CancelSignIn();

    private void OpenSignInPage_OnClick(object? sender, RoutedEventArgs e) => LocalProxy?.OpenSignInPage();

    /// <summary>For a browser that did not open, or a sign-in finished in another browser.</summary>
    private async void CopySignInUrl_OnClick(object? sender, RoutedEventArgs e)
    {
        if (LocalProxy is { SignInUrl.Length: > 0 } localProxy && TopLevel.GetTopLevel(this)?.Clipboard is { } clipboard)
        {
            await clipboard.SetTextAsync(localProxy.SignInUrl);
        }
    }
}
