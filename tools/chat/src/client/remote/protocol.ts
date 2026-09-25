// Phone ↔ desktop session sync: the formats the desktop assistant speaks.
//
// Everything here mirrors tools/codex-relay-client (DesktopSync) and the design doc
// §7.4. Two things must match the assistant byte for byte, or nothing works and
// nothing says why: the string a send is signed over, and the key fingerprint the
// user compares on both screens. Both are pinned by known-answer tests on each side.
//
// No imports on purpose: this file runs under `node --test` as-is.

export type RemotePermission = "full_access" | "auto" | "sandboxed";

export interface RemoteDevice {
  deviceId: string;
  deviceName: string;
  online: boolean;
  desktopRunning: boolean;
  canSend: boolean;
}

export interface RemoteSessionSummary {
  threadId: string;
  title: string;
  cwd: string | null;
  status: string;
  permission: RemotePermission;
  updatedAt: number | null;
}

export type SyncItemKind =
  | "user"
  | "progress"
  | "reply"
  | "thinking"
  | "command"
  | "file_change"
  | "tool"
  | "image"
  | "running"
  | "notice"
  | "turn_started"
  | "turn_ended"
  | "unknown";

export interface SyncFile {
  path: string;
  change: string;
  added: number;
  removed: number;
}

export interface SyncItem {
  seq: number;
  turnId: string | null;
  itemId: string;
  kind: SyncItemKind;
  text: string | null;
  origin?: "desktop" | "phone" | "delegated";
  /** A "progress" message the desktop wrote without saying whether it was the answer. */
  phaseMissing?: boolean;
  imageCount?: number;
  command?: string | null;
  exitCode?: number;
  status?: string | null;
  durationMs?: number;
  outputPreview?: string | null;
  outputTruncated?: boolean;
  files?: SyncFile[];
  outcome?: "completed" | "failed" | "aborted";
}

export interface RemoteSessionHeader {
  threadId: string;
  title: string | null;
  cwd: string | null;
  model: string | null;
  permission: RemotePermission;
  openTurnId: string | null;
}

export type RemoteStreamEvent =
  | { type: "items"; items: SyncItem[]; cursor: string }
  | { type: "status"; status: string; waitingOnApproval: boolean }
  | { type: "resync" }
  | { type: "revoked" }
  | { type: "end" }
  | { type: "error"; error: string; message: string };

/** The answer envelope of every command. */
export type RemoteAnswer<T> = ({ ok: true } & T) | { ok: false; error: string; message: string };

const encoder = new TextEncoder();

function hex(bytes: ArrayBuffer | Uint8Array): string {
  return Array.from(bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("");
}

function subtle(): SubtleCrypto {
  const crypto = globalThis.crypto;
  if (!crypto?.subtle) {
    throw new Error("当前浏览器不支持 WebCrypto（需要 HTTPS 页面）");
  }
  return crypto.subtle;
}

/**
 * The string a `message.send` is signed over. Must equal
 * SignedSendVerifier.Canonical on the assistant.
 */
export async function canonicalSend(
  pairingId: number,
  threadId: string,
  mode: "queue" | "insert",
  text: string,
  timestampMs: number,
  nonce: string,
): Promise<string> {
  const textHash = hex(await subtle().digest("SHA-256", encoder.encode(text)));
  return [
    "cofly-remote/1",
    "message.send",
    String(pairingId),
    threadId,
    mode,
    textHash,
    String(timestampMs),
    nonce,
  ].join("\n");
}

/**
 * Six hex digits of SHA-256 over the base64 public key *string*, grouped as "XXX XXX".
 * Must equal DesktopSyncLink.Fingerprint on the assistant.
 */
export async function fingerprint(publicKeyBase64: string): Promise<string> {
  const digest = hex(await subtle().digest("SHA-256", encoder.encode(publicKeyBase64)))
    .slice(0, 6)
    .toUpperCase();
  return `${digest.slice(0, 3)} ${digest.slice(3)}`;
}

export function base64FromBytes(bytes: ArrayBuffer | Uint8Array): string {
  const view = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  let binary = "";
  for (const byte of view) binary += String.fromCharCode(byte);
  return btoa(binary);
}

/** 18 random bytes as base64: 24 characters, within the assistant's 16–128. */
export function newNonce(): string {
  return base64FromBytes(globalThis.crypto.getRandomValues(new Uint8Array(18)));
}

/** A fresh signing key. The private half never leaves the device (non-extractable). */
export async function generateSigningKey(): Promise<{ keyPair: CryptoKeyPair; publicKey: string }> {
  const keyPair = (await subtle().generateKey({ name: "ECDSA", namedCurve: "P-256" }, false, [
    "sign",
    "verify",
  ])) as CryptoKeyPair;
  const spki = await subtle().exportKey("spki", keyPair.publicKey);
  return { keyPair, publicKey: base64FromBytes(spki) };
}

/** Signs a send exactly as the assistant verifies it: ECDSA P-256 / SHA-256, raw r‖s. */
export async function signSend(
  privateKey: CryptoKey,
  pairingId: number,
  threadId: string,
  mode: "queue" | "insert",
  text: string,
  now: number = Date.now(),
): Promise<{ ts: number; nonce: string; sig: string }> {
  const nonce = newNonce();
  const message = await canonicalSend(pairingId, threadId, mode, text, now, nonce);
  const signature = await subtle().sign(
    { name: "ECDSA", hash: "SHA-256" },
    privateKey,
    encoder.encode(message),
  );
  return { ts: now, nonce, sig: base64FromBytes(signature) };
}

// ---- Reading what the assistant sends ------------------------------------------------

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function str(value: unknown): string | null {
  return typeof value === "string" ? value : null;
}

function num(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function permission(value: unknown): RemotePermission {
  // Anything unexpected is shown as full access: calling a full-access conversation
  // safe is the one mistake that matters.
  return value === "auto" || value === "sandboxed" ? value : "full_access";
}

export function readItem(value: unknown): SyncItem | null {
  if (!isRecord(value) || typeof value.item_id !== "string" || typeof value.kind !== "string") {
    return null;
  }
  const item: SyncItem = {
    seq: num(value.seq) ?? 0,
    turnId: str(value.turn_id),
    itemId: value.item_id,
    kind: value.kind as SyncItemKind,
    text: str(value.text),
  };
  if (value.origin === "desktop" || value.origin === "phone" || value.origin === "delegated") item.origin = value.origin;
  if (value.phase_missing === true) item.phaseMissing = true;
  if (num(value.image_count) !== undefined) item.imageCount = num(value.image_count);
  if ("command" in value) item.command = str(value.command);
  if (num(value.exit_code) !== undefined) item.exitCode = num(value.exit_code);
  if ("status" in value) item.status = str(value.status);
  if (num(value.duration_ms) !== undefined) item.durationMs = num(value.duration_ms);
  if ("output_preview" in value) item.outputPreview = str(value.output_preview);
  if (value.output_truncated === true) item.outputTruncated = true;
  if (Array.isArray(value.files)) {
    item.files = value.files.filter(isRecord).map((f) => ({
      path: str(f.path) ?? "",
      change: str(f.change) ?? "update",
      added: num(f.added) ?? 0,
      removed: num(f.removed) ?? 0,
    }));
  }
  if (value.outcome === "completed" || value.outcome === "failed" || value.outcome === "aborted") item.outcome = value.outcome;
  return item;
}

export function readItems(value: unknown): SyncItem[] {
  return Array.isArray(value) ? value.map(readItem).filter((i): i is SyncItem => i !== null) : [];
}

export function readHeader(value: unknown): RemoteSessionHeader | null {
  if (!isRecord(value) || typeof value.thread_id !== "string") return null;
  return {
    threadId: value.thread_id,
    title: str(value.title),
    cwd: str(value.cwd),
    model: str(value.model),
    permission: permission(value.permission),
    openTurnId: str(value.open_turn_id),
  };
}

export function readSessionSummary(value: unknown): RemoteSessionSummary | null {
  if (!isRecord(value) || typeof value.thread_id !== "string") return null;
  return {
    threadId: value.thread_id,
    title: str(value.title) ?? "（无标题）",
    cwd: str(value.cwd),
    status: str(value.status) ?? "unknown",
    permission: permission(value.permission),
    updatedAt: num(value.updated_at) ?? null,
  };
}

/** One SSE `data:` payload of the follow stream; `event: end` frames are passed as "end". */
export function readStreamEvent(frame: string): RemoteStreamEvent | null {
  const lines = frame.split("\n");
  if (lines.some((line) => line.trim() === "event: end")) return { type: "end" };
  const data = lines
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice(5).trimStart())
    .join("\n");
  if (!data) return null;
  let payload: unknown;
  try {
    payload = JSON.parse(data);
  } catch {
    return null;
  }
  if (!isRecord(payload)) return null;
  if (payload.ok === false) {
    return { type: "error", error: str(payload.error) ?? "error", message: str(payload.message) ?? "" };
  }
  switch (payload.type) {
    case "items":
      return { type: "items", items: readItems(payload.items), cursor: str(payload.cursor) ?? "" };
    case "status":
      return { type: "status", status: str(payload.status) ?? "unknown", waitingOnApproval: payload.waiting_on_approval === true };
    case "resync":
    case "revoked":
      return { type: payload.type };
    default:
      return null;
  }
}

/**
 * Folds new items into a conversation. Items are unique by seq (their byte offset
 * in the desktop's rollout); a "running" card is dropped once anything later arrives
 * in the same turn.
 */
export function mergeItems(current: SyncItem[], incoming: SyncItem[]): SyncItem[] {
  const bySeq = new Map<number, SyncItem>();
  for (const item of current) bySeq.set(item.seq, item);
  for (const item of incoming) bySeq.set(item.seq, item);
  const ordered = Array.from(bySeq.values()).sort((a, b) => a.seq - b.seq);

  const lastSeqInTurn = new Map<string, number>();
  for (const item of ordered) {
    if (item.turnId) lastSeqInTurn.set(item.turnId, item.seq);
  }
  return ordered.filter(
    (item) => item.kind !== "running" || !item.turnId || lastSeqInTurn.get(item.turnId) === item.seq,
  );
}

/**
 * What to show for a conversation. A turn that has ended without a "reply" (the
 * desktop left the answer untagged, as ~7% of its messages are) shows its last
 * message as the reply when that message is one of the untagged ones.
 */
export function presentItems(items: SyncItem[]): SyncItem[] {
  const ended = new Set<string>();
  const answered = new Set<string>();
  const lastMessage = new Map<string, SyncItem>();
  for (const item of items) {
    if (!item.turnId) continue;
    if (item.kind === "turn_ended") ended.add(item.turnId);
    if (item.kind === "reply") answered.add(item.turnId);
    if (item.kind === "progress" || item.kind === "reply") lastMessage.set(item.turnId, item);
  }

  const promoted = new Set<number>();
  for (const [turnId, item] of lastMessage) {
    if (ended.has(turnId) && !answered.has(turnId) && item.kind === "progress" && item.phaseMissing) {
      promoted.add(item.seq);
    }
  }
  return promoted.size === 0
    ? items
    : items.map((item) => (promoted.has(item.seq) ? { ...item, kind: "reply" } : item));
}

/** What a failed turn's error most likely was, from its text alone. */
export type TurnFailureKind = "login" | "rate_limit" | "local_relay" | "upstream";

export function classifyTurnFailure(text: string | null): TurnFailureKind {
  const t = text ?? "";
  // Checked before the loopback address: a 401 from the relay names 127.0.0.1 too.
  if (/\b(401|403)\b|unauthori[sz]ed|forbidden/i.test(t)) return "login";
  if (/\b429\b|rate.?limit|too many requests/i.test(t)) return "rate_limit";
  if (/127\.0\.0\.1|localhost/i.test(t)) return "local_relay";
  return "upstream";
}

export const TURN_FAILURE_HINT: Record<TurnFailureKind, string> = {
  login: "登录或授权失效：请在电脑上确认共飞助手已登录，仍不行再点「修复 ChatGPT 启动」。",
  rate_limit: "请求太频繁或额度受限，稍等一会儿再重发。",
  local_relay: "经电脑上的本机中转时出错，多半是服务端临时故障。可以先做电脑自检，再决定是否重发。",
  upstream: "模型服务出错，可以重发；反复失败请到电脑上查看。",
};

/** The message that started a turn, for sending it again; null when there is none to send. */
export function turnMessage(items: SyncItem[], turnId: string | null): string | null {
  if (!turnId) return null;
  const user = items.find((i) => i.turnId === turnId && i.kind === "user" && i.text?.trim());
  return user?.text ?? null;
}

export const REMOTE_ERROR_TEXT: Record<string, string> = {
  disabled: "电脑上的手机同步已关闭",
  not_approved: "这台手机还没有在电脑上确认",
  not_selected: "这个会话没有在电脑上勾选同步",
  bad_signature: "签名校验失败，请重新配对",
  rate_limited: "发送太频繁，请稍后再试",
  desktop_unavailable: "电脑上的 Codex 桌面版没有运行",
  missing: "找不到这个会话的记录",
  refused: "电脑拒绝了这个请求",
  unconfirmed: "电脑没有回应，这条消息可能已经发出：请先看会话里有没有，再决定是否重发",
};
