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

    [Fact]
    public void CardsTooCloseMoveRightAndStayLevelWithTheirMessages()
    {
        var cards = new[] { (X: 600, Y: 100, Item: "a"), (X: 600, Y: 120, Item: "b"), (X: 600, Y: 140, Item: "c"), (X: 600, Y: 160, Item: "d") };

        var placed = InlinePlacement.Arrange(cards, width: 220, height: 44, gap: 4);

        Assert.Equal([("a", 600, 100), ("b", 824, 120), ("c", 1048, 140), ("d", 600, 160)], placed.Select(p => (p.Item, p.X, p.Y)));
    }

    [Fact]
    public void ALargerCardIsKeptClearOfByItsOwnSize()
    {
        // The card still waiting for a reply is larger: a compact one under it must clear its full height.
        var cards = new[] { (X: 600, Y: 100, Width: 300, Height: 84, Item: "waiting"), (X: 600, Y: 170, Width: 220, Height: 44, Item: "answered") };

        var placed = InlinePlacement.Arrange(cards, gap: 4);

        Assert.Equal([("waiting", 600), ("answered", 904)], placed.Select(p => (p.Item, p.X)));
    }

    [Fact]
    public void CardsFarEnoughApartAreNotMoved()
    {
        var cards = new[] { (X: 600, Y: 100, Item: "a"), (X: 640, Y: 148, Item: "b"), (X: 100, Y: 110, Item: "c") };

        var placed = InlinePlacement.Arrange(cards, width: 220, height: 44, gap: 4);

        Assert.Equal([("a", 600), ("c", 100), ("b", 640)], placed.Select(p => (p.Item, p.X)));
    }
}
