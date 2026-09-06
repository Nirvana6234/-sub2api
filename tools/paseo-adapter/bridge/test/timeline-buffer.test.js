import test from "node:test";
import assert from "node:assert/strict";
import { TimelineBuffer } from "../dist/timeline-buffer.js";

function makeBuffer(options = {}) {
  const emitted = [];
  let scheduled = null;
  const buffer = new TimelineBuffer({
    windowMs: 100,
    maxBuffered: 3,
    emit: (batch) => emitted.push(batch),
    // Manual clock: the test decides when the window elapses.
    schedule: (fn) => {
      scheduled = fn;
      return {};
    },
    ...options,
  });
  return { buffer, emitted, tick: () => scheduled?.() };
}

function event(agentId, text) {
  return { agentId, kind: "assistant", text, at: "2026-09-01T00:00:00Z", raw: "assistant_message" };
}

test("events are batched per agent within one window", () => {
  const { buffer, emitted, tick } = makeBuffer();

  buffer.push(event("a1", "one"));
  buffer.push(event("a1", "two"));
  buffer.push(event("a2", "other"));
  assert.equal(emitted.length, 0, "nothing should be sent before the window elapses");

  tick();

  assert.equal(emitted.length, 2);
  const first = emitted.find((b) => b.agentId === "a1");
  assert.deepEqual(first.events.map((e) => e.text), ["one", "two"]);
  assert.equal(first.dropped, undefined);
});

// Batching preserves content; only a consumer that falls far behind loses
// events, and then it is told how many so it can refetch instead of guessing.
test("overflow drops the oldest events and reports the count", () => {
  const { buffer, emitted, tick } = makeBuffer();

  for (const text of ["1", "2", "3", "4", "5"]) {
    buffer.push(event("a1", text));
  }
  tick();

  assert.equal(emitted.length, 1);
  assert.deepEqual(emitted[0].events.map((e) => e.text), ["3", "4", "5"]);
  assert.equal(emitted[0].dropped, 2);
});

test("forget discards an agent's pending events", () => {
  const { buffer, emitted, tick } = makeBuffer();

  buffer.push(event("a1", "one"));
  buffer.forget("a1");
  tick();

  assert.equal(emitted.length, 0);
});

test("flush on an empty buffer emits nothing", () => {
  const { buffer, emitted } = makeBuffer();
  buffer.flush();
  assert.equal(emitted.length, 0);
});
