using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.Workspace.Injection;
using LanAi.Workspace.Injection.Cdp;
using Xunit;

namespace AiSwitch.Injection.Tests;

public sealed class CoflyOverlayInjectorTests
{
    private const string Script = "window.__cofly = {};";
    private static readonly Uri Endpoint = new("ws://127.0.0.1:9777/devtools/page/1");

    private static async Task<(CdpConnection Connection, FakeCdpTransport Transport)> ConnectAsync()
    {
        var transport = new FakeCdpTransport { AutoAcknowledge = true };
        var connection = new CdpConnection(transport);
        await connection.ConnectAsync(Endpoint, CancellationToken.None);
        return (connection, transport);
    }

    private static List<string> MethodsSent(FakeCdpTransport transport)
    {
        var methods = new List<string>();
        foreach (var payload in transport.Sent)
        {
            using var document = JsonDocument.Parse(payload);
            methods.Add(document.RootElement.GetProperty("method").GetString() ?? string.Empty);
        }

        return methods;
    }

    [Fact]
    public async Task InstallAsync_EnablesPageRegistersScriptThenEvaluatesOnce()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var injector = new CoflyOverlayInjector(connection);

        await injector.InstallAsync(Script, CancellationToken.None);

        Assert.Equal(
            new[] { "Page.enable", "Page.addScriptToEvaluateOnNewDocument", "Runtime.evaluate" },
            MethodsSent(transport));
    }

    [Fact]
    public async Task InstallAsync_RegistersScriptForFutureDocuments()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var injector = new CoflyOverlayInjector(connection);

        await injector.InstallAsync(Script, CancellationToken.None);

        var registration = transport.Sent.First(payload =>
            payload.Contains("addScriptToEvaluateOnNewDocument", StringComparison.Ordinal));
        using var document = JsonDocument.Parse(registration);
        Assert.Equal(
            Script,
            document.RootElement.GetProperty("params").GetProperty("source").GetString());
    }

    [Fact]
    public async Task MainFrameNavigationReinjects()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var injector = new CoflyOverlayInjector(connection);
        await injector.InstallAsync(Script, CancellationToken.None);
        var before = transport.Sent.Count;

        transport.PushEvent("Page.frameNavigated", """{"frame":{"id":"main"}}""");

        await WaitForCountAsync(transport, before + 1);
        Assert.Equal("Runtime.evaluate", MethodsSent(transport).Last());
    }

    [Fact]
    public async Task SubFrameNavigationDoesNotReinject()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var injector = new CoflyOverlayInjector(connection);
        await injector.InstallAsync(Script, CancellationToken.None);
        var before = transport.Sent.Count;

        transport.PushEvent(
            "Page.frameNavigated",
            """{"frame":{"id":"child","parentId":"main"}}""");

        await Task.Delay(150);
        Assert.Equal(before, transport.Sent.Count);
    }

    /// <summary>
    /// A sub-frame event arriving before any main-frame event must not be latched as
    /// the main frame, otherwise later real main-frame navigations stop re-injecting.
    /// </summary>
    [Fact]
    public async Task SubFrameArrivingFirstDoesNotClaimMainFrame()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var injector = new CoflyOverlayInjector(connection);
        await injector.InstallAsync(Script, CancellationToken.None);
        var before = transport.Sent.Count;

        transport.PushEvent(
            "Page.frameNavigated",
            """{"frame":{"id":"child","parentId":"main"}}""");
        await Task.Delay(100);
        Assert.Equal(before, transport.Sent.Count);

        transport.PushEvent("Page.frameNavigated", """{"frame":{"id":"main"}}""");

        await WaitForCountAsync(transport, before + 1);
    }

    [Fact]
    public async Task UnrelatedEventIsIgnored()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var injector = new CoflyOverlayInjector(connection);
        await injector.InstallAsync(Script, CancellationToken.None);
        var before = transport.Sent.Count;

        transport.PushEvent("Network.responseReceived", """{"response":{"status":200}}""");

        await Task.Delay(150);
        Assert.Equal(before, transport.Sent.Count);
    }

    [Fact]
    public async Task PushStateAsync_SerializesStateThroughRenderHook()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var injector = new CoflyOverlayInjector(connection);
        await injector.InstallAsync(Script, CancellationToken.None);

        await injector.PushStateAsync(
            new JsonObject { ["route"] = "cofly", ["tokens"] = 42 },
            CancellationToken.None);

        using var document = JsonDocument.Parse(transport.LastSent);
        var expression = document.RootElement
            .GetProperty("params")
            .GetProperty("expression")
            .GetString();
        Assert.Contains("window.__cofly.render", expression!);
        Assert.Contains("\"tokens\":42", expression!);
    }

    [Fact]
    public async Task DisposeStopsReinjecting()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        var injector = new CoflyOverlayInjector(connection);
        await injector.InstallAsync(Script, CancellationToken.None);
        var before = transport.Sent.Count;

        injector.Dispose();
        transport.PushEvent("Page.frameNavigated", """{"frame":{"id":"main"}}""");

        await Task.Delay(150);
        Assert.Equal(before, transport.Sent.Count);
    }

    [Fact]
    public async Task InstallAsync_RejectsEmptyScript()
    {
        var (connection, _) = await ConnectAsync();
        using var __ = connection;
        using var injector = new CoflyOverlayInjector(connection);

        await Assert.ThrowsAsync<ArgumentException>(
            () => injector.InstallAsync("  ", CancellationToken.None));
    }

    private static async Task WaitForCountAsync(FakeCdpTransport transport, int expected)
    {
        for (var attempt = 0; attempt < 100; attempt++)
        {
            if (transport.Sent.Count >= expected)
            {
                return;
            }

            await Task.Delay(10);
        }

        throw new TimeoutException($"Expected {expected} command(s), saw {transport.Sent.Count}.");
    }
}
