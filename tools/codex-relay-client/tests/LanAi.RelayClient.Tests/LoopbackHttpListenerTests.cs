using System.Net;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>Starting on a port someone else holds moves on instead of failing.</summary>
public sealed class LoopbackHttpListenerTests
{
    [Fact]
    public async Task AFreePreferredPortIsUsedAndAnswers()
    {
        int free = LoopbackHttpListener.ProbeFreePort();

        using HttpListener listener = LoopbackHttpListener.Start(free, out int port);

        Assert.Equal(free, port);
        await AssertAnswersAsync(listener, port);
    }

    /// <summary>
    /// The collision parallel tests kept hitting: the probe cannot see another
    /// HttpListener's registration, only starting can.
    /// </summary>
    [Fact]
    public async Task APortAnotherListenerRegisteredFallsBackToAFreshOne()
    {
        using HttpListener holder = LoopbackHttpListener.Start(null, out int taken);

        using HttpListener listener = LoopbackHttpListener.Start(taken, out int port);

        Assert.NotEqual(taken, port);
        await AssertAnswersAsync(listener, port);
    }

    /// <summary>http.sys would register it anyway and then never receive a request.</summary>
    [Fact]
    public async Task APortASocketHoldsIsNotTaken()
    {
        using var occupier = new System.Net.Sockets.TcpListener(IPAddress.Loopback, 0);
        occupier.Start();
        int taken = ((IPEndPoint)occupier.LocalEndpoint).Port;

        using HttpListener listener = LoopbackHttpListener.Start(taken, out int port);

        Assert.NotEqual(taken, port);
        await AssertAnswersAsync(listener, port);
    }

    private static async Task AssertAnswersAsync(HttpListener listener, int port)
    {
        Task serve = Task.Run(async () =>
        {
            HttpListenerContext context = await listener.GetContextAsync();
            context.Response.StatusCode = 204;
            context.Response.Close();
        });

        using var client = new HttpClient { Timeout = TimeSpan.FromSeconds(10) };
        using HttpResponseMessage response = await client.GetAsync(new Uri($"http://127.0.0.1:{port}/"));
        await serve;

        Assert.Equal(HttpStatusCode.NoContent, response.StatusCode);
    }
}
