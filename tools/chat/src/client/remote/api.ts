// Talking to a paired computer through the relay. See design doc §7.
//
// Signed in is not enough: every request to a computer carries that computer's
// pairing token, and every message to send carries a signature only this phone's
// private key can make. The computer checks both against what the user approved on it.

import { pawRequest } from "../paw/api";
import { parsePawSSEChunk } from "../paw/sse";
import {
  REMOTE_ERROR_TEXT,
  fingerprint,
  generateSigningKey,
  readHeader,
  readItems,
  readSessionSummary,
  readStreamEvent,
  signSend,
  type RemoteDevice,
  type RemoteSessionHeader,
  type RemoteSessionSummary,
  type RemoteStreamEvent,
  type SyncItem,
} from "./protocol";
import { deletePairing, listPairings, savePairing, type StoredPairing } from "./store";

const PAIRING_HEADER = "X-Remote-Pairing";

export class RemoteError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

async function readJson(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

/** The server wraps its own answers in { code, message, data }; the computer's are passed through bare. */
function unwrap(payload: unknown): Record<string, unknown> {
  if (isRecord(payload) && isRecord(payload.data)) return payload.data;
  return isRecord(payload) ? payload : {};
}

async function serverFailure(response: Response): Promise<RemoteError> {
  const payload = await readJson(response);
  const reason = isRecord(payload) && typeof payload.reason === "string" ? payload.reason : `HTTP_${response.status}`;
  const message = isRecord(payload) && typeof payload.message === "string" ? payload.message : `HTTP ${response.status}`;
  const text: Record<string, string> = {
    REMOTE_DEVICE_OFFLINE: "电脑不在线（共飞助手没开，或没开启手机同步）",
    REMOTE_DEVICE_TIMEOUT: "电脑没有及时回应",
    REMOTE_DEVICE_GONE: "电脑刚刚断开了连接",
    REMOTE_PAIRING_NOT_ACTIVE: "这台手机和那台电脑的配对已失效",
    REMOTE_PAIRING_CODE_INVALID: "配对码错误或已过期",
  };
  return new RemoteError(reason, text[reason] ?? message);
}

/** A short name the computer shows when asking to approve this phone. */
export function phoneLabel(): string {
  const ua = typeof navigator === "undefined" ? "" : navigator.userAgent;
  if (/iPhone/i.test(ua)) return "iPhone";
  if (/iPad/i.test(ua)) return "iPad";
  if (/Android/i.test(ua)) return "Android 手机";
  return "浏览器";
}

// ---- Pairing -------------------------------------------------------------------------

/**
 * Takes the code shown on a computer. The pairing is not usable until the user
 * confirms it there, comparing the fingerprint this returns with the one it shows.
 */
export async function claimPairing(code: string): Promise<StoredPairing> {
  const { keyPair, publicKey } = await generateSigningKey();
  const response = await pawRequest("/api/v1/remote/pair/claim", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ code: code.replace(/\s+/g, ""), phone_label: phoneLabel(), public_key: publicKey }),
  });
  if (!response.ok) throw await serverFailure(response);

  const data = unwrap(await readJson(response));
  const pairing: StoredPairing = {
    deviceId: String(data.device_id ?? ""),
    deviceName: typeof data.device_name === "string" && data.device_name ? data.device_name : "电脑",
    pairingId: Number(data.pairing_id),
    token: String(data.token ?? ""),
    status: "claimed",
    keyPair,
    publicKey,
    fingerprint: await fingerprint(publicKey),
    createdAt: Date.now(),
  };
  if (!pairing.deviceId || !pairing.token || !Number.isFinite(pairing.pairingId)) {
    throw new RemoteError("bad_response", "服务端返回的配对信息不完整");
  }
  // Replaces any earlier pairing with the same computer.
  await savePairing(pairing);
  return pairing;
}

/** Checks whether the computer has confirmed yet; updates the stored pairing when it has. */
export async function refreshPairingStatus(pairing: StoredPairing): Promise<StoredPairing | null> {
  const response = await pawRequest(`/api/v1/remote/pairings/${pairing.pairingId}`, {
    method: "GET",
    headers: { Accept: "application/json" },
  });
  if (response.status === 404) {
    await deletePairing(pairing.deviceId);
    return null;
  }
  if (!response.ok) throw await serverFailure(response);
  const status = unwrap(await readJson(response)).status;
  if (status === "revoked") {
    await deletePairing(pairing.deviceId);
    return null;
  }
  if (status === "active" && pairing.status !== "active") {
    const confirmed = { ...pairing, status: "active" as const };
    await savePairing(confirmed);
    return confirmed;
  }
  return pairing;
}

export async function revokePairing(pairing: StoredPairing): Promise<void> {
  try {
    await pawRequest(`/api/v1/remote/pairings/${pairing.pairingId}`, { method: "DELETE" });
  } finally {
    // Forgotten here whatever the server says: this phone stops using it either way.
    await deletePairing(pairing.deviceId);
  }
}

/** This phone's paired computers, with whether each is online right now. */
export async function listDevices(): Promise<Array<{ pairing: StoredPairing; device: RemoteDevice | null }>> {
  const pairings = await listPairings();
  let online = new Map<string, RemoteDevice>();
  try {
    const response = await pawRequest("/api/v1/remote/devices", { method: "GET", headers: { Accept: "application/json" } });
    if (response.ok) {
      const payload = await readJson(response);
      const list = isRecord(payload) && Array.isArray(payload.data) ? payload.data : [];
      online = new Map(
        list.filter(isRecord).map((d) => {
          const status = isRecord(d.status) ? d.status : {};
          return [
            String(d.device_id),
            {
              deviceId: String(d.device_id),
              deviceName: typeof d.device_name === "string" ? d.device_name : "电脑",
              online: d.online === true,
              desktopRunning: status.desktop_running === true,
              canSend: status.can_send === true,
            } satisfies RemoteDevice,
          ];
        }),
      );
    }
  } catch {
    // Shown as offline; the list itself is local.
  }
  return pairings.map((pairing) => ({ pairing, device: online.get(pairing.deviceId) ?? null }));
}

// ---- Commands ------------------------------------------------------------------------

async function command(pairing: StoredPairing, body: Record<string, unknown>): Promise<Record<string, unknown>> {
  const response = await pawRequest(`/api/v1/remote/devices/${encodeURIComponent(pairing.deviceId)}/cmd`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", [PAIRING_HEADER]: pairing.token },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw await serverFailure(response);

  const answer = await readJson(response);
  if (!isRecord(answer)) throw new RemoteError("bad_response", "电脑返回的内容无法识别");
  if (answer.ok === false) {
    const code = typeof answer.error === "string" ? answer.error : "error";
    throw new RemoteError(code, REMOTE_ERROR_TEXT[code] ?? (typeof answer.message === "string" ? answer.message : code));
  }
  return answer;
}

export async function listSessions(pairing: StoredPairing): Promise<{ desktopRunning: boolean; sessions: RemoteSessionSummary[] }> {
  const answer = await command(pairing, { type: "sessions.list" });
  const sessions = Array.isArray(answer.sessions)
    ? answer.sessions.map(readSessionSummary).filter((s): s is RemoteSessionSummary => s !== null)
    : [];
  return { desktopRunning: answer.desktop_running === true, sessions };
}

export interface OpenedSession {
  header: RemoteSessionHeader | null;
  items: SyncItem[];
  hasOlder: boolean;
  truncatedTurnId: string | null;
  cursor: string;
}

export async function openSession(pairing: StoredPairing, threadId: string): Promise<OpenedSession> {
  const answer = await command(pairing, { type: "session.open", thread_id: threadId });
  return {
    header: readHeader(answer.session),
    items: readItems(answer.items),
    hasOlder: answer.has_older === true,
    truncatedTurnId: typeof answer.truncated_turn_id === "string" ? answer.truncated_turn_id : null,
    cursor: typeof answer.cursor === "string" ? answer.cursor : "",
  };
}

export async function loadHistory(
  pairing: StoredPairing,
  threadId: string,
  beforeTurnId: string,
): Promise<{ items: SyncItem[]; hasOlder: boolean }> {
  const answer = await command(pairing, { type: "session.history", thread_id: threadId, before_turn_id: beforeTurnId });
  return { items: readItems(answer.items), hasOlder: answer.has_older === true };
}

export async function loadDetail(
  pairing: StoredPairing,
  threadId: string,
  item: SyncItem,
  part: "output" | "diff" | "image",
  index = 0,
): Promise<{ text: string | null; dataUrl: string | null; truncated: boolean }> {
  const answer = await command(pairing, {
    type: "session.detail",
    thread_id: threadId,
    turn_id: item.turnId,
    item_id: item.itemId,
    part,
    index,
  });
  const data = typeof answer.data === "string" && typeof answer.media_type === "string"
    ? `data:${answer.media_type};base64,${answer.data}`
    : null;
  return { text: typeof answer.text === "string" ? answer.text : null, dataUrl: data, truncated: answer.truncated === true };
}

/**
 * Sends a message into the conversation on the computer. `queue` (the default) waits
 * for a running turn to end; `insert` folds it into the running turn, which can make
 * the model drop the rest of what it was doing.
 */
export async function sendMessage(
  pairing: StoredPairing,
  threadId: string,
  text: string,
  mode: "queue" | "insert" = "queue",
): Promise<{ queued: boolean }> {
  const signed = await signSend(pairing.keyPair.privateKey, pairing.pairingId, threadId, mode, text);
  const answer = await command(pairing, { type: "message.send", thread_id: threadId, text, mode, ...signed });
  return { queued: answer.queued === true };
}

export async function navigateOnComputer(pairing: StoredPairing, threadId: string): Promise<void> {
  await command(pairing, { type: "thread.navigate", thread_id: threadId });
}

// ---- Following -----------------------------------------------------------------------

const RETRY_DELAYS_MS = [1000, 2000, 5000, 15000, 30000];

/**
 * Follows a conversation until `signal` aborts. Reconnects after drops from the
 * cursor it last received, so nothing is missed and nothing repeats. Stops on
 * `revoked` and `resync` (the caller reopens the conversation).
 */
export async function followSession(
  pairing: StoredPairing,
  threadId: string,
  cursor: string,
  onEvent: (event: RemoteStreamEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  let current = cursor;
  let failures = 0;
  while (!signal.aborted) {
    let stop = false;
    try {
      const url = `/api/v1/remote/devices/${encodeURIComponent(pairing.deviceId)}/sessions/${encodeURIComponent(threadId)}/stream?cursor=${encodeURIComponent(current)}`;
      const response = await pawRequest(url, {
        method: "GET",
        signal,
        headers: { Accept: "text/event-stream", [PAIRING_HEADER]: pairing.token },
      });
      if (!response.ok || !response.body) {
        const failure = await serverFailure(response);
        if (failure.code === "REMOTE_PAIRING_NOT_ACTIVE") {
          onEvent({ type: "revoked" });
          return;
        }
        throw failure;
      }

      failures = 0;
      const reader = response.body.getReader();
      const decoder = new TextDecoder("utf-8");
      let buffer = "";
      while (!stop) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, "\n");
        const parsed = parsePawSSEChunk(buffer);
        buffer = parsed.remainder;
        for (const frame of parsed.frames) {
          const event = readStreamEvent(frame);
          if (!event) continue;
          if (event.type === "items" && event.cursor) current = event.cursor;
          onEvent(event);
          if (event.type === "revoked" || event.type === "resync" || event.type === "error") stop = true;
        }
      }
      if (stop) return;
    } catch (error) {
      if (signal.aborted) return;
      onEvent({ type: "error", error: error instanceof RemoteError ? error.code : "network", message: error instanceof Error ? error.message : "连接中断" });
    }

    const delay = RETRY_DELAYS_MS[Math.min(failures++, RETRY_DELAYS_MS.length - 1)];
    await new Promise<void>((resolve) => {
      const timer = setTimeout(resolve, delay);
      signal.addEventListener("abort", () => {
        clearTimeout(timer);
        resolve();
      }, { once: true });
    });
  }
}
