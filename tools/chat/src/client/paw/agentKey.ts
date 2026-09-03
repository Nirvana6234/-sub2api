/**
 * agent 会话用的**短期专属 key**：登录之后按需向服务端要一把，用完撤销。
 *
 * # 为什么会有这个文件
 *
 * PWA 这条线是**刻意不要 API key** 的 —— 不存 key 就没有存 key 的问题。工作台本来
 * 想沿用同一条路（JWT 直连），但 codex 走不通：
 *
 * - codex 的 agent 循环**自己调模型**，必须给它一个端点 + 一个 Bearer；
 * - codex 0.144.2 **只会说 Responses 这一种线协议**（二进制里 `chat/completions`
 *   出现 0 次），而 Paw 面只有 `/chat/completions`，两边对不上。
 *
 * 所以工作台这一侧要一把真的 key。约束是：**登录后按需取、只在内存里传、绝不落盘、
 * 用完撤销**。
 *
 * # 这个文件的三条铁律
 *
 * 1. **不要把 key 写进 localStorage / sessionStorage / IndexedDB / cookie。**
 *    它只应该从这里出去、直接进 `invoke("agent_start")`，然后被忘掉。
 * 2. **不要打日志。** 连长度、前缀都不要打 —— 排错时习惯性 `console.log(params)`
 *    就会把它带出去。
 * 3. **不要放进 React state 或任何会被 devtools 快照到的地方。** 取完就用，用完丢。
 *
 * 磁盘那一侧另有防线：codex 收到 key 之后会自己把它写进 `CODEX_HOME/auth.json`
 * （实测），所以 Rust 宿主在握手之后**立刻把那个文件抹掉**，抹不掉就让会话起不来。
 * 见 `codex_host::CodexHome::purge_credentials`。
 */
import { pawRequest } from "./api";

/** 一次租约。**除了立刻交给 agent_start，不要让它去任何别的地方。** */
export interface WorkbenchKeyLease {
  /** 撤销时用。 */
  id: number;
  /** 明文 key。**只在内存里。** */
  key: string;
  expiresAt?: string;
}

/**
 * 租约在服务端的名字。
 *
 * 用户会在自己的 key 列表里看见它，所以这串字必须**自解释**：告诉他这是谁建的、
 * 能不能手动删。一个看不懂来历的 key 只会让人以为账号被盗了。
 */
const LEASE_NAME = "共飞工作台（自动创建，可随时删除）";

/**
 * 租期。**故意很短。**
 *
 * 正常路径是会话结束时主动撤销；这个天数是撤销失败时的兜底 ——
 * 比如进程被强杀，没机会发那个 DELETE。
 */
const LEASE_DAYS = 1;

function unwrap<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as { data: T }).data;
  }
  return payload as T;
}

/**
 * 要一把新的工作台 key。
 *
 * 每次会话都新建一把，而不是复用一把长期的 —— 这样一次泄露的爆炸半径就是一次会话，
 * 而且撤销一把不影响别的。
 *
 * @param groupId 指定分组；不传则由服务端按用户默认分组决定。
 *   注意这里**不传** `group_id` 和**传 null** 语义不同：前者是「你决定」，
 *   后者是「清掉绑定」。所以下面是条件性地加字段，不是无脑塞 null。
 */
export async function leaseWorkbenchKey(groupId?: number): Promise<WorkbenchKeyLease> {
  const body: Record<string, unknown> = {
    name: LEASE_NAME,
    expires_in_days: LEASE_DAYS,
  };
  if (typeof groupId === "number") {
    body.group_id = groupId;
  }

  const response = await pawRequest("/api/v1/keys", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

  if (!response.ok) {
    throw new Error(`申请工作台 key 失败（HTTP ${response.status}）`);
  }

  const payload = unwrap<{ id?: number; key?: string; expires_at?: string }>(
    await response.json(),
  );

  if (!payload?.key || typeof payload.id !== "number") {
    // 不要把 payload 打出来 —— 它里面就是那把 key。
    throw new Error("服务端没有返回可用的工作台 key");
  }

  return { id: payload.id, key: payload.key, expiresAt: payload.expires_at };
}

/**
 * 撤销一把租约。
 *
 * **会话结束时一定要调**，包括出错退出的路径。撤不掉也不要往上抛到打断收尾流程 ——
 * 那把 key 一天之内会自己过期，而收尾被打断会留下更多烂摊子。
 */
export async function revokeWorkbenchKey(id: number): Promise<boolean> {
  try {
    const response = await pawRequest(`/api/v1/keys/${id}`, { method: "DELETE" });
    return response.ok;
  } catch {
    return false;
  }
}
