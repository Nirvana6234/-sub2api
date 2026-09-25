using LanAi.RelayClient.WeChatIntent;
using Xunit;

namespace LanAi.RelayClient.Tests.WeChatIntent;

public sealed class IntentSchedulerTests
{
    private static readonly DateTimeOffset T0 = new(2026, 9, 25, 20, 0, 0, TimeSpan.FromHours(8));

    private static ChatItem T(string text) => new(ChatSpeaker.Them, text);

    [Fact]
    public void AScreenIsJudgedOnceItHasBeenStillForASecond()
    {
        var scheduler = new IntentScheduler();
        ChatItem[] withoutCard = [T("那你说"), T("你最好是")];
        scheduler.OnScreen("小明", withoutCard, T0);

        Assert.Null(scheduler.Poll(T0.AddSeconds(0.5)));
        DueBatch? due = scheduler.Poll(T0.AddSeconds(1));

        Assert.NotNull(due);
        Assert.Equal(withoutCard, due.Items);
        Assert.True(scheduler.IsInFlight("小明", withoutCard[0]));
        Assert.Null(scheduler.Poll(T0.AddSeconds(5)));
    }

    [Fact]
    public void AScrollThatKeepsGoingReplacesTheWait()
    {
        var scheduler = new IntentScheduler();
        scheduler.OnScreen("小明", [T("旧的")], T0);
        scheduler.OnScreen("小明", [T("新的")], T0.AddSeconds(0.8));

        Assert.Null(scheduler.Poll(T0.AddSeconds(1.2)));
        Assert.Equal(["新的"], scheduler.Poll(T0.AddSeconds(1.8))!.Items.Select(i => i.Text));
    }

    [Fact]
    public void MessagesInFlightAreNotSentAgain()
    {
        var scheduler = new IntentScheduler();
        ChatItem first = T("那你说");
        scheduler.OnScreen("小明", [first], T0);
        scheduler.Poll(T0.AddSeconds(1));

        scheduler.OnScreen("小明", [T("那你说"), T("你最好是")], T0.AddSeconds(2));

        Assert.Equal(["你最好是"], scheduler.Poll(T0.AddSeconds(3))!.Items.Select(i => i.Text));
    }

    [Fact]
    public void AFailedMessageWaitsAMinuteBeforeTryingAgain()
    {
        var scheduler = new IntentScheduler();
        ChatItem item = T("那你说");
        scheduler.OnScreen("小明", [item], T0);
        scheduler.Poll(T0.AddSeconds(1));
        scheduler.Done("小明", item, succeeded: false, T0.AddSeconds(2));

        scheduler.OnScreen("小明", [T("那你说")], T0.AddSeconds(3));
        Assert.Null(scheduler.Poll(T0.AddSeconds(4)));

        scheduler.OnScreen("小明", [T("那你说")], T0.AddSeconds(62));
        Assert.NotNull(scheduler.Poll(T0.AddSeconds(63)));
    }

    [Fact]
    public void AtMostTenAScreenNewestFirst()
    {
        var scheduler = new IntentScheduler();
        ChatItem[] many = Enumerable.Range(0, 14).Select(i => T($"第{i}条")).ToArray();
        scheduler.OnScreen("小明", many, T0);

        DueBatch due = scheduler.Poll(T0.AddSeconds(1))!;

        Assert.Equal(IntentScheduler.MaxPerBatch, due.Items.Count);
        Assert.Equal("第13条", due.Items[^1].Text);
        Assert.Equal("第4条", due.Items[0].Text);
    }

    [Fact]
    public void PausedAndMutedConversationsAreNotJudged()
    {
        var scheduler = new IntentScheduler { PausedUntil = T0.AddMinutes(30) };
        scheduler.OnScreen("小明", [T("在吗")], T0);
        Assert.Null(scheduler.Poll(T0.AddSeconds(5)));

        scheduler.PausedUntil = null;
        scheduler.Muted.Add("小明");
        scheduler.OnScreen("小明", [T("在吗")], T0);
        Assert.Null(scheduler.Poll(T0.AddSeconds(5)));
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
