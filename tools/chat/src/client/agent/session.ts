/**
 * agent 会话的客户端封装：类型 + 一层薄薄的 `invoke` 包装。
 *
 * # 前端不认识 codex 协议
 *
 * 这里的类型是 Rust 侧 `agent/dto.rs` 的镜像，而那一套是**刻意收窄过**的：
 * 同一个「拒绝」在线上有两套词汇、三类审批的响应形状互不相同、被打断的一轮走的
 * 也是 `turn/completed`。这些细节一旦漏到界面上，就会以「某个按钮偶尔不生效」的
 * 形式回来找我们。所以这一侧只发**语义**（同意/拒绝/拒绝并中断），线上说哪个词
 * 由 Rust 照着那条请求决定。
 *
 * 改这里的类型时**必须同时改 `dto.rs`** —— 两边靠人眼对齐，没有编译器帮忙。
 */
import { getInvoke, listenToHost } from "./host";

/**
 * 推给界面的事件。tag 是 `type`，和 Rust 侧 `#[serde(tag = "type")]` 对应。
 *
 * # 多会话并发改造：几乎每个变体都带 `threadId`
 *
 * 一个引擎进程现在能同时服务好几个对话，一条事件流里会交替出现属于不同对话的
 * 通知。**`threadId` 是把事件归到正确对话的唯一依据**，不是可选装饰——`useAgentSession`
 * 按它路由到各自的消息列表。唯一的例外是 `engineStopped`：它天生是全局的
 * （进程本身没了），要广播给所有挂着的对话，不能只归给一个。
 */
export type AgentEvent =
  | { type: "threadStarted"; threadId: string }
  | { type: "turnStarted"; threadId: string; turnId: string | null }
  | { type: "agentText"; threadId: string; turnId: string | null; itemId: string; delta: string }
  | { type: "reasoning"; threadId: string; turnId: string | null; delta: string; kind: string }
  | {
      type: "commandOutput";
      threadId: string;
      turnId: string | null;
      itemId: string | null;
      chunk: string;
    }
  | { type: "turnDiffUpdated"; threadId: string; turnId: string; diff: string }
  | {
      type: "fileChangePatchUpdated";
      threadId: string;
      turnId: string;
      itemId: string;
      changes: unknown;
    }
  | {
      type: "fileChangeOutputDelta";
      threadId: string;
      turnId: string;
      itemId: string;
      delta: string;
    }
  | {
      type: "planDelta";
      threadId: string;
      turnId: string;
      itemId: string;
      delta: string;
    }
  | {
      type: "turnPlanUpdated";
      threadId: string;
      turnId: string;
      plan: unknown;
      explanation: string | null;
    }
  | {
      type: "terminalInteraction";
      threadId: string;
      turnId: string;
      itemId: string;
      processId: string;
      stdin: string;
    }
  | {
      type: "turnModerationMetadata";
      threadId: string;
      turnId: string;
      metadata: unknown;
    }
  | {
      type: "item";
      threadId: string;
      itemId: string | null;
      itemType: string;
      status: string | null;
      item: unknown;
    }
  | { type: "status"; threadId: string; waitingOnApproval: boolean; flags: string[] }
  | { type: "approvalRequested"; [k: string]: unknown }
  | { type: "approvalResolved"; threadId: string; requestId: string }
  | {
      type: "retrying";
      threadId: string | null;
      message: string;
      httpStatus: number | null;
      authFailure: boolean;
    }
  /** 同一条 thread 连续重试太多次，Rust 侧自己放弃并打断了这一轮——**终态**，
   * 不会再有这条 thread 的后续正文/错误事件。见 `agent/mod.rs` 的
   * `MAX_CONSECUTIVE_RETRIES`：数字对齐 codex 自己「Reconnecting... N/5」的 5，
   * 不是巧合。 */
  | {
      type: "gaveUpRetrying";
      threadId: string;
      attempts: number;
      lastMessage: string;
    }
  /** codex 主动说给用户听的一句话（比如模型元数据缺失时的降级提示）。
   * 和 passthrough/decodeError 不同——这条是**认识**的，只是内容因情况而异，
   * 不该套「协议可能漂了」那种诊断措辞。 */
  | { type: "warning"; threadId: string | null; message: string }
  | {
      type: "knownNotification";
      method: string;
      threadId: string | null;
      raw: unknown;
    }
  | {
      type: "failed";
      threadId: string | null;
      message: string;
      httpStatus: number | null;
      authFailure: boolean;
    }
  | {
      type: "turnCompleted";
      threadId: string;
      turnId: string | null;
      status: string;
      success: boolean;
      interrupted: boolean;
    }
  /** 一条 thread 被主动归档了（`endAgentThread` 的结果）——只影响它自己，
   * 引擎和别的对话都还在跑。和 `engineStopped`（全局）是两回事。 */
  | { type: "threadEnded"; threadId: string }
  /** **全局事件**：引擎没了，所有挂着的对话都要收到，不能只归给"当前显示的那个"。 */
  | { type: "engineStopped"; reason: string }
  // 下面两条是**诊断**，不是 agent 的输出。界面可以显示，但绝不能当成正文画出来。
  | { type: "passthrough"; method: string; raw: unknown }
  | { type: "decodeError"; line: string; error: string };

/** 一条待决的审批。 */
export interface ApprovalRequest {
  /** 这条审批属于哪个对话——尽力而为，极少数情况下挖不出来会是 `null`。 */
  threadId: string | null;
  requestId: string;
  method: string;
  kind: "command" | "fileChange" | "permissions" | "unknown";
  reason: string | null;
  /** 命令执行：真正要跑的整条命令，**必须原样展示**，不能只给摘要。 */
  command: string | null;
  cwd: string | null;
  item: unknown;
  /** 有值时这不是一次性放行，而是对某个根目录的作用域授权，界面必须说清。 */
  grantRoot: string | null;
}

/** 用户的决定 —— 只有语义。 */
export type AgentDecision = "approve" | "approveForSession" | "decline" | "cancel";

/** 沙箱地板。**注意它只约束不经审批就跑的命令**（见 `startAgent` 的说明）。 */
export type AgentSandbox = "read-only" | "workspace-write" | "danger-full-access";

/** 审批策略。只有这三个值，且是 kebab —— 没有 `on-failure`。 */
export type AgentApprovalPolicy = "untrusted" | "on-request" | "never";

/**
 * composer 工具条暴露给用户的审批模式——只有两态，不是协议的三态。
 * `untrusted` 不再对用户暴露；哪个协议值对应哪个 UI 语义，只在
 * `approvalUiModeToParams` 这一处翻译，别处不该再猜。
 */
export type AgentApprovalUiMode = "review" | "full";

/**
 * 把两态的用户语义翻成协议参数。
 *
 * `full`（完全控制）**必须**把沙箱地板一起打开到 `danger-full-access`——
 * 实测（开发计划 A10）：`approvalPolicy=never` 时，没被自动放行的命令会在
 * **进沙箱之前**被 exec_policy 静默拒掉（"blocked by policy"）。sandbox 若还留在
 * `workspace-write`，"完全控制"就会变成一个平时看不出、出事才发现的假承诺。
 */
export function approvalUiModeToParams(
  mode: AgentApprovalUiMode,
): { sandbox: AgentSandbox; approvalPolicy: AgentApprovalPolicy } {
  return mode === "review"
    ? { sandbox: "workspace-write", approvalPolicy: "on-request" }
    : { sandbox: "danger-full-access", approvalPolicy: "never" };
}

/**
 * 起一条新 thread 要的参数。
 *
 * # 多会话并发改造之后，这不是"起会话"，是"起一条 thread"
 *
 * 引擎（进程 + 转发层 + 握手）第一次调用时才懒起，之后**被所有对话共用**——
 * `relayBaseUrl`/`sessionToken`/`clientUserAgent`/`model` 只在引擎第一次起来时
 * 生效，复用已有引擎的后续调用会忽略这几个字段（这几样东西是引擎自己的，
 * 不是按对话变的）。真正每轮用的模型/推理强度走 [`SendAgentParams`]。
 */
export interface StartAgentParams {
  /** 中转站根地址。codex 永远看不到它 —— 它拿到的是壳里的回环地址。 */
  relayBaseUrl: string;
  /** 这条 thread 用哪个分组。分组是**每条 thread 一个**，不绑在 key 上。 */
  groupId: number;
  /** 当前账号会话。刷新之后要用 `pushSessionToken` 再推一次。 */
  sessionToken: string;
  /**
   * 登录那次浏览器请求的 `User-Agent`（一律传 `navigator.userAgent`）。
   *
   * 后端的账号会话绑着一个「IP + UA」指纹，指纹在登录时随 `sessionToken` 一起
   * 签发。转发层转发请求用的是 Rust 的 HTTP 客户端，默认 UA 跟浏览器的不一样——
   * 同一个 token、两种指纹，后端会当成会话被搬到了别的网络环境，直接 401
   * `SESSION_BINDING_MISMATCH` 并撤销整个 token family。传错、传空都会导致
   * **每一轮**都这样炸。
   */
  clientUserAgent: string;
  /** 引擎第一次起来时命令行的默认模型；不是这条 thread 真正会用的模型。 */
  model: string;
  /** agent 的工作目录。**一旦这条对话绑定成功就永久锁定**，不提供更换入口。 */
  cwd: string;
  sandbox: AgentSandbox;
  approvalPolicy: AgentApprovalPolicy;
}

export interface StartedThread {
  threadId: string;
  /** 引擎起了几次才成功。>1 说明前面失败过，界面可以提一句。复用已有引擎恒为 1。 */
  attempts: number;
}

/**
 * 发一轮提问要的参数。
 *
 * `model`/`reasoning` **必须每轮都传**——多会话并发之后模型不再是引擎级的常量，
 * 一个进程要同时服务好几个用了不同模型的对话。
 */
export interface SendAgentParams {
  threadId: string;
  text: string;
  /** **必须来自 `/api/v1/paw/config`** —— 后端就是拿那份目录校验的。 */
  model: string;
  /** 推理强度；空字符串＝"标准"，Rust 侧会翻成协议层的"不覆盖"。 */
  reasoning: string;
}

/** Rust 侧推事件用的通道名。 */
export const AGENT_EVENT = "agent://event";

async function call<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const invoke = await getInvoke();
  if (!invoke) {
    throw new Error("agent 只在桌面端可用");
  }
  return invoke<T>(cmd, args);
}

/**
 * 起一条新 thread（引擎懒起、之后被所有对话共用，见 [`StartAgentParams`]）。
 *
 * `sandbox` 这个参数**只约束不经审批就跑的命令**。实测：一旦用户批准，
 * 命令就完全脱离沙箱（`read-only` 下也能写到工作目录外）。所以
 * **真正的安全边界是审批，不是沙箱** —— 审批界面的措辞是安全关键，不是文案问题。
 */
export async function startAgent(params: StartAgentParams): Promise<StartedThread> {
  return call<StartedThread>("agent_start", { params });
}

export async function sendToAgent(params: SendAgentParams): Promise<void> {
  return call<void>("agent_send", { params });
}

/** 打断某条 thread 上正在跑的那一轮。**不结束这条 thread** —— 还能接着发下一轮。 */
export async function interruptAgent(threadId: string): Promise<void> {
  return call<void>("agent_interrupt", { threadId });
}

export async function answerApproval(
  requestId: string,
  decision: AgentDecision,
): Promise<void> {
  return call<void>("agent_answer", { requestId, decision });
}

/**
 * 归档一条 thread——**只影响它自己**，引擎和别的对话都还在跑。
 * 和 `stopAgent`（杀整个引擎）是两回事，别用混。
 */
export async function endAgentThread(threadId: string): Promise<void> {
  return call<void>("agent_end_thread", { threadId });
}

export async function compactAgent(threadId: string): Promise<void> {
  return call<void>("agent_compact", { threadId });
}

/**
 * 停掉**整个引擎**：杀进程、抹凭据，**影响所有还挂着的对话**。
 * 是给"整个应用要退出/登出 agent"这类场景用的，不是"结束当前这个对话"该调的东西。
 */
export async function stopAgent(): Promise<void> {
  return call<void>("agent_stop");
}

export async function agentIsRunning(): Promise<boolean> {
  const invoke = await getInvoke();
  if (!invoke) {
    return false;
  }
  return invoke<boolean>("agent_is_running");
}

/**
 * 把当前账号会话推给转发层。
 *
 * **登录成功之后、以及每次刷新拿到新 token 之后都要调**；登出时传 `null`。
 * 不推的话转发层会一直攥着一个过期 JWT，表现是后端 401 → codex 进重连循环，
 * 界面上只剩一句「正在重试」，没有任何线索。
 */
export async function pushSessionToken(token: string | null): Promise<void> {
  const invoke = await getInvoke();
  if (!invoke) {
    return;
  }
  await invoke<void>("agent_set_session_token", { token });
}

/** 订阅 agent 事件流。返回取消订阅的函数。 */
export function subscribeToAgent(
  handler: (event: AgentEvent) => void,
): Promise<() => void> {
  return listenToHost<AgentEvent>(AGENT_EVENT, handler);
}

/**
 * Rust 侧 `BridgeError` 序列化后是 `{kind, message?}` 这样一个**普通对象**，
 * 不是 `Error` 实例——`invoke()` 失败时 catch 到的就是它。直接 `String(e)` 只会
 * 拿到 `"[object Object]"`（撞过一次：agent 起会话失败，界面上原样显示这串英文，
 * 用户根本看不出发生了什么）。**所有 catch 到 agent 相关错误的地方都必须走这个**，
 * 不准各自 `String(e)`。
 */
export function describeAgentError(e: unknown): string {
  if (typeof e === "string") return e;
  if (e instanceof Error) return e.message;
  if (e && typeof e === "object") {
    const obj = e as Record<string, unknown>;
    if (typeof obj.message === "string" && obj.message) return obj.message;
    if (typeof obj.kind === "string") {
      return AGENT_ERROR_KIND_FALLBACKS[obj.kind] ?? obj.kind;
    }
  }
  try {
    return JSON.stringify(e);
  } catch {
    return String(e);
  }
}

/**
 * 只有没有 `message` 字段的变体需要在这里兜底——目前只有 `NotRunning`
 * （unit 变体，`#[serde(tag="kind", content="message")]` 不会给它生成 message 字段）。
 */
const AGENT_ERROR_KIND_FALLBACKS: Record<string, string> = {
  notRunning: "还没有正在运行的 agent 引擎",
};
