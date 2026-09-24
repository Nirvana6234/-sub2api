using System.Net;
using System.Text;

namespace LanAi.RelayClient.Server.Tests;

/// <summary>
/// A scripted transport: returns a canned reply and records what was sent.
/// </summary>
internal sealed class StubHandler : HttpMessageHandler
{
    private readonly Func<HttpRequestMessage, HttpResponseMessage> _respond;

    private StubHandler(Func<HttpRequestMessage, HttpResponseMessage> respond) => _respond = respond;

    public HttpRequestMessage? LastRequest { get; private set; }

    public string? LastRequestBody { get; private set; }

    /// <summary>Replies with the relay's envelope wrapped around <paramref name="dataJson"/>.</summary>
    public static StubHandler Envelope(HttpStatusCode status, int code, string? dataJson, string? reason = null)
    {
        string reasonPart = reason is null ? string.Empty : $",\"reason\":\"{reason}\"";
        string dataPart = dataJson is null ? string.Empty : $",\"data\":{dataJson}";
        string json = $"{{\"code\":{code},\"message\":\"stub\"{reasonPart}{dataPart}}}";

        return Raw(status, json);
    }

    /// <summary>Replies with an arbitrary body, for the "something else answered" cases.</summary>
    public static StubHandler Raw(HttpStatusCode status, string body, string contentType = "application/json") =>
        new(_ => new HttpResponseMessage(status)
        {
            Content = new StringContent(body, Encoding.UTF8, contentType),
        });

    /// <summary>Fails the way an unreachable host does.</summary>
    public static StubHandler Unreachable() =>
        new(_ => throw new HttpRequestException("no such host is known"));

    /// <summary>
    /// Replies with each envelope in turn, for walking a paginated endpoint.
    /// </summary>
    /// <remarks>
    /// Repeats the final entry once exhausted, so a client that keeps asking is
    /// caught by an assertion on <see cref="RequestCount"/> rather than by a hang.
    /// </remarks>
    public static StubHandler EnvelopeSequence(params string[] dataJson)
    {
        int index = 0;
        return new(_ =>
        {
            string data = dataJson[Math.Min(index, dataJson.Length - 1)];
            index++;
            return new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent(
                    $"{{\"code\":0,\"message\":\"stub\",\"data\":{data}}}",
                    Encoding.UTF8,
                    "application/json"),
            };
        });
    }

    public int RequestCount { get; private set; }

    public RelayServerClient CreateClient(string baseAddress = "https://relay.test/")
    {
        var http = new HttpClient(this) { BaseAddress = new Uri(baseAddress) };
        return new RelayServerClient(http);
    }

    protected override async Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request,
        CancellationToken cancellationToken)
    {
        RequestCount++;
        LastRequest = request;
        LastRequestBody = request.Content is null
            ? null
            : await request.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);

        return _respond(request);
    }
}
