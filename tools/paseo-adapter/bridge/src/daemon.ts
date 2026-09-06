/**
 * The single seam against Paseo.
 *
 * Everything Paseo-shaped is confined to this file, so a Paseo upgrade is a
 * type-check of one module rather than an audit of the whole stack.
 *
 * **Why the low-level driver and not the `PaseoClient` facade.** The facade's
 * `timeline.subscribe` is only a local filter over `agent_stream` messages. On a
 * daemon with `selectiveAgentTimeline` — every current one — the daemon sends
 * those messages only to sessions that asked for them via
 * `agent.timeline.set_subscription.request`, and the facade exposes no way to
 * ask. Using it would produce a subscription that connects, reports success, and
 * then silently delivers nothing. The driver has `setAgentTimelineSubscription`
 * (which no-ops itself against legacy daemons) and `onAgentAttentionRequired`,
 * so the bridge uses that instead. `@getpaseo/client/internal/daemon-client` is
 * a declared export of the package, and absorbing exactly this kind of internal
 * dependency is what the bridge exists for.
 *
 * Connection ownership: the bridge does NOT spawn the daemon. Host spawns both
 * as siblings inside one job object, because their failure domains differ — a
 * bridge crash must not restart a daemon that has live agent turns, and a daemon
 * restart must not require a new bridge. Hence the lazy connect + reconnect here.
 */
import { DaemonClient } from "@getpaseo/client/internal/daemon-client";
import {
  BridgeError,
  type AgentSummary,
  type AgentsListResult,
  type AttentionEvent,
  type CodexState,
  type HealthResult,
  type RelayDisableResult,
  type RelayPairResult,
  type RelayStatusResult,
  type TimelineEvent,
  type TimelineEventKind,
} from "./contract.js";

export interface DaemonConfig {
  /** Loopback WebSocket URL, e.g. `ws://127.0.0.1:6799/ws`. */
  url: string;
  /** Daemon password, when one is configured. */
  password?: string | undefined;
  connectTimeoutMs: number;
}

export interface DaemonSink {
  timeline(event: TimelineEvent): void;
  attention(event: AttentionEvent): void;
}

/** Provider id we care about. The adapter is codex-only by design. */
const CODEX_PROVIDER = "codex";

/**
 * Paseo's selective delivery keeps at most a handful of subscribed agents per
 * connection. We cap on our side too so a consumer that forgets to unsubscribe
 * degrades predictably (oldest dropped) instead of having the daemon silently
 * decide for us.
 */
const MAX_SUBSCRIBED_AGENTS = 5;

export class DaemonSession {
  private readonly config: DaemonConfig;
  private readonly sink: DaemonSink;
  private client: DaemonClient | null = null;
  private connecting: Promise<DaemonClient> | null = null;
  /** Insertion-ordered, so eviction is oldest-first. */
  private readonly subscribed = new Set<string>();
  /** Whether the consumer asked for attention events; re-applied on reconnect. */
  private attentionWanted = false;

  public constructor(config: DaemonConfig, sink: DaemonSink) {
    this.config = config;
    this.sink = sink;
  }

  private async ensureClient(): Promise<DaemonClient> {
    if (this.client) return this.client;
    if (this.connecting) return this.connecting;

    const attempt = (async (): Promise<DaemonClient> => {
      const client = new DaemonClient({
        url: this.config.url,
        connectTimeoutMs: this.config.connectTimeoutMs,
        clientType: "cli",
        // The driver requires an explicit id (the facade generates one). It
        // appears in daemon logs, so name it after this component rather than a
        // random string — a session in the log should say which process opened it.
        clientId: "cofly-paseo-bridge",
        // Reconnect is OFF deliberately. With the driver's own reconnect enabled,
        // `connect()` against a dead daemon keeps retrying instead of rejecting,
        // so `health` never answers and the consumer sees a request timeout
        // (TRANSPORT_DOWN) instead of the true state (DAEMON_DOWN). Measured:
        // the spine smoke against ws://127.0.0.1:1 failed exactly that way.
        // Reconnection is this class's job — one attempt per call, so every call
        // either succeeds or classifies.
        reconnect: { enabled: false },
        ...(this.config.password ? { password: this.config.password } : {}),
      });
      try {
        await client.connect();
      } catch (err) {
        try {
          await client.close();
        } catch {
          // closing a never-opened client is best effort
        }
        throw classifyConnectFailure(err);
      }
      this.wireEvents(client);
      this.client = client;
      // A reconnect starts with no server-side subscription, so restore what the
      // consumer already asked for. Without this, a daemon restart silently ends
      // every live timeline.
      if (this.subscribed.size > 0) {
        await client.setAgentTimelineSubscription([...this.subscribed]).catch(() => undefined);
      }
      if (this.attentionWanted) {
        await this.openAttentionSubscription(client).catch(() => undefined);
      }
      return client;
    })();

    this.connecting = attempt;
    try {
      return await attempt;
    } finally {
      this.connecting = null;
    }
  }

  private wireEvents(client: DaemonClient): void {
    client.on("agent_stream", (message) => {
      const projected = projectTimelineEvent(message.payload);
      // Attention arrives on its own channel; forwarding it here as well would
      // double-notify.
      if (projected) {
        this.sink.timeline(projected);
      }
    });

    // Attention is never filtered by subscription: a finished or blocked agent
    // has to reach the user whether or not its timeline is being watched. That
    // asymmetry with timeline events is intentional.
    client.onAgentAttentionRequired((notification) => {
      const event: AttentionEvent = {
        agentId: notification.agentId,
        reason: notification.reason,
        at: notification.timestamp,
        shouldNotify: notification.shouldNotify,
      };
      const payload = notification.notification;
      if (payload?.title) event.title = payload.title;
      if (payload?.body) event.body = payload.body;
      this.sink.attention(event);
    });
  }

  /* ---------------------------------------------------------------- *
   * Relay
   *
   * Remote access is the one capability here that changes who can reach the
   * machine, so it is deliberately the smallest possible surface: read the
   * state, turn it on and get one offer, turn it off. There is no "list paired
   * devices" because the daemon does not track pairings — possession of the
   * offer is the whole of the authorisation, which is exactly why the offer is
   * treated as a credential.
   * ---------------------------------------------------------------- */

  public async relayStatus(): Promise<RelayStatusResult> {
    const client = await this.ensureClient();
    return this.guard(async () => {
      const status = await client.getDaemonStatus();
      return {
        enabled: status.relay?.enabled ?? false,
        endpoint: status.relay?.publicEndpoint ?? status.relay?.endpoint ?? null,
        useTls: status.relay?.publicUseTls ?? status.relay?.useTls ?? false,
        serverId: status.serverId ?? null,
      };
    });
  }

  /**
   * Enables the relay and returns a pairing offer.
   *
   * Enabling is a live config patch rather than a restart: the daemon persists
   * the desired state and starts the outbound transport immediately. The offer is
   * fetched *after* the patch because it reports `relayEnabled`, and an offer
   * generated while the relay was off would advertise an endpoint nothing is
   * listening on.
   */
  public async relayPair(): Promise<RelayPairResult> {
    const client = await this.ensureClient();
    return this.guard(async () => {
      await client.patchDaemonConfig({ relay: { enabled: true } });
      const offer = await client.getDaemonPairingOffer({});
      if (!offer.url) {
        throw new BridgeError("INTERNAL", "The daemon returned no pairing URL");
      }
      return { enabled: offer.relayEnabled, url: offer.url };
    });
  }

  public async relayDisable(): Promise<RelayDisableResult> {
    const client = await this.ensureClient();
    return this.guard(async () => {
      await client.patchDaemonConfig({ relay: { enabled: false } });
      // Never true. Disabling stops the outbound connection; it does not make an
      // already-issued offer unusable.
      return { enabled: false, offerRevoked: false };
    });
  }

  /** Drops the cached client so the next call reconnects. */
  private forget(): void {
    const stale = this.client;
    this.client = null;
    if (stale) {
      void stale.close().catch(() => undefined);
    }
  }

  /**
   * Opens the agent-updates subscription that attention events ride on.
   *
   * Measured, not assumed: without this the daemon never sends
   * `agent_attention_required` at all. `broadcastAgentAttention` skips any
   * session for which `subscribesToAgent` is false
   * (`websocket-server.ts:2516`), and that predicate is false whenever the
   * session has no agent-updates subscription
   * (`agent-updates-service.ts:247-249`). A live turn confirmed the shape of the
   * failure: the timeline arrived in full and not one attention event did.
   *
   * The subscription is a side effect of `fetchAgents` with a `subscribe` block —
   * there is no dedicated RPC — so the cheapest possible listing is used purely to
   * establish it.
   */
  private async openAttentionSubscription(client: DaemonClient): Promise<void> {
    await client.fetchAgents({
      subscribe: { subscriptionId: "cofly-attention" },
      page: { limit: 1 },
    });
  }

  /** Declares interest in attention events, and keeps that true across reconnects. */
  public async subscribeAttention(): Promise<void> {
    const client = await this.ensureClient();
    this.attentionWanted = true;
    await this.guard(() => this.openAttentionSubscription(client));
  }

  public async close(): Promise<void> {
    const client = this.client;
    this.client = null;
    this.subscribed.clear();
    this.attentionWanted = false;
    if (client) {
      await client.close().catch(() => undefined);
    }
  }

  public async health(): Promise<HealthResult> {
    let client: DaemonClient;
    try {
      client = await this.ensureClient();
    } catch (err) {
      // Health is the one operation that answers instead of throwing when the
      // daemon is unusable — the consumer polls it precisely to learn that. It
      // still has to distinguish "not there" from "rejected us", because those
      // are two different repair paths.
      const unauthorized = err instanceof BridgeError && err.code === "UNAUTHORIZED";
      const daemonError = err instanceof BridgeError ? (err.detail ?? err.message) : describe(err);
      return {
        daemon: unauthorized ? "unauthorized" : "down",
        codex: "unknown",
        ...(daemonError ? { daemonError } : {}),
      };
    }

    let codex: CodexState = "unknown";
    let codexError: string | undefined;
    try {
      const availability = await client.listAvailableProviders();
      const entry = availability.providers.find((p) => p.provider === CODEX_PROVIDER);
      if (entry) {
        codex = entry.available ? "ready" : "missing";
        if (!entry.available && entry.error) codexError = entry.error;
      }
    } catch (err) {
      // Provider discovery can fail on its own while the daemon is fine; that is
      // an unknown codex state, not a dead daemon.
      codex = "unknown";
      codexError = describe(err);
    }

    const result: HealthResult = {
      daemon: "running",
      listen: hostFromWsUrl(this.config.url),
      codex,
    };
    if (codexError !== undefined) result.codexError = codexError;
    return result;
  }

  public async listAgents(): Promise<AgentsListResult> {
    const client = await this.ensureClient();
    return this.guard(async () => {
      // The driver returns directory *entries* (agent + project placement +
      // search metadata). Only the agent half crosses our contract: project
      // placement is a Paseo-side concept our consumers have no use for.
      const result = await client.fetchAgents();
      return { agents: result.entries.map((entry) => toSummary(entry.agent)) };
    });
  }

  /**
   * Starts a codex session in a registered directory.
   *
   * `cwd` arrives already resolved from a key by the caller; this class never
   * sees a consumer-supplied path (see `workdirs.ts`).
   */
  public async createAgent(input: {
    cwd: string;
    model?: string | undefined;
    prompt?: string | undefined;
    title?: string | undefined;
  }): Promise<{ agentId: string }> {
    const client = await this.ensureClient();
    return this.guard(async () => {
      const agent = await client.createAgent({
        config: {
          provider: CODEX_PROVIDER,
          cwd: input.cwd,
          ...(input.model ? { model: input.model } : {}),
          ...(input.title ? { title: input.title } : {}),
        },
        ...(input.prompt ? { initialPrompt: input.prompt } : {}),
      });
      return { agentId: agent.id };
    });
  }

  public async send(agentId: string, text: string): Promise<void> {
    const client = await this.ensureClient();
    await this.guard(() => client.sendMessage(agentId, text));
  }

  public async stop(agentId: string): Promise<void> {
    const client = await this.ensureClient();
    await this.guard(() => client.cancelAgent(agentId));
  }

  public async archive(agentId: string): Promise<{ archivedAt: string | null }> {
    const client = await this.ensureClient();
    return this.guard(async () => {
      const result = await client.archiveAgent(agentId);
      this.subscribed.delete(agentId);
      return { archivedAt: result.archivedAt ?? null };
    });
  }

  /**
   * Subscribes to one agent and returns the resulting subscription set.
   *
   * Returning the set is how eviction stays visible: past the cap the oldest
   * subscription is dropped, and a caller that only got `ok` would keep a view
   * open on an agent that had silently stopped streaming — the exact
   * silent-nothing failure this adapter classifies everywhere else. Compare the
   * returned list with what you believe you are watching.
   */
  public async subscribeTimeline(agentId: string): Promise<{ subscribed: string[] }> {
    const client = await this.ensureClient();
    if (!this.subscribed.has(agentId)) {
      this.subscribed.add(agentId);
      while (this.subscribed.size > MAX_SUBSCRIBED_AGENTS) {
        const oldest = this.subscribed.values().next().value;
        if (oldest === undefined) break;
        this.subscribed.delete(oldest);
      }
    }
    const subscribed = [...this.subscribed];
    await this.guard(() => client.setAgentTimelineSubscription(subscribed));
    return { subscribed };
  }

  public async unsubscribeTimeline(agentId: string): Promise<{ subscribed: string[] }> {
    if (!this.subscribed.delete(agentId)) {
      return { subscribed: [...this.subscribed] };
    }
    const client = await this.ensureClient();
    const subscribed = [...this.subscribed];
    await this.guard(() => client.setAgentTimelineSubscription(subscribed));
    return { subscribed };
  }

  /**
   * Runs a driver call and turns its failure into a classified error, dropping
   * the cached connection so the next call reconnects.
   *
   * Every post-connect failure is `DAEMON_DOWN` rather than `INTERNAL` on
   * purpose: the driver rejects with plain `Error`s carrying provider text, and
   * guessing a finer code from that text is how misclassification creeps in. The
   * detail string keeps the real reason visible in logs.
   */
  private async guard<T>(action: () => Promise<T>): Promise<T> {
    try {
      return await action();
    } catch (err) {
      if (err instanceof BridgeError) throw err;
      const detail = describe(err);

      // Provider unavailability is NOT a dead daemon, and conflating them is the
      // single most misleading thing this adapter could do on a novice machine:
      // the daemon is fine, codex simply is not installed, and the remedy is an
      // install flow rather than a restart. Measured: the spine smoke reported
      // `DAEMON_DOWN` with detail "Provider 'codex' is not available" until this
      // branch existed.
      if (isProviderUnavailable(detail)) {
        throw new BridgeError("CODEX_MISSING", "Codex is not available on this machine", detail);
      }

      // Only drop the connection when it is actually gone. A daemon-level
      // rejection (unknown agent id, invalid argument) proves the opposite: the
      // socket answered. Forgetting unconditionally forced a full reconnect for
      // every bad request, which is both wasteful and a good way to turn one
      // caller mistake into a visible outage.
      if (this.client && this.client.getConnectionState().status !== "connected") {
        this.forget();
      }
      throw new BridgeError("DAEMON_DOWN", "The daemon rejected the request", detail);
    }
  }
}

/**
 * Projects one Paseo stream event onto the narrow contract.
 *
 * Returns `null` for events the contract deliberately does not carry, so a new
 * upstream event type is a no-op here rather than a leak.
 */
function projectTimelineEvent(payload: {
  agentId: string;
  timestamp: string;
  seq?: number | undefined;
  event: { type: string } & Record<string, unknown>;
}): TimelineEvent | null {
  const { event } = payload;
  const base = {
    agentId: payload.agentId,
    at: payload.timestamp,
    raw: event.type,
    ...(payload.seq !== undefined ? { seq: payload.seq } : {}),
  };

  switch (event.type) {
    case "attention_required":
      // Delivered on the attention channel instead.
      return null;
    case "turn_started":
    case "turn_completed":
    case "turn_failed":
      return {
        ...base,
        kind: event.type as TimelineEventKind,
        ...(typeof event["error"] === "string" ? { text: event["error"] } : {}),
      };
    case "timeline": {
      const item = event["item"];
      if (typeof item !== "object" || item === null) return null;
      const record = item as Record<string, unknown>;
      const itemType = typeof record["type"] === "string" ? record["type"] : "other";
      const text = typeof record["text"] === "string" ? record["text"] : undefined;
      switch (itemType) {
        case "user_message":
          return { ...base, kind: "user", ...(text ? { text } : {}), raw: itemType };
        case "assistant_message":
          return { ...base, kind: "assistant", ...(text ? { text } : {}), raw: itemType };
        case "reasoning":
          return { ...base, kind: "reasoning", ...(text ? { text } : {}), raw: itemType };
        case "tool_call": {
          const toolName = typeof record["name"] === "string" ? record["name"] : undefined;
          return {
            ...base,
            kind: "tool",
            raw: itemType,
            ...(toolName ? { toolName } : {}),
          };
        }
        case "error": {
          const message = typeof record["message"] === "string" ? record["message"] : undefined;
          return { ...base, kind: "error", raw: itemType, ...(message ? { text: message } : {}) };
        }
        default:
          return { ...base, kind: "other", raw: itemType };
      }
    }
    default:
      return { ...base, kind: "other" };
  }
}

function toSummary(agent: {
  id: string;
  title?: string | null;
  status?: string;
  provider?: string;
  cwd?: string | null;
  updatedAt?: string | null;
}): AgentSummary {
  return {
    id: agent.id,
    title: agent.title ?? null,
    status: agent.status ?? "unknown",
    provider: agent.provider ?? "unknown",
    cwd: agent.cwd ?? null,
    updatedAt: agent.updatedAt ?? null,
  };
}

function hostFromWsUrl(url: string): string {
  try {
    const parsed = new URL(url);
    return parsed.host;
  } catch {
    return url;
  }
}

function describe(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

/**
 * Separates "the daemon rejected our credentials" from "the daemon is not there".
 *
 * The daemon closes an unauthenticated socket with code 4401 and reason
 * `Incorrect password` / `Password required`
 * (`server/websocket-server.ts:908-913`). The driver surfaces that as a plain
 * `Error` with the reason or the close code in its message and offers no typed
 * error to switch on, so this matches text — deliberately, and in one place, so
 * a future SDK change breaks exactly one function. Measured against a real
 * daemon: a wrong password previously arrived as DAEMON_DOWN, which would have
 * sent the user to a restart flow for a credential problem.
 */
/**
 * Recognises the daemon's "this provider cannot run here" refusal.
 *
 * Observed text: `Provider 'codex' is not available`. Like
 * {@link classifyConnectFailure}, this matches strings because the driver
 * rejects with plain `Error`s; keeping it to one predicate means a future
 * wording change breaks one function and its test, not the error model.
 */
function isProviderUnavailable(detail: string): boolean {
  return /provider\b[^]*\b(not available|unavailable|not found)/i.test(detail);
}

function classifyConnectFailure(err: unknown): BridgeError {
  const detail = describe(err);
  const isAuthFailure =
    detail.includes("4401") ||
    detail.includes("Incorrect password") ||
    detail.includes("Password required");
  return isAuthFailure
    ? new BridgeError("UNAUTHORIZED", "Paseo daemon rejected the credentials", detail)
    : new BridgeError("DAEMON_DOWN", "Paseo daemon is not reachable", detail);
}
