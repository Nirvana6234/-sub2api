/**
 * The narrow contract between the 共飞 stack and Paseo.
 *
 * This file is the *whole* agreement. Anything not named here does not exist as
 * far as consumers are concerned — that is the point: terminals, file access,
 * git and workspaces are unreachable because no operation can express them, not
 * because something downstream refuses them.
 *
 * The C# client mirrors these shapes by hand. It deliberately does NOT import
 * Paseo types, so the only file in the stack that has to survive a Paseo bump is
 * this package.
 */

/**
 * Contract version, `major.minor`.
 *
 * - add an operation or an optional field -> bump minor
 * - change semantics, remove or retype a field -> bump major
 *
 * A consumer whose *major* differs is rejected at `hello`. This is the cheapest
 * possible upgrade guard and the reason the C# side never has to know which
 * Paseo version is behind the bridge.
 *
 * Held at 1.0 deliberately while the contract is unreleased: `DaemonState` grew
 * an `unauthorized` value after the first spine run and nothing consumes this
 * contract outside this repository yet, so there is nobody to bump for. The
 * first external consumer freezes it — after that, added values are a minor.
 */
export const CONTRACT_VERSION = "1.0";

export function contractMajor(version: string): string {
  const [major] = version.split(".");
  return major ?? "";
}

/** Frame kinds on the wire. One JSON object per line, UTF-8, `\n` separated. */
export type Frame = RequestFrame | ResponseFrame | EventFrame;

export interface RequestFrame {
  t: "req";
  /** Correlates with the response. Opaque to the bridge. */
  id: string;
  op: string;
  args?: Record<string, unknown>;
}

export interface ResponseFrame {
  t: "res";
  id: string;
  ok: boolean;
  data?: unknown;
  error?: ContractError;
}

export interface EventFrame {
  t: "evt";
  topic: EventTopic;
  data: unknown;
}

export type EventTopic = "timeline" | "attention" | "daemon";

/**
 * Error codes the consumer must be able to render *differently*.
 *
 * Collapsing these into one "operation failed" is the most common way to make a
 * working system look broken: on a novice machine `CODEX_MISSING` is by far the
 * most frequent failure, and showing it as a network error sends the user down
 * the wrong path.
 *
 * `TRANSPORT_DOWN` never appears on the wire — the consumer synthesises it when
 * the pipe itself is gone, because in that case no response can arrive at all.
 */
export type ContractErrorCode =
  | "CONTRACT_MISMATCH"
  | "UNAUTHORIZED"
  | "DAEMON_DOWN"
  | "CODEX_MISSING"
  | "PERMISSION_REQUIRED"
  | "UNKNOWN_OP"
  | "BAD_REQUEST"
  | "INTERNAL";

export interface ContractError {
  code: ContractErrorCode;
  message: string;
  /** Free-form, for logs only. Never rendered verbatim to end users. */
  detail?: string;
}

export class BridgeError extends Error {
  public readonly code: ContractErrorCode;
  public readonly detail: string | undefined;

  public constructor(code: ContractErrorCode, message: string, detail?: string) {
    super(message);
    this.name = "BridgeError";
    this.code = code;
    this.detail = detail;
  }

  public toContractError(): ContractError {
    return this.detail === undefined
      ? { code: this.code, message: this.message }
      : { code: this.code, message: this.message, detail: this.detail };
  }
}

/* ------------------------------------------------------------------ *
 * Operation payloads
 * ------------------------------------------------------------------ */

export interface HelloArgs {
  /** One-shot secret handed to the bridge at spawn time by its host. */
  token: string;
  /** Consumer's contract version. */
  contract: string;
}

export interface HelloResult {
  contract: string;
  /** Diagnostic only. Consumers must never branch on this. */
  paseoClientVersion: string;
  bridgeVersion: string;
}

/**
 * `unauthorized` is separate from `down` because the remedy is different: a down
 * daemon is a restart, a rejected password is a credential repair. Consumers on
 * an older contract fall back to `down`, which is the safe direction — they show
 * "not usable" rather than pretending everything is fine.
 */
export type DaemonState = "running" | "down" | "unauthorized";
/** `unknown` means the daemon has not finished provider discovery yet. */
export type CodexState = "ready" | "missing" | "unknown";

export interface HealthResult {
  daemon: DaemonState;
  /** Present only when `daemon === "running"`. */
  listen?: string;
  /** Why the daemon is not usable. Diagnostic text; never a user-facing string. */
  daemonError?: string;
  codex: CodexState;
  /** Populated when codex is missing, so the consumer can show a real reason. */
  codexError?: string;
}

export interface AgentSummary {
  id: string;
  title: string | null;
  status: string;
  provider: string;
  cwd: string | null;
  updatedAt: string | null;
}

export interface AgentsListResult {
  agents: AgentSummary[];
}

/* ------------------------------------------------------------------ *
 * Work directories
 * ------------------------------------------------------------------ */

/**
 * A directory an agent may be started in, addressed by an opaque key.
 *
 * The blast radius of a remote session is exactly the set of directories
 * registered here, and **paths never cross the contract inbound**: a consumer
 * asks for `cwdKey`, and only the process that started the bridge — the one that
 * obtained the user's consent — can put a path behind a key. That holds for the
 * server-side consumer too: Paw's adapter cannot forge a path any more than the
 * desktop UI can, because neither of them is where the map lives.
 *
 * `path` is deliberately absent from what `workdirs.list` returns.
 */
export interface WorkdirEntry {
  key: string;
  label: string;
}

export interface WorkdirsListResult {
  workdirs: WorkdirEntry[];
}

/* ------------------------------------------------------------------ *
 * Agent lifecycle
 * ------------------------------------------------------------------ */

export interface AgentCreateArgs {
  /** Key from `workdirs.list`. Not a path — see {@link WorkdirEntry}. */
  cwdKey: string;
  /** Model id within the codex provider, e.g. `gpt-5.5`. Optional: daemon default. */
  model?: string;
  /** First prompt. Optional: an empty session is legitimate. */
  prompt?: string;
  title?: string;
}

export interface AgentCreateResult {
  agentId: string;
}

export interface AgentSendArgs {
  agentId: string;
  text: string;
}

export interface AgentIdArgs {
  agentId: string;
}

export interface ArchiveResult {
  archivedAt: string | null;
}

export interface OkResult {
  ok: true;
}

/* ------------------------------------------------------------------ *
 * Relay (remote access)
 * ------------------------------------------------------------------ */

export interface RelayStatusResult {
  enabled: boolean;
  /** Where the daemon dials out to. Ours, once self-hosted. */
  endpoint: string | null;
  useTls: boolean;
  /**
   * Identifies this daemon home, and is half of the pairing credential.
   *
   * It is also the revocation lever: replacing the Paseo home changes it, and
   * that is the *only* way to invalidate an offer that has already been handed
   * out — see {@link RelayPairResult}.
   */
  serverId: string | null;
}

export interface RelayPairResult {
  enabled: boolean;
  /**
   * The pairing URL, whose fragment carries the offer.
   *
   * **This is a credential, not a link.** The offer contains everything needed to
   * open an owner session against this machine: measured against a real daemon,
   * a relay client that supplied no password at all could list and drive agents
   * while the same daemon returned 401 to an unauthenticated loopback request.
   * Treat it like a password — never log it, never put it in a ticket, and hand
   * it only to the device being paired.
   */
  url: string;
}

export interface RelayDisableResult {
  enabled: boolean;
  /**
   * Always `false`: turning the relay off stops the daemon dialling out, but the
   * offer already handed out stays valid for whenever it is turned back on.
   * Real revocation means replacing the Paseo home so `serverId` changes. Said
   * explicitly here because "disable" reads like "revoke" and is not.
   */
  offerRevoked: boolean;
}

/* ------------------------------------------------------------------ *
 * Events
 * ------------------------------------------------------------------ */

/**
 * Narrow projection of Paseo's timeline stream.
 *
 * Paseo's own event union is rich and moves with the product; consumers get
 * these kinds instead, so a new upstream event type becomes `other` rather than
 * a breaking change. `raw` carries the upstream discriminator as an opaque
 * string for logs — it is not a schema and must not be branched on.
 */
export type TimelineEventKind =
  | "user"
  | "assistant"
  | "reasoning"
  | "tool"
  | "error"
  | "turn_started"
  | "turn_completed"
  | "turn_failed"
  | "other";

export interface TimelineEvent {
  agentId: string;
  kind: TimelineEventKind;
  text?: string;
  toolName?: string;
  seq?: number;
  at: string;
  raw: string;
}

/**
 * Timeline events are delivered in batches, not one frame each.
 *
 * A single codex turn emits a lot of small events. Batching within a short
 * window keeps the pipe from becoming the bottleneck while preserving content —
 * coalescing by *dropping* intermediates would silently lose assistant text.
 * When a consumer cannot keep up, the oldest events are dropped and `dropped`
 * says how many, which is the signal to refetch rather than to guess: the live
 * stream is for immediacy, an authoritative fetch is for correctness.
 */
export interface TimelineBatch {
  agentId: string;
  events: TimelineEvent[];
  dropped?: number;
}

/** Mirrors Paseo's own attention semantics; see the daemon's `agent_attention_required`. */
export interface AttentionEvent {
  agentId: string;
  reason: "finished" | "error" | "permission";
  at: string;
  /**
   * The daemon's decision about whether *this* client should raise the alert. It
   * elects one recipient among clients it considers present, from heartbeats the
   * bridge does not send — so today this is always `false` (measured). Advisory
   * until the bridge reports presence.
   */
  shouldNotify: boolean;
  title?: string;
  body?: string;
}

/** Emitted when the bridge's own view of the daemon changes. */
export interface DaemonEvent {
  daemon: DaemonState;
  detail?: string;
}
