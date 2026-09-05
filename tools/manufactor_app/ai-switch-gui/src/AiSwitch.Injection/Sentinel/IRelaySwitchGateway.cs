namespace LanAi.Workspace.Injection.Sentinel;

/// <summary>Where the official client's Codex configuration currently points.</summary>
public sealed record RelayRoutingState(
    bool PointsAtRelay,
    string? CodexBaseUrl,
    string? SourceId);

public sealed record RelaySwitchOutcome(bool Success, string Summary);

/// <summary>
/// The switching capability the orchestrator needs, expressed only in types this
/// assembly owns.
/// </summary>
/// <remarks>
/// The real implementation lives in the WPF layer and delegates to the legacy switch
/// coordinator. That coordinator and its <c>LiveStatus</c> / <c>OperationResult</c>
/// models are <c>internal</c> to their assembly, so they cannot appear here; the
/// adapter maps them across this boundary.
///
/// <b>Contract requirement — do not implement a gateway that skips this:</b>
/// <see cref="SwitchToRelayAsync"/> and <see cref="SwitchToOfficialAsync"/> must take a
/// snapshot of the <b>complete</b> Codex configuration file before writing. The
/// official client rewrites that file wholesale using only the keys it recognises, so
/// unrelated user settings are lost if only the routing fields are preserved.
/// </remarks>
public interface IRelaySwitchGateway
{
    Task<RelayRoutingState> ReadRoutingAsync(CancellationToken cancellationToken);

    Task<RelaySwitchOutcome> SwitchToRelayAsync(CancellationToken cancellationToken);

    Task<RelaySwitchOutcome> SwitchToOfficialAsync(CancellationToken cancellationToken);
}
