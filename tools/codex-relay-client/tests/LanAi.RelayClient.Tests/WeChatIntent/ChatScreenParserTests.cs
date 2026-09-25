using LanAi.RelayClient.WeChatIntent;
using Xunit;

namespace LanAi.RelayClient.Tests.WeChatIntent;

/// <summary>
/// Line geometry and colours are the ones measured on WeChat 4.1.13 (docs §4.5); the text is
/// made up. Area: x 302..1148, y 89..677; the other person's bubbles grey, the user's green.
/// </summary>
public sealed class ChatScreenParserTests
{
    private static readonly int[] Grey = [236, 236, 240];
    private static readonly int[] Green = [160, 240, 164];
    private static readonly int[] Background = [248, 248, 248];

    internal static ReaderEvent Frame(string title, params ReaderLine[] lines) => new()
    {
        Type = ReaderEvent.Frame,
        Title = title,
        Area = new ReaderRect { X = 302, Y = 89, W = 846, H = 589 },
        Background = Background,
        Lines = lines,
    };

    internal static ReaderLine Them(int y, string text, int x = 383, int w = 200) => new() { Text = text, X = x, Y = y, W = w, H = 13, Bg = Grey };

    internal static ReaderLine Me(int y, string text, int right = 1075, int w = 200) => new() { Text = text, X = right - w, Y = y, W = w, H = 13, Bg = Green };

    internal static ReaderLine Stamp(int y, string text) => new() { Text = text, X = 710, Y = y, W = 30, H = 9, Bg = Background };

    [Fact]
    public void SpeakersComeFromBubbleColourAndTimeStampsFromCentredBackgroundText()
    {
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明",
            Them(187, "今天下班路上看到一只猫"),
            Stamp(300, "19:12"),
            Me(378, "发来看看"),
            Them(475, "等下给你拍")), 1.0);

        Assert.Equal("小明", screen.Title);
        Assert.False(screen.IsGroup);
        Assert.Equal(
            [(ChatSpeaker.Them, "今天下班路上看到一只猫"), (ChatSpeaker.Time, "19:12"), (ChatSpeaker.Me, "发来看看"), (ChatSpeaker.Them, "等下给你拍")],
            screen.Items.Select(i => (i.Speaker, i.Text)));
    }

    [Fact]
    public void WrappedLinesOfOneBubbleAreJoined()
    {
        // Measured: two lines of one bubble at y 243 and 262, 13 px high.
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明",
            Them(187, "先说一句"),
            Them(243, "这是一段很长的话第一行"),
            Them(262, "接着是第二行")), 1.0);

        Assert.Equal(["先说一句", "这是一段很长的话第一行接着是第二行"], screen.Items.Select(i => i.Text));
    }

    [Fact]
    public void ABubbleCutByTheTopEdgeIsLeftOut()
    {
        // Measured: a cut bubble's text sat 10 px under the list's top edge.
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明",
            Me(99, "被截掉一半的"),
            Them(187, "完整的消息")), 1.0);

        Assert.Equal(["完整的消息"], screen.Items.Select(i => i.Text));
    }

    [Fact]
    public void ABubbleCutByTheBottomEdgeIsLeftOut()
    {
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明",
            Them(300, "完整的消息"),
            Them(670, "下面被截断")), 1.0);

        Assert.Equal(["完整的消息"], screen.Items.Select(i => i.Text));
    }

    [Fact]
    public void AMessageLongerThanTheScreenIsKeptAndMarkedPartial()
    {
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明",
            Them(95, "从上面就开始了"),
            Them(114, "一直写到"),
            Them(133, "最下面")), 1.0);

        ChatItem only = Assert.Single(screen.Items);
        Assert.True(only.Partial);
    }

    [Fact]
    public void StickerGarbageAndQuotedRepliesAreDropped()
    {
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明",
            Them(187, "@#"),
            new ReaderLine { Text = "小明：引用的原话", X = 383, Y = 230, W = 120, H = 12, Bg = Background },
            Them(300, "好")), 1.0);

        Assert.Equal(["好"], screen.Items.Select(i => i.Text));
    }

    [Theory]
    [InlineData("家人群(12)", true)]
    [InlineData("项目组（35）", true)]
    [InlineData("小明", false)]
    [InlineData("小明(备注)", false)]
    public void AGroupIsRecognisedByTheMemberCountInItsTitle(string title, bool group) =>
        Assert.Equal(group, ChatScreenParser.IsGroupTitle(title));

    [Fact]
    public void ALineOfUnknownColourIsNotGuessedAt()
    {
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明",
            new ReaderLine { Text = "深色模式的气泡", X = 383, Y = 300, W = 120, H = 13, Bg = [60, 60, 60] }), 1.0);

        Assert.Empty(screen.Items);
    }
}
