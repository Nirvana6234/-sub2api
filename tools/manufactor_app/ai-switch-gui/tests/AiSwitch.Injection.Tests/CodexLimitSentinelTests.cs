using System.Text.Json;
using LanAi.Workspace.Injection.Cdp;
using LanAi.Workspace.Injection.Sentinel;
using Xunit;

namespace AiSwitch.Injection.Tests;

public sealed class CodexLimitSentinelTests
{
    private static readonly Uri Endpoint = new("ws://127.0.0.1:9777/devtools/page/1");

    private static async Task<(CdpConnection Connection, FakeCdpTransport Transport)> ConnectAsync()
    {
        var transport = new FakeCdpTransport();
        var connection = new CdpConnection(transport);
        await connection.ConnectAsync(Endpoint, CancellationToken.None);
        return (connection, transport);
    }

    private static CodexLimitSentinel Sentinel(
        CdpConnection connection,
        double approachingPercent = 85d)
        => new(connection, new CodexLimitSentinelOptions { ApproachingPercent = approachingPercent });

    // ---- policy: facts -> level -------------------------------------------------

    [Fact]
    public void VisibleModalMeansReached()
    {
        using var connection = new CdpConnection(new FakeCdpTransport());
        var sentinel = Sentinel(connection);

        Assert.Equal(
            CodexLimitLevel.Reached,
            sentinel.Classify(new CodexLimitFacts { ModalVisible = true }));
    }

    [Fact]
    public void ReachedTextMeansReachedEvenWithoutModal()
    {
        using var connection = new CdpConnection(new FakeCdpTransport());
        var sentinel = Sentinel(connection);

        Assert.Equal(
            CodexLimitLevel.Reached,
            sentinel.Classify(new CodexLimitFacts { ReachedText = true }));
    }

    [Theory]
    [InlineData(84d, CodexLimitLevel.Normal)]
    [InlineData(85d, CodexLimitLevel.Approaching)]
    [InlineData(99d, CodexLimitLevel.Approaching)]
    [InlineData(100d, CodexLimitLevel.Reached)]
    public void PercentageDrivesEarlyWarning(double percent, CodexLimitLevel expected)
    {
        using var connection = new CdpConnection(new FakeCdpTransport());
        var sentinel = Sentinel(connection);

        Assert.Equal(expected, sentinel.Classify(new CodexLimitFacts { UsedPercent = percent }));
    }

    [Fact]
    public void ThresholdIsConfigurable()
    {
        using var connection = new CdpConnection(new FakeCdpTransport());
        var sentinel = Sentinel(connection, approachingPercent: 50d);

        Assert.Equal(
            CodexLimitLevel.Approaching,
            sentinel.Classify(new CodexLimitFacts { UsedPercent = 60d }));
    }

    /// <summary>
    /// The banner and the "Usage limits" heading appear in normal navigation too, so
    /// they must not by themselves trigger a switch prompt.
    /// </summary>
    [Fact]
    public void BannerOrHeadingAloneIsNotAPrompt()
    {
        using var connection = new CdpConnection(new FakeCdpTransport());
        var sentinel = Sentinel(connection);

        Assert.Equal(
            CodexLimitLevel.Normal,
            sentinel.Classify(new CodexLimitFacts { BannerVisible = true, UsageLimitsText = true }));
    }

    // ---- parsing ----------------------------------------------------------------

    [Fact]
    public void ParseFactsReadsDetectorPayload()
    {
        var facts = CodexLimitSentinel.ParseFacts(
            """
            {"version":1,"modal":true,"banner":false,"reachedText":true,
             "usageLimitsText":true,"resetText":"resets at 3:00 PM","percent":100,
             "signals":["surface:modal","text:reached"],"capped":false,"roots":7}
            """);

        Assert.True(facts.ModalVisible);
        Assert.False(facts.BannerVisible);
        Assert.True(facts.ReachedText);
        Assert.Equal("resets at 3:00 PM", facts.ResetText);
        Assert.Equal(100d, facts.UsedPercent);
        Assert.Equal(7, facts.ShadowRoots);
        Assert.Equal(["surface:modal", "text:reached"], facts.Signals);
    }

    [Fact]
    public void ParseFactsToleratesMissingAndNullMembers()
    {
        var facts = CodexLimitSentinel.ParseFacts("""{"modal":false,"percent":null}""");

        Assert.False(facts.ModalVisible);
        Assert.Null(facts.UsedPercent);
        Assert.Null(facts.ResetText);
        Assert.Empty(facts.Signals);
    }

    [Fact]
    public void ParseFactsToleratesGarbage()
    {
        Assert.False(CodexLimitSentinel.ParseFacts("not json").ModalVisible);
        Assert.False(CodexLimitSentinel.ParseFacts("[]").ModalVisible);
    }

    // ---- polling ----------------------------------------------------------------

    [Fact]
    public async Task PollOnceReportsReachedFromDetector()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var sentinel = Sentinel(connection);

        var pending = sentinel.PollOnceAsync(CancellationToken.None);
        await ReplyWithSnapshotAsync(
            transport,
            """{"modal":true,"percent":100,"resetText":"resets in 2 hours"}""");

        var snapshot = await pending;
        Assert.Equal(CodexLimitLevel.Reached, snapshot.Level);
        Assert.True(snapshot.ShouldPromptSwitch);
        Assert.Equal("resets in 2 hours", snapshot.Facts.ResetText);
    }

    [Fact]
    public async Task PollOnceReportsUnknownWhenDetectorAbsent()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var sentinel = Sentinel(connection);

        var pending = sentinel.PollOnceAsync(CancellationToken.None);
        // A null return means the detector is not present in this document.
        var id = await ReadIdAsync(transport, 1);
        transport.PushResult(id, """{"result":{"type":"object","value":null}}""");

        // That absence triggers a re-install, which also needs an answer.
        var reinstallId = await ReadIdAsync(transport, 2);
        transport.PushResult(reinstallId, """{"result":{"type":"boolean","value":true}}""");

        var snapshot = await pending;
        Assert.Equal(CodexLimitLevel.Unknown, snapshot.Level);
        Assert.False(snapshot.ShouldPromptSwitch);
    }

    /// <summary>
    /// An unanswered CDP command must not stall the poll loop forever; it degrades to
    /// Unknown once the per-poll budget expires.
    /// </summary>
    [Fact]
    public async Task UnansweredPollTimesOutAsUnknown()
    {
        var (connection, _) = await ConnectAsync();
        using var __ = connection;
        using var sentinel = new CodexLimitSentinel(
            connection,
            new CodexLimitSentinelOptions { PollTimeout = TimeSpan.FromMilliseconds(300) });

        var snapshot = await sentinel.PollOnceAsync(CancellationToken.None);

        Assert.Equal(CodexLimitLevel.Unknown, snapshot.Level);
    }

    [Fact]
    public async Task CallerCancellationPropagates()
    {
        var (connection, _) = await ConnectAsync();
        using var __ = connection;
        using var sentinel = Sentinel(connection);
        using var cts = new CancellationTokenSource();
        cts.Cancel();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(
            () => sentinel.PollOnceAsync(cts.Token));
    }

    /// <summary>
    /// A missing detector must trigger a re-install, otherwise the sentinel goes
    /// permanently blind after the page swaps documents.
    /// </summary>
    [Fact]
    public async Task DetectorAbsenceTriggersReinstall()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var sentinel = Sentinel(connection);

        var pending = sentinel.PollOnceAsync(CancellationToken.None);
        var pollId = await ReadIdAsync(transport, 1);
        transport.PushResult(pollId, """{"result":{"type":"object","value":null}}""");

        var reinstallId = await ReadIdAsync(transport, 2);
        var reinstall = transport.Sent.ElementAt(1);
        Assert.Contains("__coflySentinel", reinstall, StringComparison.Ordinal);
        transport.PushResult(reinstallId, """{"result":{"type":"boolean","value":true}}""");

        await pending;
    }

    [Fact]
    public async Task ScriptFailureIsSwallowedAsUnknown()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var sentinel = Sentinel(connection);

        var pending = sentinel.PollOnceAsync(CancellationToken.None);
        var id = await ReadIdAsync(transport, 1);
        transport.PushResult(
            id,
            """{"exceptionDetails":{"text":"Uncaught","exception":{"description":"boom"}}}""");

        var snapshot = await pending;
        Assert.Equal(CodexLimitLevel.Unknown, snapshot.Level);
    }

    // ---- change notification ----------------------------------------------------

    [Fact]
    public async Task StateChangedFiresOnLevelTransitionOnly()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var sentinel = Sentinel(connection);

        var raised = new List<CodexLimitLevel>();
        sentinel.StateChanged += (_, snapshot) => raised.Add(snapshot.Level);

        await PollWithAsync(transport, sentinel, """{"modal":false,"percent":10}""");
        await PollWithAsync(transport, sentinel, """{"modal":false,"percent":11}""");
        await PollWithAsync(transport, sentinel, """{"modal":true}""");
        await PollWithAsync(transport, sentinel, """{"modal":true}""");

        // Normal (first ever), then Reached. The two repeats are suppressed.
        Assert.Equal([CodexLimitLevel.Normal, CodexLimitLevel.Reached], raised);
    }

    [Fact]
    public async Task StateChangedFiresWhenResetTextChanges()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var sentinel = Sentinel(connection);

        var raised = 0;
        sentinel.StateChanged += (_, __) => raised++;

        await PollWithAsync(transport, sentinel, """{"modal":true,"resetText":"resets in 2 hours"}""");
        await PollWithAsync(transport, sentinel, """{"modal":true,"resetText":"resets in 1 hour"}""");

        Assert.Equal(2, raised);
    }

    [Fact]
    public async Task SmallPercentDriftDoesNotNotify()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        using var sentinel = Sentinel(connection);

        var raised = 0;
        sentinel.StateChanged += (_, __) => raised++;

        await PollWithAsync(transport, sentinel, """{"percent":90}""");
        await PollWithAsync(transport, sentinel, """{"percent":92}""");

        Assert.Equal(1, raised);
    }

    [Fact]
    public async Task InstallAsyncRegistersDetectorForFutureDocuments()
    {
        var (connection, transport) = await ConnectAsync();
        using var _ = connection;
        transport.AutoAcknowledge = true;
        using var sentinel = Sentinel(connection);

        await sentinel.InstallAsync(CancellationToken.None);

        var methods = transport.Sent
            .Select(payload =>
            {
                using var document = JsonDocument.Parse(payload);
                return document.RootElement.GetProperty("method").GetString();
            })
            .ToArray();

        Assert.Equal(
            new[] { "Page.enable", "Page.addScriptToEvaluateOnNewDocument", "Runtime.evaluate" },
            methods);
    }

    // ---- helpers ----------------------------------------------------------------

    private static async Task PollWithAsync(
        FakeCdpTransport transport,
        CodexLimitSentinel sentinel,
        string snapshotJson)
    {
        var expected = transport.Sent.Count + 1;
        var pending = sentinel.PollOnceAsync(CancellationToken.None);
        var id = await ReadIdAsync(transport, expected);
        transport.PushResult(id, BuildStringResult(snapshotJson));
        await pending;
    }

    private static async Task ReplyWithSnapshotAsync(FakeCdpTransport transport, string snapshotJson)
    {
        var id = await ReadIdAsync(transport, 1);
        transport.PushResult(id, BuildStringResult(snapshotJson));
    }

    /// <summary>Wraps the detector payload the way Runtime.evaluate returns a string.</summary>
    private static string BuildStringResult(string snapshotJson)
        => $"{{\"result\":{{\"type\":\"string\",\"value\":{JsonSerializer.Serialize(snapshotJson)}}}}}";

    private static async Task<int> ReadIdAsync(FakeCdpTransport transport, int expectedCount)
    {
        for (var attempt = 0; attempt < 200; attempt++)
        {
            if (transport.Sent.Count >= expectedCount)
            {
                using var document = JsonDocument.Parse(transport.Sent.ElementAt(expectedCount - 1));
                return document.RootElement.GetProperty("id").GetInt32();
            }

            await Task.Delay(10);
        }

        throw new TimeoutException($"Expected {expectedCount} command(s), saw {transport.Sent.Count}.");
    }
}
