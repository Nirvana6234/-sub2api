using System.Text;
using Markdig;
using Markdig.Syntax;
using Markdig.Syntax.Inlines;

namespace LanAi.RelayClient.Services;

/// <summary>One piece of a rendered announcement.</summary>
internal abstract record AnnouncementNode;

/// <summary>A run of text sharing one set of styles.</summary>
/// <param name="LinkUrl">Set when the run is part of a hyperlink label.</param>
internal sealed record AnnouncementRun(
    string Text,
    bool Bold = false,
    bool Italic = false,
    bool Code = false,
    string? LinkUrl = null);

/// <param name="HeadingLevel">1-6 for a heading, 0 for body text.</param>
/// <param name="Quoted">True when the text came from a block quote.</param>
internal sealed record AnnouncementParagraph(
    IReadOnlyList<AnnouncementRun> Runs,
    int HeadingLevel = 0,
    bool Quoted = false) : AnnouncementNode
{
    public string PlainText => string.Concat(Runs.Select(run => run.Text));
}

/// <param name="Source">The image reference exactly as written; may be relative.</param>
/// <param name="AltText">What to show instead when the image cannot be loaded.</param>
internal sealed record AnnouncementImageNode(string Source, string AltText) : AnnouncementNode;

/// <param name="Marker">The bullet or number shown in front of the item.</param>
internal sealed record AnnouncementListItem(string Marker, AnnouncementParagraph Content);

internal sealed record AnnouncementListNode(IReadOnlyList<AnnouncementListItem> Items) : AnnouncementNode;

internal sealed record AnnouncementCodeNode(string Text) : AnnouncementNode;

internal sealed record AnnouncementDividerNode : AnnouncementNode;

/// <summary>
/// Turns announcement markdown into a small display model.
/// </summary>
/// <remarks>
/// <para>
/// Split from the WPF rendering on purpose. This half holds every decision worth
/// testing — which nodes are understood, how images are lifted out of paragraphs,
/// what happens to markup nobody mapped — and it can be tested without a
/// dispatcher or an STA thread, neither of which this test project provides.
/// </para>
/// <para>
/// Markdig parses; the mapping to display nodes is ours. The alternative,
/// Markdig.Wpf, brings its own resource dictionary and would fight the styling
/// this client already has.
/// </para>
/// </remarks>
internal static class AnnouncementMarkdownParser
{
    /// <remarks>
    /// A bare pipeline: no extension is enabled that this parser does not map.
    /// Anything it therefore fails to recognise — tables, raw HTML — survives as
    /// its own source text through <see cref="AppendLiteralBlock"/>, which is the
    /// point. A 公告 that reads correctly on the web must never come out of here
    /// with a sentence missing.
    /// </remarks>
    private static readonly MarkdownPipeline Pipeline = new MarkdownPipelineBuilder().Build();

    public static IReadOnlyList<AnnouncementNode> Parse(string? markdown)
    {
        if (string.IsNullOrWhiteSpace(markdown))
        {
            return [];
        }

        var nodes = new List<AnnouncementNode>();

        MarkdownDocument document;
        try
        {
            document = Markdown.Parse(markdown, Pipeline);
        }
        catch (Exception exception) when (exception is not (OutOfMemoryException or StackOverflowException))
        {
            // Unreadable markup still has to be readable content: the raw body is
            // worth more to the user than an empty window.
            ClientLog.Warning("公告正文解析失败，按纯文本显示", exception);
            return [new AnnouncementParagraph([new AnnouncementRun(markdown)])];
        }

        AppendBlocks(document, markdown, nodes, quoted: false);
        return nodes;
    }

    private static void AppendBlocks(
        ContainerBlock container,
        string source,
        List<AnnouncementNode> nodes,
        bool quoted)
    {
        foreach (Block block in container)
        {
            switch (block)
            {
                case HeadingBlock heading:
                    AppendInlineBlock(heading.Inline, nodes, heading.Level, quoted);
                    break;

                case ParagraphBlock paragraph:
                    AppendInlineBlock(paragraph.Inline, nodes, headingLevel: 0, quoted);
                    break;

                case QuoteBlock quote:
                    AppendBlocks(quote, source, nodes, quoted: true);
                    break;

                case ListBlock list:
                    AppendList(list, source, nodes, quoted);
                    break;

                case CodeBlock code:
                    AppendCode(code, nodes);
                    break;

                case ThematicBreakBlock:
                    nodes.Add(new AnnouncementDividerNode());
                    break;

                case LinkReferenceDefinitionGroup:
                    // Definitions produce no output of their own; their targets have
                    // already been resolved into the links that reference them.
                    break;

                default:
                    AppendLiteralBlock(block, source, nodes, quoted);
                    break;
            }
        }
    }

    private static void AppendList(
        ListBlock list,
        string source,
        List<AnnouncementNode> nodes,
        bool quoted)
    {
        var items = new List<AnnouncementListItem>();
        int ordinal = list.IsOrdered && int.TryParse(list.OrderedStart, out int start) ? start : 1;

        foreach (Block child in list)
        {
            if (child is not ListItemBlock item)
            {
                AppendLiteralBlock(child, source, nodes, quoted);
                continue;
            }

            // An item's own blocks are flattened into the item line, and anything
            // that is not a paragraph — a nested list, an image, a code block —
            // is emitted after it rather than dropped.
            var itemNodes = new List<AnnouncementNode>();
            AppendBlocks(item, source, itemNodes, quoted);

            AnnouncementParagraph content = itemNodes.OfType<AnnouncementParagraph>().FirstOrDefault()
                                            ?? new AnnouncementParagraph([]);

            string marker = list.IsOrdered ? $"{ordinal}." : "•";
            ordinal++;
            items.Add(new AnnouncementListItem(marker, content));

            foreach (AnnouncementNode extra in itemNodes.Where(node => !ReferenceEquals(node, content)))
            {
                if (items.Count > 0)
                {
                    nodes.Add(new AnnouncementListNode([.. items]));
                    items.Clear();
                }

                nodes.Add(extra);
            }
        }

        if (items.Count > 0)
        {
            nodes.Add(new AnnouncementListNode(items));
        }
    }

    private static void AppendCode(CodeBlock code, List<AnnouncementNode> nodes)
    {
        var text = new StringBuilder();
        for (int i = 0; i < code.Lines.Count; i++)
        {
            // '\n' rather than AppendLine: the display model must not vary with the
            // host's line ending, and hard breaks in body text already use '\n'.
            text.Append(code.Lines.Lines[i].Slice.ToString()).Append('\n');
        }

        string body = text.ToString().TrimEnd('\r', '\n');
        if (body.Length > 0)
        {
            nodes.Add(new AnnouncementCodeNode(body));
        }
    }

    /// <summary>
    /// Emits markup this parser does not model as its own source text.
    /// </summary>
    /// <remarks>
    /// Dropping it would be worse than rendering it plainly: a table or a raw
    /// HTML block that vanishes leaves the user reading an announcement that is
    /// quietly missing part of what the operator wrote, with nothing on screen to
    /// say so.
    /// </remarks>
    private static void AppendLiteralBlock(
        Block block,
        string source,
        List<AnnouncementNode> nodes,
        bool quoted)
    {
        if (block.Span.Start < 0 || block.Span.End < block.Span.Start || block.Span.End >= source.Length)
        {
            return;
        }

        string literal = source[block.Span.Start..(block.Span.End + 1)].Trim();
        if (literal.Length > 0)
        {
            nodes.Add(new AnnouncementParagraph([new AnnouncementRun(literal)], HeadingLevel: 0, Quoted: quoted));
        }
    }

    /// <summary>
    /// Walks one paragraph's inlines, splitting it wherever an image appears.
    /// </summary>
    /// <remarks>
    /// Images are lifted to their own node even when written inline. A relay
    /// announcement's images are QR codes and screenshots — content that wants
    /// its own line — and hosting them inside a text line in a FlowDocument
    /// costs baseline alignment work for a case that does not occur.
    /// </remarks>
    private static void AppendInlineBlock(
        ContainerInline? inline,
        List<AnnouncementNode> nodes,
        int headingLevel,
        bool quoted)
    {
        if (inline is null)
        {
            return;
        }

        var runs = new List<AnnouncementRun>();
        AppendInlines(inline, runs, nodes, new RunStyle(), headingLevel, quoted);
        FlushRuns(runs, nodes, headingLevel, quoted);
    }

    private static void AppendInlines(
        ContainerInline container,
        List<AnnouncementRun> runs,
        List<AnnouncementNode> nodes,
        RunStyle style,
        int headingLevel,
        bool quoted)
    {
        foreach (Inline inline in container)
        {
            switch (inline)
            {
                case LiteralInline literal:
                    AddRun(runs, literal.Content.ToString(), style);
                    break;

                case EmphasisInline emphasis:
                    AppendInlines(
                        emphasis,
                        runs,
                        nodes,
                        emphasis.DelimiterCount >= 2 ? style with { Bold = true } : style with { Italic = true },
                        headingLevel,
                        quoted);
                    break;

                case CodeInline code:
                    AddRun(runs, code.Content ?? string.Empty, style with { Code = true });
                    break;

                case LinkInline { IsImage: true } image:
                    FlushRuns(runs, nodes, headingLevel, quoted);
                    nodes.Add(new AnnouncementImageNode(
                        image.Url ?? string.Empty,
                        FlattenText(image) is { Length: > 0 } alt ? alt : "[图片]"));
                    break;

                case LinkInline link:
                    AppendInlines(link, runs, nodes, style with { LinkUrl = link.Url }, headingLevel, quoted);
                    break;

                case AutolinkInline autolink:
                    AddRun(runs, autolink.Url ?? string.Empty, style with { LinkUrl = autolink.Url });
                    break;

                case LineBreakInline lineBreak:
                    // A soft break is a space, matching how the same body renders in
                    // the web panel; only an explicit hard break moves to a new line.
                    AddRun(runs, lineBreak.IsHard ? "\n" : " ", style);
                    break;

                case ContainerInline nested:
                    AppendInlines(nested, runs, nodes, style, headingLevel, quoted);
                    break;

                default:
                    // Raw HTML and anything else unmapped keeps its source text, for
                    // the same reason unmapped blocks do.
                    AddRun(runs, inline.ToString() ?? string.Empty, style);
                    break;
            }
        }
    }

    private static void AddRun(List<AnnouncementRun> runs, string text, RunStyle style)
    {
        if (text.Length == 0)
        {
            return;
        }

        runs.Add(new AnnouncementRun(text, style.Bold, style.Italic, style.Code, style.LinkUrl));
    }

    private static void FlushRuns(
        List<AnnouncementRun> runs,
        List<AnnouncementNode> nodes,
        int headingLevel,
        bool quoted)
    {
        if (runs.Count == 0)
        {
            return;
        }

        nodes.Add(new AnnouncementParagraph([.. runs], headingLevel, quoted));
        runs.Clear();
    }

    private static string FlattenText(ContainerInline container)
    {
        var text = new StringBuilder();
        foreach (Inline inline in container)
        {
            switch (inline)
            {
                case LiteralInline literal:
                    text.Append(literal.Content.ToString());
                    break;
                case CodeInline code:
                    text.Append(code.Content);
                    break;
                case ContainerInline nested:
                    text.Append(FlattenText(nested));
                    break;
            }
        }

        return text.ToString().Trim();
    }

    private readonly record struct RunStyle(
        bool Bold = false,
        bool Italic = false,
        bool Code = false,
        string? LinkUrl = null);
}
