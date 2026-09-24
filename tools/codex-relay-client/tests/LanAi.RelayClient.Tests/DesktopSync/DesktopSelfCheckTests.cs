using System.Net;
using LanAi.RelayClient.DesktopSync;
using Xunit;

namespace LanAi.RelayClient.Tests.DesktopSync;

/// <summary>What each probe of 电脑自检 counts as working.</summary>
public sealed class DesktopSelfCheckTests
{
    private static readonly Uri Server = new("https://relay.test/");

    private static DesktopSelfCheck Check(Func<Uri, HttpStatusCode?> answer, string? relayOrigin = "http://127.0.0.1:5000", bool signedIn = true) =>
        new(new HttpClient(new Handler(answer)), () => relayOrigin, Server, _ => Task.FromResult(signedIn));

    /// <summary>The relay answers 404 to a GET; any answer means it is listening.</summary>
    [Fact]
    public async Task AnyAnswerFromTheRelayCountsAndOnlyAHealthyServerDoes()
    {
        DesktopSelfCheckResult result = await Check(uri => uri.Port == 5000 ? HttpStatusCode.NotFound : HttpStatusCode.ServiceUnavailable)
            .RunAsync(CancellationToken.None);

        Assert.True(result.RelayListening);
        Assert.False(result.ServerReachable);
        Assert.True(result.SignedIn);
    }

    [Fact]
    public async Task NoAnswerAndNoRelayCountAsDown()
    {
        DesktopSelfCheckResult unreachable = await Check(_ => null).RunAsync(CancellationToken.None);
        DesktopSelfCheckResult notStarted = await Check(_ => HttpStatusCode.OK, relayOrigin: null).RunAsync(CancellationToken.None);

        Assert.False(unreachable.RelayListening);
        Assert.False(unreachable.ServerReachable);
        Assert.False(notStarted.RelayListening);
        Assert.True(notStarted.ServerReachable);
    }

    [Fact]
    public async Task TheServerIsAskedForItsHealthPage()
    {
        var asked = new List<string>();
        await Check(uri =>
        {
            lock (asked)
            {
                asked.Add(uri.AbsoluteUri);
            }

            return HttpStatusCode.OK;
        }).RunAsync(CancellationToken.None);

        Assert.Contains("https://relay.test/health", asked);
    }

    /// <summary>Null from the script means the connection fails.</summary>
    private sealed class Handler(Func<Uri, HttpStatusCode?> answer) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) =>
            answer(request.RequestUri!) is HttpStatusCode status
                ? Task.FromResult(new HttpResponseMessage(status))
                : throw new HttpRequestException("connection refused");
    }
}
