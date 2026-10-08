using LanAi.RelayClient.WeChatIntent;
using Xunit;

namespace LanAi.RelayClient.Tests.WeChatIntent;

public sealed class IntentSchedulerTests
{
    private static readonly DateTimeOffset T0 = new(2026, 9, 25, 20, 0, 0, TimeSpan.FromHours(8));

    private static ChatItem T(string text) => new(ChatSpeaker.Them, text);

    private static ChatBatch Batch(params ChatItem[] items) => new(items, items, Stale: false);

    [Fact]
    public void ABatchIsJudgedInOneRequestOnceItHasBeenStillForASecond()
    {
        var scheduler = new IntentScheduler();
        ChatItem[] batch = [T("那你说"), T("你最好是")];
        scheduler.OnScreen("小明", batch, T0);

        Assert.Empty(scheduler.Poll(T0.AddSeconds(0.5)));
        DueBatch due = Assert.Single(scheduler.Poll(T0.AddSeconds(1)));

        Assert.Equal(batch, due.Items);
        Assert.Same(batch[^1], due.Last);

        // Known by its last message: that one shows 「分析中」, the others no placeholder of their own.
        Assert.True(scheduler.IsInFlight("小明", batch[^1]));
        Assert.False(scheduler.IsInFlight("小明", batch[0]));
        Assert.Empty(scheduler.Poll(T0.AddSeconds(5)));
    }

    [Fact]
    public void EveryBatchOnAScreenGoesOutTheLowestFirstWithItsOwnContext()
    {
        var scheduler = new IntentScheduler();
        ChatBatch earlier = Batch(T("在吗"));
        ChatBatch later = new([T("那你说"), T("你最好是")], [T("在吗"), new ChatItem(ChatSpeaker.Me, "在"), T("那你说"), T("你最好是")], Stale: true);
        scheduler.OnScreen("小明", [earlier, later], T0);

        IReadOnlyList<DueBatch> due = scheduler.Poll(T0.AddSeconds(1));

        Assert.Equal(["你最好是", "在吗"], due.Select(d => d.Last.Text));
        Assert.Equal(4, due[0].Context.Count);
        Assert.True(due[0].Stale);
        Assert.False(due[1].Stale);
    }

    [Fact]
    public void AScrollThatKeepsGoingReplacesTheWait()
    {
        var scheduler = new IntentScheduler();
        scheduler.OnScreen("小明", [T("旧的")], T0);
        scheduler.OnScreen("小明", [T("新的")], T0.AddSeconds(0.8));

        Assert.Empty(scheduler.Poll(T0.AddSeconds(1.2)));
        Assert.Equal(["新的"], Assert.Single(scheduler.Poll(T0.AddSeconds(1.8))).Items.Select(i => i.Text));
    }

    [Fact]
    public void AScreenThatChangesWithoutTheConversationMovingKeepsTheWait()
    {
        // The overlay scrollbar fading in and out: a new picture every second, same messages.
        var scheduler = new IntentScheduler();
        scheduler.OnScreen("小明", [T("那你说")], T0);
        scheduler.OnScreen("小明", [T("那你说")], T0.AddSeconds(0.8));
        scheduler.OnScreen("小明", [T("那你说"), T("你最好是")], T0.AddSeconds(1.6));

        Assert.Equal(["那你说", "你最好是"], Assert.Single(scheduler.Poll(T0.AddSeconds(1.7))).Items.Select(i => i.Text));
    }

    [Fact]
    public void AnotherConversationStartsItsOwnWait()
    {
        var scheduler = new IntentScheduler();
        scheduler.OnScreen("小明", [T("那你说")], T0);
        scheduler.OnScreen("小红", [T("那你说")], T0.AddSeconds(0.8));

        Assert.Empty(scheduler.Poll(T0.AddSeconds(1.2)));
        Assert.Equal("小红", Assert.Single(scheduler.Poll(T0.AddSeconds(1.8))).Chat);
    }

    [Fact]
    public void ABatchInFlightIsNotSentAgainButOneThatGrewIs()
    {
        var scheduler = new IntentScheduler();
        ChatItem sayIt = T("那你说");
        scheduler.OnScreen("小明", [sayIt], T0);
        scheduler.Poll(T0.AddSeconds(1));

        scheduler.OnScreen("小明", [sayIt], T0.AddSeconds(2));
        Assert.Empty(scheduler.Poll(T0.AddSeconds(3)));

        // They wrote again: the longer batch is a new question, asked whole.
        scheduler.OnScreen("小明", [sayIt, T("你最好是")], T0.AddSeconds(4));
        Assert.Equal(["那你说", "你最好是"], Assert.Single(scheduler.Poll(T0.AddSeconds(5))).Items.Select(i => i.Text));
    }

    [Fact]
    public void ABatchEndingInTheSameWordsAsTheOneBeingJudgedIsStillANewBatch()
    {
        // 「好」 being judged, then another 「好」: the batch grew, though its last words did not change.
        var scheduler = new IntentScheduler();
        ChatItem first = T("好");
        scheduler.OnScreen("小明", [first], T0);
        scheduler.Poll(T0.AddSeconds(1));

        ChatItem second = T("好");
        Assert.False(scheduler.IsJudging("小明", second));
        Assert.True(scheduler.IsInFlight("小明", second));      // the placeholder still goes by the bubble's text
        scheduler.OnScreen("小明", [first, second], T0.AddSeconds(2));

        Assert.Same(second, Assert.Single(scheduler.Poll(T0.AddSeconds(3))).Last);
    }

    [Fact]
    public void AFailedBatchWaitsAMinuteBeforeTryingAgain()
    {
        var scheduler = new IntentScheduler();
        ChatItem item = T("那你说");
        scheduler.OnScreen("小明", [item], T0);
        scheduler.Poll(T0.AddSeconds(1));
        scheduler.Done("小明", item, succeeded: false, T0.AddSeconds(2));

        scheduler.OnScreen("小明", [item], T0.AddSeconds(3));
        Assert.Empty(scheduler.Poll(T0.AddSeconds(4)));

        scheduler.OnScreen("小明", [item], T0.AddSeconds(62));
        Assert.Single(scheduler.Poll(T0.AddSeconds(63)));
    }

    [Fact]
    public void ALongBatchSendsItsNewestTen()
    {
        var scheduler = new IntentScheduler();
        ChatItem[] many = Enumerable.Range(0, 14).Select(i => T($"第{i}条")).ToArray();
        scheduler.OnScreen("小明", many, T0);

        DueBatch due = Assert.Single(scheduler.Poll(T0.AddSeconds(1)));

        Assert.Equal(IntentScheduler.MaxPerBatch, due.Items.Count);
        Assert.Equal("第13条", due.Items[^1].Text);
        Assert.Equal("第4条", due.Items[0].Text);
    }

    [Fact]
    public void AtMostSixtyRequestsAMinute()
    {
        var scheduler = new IntentScheduler();
        ChatBatch[] batches = Enumerable.Range(0, 70).Select(i => Batch(T($"第{i}批"))).ToArray();
        scheduler.OnScreen("小明", batches, T0);

        Assert.Equal(IntentScheduler.PerMinute, scheduler.Poll(T0.AddSeconds(1)).Count);
    }

    [Fact]
    public void PausedAndMutedConversationsAreNotJudged()
    {
        var scheduler = new IntentScheduler { PausedUntil = T0.AddMinutes(30) };
        scheduler.OnScreen("小明", [T("在吗")], T0);
        Assert.Empty(scheduler.Poll(T0.AddSeconds(5)));

        scheduler.PausedUntil = null;
        scheduler.Muted.Add("小明");
        scheduler.OnScreen("小明", [T("在吗")], T0);
        Assert.Empty(scheduler.Poll(T0.AddSeconds(5)));
    }

    [Fact]
    public void ResetForgetsEverything()
    {
        var scheduler = new IntentScheduler();
        ChatItem item = T("那你说");
        scheduler.OnScreen("小明", [item], T0);
        scheduler.Poll(T0.AddSeconds(1));

        scheduler.Reset();

        Assert.False(scheduler.IsInFlight("小明", item));
    }
}
