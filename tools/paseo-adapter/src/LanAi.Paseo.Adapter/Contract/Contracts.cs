using System.Text.Json.Serialization;

namespace LanAi.Paseo.Adapter;

/// <summary>
/// The narrow contract, mirrored by hand from <c>bridge/src/contract.ts</c>.
/// </summary>
/// <remarks>
/// <para>
/// Hand-mirroring is the point of the whole design: nothing here is generated
/// from, or convertible to, a Paseo type. A Paseo upgrade must be able to break
/// the bridge's type-check without touching a single line of C#.
/// </para>
/// <para>
/// Every contract type is a <b>positional</b> record. That is not a style
/// choice: source-generated serialization cannot assign <c>init</c>-only
/// properties and routes such types through the parameterized-constructor
/// converter, where an omitted field arrives as <c>null</c> and the property
/// initializer never runs (the same trap documented at length in
/// <c>LanAi.RelayClient.Server/RelayJsonContext.cs</c>). Positional records make
/// that behaviour explicit: absence is <c>null</c>, and normalisation happens in
/// one place, in code, where it can be tested.
/// </para>
/// </remarks>
public static class PaseoContract
{
    /// <summary>
    /// Contract version this client speaks. Major must match the bridge or
    /// <c>hello</c> is rejected.
    /// </summary>
    public const string Version = "1.0";

    internal static string Major(string version)
    {
        var separator = version.IndexOf('.');
        return separator < 0 ? version : version[..separator];
    }
}

/// <summary>Codes the consumer must be able to render differently. See <see cref="PaseoAdapterException"/>.</summary>
public enum PaseoErrorCode
{
    /// <summary>The pipe itself is gone. Synthesised locally: no response can arrive.</summary>
    TransportDown,

    /// <summary>Bridge and consumer disagree on the contract major version.</summary>
    ContractMismatch,

    /// <summary>Handshake refused (bad or missing token, or an operation before <c>hello</c>).</summary>
    Unauthorized,

    /// <summary>The bridge is alive but the Paseo daemon is not answering.</summary>
    DaemonDown,

    /// <summary>Codex is not installed or not usable. On a novice machine this is the most frequent failure.</summary>
    CodexMissing,

    /// <summary>An agent turn is waiting for a human decision. Not an error dialog — a prompt.</summary>
    PermissionRequired,

    /// <summary>The bridge does not know this operation. Almost always a version-skew bug.</summary>
    UnknownOperation,

    /// <summary>Malformed request. Ours to fix.</summary>
    BadRequest,

    /// <summary>Anything else. Log it; show a generic message.</summary>
    Internal,
}

/// <summary>Daemon reachability as reported by <c>health</c>.</summary>
/// <remarks>
/// <see cref="Unauthorized"/> is separate from <see cref="Down"/> because the
/// remedy differs: a down daemon is a restart, a rejected password is a
/// credential repair. Any value this client does not recognise falls back to
/// <see cref="Down"/> — the safe direction, since it reads as "not usable"
/// rather than "fine".
/// </remarks>
public enum DaemonState
{
    Down,
    Running,
    Unauthorized,
}

/// <summary>Codex availability. <see cref="Unknown"/> means provider discovery has not settled yet.</summary>
public enum CodexState
{
    Unknown,
    Ready,
    Missing,
}

/// <summary>Result of the handshake. <see cref="PaseoClientVersion"/> is diagnostic only — never branch on it.</summary>
public sealed record HelloResult(
    [property: JsonPropertyName("contract")] string? Contract,
    [property: JsonPropertyName("paseoClientVersion")] string? PaseoClientVersion,
    [property: JsonPropertyName("bridgeVersion")] string? BridgeVersion);

/// <summary>Raw <c>health</c> payload. Consumers get the normalised <see cref="HealthSnapshot"/> instead.</summary>
public sealed record HealthPayload(
    [property: JsonPropertyName("daemon")] string? Daemon,
    [property: JsonPropertyName("listen")] string? Listen,
    [property: JsonPropertyName("daemonError")] string? DaemonError,
    [property: JsonPropertyName("codex")] string? Codex,
    [property: JsonPropertyName("codexError")] string? CodexError);

/// <summary>Normalised health, safe to bind to UI.</summary>
public sealed record HealthSnapshot(
    DaemonState Daemon,
    string? Listen,
    string? DaemonError,
    CodexState Codex,
    string? CodexError);

/// <summary>One agent row. Only the fields every consumer needs; deliberately not Paseo's full snapshot.</summary>
public sealed record AgentSummary(
    [property: JsonPropertyName("id")] string? Id,
    [property: JsonPropertyName("title")] string? Title,
    [property: JsonPropertyName("status")] string? Status,
    [property: JsonPropertyName("provider")] string? Provider,
    [property: JsonPropertyName("cwd")] string? Cwd,
    [property: JsonPropertyName("updatedAt")] string? UpdatedAt);

/// <summary>Payload of <c>agents.list</c>.</summary>
public sealed record AgentsListPayload(
    [property: JsonPropertyName("agents")] IReadOnlyList<AgentSummary>? Agents);

/// <summary>
/// A directory an agent may be started in, addressed by an opaque key.
/// </summary>
/// <remarks>
/// The blast radius of a remote session is exactly the set of registered
/// directories, and <b>paths never travel inbound</b>: callers pass a
/// <see cref="Key"/>, and only the process that started the bridge — the one
/// that obtained the user's consent — can put a path behind a key. That holds
/// for the server-side consumer too, so a compromised or buggy consumer cannot
/// widen its own reach; the worst it can do is name a key that already exists.
/// </remarks>
public sealed record WorkdirEntry(
    [property: JsonPropertyName("key")] string? Key,
    [property: JsonPropertyName("label")] string? Label);

/// <summary>Payload of <c>workdirs.list</c>.</summary>
public sealed record WorkdirsListPayload(
    [property: JsonPropertyName("workdirs")] IReadOnlyList<WorkdirEntry>? Workdirs);

/// <summary>Payload of <c>agents.create</c>.</summary>
public sealed record AgentCreatePayload(
    [property: JsonPropertyName("agentId")] string? AgentId);

/// <summary>Payload of <c>timeline.subscribe</c> and <c>timeline.unsubscribe</c>.</summary>
/// <remarks>
/// The effective subscription set, not a bare acknowledgement. The daemon keeps
/// only a handful of timeline subscriptions per connection and the bridge caps
/// them too, dropping the oldest; a caller that got only "ok" would keep a view
/// open on an agent that had silently stopped streaming. Compare this list with
/// what you believe you are watching.
/// </remarks>
public sealed record SubscriptionPayload(
    [property: JsonPropertyName("subscribed")] IReadOnlyList<string>? Subscribed);

/// <summary>Payload of <c>agents.archive</c>.</summary>
public sealed record ArchivePayload(
    [property: JsonPropertyName("archivedAt")] string? ArchivedAt);

/// <summary>How a timeline event should be rendered, independent of Paseo's own event union.</summary>
/// <remarks>
/// Upstream event types this client has never heard of arrive as
/// <see cref="Other"/> rather than breaking deserialization — a new Paseo event
/// kind must not be able to stop a chat from rendering.
/// </remarks>
public enum TimelineEventKind
{
    Other,
    User,
    Assistant,
    Reasoning,
    Tool,
    Error,
    TurnStarted,
    TurnCompleted,
    TurnFailed,
}

/// <summary>One projected timeline event.</summary>
public sealed record TimelineEventPayload(
    [property: JsonPropertyName("agentId")] string? AgentId,
    [property: JsonPropertyName("kind")] string? Kind,
    [property: JsonPropertyName("text")] string? Text,
    [property: JsonPropertyName("toolName")] string? ToolName,
    [property: JsonPropertyName("seq")] long? Seq,
    [property: JsonPropertyName("at")] string? At,
    [property: JsonPropertyName("raw")] string? Raw);

/// <summary>A batch of timeline events for one agent.</summary>
public sealed record TimelineBatchPayload(
    [property: JsonPropertyName("agentId")] string? AgentId,
    [property: JsonPropertyName("events")] IReadOnlyList<TimelineEventPayload>? Events,
    [property: JsonPropertyName("dropped")] int? Dropped);

/// <summary>Normalised timeline event handed to consumers.</summary>
public sealed record TimelineEvent(
    string AgentId,
    TimelineEventKind Kind,
    string? Text,
    string? ToolName,
    long? Seq,
    string? At,
    string Raw);

/// <summary>
/// A batch of timeline events.
/// </summary>
/// <param name="Dropped">
/// How many events were discarded because the consumer fell behind. Non-zero
/// means the live view is incomplete and only an authoritative refetch can
/// repair it — the live stream is for immediacy, not for correctness.
/// </param>
public sealed record TimelineBatch(
    string AgentId,
    IReadOnlyList<TimelineEvent> Events,
    int Dropped);

/// <summary>Why an agent wants a human.</summary>
public enum AttentionReason
{
    Finished,
    Error,
    Permission,
}

/// <summary>Raw <c>attention</c> event body.</summary>
public sealed record AttentionEventPayload(
    [property: JsonPropertyName("agentId")] string? AgentId,
    [property: JsonPropertyName("reason")] string? Reason,
    [property: JsonPropertyName("at")] string? At,
    [property: JsonPropertyName("shouldNotify")] bool? ShouldNotify,
    [property: JsonPropertyName("title")] string? Title,
    [property: JsonPropertyName("body")] string? Body);

/// <summary>
/// Normalised attention event.
/// </summary>
/// <param name="ShouldNotify">
/// The daemon's own decision about whether <i>this</i> client should raise the
/// alert. It picks one recipient among the clients it considers present, using
/// heartbeats the adapter does not currently send — so in practice this arrives
/// <b>false</b> today (measured on a real turn). Treat it as advisory: decide
/// locally whether to notify, and revisit once the adapter reports presence, at
/// which point the daemon can stop PC and phone from both alerting for the same
/// turn.
/// </param>
public sealed record AttentionEvent(
    string AgentId,
    AttentionReason Reason,
    string? At,
    bool ShouldNotify,
    string? Title,
    string? Body);

/// <summary>Remote-access state.</summary>
public sealed record RelayStatusPayload(
    [property: JsonPropertyName("enabled")] bool? Enabled,
    [property: JsonPropertyName("endpoint")] string? Endpoint,
    [property: JsonPropertyName("useTls")] bool? UseTls,
    [property: JsonPropertyName("serverId")] string? ServerId);

/// <summary>Normalised relay state.</summary>
/// <param name="ServerId">
/// Identifies this daemon home and is half of the pairing credential. It is also
/// the revocation lever: replacing the Paseo home changes it, and that is the
/// only way to invalidate an offer already handed out.
/// </param>
public sealed record RelayStatus(bool Enabled, string? Endpoint, bool UseTls, string? ServerId);

/// <summary>Result of enabling the relay.</summary>
public sealed record RelayPairPayload(
    [property: JsonPropertyName("enabled")] bool? Enabled,
    [property: JsonPropertyName("url")] string? Url);

/// <summary>
/// A pairing offer.
/// </summary>
/// <param name="Url">
/// <b>A credential, not a link.</b> Its fragment carries everything needed to open
/// an owner session against this machine: measured against a real daemon, a relay
/// client supplying no password at all could list and drive agents while the same
/// daemon returned 401 to an unauthenticated loopback request. Treat it like a
/// password — never log it, never put it in a ticket or a screenshot, and hand it
/// only to the device being paired.
/// </param>
public sealed record RelayPairing(bool Enabled, string Url);

/// <summary>Result of turning the relay off.</summary>
public sealed record RelayDisablePayload(
    [property: JsonPropertyName("enabled")] bool? Enabled,
    [property: JsonPropertyName("offerRevoked")] bool? OfferRevoked);

/// <summary>Error body carried by a failed response frame.</summary>
public sealed record ContractErrorPayload(
    [property: JsonPropertyName("code")] string? Code,
    [property: JsonPropertyName("message")] string? Message,
    [property: JsonPropertyName("detail")] string? Detail);
