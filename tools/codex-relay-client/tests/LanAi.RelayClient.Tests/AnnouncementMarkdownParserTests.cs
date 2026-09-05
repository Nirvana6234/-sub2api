using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class AnnouncementMarkdownParserTests
{
    [Fact]
    public void EmptyBodyProducesNothing()
    {
        Assert.Empty(AnnouncementMarkdownParser.Parse(null));
        Assert.Empty(AnnouncementMarkdownParser.Parse("   "));
    }

    [Fact]
    public void HeadingsKeepTheirLevel()
    {
        IReadOnlyList<AnnouncementNode> nodes = AnnouncementMarkdownParser.Parse("## 系统维护");

        AnnouncementParagraph heading = Assert.IsType<AnnouncementParagraph>(Assert.Single(nodes));
        Assert.Equal(2, heading.HeadingLevel);
        Assert.Equal("系统维护", heading.PlainText);
    }

    [Fact]
    public void EmphasisBecomesStyledRuns()
    {
        IReadOnlyList<AnnouncementNode> nodes = AnnouncementMarkdownParser.Parse("请**立即**处理，*谢谢*");

        AnnouncementParagraph paragraph = Assert.IsType<AnnouncementParagraph>(Assert.Single(nodes));
        Assert.Contains(paragraph.Runs, run => run.Text == "立即" && run.Bold);
        Assert.Contains(paragraph.Runs, run => run.Text == "谢谢" && run.Italic);
    }

    [Fact]
    public void LinkTextCarriesItsTarget()
    {
        IReadOnlyList<AnnouncementNode> nodes =
            AnnouncementMarkdownParser.Parse("详见[帮助页](https://example.com/help)");

        AnnouncementParagraph paragraph = Assert.IsType<AnnouncementParagraph>(Assert.Single(nodes));
        Assert.Contains(paragraph.Runs, run => run.Text == "帮助页" && run.LinkUrl == "https://example.com/help");
    }

    [Fact]
    public void ImagesBecomeTheirOwnNodeAndKeepTheirSourceVerbatim()
    {
        IReadOnlyList<AnnouncementNode> nodes =
            AnnouncementMarkdownParser.Parse("扫码加群：\n\n![客服二维码](/uploads/qr.png)");

        AnnouncementImageNode image = Assert.Single(nodes.OfType<AnnouncementImageNode>());

        // Left relative on purpose: resolving against the relay base address is the
        // image loader's job, and it is the common case for uploaded images.
        Assert.Equal("/uploads/qr.png", image.Source);
        Assert.Equal("客服二维码", image.AltText);
    }

    [Fact]
    public void AnImageWrittenInsideALineIsLiftedOutOfIt()
    {
        IReadOnlyList<AnnouncementNode> nodes =
            AnnouncementMarkdownParser.Parse("前面 ![图](a.png) 后面");

        Assert.Collection(
            nodes,
            node => Assert.Equal("前面 ", Assert.IsType<AnnouncementParagraph>(node).PlainText),
            node => Assert.Equal("a.png", Assert.IsType<AnnouncementImageNode>(node).Source),
            node => Assert.Equal(" 后面", Assert.IsType<AnnouncementParagraph>(node).PlainText));
    }

    [Fact]
    public void AnImageWithNoAltTextStillHasSomethingToShow()
    {
        IReadOnlyList<AnnouncementNode> nodes = AnnouncementMarkdownParser.Parse("![](a.png)");

        Assert.Equal("[图片]", Assert.Single(nodes.OfType<AnnouncementImageNode>()).AltText);
    }

    [Fact]
    public void ListItemsKeepTheirMarkers()
    {
        IReadOnlyList<AnnouncementNode> nodes = AnnouncementMarkdownParser.Parse("- 甲\n- 乙");

        AnnouncementListNode list = Assert.Single(nodes.OfType<AnnouncementListNode>());
        Assert.Equal(["•", "•"], list.Items.Select(item => item.Marker));
        Assert.Equal(["甲", "乙"], list.Items.Select(item => item.Content.PlainText));
    }

    [Fact]
    public void OrderedListsNumberFromTheirStart()
    {
        IReadOnlyList<AnnouncementNode> nodes = AnnouncementMarkdownParser.Parse("3. 甲\n4. 乙");

        AnnouncementListNode list = Assert.Single(nodes.OfType<AnnouncementListNode>());
        Assert.Equal(["3.", "4."], list.Items.Select(item => item.Marker));
    }

    [Fact]
    public void FencedCodeKeepsItsLines()
    {
        IReadOnlyList<AnnouncementNode> nodes =
            AnnouncementMarkdownParser.Parse("```\nline one\nline two\n```");

        Assert.Equal("line one\nline two", Assert.Single(nodes.OfType<AnnouncementCodeNode>()).Text);
    }

    [Fact]
    public void QuotesAreMarkedAsQuoted()
    {
        IReadOnlyList<AnnouncementNode> nodes = AnnouncementMarkdownParser.Parse("> 请注意");

        Assert.True(Assert.IsType<AnnouncementParagraph>(Assert.Single(nodes)).Quoted);
    }

    /// <remarks>
    /// The rule that matters most: markup this parser does not model must survive
    /// as text. Dropping it would leave a 公告 that reads correctly on the web
    /// quietly missing a sentence here, with nothing on screen to say so.
    /// </remarks>
    [Fact]
    public void RawHtmlSurvivesAsLiteralTextInsteadOfBeingDropped()
    {
        IReadOnlyList<AnnouncementNode> nodes =
            AnnouncementMarkdownParser.Parse("<div>联系客服 QQ 12345</div>");

        Assert.Contains(
            nodes.OfType<AnnouncementParagraph>(),
            paragraph => paragraph.PlainText.Contains("联系客服 QQ 12345", StringComparison.Ordinal));
    }

    [Fact]
    public void TableSyntaxIsNotSilentlyLost()
    {
        IReadOnlyList<AnnouncementNode> nodes =
            AnnouncementMarkdownParser.Parse("| 项目 | 价格 |\n| --- | --- |\n| 套餐A | 50 |");

        string text = string.Concat(nodes.OfType<AnnouncementParagraph>().Select(p => p.PlainText));
        Assert.Contains("套餐A", text, StringComparison.Ordinal);
        Assert.Contains("50", text, StringComparison.Ordinal);
    }

    [Fact]
    public void HardBreaksMoveToANewLineAndSoftBreaksDoNot()
    {
        AnnouncementParagraph soft = Assert.IsType<AnnouncementParagraph>(
            Assert.Single(AnnouncementMarkdownParser.Parse("第一行\n第二行")));
        Assert.DoesNotContain('\n', soft.PlainText);

        AnnouncementParagraph hard = Assert.IsType<AnnouncementParagraph>(
            Assert.Single(AnnouncementMarkdownParser.Parse("第一行  \n第二行")));
        Assert.Contains('\n', hard.PlainText);
    }
}
