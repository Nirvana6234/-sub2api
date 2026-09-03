"use client";

/**
 * 把 agent 能力接进普通的聊天会话，而不是另开一个页面。
 *
 * # 为什么是这个形状
 *
 * 第一版把它做成了切换进去的独立"模式"（一个 agent 按钮，进去之后整个界面换掉）。
 * 用户纠正过：agent 不该是模式，而应该是给某个对话"挂上"一个工作目录——挂上之后
 * 这个对话的发送就走 codex，跟普通对话长得一样（同一个消息列表、同一套气泡渲染），
 * 只是多了工具调用产生的正文。分组/模型复用 composer 已有的选择器，不重复造一套；
 * 沙箱/审批策略是设置，进 `PawSettingsModal`，不进聊天工具条。
 *
 * # 一次只能有一条活的线程
 *
 * `AgentBridge` 自己就是这么设计的（`running: Mutex<Option<Running>>`）。这里也不
 * 假装能同时支持多个会话都在跑：`liveConversationId` 记着哪个对话拥有当前这条线程，
 * 别的对话想发就先提示"结束当前 agent 会话"，不会去挤占或者静默失败。
 *
 * # 状态落在哪
 *
 * 目录/沙箱/审批策略是**每个对话的绑定**，只存在这个 hook 里——不进
 * `usePawClient`，那边要保持对 PWA 安全、不认识 Tauri。agent 产生的正文，
 * 通过调用方传入的 `beginTurn/appendDelta/finishTurn/appendNotice` 写回普通的
 * 会话消息列表，复用 Markdown、推理折叠块等现成渲染，不需要一套平行的气泡组件。
 */
import { useCallback, useEffect, useRef, useState } from "react";

import { isTauri } from "./host";
import {
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
} from "./session";
import { loadAgentSettings } from "./settings";

interface AgentBinding {
  cwd: string;
  sandbox: AgentSandbox;
  approvalPolicy: AgentApprovalPolicy;
}

interface TurnHandles {
  userMessage: { id: string };
  assistantMessage: { id: string };
}

export interface UseAgentSessionParams {
  activeConversationId: string | null;
  /** 没有活跃会话时，先建一个再返回它的 id（同步）。 */
  ensureActiveConversationId: () => string;
  groupId: number | null;
  modelId: string;
  relayBaseUrl: string;
  sessionToken: string | null;
  beginTurn: (conversationId: string, text: string) => TurnHandles;
  appendDelta: (
    conversationId: string,
    messageId: string,
    delta: { content?: string; reasoning?: string },
  ) => void;
  finishTurn: (conversationId: string, messageId: string, opts?: { error?: boolean }) => void;
  appendNotice: (conversationId: string, text: string) => void;
}

export interface AgentSessionApi {
  /** 只在桌面端为 true；PWA 里这一整套形同虚设。 */
  desktop: boolean;
  /** 当前显示的这个对话是不是挂了工作目录。 */
  armed: boolean;
  /** 挂的是哪个目录（未挂时为 null）。 */
  cwd: string | null;
  /** 正在起会话/结束会话（区别于某一轮是否在跑）。 */
  busy: boolean;
  /** 当前对话有一轮 agent 正在跑。 */
  sending: boolean;
  approvals: ApprovalRequest[];
  waitingOnApproval: boolean;
  error: string | null;
  pickDirectory: () => Promise<void>;
  changeDirectory: () => Promise<void>;
  endSession: () => Promise<void>;
  send: (text: string) => Promise<void>;
  interruptTurn: () => Promise<void>;
  answer: (requestId: string, approve: boolean) => Promise<void>;
}

export function useAgentSession(params: UseAgentSessionParams): AgentSessionApi {
  const {
    activeConversationId,
    ensureActiveConversationId,
    groupId,
    modelId,
    relayBaseUrl,
    sessionToken,
    beginTurn,
    appendDelta,
    finishTurn,
    appendNotice,
  } = params;

  const [desktop, setDesktop] = useState(false);
  const [bindings, setBindings] = useState<Record<string, AgentBinding>>({});
  const [liveConversationId, setLiveConversationId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [sendingTurn, setSendingTurn] = useState(false);
  const [approvals, setApprovals] = useState<ApprovalRequest[]>([]);
  const [waitingOnApproval, setWaitingOnApproval] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const liveThreadIdRef = useRef<string | null>(null);
  const pendingAssistantRef = useRef<{ conversationId: string; messageId: string } | null>(null);
  // 命令输出是一段一段到的；攒够一整段再落一次笔，免得把逐字节碎片
  // 撒进消息内容里，变成一堆读不成句的东西。按 itemId 分桶，item 结束时冲掉。
  const commandBuffersRef = useRef<Map<string, string>>(new Map());

  useEffect(() => {
    setDesktop(isTauri());
  }, []);

  const flushCommandBuffer = useCallback((itemId: string | null | undefined) => {
    if (!itemId) return;
    const buffered = commandBuffersRef.current.get(itemId);
    if (!buffered) return;
    commandBuffersRef.current.delete(itemId);
    const pending = pendingAssistantRef.current;
    if (!pending) return;
    appendDelta(pending.conversationId, pending.messageId, {
      content: `\n\n\`\`\`\n${buffered}\n\`\`\`\n`,
    });
  }, [appendDelta]);

  useEffect(() => {
    if (!desktop) return;
    let cancelled = false;
    let unsubscribe: (() => void) | null = null;

    const onEvent = (event: AgentEvent) => {
      const pending = pendingAssistantRef.current;
      switch (event.type) {
        case "turnStarted":
          setSendingTurn(true);
          break;
        case "agentText":
          if (pending) appendDelta(pending.conversationId, pending.messageId, { content: event.delta });
          break;
        case "reasoning":
          if (pending) appendDelta(pending.conversationId, pending.messageId, { reasoning: event.delta });
          break;
        case "commandOutput": {
          const key = event.itemId ?? "_";
          commandBuffersRef.current.set(key, (commandBuffersRef.current.get(key) ?? "") + event.chunk);
          break;
        }
        case "item":
          // 只在命令执行**结束**（有 status 了）时冲缓冲区，避免命令还在跑
          // 就把半截输出当成定论落进正文。
          if (event.itemType === "commandExecution" && event.status) {
            flushCommandBuffer(event.itemId);
          }
          break;
        case "status":
          setWaitingOnApproval(event.waitingOnApproval);
          break;
        case "approvalRequested":
          setApprovals((prev) => [...prev, event as unknown as ApprovalRequest]);
          break;
        case "approvalResolved":
          // 另一端答过了 —— 队列里这条要消失，否则两处会各批一次。
          setApprovals((prev) => prev.filter((a) => a.requestId !== event.requestId));
          break;
        case "retrying":
          // **不是终态**：codex 会自己重试，别把它当失败呈现。
          if (liveConversationId) appendNotice(liveConversationId, `_正在重试：${event.message}_`);
          break;
        case "warning":
          // codex 认识的一句话提醒（比如模型元数据缺失的降级提示）——
          // 和下面的 passthrough/decodeError 不同，这条**没有**"协议可能漂了"的意味。
          if (liveConversationId) appendNotice(liveConversationId, `⚠️ ${event.message}`);
          break;
        case "failed":
          setSendingTurn(false);
          if (pending) finishTurn(pending.conversationId, pending.messageId, { error: true });
          if (liveConversationId) appendNotice(liveConversationId, `失败：${event.message}`);
          pendingAssistantRef.current = null;
          break;
        case "turnCompleted":
          setSendingTurn(false);
          if (pending && event.interrupted) {
            appendNotice(pending.conversationId, "_已停止。_");
          }
          pendingAssistantRef.current = null;
          break;
        case "engineStopped":
          setSendingTurn(false);
          setBusy(false);
          setApprovals([]);
          setWaitingOnApproval(false);
          if (liveConversationId) appendNotice(liveConversationId, `会话已结束：${event.reason}`);
          setLiveConversationId(null);
          liveThreadIdRef.current = null;
          pendingAssistantRef.current = null;
          break;
        case "passthrough":
          // 诊断：**必须能看见**（协议漂了要有人发现），但只在有活跃对话时才有地方放。
          if (liveConversationId) {
            appendNotice(liveConversationId, `⚠️ 未识别的上游通知：${event.method}`);
          }
          break;
        case "decodeError":
          if (liveConversationId) {
            appendNotice(liveConversationId, `⚠️ 协议解不开的一行：${event.error}`);
          }
          break;
        default:
          break;
      }
    };

    void subscribeToAgent(onEvent).then((un) => {
      if (cancelled) un();
      else unsubscribe = un;
    });

    return () => {
      cancelled = true;
      unsubscribe?.();
    };
  }, [desktop, liveConversationId, appendDelta, appendNotice, finishTurn, flushCommandBuffer]);

  const armed = Boolean(activeConversationId && bindings[activeConversationId]);
  const cwd = (activeConversationId && bindings[activeConversationId]?.cwd) || null;

  const pickForConversation = useCallback(async (conversationId: string) => {
    setError(null);
    try {
      const { open } = await import("@tauri-apps/plugin-dialog");
      const picked = await open({ directory: true, multiple: false, title: "选择 agent 的工作目录" });
      if (typeof picked !== "string") return;
      const settings = loadAgentSettings();
      setBindings((prev) => ({
        ...prev,
        [conversationId]: { cwd: picked, sandbox: settings.sandbox, approvalPolicy: settings.approvalPolicy },
      }));
    } catch (e) {
      setError(`打不开目录选择器：${String(e)}`);
    }
  }, []);

  const pickDirectory = useCallback(async () => {
    if (!desktop) return;
    const conversationId = activeConversationId ?? ensureActiveConversationId();
    await pickForConversation(conversationId);
  }, [desktop, activeConversationId, ensureActiveConversationId, pickForConversation]);

  const endSession = useCallback(async () => {
    const conversationId = activeConversationId;
    if (!conversationId) return;
    setError(null);
    if (liveConversationId === conversationId) {
      setBusy(true);
      try {
        await stopAgent();
      } catch (e) {
        setError(String(e));
      } finally {
        setBusy(false);
      }
      setLiveConversationId(null);
      liveThreadIdRef.current = null;
      setApprovals([]);
      setWaitingOnApproval(false);
      setSendingTurn(false);
      pendingAssistantRef.current = null;
    }
    setBindings((prev) => {
      const next = { ...prev };
      delete next[conversationId];
      return next;
    });
  }, [activeConversationId, liveConversationId]);

  const changeDirectory = useCallback(async () => {
    const conversationId = activeConversationId;
    if (!conversationId) return;
    if (liveConversationId === conversationId) {
      await endSession();
    }
    await pickForConversation(conversationId);
  }, [activeConversationId, liveConversationId, endSession, pickForConversation]);

  const send = useCallback(
    async (text: string) => {
      if (!desktop) return;
      const conversationId = activeConversationId;
      if (!conversationId) return;
      const binding = bindings[conversationId];
      if (!binding) {
        setError("先选择工作目录。");
        return;
      }
      if (liveConversationId && liveConversationId !== conversationId) {
        setError("先结束当前 agent 会话，才能在这个对话里用 agent。");
        return;
      }
      if (groupId == null || !modelId) {
        setError("分组或模型还没选好。");
        return;
      }
      if (!sessionToken) {
        setError("还没登录 —— agent 要用账号会话去中转站取额度。");
        return;
      }
      setError(null);

      const { assistantMessage } = beginTurn(conversationId, text);
      pendingAssistantRef.current = { conversationId, messageId: assistantMessage.id };

      try {
        if (liveConversationId !== conversationId || !liveThreadIdRef.current) {
          setBusy(true);
          const started = await startAgent({
            relayBaseUrl,
            groupId,
            sessionToken,
            // 必须是登录那次浏览器请求的 UA——转发层靠它冒充回浏览器身份，
            // 否则后端的会话指纹校验会把每一轮都当成"换了网络环境"直接 401。
            clientUserAgent: navigator.userAgent,
            model: modelId,
            cwd: binding.cwd,
            sandbox: binding.sandbox,
            approvalPolicy: binding.approvalPolicy,
          });
          liveThreadIdRef.current = started.threadId;
          setLiveConversationId(conversationId);
          setBusy(false);
        }
        setSendingTurn(true);
        await sendToAgent(text);
      } catch (e) {
        setBusy(false);
        setSendingTurn(false);
        finishTurn(conversationId, assistantMessage.id, { error: true });
        appendDelta(conversationId, assistantMessage.id, { content: String(e) });
        pendingAssistantRef.current = null;
      }
    },
    [
      desktop,
      activeConversationId,
      bindings,
      liveConversationId,
      groupId,
      modelId,
      sessionToken,
      relayBaseUrl,
      beginTurn,
      finishTurn,
      appendDelta,
    ],
  );

  const interruptTurn = useCallback(async () => {
    try {
      await interruptAgent();
    } catch (e) {
      setError(String(e));
    }
  }, []);

  const answer = useCallback(async (requestId: string, approve: boolean) => {
    // 先从队列里去掉，避免用户重复点；真失败了 answerApproval 会抛，
    // 但请求已经不在了——这条留给下一次事件（比如另一端答过）去对齐。
    setApprovals((prev) => prev.filter((a) => a.requestId !== requestId));
    try {
      await answerApproval(requestId, approve ? "approve" : "decline");
    } catch (e) {
      setError(String(e));
    }
  }, []);

  return {
    desktop,
    armed,
    cwd,
    busy,
    sending: sendingTurn,
    approvals,
    waitingOnApproval,
    error,
    pickDirectory,
    changeDirectory,
    endSession,
    send,
    interruptTurn,
    answer,
  };
}
