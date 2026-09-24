using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Documents;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Media.Imaging;
using Avalonia.Threading;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.App.Views;

/// <summary>
/// Turns a parsed announcement into Avalonia controls.
/// </summary>
/// <remarks>
/// <para>
/// <b>The one part of this port with no counterpart to translate.</b> The WPF reader
/// builds a <c>FlowDocument</c> and hands it to a <c>FlowDocumentScrollViewer</c>;
/// Avalonia has neither, and nothing equivalent. So instead of a document this
/// produces a stack of ordinary controls — one per block — inside a
/// <c>ScrollViewer</c>.
/// </para>
/// <para>
/// What is kept identical is every visual decision the WPF builder made: heading
/// sizes 20/17/15.5/14.5, the 3px quote bar in #D1D5DB with #4B5563 text, the #F3F4F6
/// code background, the 420px image ceiling, and alt text shown first and replaced
/// only if the picture loads. Those numbers are copied deliberately rather than
/// re-chosen, so the two readers show operators' announcements the same way.
/// </para>
/// <para>
/// Inline runs use <see cref="InlineCollection"/> on a <see cref="SelectableTextBlock"/>,
/// which is the closest Avalonia equivalent to a WPF <c>Paragraph</c>'s inlines and
/// keeps announcement text selectable — users copy order numbers and URLs out of these.
/// Links are ordinary runs too; see <see cref="AttachLinkHandling"/> for how they are
/// made clickable without giving that up.
/// </para>
/// </remarks>
internal static class AnnouncementContentBuilder
{
    private static readonly FontFamily MonospaceFont =
        FontFamily.Parse("Consolas, Cascadia Mono, Menlo, Courier New, monospace");

    /// <summary>Ceiling on how wide an image is drawn, so a large upload cannot push the layout out.</summary>
    private const double MaxImageWidth = 420;

    private static readonly IBrush CodeBackground = new SolidColorBrush(Color.FromRgb(0xF3, 0xF4, 0xF6));
    private static readonly IBrush QuoteBar = new SolidColorBrush(Color.FromRgb(0xD1, 0xD5, 0xDB));
    private static readonly IBrush QuoteText = new SolidColorBrush(Color.FromRgb(0x4B, 0x55, 0x63));
    private static readonly IBrush MutedText = new SolidColorBrush(Color.FromRgb(0x9C, 0xA3, 0xAF));
    private static readonly IBrush LinkText = new SolidColorBrush(Color.FromRgb(0x25, 0x63, 0xEB));

    public static Control Build(
        string? markdown,
        Uri baseUri,
        IAnnouncementImageLoader? imageLoader,
        Action<Uri>? onNavigate)
    {
        ArgumentNullException.ThrowIfNull(baseUri);

        var stack = new StackPanel();

        foreach (AnnouncementNode node in AnnouncementMarkdownParser.Parse(markdown))
        {
            switch (node)
            {
                case AnnouncementParagraph paragraph:
                    stack.Children.Add(BuildParagraph(paragraph, baseUri, onNavigate));
                    break;

                case AnnouncementListNode list:
                    foreach (AnnouncementListItem item in list.Items)
                    {
                        Control line = BuildParagraph(
                            item.Content,
                            baseUri,
                            onNavigate,
                            markerPrefix: item.Marker + " ");
                        line.Margin = new Thickness(16, 2, 0, 2);
                        stack.Children.Add(line);
                    }

                    break;

                case AnnouncementImageNode image:
                    stack.Children.Add(BuildImage(image, imageLoader));
                    break;

                case AnnouncementCodeNode code:
                    stack.Children.Add(new Border
                    {
                        Background = CodeBackground,
                        Padding = new Thickness(10, 8, 10, 8),
                        Margin = new Thickness(0, 6, 0, 6),
                        Child = new SelectableTextBlock
                        {
                            Text = code.Text,
                            FontFamily = MonospaceFont,
                            FontSize = 12.5,
                            TextWrapping = TextWrapping.Wrap,
                        },
                    });
                    break;

                case AnnouncementDividerNode:
                    stack.Children.Add(new Border
                    {
                        Height = 1,
                        Background = QuoteBar,
                        Margin = new Thickness(0, 8, 0, 8),
                    });
                    break;
            }
        }

        if (stack.Children.Count == 0)
        {
            stack.Children.Add(new TextBlock
            {
                Text = "（本条公告没有正文）",
                Foreground = MutedText,
            });
        }

        return stack;
    }

    private static Control BuildParagraph(
        AnnouncementParagraph source,
        Uri baseUri,
        Action<Uri>? onNavigate,
        string? markerPrefix = null)
    {
        var text = new SelectableTextBlock
        {
            TextWrapping = TextWrapping.Wrap,
            FontSize = 14,
            LineHeight = 24,
        };

        // Character ranges that are links, in the order they appear. Used by the
        // hit-test below to turn a click position back into a URL.
        var links = new List<(int Start, int Length, Uri Target)>();
        int offset = 0;

        if (markerPrefix is not null)
        {
            text.Inlines!.Add(new Run(markerPrefix));
            offset += markerPrefix.Length;
        }

        if (source.HeadingLevel > 0)
        {
            text.FontSize = source.HeadingLevel switch
            {
                1 => 20,
                2 => 17,
                3 => 15.5,
                _ => 14.5,
            };
            text.FontWeight = FontWeight.SemiBold;
        }

        foreach (AnnouncementRun run in source.Runs)
        {
            text.Inlines!.Add(BuildInline(run, baseUri));

            if (run.LinkUrl is not null && Uri.TryCreate(baseUri, run.LinkUrl, out Uri? target))
            {
                links.Add((offset, run.Text.Length, target));
            }

            offset += run.Text.Length;
        }

        if (links.Count > 0 && onNavigate is not null)
        {
            AttachLinkHandling(text, links, onNavigate);
        }

        Thickness margin = source.HeadingLevel > 0
            ? new Thickness(0, 10, 0, 4)
            : new Thickness(0, 4, 0, 4);

        if (!source.Quoted)
        {
            text.Margin = margin;
            return text;
        }

        // A quote is a left bar plus indented, dimmed text. WPF put the border on the
        // paragraph itself; Avalonia text blocks have no border, so it becomes a Border
        // around the text — same appearance, one more element.
        text.Foreground = QuoteText;
        return new Border
        {
            BorderBrush = QuoteBar,
            BorderThickness = new Thickness(3, 0, 0, 0),
            Padding = new Thickness(10, 0, 0, 0),
            Margin = margin,
            Child = text,
        };
    }

    private static Inline BuildInline(AnnouncementRun source, Uri baseUri)
    {
        var run = new Run(source.Text);

        if (source.Bold)
        {
            run.FontWeight = FontWeight.SemiBold;
        }

        if (source.Italic)
        {
            run.FontStyle = FontStyle.Italic;
        }

        if (source.Code)
        {
            run.FontFamily = MonospaceFont;
            run.Background = CodeBackground;
        }

        if (source.LinkUrl is null || !Uri.TryCreate(baseUri, source.LinkUrl, out _))
        {
            return run;
        }

        run.Foreground = LinkText;
        run.TextDecorations = TextDecorations.Underline;
        return run;
    }

    /// <summary>Makes the link ranges in a block clickable.</summary>
    /// <remarks>
    /// <para>
    /// Avalonia has no <c>Hyperlink</c> inline and no pointer events on <c>Run</c>, so a
    /// click has to be resolved from its position. The first attempt hosted each link in
    /// an <c>InlineUIContainer</c>, which does receive clicks — but a hosted control does
    /// not sit on the surrounding text's baseline (neither <c>Baseline</c> nor
    /// <c>TextBottom</c> alignment fixed it), so every link floated a few pixels above
    /// the words either side of it, and its text dropped out of the block's selection.
    /// </para>
    /// <para>
    /// Hit-testing the laid-out text avoids both problems: links stay ordinary runs, so
    /// they sit on the baseline and remain selectable, and the click is mapped back to a
    /// character index and then to whichever range covers it.
    /// </para>
    /// <para>
    /// Handled on the tunnelling route so it runs before the block's own selection
    /// handling, and only when the press actually lands inside the text.
    /// </para>
    /// </remarks>
    private static void AttachLinkHandling(
        SelectableTextBlock text,
        List<(int Start, int Length, Uri Target)> links,
        Action<Uri> onNavigate)
    {
        Uri? HitTest(Point position)
        {
            TextHitTestResult hit = text.TextLayout.HitTestPoint(position);
            if (!hit.IsInside)
            {
                return null;
            }

            foreach ((int start, int length, Uri target) in links)
            {
                if (hit.TextPosition >= start && hit.TextPosition < start + length)
                {
                    return target;
                }
            }

            return null;
        }

        text.PointerMoved += (_, args) =>
            text.Cursor = HitTest(args.GetPosition(text)) is null
                ? Cursor.Default
                : new Cursor(StandardCursorType.Hand);

        text.AddHandler(
            InputElement.PointerPressedEvent,
            (_, args) =>
            {
                if (!args.GetCurrentPoint(text).Properties.IsLeftButtonPressed)
                {
                    return;
                }

                if (HitTest(args.GetPosition(text)) is { } target)
                {
                    onNavigate(target);
                    args.Handled = true;
                }
            },
            RoutingStrategies.Tunnel);
    }

    /// <summary>
    /// Lays out an image as its alt text first, replaced by the picture if it loads.
    /// </summary>
    /// <remarks>
    /// Alt-first rather than a spinner-then-nothing: if the load fails for any of the
    /// reasons <see cref="IAnnouncementImageLoader"/> tolerates, what is left on screen
    /// already says what the picture was supposed to be.
    /// </remarks>
    private static Control BuildImage(AnnouncementImageNode source, IAnnouncementImageLoader? imageLoader)
    {
        var placeholder = new TextBlock
        {
            Text = source.AltText,
            Foreground = MutedText,
            FontSize = 12.5,
            TextWrapping = TextWrapping.Wrap,
        };

        var picture = new Image
        {
            MaxWidth = MaxImageWidth,
            Stretch = Stretch.Uniform,
            StretchDirection = StretchDirection.DownOnly,
            HorizontalAlignment = HorizontalAlignment.Left,
            IsVisible = false,
        };

        var host = new StackPanel { Margin = new Thickness(0, 6, 0, 6) };
        host.Children.Add(picture);
        host.Children.Add(placeholder);

        if (imageLoader is not null)
        {
            _ = LoadIntoAsync(imageLoader, source.Source, picture, placeholder);
        }

        return host;
    }

    private static async Task LoadIntoAsync(
        IAnnouncementImageLoader imageLoader,
        string source,
        Image picture,
        TextBlock placeholder)
    {
        // LoadAsync is documented never to throw, so this fire-and-forget cannot leave
        // an unobserved fault behind.
        byte[]? bytes = await imageLoader.LoadAsync(source).ConfigureAwait(false);
        if (bytes is null || bytes.Length == 0)
        {
            return;
        }

        Bitmap bitmap;
        try
        {
            using var stream = new MemoryStream(bytes, writable: false);
            bitmap = new Bitmap(stream);
        }
        catch (Exception ex) when (ex is ArgumentException or NotSupportedException or InvalidOperationException)
        {
            // Not an image, or a codec this machine lacks. The alt text stays, which is
            // the whole reason it is drawn first. Throwing here would take the reader
            // down over one broken image in one announcement — and this decode moved
            // out of the loader, so the guard had to move with it.
            ClientLog.Warning("公告图片无法解码", ex);
            return;
        }

        await Dispatcher.UIThread.InvokeAsync(() =>
        {
            picture.Source = bitmap;
            picture.IsVisible = true;
            placeholder.IsVisible = false;
        });
    }
}
