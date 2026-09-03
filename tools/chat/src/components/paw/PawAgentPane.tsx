"use client";

/**
 * agent 会话面（A6）。
 *
 * # 只在桌面端出现
 *
 * PWA 和桌面端共用同一份产物，所以这个组件**一定会**被发到浏览器里去。
 * 它靠 `isTauri()` 在运行时把自己关掉，而那个判断只能在 effect 里做 ——
 * 静态导出会预渲染，模块顶层算出来的答案会被烤进产物里。见 `client/agent/host.ts`。
 *
 * # 不复用 PWA 的对话模型
 *
 * `paw.conversations` 是 PWA 的状态：存在 localStorage 里、跨端同步、没有 `cwd`、
 * 没有 threadId、没有审批队列。agent 会话则是 **Rust 持有的**：桥里同一时刻只有
 * 一条，threadId 由 `agent_start` 给出，关掉 Chat 就跟着没了。把两者并进一个数组，
 * PWA 用户会看到一堆点不开的幽灵条目，而那套对账逻辑会变成 bug 的温床。
 *
 * # 这一版**没有**做的事
 *
 * - **审批界面只做到"不卡死"**。A7 才是正经做它的地方：要区分
 *   `command` / `writeStdin`（0.153.0 新增的 `CommandExecutionApprovalKind`）、
 *   要画文件改动的 diff、要把"同意等于什么"说清楚。这里刻意**不提供
 *   `approveForSession`** —— 那等于整个会话把整台机器授出去，在能好好措辞之前不该给。
 * - **不支持 `thread/resume`**。历史列表点一下是"照这个配置再开一条"，
 *   不是"接着上次那条跑"，因为桥还没有 resume 这条路。不假装能做。
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  agentIsRunning,
  answerApproval,
  interruptAgent,
  sendToAgent,
  startAgent,
  stopAgent,
  subscribeToAgent,
  type AgentApprovalPolicy,
  type AgentEvent,
  type AgentSandbox,
  type ApprovalRequest,
} from "../../client/agent/session";
import { isTauri } from "../../client/agent/host";
import type { PawConfigData } from "../../client/paw/types";

/** 转录里的一条。`kind` 决定怎么画。 */
interface Line {
  id: string;
  kind: "user" | "agent" | "reasoning" | "command" | "notice" | "diagnostic";
  text: string;
}

/** 历史会话 —— 只在本机存，供"照这个配置再开一条"用。 */
interface PastSession {
  threadId: string;
  cwd: string;
  groupId: number;
  model: string;
  sandbox: AgentSandbox;
  approvalPolicy: AgentApprovalPolicy;
  startedAt: number;
}

const HISTORY_KEY = "cofly-agent-sessions";
const MAX_HISTORY = 20;

function loadHistory(): PastSession[] {
  try {
    const raw = window.localStorage.getItem(HISTORY_KEY);
    return raw ? (JSON.parse(raw) as PastSession[]) : [];
  } catch {
    // 存储被禁掉/内容坏了都不该让整个面板打不开。
    return [];
  }
}

function saveHistory(list: PastSession[]) {
  try {
    window.localStorage.setItem(HISTORY_KEY, JSON.stringify(list.slice(0, MAX_HISTORY)));
  } catch {
    /* 历史存不下不影响用 */
  }
}

let lineSeq = 0;
function nextLineId() {
  lineSeq += 1;
  return `l${lineSeq}`;
}

export interface PawAgentPaneProps {
  config: PawConfigData | null;
  relayBaseUrl: string;
  sessionToken: string | null;
}

export function PawAgentPane({ config, relayBaseUrl, sessionToken }: PawAgentPaneProps) {
  const [desktop, setDesktop] = useState(false);
  const [running, setRunning] = useState(false);
  const [busy, setBusy] = useState(false);
  const [threadId, setThreadId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [cwd, setCwd] = useState("");
  const [groupId, setGroupId] = useState<number | null>(null);
  const [model, setModel] = useState("");
  const [sandbox, setSandbox] = useState<AgentSandbox>("workspace-write");
  const [approvalPolicy, setApprovalPolicy] = useState<AgentApprovalPolicy>("on-request");

  const [lines, setLines] = useState<Line[]>([]);
  const [approvals, setApprovals] = useState<ApprovalRequest[]>([]);
  const [waiting, setWaiting] = useState(false);
  const [turnActive, setTurnActive] = useState(false);
  const [draft, setDraft] = useState("");
  const [history, setHistory] = useState<PastSession[]>([]);

  // 正文是**逐块到达**的，按 itemId 续到同一行上，否则会变成一行一个字。
  const agentLineByItem = useRef(new Map<string, string>());
  const scrollRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    setDesktop(isTauri());
    setHistory(loadHistory());
  }, []);

  useEffect(() => {
    if (!desktop) return;
    void agentIsRunning().then(setRunning).catch(() => {});
  }, [desktop]);

  const groups = config?.groups ?? [];
  const activeGroup = useMemo(
    () => groups.find((g) => g.id === groupId) ?? null,
    [groups, groupId],
  );

  // 分组/模型都得来自 /api/v1/paw/config —— 后端就是拿那份目录校验的，
  // 从别处来的模型名会让**每一轮**都以 MODEL_UNAVAILABLE 400 掉。
  useEffect(() => {
    if (!config) return;
    setGroupId((prev) => prev ?? config.defaults.group_id ?? groups[0]?.id ?? null);
  }, [config, groups]);

  useEffect(() => {
    if (!activeGroup) return;
    setModel((prev) =>
      activeGroup.models.some((m) => m.id === prev)
        ? prev
        : (config?.defaults.model_id && activeGroup.models.some((m) => m.id === config.defaults.model_id)
            ? config.defaults.model_id
            : activeGroup.models[0]?.id ?? ""),
    );
  }, [activeGroup, config]);

  const pushLine = useCallback((kind: Line["kind"], text: string) => {
    setLines((prev) => [...prev, { id: nextLineId(), kind, text }]);
  }, []);

  const onEvent = useCallback(
    (event: AgentEvent) => {
      switch (event.type) {
        case "threadStarted":
          setThreadId(event.threadId);
          break;
        case "turnStarted":
          setTurnActive(true);
          break;
        case "agentText": {
          const existing = agentLineByItem.current.get(event.itemId);
          if (existing) {
            setLines((prev) =>
              prev.map((l) => (l.id === existing ? { ...l, text: l.text + event.delta } : l)),
            );
          } else {
            const id = nextLineId();
            agentLineByItem.current.set(event.itemId, id);
            setLines((prev) => [...prev, { id, kind: "agent", text: event.delta }]);
          }
          break;
        }
        case "reasoning":
          pushLine("reasoning", event.delta);
          break;
        case "commandOutput":
          pushLine("command", event.chunk);
          break;
        case "status":
          setWaiting(event.waitingOnApproval);
          break;
        case "approvalRequested":
          setApprovals((prev) => [...prev, event as unknown as ApprovalRequest]);
          break;
        case "approvalResolved":
          // 另一端答过了 —— 队列里这条要消失，否则两处会各批一次。
          setApprovals((prev) => prev.filter((a) => a.requestId !== event.requestId));
          break;
        case "retrying":
          // **不是终态**：codex 会自己重试。说成"失败"会把用户吓走。
          pushLine("notice", `正在重试：${event.message}`);
          break;
        case "failed":
          setTurnActive(false);
          pushLine("notice", `失败：${event.message}`);
          break;
        case "turnCompleted":
          setTurnActive(false);
          agentLineByItem.current.clear();
          if (event.interrupted) {
            pushLine("notice", "已停止。");
          }
          break;
        case "engineStopped":
          setRunning(false);
          setTurnActive(false);
          pushLine("notice", `会话已结束：${event.reason}`);
          break;
        case "passthrough":
        case "decodeError":
          // 诊断。**必须能看见**（协议漂了要有人发现），但绝不能画成 agent 的正文。
          pushLine(
            "diagnostic",
            event.type === "passthrough"
              ? `未投影的上游通知：${event.method}`
              : `解不开的一行：${event.error}`,
          );
          break;
        default:
          break;
      }
    },
    [pushLine],
  );

  useEffect(() => {
    if (!desktop) return;
    let stop: (() => void) | null = null;
    let dead = false;
    void subscribeToAgent(onEvent).then((un) => {
      if (dead) un();
      else stop = un;
    });
    return () => {
      dead = true;
      stop?.();
    };
  }, [desktop, onEvent]);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [lines]);

  const pickDirectory = useCallback(async () => {
    setError(null);
    try {
      const { open } = await import("@tauri-apps/plugin-dialog");
      const picked = await open({ directory: true, multiple: false, title: "选择 agent 的工作目录" });
      if (typeof picked === "string") setCwd(picked);
    } catch (e) {
      setError(`打不开目录选择器：${String(e)}`);
    }
  }, []);

  const start = useCallback(async () => {
    if (!sessionToken) {
      setError("还没登录 —— agent 要用账号会话去中转站取额度。");
      return;
    }
    if (!cwd) {
      setError("先选一个工作目录。");
      return;
    }
    if (groupId == null || !model) {
      setError("分组或模型还没选好。");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const started = await startAgent({
        relayBaseUrl,
        groupId,
        sessionToken,
        model,
        cwd,
        sandbox,
        approvalPolicy,
      });
      setRunning(true);
      setLines([]);
      agentLineByItem.current.clear();
      setThreadId(started.threadId);
      if (started.attempts > 1) {
        pushLine("notice", `起了 ${started.attempts} 次才成功。`);
      }
      const entry: PastSession = {
        threadId: started.threadId,
        cwd,
        groupId,
        model,
        sandbox,
        approvalPolicy,
        startedAt: Date.now(),
      };
      setHistory((prev) => {
        const next = [entry, ...prev.filter((p) => p.threadId !== entry.threadId)];
        saveHistory(next);
        return next;
      });
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }, [approvalPolicy, cwd, groupId, model, pushLine, relayBaseUrl, sandbox, sessionToken]);

  const send = useCallback(async () => {
    const text = draft.trim();
    if (!text) return;
    setDraft("");
    pushLine("user", text);
    try {
      await sendToAgent(text);
    } catch (e) {
      setError(String(e));
    }
  }, [draft, pushLine]);

  const answer = useCallback(async (requestId: string, approve: boolean) => {
    // 先从队列里去掉，避免重复点。真失败了再放回去。
    setApprovals((prev) => prev.filter((a) => a.requestId !== requestId));
    try {
      await answerApproval(requestId, approve ? "approve" : "decline");
    } catch (e) {
      setError(String(e));
    }
  }, []);

  const stop = useCallback(async () => {
    setBusy(true);
    try {
      await stopAgent();
      setRunning(false);
      setTurnActive(false);
      setApprovals([]);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }, []);

  if (!desktop) return null;

  return (
    <section className="paw-agent" aria-label="agent 会话">
      {error ? <p className="paw-agent-error" role="alert">{error}</p> : null}

      {!running ? (
        <div className="paw-agent-setup">
          <div className="paw-agent-row">
            <button type="button" onClick={() => void pickDirectory()}>选择工作目录…</button>
            <code className="paw-agent-cwd">{cwd || "（未选择）"}</code>
          </div>

          <label>
            分组
            <select
              value={groupId ?? ""}
              onChange={(e) => setGroupId(Number(e.target.value))}
            >
              {groups.map((g) => (
                <option key={g.id} value={g.id}>{g.name}</option>
              ))}
            </select>
          </label>

          <label>
            模型
            <select value={model} onChange={(e) => setModel(e.target.value)}>
              {(activeGroup?.models ?? []).map((m) => (
                <option key={m.id} value={m.id}>{m.name || m.id}</option>
              ))}
            </select>
          </label>

          <label>
            沙箱
            <select value={sandbox} onChange={(e) => setSandbox(e.target.value as AgentSandbox)}>
              <option value="read-only">只读</option>
              <option value="workspace-write">可写工作目录</option>
              <option value="danger-full-access">不设限</option>
            </select>
          </label>

          <label>
            审批
            <select
              value={approvalPolicy}
              onChange={(e) => setApprovalPolicy(e.target.value as AgentApprovalPolicy)}
            >
              <option value="on-request">按需询问</option>
              <option value="untrusted">只放行可信命令</option>
              <option value="never">从不询问</option>
            </select>
          </label>

          {/* 这句不是免责声明，是实测结论：批准之后命令完全脱离沙箱。 */}
          <p className="paw-agent-hint">
            沙箱只约束<strong>不经审批就跑</strong>的命令。一旦你点同意，那条命令就带着
            本程序的全部权限运行，工作目录挡不住它。
          </p>

          <button type="button" disabled={busy} onClick={() => void start()}>
            {busy ? "启动中…" : "开始"}
          </button>

          {history.length ? (
            <div className="paw-agent-history">
              <h3>最近的会话</h3>
              <ul>
                {history.map((h) => (
                  <li key={h.threadId}>
                    <button
                      type="button"
                      title="照这个配置再开一条（不是接着上次那条跑）"
                      onClick={() => {
                        setCwd(h.cwd);
                        setGroupId(h.groupId);
                        setModel(h.model);
                        setSandbox(h.sandbox);
                        setApprovalPolicy(h.approvalPolicy);
                      }}
                    >
                      <code>{h.cwd}</code>
                      <span>{new Date(h.startedAt).toLocaleString()}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      ) : (
        <div className="paw-agent-live">
          <header className="paw-agent-bar">
            <code>{cwd}</code>
            {threadId ? <small>{threadId}</small> : null}
            <button type="button" disabled={!turnActive} onClick={() => void interruptAgent()}>
              停止本轮
            </button>
            <button type="button" disabled={busy} onClick={() => void stop()}>
              结束会话
            </button>
          </header>

          {waiting ? <p className="paw-agent-waiting">正在等待你的批准…</p> : null}

          {approvals.map((a) => (
            <div key={a.requestId} className="paw-agent-approval">
              <p>{a.reason ?? "agent 要求执行一个操作"}</p>
              {a.command ? <pre>{a.command}</pre> : null}
              {a.grantRoot ? (
                <p><strong>这不是一次性放行</strong>：它要的是 {a.grantRoot} 这个目录的长期写权限。</p>
              ) : null}
              <button type="button" onClick={() => void answer(a.requestId, true)}>同意</button>
              <button type="button" onClick={() => void answer(a.requestId, false)}>拒绝</button>
            </div>
          ))}

          <div className="paw-agent-transcript" ref={scrollRef}>
            {lines.map((l) => (
              <p key={l.id} className={`paw-agent-line paw-agent-${l.kind}`}>{l.text}</p>
            ))}
          </div>

          <div className="paw-agent-compose">
            <textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder="让 agent 做点什么…"
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  void send();
                }
              }}
            />
            <button type="button" onClick={() => void send()}>发送</button>
          </div>
        </div>
      )}
    </section>
  );
}

export default PawAgentPane;
