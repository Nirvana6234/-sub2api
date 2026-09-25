using LanAi.RelayClient.WeChatIntent;
using Xunit;

namespace LanAi.RelayClient.Tests.WeChatIntent;

/// <summary>
/// Scrolling (docs §4.5 item 5): a made-up conversation is cut into a series of "screens",
/// the way WeChat's list shows it while it scrolls and while new messages push it up.
/// </summary>
public sealed class ChatTranscriptTests
{
    private static ChatItem T(string text) => new(ChatSpeaker.Them, text);

    private static ChatItem M(string text) => new(ChatSpeaker.Me, text);

    private static ChatItem Stamp(string text) => new(ChatSpeaker.Time, text);

    private static ChatScreen Screen(params ChatItem[] items) => new("小明", false, items);

    private static readonly ChatItem[] Conversation =
    [
        Stamp("18:30"), T("在吗"), M("在"), T("晚上吃什么"), M("都行"), T("你每次都说都行"),
        M("那吃火锅"), T("好"), M("几点"), T("七点吧"), M("好"), T("好"),
    ];

    [Fact]
    public void FirstSightJudgesTheTrailingBatchFromThemOnce()
    {
        var transcript = new ChatTranscript();

        TranscriptUpdate update = transcript.Apply(Screen(M("那吃火锅"), T("好"), T("七点吧")));

        Assert.True(update.FirstSight);
        Assert.Equal(["好", "七点吧"], update.NewFromThem.Select(i => i.Text));
        Assert.Equal(TranscriptUpdate.Nothing, transcript.Apply(Screen(M("那吃火锅"), T("好"), T("七点吧"))));
    }

    [Fact]
    public void FirstSightEndingWithTheUserJudgesNothing()
    {
        TranscriptUpdate update = new ChatTranscript().Apply(Screen(T("在吗"), M("在")));

        Assert.True(update.FirstSight);
        Assert.Empty(update.NewFromThem);
    }

    [Fact]
    public void ANewMessagePushingTheListUpIsNewExactlyOnce()
    {
        var transcript = new ChatTranscript();
        transcript.Apply(Screen(Conversation[1..6]));

        // One more message: everything moves up one row, the first row scrolls away.
        TranscriptUpdate update = transcript.Apply(Screen(Conversation[2..7]));
        Assert.Empty(update.NewFromThem);   // that one was the user's

        update = transcript.Apply(Screen(Conversation[3..8]));
        Assert.Equal(["好"], update.NewFromThem.Select(i => i.Text));

        // The same picture again, settled: nothing new.
        Assert.Empty(transcript.Apply(Screen(Conversation[3..8])).NewFromThem);
    }

    [Fact]
    public void ScrollingUpAndBackDownTriggersNothing()
    {
        var transcript = new ChatTranscript();
        transcript.Apply(Screen(Conversation[5..12]));

        Assert.Empty(transcript.Apply(Screen(Conversation[3..9])).NewFromThem);   // up: earlier history joins
        Assert.Empty(transcript.Apply(Screen(Conversation[0..6])).NewFromThem);   // further up
        Assert.Empty(transcript.Apply(Screen(Conversation[5..12])).NewFromThem);  // back to the bottom

        Assert.Equal(Conversation, transcript.Items);
    }

    [Fact]
    public void RepeatedShortMessagesAreNotMistakenForOldOnes()
    {
        var transcript = new ChatTranscript();
        transcript.Apply(Screen(M("几点"), T("七点吧"), M("好"), T("好")));

        // Another 「好」 from them. A set of fingerprints would call it already seen.
        TranscriptUpdate update = transcript.Apply(Screen(T("七点吧"), M("好"), T("好"), T("好")));

        Assert.Equal(["好"], update.NewFromThem.Select(i => i.Text));
        Assert.Equal(5, transcript.Items.Count);

        // What gets judged is the whole batch since the user last spoke.
        Assert.Equal(["好", "好"], transcript.TrailingFromThem().Select(i => i.Text));
    }

    [Fact]
    public void NewMessagesArrivingWhileScrolledUpAreJudgedWhenScrolledBackDown()
    {
        var transcript = new ChatTranscript();
        transcript.Apply(Screen(Conversation[5..10]));
        transcript.Apply(Screen(Conversation[1..6]));   // reading history; meanwhile two arrive unseen

        TranscriptUpdate update = transcript.Apply(Screen(Conversation[7..12]));

        Assert.Equal(["好"], update.NewFromThem.Select(i => i.Text));
    }

    [Fact]
    public void AScrolledUpScreenIsNotJudgedEvenIfItLooksNew()
    {
        var transcript = new ChatTranscript();
        transcript.Apply(Screen(Conversation[1..6]));

        TranscriptUpdate update = transcript.Apply(new ChatScreen("小明", false, Conversation[3..8]) { IsAtBottom = false });

        Assert.Empty(update.NewFromThem);
    }

    [Fact]
    public void OcrWobbleOnALongMessageStillAligns()
    {
        var transcript = new ChatTranscript();
        transcript.Apply(Screen(T("今天下班路上看到一只超级胖的橘猫"), M("哈哈"), T("你看")));

        TranscriptUpdate update = transcript.Apply(Screen(T("今天下班路上看到一只超级胖的桔猫"), M("哈哈"), T("你看"), T("可爱吧")));

        Assert.Equal(["可爱吧"], update.NewFromThem.Select(i => i.Text));
    }

    [Fact]
    public void AScreenThatFitsNowhereJudgesNothingAndLeavesTheLiveTranscript()
    {
        var transcript = new ChatTranscript();
        transcript.Apply(Screen(Conversation[5..12]));

        TranscriptUpdate update = transcript.Apply(Screen(M("完全"), T("不同"), T("的对话")));

        Assert.True(update.Unaligned);
        Assert.False(update.FirstSight);
        Assert.Empty(update.NewFromThem);
        Assert.Equal(Conversation[5..12], transcript.Items);
    }

    [Theory]
    [InlineData(4)]   // overlapping screens: history is stitched on until the transcript is full
    [InlineData(8)]   // a page at a time: every screen up is a jump into history
    public void ScrollingFarUpALongChatAndBackJudgesOnlyTheNextNewMessage(int step)
    {
        // 60 messages, 8 on screen. Scroll to the top and back down, then one new one arrives.
        ChatItem[] chat = Enumerable.Range(0, 60).Select(i => i % 3 == 0 ? M($"我说{i}") : T($"对方{i}")).ToArray();
        var transcript = new ChatTranscript();
        ChatScreen At(int top) => Screen(chat[top..(top + 8)]);
        transcript.Apply(At(52));

        var tops = new List<int>();
        for (int top = 52 - step; top >= 0; top -= step)
        {
            tops.Add(top);
        }

        tops.AddRange(Enumerable.Reverse(tops).Skip(1));
        tops.Add(52);
        var judged = new List<string>();
        foreach (int top in tops)
        {
            judged.AddRange(transcript.Apply(At(top)).NewFromThem.Select(i => i.Text));
        }

        Assert.Empty(judged);

        TranscriptUpdate update = transcript.Apply(Screen([.. chat[53..60], T("新来的")]));
        Assert.Equal(["新来的"], update.NewFromThem.Select(i => i.Text));
        Assert.Empty(transcript.Apply(Screen([.. chat[53..60], T("新来的")])).NewFromThem);
    }

    [Fact]
    public void MessagesThatArrivedWhileReadingOldHistoryAreJudgedOnReturn()
    {
        ChatItem[] chat = Enumerable.Range(0, 40).Select(i => i % 2 == 0 ? M($"我说{i}") : T($"对方{i}")).ToArray();
        var transcript = new ChatTranscript();
        transcript.Apply(Screen(chat[32..40]));
        transcript.Apply(Screen(chat[0..8]));       // a jump to the top

        // Back at the bottom, where two new ones now wait under the old end.
        TranscriptUpdate update = transcript.Apply(Screen([.. chat[34..40], M("我先说"), T("对方新消息")]));

        Assert.Equal(["对方新消息"], update.NewFromThem.Select(i => i.Text));
    }

    [Fact]
    public void ABatchUnderAnOldStampIsMarkedStale()
    {
        TranscriptUpdate update = new ChatTranscript().Apply(Screen(M("晚安"), Stamp("昨天 23:10"), T("你睡了吗")));

        Assert.True(update.Stale);
        Assert.False(new ChatTranscript().Apply(Screen(M("晚安"), Stamp("19:12"), T("你睡了吗"))).Stale);
    }

    [Fact]
    public void TheBatchBeingAnsweredIsEverythingFromThemSinceTheUserLastSpoke()
    {
        var transcript = new ChatTranscript();
        transcript.Apply(Screen(M("几点"), T("七点吧"), Stamp("19:02"), T("别迟到")));

        Assert.Equal(["七点吧", "别迟到"], transcript.TrailingFromThem().Select(i => i.Text));
        Assert.DoesNotContain(transcript.Recent(10), i => i.Speaker == ChatSpeaker.Time);
    }

    [Fact]
    public void TheTranscriptKeepsTheNewestFifty()
    {
        var transcript = new ChatTranscript();
        var screen = Enumerable.Range(0, 60).Select(i => i % 2 == 0 ? T($"第{i}条") : M($"第{i}条")).ToArray();

        transcript.Apply(Screen(screen));

        Assert.Equal(ChatTranscript.Capacity, transcript.Items.Count);
        Assert.Equal("第59条", transcript.Items[^1].Text);
    }
}
