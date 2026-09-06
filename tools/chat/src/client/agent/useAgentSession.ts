"use client";

/**
 * 把 agent 能力接进普通的聊天会话，而不是另开一个页面。
 *
 * # 为什么是这个形状
 *
 * 用户纠正过两次：agent 不该是切换进去的独立"模式"，而应该是给某个对话"挂上"一个
 * 工作目录——挂上之后这个对话的发送就走 codex，跟普通对话长得一样（同一个消息列表、
 * 同一套气泡渲染），只是多了工具调用产生的正文。
 *
 * # 多会话并发改造：不再只有一条"活的" thread
 *
 * 第一版这里有个 `liveConversationId` 单例，别的对话想发就先提示"结束当前 agent
 * 会话"。那条限制**不是 codex 的限制，是这座桥（当时）自己的简化**——探针实测
 * 证明一个 codex 进程能真的并发跑好几条 thread。现在 Rust 侧的引擎已经改成共享的，
 * 这里对应地把状态从"一个全局槽位"换成**按对话 id 分桶**：每个挂了目录的对话
 * 各自持有自己的 threadId、是否正在发送、待批准队列——互不打扰，可以同时发送。
 *
 * # 工作目录：选目录不算数，起了真正的会话才锁定
 *
 * cwd 和审批模式现在是**对话记录自己的字段**（`PawConversation.agentCwd` /
 * `agentApprovalMode`），跟着 `paw-conversations` 一起落盘，不再是这个 hook 里
 * 一份重启就丢的内存 state。**锁定点不是"选完目录"，是"第一次成功发消息"**——
 * 选完目录、还没发消息之前可以随便重选，改主意的代价是零；一旦 `send()` 真的
 * 起了一条 codex thread（`lockAgentCwd`），才没有"更换目录"这回事了。
 *
 * # 状态落在哪
 *
 * 每个对话的运行时状态（threadId、发送中、待批准）只存在这个 hook 里——不进
 * `usePawClient`，那边要保持对 PWA 安全、不认识 Tauri。agent 产生的正文，
 * 通过调用方传入的 `beginTurn/appendDelta/finishTurn/appendNotice` 写回普通的
 * 会话消息列表，复用 Markdown、推理折叠块等现成渲染，不需要一套平行的气泡组件。
 */
import { useCallback, useEffect, useRef, useState } from "react";

import type {
  PawAgentFileChange,
  PawAgentPlan,
  PawConversation,
} from "@/client/paw/types";
import { isTauri } from "./host";
import {
  answerApproval,
  approvalUiModeToParams,
  compactAgent,
  describeAgentError,
  endAgentThread,
  interruptAgent,
  sendToAgent,
  startAgent,
  subscribeToAgent,
  type AgentApprovalUiMode,
  type AgentEvent,
  type ApprovalRequest,
} from "./session";

interface TurnHandles {
  userMessage: { id: string };
  assistantMessage: { id: string };
}

/** 一个对话的运行时状态——只在这个 hook 里活，重启即丢（不支持 thread/resume）。 */
interface ConversationRuntime {
  threadId: string | null;
  compacting: boolean;
  /** 正在起/发送这一轮（合并了"起 thread"和"轮次真的在跑"两段，UI 只需要一个禁用信号）。 */
  sending: boolean;
  waitingOnApproval: boolean;
  approvals: ApprovalRequest[];
  /**
   * 有几条命令正在跑（`item/started` 到 `item/completed` 之间）。
   *
   * 命令输出要等跑完才落成代码块（见 `flushCommandBuffer`），所以一条耗时命令
   * 执行期间，界面上什么新内容都不会出现——用户会以为卡住了。这个计数器只用来
   * 驱动一句"正在执行命令"的提示，不影响正文怎么拼。
   */
  runningCommands: number;
  /**
   * 正在重试（codex 自己在按它的节奏重连，还没到我们放弃的那一步）。
   * 只存**最新一条**，不追加——上游可能连着推好几条"Reconnecting... N/5"，
   * 全部堆进消息列表会刷屏（真实撞见过：一次网关并发限流连续推了 4+ 条）。
   * 这是一条会被下一条覆盖、会被进展清空的瞬时状态，不是聊天记录的一部分。
   */
  retrying: { message: string } | null;
}

/** 不驱动渲染、纯记账的部分，放 ref 里，避免每条增量都触发一次 React state 更新。 */
interface ConversationBookkeeping {
  pendingAssistant: { messageId: string } | null;
  /** 命令输出一段一段到，攒够一整段再落一次笔，免得把逐字节碎片撒进消息内容里。 */
  commandBuffers: Map<string, string>;
  /** `item/started` 时记下这条命令的原文，flush 输出时配一个能看懂的一行摘要——
   * 光看 itemId 用户什么都看不出来。 */
  commandMeta: Map<string, string>;
  planDelta: string;
  fileChangeOutputs: Map<string, string>;
  fileSearches: Map<string, { query: string; files: unknown[] }>;
}

/** `item.command` 是协议里唯一告诉我们"这条命令到底是什么"的地方——`item` 是
 * `unknown`（前端不认识 codex 协议的具体形状），所以只做最小限度的安全读取，
 * 读不出来就返回 null，绝不编一个假摘要。 */
function readCommandText(item: unknown): string | null {
  if (item && typeof item === "object" && "command" in item) {
    const value = (item as { command?: unknown }).command;
    if (typeof value === "string" && value.trim()) return value;
  }
  return null;
}

function normalizeAgentPlan(
  plan: unknown,
  explanation: string | null,
  delta: string,
): PawAgentPlan {
  const steps =
    Array.isArray(plan)
      ? plan
      : plan &&
          typeof plan === "object" &&
          Array.isArray((plan as { steps?: unknown }).steps)
        ? (plan as { steps: unknown[] }).steps
        : plan == null
          ? []
          : [plan];
  return { explanation, steps, delta: delta || undefined };
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function stringValue(value: unknown): string | null {
  return typeof value === "string" && value.trim() ? value : null;
}

function knownNotificationMessage(method: string, raw: unknown): string {
  const data = asRecord(raw);
  switch (method) {
    case "thread/closed":
      return "会话已关闭";
    case "thread/deleted":
      return "会话已删除";
    case "thread/unarchived":
      return "会话已取消归档";
    case "thread/compacted":
      return "上下文已压缩";
    case "thread/reverted":
      return "会话已回退";
    case "thread/settings/updated":
      return "会话设置已更新";
    case "thread/queue/changed":
      return "会话队列已更新";
    case "thread/name/updated": {
      const name = data.threadName;
      return typeof name === "string" && name.trim()
        ? `会话名称已更新为“${name}”`
        : "会话名称已清除";
    }
    case "thread/project/updated": {
      const projectId = data.projectId;
      return typeof projectId === "string" && projectId
        ? `会话项目已更新：${projectId}`
        : "会话已解除项目关联";
    }
    case "mcpServer/oauthLogin/completed": {
      const name = stringValue(data.name) ?? "MCP 服务";
      const error = stringValue(data.error);
      return data.success === true
        ? `${name} OAuth 登录完成`
        : `${name} OAuth 登录失败${error ? `：${error}` : ""}`;
    }
    case "windowsSandbox/setupCompleted": {
      const error = stringValue(data.error);
      return data.success === true
        ? `Windows 沙箱已完成设置（${String(data.mode ?? "unknown")}）`
        : `Windows 沙箱设置失败${error ? `：${error}` : ""}`;
    }
    case "autoApprovalReview/strictReviewRequired":
      return "自动审批需要严格审核";
    case "item/autoApprovalReview/started":
      return "自动审批审核已开始";
    case "item/autoApprovalReview/completed": {
      const review = asRecord(data.review);
      const status = stringValue(review.status);
      return status ? `自动审批审核已完成：${status}` : "自动审批审核已完成";
    }
    default:
      return method;
  }
}

const EMPTY_RUNTIME: ConversationRuntime = {
  threadId: null,
  compacting: false,
  sending: false,
  waitingOnApproval: false,
  approvals: [],
  runningCommands: 0,
  retrying: null,
};

export interface UseAgentSessionParams {
  activeConversationId: string | null;
  /** 当前显示的这个对话记录——cwd/审批模式从这里读。 */
  activeConversation: PawConversation | null;
  /** 没有活跃会话时，先建一个再返回它的 id（同步）。 */
  ensureActiveConversationId: () => string;
  groupId: number | null;
  modelId: string;
  reasoning: string;
  relayBaseUrl: string;
  sessionToken: string | null;
  /** 设置/重选工作目录——发消息前可以随便调，数据层只在锁定后才挡写入。 */
  setAgentBinding: (conversationId: string, cwd: string) => void;
  /** 锁死当前工作目录——只在真正起了一条会话（第一次成功发消息）之后才调。 */
  lockAgentCwd: (conversationId: string) => void;
  setAgentApprovalMode: (conversationId: string, mode: AgentApprovalUiMode) => void;
  beginTurn: (conversationId: string, text: string) => TurnHandles;
  appendDelta: (
    conversationId: string,
    messageId: string,
    delta: { content?: string; reasoning?: string },
  ) => void;
  updateAgentPanel: (
    conversationId: string,
    messageId: string,
    patch: {
      plan?: PawAgentPlan;
      diff?: string;
      fileChanges?: Record<string, PawAgentFileChange>;
      terminalInteractions?: Array<{
        itemId: string;
        processId: string;
        stdin: string;
        createdAt: number;
      }>;
      moderationMetadata?: unknown[];
      notifications?: Array<{
        method: string;
        message: string;
        raw: unknown;
        createdAt: number;
      }>;
      fileSearches?: Record<
        string,
        {
          sessionId: string;
          query: string;
          files: unknown[];
          completed: boolean;
          updatedAt: number;
        }
      >;
      approvalReviews?: Record<
        string,
        {
          reviewId: string;
          method: string;
          raw: unknown;
          updatedAt: number;
        }
      >;
    },
  ) => void;
  finishTurn: (conversationId: string, messageId: string, opts?: { error?: boolean }) => void;
  appendNotice: (conversationId: string, text: string) => void;
}

export interface AgentSessionApi {
  /** 只在桌面端为 true；PWA 里这一整套形同虚设。 */
  desktop: boolean;
  /** 当前显示的这个对话是不是挂了工作目录（不管锁没锁定）。 */
  armed: boolean;
  /** 挂的是哪个目录（未挂时为 null）。 */
  cwd: string | null;
  /**
   * `cwd` 是不是已经锁死了——真正起过一次会话（成功发过至少一条消息）才会是
   * `true`。锁定前 `pickDirectory` 可以随便重选；锁定后是 no-op。
   */
  cwdLocked: boolean;
  /** 当前显示的这个对话的审批模式；未设置时按"完全控制"处理。 */
  approvalMode: AgentApprovalUiMode;
  /** 当前显示的这个对话是否正在起/发送这一轮（用于禁用输入框）。 */
  busy: boolean;
  /** 和 `busy` 同义，留着是因为 `PawChatPane` 的 props 已经用了这个名字。 */
  sending: boolean;
  /**
   * 有命令正在跑（`item/started` 到 `item/completed` 之间）——命令输出要等跑完
   * 才落进正文，这段时间界面上不会有任何新内容出现，容易被当成卡住了。
   * 给一句"正在执行命令"的提示用。
   */
  runningTool: boolean;
  /** 正在重试——只有最新一条，不是一份历史列表。`null` 表示这个对话此刻
   * 没有卡在重试上（要么在正常处理，要么根本没在跑）。 */
  retrying: { message: string } | null;
  compacting: boolean;
  approvals: ApprovalRequest[];
  waitingOnApproval: boolean;
  error: string | null;
  /** 未挂目录时点它：弹系统目录选择器。**已挂目录时是 no-op**——锁定之后没有入口再改。 */
  pickDirectory: () => Promise<void>;
  /**
   * 切换审批模式。如果这个对话正挂着一条活的 thread，会先归档它（不影响引擎、
   * 不影响别的对话），新模式在下一次发送时随新 thread 生效。
   */
  setApprovalMode: (mode: AgentApprovalUiMode) => Promise<void>;
  send: (text: string) => Promise<void>;
  interruptTurn: () => Promise<void>;
  answer: (requestId: string, approve: boolean) => Promise<void>;
  /**
   * 内部清理用：某个对话要被删除了，把它名下的活 thread（如果有）归档掉。
   * **不是"结束会话"按钮**——没有对应的 UI 入口，只在删除整条对话记录时调用，
   * 删除之后 cwd/审批模式随整条记录一起消失，这里只负责别让 Rust 那侧的 thread
   * 变成孤儿（还占着，但已经没有任何界面引用它）。
   */
  discardConversation: (conversationId: string) => Promise<void>;
  compact: () => Promise<void>;
}

export function useAgentSession(params: UseAgentSessionParams): AgentSessionApi {
  const {
    activeConversationId,
    activeConversation,
    ensureActiveConversationId,
    groupId,
    modelId,
    reasoning,
    relayBaseUrl,
    sessionToken,
    setAgentBinding,
    lockAgentCwd,
    setAgentApprovalMode,
    beginTurn,
    appendDelta,
    updateAgentPanel,
    finishTurn,
    appendNotice,
  } = params;

  const [desktop, setDesktop] = useState(false);
  const [runtimes, setRuntimes] = useState<Record<string, ConversationRuntime>>({});
  const [error, setError] = useState<string | null>(null);

  // `patchRuntime` 的 approvals 更新要读"当前"的 runtimes，但下面那个 effect 的
  // 闭包只会在依赖变化时重建——用一个 ref 镜像最新值，避免用到过期的队列。
  // 必须在下面那个 effect 之前声明：effect 的闭包在这个函数体里捕获它。
  const runtimesRef = useRef(runtimes);
  useEffect(() => {
    runtimesRef.current = runtimes;
  }, [runtimes]);

  // threadId → conversationId：事件流只带 threadId，这张表是唯一的回查路径。
  const threadOwnerRef = useRef<Map<string, string>>(new Map());
  const bookkeepingRef = useRef<Map<string, ConversationBookkeeping>>(new Map());

  // 同样是给事件订阅那个 effect 用的镜像——切对话很频繁，不想每次都重新订阅一次
  // agent 事件流，所以用 ref 而不是把 activeConversationId 放进依赖数组。
  const activeConversationIdRef = useRef(activeConversationId);
  useEffect(() => {
    activeConversationIdRef.current = activeConversationId;
  }, [activeConversationId]);

  useEffect(() => {
    setDesktop(isTauri());
  }, []);

  const patchRuntime = useCallback(
    (conversationId: string, patch: Partial<ConversationRuntime>) => {
      setRuntimes((prev) => ({
        ...prev,
        [conversationId]: { ...(prev[conversationId] ?? EMPTY_RUNTIME), ...patch },
      }));
    },
    [],
  );

  const bookkeepingFor = useCallback((conversationId: string): ConversationBookkeeping => {
    let entry = bookkeepingRef.current.get(conversationId);
    if (!entry) {
      entry = {
        pendingAssistant: null,
        commandBuffers: new Map(),
        commandMeta: new Map(),
        planDelta: "",
        fileChangeOutputs: new Map(),
        fileSearches: new Map(),
      };
      bookkeepingRef.current.set(conversationId, entry);
    }
    return entry;
  }, []);

  const flushCommandBuffer = useCallback(
    (conversationId: string, itemId: string | null | undefined) => {
      if (!itemId) return;
      const book = bookkeepingRef.current.get(conversationId);
      const buffered = book?.commandBuffers.get(itemId);
      const command = book?.commandMeta.get(itemId) ?? null;
      book?.commandMeta.delete(itemId);
      if (!buffered) return;
      book!.commandBuffers.delete(itemId);
      const pending = book!.pendingAssistant;
      if (!pending) return;
      // 语言标成 `agent-output`——不是给它语法高亮，是让 PawMarkdown 认出
      // "这是执行过程，不是正文" 从而默认折叠成一行。第一行放命令原文当摘要，
      // 是我们自己拼的格式，不会跟真实输出的第一行混。
      const label = command ? `$ ${command}` : "命令输出";
      appendDelta(conversationId, pending.messageId, {
        content: `\n\n\`\`\`agent-output\n${label}\n${buffered}\n\`\`\`\n`,
      });
    },
    [appendDelta],
  );

  useEffect(() => {
    if (!desktop) return;
    let cancelled = false;
    let unsubscribe: (() => void) | null = null;

    const onEvent = (event: AgentEvent) => {
      // engineStopped 是唯一没有 threadId 的事件——全局的，广播给所有还挂着
      // thread 的对话。
      if (event.type === "engineStopped") {
        const owners = new Set(threadOwnerRef.current.values());
        threadOwnerRef.current.clear();
        for (const conversationId of owners) {
          const book = bookkeepingRef.current.get(conversationId);
          patchRuntime(conversationId, {
            threadId: null,
            sending: false,
            waitingOnApproval: false,
            approvals: [],
            runningCommands: 0,
            compacting: false,
          });
          if (book?.pendingAssistant) {
            finishTurn(conversationId, book.pendingAssistant.messageId, { error: true });
            book.pendingAssistant = null;
          }
          appendNotice(conversationId, `引擎已停止：${event.reason}`);
        }
        return;
      }

      if (event.type === "threadEnded") {
        const conversationId = threadOwnerRef.current.get(event.threadId);
        threadOwnerRef.current.delete(event.threadId);
        if (conversationId) {
          patchRuntime(conversationId, {
            threadId: null,
            sending: false,
            waitingOnApproval: false,
            approvals: [],
            runningCommands: 0,
            compacting: false,
          });
        }
        return;
      }

      if (event.type === "threadStarted") {
        // 归属在 `startAgent` 的返回值里已经登记过了（见 `send`），这里不用重复处理；
        // 事件仍然可能到达（比如引擎自己重连），但没有归属就没地方放，安静地丢弃。
        return;
      }

      // passthrough/decodeError 是**协议级诊断**，天生就没有 threadId——它们说的是
      // "这一行我们解不开/不认识"，不是某个对话的输出。没有真正的归属可言，
      // 只能挑一个看得见的地方放：当前正显示的那个对话。挑错了也无所谓，
      // 反正不是内容正确性问题，只是"这条提醒该出现在哪个转录里"。
      if (event.type === "passthrough" || event.type === "decodeError") {
        const fallback = activeConversationIdRef.current;
        if (fallback) {
          appendNotice(
            fallback,
            event.type === "passthrough"
              ? `⚠️ 未识别的上游通知：${event.method}`
              : `⚠️ 协议解不开的一行：${event.error}`,
          );
        }
        return;
      }

      // 剩下的事件类型都带 threadId——除了 approvalRequested（归属在
      // ApprovalRequest.threadId 里）。
      const threadId =
        event.type === "approvalRequested"
          ? (event as unknown as ApprovalRequest).threadId
          : event.threadId ?? null;
      const conversationId = threadId
        ? threadOwnerRef.current.get(threadId) ??
          (event.type === "knownNotification" ? activeConversationIdRef.current : undefined)
        : event.type === "warning" || event.type === "knownNotification"
          ? activeConversationIdRef.current
          : undefined;
      if (!conversationId) return; // 挖不出归属，没地方放，只能丢——极少数边界情况。

      const book = bookkeepingFor(conversationId);
      const pending = book.pendingAssistant;

      if (event.type === "turnDiffUpdated") {
        if (pending) {
          updateAgentPanel(conversationId, pending.messageId, { diff: event.diff });
        }
        return;
      }
      if (event.type === "fileChangePatchUpdated") {
        if (pending) {
          updateAgentPanel(conversationId, pending.messageId, {
            fileChanges: {
              [event.itemId]: {
                itemId: event.itemId,
                changes: event.changes,
                output: book.fileChangeOutputs.get(event.itemId),
              },
            },
          });
        }
        return;
      }
      if (event.type === "fileChangeOutputDelta") {
        book.fileChangeOutputs.set(
          event.itemId,
          (book.fileChangeOutputs.get(event.itemId) ?? "") + event.delta,
        );
        if (pending) {
          updateAgentPanel(conversationId, pending.messageId, {
            fileChanges: {
              [event.itemId]: {
                itemId: event.itemId,
                output: book.fileChangeOutputs.get(event.itemId),
              },
            },
          });
        }
        return;
      }
      if (event.type === "planDelta") {
        book.planDelta += event.delta;
        if (pending) {
          updateAgentPanel(conversationId, pending.messageId, {
            plan: normalizeAgentPlan(null, null, book.planDelta),
          });
        }
        return;
      }
      if (event.type === "turnPlanUpdated") {
        if (pending) {
          updateAgentPanel(conversationId, pending.messageId, {
            plan: normalizeAgentPlan(event.plan, event.explanation, book.planDelta),
          });
        }
        return;
      }
      if (event.type === "terminalInteraction") {
        if (pending) {
          updateAgentPanel(conversationId, pending.messageId, {
            terminalInteractions: [
              {
                itemId: event.itemId,
                processId: event.processId,
                stdin: event.stdin,
                createdAt: Date.now(),
              },
            ],
          });
        }
        return;
      }
      if (event.type === "turnModerationMetadata") {
        if (pending) {
          updateAgentPanel(conversationId, pending.messageId, {
            moderationMetadata: [event.metadata],
          });
        }
        return;
      }
      if (event.type === "knownNotification") {
        const data = asRecord(event.raw);
        const now = Date.now();
        const message = knownNotificationMessage(event.method, event.raw);

        if (
          event.method === "fuzzyFileSearch/sessionUpdated" ||
          event.method === "fuzzyFileSearch/sessionCompleted"
        ) {
          const sessionId = stringValue(data.sessionId);
          if (!sessionId) {
            appendNotice(conversationId, `文件搜索通知缺少 sessionId：${event.method}`);
            return;
          }
          const previous = book.fileSearches.get(sessionId);
          const search = {
            query: stringValue(data.query) ?? previous?.query ?? "",
            files: Array.isArray(data.files) ? data.files : (previous?.files ?? []),
          };
          book.fileSearches.set(sessionId, search);
          if (pending) {
            updateAgentPanel(conversationId, pending.messageId, {
              fileSearches: {
                [sessionId]: {
                  sessionId,
                  query: search.query,
                  files: search.files,
                  completed: event.method === "fuzzyFileSearch/sessionCompleted",
                  updatedAt: now,
                },
              },
            });
          } else {
            appendNotice(conversationId, message);
          }
          return;
        }

        if (
          event.method === "item/autoApprovalReview/started" ||
          event.method === "item/autoApprovalReview/completed" ||
          event.method === "autoApprovalReview/strictReviewRequired"
        ) {
          const reviewId =
            stringValue(data.reviewId) ??
            `strict-${event.threadId ?? "global"}-${stringValue(data.turnId) ?? "unknown"}`;
          if (pending) {
            updateAgentPanel(conversationId, pending.messageId, {
              approvalReviews: {
                [reviewId]: {
                  reviewId,
                  method: event.method,
                  raw: event.raw,
                  updatedAt: now,
                },
              },
            });
          } else {
            appendNotice(conversationId, message);
          }
          return;
        }

        if (pending) {
          updateAgentPanel(conversationId, pending.messageId, {
            notifications: [
              {
                method: event.method,
                message,
                raw: event.raw,
                createdAt: now,
              },
            ],
          });
        } else {
          appendNotice(conversationId, message);
        }

        if (
          event.method === "thread/closed" ||
          event.method === "thread/deleted" ||
          event.method === "thread/reverted"
        ) {
          if (event.threadId) threadOwnerRef.current.delete(event.threadId);
          patchRuntime(conversationId, {
            threadId: null,
            sending: false,
            waitingOnApproval: false,
            approvals: [],
            runningCommands: 0,
            compacting: false,
          });
        }
        if (event.method === "thread/compacted") {
          patchRuntime(conversationId, { compacting: false });
        }
        return;
      }

      switch (event.type) {
        case "agentText":
          if (pending) appendDelta(conversationId, pending.messageId, { content: event.delta });
          // 有新文字流回来了，之前挂着的"正在重试"就是过时信息——清掉，
          // 别让它跟正文一起留在界面上误导人。
          if (runtimesRef.current[conversationId]?.retrying) {
            patchRuntime(conversationId, { retrying: null });
          }
          break;
        case "reasoning":
          if (pending) appendDelta(conversationId, pending.messageId, { reasoning: event.delta });
          break;
        case "commandOutput": {
          const key = event.itemId ?? "_";
          book.commandBuffers.set(key, (book.commandBuffers.get(key) ?? "") + event.chunk);
          break;
        }
        /* Legacy text projection kept disabled; structured panels are handled above.
        case "turnDiffUpdated":
          appendAgentOutput(
            appendDelta,
            appendNotice,
            conversationId,
            pending?.messageId ?? null,
            "最新 turn diff",
            event.diff,
          );
          break;
        case "fileChangePatchUpdated":
          appendAgentOutput(
            appendDelta,
            appendNotice,
            conversationId,
            pending?.messageId ?? null,
            "文件变更",
            formatFileChanges(event.changes),
          );
          break;
        case "fileChangeOutputDelta":
          appendAgentOutput(
            appendDelta,
            appendNotice,
            conversationId,
            pending?.messageId ?? null,
            "文件变更输出",
            event.delta,
          );
          break;
        case "planDelta":
          appendAgentOutput(
            appendDelta,
            appendNotice,
            conversationId,
            pending?.messageId ?? null,
            "计划增量",
            event.delta,
          );
          break;
        case "turnPlanUpdated":
          appendAgentOutput(
            appendDelta,
            appendNotice,
            conversationId,
            pending?.messageId ?? null,
            "当前计划",
            formatTurnPlan(event.plan, event.explanation),
          );
          break;
        case "terminalInteraction":
          appendAgentOutput(
            appendDelta,
            appendNotice,
            conversationId,
            pending?.messageId ?? null,
            `终端交互 (${event.processId})`,
            event.stdin,
          );
          break;
        case "turnModerationMetadata":
          appendAgentOutput(
            appendDelta,
            appendNotice,
            conversationId,
            pending?.messageId ?? null,
            "内容审核元数据",
            formatModerationMetadata(event.metadata),
          );
          break;
        */
        case "item":
          if (event.itemType === "contextCompaction") {
            patchRuntime(conversationId, { compacting: !event.status });
            if (event.status) {
              appendNotice(conversationId, "涓婁笅鏂囧帇缂╁凡瀹屾垚");
            }
            break;
          }
          if (event.itemType === "commandExecution") {
            if (event.status) {
              // 命令执行**结束**（有 status 了）——冲缓冲区，避免命令还在跑
              // 就把半截输出当成定论落进正文；同时这条命令不再算"正在跑"。
              flushCommandBuffer(conversationId, event.itemId);
              const current = runtimesRef.current[conversationId]?.runningCommands ?? 0;
              patchRuntime(conversationId, { runningCommands: Math.max(0, current - 1) });
            } else {
              // 刚开始跑，还没有输出——这正是"看着像卡住了"的那段时间，
              // 计数器给界面一句"正在执行命令"的提示。顺手记下命令原文，
              // 等 flush 的时候配一句摘要（只在这里拿得到，flush 时只有 itemId）。
              if (event.itemId) {
                const command = readCommandText(event.item);
                if (command) book.commandMeta.set(event.itemId, command);
              }
              const current = runtimesRef.current[conversationId]?.runningCommands ?? 0;
              patchRuntime(conversationId, { runningCommands: current + 1 });
            }
          }
          break;
        case "status":
          patchRuntime(conversationId, { waitingOnApproval: event.waitingOnApproval });
          break;
        case "approvalRequested":
          patchRuntime(conversationId, {
            approvals: [
              ...(runtimesRef.current[conversationId]?.approvals ?? []),
              event as unknown as ApprovalRequest,
            ],
          });
          break;
        case "approvalResolved":
          patchRuntime(conversationId, {
            approvals: (runtimesRef.current[conversationId]?.approvals ?? []).filter(
              (a) => a.requestId !== event.requestId,
            ),
          });
          break;
        case "retrying":
          // 只存最新一条、不追加——上游可能连着推好几条"Reconnecting... N/5"，
          // 真实撞见过一次连续 4 条以上（账号并发配额被设成了 1）。全部堆进
          // 消息列表会刷屏，所以这条不进聊天记录，只更新一个瞬时状态，
          // 由 composer 上方的状态行显示（见 PawChatPane 的 agentRetrying）。
          patchRuntime(conversationId, { retrying: { message: event.message } });
          break;
        case "warning":
          appendNotice(conversationId, `⚠️ ${event.message}`);
          break;
        case "gaveUpRetrying":
          // Rust 侧已经数到 MAX_CONSECUTIVE_RETRIES 并主动打断了这一轮——这是
          // 终态，跟 "failed" 一样要收尾，但措辞得说清楚"是我们自己放弃的"，
          // 不是上游直接判了死刑，用户看到时才知道该做什么（比如去查一下
          // 账号/网络，而不是以为模型本身坏了）。
          patchRuntime(conversationId, {
            sending: false,
            runningCommands: 0,
            retrying: null,
            compacting: false,
          });
          if (pending) finishTurn(conversationId, pending.messageId, { error: true });
          appendNotice(
            conversationId,
            `❌ 连续重试 ${event.attempts} 次仍未成功，已停止本轮：${event.lastMessage}`,
          );
          book.pendingAssistant = null;
          break;
        case "failed":
          // 保险丝：正常情况下每条命令的 item/completed 都会把计数器减回去，
          // 但轮次都失败了，别让一个漏减的命令让下一轮一开始就顶着假的"正在执行"。
          patchRuntime(conversationId, {
            sending: false,
            runningCommands: 0,
            retrying: null,
            compacting: false,
          });
          if (pending) finishTurn(conversationId, pending.messageId, { error: true });
          appendNotice(conversationId, `失败：${event.message}`);
          book.pendingAssistant = null;
          break;
        case "turnCompleted":
          patchRuntime(conversationId, {
            sending: false,
            runningCommands: 0,
            retrying: null,
            compacting: false,
          });
          if (pending) {
            finishTurn(conversationId, pending.messageId, {
              error: !event.success && !event.interrupted,
            });
          }
          if (pending && event.interrupted) {
            appendNotice(conversationId, "_已停止。_");
          }
          book.pendingAssistant = null;
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    desktop,
    appendDelta,
    updateAgentPanel,
    appendNotice,
    finishTurn,
    flushCommandBuffer,
    bookkeepingFor,
    patchRuntime,
  ]);

  const armed = Boolean(activeConversation?.agentCwd);
  const cwd = activeConversation?.agentCwd ?? null;
  const approvalMode: AgentApprovalUiMode = activeConversation?.agentApprovalMode ?? "full";
  const runtime = (activeConversationId && runtimes[activeConversationId]) || EMPTY_RUNTIME;

  const pickDirectory = useCallback(async () => {
    if (!desktop) return;
    const conversationId = activeConversationId ?? ensureActiveConversationId();
    // 锁定之后没有"更换"这回事——这里再挡一层，双保险，真正的锁定在数据层
    // （`setAgentBinding` 只在 `agentCwdLocked` 之后才不写）。**没锁定时可以随便
    // 重选**：还没起过真正的会话，改主意的代价是零。
    if (activeConversation?.agentCwdLocked) return;
    setError(null);
    try {
      const { open } = await import("@tauri-apps/plugin-dialog");
      const picked = await open({ directory: true, multiple: false, title: "选择 agent 的工作目录" });
      if (typeof picked !== "string") return;
      setAgentBinding(conversationId, picked);
    } catch (e) {
      setError(`打不开目录选择器：${describeAgentError(e)}`);
    }
  }, [desktop, activeConversationId, activeConversation, ensureActiveConversationId, setAgentBinding]);

  /** 内部用：归档某个对话正挂着的 thread（如果有），不影响引擎或别的对话。 */
  const endLiveThread = useCallback(
    async (conversationId: string) => {
      const threadId = runtimesRef.current[conversationId]?.threadId;
      if (!threadId) return;
      try {
        await endAgentThread(threadId);
      } catch (e) {
        setError(describeAgentError(e));
      }
      threadOwnerRef.current.delete(threadId);
      patchRuntime(conversationId, {
        threadId: null,
        sending: false,
        waitingOnApproval: false,
        approvals: [],
        compacting: false,
      });
    },
    [patchRuntime],
  );

  const setApprovalModeApi = useCallback(
    async (mode: AgentApprovalUiMode) => {
      const conversationId = activeConversationId;
      if (!conversationId) return;
      await endLiveThread(conversationId);
      setAgentApprovalMode(conversationId, mode);
    },
    [activeConversationId, endLiveThread, setAgentApprovalMode],
  );

  const send = useCallback(
    async (text: string) => {
      if (!desktop) return;
      const conversationId = activeConversationId;
      if (!conversationId) return;
      if (!activeConversation?.agentCwd) {
        setError("先选择工作目录。");
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
      const book = bookkeepingFor(conversationId);
      book.pendingAssistant = { messageId: assistantMessage.id };
      book.planDelta = "";
      book.fileChangeOutputs.clear();
      book.fileSearches.clear();

      try {
        let threadId = runtimesRef.current[conversationId]?.threadId ?? null;
        let attempts = 1;
        if (!threadId) {
          patchRuntime(conversationId, { sending: true });
          const { sandbox, approvalPolicy } = approvalUiModeToParams(approvalMode);
          const started = await startAgent({
            relayBaseUrl,
            groupId,
            sessionToken,
            // 必须是登录那次浏览器请求的 UA——转发层靠它冒充回浏览器身份，
            // 否则后端的会话指纹校验会把每一轮都当成"换了网络环境"直接 401。
            clientUserAgent: navigator.userAgent,
            model: modelId,
            cwd: activeConversation.agentCwd,
            sandbox,
            approvalPolicy,
          });
          threadId = started.threadId;
          attempts = started.attempts;
          threadOwnerRef.current.set(threadId, conversationId);
          patchRuntime(conversationId, { threadId });
          // **这才是真正"开启会话"的那一刻**——之前光选目录不算数。
          // 起会话成功了，工作目录才锁死，不能再重选。
          lockAgentCwd(conversationId);
        }
        if (attempts > 1) {
          appendNotice(conversationId, `_引擎重试了 ${attempts} 次才起来。_`);
        }
        patchRuntime(conversationId, { sending: true });
        await sendToAgent({ threadId, text, model: modelId, reasoning });
      } catch (e) {
        patchRuntime(conversationId, { sending: false });
        finishTurn(conversationId, assistantMessage.id, { error: true });
        appendDelta(conversationId, assistantMessage.id, { content: describeAgentError(e) });
        book.pendingAssistant = null;
      }
    },
    [
      desktop,
      activeConversationId,
      activeConversation,
      approvalMode,
      groupId,
      modelId,
      reasoning,
      sessionToken,
      relayBaseUrl,
      lockAgentCwd,
      beginTurn,
      finishTurn,
      appendDelta,
      appendNotice,
      bookkeepingFor,
      patchRuntime,
    ],
  );

  const interruptTurn = useCallback(async () => {
    const threadId = activeConversationId ? runtimesRef.current[activeConversationId]?.threadId : null;
    if (!threadId) return;
    try {
      await interruptAgent(threadId);
    } catch (e) {
      setError(describeAgentError(e));
    }
  }, [activeConversationId]);

  const compact = useCallback(async () => {
    if (!desktop) {
      setError("涓婁笅鏂囧帇缂╀粎鍦ㄦ闈㈢ agent 涓彲鐢ㄣ€?");
      return;
    }
    const conversationId = activeConversationId;
    const threadId = conversationId
      ? runtimesRef.current[conversationId]?.threadId
      : null;
    if (!conversationId || !threadId) {
      setError("褰撳墠瀵硅瘽杩樻病鏈夊彲鍘嬬缉鐨� agent thread");
      return;
    }
    setError(null);
    patchRuntime(conversationId, { compacting: true });
    try {
      await compactAgent(threadId);
      appendNotice(conversationId, "宸插紑濮嬩笂涓嬫枃鍘嬬缉");
    } catch (e) {
      patchRuntime(conversationId, { compacting: false });
      setError(describeAgentError(e));
    }
  }, [activeConversationId, appendNotice, desktop, patchRuntime]);

  const answer = useCallback(async (requestId: string, approve: boolean) => {
    // 先从队列里去掉，避免用户重复点；真失败了 answerApproval 会抛，
    // 但请求已经不在了——这条留给下一次事件（比如另一端答过）去对齐。
    if (activeConversationId) {
      patchRuntime(activeConversationId, {
        approvals: (runtimesRef.current[activeConversationId]?.approvals ?? []).filter(
          (a) => a.requestId !== requestId,
        ),
      });
    }
    try {
      await answerApproval(requestId, approve ? "approve" : "decline");
    } catch (e) {
      setError(describeAgentError(e));
    }
  }, [activeConversationId, patchRuntime]);

  return {
    desktop,
    armed,
    cwd,
    cwdLocked: Boolean(activeConversation?.agentCwdLocked),
    approvalMode,
    busy: runtime.sending,
    sending: runtime.sending,
    runningTool: runtime.runningCommands > 0,
    retrying: runtime.retrying,
    compacting: runtime.compacting,
    approvals: runtime.approvals,
    waitingOnApproval: runtime.waitingOnApproval,
    error,
    pickDirectory,
    setApprovalMode: setApprovalModeApi,
    send,
    interruptTurn,
    answer,
    discardConversation: endLiveThread,
    compact,
  };
}
