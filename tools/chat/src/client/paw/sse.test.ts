// Run with `npm test` (node --test; Node strips the types itself).

import assert from "node:assert/strict";
import { test } from "node:test";

import { parsePawSSEChunk, readPawSSEFrameData } from "./sse.ts";

test("frame data: only data lines, joined", () => {
  assert.equal(readPawSSEFrameData('event: message\nid: 7\ndata: {"a":1}'), '{"a":1}');
  assert.equal(readPawSSEFrameData("data: line one\ndata: line two"), "line one\nline two");
  assert.equal(readPawSSEFrameData("data:[DONE]"), "[DONE]");
});

test("frame data: a comment-only frame has none", () => {
  assert.equal(readPawSSEFrameData(": keepalive"), null);
  assert.equal(readPawSSEFrameData("event: ping"), null);
});

test("CRLF streams split into frames once normalised", () => {
  const raw = 'data: {"a":1}\r\n\r\n: keepalive\r\n\r\ndata: [DONE]\r\n\r\n'.replace(/\r\n/g, "\n");
  const { frames, remainder } = parsePawSSEChunk(raw);
  assert.deepEqual(frames.map(readPawSSEFrameData), ['{"a":1}', null, "[DONE]"]);
  assert.equal(remainder, "");
});
