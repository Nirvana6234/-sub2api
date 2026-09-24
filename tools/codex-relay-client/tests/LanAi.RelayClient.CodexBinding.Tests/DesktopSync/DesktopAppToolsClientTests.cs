using System.Buffers.Binary;
using System.Text.Json;
using LanAi.RelayClient.CodexBinding.DesktopSync;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests.DesktopSync;

/// <summary>
/// The desktop app's app-tools pipe is undocumented and shared with pipes that speak
/// something else. These pin down how the client finds it, what it will and will not
/// send, and what it does when the desktop app goes away mid-call.
/// </summary>
public sealed class DesktopAppToolsClientTests
{
    private const string Caller = "01a0c892-0000-7000-8000-000000000001";
    private const string Target = "01a0ced9-0000-7000-8000-000000000002";

    private readonly FakeAppToolsTransport _transport = new();

    private DesktopAppToolsClient Client(Func<string, string?>? findCaller = null) =>
        new(_transport, findCaller ?? (_ => Caller));

    // ---- Finding the pipe ------------------------------------------------------------

    [Fact]
    public async Task DiscoveryPicksThePipeThatAnswersAsAppToolsAndSkipsTheOthers()
    {
        _transport.Add("codex-browser-use-a", FakeAppToolsTransport.NotAppTools);
        _transport.Add("codex-browser-use-b", FakeAppToolsTransport.AppTools((_, _) => new PipeReply.Silence()));
        _transport.Add("codex-browser-use-c", FakeAppToolsTransport.NotAppTools);

        await using DesktopAppToolsClient client = Client();
        AppToolsCapabilities capabilities = await client.ConnectAsync(CancellationToken.None);

        Assert.Equal("codex-browser-use-b", client.PipeName);
        Assert.Equal(new AppToolsCapabilities(true, true, true, true), capabilities);
    }

    /// <summary>A pipe that never answers must cost its own short probe, not the whole call.</summary>
    [Fact]
    public async Task APipeThatNeverAnswersIsSkipped()
    {
        _transport.Add("codex-browser-use-a", _ => new PipeReply.Silence());
        _transport.Add("codex-browser-use-b", FakeAppToolsTransport.AppTools((_, _) => new PipeReply.Silence()));

        await using DesktopAppToolsClient client = Client();
        await client.ConnectAsync(CancellationToken.None);

        Assert.Equal("codex-browser-use-b", client.PipeName);
    }

    [Fact]
    public async Task NoDesktopAppMeansUnavailable()
    {
        _transport.Add("codex-browser-use-a", FakeAppToolsTransport.NotAppTools);

        await using DesktopAppToolsClient client = Client();
        var failure = await Assert.ThrowsAsync<DesktopAppToolsException>(() => client.ListThreadsAsync(5, CancellationToken.None));

        Assert.Equal(DesktopAppToolsFailure.Unavailable, failure.Failure);
    }

    // ---- The contract check ----------------------------------------------------------

    /// <summary>
    /// A new required argument we do not send switches that one call off. The rest keep
    /// working, which is what "read-only degradation" means in practice.
    /// </summary>
    [Fact]
    public async Task ANewRequiredArgumentDisablesOnlyThatCall()
    {
        string sendNeedsHost = ToolSchemas.SendMessage.Replace("\"required\":[\"threadId\",\"prompt\"]", "\"required\":[\"threadId\",\"prompt\",\"hostId\"]");
        string tools = $"[{ToolSchemas.ListThreads},{ToolSchemas.ReadThread},{sendNeedsHost},{ToolSchemas.Navigate}]";
        _transport.Add("codex-browser-use-a", FakeAppToolsTransport.AppTools(
            (_, request) => FakeAppToolsTransport.ToolResult(request, """{"threads":[]}"""), tools));

        await using DesktopAppToolsClient client = Client();
        AppToolsCapabilities capabilities = await client.ConnectAsync(CancellationToken.None);

        Assert.Equal(new AppToolsCapabilities(CanList: true, CanReadStatus: true, CanSend: false, CanNavigate: true), capabilities);
        var failure = await Assert.ThrowsAsync<DesktopAppToolsException>(() => client.SendMessageAsync(Target, "hi", CancellationToken.None));
        Assert.Equal(DesktopAppToolsFailure.Unsupported, failure.Failure);
        Assert.DoesNotContain(_transport.Requests, r => r.Tool == "send_message_to_thread");
        Assert.Empty(await client.ListThreadsAsync(5, CancellationToken.None));
    }

    [Fact]
    public void ARenamedArgumentDisablesTheCall()
    {
        string renamed = ToolSchemas.SendMessage.Replace("\"prompt\":{", "\"message\":{").Replace("\"prompt\"]", "\"message\"]");
        using JsonDocument result = JsonDocument.Parse($$"""{"tools":[{{renamed}}]}""");

        Assert.False(AppToolsContract.Evaluate(result.RootElement).CanSend);
    }

    /// <summary>The model list lives in this description; it changes and must not matter.</summary>
    [Fact]
    public void DescriptionsAreIgnored()
    {
        string described = ToolSchemas.SendMessage.Replace("\"namespace\":", "\"description\":\"Models: gpt-7-new …\",\"namespace\":");
        using JsonDocument result = JsonDocument.Parse($$"""{"tools":[{{described}}]}""");

        Assert.True(AppToolsContract.Evaluate(result.RootElement).CanSend);
    }

    // ---- Calls -----------------------------------------------------------------------

    [Fact]
    public async Task ListThreadsReadsPinnedAndOtherThreadsAndNamesARealCaller()
    {
        JsonElement? seen = null;
        _transport.Add("codex-browser-use-a", FakeAppToolsTransport.AppTools((_, request) =>
        {
            seen = request.Clone();
            return FakeAppToolsTransport.ToolResult(request, """
                {"schemaVersion":4,"untrustedDataNotice":"…",
                 "pinnedThreads":[{"id":"p1","kind":"codex","status":"idle","cwd":"C:\\w","updatedAt":5,"title":"Pinned","pinnedIndex":1}],
                 "threads":[{"id":"t1","kind":"codex","status":"notLoaded","cwd":"C:\\w","updatedAt":7,"title":"Plain","summary":null},
                            {"id":"t2","kind":"codex","status":"systemError","updatedAt":6,"title":null}]}
                """);
        }));

        string? askedFor = null;
        await using DesktopAppToolsClient client = Client(target => { askedFor = target; return Caller; });
        IReadOnlyList<DesktopThread> threads = await client.ListThreadsAsync(500, CancellationToken.None);

        Assert.Equal(["p1", "t1", "t2"], threads.Select(t => t.Id));
        Assert.Equal(["idle", "notLoaded", "systemError"], threads.Select(t => t.Status));
        Assert.Equal("", askedFor);

        JsonElement parameters = seen!.Value.GetProperty("params");
        Assert.Equal("codex_app", parameters.GetProperty("namespace").GetString());
        Assert.Equal(Caller, parameters.GetProperty("threadId").GetString());
        Assert.Equal(50, parameters.GetProperty("arguments").GetProperty("limit").GetInt32());
    }

    [Fact]
    public async Task SendingNamesAnotherConversationAsTheCaller()
    {
        JsonElement? seen = null;
        _transport.Add("codex-browser-use-a", FakeAppToolsTransport.AppTools((_, request) =>
        {
            seen = request.Clone();
            return FakeAppToolsTransport.ToolResult(request, $$"""{"threadId":"{{Target}}"}""");
        }));

        string? askedFor = null;
        await using DesktopAppToolsClient client = Client(target => { askedFor = target; return Caller; });
        await client.SendMessageAsync(Target, "请只回复 OK", CancellationToken.None);

        Assert.Equal(Target, askedFor);
        JsonElement parameters = seen!.Value.GetProperty("params");
        Assert.Equal(Caller, parameters.GetProperty("threadId").GetString());
        Assert.Equal(Target, parameters.GetProperty("arguments").GetProperty("threadId").GetString());
        Assert.Equal("请只回复 OK", parameters.GetProperty("arguments").GetProperty("prompt").GetString());
        Assert.False(parameters.GetProperty("arguments").TryGetProperty("model", out _));
    }

    [Fact]
    public async Task NoOtherConversationMeansNoCaller()
    {
        _transport.Add("codex-browser-use-a", FakeAppToolsTransport.AppTools((_, _) => new PipeReply.Silence()));

        await using DesktopAppToolsClient client = Client(_ => null);
        var failure = await Assert.ThrowsAsync<DesktopAppToolsException>(() => client.SendMessageAsync(Target, "hi", CancellationToken.None));

        Assert.Equal(DesktopAppToolsFailure.NoCaller, failure.Failure);
    }

    /// <summary>What the pipe answers for, among other things, a caller id that does not exist.</summary>
    [Fact]
    public async Task AJsonRpcErrorIsReportedAsSuch()
    {
        _transport.Add("codex-browser-use-a", FakeAppToolsTransport.AppTools(
            (_, request) => new PipeReply.Json(FakeAppToolsTransport.Error(request, -32000, "Codex app tool request failed"))));

        await using DesktopAppToolsClient client = Client();
        var failure = await Assert.ThrowsAsync<DesktopAppToolsException>(() => client.ListThreadsAsync(5, CancellationToken.None));

        Assert.Equal(DesktopAppToolsFailure.RpcError, failure.Failure);
        Assert.Contains("-32000", failure.Message);
    }

    [Fact]
    public async Task AToolThatReportsFailureCarriesItsOwnText()
    {
        _transport.Add("codex-browser-use-a", FakeAppToolsTransport.AppTools(
            (_, request) => FakeAppToolsTransport.ToolResult(request, "wait_threads cannot wait on the calling thread.", success: false)));

        await using DesktopAppToolsClient client = Client();
        var failure = await Assert.ThrowsAsync<DesktopAppToolsException>(() => client.NavigateToAsync(Target, CancellationToken.None));

        Assert.Equal(DesktopAppToolsFailure.ToolFailed, failure.Failure);
        Assert.Equal("wait_threads cannot wait on the calling thread.", failure.Message);
    }

    [Theory]
    [InlineData("""{"type":"active","activeFlags":["waitingOnApproval"]}""", "active", true)]
    [InlineData("""{"type":"idle"}""", "idle", false)]
    [InlineData("""{"type":"notLoaded"}""", "notLoaded", false)]
    public async Task StatusComesFromReadThread(string status, string type, bool waiting)
    {
        _transport.Add("codex-browser-use-a", FakeAppToolsTransport.AppTools((tool, request) =>
        {
            Assert.Equal("read_thread", tool);
            return FakeAppToolsTransport.ToolResult(request, $$"""{"schemaVersion":1,"thread":{"id":"{{Target}}","status":{{status}}},"turns":[]}""");
        }));

        await using DesktopAppToolsClient client = Client();
        DesktopThreadStatus result = await client.GetThreadStatusAsync(Target, CancellationToken.None);

        Assert.Equal(type, result.Type);
        Assert.Equal(waiting, result.WaitingOnApproval);
    }

    // ---- The desktop app going away --------------------------------------------------

    /// <summary>
    /// The pipe is renamed when the desktop app restarts. A read that finds the old one
    /// gone rediscovers and succeeds on the new one.
    /// </summary>
    [Fact]
    public async Task AReadAfterARestartFindsTheRenamedPipe()
    {
        _transport.Add("codex-browser-use-old", FakeAppToolsTransport.AppTools((_, _) => new PipeReply.Hangup()));

        await using DesktopAppToolsClient client = Client();
        await client.ConnectAsync(CancellationToken.None);

        _transport.Remove("codex-browser-use-old");
        _transport.Add("codex-browser-use-new", FakeAppToolsTransport.AppTools(
            (_, request) => FakeAppToolsTransport.ToolResult(request, """{"threads":[{"id":"t1","status":"idle","updatedAt":1}]}""")));

        IReadOnlyList<DesktopThread> threads = await client.ListThreadsAsync(5, CancellationToken.None);

        Assert.Equal("t1", Assert.Single(threads).Id);
        Assert.Equal("codex-browser-use-new", client.PipeName);
    }

    /// <summary>
    /// If the pipe dies after a send went out, the message may already be running.
    /// Sending it again could run it twice, so the send is reported, not retried.
    /// </summary>
    [Fact]
    public async Task ASendIsNeverRetried()
    {
        _transport.Add("codex-browser-use-a", FakeAppToolsTransport.AppTools((_, _) => new PipeReply.Hangup()));

        await using DesktopAppToolsClient client = Client();
        var failure = await Assert.ThrowsAsync<DesktopAppToolsException>(() => client.SendMessageAsync(Target, "hi", CancellationToken.None));

        Assert.Equal(DesktopAppToolsFailure.Unavailable, failure.Failure);
        Assert.Single(_transport.Requests, r => r.Tool == "send_message_to_thread");
    }

    // ---- Framing ---------------------------------------------------------------------

    [Fact]
    public async Task FramesRoundTrip()
    {
        using var stream = new MemoryStream();
        await AppToolsFraming.WriteFrameAsync(stream, "{\"a\":\"中\"}"u8.ToArray(), CancellationToken.None);
        stream.Position = 0;

        Assert.Equal("{\"a\":\"中\"}"u8.ToArray(), await AppToolsFraming.ReadFrameAsync(stream, CancellationToken.None));
    }

    [Fact]
    public async Task AnOversizedFrameIsRefusedBeforeAllocating()
    {
        byte[] header = new byte[4];
        BinaryPrimitives.WriteUInt32LittleEndian(header, AppToolsFraming.MaxFrameBytes + 1u);
        using var stream = new MemoryStream(header);

        await Assert.ThrowsAsync<InvalidDataException>(() => AppToolsFraming.ReadFrameAsync(stream, CancellationToken.None));
    }
}
