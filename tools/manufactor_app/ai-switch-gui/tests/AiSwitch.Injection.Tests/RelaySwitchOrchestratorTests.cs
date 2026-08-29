using LanAi.Workspace.Injection.Sentinel;
using Xunit;

namespace AiSwitch.Injection.Tests;

internal sealed class FakeRelaySwitchGateway : IRelaySwitchGateway
{
    public RelayRoutingState Routing { get; set; } = new(false, "https://official", "cloud");

    public RelaySwitchOutcome RelayOutcome { get; set; } = new(true, "已切换到本机中转");

    public int SwitchToRelayCalls { get; private set; }

    public Exception? ReadRoutingThrows { get; set; }

    public Exception? SwitchThrows { get; set; }

    public Task<RelayRoutingState> ReadRoutingAsync(CancellationToken cancellationToken)
        => ReadRoutingThrows is not null
            ? Task.FromException<RelayRoutingState>(ReadRoutingThrows)
            : Task.FromResult(Routing);

    public Task<RelaySwitchOutcome> SwitchToRelayAsync(CancellationToken cancellationToken)
    {
        SwitchToRelayCalls++;
        return SwitchThrows is not null
            ? Task.FromException<RelaySwitchOutcome>(SwitchThrows)
            : Task.FromResult(RelayOutcome);
    }

    public Task<RelaySwitchOutcome> SwitchToOfficialAsync(CancellationToken cancellationToken)
        => Task.FromResult(new RelaySwitchOutcome(true, "已切回官方"));
}

public sealed class RelaySwitchOrchestratorTests
{
    private static CodexLimitSnapshot Snapshot(CodexLimitLevel level, double? percent = null)
        => new(
            level,
            new CodexLimitFacts { UsedPercent = percent, ModalVisible = level == CodexLimitLevel.Reached },
            DateTimeOffset.UtcNow);

    private static (RelaySwitchOrchestrator Orchestrator, FakeRelaySwitchGateway Gateway) Create()
    {
        var gateway = new FakeRelaySwitchGateway();
        return (new RelaySwitchOrchestrator(gateway), gateway);
    }

    // ---- prompt policy ----------------------------------------------------------

    [Fact]
    public void ReachedRaisesAnOffer()
    {
        var (orchestrator, _) = Create();
        using var _o = orchestrator;

        var raised = new List<RelaySwitchPrompt>();
        orchestrator.PromptRequested += (_, prompt) => raised.Add(prompt);

        var result = orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached));

        Assert.NotNull(result);
        Assert.Equal(RelaySwitchReason.LimitReached, result!.Reason);
        Assert.Single(raised);
        Assert.True(result.PreservesLocalHistory);
    }

    [Fact]
    public void NormalRaisesNothing()
    {
        var (orchestrator, _) = Create();
        using var _o = orchestrator;

        Assert.Null(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Normal)));
    }

    [Fact]
    public void RepeatedReachedOffersOnlyOncePerEpisode()
    {
        var (orchestrator, _) = Create();
        using var _o = orchestrator;

        Assert.NotNull(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached)));
        Assert.Null(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached)));
        Assert.Null(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached)));
    }

    /// <summary>
    /// Unknown shows up whenever the app is loading or a poll times out. If it were
    /// treated as the end of an episode, the offer would reappear on every blip.
    /// </summary>
    [Fact]
    public void UnknownBetweenReachedDoesNotReopenTheEpisode()
    {
        var (orchestrator, _) = Create();
        using var _o = orchestrator;

        Assert.NotNull(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached)));
        Assert.Null(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Unknown)));
        Assert.Null(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached)));
    }

    [Fact]
    public void ReturningToNormalEndsTheEpisodeSoTheNextLimitOffersAgain()
    {
        var (orchestrator, _) = Create();
        using var _o = orchestrator;

        Assert.NotNull(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached)));
        orchestrator.Evaluate(Snapshot(CodexLimitLevel.Normal));

        Assert.NotNull(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached)));
    }

    [Fact]
    public void DeclineSuppressesFurtherOffersInTheSameEpisode()
    {
        var (orchestrator, _) = Create();
        using var _o = orchestrator;

        orchestrator.Evaluate(Snapshot(CodexLimitLevel.Approaching, 90));
        orchestrator.Decline();

        // Including the escalation to Reached: the overlay still shows the state, so
        // the user keeps the information without being asked twice.
        Assert.Null(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached)));
        Assert.True(orchestrator.DeclinedThisEpisode);
    }

    [Fact]
    public void DeclineIsForgottenOnceTheEpisodeEnds()
    {
        var (orchestrator, _) = Create();
        using var _o = orchestrator;

        orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached));
        orchestrator.Decline();
        orchestrator.Evaluate(Snapshot(CodexLimitLevel.Normal));

        Assert.False(orchestrator.DeclinedThisEpisode);
        Assert.NotNull(orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached)));
    }

    [Fact]
    public void EscalationFromApproachingToReachedOffersAgainWhenNotDeclined()
    {
        var (orchestrator, _) = Create();
        using var _o = orchestrator;

        var first = orchestrator.Evaluate(Snapshot(CodexLimitLevel.Approaching, 90));
        var second = orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached));

        Assert.Equal(RelaySwitchReason.ApproachingLimit, first!.Reason);
        Assert.Equal(RelaySwitchReason.LimitReached, second!.Reason);
    }

    // ---- accepting --------------------------------------------------------------

    [Fact]
    public async Task AcceptCallsTheGatewayAndReportsSuccess()
    {
        var (orchestrator, gateway) = Create();
        using var _o = orchestrator;

        var completed = new List<RelaySwitchOutcome>();
        orchestrator.SwitchCompleted += (_, outcome) => completed.Add(outcome);

        var outcome = await orchestrator.AcceptAsync(CancellationToken.None);

        Assert.True(outcome.Success);
        Assert.Equal(1, gateway.SwitchToRelayCalls);
        Assert.Single(completed);
    }

    [Fact]
    public async Task GatewayFailureIsReportedNotThrown()
    {
        var (orchestrator, gateway) = Create();
        using var _o = orchestrator;
        gateway.SwitchThrows = new InvalidOperationException("配置被占用");

        var outcome = await orchestrator.AcceptAsync(CancellationToken.None);

        Assert.False(outcome.Success);
        Assert.Contains("配置被占用", outcome.Summary);
    }

    // ---- routing durability -----------------------------------------------------

    /// <summary>
    /// The case worth surfacing: routing was on the relay, is no longer, and the
    /// account is still limited — the official client rewrote the config.
    /// </summary>
    [Fact]
    public async Task RoutingLostWhileLimitedRaisesAnOffer()
    {
        var (orchestrator, gateway) = Create();
        using var _o = orchestrator;

        orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached));

        gateway.Routing = new RelayRoutingState(true, "http://127.0.0.1:8080/v1", "local");
        Assert.Null(await orchestrator.CheckRoutingAsync(CancellationToken.None));

        gateway.Routing = new RelayRoutingState(false, "https://official", "cloud");
        var prompt = await orchestrator.CheckRoutingAsync(CancellationToken.None);

        Assert.NotNull(prompt);
        Assert.Equal(RelaySwitchReason.RoutingLost, prompt!.Reason);
    }

    /// <summary>
    /// A user who deliberately went back to official while not limited must not be
    /// nagged about it.
    /// </summary>
    [Fact]
    public async Task RoutingLeftWhileNotLimitedIsSilent()
    {
        var (orchestrator, gateway) = Create();
        using var _o = orchestrator;

        orchestrator.Evaluate(Snapshot(CodexLimitLevel.Normal));

        gateway.Routing = new RelayRoutingState(true, "http://127.0.0.1:8080/v1", "local");
        await orchestrator.CheckRoutingAsync(CancellationToken.None);

        gateway.Routing = new RelayRoutingState(false, "https://official", "cloud");
        Assert.Null(await orchestrator.CheckRoutingAsync(CancellationToken.None));
    }

    [Fact]
    public async Task RoutingLostIsOfferedOnlyOncePerEpisode()
    {
        var (orchestrator, gateway) = Create();
        using var _o = orchestrator;

        orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached));
        gateway.Routing = new RelayRoutingState(true, "http://127.0.0.1:8080/v1", "local");
        await orchestrator.CheckRoutingAsync(CancellationToken.None);

        gateway.Routing = new RelayRoutingState(false, "https://official", "cloud");
        Assert.NotNull(await orchestrator.CheckRoutingAsync(CancellationToken.None));

        // Still off the relay on the next tick; the offer must not repeat.
        Assert.Null(await orchestrator.CheckRoutingAsync(CancellationToken.None));
    }

    [Fact]
    public async Task AcceptingEstablishesTheRelayBaselineForTheWatch()
    {
        var (orchestrator, gateway) = Create();
        using var _o = orchestrator;

        orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached));
        await orchestrator.AcceptAsync(CancellationToken.None);

        // No prior ReadRoutingAsync happened, yet a subsequent clobber is still caught
        // because the successful switch set the baseline.
        gateway.Routing = new RelayRoutingState(false, "https://official", "cloud");
        var prompt = await orchestrator.CheckRoutingAsync(CancellationToken.None);

        Assert.NotNull(prompt);
        Assert.Equal(RelaySwitchReason.RoutingLost, prompt!.Reason);
    }

    [Fact]
    public async Task ReadRoutingFailureIsSwallowed()
    {
        var (orchestrator, gateway) = Create();
        using var _o = orchestrator;
        gateway.ReadRoutingThrows = new IOException("配置文件被锁定");

        Assert.Null(await orchestrator.CheckRoutingAsync(CancellationToken.None));
    }

    [Fact]
    public async Task DeclinedEpisodeSuppressesRoutingLostToo()
    {
        var (orchestrator, gateway) = Create();
        using var _o = orchestrator;

        orchestrator.Evaluate(Snapshot(CodexLimitLevel.Reached));
        orchestrator.Decline();

        gateway.Routing = new RelayRoutingState(true, "http://127.0.0.1:8080/v1", "local");
        await orchestrator.CheckRoutingAsync(CancellationToken.None);
        gateway.Routing = new RelayRoutingState(false, "https://official", "cloud");

        Assert.Null(await orchestrator.CheckRoutingAsync(CancellationToken.None));
    }

    // ---- sentinel wiring --------------------------------------------------------

    [Fact]
    public async Task AttachedSentinelDrivesOffers()
    {
        var transport = new FakeCdpTransport();
        using var connection = new LanAi.Workspace.Injection.Cdp.CdpConnection(transport);
        await connection.ConnectAsync(new Uri("ws://127.0.0.1:9777/devtools/page/1"), CancellationToken.None);
        using var sentinel = new CodexLimitSentinel(connection);

        var (orchestrator, _) = Create();
        using var _o = orchestrator;
        orchestrator.Attach(sentinel);

        var raised = new List<RelaySwitchPrompt>();
        orchestrator.PromptRequested += (_, prompt) => raised.Add(prompt);

        var pending = sentinel.PollOnceAsync(CancellationToken.None);
        var id = await ReadFirstIdAsync(transport);
        transport.PushResult(
            id,
            "{\"result\":{\"type\":\"string\",\"value\":\"{\\\"modal\\\":true}\"}}");
        await pending;

        Assert.Single(raised);
        Assert.Equal(RelaySwitchReason.LimitReached, raised[0].Reason);
    }

    private static async Task<int> ReadFirstIdAsync(FakeCdpTransport transport)
    {
        for (var attempt = 0; attempt < 200; attempt++)
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
