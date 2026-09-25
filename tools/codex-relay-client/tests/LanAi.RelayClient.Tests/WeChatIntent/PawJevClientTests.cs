using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.WeChatIntent;
using Xunit;

namespace LanAi.RelayClient.Tests.WeChatIntent;

/// <summary>
/// The relay route (docs §7.4), against a fake server. The contract here is the one proposed to
/// the server side; adjust when its final error table arrives.
/// </summary>
public sealed class PawJevClientTests
{
    private static readonly JevState State = JevRequestBody.StateFor(
        [new ChatItem(ChatSpeaker.Them, "你今天是不是又忘了"), new ChatItem(ChatSpeaker.Me, "记得"), new ChatItem(ChatSpeaker.Them, "那你说")],
        [new ChatItem(ChatSpeaker.Them, "那你说")]);

    private sealed class FakeRelay(params Func<HttpRequestMessage, HttpResponseMessage>[] replies) : HttpMessageHandler
    {
        public List<(HttpRequestMessage Request, string Body)> Seen { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            string body = request.Content is null ? string.Empty : await request.Content.ReadAsStringAsync(cancellationToken);
            Seen.Add((request, body));
            return replies[Math.Min(Seen.Count - 1, replies.Length - 1)](request);
        }
    }

    private static HttpResponseMessage Reply(HttpStatusCode status, string body = "{}") =>
        new(status) { Content = new StringContent(body, Encoding.UTF8, "application/json") };

    private static HttpResponseMessage PawError(HttpStatusCode status, string code) =>
        Reply(status, """{"error":{"code":"CODE","message":"m"}}""".Replace("CODE", code, StringComparison.Ordinal));

    private sealed class Session
    {
        public int Token { get; set; } = 1;

        public List<string> Rejected { get; } = [];
    }

    private static (PawJevClient Client, FakeRelay Fake, Session Session) Create(long? group, params Func<HttpRequestMessage, HttpResponseMessage>[] replies)
    {
        var fake = new FakeRelay(replies);
        var session = new Session();
        var client = new PawJevClient(
            new Uri("https://relay.test/"),
            _ => Task.FromResult($"jwt-{session.Token}"),
            (rejected, _) =>
            {
                session.Rejected.Add(rejected);
                session.Token++;
                return Task.CompletedTask;
            },
            () => group,
            fake,
            (_, _) => Task.CompletedTask);
        return (client, fake, session);
    }

    [Fact]
    public async Task ARequestCarriesTheSessionAndTheGroupAndTypeSafesOwnBody()
    {
        var (client, fake, _) = Create(42, _ => Reply(HttpStatusCode.OK, JevClientTests.OkBody));

        JevOutcome outcome = await client.EvaluateAsync(State, CancellationToken.None);

        Assert.True(outcome.Succeeded);
        (HttpRequestMessage request, string body) = Assert.Single(fake.Seen);
        Assert.Equal("https://relay.test/api/v1/paw/systemone", request.RequestUri!.ToString());
        Assert.Equal("Bearer jwt-1", request.Headers.Authorization!.ToString());
        Assert.Equal("42", request.Headers.GetValues(PawJevClient.GroupHeader).Single());
        Assert.Empty(request.Headers.UserAgent);   // the session is bound to the fingerprint it was issued under
        using JsonDocument sent = JsonDocument.Parse(body);
        Assert.Equal(WeChatIntentQuestions.Model, sent.RootElement.GetProperty("model").GetString());
        Assert.True(sent.RootElement.TryGetProperty("questions", out _));
    }

    [Fact]
    public async Task WithoutAGroupNothingIsSent()
    {
        var (client, fake, _) = Create(null, _ => Reply(HttpStatusCode.OK, JevClientTests.OkBody));

        Assert.Equal(JevFailure.GroupUnavailable, (await client.EvaluateAsync(State, CancellationToken.None)).Failure);
        Assert.Empty(fake.Seen);
    }

    [Fact]
    public async Task ARevokedSessionIsRenewedOnceAndTheRequestRetried()
    {
        var (client, fake, session) = Create(42,
            _ => PawError(HttpStatusCode.Unauthorized, "UNAUTHORIZED"),
            _ => Reply(HttpStatusCode.OK, JevClientTests.OkBody));

        JevOutcome outcome = await client.EvaluateAsync(State, CancellationToken.None);

        Assert.True(outcome.Succeeded);
        Assert.Equal(["jwt-1"], session.Rejected);
        Assert.Equal("Bearer jwt-2", fake.Seen[1].Request.Headers.Authorization!.ToString());
    }

    [Fact]
    public async Task A401AfterRenewalMeansSignedOut()
    {
        var (client, fake, _) = Create(42, _ => PawError(HttpStatusCode.Unauthorized, "UNAUTHORIZED"));

        Assert.Equal(JevFailure.SignedOut, (await client.EvaluateAsync(State, CancellationToken.None)).Failure);
        Assert.Equal(2, fake.Seen.Count);
    }

    [Theory]
    // The server side's error table (AI_FLY session, 2026-09-25).
    [InlineData(400, "INVALID_REQUEST", nameof(JevFailure.BadRequest))]
    [InlineData(403, "GROUP_FORBIDDEN", nameof(JevFailure.GroupUnavailable))]
    [InlineData(403, "BILLING_ERROR", nameof(JevFailure.Quota))]
    [InlineData(413, "REQUEST_TOO_LARGE", nameof(JevFailure.BadRequest))]
    [InlineData(429, "QUOTA_EXCEEDED", nameof(JevFailure.Quota))]
    [InlineData(502, "UPSTREAM_ERROR", nameof(JevFailure.ServerError))]
    [InlineData(503, "CONFIG_UNAVAILABLE", nameof(JevFailure.ServiceUnavailable))]
    [InlineData(503, "PRICING_UNAVAILABLE", nameof(JevFailure.ServiceUnavailable))]
    [InlineData(503, "BILLING_SERVICE_ERROR", nameof(JevFailure.ServiceUnavailable))]
    [InlineData(503, "NO_AVAILABLE_ACCOUNTS", nameof(JevFailure.ServiceUnavailable))]
    public async Task TheRelaysOwnErrorsMapToTheirFailure(int status, string code, string failure)
    {
        var (client, fake, _) = Create(42, _ => PawError((HttpStatusCode)status, code));

        JevOutcome outcome = await client.EvaluateAsync(State, CancellationToken.None);

        Assert.Equal(Enum.Parse<JevFailure>(failure), outcome.Failure);
        Assert.Single(fake.Seen);
    }

    [Fact]
    public async Task AModelTheGroupDoesNotAllowIsAGroupProblem()
    {
        // The allowlist's 404, as the server actually sends it (corrected by the server side 2026-09-25).
        var (client, _, _) = Create(42, _ => Reply(HttpStatusCode.NotFound,
            """{"error":{"code":"model_not_found","type":"invalid_request_error","message":"Model \"jev-1.13.0\" is not available for this group"}}"""));

        Assert.Equal(JevFailure.GroupUnavailable, (await client.EvaluateAsync(State, CancellationToken.None)).Failure);
    }

    [Theory]
    [InlineData(429, "RATE_LIMIT_EXCEEDED")]
    [InlineData(429, "UPSTREAM_BUSY")]
    [InlineData(529, "UPSTREAM_BUSY")]
    public async Task PassingLimitsAreRetriedOnce(int status, string code)
    {
        var (client, fake, _) = Create(42,
            _ => Reply((HttpStatusCode)status, """{"error":{"type":"t","code":"CODE","message":"m"}}""".Replace("CODE", code, StringComparison.Ordinal)),
            _ => Reply(HttpStatusCode.OK, JevClientTests.OkBody));

        Assert.True((await client.EvaluateAsync(State, CancellationToken.None)).Succeeded);
        Assert.Equal(2, fake.Seen.Count);
    }

    [Fact]
    public async Task BusyUpstreamIsRetriedOnce()
    {
        var (client, fake, _) = Create(42, _ => Reply((HttpStatusCode)529), _ => Reply(HttpStatusCode.OK, JevClientTests.OkBody));

        Assert.True((await client.EvaluateAsync(State, CancellationToken.None)).Succeeded);
        Assert.Equal(2, fake.Seen.Count);
    }

    [Fact]
    public async Task UnknownModelPassedThroughFallsBackToLatest()
    {
        var (client, fake, _) = Create(42,
            _ => Reply(HttpStatusCode.BadRequest, """{"detail":{"error_type":"api_usage_error","message":"Unknown model: jev-1.13.0"}}"""),
            _ => Reply(HttpStatusCode.OK, JevClientTests.OkBody));

        JevOutcome outcome = await client.EvaluateAsync(State, CancellationToken.None);

        Assert.True(outcome.FellBack);
        Assert.Contains(WeChatIntentQuestions.FallbackModel, fake.Seen[1].Body, StringComparison.Ordinal);
    }
}
