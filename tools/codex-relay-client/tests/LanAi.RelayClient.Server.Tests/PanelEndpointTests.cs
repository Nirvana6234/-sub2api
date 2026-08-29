using System.Net;
using System.Text.Json;
using Xunit;

namespace LanAi.RelayClient.Server.Tests;

/// <summary>
/// Covers the endpoints M2 adds: the F4 information cards and the F5 group surface.
/// </summary>
public sealed class PanelEndpointTests
{
    [Fact]
    public async Task DashboardStatsBindsTheTodayFiguresTheCardsShow()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"today_requests":42,"today_tokens":13500,"today_actual_cost":1.25,"today_cost":1.8,"total_requests":900}""");

        DashboardStats stats = await handler.CreateClient().GetDashboardStatsAsync("at");

        Assert.Equal(42, stats.TodayRequests);
        Assert.Equal(13500, stats.TodayTokens);
        Assert.Equal(1.25, stats.TodayActualCost);
        Assert.Equal(900, stats.TotalRequests);
    }

    [Fact]
    public async Task AvailableGroupsBindsTheFieldsFiveDisplays()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """[{"id":11,"name":"标准组","description":"通用","platform":"openai","rate_multiplier":1.5,"subscription_type":"standard"}]""");

        IReadOnlyList<RelayGroup> groups = await handler.CreateClient().GetAvailableGroupsAsync("at");

        RelayGroup group = Assert.Single(groups);
        Assert.Equal(11, group.Id);
        Assert.Equal("标准组", group.Name);
        Assert.Equal("openai", group.Platform);
        Assert.Equal(1.5, group.RateMultiplier);
        Assert.False(group.IsSubscription);
    }

    [Fact]
    public async Task GroupRatesBindsDespiteJsonObjectKeysBeingStrings()
    {
        // The server marshals map[int64]float64, so the keys arrive quoted. If the
        // numeric-key binding ever regressed, every user-specific rate would go
        // missing and the panel would quietly bill-display the default instead.
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, """{"11":0.8,"12":0}""");

        IReadOnlyDictionary<long, double> rates = await handler.CreateClient().GetUserGroupRatesAsync("at");

        Assert.Equal(0.8, rates[11]);
        Assert.Equal(0, rates[12]);
        Assert.False(rates.ContainsKey(13));
    }

    [Fact]
    public async Task ListingKeysUnwrapsThePaginationEnvelope()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"items":[{"id":5,"name":"共飞直连客户端-PC-abc","key":"sk-live","group_id":11,"expires_at":"2026-08-02T10:00:00Z"}],"total":1,"page":1,"page_size":100,"pages":1}""");

        IReadOnlyList<RelayApiKey> keys = await handler.CreateClient().ListApiKeysAsync("at");

        RelayApiKey key = Assert.Single(keys);
        Assert.Equal(5, key.Id);
        Assert.Equal("共飞直连客户端-PC-abc", key.Name);
        Assert.Equal("sk-live", key.Key);
        Assert.Equal(11, key.GroupId);
        Assert.NotNull(key.ExpiresAt);
    }

    [Fact]
    public async Task ListingKeysTreatsAKeyWithoutAnExpiryAsUnbounded()
    {
        // Under the F3.2 lease model a null expiry is not a tidy default — it is an
        // authorization that outlives the client. It must survive binding as null
        // so the caller can act on it, rather than collapsing to a sentinel date.
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"items":[{"id":6,"name":"手动建的","key":"sk-x","expires_at":null}],"total":1,"page":1,"page_size":100,"pages":1}""");

        IReadOnlyList<RelayApiKey> keys = await handler.CreateClient().ListApiKeysAsync("at");

        Assert.Null(Assert.Single(keys).ExpiresAt);
        Assert.Null(keys[0].GroupId);
    }

    [Fact]
    public async Task ListingKeysWalksEveryPageEvenWhenTheServerShrinksThePage()
    {
        // The client asks for 100 per page but the server is free to serve fewer.
        // Treating a page shorter than the requested size as the last one would
        // stop at page 1 — and a managed-key lookup that misses the real key issues
        // a duplicate lease instead of renewing the existing one.
        var handler = StubHandler.EnvelopeSequence(
            """{"items":[{"id":1,"name":"a"},{"id":2,"name":"b"}],"total":3,"page":1,"page_size":2,"pages":2}""",
            """{"items":[{"id":3,"name":"共飞直连客户端-PC-abc"}],"total":3,"page":2,"page_size":2,"pages":2}""");

        IReadOnlyList<RelayApiKey> keys = await handler.CreateClient().ListApiKeysAsync("at");

        Assert.Equal(3, keys.Count);
        Assert.Equal(2, handler.RequestCount);
        Assert.Contains(keys, k => k.Name == "共飞直连客户端-PC-abc");
    }

    [Fact]
    public async Task ListingKeysStopsOnAnEmptyPageRatherThanTrustingPageCount()
    {
        // Inconsistent metadata (pages says 5, page 2 is empty) must terminate the
        // walk rather than spin against the server.
        var handler = StubHandler.EnvelopeSequence(
            """{"items":[{"id":1,"name":"a"}],"total":9,"page":1,"page_size":1,"pages":5}""",
            """{"items":[],"total":9,"page":2,"page_size":1,"pages":5}""");

        IReadOnlyList<RelayApiKey> keys = await handler.CreateClient().ListApiKeysAsync("at");

        Assert.Single(keys);
        Assert.Equal(2, handler.RequestCount);
    }

    [Fact]
    public async Task NoGroupOverridesIsAnEmptyMapNotAFailure()
    {
        // The service returns a nil map when the rate repository is not wired, and
        // Go marshals nil as null. Having no special deal is the ordinary case, so
        // it must not fail the group surface for every such user.
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, "null");

        IReadOnlyDictionary<long, double> rates = await handler.CreateClient().GetUserGroupRatesAsync("at");

        Assert.Empty(rates);
    }

    [Fact]
    public async Task SwitchingGroupsSendsGroupIdAndNothingElse()
    {
        // The guard this test exists for: the update handler reads an empty
        // expires_at as "clear the expiry" and an absent one as "leave it".
        // A body that carried expires_at:"" would convert the one-day lease into a
        // permanent key on what the user thinks is only a group switch.
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"id":5,"name":"k","key":"sk","group_id":12}""");

        RelayApiKey updated = await handler.CreateClient().UpdateApiKeyGroupAsync("at", keyId: 5, groupId: 12);

        Assert.Equal(12, updated.GroupId);

        using JsonDocument sent = JsonDocument.Parse(handler.LastRequestBody!);
        Assert.Equal(12, sent.RootElement.GetProperty("group_id").GetInt64());
        Assert.False(sent.RootElement.TryGetProperty("expires_at", out _));
        Assert.Single(sent.RootElement.EnumerateObject());
        Assert.Equal(HttpMethod.Put, handler.LastRequest!.Method);
    }

    [Fact]
    public async Task IssuingAKeySendsTheLeaseAndAFreshIdempotencyKey()
    {
        // The header is the server's only replay key — it is never derived from the
        // body. Omitting it works today only because the server runs observe-only;
        // an operator enforcing idempotency would reject every header-less write.
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"id":9,"name":"共飞直连客户端-PC-abc","key":"sk-new","group_id":11}""");

        RelayApiKey created = await handler.CreateClient()
            .CreateApiKeyAsync("at", "共飞直连客户端-PC-abc", groupId: 11, expiresInDays: 1);

        Assert.Equal("sk-new", created.Key);

        using JsonDocument sent = JsonDocument.Parse(handler.LastRequestBody!);
        Assert.Equal("共飞直连客户端-PC-abc", sent.RootElement.GetProperty("name").GetString());
        Assert.Equal(1, sent.RootElement.GetProperty("expires_in_days").GetInt32());
        Assert.Equal(11, sent.RootElement.GetProperty("group_id").GetInt64());

        Assert.True(handler.LastRequest!.Headers.TryGetValues("Idempotency-Key", out var values));
        Assert.False(string.IsNullOrWhiteSpace(values!.Single()));
    }

    [Fact]
    public async Task EachIssuanceUsesADifferentIdempotencyKey()
    {
        // A stable key would replay a cached response for up to a day: reissuing
        // after the user deleted the key in the web panel would hand back the
        // deleted key's details, and the client would write a dead credential and
        // report success.
        var first = StubHandler.Envelope(HttpStatusCode.OK, code: 0, """{"id":1,"name":"k","key":"sk"}""");
        var second = StubHandler.Envelope(HttpStatusCode.OK, code: 0, """{"id":1,"name":"k","key":"sk"}""");

        await first.CreateClient().CreateApiKeyAsync("at", "k", groupId: null, expiresInDays: 1);
        await second.CreateClient().CreateApiKeyAsync("at", "k", groupId: null, expiresInDays: 1);

        string a = first.LastRequest!.Headers.GetValues("Idempotency-Key").Single();
        string b = second.LastRequest!.Headers.GetValues("Idempotency-Key").Single();

        Assert.NotEqual(a, b);
    }

    [Fact]
    public async Task IssuingWithoutAGroupOmitsTheFieldRatherThanSendingNull()
    {
        // Sending null would tell the server to clear the binding; omitting it lets
        // the server apply its own default.
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, """{"id":1,"name":"k","key":"sk"}""");

        await handler.CreateClient().CreateApiKeyAsync("at", "k", groupId: null, expiresInDays: 1);

        using JsonDocument sent = JsonDocument.Parse(handler.LastRequestBody!);
        Assert.False(sent.RootElement.TryGetProperty("group_id", out _));
    }

    [Fact]
    public async Task DeletingAManagedKeyUsesTheAuthenticatedUserEndpoint()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, "{}");

        await handler.CreateClient().DeleteApiKeyAsync("access-token", keyId: 42);

        Assert.Equal(HttpMethod.Delete, handler.LastRequest!.Method);
        Assert.Equal("/api/v1/keys/42", handler.LastRequest.RequestUri!.AbsolutePath);
        Assert.Equal("Bearer", handler.LastRequest.Headers.Authorization!.Scheme);
        Assert.Equal("access-token", handler.LastRequest.Headers.Authorization.Parameter);
        Assert.Null(handler.LastRequestBody);
    }

    [Fact]
    public async Task DeletingAKeyStillHonoursTheEnvelopeBusinessCode()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 4107,
            dataJson: null,
            reason: "API_KEY_NOT_FOUND");

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(() =>
            handler.CreateClient().DeleteApiKeyAsync("access-token", keyId: 42));

        Assert.Equal("API_KEY_NOT_FOUND", error.Reason);
    }

    [Fact]
    public async Task PanelCallsCarryTheBearerToken()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, "[]");

        await handler.CreateClient().GetAvailableGroupsAsync("secret-token");

        Assert.Equal("Bearer", handler.LastRequest!.Headers.Authorization!.Scheme);
        Assert.Equal("secret-token", handler.LastRequest.Headers.Authorization.Parameter);
    }

    [Fact]
    public async Task AnExpiredSessionOnACardEndpointIsUnauthenticatedNotBadCredentials()
    {
        // F4.2 forbids a failed card from ending the session, and that starts with
        // classifying the failure correctly: a 401 here is a stale token, not a
        // user who typed the wrong password.
        var handler = StubHandler.Envelope(HttpStatusCode.Unauthorized, code: 1, dataJson: null);

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().GetDashboardStatsAsync("stale"));

        Assert.Equal(RelayFailure.Unauthenticated, error.Failure);
    }

    [Fact]
    public async Task PanelRateLimitingIsReportedAsSuchSoTheCallerCanBackOff()
    {
        // The authenticated route group carries a per-user limiter, and F4's
        // acceptance criteria require backing off rather than reporting an error
        // on every poll.
        var handler = StubHandler.Envelope(HttpStatusCode.TooManyRequests, code: 1, dataJson: null);

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().GetDashboardStatsAsync("at"));

        Assert.Equal(RelayFailure.RateLimited, error.Failure);
    }

    [Fact]
    public async Task SubscriptionSummaryBindsAnEmptyListForUsersWithoutOne()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"active_count":0,"total_used_usd":0,"subscriptions":[]}""");

        IReadOnlyList<SubscriptionSummaryItem> items =
            await handler.CreateClient().GetSubscriptionSummaryAsync("at");

        Assert.Empty(items);
    }

    [Fact]
    public async Task SubscriptionSummaryReturnsTheNestedSubscriptions()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"active_count":1,"total_used_usd":5.25,"subscriptions":[{"id":7,"group_id":11,"group_name":"标准组","status":"active","monthly_used_usd":5.25,"monthly_limit_usd":20}]}""");

        IReadOnlyList<SubscriptionSummaryItem> items =
            await handler.CreateClient().GetSubscriptionSummaryAsync("at");

        SubscriptionSummaryItem item = Assert.Single(items);
        Assert.Equal(7, item.Id);
        Assert.Equal(11, item.GroupId);
        Assert.Equal("标准组", item.GroupName);
        Assert.Equal(5.25, item.MonthlyUsedUsd);
        Assert.Equal(20, item.MonthlyLimitUsd);
    }
}
