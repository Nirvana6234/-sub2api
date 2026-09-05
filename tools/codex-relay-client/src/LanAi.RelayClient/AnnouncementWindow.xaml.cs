using System.Diagnostics;
using System.Windows;
using System.Windows.Controls;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using MessageBox = System.Windows.MessageBox;

namespace LanAi.RelayClient;

/// <summary>
/// The announcement reader.
/// </summary>
/// <remarks>
/// Opened as a singleton by <see cref="MainWindow"/>: three entry points lead
/// here — the title-bar bell, the tray menu and the tray balloon — and each of
/// them opening its own copy would leave the user with stacked windows showing
/// the same list.
/// </remarks>
public partial class AnnouncementWindow : Window
{
    private readonly AnnouncementsViewModel _viewModel;
    private readonly IAnnouncementImageLoader? _imageLoader;
    private readonly Uri _baseUri;

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

        Loaded += Window_OnLoaded;
    }

    private void Window_OnLoaded(object sender, RoutedEventArgs e)
    {
        // Opening straight into the first unread one: the user arrived here from a
        // balloon or a badge that said something was waiting, so making them pick
        // it out of the list again would be a step for nothing.
        _viewModel.Selected ??= _viewModel.Items.FirstOrDefault(item => item.IsUnread)
                                ?? _viewModel.Items.FirstOrDefault();

        RenderSelected();
    }

    private void AnnouncementList_OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        RenderSelected();

        if (_viewModel.Selected is { IsUnread: true } item)
        {
            // Fire-and-forget: marking read is bookkeeping, and making the reader
            // wait on a round trip before showing the body would be worse than a
            // badge that corrects itself a moment later.
            _ = _viewModel.MarkReadAsync(item);
        }
    }

    private void RenderSelected()
    {
        ContentViewer.Document = _viewModel.Selected is null
            ? null
            : AnnouncementDocumentBuilder.Build(
                _viewModel.Selected.Content,
                _baseUri,
                _imageLoader,
                OpenInBrowser);
    }

    /// <remarks>
    /// The escape hatch for a body this reader renders poorly — a table, raw HTML,
    /// anything the parser could only degrade to literal text. It opens the panel
    /// dashboard rather than an announcements page, because there is no
    /// user-facing announcements route: on the web the same list hangs off the
    /// bell in the panel layout.
    /// </remarks>
    private void OpenInBrowser_OnClick(object sender, RoutedEventArgs e) =>
        OpenInBrowser(new Uri(_baseUri, "dashboard"));

    private void Close_OnClick(object sender, RoutedEventArgs e) => Close();

    private static void OpenInBrowser(Uri uri)
    {
        try
        {
            Process.Start(new ProcessStartInfo(uri.ToString()) { UseShellExecute = true });
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            MessageBox.Show("无法打开浏览器，请手动访问：" + uri, "共飞-ChatGPT助手");
        }
    }
}
