// Run with: node --test test/
// Uses node:test so the bridge keeps exactly one dependency (@getpaseo/client).
import test from "node:test";
import assert from "node:assert/strict";
import { WorkdirRegistry } from "../dist/workdirs.js";

test("list exposes keys and labels but never paths", () => {
  const registry = WorkdirRegistry.fromEnv(
    JSON.stringify([{ key: "default", path: "C:/work/agent", label: "默认工作区" }]),
  );

  const listed = registry.list();
  assert.deepEqual(listed, [{ key: "default", label: "默认工作区" }]);
  // The invariant the whole design rests on: a consumer cannot learn or supply a
  // path, so it cannot widen its own blast radius.
  assert.equal(JSON.stringify(listed).includes("C:/work/agent"), false);
});

test("resolve maps a key to its path", () => {
  const registry = WorkdirRegistry.fromEnv(
    JSON.stringify([{ key: "default", path: "C:/work/agent" }]),
  );
  assert.equal(registry.resolve("default"), "C:/work/agent");
});

test("an unknown key is a BAD_REQUEST that names the registered keys", () => {
  const registry = WorkdirRegistry.fromEnv(
    JSON.stringify([{ key: "a", path: "C:/a" }, { key: "b", path: "C:/b" }]),
  );

  assert.throws(
    () => registry.resolve("../../etc"),
    (err) => err.code === "BAD_REQUEST" && String(err.detail).includes("a, b"),
  );
});

test("an empty registry says so rather than reporting an unknown key", () => {
  const registry = WorkdirRegistry.fromEnv(undefined);
  assert.equal(registry.list().length, 0);
  assert.throws(
    () => registry.resolve("default"),
    (err) => err.code === "BAD_REQUEST" && /No work directories/.test(err.message),
  );
});

// Silently starting with an empty registry would look like "the user has no
// directories" and hide a deployment bug behind a plausible UI state.
test("malformed configuration is fatal, not ignored", () => {
  assert.throws(() => WorkdirRegistry.fromEnv("{not json"), /not valid JSON/);
  assert.throws(() => WorkdirRegistry.fromEnv('{"key":"a"}'), /must be a JSON array/);
  assert.throws(() => WorkdirRegistry.fromEnv('[{"key":"a"}]'), /require non-empty/);
});
