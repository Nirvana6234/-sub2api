/**
 * cofly-paseo-bridge entry point.
 *
 * Spawned by the adapter host with:
 *   COFLY_PIPE       named pipe / socket path created and ACL'd by the consumer
 *   COFLY_TOKEN      one-shot handshake secret
 *   COFLY_DAEMON_URL loopback daemon WebSocket URL
 *   COFLY_DAEMON_PASSWORD (optional)
 *   COFLY_WORKDIRS   (optional) JSON array of {key, path, label} — the only place
 *                    a filesystem path ever enters; see workdirs.ts
 *   COFLY_ALLOW_RELAY  (optional) "1" to permit the relay operations at all
 *
 * Secrets travel in the environment rather than on the wire so they never end up
 * in a frame that could be logged by either side.
 */
import { DaemonSession } from "./daemon.js";
import { connectPipe } from "./pipe.js";
import { TimelineBuffer } from "./timeline-buffer.js";
import { WorkdirRegistry } from "./workdirs.js";
import {
  BridgeError,
  CONTRACT_VERSION,
  contractMajor,
  type AgentCreateArgs,
  type AgentSendArgs,
  type Frame,
  type HelloArgs,
  type HelloResult,
  type RequestFrame,
  type ResponseFrame,
} from "./contract.js";

const BRIDGE_VERSION = "0.1.0";
/** Pinned in package.json; reported for diagnostics only. */
const PASEO_CLIENT_VERSION = "0.7.0-beta.3";
const PIPE_CONNECT_TIMEOUT_MS = 10_000;
/**
 * Short on purpose: the daemon is on loopback, so a slow connect means it is not
 * there. A long budget here would turn "daemon is down" — a state the consumer
 * polls for and handles — into a request timeout it can only report as a
 * transport failure.
 */
const DAEMON_CONNECT_TIMEOUT_MS = 5_000;
const TIMELINE_WINDOW_MS = 100;
const TIMELINE_MAX_BUFFERED = 500;

function requireEnv(name: string): string {
  const value = process.env[name];
  if (!value) {
    process.stderr.write(`[bridge] missing required environment variable ${name}\n`);
    process.exit(2);
  }
  return value;
}

function requireString(args: Record<string, unknown> | undefined, field: string): string {
  const value = args?.[field];
  if (typeof value !== "string" || value.trim().length === 0) {
    throw new BridgeError("BAD_REQUEST", `'${field}' is required`);
  }
  return value;
}

function requireRelayGrant(granted: boolean): void {
  if (!granted) {
    throw new BridgeError(
      "UNAUTHORIZED",
      "This bridge was not granted the relay operations",
      "set COFLY_ALLOW_RELAY=1 in the host that owns user consent",
    );
  }
}

function optionalString(args: Record<string, unknown> | undefined, field: string): string | undefined {
  const value = args?.[field];
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

async function main(): Promise<void> {
  const pipePath = requireEnv("COFLY_PIPE");
  const token = requireEnv("COFLY_TOKEN");
  const daemonUrl = requireEnv("COFLY_DAEMON_URL");
  const daemonPassword = process.env["COFLY_DAEMON_PASSWORD"];
  const workdirs = WorkdirRegistry.fromEnv(process.env["COFLY_WORKDIRS"]);
  // Relay operations change who can reach this machine, so they are host-granted
  // rather than always-on: only the process that spawned the bridge — the one
  // that can ask the user — can turn them on. A bridge embedded in some other
  // consumer cannot enable remote access behind the user's back.
  const relayAllowed = process.env["COFLY_ALLOW_RELAY"] === "1";

  const channel = await connectPipe(pipePath, PIPE_CONNECT_TIMEOUT_MS);

  const timelineBuffer = new TimelineBuffer({
    windowMs: TIMELINE_WINDOW_MS,
    maxBuffered: TIMELINE_MAX_BUFFERED,
    emit: (batch) => channel.send({ t: "evt", topic: "timeline", data: batch }),
  });

  const session = new DaemonSession(
    {
      url: daemonUrl,
      password: daemonPassword,
      connectTimeoutMs: DAEMON_CONNECT_TIMEOUT_MS,
    },
    {
      timeline: (event) => timelineBuffer.push(event),
      // Attention is never buffered: it is the "a human is needed" signal, and a
      // 100 ms batching window would be the wrong trade for exactly one event
      // per turn.
      attention: (event) => channel.send({ t: "evt", topic: "attention", data: event }),
    },
  );

  /** Until `hello` succeeds every other operation is refused. */
  let greeted = false;

  const respond = (frame: ResponseFrame): void => channel.send(frame);

  const handleHello = (args: HelloArgs | undefined): HelloResult => {
    if (!args || typeof args.token !== "string" || typeof args.contract !== "string") {
      throw new BridgeError("BAD_REQUEST", "hello requires token and contract");
    }
    if (args.token !== token) {
      // Deliberately says nothing about which half was wrong.
      throw new BridgeError("UNAUTHORIZED", "Handshake rejected");
    }
    if (contractMajor(args.contract) !== contractMajor(CONTRACT_VERSION)) {
      throw new BridgeError(
        "CONTRACT_MISMATCH",
        `Bridge speaks contract ${CONTRACT_VERSION}, consumer asked for ${args.contract}`,
      );
    }
    greeted = true;
    return {
      contract: CONTRACT_VERSION,
      paseoClientVersion: PASEO_CLIENT_VERSION,
      bridgeVersion: BRIDGE_VERSION,
    };
  };

  const dispatch = async (request: RequestFrame): Promise<unknown> => {
    if (request.op === "hello") {
      return handleHello(request.args as HelloArgs | undefined);
    }
    if (!greeted) {
      throw new BridgeError("UNAUTHORIZED", "hello must succeed before any other operation");
    }
    switch (request.op) {
      case "health":
        return await session.health();

      case "workdirs.list":
        return { workdirs: workdirs.list() };

      case "agents.list":
        return await session.listAgents();

      case "agents.create": {
        const args = request.args as Partial<AgentCreateArgs> | undefined;
        // Resolution happens here, from a key the host registered. A consumer
        // cannot name a directory that was never consented to.
        const cwd = workdirs.resolve(requireString(args as Record<string, unknown>, "cwdKey"));
        return await session.createAgent({
          cwd,
          model: optionalString(args as Record<string, unknown>, "model"),
          prompt: optionalString(args as Record<string, unknown>, "prompt"),
          title: optionalString(args as Record<string, unknown>, "title"),
        });
      }

      case "agents.send": {
        const args = request.args as Partial<AgentSendArgs> | undefined;
        const record = args as Record<string, unknown> | undefined;
        await session.send(requireString(record, "agentId"), requireString(record, "text"));
        return { ok: true };
      }

      case "agents.stop": {
        const record = request.args as Record<string, unknown> | undefined;
        await session.stop(requireString(record, "agentId"));
        return { ok: true };
      }

      case "agents.archive": {
        const record = request.args as Record<string, unknown> | undefined;
        const agentId = requireString(record, "agentId");
        const result = await session.archive(agentId);
        timelineBuffer.forget(agentId);
        return result;
      }

      case "timeline.subscribe": {
        const record = request.args as Record<string, unknown> | undefined;
        // Returns the effective set, so an eviction past the cap is visible to
        // the caller instead of silently ending a stream it still believes in.
        return await session.subscribeTimeline(requireString(record, "agentId"));
      }

      case "timeline.unsubscribe": {
        const record = request.args as Record<string, unknown> | undefined;
        const agentId = requireString(record, "agentId");
        const result = await session.unsubscribeTimeline(agentId);
        timelineBuffer.forget(agentId);
        return result;
      }

      case "notifications.subscribe":
        // Not a formality: the daemon only broadcasts attention to sessions that
        // hold an agent-updates subscription, so this call is what makes the
        // channel exist. It used to just ensure a connection, which reported
        // success and delivered nothing — a live turn produced a full timeline
        // and zero attention events.
        await session.subscribeAttention();
        return { ok: true };

      case "relay.status":
        // Ungated: reading the state changes nothing, and a consumer that cannot
        // even display "remote access: off" has no way to reassure the user.
        return await session.relayStatus();

      case "relay.pair":
        // The only gated one. It is the single operation that widens who can
        // reach this machine.
        requireRelayGrant(relayAllowed);
        return await session.relayPair();

      case "relay.disable":
        // Ungated on purpose: turning remote access OFF must never require a
        // permission the caller might not have. De-escalation is always allowed.
        return await session.relayDisable();

      default:
        throw new BridgeError("UNKNOWN_OP", `Unknown operation '${request.op}'`);
    }
  };

  channel.onFrame((frame: Frame) => {
    if (frame.t !== "req") return;
    void (async () => {
      try {
        const data = await dispatch(frame);
        respond({ t: "res", id: frame.id, ok: true, data });
      } catch (err) {
        const error =
          err instanceof BridgeError
            ? err.toContractError()
            : {
                code: "INTERNAL" as const,
                message: "Bridge failed to handle the request",
                detail: err instanceof Error ? err.message : String(err),
              };
        respond({ t: "res", id: frame.id, ok: false, error });
      }
    })();
  });

  channel.onClose(() => {
    // The consumer owns the pipe. When it goes away the bridge has no reason to
    // linger — and lingering would leave an orphan holding a daemon connection.
    void session.close().finally(() => process.exit(0));
  });
}

main().catch((err: unknown) => {
  process.stderr.write(`[bridge] fatal: ${err instanceof Error ? err.stack : String(err)}\n`);
  process.exit(1);
});
