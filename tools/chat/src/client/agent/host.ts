/**
 * 判断我们是不是跑在桌面壳里，以及**按需**取 Tauri 的 `invoke`。
 *
 * # 为什么这层不能省
 *
 * PWA 和桌面端**共用同一份静态产物**（`next build` 出 `out/`，Tauri 再把它包起来）。
 * 也就是说 agent 那部分代码**一定会**被发到浏览器里去，它只能在运行时把自己关掉。
 *
 * 两条纪律：
 *
 * 1. **判断只能在运行时做，不能在模块顶层做。** 静态导出会预渲染，模块顶层的
 *    `window` 在构建机上是 undefined —— 那时候算出来的答案会被烤进产物里，
 *    于是桌面端拿到一份"我不是桌面端"的常量。所以 `isTauri()` 是个函数，
 *    调用方要在 effect 里调。
 * 2. **`@tauri-apps/api` 只能动态 import。** 顶层静态 import 会把它打进浏览器那份
 *    产物里一起求值。用 `await import()` 之后，PWA 那条路上这个模块从头到尾不会被碰。
 */

/** Tauri v2 注入到 window 上的内部对象。v1 用的是 `__TAURI__`，我们只支持 v2。 */
const TAURI_MARKER = "__TAURI_INTERNALS__";

/**
 * 现在是不是跑在桌面壳里。
 *
 * **不要在模块顶层调用**（见文件头第 1 条）。在 `useEffect` 里调，把结果放进 state。
 */
export function isTauri(): boolean {
  return typeof window !== "undefined" && TAURI_MARKER in window;
}

/**
 * 取 `invoke`。不在桌面壳里就返回 `null` —— **不是抛异常**。
 *
 * 返回 null 而不是抛，是因为"在浏览器里没有 agent"是个**正常状态**，不是故障：
 * PWA 用户本来就该看不到 agent 那一半。抛异常会逼每个调用点写 try/catch，
 * 而那些 catch 迟早会把真正的故障也一起吞掉。
 */
export async function getInvoke(): Promise<
  (<T>(cmd: string, args?: Record<string, unknown>) => Promise<T>) | null
> {
  if (!isTauri()) {
    return null;
  }
  const { invoke } = await import("@tauri-apps/api/core");
  return invoke as <T>(cmd: string, args?: Record<string, unknown>) => Promise<T>;
}

/**
 * 订阅桌面壳推上来的事件。返回取消订阅的函数；不在壳里就返回一个空函数。
 */
export async function listenToHost<T>(
  event: string,
  handler: (payload: T) => void,
): Promise<() => void> {
  if (!isTauri()) {
    return () => {};
  }
  const { listen } = await import("@tauri-apps/api/event");
  const unlisten = await listen<T>(event, (e) => handler(e.payload));
  return unlisten;
}
