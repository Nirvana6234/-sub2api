using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class AnnouncementMonitorTests
{
    /// <remarks>
    /// There is deliberately no first-run grace period. The server's read state
    /// already says what this user has seen, so an extra client-side baseline
    /// would swallow the first announcement after every install and every client
    /// upgrade — the one most worth delivering.
    /// </remarks>
    [Fact]
    public async Task FirstPollStillAnnouncesWhatTheUserHasNotRead()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true, title: "开服公告")];

        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.True(observation.Succeeded);
        Assert.Single(observation.Announcements);
        Assert.Equal(1, observation.UnreadCount);
        Assert.True(observation.ShouldNotify);
        Assert.Equal("开服公告", observation.LatestTitle);
    }

    [Fact]
    public async Task FirstPollStaysQuietWhenEverythingWasAlreadyReadOnTheWeb()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true, readAt: DateTimeOffset.UtcNow)];

        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.False(observation.ShouldNotify);
        Assert.Equal(0, observation.UnreadCount);
        Assert.Single(observation.Announcements);
    }

    [Fact]
    public async Task AnnouncementArrivingAfterTheBaselineRaisesOneBalloon()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        relay.Announcements = [Announcement(2, popup: true, title: "维护通知"), Announcement(1, popup: true)];
        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.True(observation.ShouldNotify);
        Assert.Equal(1, observation.NewCount);
        Assert.Equal("维护通知", observation.LatestTitle);
    }

    [Fact]
    public async Task SeveralArrivalsAreReportedAsOneBalloon()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        relay.Announcements =
        [
            Announcement(3, popup: true, title: "最新", createdAt: DateTimeOffset.UtcNow),
            Announcement(2, popup: true, title: "较早", createdAt: DateTimeOffset.UtcNow.AddHours(-1)),
            Announcement(1, popup: true),
        ];
        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.True(observation.ShouldNotify);
        Assert.Equal(2, observation.NewCount);
        Assert.Equal("最新", observation.LatestTitle);
    }

    [Fact]
    public async Task PollingAgainDoesNotRepeatABalloonForTheSameAnnouncement()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        relay.Announcements = [Announcement(2, popup: true), Announcement(1, popup: true)];
        Assert.True((await monitor.CheckAsync()).ShouldNotify);
        Assert.False((await monitor.CheckAsync()).ShouldNotify);
    }

    [Fact]
    public async Task AnAnnouncementAlreadyReadElsewhereDoesNotRaiseABalloon()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        // Read on the web panel before this client ever saw it.
        relay.Announcements =
        [
            Announcement(2, popup: true, readAt: DateTimeOffset.UtcNow),
            Announcement(1, popup: true),
        ];
        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.False(observation.ShouldNotify);
        Assert.Equal(1, observation.UnreadCount);
    }

    [Fact]
    public async Task SilentAnnouncementReachesTheListButNotTheBalloon()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        relay.Announcements = [Announcement(2, popup: false), Announcement(1, popup: true)];
        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.False(observation.ShouldNotify);
        Assert.Equal(2, observation.UnreadCount);
        Assert.Equal(2, observation.Announcements.Count);
    }

    /// <remarks>
    /// The case a high-water mark gets wrong. An announcement written earlier but
    /// scheduled to start now — or switched from draft, or whose targeting was
    /// widened until this user became eligible — arrives with an id below ones
    /// already seen.
    /// </remarks>
    [Fact]
    public async Task AnnouncementWithALowerIdBecomingVisibleLaterStillNotifies()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(50, popup: true)];
        await monitor.CheckAsync();

        relay.Announcements = [Announcement(50, popup: true), Announcement(7, popup: true, title: "补发公告")];
        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.True(observation.ShouldNotify);
        Assert.Equal(1, observation.NewCount);
        Assert.Equal("补发公告", observation.LatestTitle);
    }

    [Fact]
    public async Task AnnouncementThatDisappearsAndReturnsIsTreatedAsNewAgain()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, FakeAnnouncementNotifyStateStore store) =
            await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        relay.Announcements = [];
        await monitor.CheckAsync();

        // Pruning to what is visible is also what keeps the stored set bounded.
        Assert.Empty(store.Saved.Values.Single());

        relay.Announcements = [Announcement(1, popup: true)];
        Assert.True((await monitor.CheckAsync()).ShouldNotify);
    }

    [Fact]
    public async Task SigningOutAndBackInDoesNotReAnnounceEverything()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true), Announcement(2, popup: true)];
        await monitor.CheckAsync();

        // Reset drops only the in-memory copy; the stored record is what tells a
        // returning user apart from a brand new one.
        monitor.Reset();

        Assert.False((await monitor.CheckAsync()).ShouldNotify);
    }

    [Fact]
    public async Task ASecondAccountIsNotSilencedByTheFirstAccountRecord()
    {
        var store = new FakeAnnouncementNotifyStateStore();
        (AnnouncementMonitor first, FakeRelayClient firstRelay, _) = await BuildAsync(store, "a@b.com");
        firstRelay.Announcements = [Announcement(1, popup: true), Announcement(2, popup: true)];
        await first.CheckAsync();
        Assert.False((await first.CheckAsync()).ShouldNotify);

        (AnnouncementMonitor second, FakeRelayClient secondRelay, _) = await BuildAsync(store, "other@b.com");
        secondRelay.Announcements = [Announcement(1, popup: true), Announcement(2, popup: true)];

        // The other account has read neither, so both are news to it.
        AnnouncementObservation observation = await second.CheckAsync();
        Assert.True(observation.ShouldNotify);
        Assert.Equal(2, observation.NewCount);
        Assert.Equal(2, store.Saved.Count);
    }

    [Fact]
    public async Task AFailedPollReportsFailureRatherThanAnEmptyList()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        // Moved on, so the probe reports a change and the list is actually
        // attempted — otherwise this would exercise the unchanged path instead.
        relay.Announcements = [Announcement(2, popup: true), Announcement(1, popup: true)];
        relay.OnListAnnouncements = () => new RelayApiException(RelayFailure.NetworkUnreachable, "断网了");
        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.False(observation.Succeeded);
        Assert.False(observation.Changed);
        Assert.False(observation.ShouldNotify);
        Assert.Empty(observation.Announcements);
    }

    [Fact]
    public async Task AFailedPollDoesNotDisturbTheStoredRecord()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, FakeAnnouncementNotifyStateStore store) =
            await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        relay.Announcements = [Announcement(2, popup: true), Announcement(1, popup: true)];
        relay.OnListAnnouncements = () => new RelayApiException(RelayFailure.ServerError, "500");
        await monitor.CheckAsync();

        // The record still holds only what was successfully seen, so announcement
        // 2 is still news once the server comes back.
        Assert.Equal([1L], store.Saved.Values.Single());

        relay.OnListAnnouncements = null;
        Assert.True((await monitor.CheckAsync()).ShouldNotify);

        // Ordered for comparison only: the record is a set, and the monitor never
        // depends on the order it comes back in.
        Assert.Equal([1L, 2L], store.Saved.Values.Single().Order());
    }

    [Fact]
    public async Task AnUnchangedSummarySkipsPullingTheListAgain()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();
        int listCallsAfterFirstPoll = relay.ListAnnouncementsCallCount;

        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.True(observation.Succeeded);
        Assert.False(observation.Changed);
        Assert.Equal(listCallsAfterFirstPoll, relay.ListAnnouncementsCallCount);
        Assert.Equal(1, relay.AnnouncementHeadCallCount);
    }

    [Fact]
    public async Task AMovedSummaryPullsTheListAndAnnounces()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        relay.Announcements = [Announcement(2, popup: true, title: "新公告"), Announcement(1, popup: true)];
        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.True(observation.Changed);
        Assert.True(observation.ShouldNotify);
        Assert.Equal("新公告", observation.LatestTitle);
    }

    /// <remarks>
    /// A withdrawn announcement that had been read moves neither the watermark nor
    /// the unread count; the total is what catches it.
    /// </remarks>
    [Fact]
    public async Task AWithdrawnAnnouncementIsNoticedThroughTheTotal()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        DateTimeOffset read = DateTimeOffset.UtcNow;
        relay.Announcements = [Announcement(2, popup: true), Announcement(1, popup: true, readAt: read)];
        await monitor.CheckAsync();

        relay.Announcements = [Announcement(2, popup: true)];
        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.True(observation.Changed);
        Assert.Single(observation.Announcements);
    }

    [Fact]
    public async Task MarkingSomethingReadLocallyDoesNotForceARefetch()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();
        int listCallsAfterFirstPoll = relay.ListAnnouncementsCallCount;

        // The client marked it read; the server now reports one fewer unread.
        monitor.NoteLocallyRead();
        relay.Announcements = [Announcement(1, popup: true, readAt: DateTimeOffset.UtcNow)];

        Assert.False((await monitor.CheckAsync()).Changed);
        Assert.Equal(listCallsAfterFirstPoll, relay.ListAnnouncementsCallCount);
    }

    /// <remarks>
    /// A client can be newer than the relay it talks to. Losing the optimisation
    /// is acceptable; losing announcements is not.
    /// </remarks>
    [Fact]
    public async Task ARelayWithoutTheSummaryEndpointFallsBackToTheFullList()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.OnAnnouncementHead = () => new RelayApiException(RelayFailure.NotFound, "404");
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        relay.Announcements = [Announcement(2, popup: true, title: "新公告"), Announcement(1, popup: true)];
        AnnouncementObservation observation = await monitor.CheckAsync();

        Assert.True(observation.Changed);
        Assert.True(observation.ShouldNotify);

        // Probed once, then never again for the life of the session.
        Assert.Equal(1, relay.AnnouncementHeadCallCount);
        Assert.Equal(2, relay.ListAnnouncementsCallCount);
    }

    [Fact]
    public async Task ResetDropsTheCachedSummarySoTheNextAccountIsFetched()
    {
        (AnnouncementMonitor monitor, FakeRelayClient relay, _) = await BuildAsync();
        relay.Announcements = [Announcement(1, popup: true)];
        await monitor.CheckAsync();

        monitor.Reset();
        int listCallsBefore = relay.ListAnnouncementsCallCount;
        await monitor.CheckAsync();

        Assert.Equal(listCallsBefore + 1, relay.ListAnnouncementsCallCount);
    }

    private static RelayAnnouncement Announcement(
        long id,
        bool popup,
        string title = "公告",
        DateTimeOffset? readAt = null,
        DateTimeOffset? createdAt = null) =>
        new()
        {
            Id = id,
            Title = title,
            Content = "正文",
            NotifyMode = popup ? "popup" : "silent",
            ReadAt = readAt,
            CreatedAt = createdAt ?? DateTimeOffset.UtcNow,
        };

    private static Task<(AnnouncementMonitor Monitor, FakeRelayClient Relay, FakeAnnouncementNotifyStateStore Store)>
        BuildAsync() => BuildAsync(new FakeAnnouncementNotifyStateStore(), "a@b.com");

    private static async Task<(AnnouncementMonitor Monitor, FakeRelayClient Relay, FakeAnnouncementNotifyStateStore Store)>
        BuildAsync(FakeAnnouncementNotifyStateStore store, string email)
    {
        var relay = new FakeRelayClient();
        relay.OnLogin = () => LoginOutcome.Authenticated(FakeRelayClient.Tokens("at", email: email));

        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync(email, "pw");

        return (new AnnouncementMonitor(relay, session, store), relay, store);
    }
}

/// <summary>An in-memory notified-id record, so tests never touch the user's profile.</summary>
internal sealed class FakeAnnouncementNotifyStateStore : IAnnouncementNotifyStateStore
{
    public Dictionary<string, long[]> Saved { get; } = [];

    public IReadOnlyCollection<long>? Load(string accountKey) =>
        Saved.TryGetValue(accountKey, out long[]? ids) ? ids : null;

    public void Save(string accountKey, IReadOnlyCollection<long> notifiedIds) =>
        Saved[accountKey] = [.. notifiedIds];
}
