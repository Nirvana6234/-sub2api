// Run with `npm test` (node --test; Node strips the types itself).
//
// The first three tests hold the same vectors as the assistant's
// PhoneProtocolVectorTests.cs: if either side changes the signed string or the
// fingerprint, both suites fail instead of every send being silently refused.

import assert from "node:assert/strict";
import { test } from "node:test";

import {
  canonicalSend,
  fingerprint,
  generateSigningKey,
  mergeItems,
  readStreamEvent,
  signSend,
  type SyncItem,
} from "./protocol.ts";

test("the signed string matches the assistant's", async () => {
  assert.equal(
    await canonicalSend(5, "01a0ced9-0000-7000-8000-000000000001", "queue", "请只回复 OK", 1790200000000, "bm9uY2Utbm9uY2Utbm9uY2Ut"),
    "cofly-remote/1\nmessage.send\n5\n01a0ced9-0000-7000-8000-000000000001\nqueue\n" +
      "5cf4668abee37f0614db4b4df55676a9f0c0119d21a4cf6f269ce0c0be6f2267\n1790200000000\nbm9uY2Utbm9uY2Utbm9uY2Ut",
  );
});

test("the fingerprint matches the assistant's", async () => {
  assert.equal(await fingerprint("AAAA"), "63C 1DD");
});

test("a send is signed so that the matching public key verifies it", async () => {
  const { keyPair, publicKey } = await generateSigningKey();
  const signed = await signSend(keyPair.privateKey, 5, "t1", "insert", "改一下", 1790200000000);

  const message = new TextEncoder().encode(
    await canonicalSend(5, "t1", "insert", "改一下", signed.ts, signed.nonce),
  );
  const sig = Uint8Array.from(atob(signed.sig), (c) => c.charCodeAt(0));
  assert.equal(sig.length, 64, "raw r‖s, as the assistant expects");
  assert.ok(await crypto.subtle.verify({ name: "ECDSA", hash: "SHA-256" }, keyPair.publicKey, sig, message));
  assert.ok(publicKey.length > 80);
});

test("the private key cannot be exported", async () => {
  const { keyPair } = await generateSigningKey();
  await assert.rejects(crypto.subtle.exportKey("pkcs8", keyPair.privateKey));
});

const item = (seq: number, kind: SyncItem["kind"], turnId = "t1"): SyncItem => ({
  seq,
  turnId,
  itemId: `i${seq}`,
  kind,
  text: null,
});

test("a running card goes away once its turn moves on", () => {
  const merged = mergeItems([item(1, "turn_started"), item(2, "running")], [item(3, "command")]);
  assert.deepEqual(merged.map((i) => i.kind), ["turn_started", "command"]);
});

test("a running card stays while it is the latest thing in its turn", () => {
  const merged = mergeItems([item(1, "turn_started")], [item(2, "running")]);
  assert.deepEqual(merged.map((i) => i.kind), ["turn_started", "running"]);
});

test("the same item arriving twice is kept once, in order", () => {
  const merged = mergeItems([item(5, "reply"), item(1, "user")], [item(5, "reply"), item(3, "progress")]);
  assert.deepEqual(merged.map((i) => i.seq), [1, 3, 5]);
});

test("stream frames are read by type", () => {
  assert.deepEqual(readStreamEvent('data: {"type":"status","status":"active","waiting_on_approval":true}'), {
    type: "status",
    status: "active",
    waitingOnApproval: true,
  });
  assert.deepEqual(readStreamEvent("event: end\ndata: {}"), { type: "end" });
  assert.deepEqual(readStreamEvent('data: {"type":"revoked"}'), { type: "revoked" });
  assert.equal(readStreamEvent(": ping"), null);

  const items = readStreamEvent('data: {"type":"items","cursor":"12.ab","items":[{"seq":12,"turn_id":"t","item_id":"x","kind":"reply","text":"好"}]}');
  assert.equal(items?.type, "items");
  assert.equal(items?.type === "items" ? items.items[0].text : null, "好");
});

test("a refusal carried on the stream is surfaced", () => {
  assert.deepEqual(readStreamEvent('data: {"ok":false,"error":"not_selected","message":"没勾选"}'), {
    type: "error",
    error: "not_selected",
    message: "没勾选",
  });
});
