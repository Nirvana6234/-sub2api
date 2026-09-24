using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views;

/// <summary>
/// The announcement reader.
/// </summary>
/// <remarks>
/// Opened as a singleton by the shell: several entry points lead here — the title-bar
/// bell today, the tray menu and balloon once those are ported — and each of them
/// opening its own copy would leave the user with stacked windows showing the same
/// list.
/// </remarks>
public partial class AnnouncementWindow : Window
{
    private readonly AnnouncementsViewModel? _viewModel;
    private readonly IAnnouncementImageLoader? _imageLoader;
    private readonly Uri? _baseUri;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public AnnouncementWindow()
    {
        InitializeComponent();
    }

    internal AnnouncementWindow(
        AnnouncementsViewModel viewModel,
        Uri baseUri,
        IAnnouncementImageLoader? imageLoader = null)
    {
        _viewModel = viewModel ?? throw new ArgumentNullException(nameof(viewModel));
        _baseUri = baseUri ?? throw new ArgumentNullException(nameof(baseUri));
        _imageLoader = imageLoader;

        InitializeComponent();
        DataContext = _viewModel;

        Opened += Window_OnOpened;
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    private void Window_OnOpened(object? sender, EventArgs e)
    {
        if (_viewModel is null)
        {
            return;
        }

        // Opening straight into the first unread one: the user arrived here from a
        // balloon or a badge that said something was waiting, so making them pick it
        // out of the list again would be a step for nothing.
        _viewModel.Selected ??= _viewModel.Items.FirstOrDefault(item => item.IsUnread)
                                ?? _viewModel.Items.FirstOrDefault();

        RenderSelected();
    }

    private void AnnouncementList_OnSelectionChanged(object? sender, SelectionChangedEventArgs e)
    {
        RenderSelected();

        if (_viewModel?.Selected is { IsUnread: true } item)
        {
            // Fire-and-forget: marking read is bookkeeping, and making the reader wait
            // on a round trip before showing the body would be worse than a badge that
            // corrects itself a moment later.
            _ = _viewModel.MarkReadAsync(item);
        }
    }

    private void RenderSelected()
    {
        var viewer = this.FindControl<ScrollViewer>("ContentViewer")!;

        viewer.Content = _viewModel?.Selected is null
            ? null
            : AnnouncementContentBuilder.Build(
                _viewModel.Selected.Content,
                _baseUri!,
                _imageLoader,
                OpenInBrowser);
    }

    /// <remarks>
    /// The escape hatch for a body this reader renders poorly — a table, raw HTML,
    /// anything the parser could only degrade to literal text. It opens the panel
    /// dashboard rather than an announcements page, because there is no user-facing
    /// announcements route: on the web the same list hangs off the bell in the panel
    /// layout.
    /// </remarks>
    private void OpenInBrowser_OnClick(object? sender, RoutedEventArgs e) =>
        OpenInBrowser(new Uri(_baseUri!, "dashboard"));

    private void Close_OnClick(object? sender, RoutedEventArgs e) => Close();

    private static void OpenInBrowser(Uri uri) => BrowserLauncher.TryOpen(uri);
}
