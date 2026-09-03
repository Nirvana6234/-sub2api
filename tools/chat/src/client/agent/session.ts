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

/** 推给界面的事件。tag 是 `type`，和 Rust 侧 `#[serde(tag = "type")]` 对应。 */
export type AgentEvent =
  | { type: "threadStarted"; threadId: string }
  | { type: "turnStarted"; turnId: string | null }
  | { type: "agentText"; turnId: string | null; itemId: string; delta: string }
  | { type: "reasoning"; turnId: string | null; delta: string; kind: string }
  | { type: "commandOutput"; turnId: string | null; itemId: string | null; chunk: string }
  | {
      type: "item";
      itemId: string | null;
      itemType: string;
      status: string | null;
      item: unknown;
    }
  | { type: "status"; waitingOnApproval: boolean; flags: string[] }
  | { type: "approvalRequested"; [k: string]: unknown }
  | { type: "approvalResolved"; requestId: string }
  | { type: "retrying"; message: string; httpStatus: number | null; authFailure: boolean }
  | { type: "failed"; message: string; httpStatus: number | null; authFailure: boolean }
  | {
      type: "turnCompleted";
      turnId: string | null;
      status: string;
      success: boolean;
      interrupted: boolean;
    }
  | { type: "engineStopped"; reason: string }
  // 下面两条是**诊断**，不是 agent 的输出。界面可以显示，但绝不能当成正文画出来。
  | { type: "passthrough"; method: string; raw: unknown }
  | { type: "decodeError"; line: string; error: string };

/** 一条待决的审批。 */
export interface ApprovalRequest {
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

export interface StartAgentParams {
  /** 中转站根地址。codex 永远看不到它 —— 它拿到的是壳里的回环地址。 */
  relayBaseUrl: string;
  /** 这条会话用哪个分组。分组是**每条会话一个**，不绑在 key 上。 */
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
  /** 模型 id。**必须来自 `/api/v1/paw/config`** —— 后端就是拿那份目录校验的。 */
  model: string;
  /** agent 的工作目录。 */
  cwd: string;
  sandbox: AgentSandbox;
  approvalPolicy: AgentApprovalPolicy;
}

export interface StartedThread {
  threadId: string;
  /** 起了几次才成功。>1 说明前面失败过，界面可以提一句。 */
  attempts: number;
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
 * 起一条会话。
 *
 * `sandbox` 这个参数**只约束不经审批就跑的命令**。实测：一旦用户批准，
 * 命令就完全脱离沙箱（`read-only` 下也能写到工作目录外）。所以
 * **真正的安全边界是审批，不是沙箱** —— 审批界面的措辞是安全关键，不是文案问题。
 */
export async function startAgent(params: StartAgentParams): Promise<StartedThread> {
  return call<StartedThread>("agent_start", { params });
}

export async function sendToAgent(text: string): Promise<void> {
  return call<void>("agent_send", { text });
}

/** 停止当前这一轮。**不结束会话** —— 会话还在，可以接着发下一轮。 */
export async function interruptAgent(): Promise<void> {
  return call<void>("agent_interrupt");
}

export async function answerApproval(
  requestId: string,
  decision: AgentDecision,
): Promise<void> {
  return call<void>("agent_answer", { requestId, decision });
}

/** 结束会话：停进程、抹掉落盘的凭据。 */
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
