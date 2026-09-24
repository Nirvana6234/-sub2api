using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.Workspace.Injection.Cdp;
using Xunit;

namespace AiSwitch.Injection.Tests;

public sealed class CdpConnectionTests
{
    private static readonly Uri Endpoint = new("ws://127.0.0.1:9777/devtools/page/1");

    private static async Task<(CdpConnection Connection, FakeCdpTransport Transport)> ConnectAsync()
    {
        var transport = new FakeCdpTransport();
        var connection = new CdpConnection(transport);
        await connection.ConnectAsync(Endpoint, CancellationToken.None);
        return (connection, transport);
    }

    [Fact]
    public async Task InvokeAsync_SerializesMethodAndParameters()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;

        var pending = connection.InvokeAsync(
            "Page.addScriptToEvaluateOnNewDocument",
            new JsonObject { ["source"] = "1+1" },
            CancellationToken.None);

        var sent = await WaitForSentAsync(transport);
        using var document = JsonDocument.Parse(sent);
        Assert.Equal(
            "Page.addScriptToEvaluateOnNewDocument",
            document.RootElement.GetProperty("method").GetString());
        Assert.Equal("1+1", document.RootElement.GetProperty("params").GetProperty("source").GetString());

        var id = document.RootElement.GetProperty("id").GetInt32();
        transport.PushResult(id, """{"identifier":"7"}""");

        var result = await pending;
        Assert.Equal("7", result.GetProperty("identifier").GetString());
    }

    [Fact]
    public async Task InvokeAsync_CorrelatesRepliesArrivingOutOfOrder()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;

        var first = connection.InvokeAsync("First", null, CancellationToken.None);
        var firstId = await ReadIdAsync(transport, 1);
        var second = connection.InvokeAsync("Second", null, CancellationToken.None);
        var secondId = await ReadIdAsync(transport, 2);

        Assert.NotEqual(firstId, secondId);

        transport.PushResult(secondId, """{"which":"second"}""");
        transport.PushResult(firstId, """{"which":"first"}""");

        Assert.Equal("second", (await second).GetProperty("which").GetString());
        Assert.Equal("first", (await first).GetProperty("which").GetString());
    }

    [Fact]
    public async Task InvokeAsync_ThrowsProtocolExceptionOnErrorReply()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;

        var pending = connection.InvokeAsync("Page.enable", null, CancellationToken.None);
        var id = await ReadIdAsync(transport, 1);
        transport.PushError(id, -32000, "target closed");

        var exception = await Assert.ThrowsAsync<CdpProtocolException>(() => pending);
        Assert.Equal(-32000, exception.Code);
        Assert.Contains("target closed", exception.Message);
    }

    [Fact]
    public async Task EventsAreRaisedAndDoNotCompleteCommands()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;

        var received = new TaskCompletionSource<CdpEvent>(
            TaskCreationOptions.RunContinuationsAsynchronously);
        connection.EventReceived += (_, e) => received.TrySetResult(e);

        var pending = connection.InvokeAsync("Page.enable", null, CancellationToken.None);
        var id = await ReadIdAsync(transport, 1);

        transport.PushEvent("Page.frameNavigated", """{"frame":{"id":"main"}}""");

        var raised = await received.Task;
        Assert.Equal("Page.frameNavigated", raised.Method);
        Assert.Equal("main", raised.Parameters.GetProperty("frame").GetProperty("id").GetString());
        Assert.False(pending.IsCompleted);

        transport.PushResult(id, "{}");
        await pending;
    }

    [Fact]
    public async Task ChannelCloseFailsPendingCommands()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;

        var pending = connection.InvokeAsync("Page.enable", null, CancellationToken.None);
        await ReadIdAsync(transport, 1);

        transport.Close();

        await Assert.ThrowsAsync<IOException>(() => pending);
    }

    [Fact]
    public async Task MalformedMessageIsIgnoredAndDoesNotKillReceiveLoop()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;

        transport.PushRaw("not json at all");

        var pending = connection.InvokeAsync("Page.enable", null, CancellationToken.None);
        var id = await ReadIdAsync(transport, 1);
        transport.PushResult(id, """{"ok":true}""");

        Assert.True((await pending).GetProperty("ok").GetBoolean());
    }

    [Fact]
    public async Task DisposeDisposesTransport()
    {
        var (connection, transport) = await ConnectAsync();
        connection.Dispose();
        Assert.True(transport.Disposed);
    }

    private static async Task<string> WaitForSentAsync(FakeCdpTransport transport)
    {
        for (var attempt = 0; attempt < 100; attempt++)
        {
            if (transport.Sent.TryPeek(out var payload))
            {
                return payload;
            }

            await Task.Delay(10);
        }

        throw new TimeoutException("No command was sent.");
    }

    private static async Task<int> ReadIdAsync(FakeCdpTransport transport, int expectedCount)
    {
        for (var attempt = 0; attempt < 100; attempt++)
        {
            if (transport.Sent.Count >= expectedCount)
            {
                var payload = transport.Sent.ElementAt(expectedCount - 1);
                using var document = JsonDocument.Parse(payload);
                return document.RootElement.GetProperty("id").GetInt32();
            }

            await Task.Delay(10);
        }

        throw new TimeoutException($"Expected {expectedCount} command(s).");
    }
}
