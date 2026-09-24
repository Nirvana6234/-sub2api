"use client";

// 「电脑」: pairing with computers, and one conversation a paired computer shares.
//
// The shared conversations themselves are listed in the sidebar group
// (PawRemoteSidebar). What may be reached is decided on the computer (its 「同步会话」
// tab); these pages only show what the computer answers and say plainly when it refuses.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  RemoteError,
  claimPairing,
  followSession,
  listDevices,
  loadDetail,
  loadHistory,
  navigateOnComputer,
  openSession,
  refreshPairingStatus,
  revokePairing,
  sendMessage,
} from "../../client/remote/api";
import { mergeItems, type RemoteDevice, type RemoteSessionHeader, type SyncItem } from "../../client/remote/protocol";
import { deleteSession, loadSession, saveSession, type StoredPairing } from "../../client/remote/store";
import { PawMarkdown } from "./PawMarkdown";
import { PawModal } from "./PawModal";

interface PawRemotePageProps {
  onClose: () => void;
  /** A computer was paired or unpaired: the sidebar group reloads. */
  onChanged: () => void;
}

const PERMISSION_TEXT: Record<string, string> = {
  full_access: "完全访问 · 指令会直接执行",
  auto: "自动 · 越界操作需在电脑上确认",
  sandboxed: "沙箱 · 越界操作会失败",
};

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : "出错了";
}

export function PawRemotePage({ onClose, onChanged }: PawRemotePageProps) {
  const [devices, setDevices] = useState<Array<{ pairing: StoredPairing; device: RemoteDevice | null }>>([]);
  const onChangedRef = useRef(onChanged);
  onChangedRef.current = onChanged;

  const reload = useCallback(async () => {
    setDevices(await listDevices());
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const changed = useCallback(async () => {
    await reload();
    onChangedRef.current();
  }, [reload]);

  return (
    <main className="paw-account-page paw-remote-page">
      <header className="paw-account-page-head">
        <div>
          <h1>电脑</h1>
          <p>配对电脑后，它共享的 Codex 会话会列在左侧「电脑 · 同步会话」里</p>
        </div>
        <button type="button" className="paw-button" onClick={onClose}>
          返回对话
        </button>
      </header>
      <div className="paw-account-page-scroll">
        <div className="paw-remote-content">
          <RemoteDevices devices={devices} onChanged={changed} />
        </div>
      </div>
    </main>
  );
}

/**
 * One shared conversation in the main pane, opened from the sidebar group. Same look as
 * the full page; 返回对话 goes back to this app's own conversations.
 */
export function PawRemoteSessionPage({
  pairing,
  threadId,
  onClose,
  onRevoked,
}: {
  pairing: StoredPairing;
  threadId: string;
  onClose: () => void;
  onRevoked: (message: string) => void;
}) {
  return (
    <main className="paw-account-page paw-remote-page">
      <header className="paw-account-page-head">
        <div>
          <h1>{pairing.deviceName}</h1>
          <p>电脑上的 Codex 会话</p>
        </div>
        <button type="button" className="paw-button" onClick={onClose}>
          返回对话
        </button>
      </header>
      <div className="paw-account-page-scroll">
        <div className="paw-remote-content">
          <RemoteConversation key={`${pairing.deviceId}|${threadId}`} pairing={pairing} threadId={threadId} onRevoked={onRevoked} />
        </div>
      </div>
    </main>
  );
}

// ---- Computers and pairing -----------------------------------------------------------

function RemoteDevices({
  devices,
  onChanged,
}: {
  devices: Array<{ pairing: StoredPairing; device: RemoteDevice | null }>;
  onChanged: () => Promise<void>;
}) {
  const [forgetting, setForgetting] = useState<StoredPairing | null>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pending = devices.filter((d) => d.pairing.status === "claimed");
  const pendingRef = useRef(pending);
  pendingRef.current = pending;
  const pendingKey = pending.map((d) => d.pairing.deviceId).join(",");

  // A claimed pairing waits for the user to confirm it on the computer.
  useEffect(() => {
    if (!pendingKey) return;
    const timer = window.setInterval(async () => {
      for (const { pairing } of pendingRef.current) {
        try {
          const updated = await refreshPairingStatus(pairing);
          if (!updated || updated.status !== pairing.status) await onChanged();
        } catch {
          // Try again on the next tick.
        }
      }
    }, 2000);
    return () => window.clearInterval(timer);
  }, [pendingKey, onChanged]);

  const claim = async () => {
    setBusy(true);
    setError(null);
    try {
      await claimPairing(code);
      setCode("");
      await onChanged();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <section className="paw-account-section">
        <h3 className="paw-remote-heading">我的电脑</h3>
        {devices.filter((d) => d.pairing.status === "active").length === 0 ? (
          <p className="paw-remote-muted">还没有配对的电脑。</p>
        ) : null}
        {devices
          .filter((d) => d.pairing.status === "active")
          .map(({ pairing, device }) => (
            <div key={pairing.deviceId} className="paw-remote-row">
              <span className={`paw-remote-dot ${device?.online ? "on" : ""}`} />
              <span className="paw-remote-row-main">
                <strong>{pairing.deviceName}</strong>
                <small>
                  <span className="paw-remote-paired">已配对</span>
                  {!device?.online
                    ? "电脑不在线"
                    : device.desktopRunning
                      ? "在线 · Codex 桌面版已打开"
                      : "在线 · Codex 桌面版未打开"}
                </small>
              </span>
              <button type="button" className="paw-button" onClick={() => setForgetting(pairing)}>
                解除配对
              </button>
            </div>
          ))}
      </section>

      {pending.map(({ pairing }) => (
        <section key={pairing.deviceId} className="paw-account-section paw-remote-pending">
          <h3 className="paw-remote-heading">等待电脑确认</h3>
          <p>
            请到电脑上的共飞助手确认配对，并核对两边的指纹一致：
          </p>
          <div className="paw-remote-fingerprint">{pairing.fingerprint}</div>
          <p className="paw-remote-muted">不一致就在电脑上点拒绝。确认后这里会自动更新。</p>
          <button type="button" className="paw-button" onClick={async () => { await revokePairing(pairing); await onChanged(); }}>
            取消
          </button>
        </section>
      ))}

      <section className="paw-account-section">
        <h3 className="paw-remote-heading">配对新电脑</h3>
        <p className="paw-remote-muted">
          在电脑上的共飞助手打开「同步会话」，点「配对新手机」，把显示的 6 位数字填在这里。
        </p>
        <div className="paw-remote-claim">
          <input
            className="paw-input"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={7}
            placeholder="6 位配对码"
            value={code}
            onChange={(event) => setCode(event.target.value)}
          />
          <button type="button" className="paw-button primary" disabled={busy || code.replace(/\s/g, "").length !== 6} onClick={claim}>
            {busy ? "提交中…" : "配对"}
          </button>
        </div>
        {error ? <p className="paw-remote-error">{error}</p> : null}
      </section>

      {forgetting ? (
        <PawModal
          title="解除配对"
          onClose={() => setForgetting(null)}
          actions={
            <>
              <button type="button" className="paw-button" onClick={() => setForgetting(null)}>取消</button>
              <button
                type="button"
                className="paw-button primary"
                onClick={async () => {
                  const pairing = forgetting;
                  setForgetting(null);
                  await revokePairing(pairing);
                  await onChanged();
                }}
              >
                解除
              </button>
            </>
          }
        >
          <p>解除后这台手机不能再访问「{forgetting.deviceName}」，本机缓存的会话记录也会删除。</p>
        </PawModal>
      ) : null}
    </>
  );
}

// ---- One conversation ----------------------------------------------------------------

function useTicker(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active]);
  return now;
}

function RemoteConversation({
  pairing,
  threadId,
  onRevoked,
}: {
  pairing: StoredPairing;
  threadId: string;
  onRevoked: (message: string) => void;
}) {
  const [header, setHeader] = useState<RemoteSessionHeader | null>(null);
  const [items, setItems] = useState<SyncItem[]>([]);
  const [hasOlder, setHasOlder] = useState(false);
  const [waitingOnApproval, setWaitingOnApproval] = useState(false);
  const [streamError, setStreamError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState<string | null>(null);
  const [detail, setDetail] = useState<{ title: string; text: string | null; image: string | null; truncated: boolean } | null>(null);
  const [runningSince, setRunningSince] = useState<number | null>(null);
  const cursorRef = useRef("");
  // The source of truth while following: React state updaters run later, not when
  // called, so reading back from setItems would see stale items.
  const itemsRef = useRef<SyncItem[]>([]);
  const headerRef = useRef<RemoteSessionHeader | null>(null);
  const endRef = useRef<HTMLDivElement | null>(null);
  const [generation, setGeneration] = useState(0);
  // Held in a ref: the parent passes a new function each render, and the stream must
  // not be torn down and reopened because of that.
  const onRevokedRef = useRef(onRevoked);
  onRevokedRef.current = onRevoked;

  // Open (cache first, then the computer), then follow until the page closes.
  useEffect(() => {
    const abort = new AbortController();
    let cancelled = false;

    const apply = (next: SyncItem[]) => {
      itemsRef.current = next;
      setItems(next);
      const lastStart = [...next].reverse().find((i) => i.kind === "turn_started" || i.kind === "turn_ended");
      setRunningSince((since) => (lastStart?.kind === "turn_started" ? since ?? Date.now() : null));
      if (lastStart?.kind !== "turn_started") setWaitingOnApproval(false);
    };

    (async () => {
      const cached = await loadSession(pairing.deviceId, threadId);
      if (cached && !cancelled) {
        headerRef.current = cached.header;
        setHeader(cached.header);
        setHasOlder(cached.hasOlder);
        apply(cached.items);
      }
      try {
        const opened = await openSession(pairing, threadId);
        if (cancelled) return;
        headerRef.current = opened.header;
        setHeader(opened.header);
        setHasOlder(opened.hasOlder || opened.truncatedTurnId !== null);
        apply(opened.items);
        cursorRef.current = opened.cursor;
        await saveSession(pairing.deviceId, threadId, { header: opened.header, items: opened.items, cursor: opened.cursor, hasOlder: opened.hasOlder });
        setError(null);
      } catch (err) {
        if (!cancelled) setError(errorText(err));
        if (err instanceof RemoteError && err.code === "not_selected") {
          await deleteSession(pairing.deviceId, threadId);
          onRevokedRef.current("这个会话已在电脑上取消同步。");
        }
        return;
      }

      await followSession(pairing, threadId, cursorRef.current, (event) => {
        switch (event.type) {
          case "items":
            setStreamError(null);
            apply(mergeItems(itemsRef.current, event.items));
            cursorRef.current = event.cursor;
            void saveSession(pairing.deviceId, threadId, {
              header: headerRef.current,
              items: itemsRef.current,
              cursor: event.cursor,
              hasOlder: true,
            });
            break;
          case "status":
            setWaitingOnApproval(event.waitingOnApproval);
            break;
          case "resync":
            setGeneration((g) => g + 1);
            break;
          case "revoked":
            void deleteSession(pairing.deviceId, threadId);
            onRevokedRef.current("这个会话已在电脑上取消同步，或这台手机已被解除配对。");
            break;
          case "error":
            setStreamError(event.message);
            break;
          case "end":
            break;
        }
      }, abort.signal);
    })();

    return () => {
      cancelled = true;
      abort.abort();
    };
  }, [pairing, threadId, generation]);

  useEffect(() => {
    endRef.current?.scrollIntoView({ block: "end" });
  }, [items.length]);

  const now = useTicker(runningSince !== null);
  const running = runningSince !== null;

  const olderTurn = useMemo(() => items.find((i) => i.turnId)?.turnId ?? null, [items]);

  const loadOlder = async () => {
    if (!olderTurn) return;
    try {
      const page = await loadHistory(pairing, threadId, olderTurn);
      itemsRef.current = mergeItems(page.items, itemsRef.current);
      setItems(itemsRef.current);
      setHasOlder(page.hasOlder);
    } catch (err) {
      setError(errorText(err));
    }
  };

  const send = async (mode: "queue" | "insert") => {
    const text = draft.trim();
    if (!text) return;
    setSending(true);
    setError(null);
    try {
      const result = await sendMessage(pairing, threadId, text, mode);
      setDraft("");
      setSent(result.queued ? "已排队：这一轮结束后发送" : "已送达电脑");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setSending(false);
    }
  };

  const openDetail = async (item: SyncItem, part: "output" | "diff" | "image", index = 0) => {
    try {
      const result = await loadDetail(pairing, threadId, item, part, index);
      setDetail({
        title: part === "output" ? item.command ?? "命令输出" : part === "diff" ? "改动" : "图片",
        text: result.text,
        image: result.dataUrl,
        truncated: result.truncated,
      });
    } catch (err) {
      setError(errorText(err));
    }
  };

  return (
    <div className="paw-remote-conversation">
      <div className="paw-remote-toolbar">
        <button type="button" className="paw-button" onClick={() => void navigateOnComputer(pairing, threadId).catch((err) => setError(errorText(err)))}>
          在电脑上打开
        </button>
      </div>

      <section className="paw-account-section paw-remote-head">
        <h3 className="paw-remote-heading">{header?.title ?? "会话"}</h3>
        {header ? (
          <small className={header.permission === "full_access" ? "paw-remote-danger" : "paw-remote-muted"}>
            {PERMISSION_TEXT[header.permission]}
            {header.model ? ` · ${header.model}` : ""}
          </small>
        ) : null}
        <div className={`paw-remote-status ${waitingOnApproval ? "waiting" : running ? "running" : ""}`}>
          {waitingOnApproval
            ? "等待电脑批准：请到电脑上确认"
            : running
              ? `运行中 · 已 ${Math.max(0, Math.round((now - (runningSince ?? now)) / 1000))} 秒`
              : "空闲"}
        </div>
        {streamError ? <p className="paw-remote-warn">连接中断，正在重连：{streamError}</p> : null}
      </section>

      {hasOlder && olderTurn ? (
        <button type="button" className="paw-button paw-remote-older" onClick={() => void loadOlder()}>加载更早的轮次</button>
      ) : null}

      <div className="paw-remote-items">
        {items.map((item) => (
          <RemoteItem key={item.seq} item={item} onDetail={openDetail} />
        ))}
        <div ref={endRef} />
      </div>

      {error ? <p className="paw-remote-error">{error}</p> : null}

      <div className="paw-remote-compose">
        <textarea
          className="paw-input"
          rows={3}
          maxLength={8000}
          placeholder={running ? "这一轮还在跑：发送会排队，结束后再发" : "接着在电脑上的这个会话里说…"}
          value={draft}
          onChange={(event) => {
            setDraft(event.target.value);
            setSent(null);
          }}
        />
        <div className="paw-remote-compose-actions">
          {sent ? <small className="paw-remote-muted">{sent}</small> : <span />}
          <div>
            {running ? (
              <button type="button" className="paw-button" disabled={sending || !draft.trim()} onClick={() => void send("insert")}
                title="插进正在跑的这一轮：模型可能放弃上一条指令没做完的部分">
                插入当前这一轮
              </button>
            ) : null}
            <button type="button" className="paw-button primary" disabled={sending || !draft.trim()} onClick={() => void send("queue")}>
              {sending ? "发送中…" : running ? "排队发送" : "发送"}
            </button>
          </div>
        </div>
      </div>

      {detail ? (
        <PawModal title={detail.title} onClose={() => setDetail(null)}>
          {detail.image ? <img className="paw-remote-image" src={detail.image} alt="图片" /> : null}
          {detail.text !== null ? <pre className="paw-remote-pre">{detail.text}</pre> : null}
          {detail.truncated ? <p className="paw-remote-muted">内容过长，只显示了一部分，完整内容请在电脑上查看。</p> : null}
        </PawModal>
      ) : null}
    </div>
  );
}

function RemoteItem({ item, onDetail }: { item: SyncItem; onDetail: (item: SyncItem, part: "output" | "diff" | "image", index?: number) => void }) {
  switch (item.kind) {
    case "user":
      return (
        <div className="paw-remote-user">
          <div className="paw-remote-bubble">{item.text}</div>
          <small className="paw-remote-muted">
            {item.origin === "phone" ? "来自手机" : item.origin === "delegated" ? "委派消息" : "电脑上输入"}
          </small>
          {Array.from({ length: item.imageCount ?? 0 }, (_, index) => (
            <button key={index} type="button" className="paw-button paw-remote-small" onClick={() => onDetail(item, "image", index)}>
              查看图片 {index + 1}
            </button>
          ))}
        </div>
      );
    case "progress":
      return <p className="paw-remote-progress">{item.text}</p>;
    case "reply":
      return (
        <div className="paw-remote-reply">
          <PawMarkdown content={item.text ?? ""} />
        </div>
      );
    case "thinking":
      return (
        <details className="paw-remote-thinking">
          <summary>思考</summary>
          <p>{item.text}</p>
        </details>
      );
    case "command":
      return (
        <div className={`paw-remote-card ${item.status === "completed" && (item.exitCode ?? 0) === 0 ? "" : "failed"}`}>
          <code>$ {item.command ?? "（脚本）"}</code>
          <small className="paw-remote-muted">
            {item.status === "declined" ? "已拒绝" : item.exitCode !== undefined ? `退出码 ${item.exitCode}` : item.status ?? ""}
            {item.durationMs !== undefined ? ` · ${(item.durationMs / 1000).toFixed(1)} 秒` : ""}
          </small>
          {item.outputPreview ? <pre className="paw-remote-pre">{item.outputPreview}</pre> : null}
          <button type="button" className="paw-button paw-remote-small" onClick={() => onDetail(item, "output")}>查看完整输出</button>
        </div>
      );
    case "file_change":
      return (
        <div className="paw-remote-card">
          {(item.files ?? []).map((file) => (
            <div key={file.path} className="paw-remote-file">
              <span>{file.change === "add" ? "新建" : file.change === "delete" ? "删除" : "修改"} {file.path}</span>
              <small><span className="paw-remote-added">+{file.added}</span> <span className="paw-remote-removed">-{file.removed}</span></small>
            </div>
          ))}
          <button type="button" className="paw-button paw-remote-small" onClick={() => onDetail(item, "diff")}>查看改动</button>
        </div>
      );
    case "tool":
      return <p className="paw-remote-progress">⚙ {item.text}{item.status ? ` · ${item.status}` : ""}</p>;
    case "image":
      return <button type="button" className="paw-button paw-remote-small" onClick={() => onDetail(item, "image")}>查看图片</button>;
    case "running":
      return (
        <div className="paw-remote-card running">
          <span className="paw-remote-spinner" />
          <code>正在执行：{item.command ?? "脚本"}</code>
        </div>
      );
    case "notice":
      return <div className="paw-remote-divider">{item.text}</div>;
    case "turn_started":
      return <div className="paw-remote-divider" />;
    case "turn_ended":
      return item.outcome === "failed" ? (
        <p className="paw-remote-error">这一轮出错：{item.text}</p>
      ) : item.outcome === "aborted" ? (
        <p className="paw-remote-muted">这一轮已中止</p>
      ) : null;
    default:
      return <p className="paw-remote-muted">此类内容请在电脑上查看（{item.text}）</p>;
  }
}
