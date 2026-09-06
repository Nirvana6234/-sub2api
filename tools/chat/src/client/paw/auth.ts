import { safeLocalStorage } from "../../utils/storage";
import {
  PAW_SESSION_EXPIRED_KEY,
  PAW_SESSION_STORAGE_KEY,
} from "./config";
import type { PawSession } from "./types";

function getStorage() {
  return safeLocalStorage();
}

export function loadPawSession(): PawSession | null {
  const storage = getStorage();
  const raw = storage.getItem(PAW_SESSION_STORAGE_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Partial<PawSession> | null;
    const accessToken = typeof parsed?.accessToken === "string" ? parsed.accessToken.trim() : "";
    if (!accessToken) return null;
    return {
      accessToken,
      refreshToken: typeof parsed?.refreshToken === "string" ? parsed.refreshToken.trim() : undefined,
      expiresAt: typeof parsed?.expiresAt === "number" && Number.isFinite(parsed.expiresAt)
        ? parsed.expiresAt
        : undefined,
      user: typeof parsed?.user === "object" && parsed?.user !== null ? parsed.user : undefined,
    };
  } catch {
    return null;
  }
}

/**
 * 把当前账号会话同步给桌面壳的转发层。
 *
 * **放在这里而不是各个调用点，是刻意的。** 令牌变化有四条路（登录、注册、
 * 静默刷新、登出），其中**静默刷新只写 localStorage，根本不碰 React 状态** ——
 * 所以"在组件里用 effect 盯着 session"会漏掉恰恰最要紧的那一条。
 *
 * 漏掉的表现极难查：转发层攥着一个过期 JWT，后端 401，codex 进重连循环，
 * 界面上只剩一句"正在重试"。
 *
 * 存储是所有令牌变化的唯一咽喉，所以同步动作就钉在这里，谁也绕不过去。
 * 浏览器里这是个 no-op（`pushSessionToken` 自己会判断）。
 */
function syncSessionToHost(token: string | null): void {
  void import("../agent/session")
    .then((m) => m.pushSessionToken(token))
    .catch(() => {
      /* 壳里没有 agent，或者还没起来 —— 不该影响登录本身 */
    });
}

export function savePawSession(session: PawSession): void {
  const storage = getStorage();
  storage.setItem(PAW_SESSION_STORAGE_KEY, JSON.stringify(session));
  storage.removeItem(PAW_SESSION_EXPIRED_KEY);
  syncSessionToHost(session.accessToken ?? null);
}

export function clearPawSession(): void {
  const storage = getStorage();
  storage.removeItem(PAW_SESSION_STORAGE_KEY);
  syncSessionToHost(null);
}

export function markPawSessionExpired(): void {
  const storage = getStorage();
  storage.setItem(PAW_SESSION_EXPIRED_KEY, "1");
}

export function wasPawSessionExpired(): boolean {
  const storage = getStorage();
  return storage.getItem(PAW_SESSION_EXPIRED_KEY) === "1";
}
