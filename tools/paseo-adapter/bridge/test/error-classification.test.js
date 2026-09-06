import test from "node:test";
import assert from "node:assert/strict";
import { DaemonSession } from "../dist/daemon.js";

// The classification predicates are not exported (they are implementation
// detail), so they are exercised through the session's own failure path with a
// stubbed driver call. What is being locked in is the mapping, which is the part
// consumers branch on.
function sessionWithFailure(message) {
  const session = new DaemonSession(
    { url: "ws://127.0.0.1:1/ws", connectTimeoutMs: 10 },
    { timeline() {}, attention() {} },
  );
  // Pretend the connection already exists so `guard` is what classifies. The
  // stub reports itself connected because that is the interesting case: a
  // daemon-level rejection over a live socket must classify without tearing the
  // connection down.
  session.client = {
    fetchAgents: async () => {
      throw new Error(message);
    },
    getConnectionState: () => ({ status: "connected" }),
    close: async () => {},
  };
  return session;
}

test("provider unavailability is CODEX_MISSING, not a dead daemon", async () => {
  const session = sessionWithFailure("Provider 'codex' is not available");
  await assert.rejects(
    () => session.listAgents(),
    (err) => err.code === "CODEX_MISSING",
  );
});

test("an ordinary refusal stays DAEMON_DOWN", async () => {
  const session = sessionWithFailure("Something else went wrong");
  await assert.rejects(
    () => session.listAgents(),
    (err) => err.code === "DAEMON_DOWN",
  );
});

// Guard against a lazy regex: an agent whose *prompt* mentions the word
// "provider" must not turn an unrelated failure into a bogus install prompt.
test("the provider match needs both halves of the phrase", async () => {
  const session = sessionWithFailure("provider said hello");
  await assert.rejects(
    () => session.listAgents(),
    (err) => err.code === "DAEMON_DOWN",
  );
});

// A rejection over a live socket must not cost the connection: otherwise one bad
// agent id from a caller forces a full reconnect for everyone.
test("a daemon-level rejection keeps the connection", async () => {
  const session = sessionWithFailure("Agent not found");
  await assert.rejects(() => session.listAgents());
  assert.notEqual(session.client, null, "the client should still be cached");
});
