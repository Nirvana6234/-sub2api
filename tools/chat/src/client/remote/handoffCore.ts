/**
 * 主站带配对码跳转（/paw/?pair=123456）的纯逻辑，不依赖浏览器全局与 Paw 的会话模块，
 * 好在 node --test 里单独测。接线见 handoff.ts。
 */

export const PENDING_PAIR_CODE_KEY = "paw-pending-pair-code";
export const PAIR_PARAM = "pair";

/** 主站 (frontend/src/stores/auth.ts) 存访问令牌与过期时间（毫秒）的键。 */
export const MAIN_SITE_TOKEN_KEY = "auth_token";
export const MAIN_SITE_EXPIRES_AT_KEY = "token_expires_at";

/** 主站令牌离过期不到这么久就不接了，免得刚进来就被踢回登录页。 */
export const MIN_REMAINING_MS = 60_000;

export interface KeyValueStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export function normalizePairCode(raw: string | null | undefined): string | null {
  const code = (raw ?? "").replace(/\s/g, "");
  return /^\d{6}$/.test(code) ? code : null;
}

/**
 * 从地址里取出配对码。返回去掉 pair 参数后的地址（无论码是否有效都去掉），
 * 以及规整后的码；地址里没有 pair 参数时返回 null。
 */
export function extractPairCode(href: string): { code: string | null; cleanedUrl: string } | null {
  let url: URL;
  try {
    url = new URL(href);
  } catch {
    return null;
  }
  if (!url.searchParams.has(PAIR_PARAM)) return null;
  const code = normalizePairCode(url.searchParams.get(PAIR_PARAM));
  url.searchParams.delete(PAIR_PARAM);
  return { code, cleanedUrl: `${url.pathname}${url.search}${url.hash}` };
}

/**
 * 主站的访问令牌能不能接过来用：有令牌，且（知道过期时间时）离过期还有一分钟以上。
 * 只接访问令牌、不接刷新令牌——两边共用一个刷新令牌时，谁先刷新谁就让另一边的变成
 * "重复使用"，服务端会把整个登录会话吊销。
 */
export function mainSiteAccessToken(
  store: Pick<KeyValueStore, "getItem">,
  now: number,
): { accessToken: string; expiresAt?: number } | null {
  const token = store.getItem(MAIN_SITE_TOKEN_KEY)?.trim() ?? "";
  if (!token) return null;
  const expiresAt = Number(store.getItem(MAIN_SITE_EXPIRES_AT_KEY));
  if (Number.isFinite(expiresAt) && expiresAt > 0) {
    if (expiresAt - now < MIN_REMAINING_MS) return null;
    return { accessToken: token, expiresAt };
  }
  return { accessToken: token };
}
