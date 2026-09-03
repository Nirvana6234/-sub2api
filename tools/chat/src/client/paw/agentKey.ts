/**
 * agent 会话用的**托管 key**：登录之后按需取一把，识别与续期的规则照搬小白端。
 *
 * # 为什么工作台需要一把 key，而 PWA 不需要
 *
 * PWA 这条线是**刻意不要 API key** 的 —— 不存 key 就没有存 key 的问题。工作台本来想
 * 沿用同一条路（JWT 直连），但 codex 走不通，两条都是实测：
 *
 * - codex 的 agent 循环**自己调模型**，必须给它一个端点 + 一个 Bearer；
 * - codex 0.144.2 **只会说 Responses 一种线协议**（真二进制里 `chat/completions`
 *   出现 0 次），而 Paw 面只有 `/chat/completions`，两边对不上。
 *
 * 落盘是可以接受的：**我们是 codex 的宿主**，凭据落在我们自己的程序目录下本来就该我们管。
 * Rust 那侧把这条做成了结构性保证（`CodexHome::under_app_dir`：位置由程序推出来，
 * 调用方指不到别处），并在**会话结束时抹掉**。
 *
 * # 识别规则照搬小白端（`ManagedKeyNaming.cs`），因为里面几条是踩出来的
 *
 * - **按名字认，不按值认。** 列表接口会把明文 key 原样返回，所以任何地方都不需要缓存它。
 * - 名字是 `共飞直连客户端-<机器名>-<安装ID>`。**机器名**让一个账号的几台机器各持各的
 *   租约，在一台上登出不会撤掉另一台的授权；**安装 ID** 让同机的前后两次安装分开，
 *   重装之后不会去收养一个自己说不清来历、却还在替它续期的租约。
 * - **没有过期时间的 key 排在最后，不是最前。** 在租约模型下「永不过期」是个**缺陷**
 *   （授权活得比客户端还久，多半是某次更新把 `expires_at` 清掉了）。把它当最佳候选，
 *   等于让客户端正好收养了租约模型本身要防的那个东西，然后永远替它续下去。
 *
 * # 和小白端共用同一把租约，是刻意的
 *
 * 两个端是二选一的关系，所以这里**沿用同一个产品前缀**，能直接认领并续期小白端那把 key，
 * 不会在用户的 key 列表里堆出两条。代价是：**我们绝不删别的安装留下的 key** ——
 * 小白端会清理「同机旧安装」的孤儿，我们不清，因为那可能正是另一个端在用的那把，
 * 删掉等于把它悄悄登出。租约本身会到期，让它自己过期比替别人做主稳妥。
 *
 * # 三条铁律
 *
 * 1. **不要把 key 写进 localStorage / sessionStorage / IndexedDB / cookie。** 取完直接
 *    交给 `invoke("agent_start")`，然后忘掉。要用的时候重新取 —— 列表接口本来就给。
 * 2. **不要打日志。** 连长度、前缀都不要 —— 排错时习惯性 `console.log(params)` 就带出去了。
 * 3. **不要放进 React state 或任何会被 devtools 快照到的地方。**
 */
import { pawRequest } from "./api";

/** 服务端返回的一把 key（只取我们用得到的字段）。 */
interface RelayApiKey {
  id: number;
  key: string;
  name: string;
  expires_at?: string | null;
  group_id?: number | null;
}

/** 一次租约。**除了立刻交给 agent_start，不要让它去任何别的地方。** */
export interface WorkbenchKeyLease {
  id: number;
  /** 明文 key。**只在内存里。** */
  key: string;
  expiresAt?: string | null;
  /** 这把 key 是新建的，还是认领/续期了已有的。仅供界面提示与排错。 */
  origin: "created" | "adopted";
}

/** 这台机器上这一次安装的身份，由 Rust 侧的 `agent_device_identity` 给出。 */
export interface DeviceIdentity {
  machineName: string;
  installId: string;
}

/**
 * 产品前缀。**和小白端保持一致**是刻意的，见文件头。
 * 改这个字符串等于放弃认领已有租约，会在用户的列表里多出一条。
 */
const PRODUCT = "共飞直连客户端";

/** 租期天数。到期是撤销失败（例如进程被强杀，来不及发 DELETE）时的兜底。 */
const LEASE_DAYS = 30;

/** 剩余不足这么多天就续期，别等到过期那一刻才动。 */
const RENEW_WHEN_DAYS_LEFT = 7;

export function keyName(id: DeviceIdentity): string {
  return `${PRODUCT}-${id.machineName}-${id.installId}`;
}

function machinePrefix(id: DeviceIdentity): string {
  return `${PRODUCT}-${id.machineName}-`;
}

function unwrap<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as { data: T }).data;
  }
  return payload as T;
}

/**
 * 从候选里挑一把「当前」的 key。
 *
 * 本次安装自己的那把**无条件优先**；只有一把都没有时才放宽到同机旧安装留下的
 * （那些同样授权这个账号访问同一个中转站，可以认领，但绝不能盖过本次安装要用的名字）。
 *
 * 排序里那条 `hasExpiry` 是要点：**没有过期时间的排最后**，理由见文件头。
 */
export function findCurrent(keys: RelayApiKey[], id: DeviceIdentity): RelayApiKey | undefined {
  const mine = keyName(id);
  const fromThisMachine = keys.filter((k) => k.name?.startsWith(machinePrefix(id)));

  const best = (candidates: RelayApiKey[]): RelayApiKey | undefined =>
    [...candidates].sort((a, b) => {
      const aHas = a.expires_at ? 1 : 0;
      const bHas = b.expires_at ? 1 : 0;
      if (aHas !== bHas) return bHas - aHas; // 有过期时间的排前面
      const at = a.expires_at ? Date.parse(a.expires_at) : 0;
      const bt = b.expires_at ? Date.parse(b.expires_at) : 0;
      return bt - at; // 同类里挑到期最晚的
    })[0];

  return best(fromThisMachine.filter((k) => k.name === mine)) ?? best(fromThisMachine);
}

async function listKeys(): Promise<RelayApiKey[]> {
  const response = await pawRequest("/api/v1/keys", { method: "GET" });
  if (!response.ok) {
    throw new Error(`读取 key 列表失败（HTTP ${response.status}）`);
  }
  const payload = unwrap<RelayApiKey[] | { items?: RelayApiKey[] }>(await response.json());
  if (Array.isArray(payload)) return payload;
  return payload?.items ?? [];
}

function daysFromNow(days: number): string {
  return new Date(Date.now() + days * 86_400_000).toISOString().replace(/\.\d{3}Z$/, "Z");
}

function needsRenewal(key: RelayApiKey): boolean {
  if (!key.expires_at) return true; // 没有过期时间本身就是要修的状态
  const left = Date.parse(key.expires_at) - Date.now();
  return !Number.isFinite(left) || left < RENEW_WHEN_DAYS_LEFT * 86_400_000;
}

/**
 * 拿到这次会话要用的 key：认领已有的（必要时续期），没有就新建一把。
 *
 * @param identity 由 `invoke("agent_device_identity", { appDir })` 取得。
 * @param groupId 指定分组；不传则由服务端按用户默认分组决定。
 */
export async function acquireWorkbenchKey(
  identity: DeviceIdentity,
  groupId?: number,
): Promise<WorkbenchKeyLease> {
  const existing = findCurrent(await listKeys(), identity);

  if (existing?.key) {
    const renewed = needsRenewal(existing) ? await renewKey(existing.id) : existing;
    return {
      id: renewed.id,
      key: renewed.key || existing.key,
      expiresAt: renewed.expires_at,
      origin: "adopted",
    };
  }

  const created = await createKey(keyName(identity), groupId);
  return { id: created.id, key: created.key, expiresAt: created.expires_at, origin: "created" };
}

async function createKey(name: string, groupId?: number): Promise<RelayApiKey> {
  const body: Record<string, unknown> = { name, expires_in_days: LEASE_DAYS };
  // **不传** group_id 和**传 null** 语义不同：前者是「服务端你决定」，后者是「清掉绑定」。
  // 所以这里是条件性地加字段，不是无脑塞一个可能为 undefined 的值。
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
  const created = unwrap<RelayApiKey>(await response.json());
  if (!created?.key || typeof created.id !== "number") {
    // 不要把 payload 打出来 —— 它里面就是那把 key。
    throw new Error("服务端没有返回可用的工作台 key");
  }
  return created;
}

/**
 * 续期：**请求体里只放 `expires_at`，一个字段都不要多**。
 *
 * 后端把 `expires_at` 当三态处理（`api_key_handler.go`）：
 *
 * | 传什么 | 后端理解为 |
 * |---|---|
 * | 字段缺席 | 不动 |
 * | **空字符串** | **清除过期时间 —— 这把 key 从此永不过期** |
 * | RFC3339 | 设成这个时间 |
 *
 * 所以**绝不能**序列化一个「`expiresAt` 默认为 `""`」的对象过去：那会在用户以为
 * 只是续期/换组的动作里，把一个有期限的租约悄悄变成永久授权，而且没有任何提示。
 * 用一个只有一个键的字面量，就长不出那种默认值。
 */
async function renewKey(id: number): Promise<RelayApiKey> {
  const response = await pawRequest(`/api/v1/keys/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ expires_at: daysFromNow(LEASE_DAYS) }),
  });
  if (!response.ok) {
    throw new Error(`续期工作台 key 失败（HTTP ${response.status}）`);
  }
  return unwrap<RelayApiKey>(await response.json());
}

/**
 * 换分组：**同样只放 `group_id` 一个字段**，理由见 [`renewKey`] 上那张表。
 *
 * 换组之后**新会话**才会用新分组 —— codex 不认识分组，切换等于换一把 key，
 * 正在跑的那一轮不受影响。
 */
export async function switchWorkbenchKeyGroup(id: number, groupId: number): Promise<RelayApiKey> {
  const response = await pawRequest(`/api/v1/keys/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ group_id: groupId }),
  });
  if (!response.ok) {
    throw new Error(`切换分组失败（HTTP ${response.status}）`);
  }
  return unwrap<RelayApiKey>(await response.json());
}

/**
 * 撤销一把租约。
 *
 * **只撤销我们自己这次安装建的那把。** 别去清同机其他安装留下的 ——
 * 那可能正是小白端在用的，删掉等于把它悄悄登出（两个端是二选一，不是互斥到要互相拆台）。
 *
 * 撤不掉也不要往上抛打断收尾流程：那把 key 会自己到期，而收尾被打断会留下更多烂摊子。
 */
export async function revokeWorkbenchKey(id: number): Promise<boolean> {
  try {
    const response = await pawRequest(`/api/v1/keys/${id}`, { method: "DELETE" });
    return response.ok;
  } catch {
    return false;
  }
}
