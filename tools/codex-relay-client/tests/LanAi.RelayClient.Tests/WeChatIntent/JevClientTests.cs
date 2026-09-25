using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.WeChatIntent;
using Xunit;

namespace LanAi.RelayClient.Tests.WeChatIntent;

/// <summary>The error table of docs §5.6, row by row, against a fake TypeSafe.</summary>
public sealed class JevClientTests
{
    /// <summary>TypeSafe's documented example response, verbatim in shape.</summary>
    internal const string OkBody = """
        {"model":"jev-1.13.0","answers":{
          "intent":{"type":"choice","choice":"test","probabilities":{"test":0.93,"reassurance":0.05,"other":0.02},"confidence":0.9},
          "emotion":{"type":"choice","choice":"annoyed","probabilities":{"annoyed":0.8,"calm":0.2},"confidence":0.7},
          "risk":{"type":"score","score":2.8,"legend":{"0":"a","1":"b","2":"c","3":"d","4":"e"},"probabilities":{"0":0.0,"1":0.05,"2":0.2,"3":0.7,"4":0.05},"confidence":0.6},
          "needs_quick_reply":{"type":"noul","noul":0.79},
          "best_action":{"type":"choice","choice":"explain","probabilities":{"explain":0.8,"confirm":0.1,"ask":0.1},"confidence":0.7}},
         "usage":{"input_tokens":1132,"output_tokens":90}}
        """;

    private static readonly JevState State = JevRequestBody.StateFor(
        [new ChatItem(ChatSpeaker.Them, "你今天是不是又忘了"), new ChatItem(ChatSpeaker.Me, "记得")],
        [new ChatItem(ChatSpeaker.Them, "那你说")]);

    private sealed class FakeTypeSafe(params Func<HttpRequestMessage, HttpResponseMessage>[] replies) : HttpMessageHandler
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

    private static (DirectJevClient Client, FakeTypeSafe Fake, List<TimeSpan> Waits) Create(string? key, params Func<HttpRequestMessage, HttpResponseMessage>[] replies)
    {
        var fake = new FakeTypeSafe(replies);
        var waits = new List<TimeSpan>();
        var client = new DirectJevClient(() => key, () => fake, (wait, _) =>
        {
            waits.Add(wait);
            return Task.CompletedTask;
        });
        return (client, fake, waits);
    }

    [Fact]
    public async Task ASuccessCarriesTheAnswersAndTheRequestIsWhatTheDesignSays()
    {
        var (client, fake, _) = Create("apikey-123", _ => Reply(HttpStatusCode.OK, OkBody));

        JevOutcome outcome = await client.EvaluateAsync(State, CancellationToken.None);

        Assert.True(outcome.Succeeded);
        Assert.Equal("jev-1.13.0", outcome.Response!.Model);
        Assert.Equal(1132, outcome.Response.Usage!.InputTokens);
        (HttpRequestMessage request, string body) = Assert.Single(fake.Seen);
        Assert.Equal("https://api.typesafe.ai/v1/systemone", request.RequestUri!.ToString());
        Assert.Equal("Bearer apikey-123", request.Headers.Authorization!.ToString());

        using JsonDocument sent = JsonDocument.Parse(body);
        Assert.Equal(WeChatIntentQuestions.Model, sent.RootElement.GetProperty("model").GetString());
        Assert.Equal("那你说", sent.RootElement.GetProperty("state").GetProperty("latest_from_them")[0].GetString());
        Assert.Equal("me", sent.RootElement.GetProperty("state").GetProperty("conversation")[1].GetProperty("speaker").GetString());
        Assert.Equal(
            ["intent", "emotion", "risk", "needs_quick_reply", "best_action"],
            sent.RootElement.GetProperty("questions").EnumerateObject().Select(p => p.Name));
    }

    [Fact]
    public async Task NoKeyMeansNoRequest()
    {
        var (client, fake, _) = Create(null, _ => Reply(HttpStatusCode.OK, OkBody));

        Assert.Equal(JevFailure.NoKey, (await client.EvaluateAsync(State, CancellationToken.None)).Failure);
        Assert.Empty(fake.Seen);
    }

    [Theory]
    [InlineData(HttpStatusCode.Unauthorized, nameof(JevFailure.InvalidKey))]
    [InlineData(HttpStatusCode.PaymentRequired, nameof(JevFailure.Quota))]
    [InlineData(HttpStatusCode.Forbidden, nameof(JevFailure.Quota))]
    [InlineData(HttpStatusCode.UnprocessableEntity, nameof(JevFailure.BadRequest))]
    [InlineData(HttpStatusCode.InternalServerError, nameof(JevFailure.ServerError))]
    public async Task StatusesMapToTheirFailure(HttpStatusCode status, string failure)
    {
        JevFailure expected = Enum.Parse<JevFailure>(failure);
        var (client, fake, _) = Create("k", _ => Reply(status));

        JevOutcome outcome = await client.EvaluateAsync(State, CancellationToken.None);

        Assert.Equal(expected, outcome.Failure);
        Assert.Single(fake.Seen);
    }

    [Theory]
    [InlineData(429)]
    [InlineData(529)]
    public async Task BusyIsRetriedOnceAfterRetryAfter(int status)
    {
        var (client, fake, waits) = Create("k",
            _ =>
            {
                HttpResponseMessage busy = Reply((HttpStatusCode)status);
                busy.Headers.RetryAfter = new System.Net.Http.Headers.RetryConditionHeaderValue(TimeSpan.FromSeconds(2));
                return busy;
            },
            _ => Reply(HttpStatusCode.OK, OkBody));

        JevOutcome outcome = await client.EvaluateAsync(State, CancellationToken.None);

        Assert.True(outcome.Succeeded);
        Assert.Equal(2, fake.Seen.Count);
        Assert.Equal([TimeSpan.FromSeconds(2)], waits);
    }

    [Fact]
    public async Task BusyTwiceGivesUpAndNeverFallsBackToAnotherModel()
    {
        var (client, fake, _) = Create("k", _ => Reply((HttpStatusCode)529));

        JevOutcome outcome = await client.EvaluateAsync(State, CancellationToken.None);

        Assert.Equal(JevFailure.Busy, outcome.Failure);
        Assert.All(fake.Seen, s => Assert.Contains(WeChatIntentQuestions.Model, s.Body, StringComparison.Ordinal));
    }

    [Fact]
    public async Task OnlyUnknownModelFallsBackToLatest()
    {
        // Measured: this is what TypeSafe answers for a model name it does not know.
        var (client, fake, _) = Create("k",
            _ => Reply(HttpStatusCode.BadRequest, """{"detail":{"error_type":"api_usage_error","message":"Unknown model: jev-1.13.0"}}"""),
            _ => Reply(HttpStatusCode.OK, OkBody));

        JevOutcome outcome = await client.EvaluateAsync(State, CancellationToken.None);

        Assert.True(outcome.Succeeded);
        Assert.True(outcome.FellBack);
        Assert.Contains(WeChatIntentQuestions.FallbackModel, fake.Seen[1].Body, StringComparison.Ordinal);
    }

    [Fact]
    public async Task AnyOtherBadRequestDoesNotFallBack()
    {
        var (client, fake, _) = Create("k", _ => Reply(HttpStatusCode.BadRequest, """{"detail":{"message":"questions: invalid"}}"""));

        JevOutcome outcome = await client.EvaluateAsync(State, CancellationToken.None);

        Assert.Equal(JevFailure.BadRequest, outcome.Failure);
        Assert.Single(fake.Seen);
    }

    [Fact]
    public async Task ANetworkFailureIsReportedAsSuch()
    {
        var (client, _, _) = Create("k", _ => throw new HttpRequestException("no route"));

        Assert.Equal(JevFailure.Network, (await client.EvaluateAsync(State, CancellationToken.None)).Failure);
    }

    [Theory]
    [InlineData(HttpStatusCode.OK, """{"models":[{"name":"jev-latest"},{"name":"jev-preview"}]}""", nameof(JevKeyCheck.Valid))]
    [InlineData(HttpStatusCode.Unauthorized, "{}", nameof(JevKeyCheck.Invalid))]
    [InlineData(HttpStatusCode.BadGateway, "{}", nameof(JevKeyCheck.Unreachable))]
    public async Task AKeyIsCheckedAgainstTheModelList(HttpStatusCode status, string body, string check)
    {
        JevKeyCheck expected = Enum.Parse<JevKeyCheck>(check);
        var (client, fake, _) = Create(null, _ => Reply(status, body));

        Assert.Equal(expected, await client.CheckKeyAsync(" apikey-9 ", CancellationToken.None));
        Assert.Equal("https://api.typesafe.ai/v1/models", fake.Seen[0].Request.RequestUri!.ToString());
        Assert.Equal("Bearer apikey-9", fake.Seen[0].Request.Headers.Authorization!.ToString());
    }
}
