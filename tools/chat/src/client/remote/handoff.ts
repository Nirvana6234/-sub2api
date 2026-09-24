/**
 * 从主站（用户仪表盘）带着配对码跳进来：`/paw/?pair=123456`。
 *
 * 主站只负责把 6 位码交过来；认领（生成手机签名密钥、拿配对令牌）仍由 Paw 自己做，
 * 因为密钥和令牌都存在 Paw 这边，主站碰不到也不该碰。
 *
 * 码先落到 sessionStorage 再从地址栏抹掉：没登录时要先走登录页，登录完还得接着配；
 * 留在地址栏里则会进浏览器历史，刷新一次就会拿一个已经用掉的码再认领一遍。
 */
import { loadPawSession, savePawSession } from "../paw/auth";
import {
  PENDING_PAIR_CODE_KEY,
  extractPairCode,
  mainSiteAccessToken,
  normalizePairCode,
} from "./handoffCore";

function sessionStore(): Storage | null {
  try {
    return typeof window !== "undefined" ? window.sessionStorage : null;
  } catch {
    return null;
  }
}

/**
 * 读地址栏里的配对码并收起来。没有 Paw 会话时顺带接过主站的访问令牌（同源、同一个账号），
 * 用户不用再登录一次；令牌过期后 Paw 会回到登录页。返回这次是否带了有效的配对码。
 */
export function captureRemoteHandoff(now: number = Date.now()): boolean {
  if (typeof window === "undefined") return false;
  const extracted = extractPairCode(window.location.href);
  if (!extracted) return false;
  try {
    window.history.replaceState(window.history.state, "", extracted.cleanedUrl);
  } catch {
    // 地址栏改不了不影响配对本身。
  }
  if (!extracted.code) return false;

  sessionStore()?.setItem(PENDING_PAIR_CODE_KEY, extracted.code);
  if (!loadPawSession()) {
    try {
      const adopted = mainSiteAccessToken(window.localStorage, now);
      if (adopted) savePawSession(adopted);
    } catch {
      // 读不到主站令牌就走登录页。
    }
  }
  return true;
}

export function hasPendingPairCode(): boolean {
  return normalizePairCode(sessionStore()?.getItem(PENDING_PAIR_CODE_KEY)) !== null;
}

/** 取出待认领的码，取一次就清掉：认领失败也不自动重试同一个码。 */
export function takePendingPairCode(): string | null {
  const store = sessionStore();
  const code = normalizePairCode(store?.getItem(PENDING_PAIR_CODE_KEY));
  store?.removeItem(PENDING_PAIR_CODE_KEY);
  return code;
}
