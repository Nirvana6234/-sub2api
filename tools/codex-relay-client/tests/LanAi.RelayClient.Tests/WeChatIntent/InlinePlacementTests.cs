using LanAi.RelayClient.WeChatIntent;
using Xunit;
using static LanAi.RelayClient.Tests.WeChatIntent.ChatScreenParserTests;

namespace LanAi.RelayClient.Tests.WeChatIntent;

public sealed class InlinePlacementTests
{
    private static AnchoredCard Card(string text) =>
        new(new ChatItem(ChatSpeaker.Them, text), new IntentCard { About = text });

    [Fact]
    public void TheParserReportsWhereEachBubbleIs()
    {
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明",
            Them(187, "在吗", w: 60),
            Them(243, "第一行", w: 300),
            Them(262, "第二行", w: 200)), 1.0);

        Assert.Equal(2, screen.Bounds.Count);
        Assert.Equal(new BubbleBounds(383, 187, 443, 200), screen.Bounds[0]);
        Assert.Equal(new BubbleBounds(383, 243, 683, 275), screen.Bounds[1]);
        Assert.Equal(1148, screen.AreaRight);
    }

    [Fact]
    public void EachCardGoesToItsOwnMessageWhereverItIsNow()
    {
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明",
            Them(187, "那你说"), Me(250, "我想说完整一点"), Them(320, "你最好是"), Me(400, "我想起来了")), 1.0);

        IReadOnlyList<PlacedCard> placed = InlinePlacement.Place([Card("那你说"), Card("你最好是")], screen);

        Assert.Equal([187, 320], placed.Select(p => p.Bubble.Top));
        Assert.Equal([false, true], placed.Select(p => p.IsLatest));
    }

    [Fact]
    public void ACardWhoseMessageScrolledAwayIsNotShown()
    {
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明", Them(187, "你最好是"), Me(250, "好的")), 1.0);

        IReadOnlyList<PlacedCard> placed = InlinePlacement.Place([Card("那你说"), Card("你最好是")], screen);

        Assert.Equal(["你最好是"], placed.Select(p => p.Card.Anchor.Text));
    }

    [Fact]
    public void TwoCardsForTheSameShortTextGoToTwoBubblesNewestLowest()
    {
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明", Them(187, "好"), Me(250, "几点"), Them(320, "好")), 1.0);

        IReadOnlyList<PlacedCard> placed = InlinePlacement.Place([Card("好"), Card("好")], screen);

        Assert.Equal([187, 320], placed.Select(p => p.Bubble.Top));
        Assert.True(placed[1].IsLatest);
    }

    [Fact]
    public void TheUsersOwnMessagesNeverCarryACard()
    {
        ChatScreen screen = ChatScreenParser.Parse(Frame("小明", Me(187, "在吗"), Them(250, "嗯")), 1.0);

        Assert.Empty(InlinePlacement.Place([Card("在吗")], screen));
    }
}
