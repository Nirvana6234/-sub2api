using System.IO;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Documents;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using System.Windows.Navigation;

namespace LanAi.RelayClient.Services;

/// <summary>
/// Turns the parsed announcement model into something a reader can display.
/// </summary>
/// <remarks>
/// Kept apart from <see cref="AnnouncementMarkdownParser"/> so the parsing rules
/// stay testable off the UI thread. Everything here is presentation: no decision
/// about what the markdown means is taken in this file.
/// </remarks>
internal static class AnnouncementDocumentBuilder
{
    private static readonly FontFamily MonospaceFont = new("Consolas, Cascadia Mono, Courier New");

    /// <summary>Ceiling on how wide an image is drawn, so a large upload cannot push the layout out.</summary>
    private const double MaxImageWidth = 420;

    public static FlowDocument Build(
        string? markdown,
        Uri baseUri,
        IAnnouncementImageLoader? imageLoader,
        Action<Uri>? onNavigate)
    {
        ArgumentNullException.ThrowIfNull(baseUri);

        var document = new FlowDocument
        {
            FontSize = 14,
            LineHeight = 24,
            PagePadding = new Thickness(0),
            TextAlignment = TextAlignment.Left,
        };

        foreach (AnnouncementNode node in AnnouncementMarkdownParser.Parse(markdown))
        {
            switch (node)
            {
                case AnnouncementParagraph paragraph:
                    document.Blocks.Add(BuildParagraph(paragraph, baseUri, onNavigate));
                    break;

                case AnnouncementListNode list:
                    foreach (AnnouncementListItem item in list.Items)
                    {
                        Paragraph line = BuildParagraph(item.Content, baseUri, onNavigate);
                        line.Margin = new Thickness(16, 2, 0, 2);
                        line.Inlines.InsertBefore(line.Inlines.FirstInline, new Run(item.Marker + " "));
                        document.Blocks.Add(line);
                    }

                    break;

                case AnnouncementImageNode image:
                    document.Blocks.Add(BuildImage(image, imageLoader));
                    break;

                case AnnouncementCodeNode code:
                    document.Blocks.Add(new Paragraph(new Run(code.Text))
                    {
                        FontFamily = MonospaceFont,
                        FontSize = 12.5,
                        Background = new SolidColorBrush(Color.FromRgb(0xF3, 0xF4, 0xF6)),
                        Padding = new Thickness(10, 8, 10, 8),
                        Margin = new Thickness(0, 6, 0, 6),
                    });
                    break;

                case AnnouncementDividerNode:
                    document.Blocks.Add(new BlockUIContainer(new Separator
                    {
                        Margin = new Thickness(0, 8, 0, 8),
                    }));
                    break;
            }
        }

        if (document.Blocks.Count == 0)
        {
            document.Blocks.Add(new Paragraph(new Run("（本条公告没有正文）"))
            {
                Foreground = Brushes.Gray,
            });
        }

        return document;
    }

    private static Paragraph BuildParagraph(AnnouncementParagraph source, Uri baseUri, Action<Uri>? onNavigate)
    {
        var paragraph = new Paragraph { Margin = new Thickness(0, 4, 0, 4) };

        if (source.HeadingLevel > 0)
        {
            paragraph.FontSize = source.HeadingLevel switch
            {
                1 => 20,
                2 => 17,
                3 => 15.5,
                _ => 14.5,
            };
            paragraph.FontWeight = FontWeights.SemiBold;
            paragraph.Margin = new Thickness(0, 10, 0, 4);
        }

        if (source.Quoted)
        {
            paragraph.Padding = new Thickness(10, 0, 0, 0);
            paragraph.BorderThickness = new Thickness(3, 0, 0, 0);
            paragraph.BorderBrush = new SolidColorBrush(Color.FromRgb(0xD1, 0xD5, 0xDB));
            paragraph.Foreground = new SolidColorBrush(Color.FromRgb(0x4B, 0x55, 0x63));
        }

        foreach (AnnouncementRun run in source.Runs)
        {
            paragraph.Inlines.Add(BuildInline(run, baseUri, onNavigate));
        }

        return paragraph;
    }

    private static Inline BuildInline(AnnouncementRun source, Uri baseUri, Action<Uri>? onNavigate)
    {
        var run = new Run(source.Text);

        if (source.Bold)
        {
            run.FontWeight = FontWeights.SemiBold;
        }

        if (source.Italic)
        {
            run.FontStyle = FontStyles.Italic;
        }

        if (source.Code)
        {
            run.FontFamily = MonospaceFont;
            run.Background = new SolidColorBrush(Color.FromRgb(0xF3, 0xF4, 0xF6));
        }

        if (source.LinkUrl is null || !Uri.TryCreate(baseUri, source.LinkUrl, out Uri? target))
        {
            return run;
        }

        var hyperlink = new Hyperlink(run) { NavigateUri = target };
        hyperlink.RequestNavigate += (_, args) =>
        {
            // Opened in the user's own browser rather than anything embedded: the
            // client has no navigation surface, and an operator-authored link is
            // not something to render inside the app's own chrome.
            onNavigate?.Invoke(args.Uri);
            args.Handled = true;
        };

        return hyperlink;
    }

    /// <summary>
    /// Lays out an image as its alt text first, replaced by the picture if it loads.
    /// </summary>
    /// <remarks>
    /// Alt-first rather than a spinner-then-nothing: if the load fails for any of
    /// the reasons <see cref="IAnnouncementImageLoader"/> tolerates, what is left
    /// on screen already says what the picture was supposed to be.
    /// </remarks>
    private static Block BuildImage(AnnouncementImageNode source, IAnnouncementImageLoader? imageLoader)
    {
        var placeholder = new TextBlock
        {
            Text = source.AltText,
            Foreground = Brushes.Gray,
            FontSize = 12.5,
            TextWrapping = TextWrapping.Wrap,
        };

        var picture = new Image
        {
            MaxWidth = MaxImageWidth,
            Stretch = Stretch.Uniform,
            StretchDirection = StretchDirection.DownOnly,
            HorizontalAlignment = HorizontalAlignment.Left,
            Visibility = Visibility.Collapsed,
        };

        var host = new StackPanel { Margin = new Thickness(0, 6, 0, 6) };
        host.Children.Add(picture);
        host.Children.Add(placeholder);

        if (imageLoader is not null)
        {
            _ = LoadIntoAsync(imageLoader, source.Source, picture, placeholder);
        }

        return new BlockUIContainer(host);
    }

    /// <summary>Decodes to a frozen bitmap.</summary>
    /// <remarks>
    /// <para>
    /// Moved here from the loader when that moved to the shared project: decoding is
    /// the only part of image handling that has no cross-platform form.
    /// </para>
    /// <para>
    /// <c>OnLoad</c> then <c>Freeze</c>, in that order: OnLoad forces the decode to
    /// finish at <c>EndInit</c> so the stream can be released, and freezing is what
    /// makes the result safe to hand to the UI thread from the background one that
    /// downloaded it. An unfrozen bitmap built off-thread throws when it is bound.
    /// </para>
    /// <para>
    /// Returning null on a bad decode is what keeps the alt text on screen. Letting it
    /// throw would take down the reader for one broken image in one announcement.
    /// </para>
    /// </remarks>
    private static BitmapSource? Decode(byte[] bytes)
    {
        try
        {
            using var stream = new MemoryStream(bytes, writable: false);

            var bitmap = new BitmapImage();
            bitmap.BeginInit();
            bitmap.StreamSource = stream;
            bitmap.CacheOption = BitmapCacheOption.OnLoad;
            bitmap.EndInit();
            bitmap.Freeze();
            return bitmap;
        }
        catch (Exception ex) when (ex is NotSupportedException or ArgumentException or FileFormatException)
        {
            // Not an image, or an image in a codec this machine lacks.
            ClientLog.Warning("公告图片无法解码", ex);
            return null;
        }
    }

    private static async Task LoadIntoAsync(
        IAnnouncementImageLoader imageLoader,
        string source,
        Image picture,
        TextBlock placeholder)
    {
        // LoadAsync is documented never to throw, so this fire-and-forget cannot
        // leave an unobserved fault behind.
        byte[]? bytes = await imageLoader.LoadAsync(source).ConfigureAwait(false);
        BitmapSource? bitmap = bytes is null ? null : Decode(bytes);
        if (bitmap is null)
        {
            return;
        }

        await picture.Dispatcher.InvokeAsync(() =>
        {
            picture.Source = bitmap;
            picture.Visibility = Visibility.Visible;
            placeholder.Visibility = Visibility.Collapsed;
        });
    }
}
