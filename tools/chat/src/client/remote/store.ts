// What the phone keeps about its paired computers, in IndexedDB.
//
// The signing key pair is stored as CryptoKey objects: IndexedDB can hold them, and
// because the private key was created non-extractable, not even this page can read
// its bytes back — only use it to sign. The pairing token is a bearer secret for one
// computer; it never leaves this origin except in the X-Remote-Pairing header.
//
// A pairing belongs to the account that made it (userId) and outlives sign-out: signing
// back in continues with it, with no new code. Another account signing in on this
// browser does not see it, and could not use it anyway — the server takes a pairing's
// token only with a session of the account that paired. It goes when the user 解除配对,
// or when the server says it was revoked. Cached conversation content is cleared on
// sign-out (clearRemoteCache) and refills from the computer.

import type { RemoteSessionHeader, SyncItem } from "./protocol";

const DATABASE_NAME = "paw-remote";
const DATABASE_VERSION = 1;
const PAIRINGS = "pairings";
const SESSIONS = "sessions";

/** Most recent items kept per conversation; older ones are fetched again when scrolled to. */
export const CACHED_ITEMS_PER_SESSION = 400;

export interface StoredPairing {
  deviceId: string;
  deviceName: string;
  pairingId: number;
  token: string;
  status: "claimed" | "active";
  keyPair: CryptoKeyPair;
  publicKey: string;
  fingerprint: string;
  createdAt: number;
  /** The account that paired. Missing on pairings saved before this was kept. */
  userId?: number;
}

export interface StoredSession {
  key: string;
  header: RemoteSessionHeader | null;
  items: SyncItem[];
  cursor: string;
  hasOlder: boolean;
  savedAt: number;
}

function canUseIndexedDb(): boolean {
  return typeof window !== "undefined" && typeof window.indexedDB !== "undefined";
}

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = window.indexedDB.open(DATABASE_NAME, DATABASE_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(PAIRINGS)) db.createObjectStore(PAIRINGS, { keyPath: "deviceId" });
      if (!db.objectStoreNames.contains(SESSIONS)) db.createObjectStore(SESSIONS, { keyPath: "key" });
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error("IndexedDB open failed"));
  });
}

function requestResult<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error("IndexedDB request failed"));
  });
}

function done(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error ?? new Error("IndexedDB transaction failed"));
    transaction.onabort = () => reject(transaction.error ?? new Error("IndexedDB transaction aborted"));
  });
}

async function withStore<T>(
  store: string,
  mode: IDBTransactionMode,
  work: (objects: IDBObjectStore) => IDBRequest<T> | void,
): Promise<T | undefined> {
  if (!canUseIndexedDb()) return undefined;
  const db = await openDatabase();
  try {
    const transaction = db.transaction(store, mode);
    const request = work(transaction.objectStore(store));
    const result = request ? await requestResult(request) : undefined;
    await done(transaction);
    return result;
  } finally {
    db.close();
  }
}

export function sessionKey(deviceId: string, threadId: string): string {
  return `${deviceId}|${threadId}`;
}

export async function listPairings(): Promise<StoredPairing[]> {
  return ((await withStore<StoredPairing[]>(PAIRINGS, "readonly", (s) => s.getAll())) ?? []).sort(
    (a, b) => a.createdAt - b.createdAt,
  );
}

export async function getPairing(deviceId: string): Promise<StoredPairing | undefined> {
  return withStore<StoredPairing>(PAIRINGS, "readonly", (s) => s.get(deviceId));
}

export async function savePairing(pairing: StoredPairing): Promise<void> {
  await withStore(PAIRINGS, "readwrite", (s) => s.put(pairing));
}

/** Forgets a computer and everything cached from it. */
export async function deletePairing(deviceId: string): Promise<void> {
  await withStore(PAIRINGS, "readwrite", (s) => s.delete(deviceId));
  const sessions = (await withStore<StoredSession[]>(SESSIONS, "readonly", (s) => s.getAll())) ?? [];
  for (const session of sessions.filter((x) => x.key.startsWith(`${deviceId}|`))) {
    await withStore(SESSIONS, "readwrite", (s) => s.delete(session.key));
  }
}

export async function loadSession(deviceId: string, threadId: string): Promise<StoredSession | undefined> {
  return withStore<StoredSession>(SESSIONS, "readonly", (s) => s.get(sessionKey(deviceId, threadId)));
}

export async function saveSession(deviceId: string, threadId: string, session: Omit<StoredSession, "key" | "savedAt">): Promise<void> {
  const items = session.items.slice(-CACHED_ITEMS_PER_SESSION);
  await withStore(SESSIONS, "readwrite", (s) =>
    s.put({
      ...session,
      items,
      hasOlder: session.hasOlder || items.length < session.items.length,
      key: sessionKey(deviceId, threadId),
      savedAt: Date.now(),
    } satisfies StoredSession),
  );
}

export async function deleteSession(deviceId: string, threadId: string): Promise<void> {
  await withStore(SESSIONS, "readwrite", (s) => s.delete(sessionKey(deviceId, threadId)));
}

/** Drops cached conversations the computer no longer shares. */
export async function pruneSessions(deviceId: string, sharedThreadIds: string[]): Promise<void> {
  const keep = new Set(sharedThreadIds.map((id) => sessionKey(deviceId, id)));
  const sessions = (await withStore<StoredSession[]>(SESSIONS, "readonly", (s) => s.getAll())) ?? [];
  for (const session of sessions.filter((x) => x.key.startsWith(`${deviceId}|`) && !keep.has(x.key))) {
    await withStore(SESSIONS, "readwrite", (s) => s.delete(session.key));
  }
}

/** Everything, on sign-out: the next account must not inherit this one's computers. */
/** On sign-out: the cached conversations go, the pairings stay (see the top of this file). */
export async function clearRemoteCache(): Promise<void> {
  if (!canUseIndexedDb()) return;
  try {
    await withStore(SESSIONS, "readwrite", (s) => s.clear());
  } catch {
    // Best effort; a failed clear leaves only data this account already had.
  }
}
