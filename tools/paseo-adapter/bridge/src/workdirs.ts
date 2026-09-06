/**
 * The key → path map, and the reason it lives here.
 *
 * Whoever starts the bridge owns consent: on a desktop that is the adapter host
 * (after the user picked a directory), on the server it is the process holding
 * the binding record the PC wrote. Both hand the map in at spawn time. Because
 * the map is not reachable from the contract, **no consumer can widen its own
 * blast radius** — the worst a compromised or buggy consumer can do is name a
 * key that already exists.
 *
 * Registering nothing is a valid state: it means "this bridge may not start
 * agents", and `agents.create` then fails with a plain, actionable message.
 */
import { BridgeError, type WorkdirEntry } from "./contract.js";

interface WorkdirRecord extends WorkdirEntry {
  path: string;
}

export class WorkdirRegistry {
  private readonly byKey = new Map<string, WorkdirRecord>();

  private constructor(records: WorkdirRecord[]) {
    for (const record of records) {
      this.byKey.set(record.key, record);
    }
  }

  /**
   * Parses the `COFLY_WORKDIRS` environment value: a JSON array of
   * `{ key, path, label? }`.
   *
   * A malformed value is fatal rather than ignored. Silently starting with an
   * empty registry would look like "the user has no directories" and send the
   * consumer down a UI path that hides a deployment bug.
   */
  public static fromEnv(raw: string | undefined): WorkdirRegistry {
    if (!raw || raw.trim().length === 0) {
      return new WorkdirRegistry([]);
    }

    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch (err) {
      throw new Error(`COFLY_WORKDIRS is not valid JSON: ${err instanceof Error ? err.message : String(err)}`);
    }
    if (!Array.isArray(parsed)) {
      throw new Error("COFLY_WORKDIRS must be a JSON array");
    }

    const records: WorkdirRecord[] = [];
    for (const entry of parsed) {
      if (typeof entry !== "object" || entry === null) {
        throw new Error("COFLY_WORKDIRS entries must be objects");
      }
      const candidate = entry as Record<string, unknown>;
      const key = typeof candidate["key"] === "string" ? candidate["key"].trim() : "";
      const path = typeof candidate["path"] === "string" ? candidate["path"].trim() : "";
      const label = typeof candidate["label"] === "string" ? candidate["label"] : key;
      if (!key || !path) {
        throw new Error("COFLY_WORKDIRS entries require non-empty 'key' and 'path'");
      }
      records.push({ key, path, label });
    }
    return new WorkdirRegistry(records);
  }

  /** What consumers may see: keys and labels, never paths. */
  public list(): WorkdirEntry[] {
    return [...this.byKey.values()].map((record) => ({ key: record.key, label: record.label }));
  }

  /**
   * Resolves a key to its path.
   *
   * @throws BridgeError `BAD_REQUEST` for an unknown key — the message names the
   * registered keys, because in practice this fires when a consumer and its host
   * disagree about configuration, and that is the fastest way to see it.
   */
  public resolve(key: string): string {
    const record = this.byKey.get(key);
    if (!record) {
      const known = [...this.byKey.keys()];
      throw new BridgeError(
        "BAD_REQUEST",
        known.length === 0
          ? "No work directories are registered for this bridge"
          : `Unknown work directory '${key}'`,
        known.length === 0 ? undefined : `registered: ${known.join(", ")}`,
      );
    }
    return record.path;
  }
}
