using LanAi.Workspace.Injection.Cdp;
using Xunit;

namespace AiSwitch.Injection.Tests;

/// <summary>
/// A script that throws is reported in <c>exceptionDetails</c>, not as a protocol
/// error. Without an explicit check the caller would treat it as success and the
/// overlay would be reported installed while it never ran.
/// </summary>
public sealed class CdpEvaluateFailureTests
{
    private static readonly Uri Endpoint = new("ws://127.0.0.1:9777/devtools/page/1");

    [Fact]
    public async Task EvaluateAsync_ThrowsWhenScriptThrew()
    {
        var transport = new FakeCdpTransport();
        using var connection = new CdpConnection(transport);
        await connection.ConnectAsync(Endpoint, CancellationToken.None);

        var pending = connection.EvaluateAsync("boom()", CancellationToken.None);
        var id = await ReadIdAsync(transport);
        transport.PushResult(
            id,
            """
            {"result":{"type":"object"},
             "exceptionDetails":{"text":"Uncaught",
               "exception":{"description":"ReferenceError: boom is not defined"}}}
            """);

        var exception = await Assert.ThrowsAsync<CdpScriptException>(() => pending);
        Assert.Contains("boom is not defined", exception.Message);
    }

    [Fact]
    public async Task EvaluateAsync_FallsBackToExceptionTextWhenDescriptionAbsent()
    {
        var transport = new FakeCdpTransport();
        using var connection = new CdpConnection(transport);
        await connection.ConnectAsync(Endpoint, CancellationToken.None);

        var pending = connection.EvaluateAsync("boom()", CancellationToken.None);
        var id = await ReadIdAsync(transport);
        transport.PushResult(id, """{"exceptionDetails":{"text":"Uncaught SyntaxError"}}""");

        var exception = await Assert.ThrowsAsync<CdpScriptException>(() => pending);
        Assert.Contains("SyntaxError", exception.Message);
    }

    [Fact]
    public async Task EvaluateAsync_ReturnsValueWhenScriptSucceeded()
    {
        var transport = new FakeCdpTransport();
        using var connection = new CdpConnection(transport);
        await connection.ConnectAsync(Endpoint, CancellationToken.None);

        var pending = connection.EvaluateAsync("1+1", CancellationToken.None);
        var id = await ReadIdAsync(transport);
        transport.PushResult(id, """{"result":{"type":"number","value":2}}""");

        var result = await pending;
        Assert.Equal(2, result.GetProperty("result").GetProperty("value").GetInt32());
    }

    private static async Task<int> ReadIdAsync(FakeCdpTransport transport)
    {
        for (var attempt = 0; attempt < 100; attempt++)
        {
            if (transport.Sent.TryPeek(out var payload))
            {
                using var document = System.Text.Json.JsonDocument.Parse(payload);
                return document.RootElement.GetProperty("id").GetInt32();
            }

            await Task.Delay(10);
        }

        throw new TimeoutException("No command was sent.");
    }
}
