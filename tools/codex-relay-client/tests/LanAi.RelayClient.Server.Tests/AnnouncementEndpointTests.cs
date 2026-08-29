using System.Net;
using Xunit;

namespace LanAi.RelayClient.Server.Tests;

public sealed class AnnouncementEndpointTests
{
    [Fact]
    public async Task ListBindsTheUserAnnouncementShape()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """[{"id":7,"title":"系统维护","content":"# 维护\n今晚 22:00","notify_mode":"popup","created_at":"2026-08-15T10:00:00Z","updated_at":"2026-08-15T10:00:00Z"}]""");

        IReadOnlyList<RelayAnnouncement> items = await handler.CreateClient().ListAnnouncementsAsync("at");

        RelayAnnouncement item = Assert.Single(items);
        Assert.Equal(7, item.Id);
        Assert.Equal("系统维护", item.Title);
        Assert.Equal("# 维护\n今晚 22:00", item.Content);
        Assert.True(item.WantsPopup);

        // read_at is omitted for an unread announcement rather than sent as null.
        Assert.Null(item.ReadAt);
        Assert.True(item.IsUnread);
    }

    [Fact]
    public async Task ListReadsTheReadTimestampAsHavingBeenRead()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """[{"id":7,"title":"t","content":"c","notify_mode":"silent","read_at":"2026-08-15T11:00:00Z","created_at":"2026-08-15T10:00:00Z","updated_at":"2026-08-15T10:00:00Z"}]""");

        RelayAnnouncement item = Assert.Single(await handler.CreateClient().ListAnnouncementsAsync("at"));

        Assert.False(item.IsUnread);
        Assert.False(item.WantsPopup);
    }

    [Fact]
    public async Task ListAsksForEverythingRatherThanUnreadOnly()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, "[]");

        await handler.CreateClient().ListAnnouncementsAsync("at");

        // The read ones fill the list view, and they are what lets the monitor tell
        // "already read" apart from "no longer visible" when it prunes.
        Assert.Equal("/api/v1/announcements", handler.LastRequest!.RequestUri!.AbsolutePath);
        Assert.Equal(string.Empty, handler.LastRequest.RequestUri.Query);
        Assert.Equal("Bearer at", handler.LastRequest.Headers.Authorization?.ToString());
    }

    [Fact]
    public async Task AnEmptyListIsNotAnError()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, "[]");

        Assert.Empty(await handler.CreateClient().ListAnnouncementsAsync("at"));
    }

    [Fact]
    public async Task HeadBindsTheSummaryWithoutAskingForBodies()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"max_id":7,"unread_count":2,"total":3}""");

        AnnouncementHead head = await handler.CreateClient().GetAnnouncementHeadAsync("at");

        Assert.Equal(7, head.MaxId);
        Assert.Equal(2, head.UnreadCount);
        Assert.Equal(3, head.Total);
        Assert.Equal("/api/v1/announcements/head", handler.LastRequest!.RequestUri!.AbsolutePath);
        Assert.Equal("Bearer at", handler.LastRequest.Headers.Authorization?.ToString());
    }

    [Fact]
    public async Task HeadBindsZeroesForAUserWithNoAnnouncements()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"max_id":0,"unread_count":0,"total":0}""");

        AnnouncementHead head = await handler.CreateClient().GetAnnouncementHeadAsync("at");

        Assert.Equal(0, head.MaxId);
        Assert.Equal(0, head.Total);
    }

    /// <remarks>
    /// The endpoint postdates the client's first release, so this 404 is the
    /// signal a caller uses to fall back to the full list rather than an error to
    /// surface.
    /// </remarks>
    [Fact]
    public async Task HeadReportsNotFoundOnARelayThatPredatesTheEndpoint()
    {
        var handler = StubHandler.Raw(HttpStatusCode.NotFound, """{"code":404,"message":"Not Found"}""");

        RelayApiException failure = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().GetAnnouncementHeadAsync("at"));

        Assert.Equal(RelayFailure.NotFound, failure.Failure);
    }

    [Fact]
    public async Task MarkReadPostsToTheAnnouncementItNames()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, """{"message":"ok"}""");

        await handler.CreateClient().MarkAnnouncementReadAsync("at", 42);

        Assert.Equal(HttpMethod.Post, handler.LastRequest!.Method);
        Assert.Equal("/api/v1/announcements/42/read", handler.LastRequest.RequestUri!.AbsolutePath);
    }

    [Fact]
    public async Task MarkReadSurfacesARejectedAnnouncement()
    {
        // The server re-checks visibility, so an id this user was never eligible
        // for comes back rejected rather than quietly succeeding.
        var handler = StubHandler.Envelope(HttpStatusCode.NotFound, code: 40400, dataJson: null);

        RelayApiException failure = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().MarkAnnouncementReadAsync("at", 999));

        Assert.Equal(RelayFailure.NotFound, failure.Failure);
    }
}
