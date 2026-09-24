namespace LanAi.Workspace.Injection.Sentinel;

/// <summary>Why the user is being offered the relay switch.</summary>
public enum RelaySwitchReason
{
    /// <summary>Usage is near the cap but work can still proceed.</summary>
    ApproachingLimit,

    /// <summary>The limit has been hit; work is blocked until reset.</summary>
    LimitReached,

    /// <summary>
    /// Routing was pointing at the relay and no longer is, while the account is still
    /// limited — the official client rewrote the configuration.
    /// </summary>
    RoutingLost,
}

public sealed record RelaySwitchPrompt(
    RelaySwitchReason Reason,
    CodexLimitSnapshot? Snapshot,
    RelayRoutingState? Routing)
{
    /// <summary>
    /// Whether the local conversation history survives the switch. It does: sessions
    /// and memory are plain files under the Codex home directory and switching only
    /// rewrites the provider fields.
    /// </summary>
    public bool PreservesLocalHistory => true;
}

public sealed record RelaySwitchOrchestratorOptions
{
    /// <summary>How often the routing durability watch re-reads the live config.</summary>
    public TimeSpan RoutingCheckInterval { get; init; } = TimeSpan.FromSeconds(30);
}

/// <summary>
/// Turns limit signals into a switch offer, performs the switch when the user accepts,
/// and keeps watching that the routing stays where it was put.
/// </summary>
/// <remarks>
/// <para><b>Prompt policy.</b> One offer per limit <i>episode</i>, not per poll. An
/// episode opens when the level first leaves Normal and closes when it returns to
/// Normal. <see cref="CodexLimitLevel.Unknown"/> does <b>not</b> close an episode:
/// it appears routinely while the app is loading or when a poll times out, and
/// treating it as a boundary would re-offer the switch on every such blip. A decline
/// suppresses further offers for the rest of the episode — including an escalation
/// from Approaching to Reached. The status overlay still reflects the state, so a
/// declined user keeps the information without being asked again.</para>
///
/// <para><b>Routing durability.</b> The official client rewrites its configuration
/// with only the keys it recognises, dropping a custom provider. The watch therefore
/// re-reads the live routing on a timer rather than listening for a login event —
/// login is the trigger that was observed, not necessarily the only one. A routing
/// change is only surfaced when the account is still limited; a user who deliberately
/// returned to official is not nagged.</para>
/// </remarks>
public sealed class RelaySwitchOrchestrator : IDisposable
{
    private readonly IRelaySwitchGateway _gateway;
    private readonly RelaySwitchOrchestratorOptions _options;
    private readonly CancellationTokenSource _shutdown = new();

    private bool _declinedThisEpisode;
    private readonly HashSet<RelaySwitchReason> _offeredThisEpisode = [];
    private bool? _lastKnownPointsAtRelay;
    private CodexLimitLevel _lastLevel = CodexLimitLevel.Unknown;
    private bool _disposed;

    public RelaySwitchOrchestrator(
        IRelaySwitchGateway gateway,
        RelaySwitchOrchestratorOptions? options = null)
    {
        _gateway = gateway ?? throw new ArgumentNullException(nameof(gateway));
        _options = options ?? new RelaySwitchOrchestratorOptions();
    }

    /// <summary>Raised when the user should be offered the switch. The UI decides how to show it.</summary>
    public event EventHandler<RelaySwitchPrompt>? PromptRequested;

    /// <summary>Raised after an accepted switch completes, successfully or not.</summary>
    public event EventHandler<RelaySwitchOutcome>? SwitchCompleted;

    /// <summary>True once the user declined; reset when the limit episode ends.</summary>
    public bool DeclinedThisEpisode => _declinedThisEpisode;

    /// <summary>Attaches to a sentinel so its state changes drive the offers.</summary>
    public void Attach(CodexLimitSentinel sentinel)
    {
        ArgumentNullException.ThrowIfNull(sentinel);
        sentinel.StateChanged += OnSentinelStateChanged;
    }

    public void Detach(CodexLimitSentinel sentinel)
    {
        ArgumentNullException.ThrowIfNull(sentinel);
        sentinel.StateChanged -= OnSentinelStateChanged;
    }

    /// <summary>
    /// Applies the prompt policy to a snapshot and returns the offer to show, or
    /// <c>null</c> when nothing should be shown.
    /// </summary>
    public RelaySwitchPrompt? Evaluate(CodexLimitSnapshot snapshot)
    {
        ArgumentNullException.ThrowIfNull(snapshot);

        // Unknown is a blip, not an episode boundary: it occurs while the app loads
        // and whenever a poll times out.
        if (snapshot.Level == CodexLimitLevel.Unknown)
        {
            return null;
        }

        _lastLevel = snapshot.Level;

        if (snapshot.Level == CodexLimitLevel.Normal)
        {
            CloseEpisode();
            return null;
        }

        if (_declinedThisEpisode)
        {
            return null;
        }

        var reason = snapshot.Level == CodexLimitLevel.Reached
            ? RelaySwitchReason.LimitReached
            // NOTE: the Approaching path depends on rateLimitPercent being readable
            // from the page, which has never been observed in a real limited state.
            // Until then this branch may simply never fire.
            : RelaySwitchReason.ApproachingLimit;

        if (!_offeredThisEpisode.Add(reason))
        {
            return null;
        }

        var prompt = new RelaySwitchPrompt(reason, snapshot, null);
        PromptRequested?.Invoke(this, prompt);
        return prompt;
    }

    /// <summary>
    /// Re-reads the live routing and returns an offer when it was reset out from under
    /// a still-limited account.
    /// </summary>
    public async Task<RelaySwitchPrompt?> CheckRoutingAsync(CancellationToken cancellationToken)
    {
        RelayRoutingState routing;
        try
        {
            routing = await _gateway.ReadRoutingAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (Exception exception) when (exception is not OperationCanceledException)
        {
            return null;
        }

        var previous = _lastKnownPointsAtRelay;
        _lastKnownPointsAtRelay = routing.PointsAtRelay;

        if (previous != true || routing.PointsAtRelay)
        {
            return null;
        }

        // Routing left the relay. Only worth raising while the account is still
        // limited; otherwise the user simply switched back on purpose.
        if (_lastLevel != CodexLimitLevel.Reached || _declinedThisEpisode)
        {
            return null;
        }

        if (!_offeredThisEpisode.Add(RelaySwitchReason.RoutingLost))
        {
            return null;
        }

        var prompt = new RelaySwitchPrompt(RelaySwitchReason.RoutingLost, null, routing);
        PromptRequested?.Invoke(this, prompt);
        return prompt;
    }

    /// <summary>Performs the switch to the relay after the user accepted.</summary>
    public async Task<RelaySwitchOutcome> AcceptAsync(CancellationToken cancellationToken)
    {
        RelaySwitchOutcome outcome;
        try
        {
            outcome = await _gateway.SwitchToRelayAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (Exception exception) when (exception is not OperationCanceledException)
        {
            outcome = new RelaySwitchOutcome(false, $"切换失败：{exception.Message}");
        }

        if (outcome.Success)
        {
            // Establishes the baseline the durability watch compares against.
            _lastKnownPointsAtRelay = true;
        }

        SwitchCompleted?.Invoke(this, outcome);
        return outcome;
    }

    /// <summary>Records the user's refusal, silencing offers until the episode ends.</summary>
    public void Decline() => _declinedThisEpisode = true;

    /// <summary>Starts the routing durability watch.</summary>
    public Task StartRoutingWatchAsync()
    {
        return Task.Run(() => RoutingWatchLoopAsync(_shutdown.Token), CancellationToken.None);
    }

    private void OnSentinelStateChanged(object? sender, CodexLimitSnapshot snapshot)
    {
        try
        {
            Evaluate(snapshot);
        }
        catch
        {
            // An offer that cannot be raised must not disturb the sentinel loop.
        }
    }

    private void CloseEpisode()
    {
        _declinedThisEpisode = false;
        _offeredThisEpisode.Clear();
    }

    private async Task RoutingWatchLoopAsync(CancellationToken cancellationToken)
    {
        while (!cancellationToken.IsCancellationRequested)
        {
            try
            {
                await Task.Delay(_options.RoutingCheckInterval, cancellationToken).ConfigureAwait(false);
                await CheckRoutingAsync(cancellationToken).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                return;
            }
            catch
            {
                // Keep watching; the next tick retries.
            }
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _shutdown.Cancel();
        _shutdown.Dispose();
    }
}
