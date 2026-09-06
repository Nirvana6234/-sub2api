export function safeLocalStorage(): {
  getItem: (key: string) => string | null;
  /** 存成功返回 true。**不抛**——配额满了（真实撞见过：长会话把 agent 的
   * 命令输出攒进 `paw-conversations`，写爆了 localStorage 的配额）或隐私模式
   * 禁止写入，都不该让一次持久化失败带崩整个页面。调用方想知道"这次真的
   * 没存上"就看返回值；只是想尽力而为地存一下就不用管。 */
  setItem: (key: string, value: string) => boolean;
  removeItem: (key: string) => void;
  clear: () => void;
} {
  let storage: Storage | null;

  try {
    storage = typeof window !== "undefined" && window.localStorage ? window.localStorage : null;
  } catch {
    storage = null;
  }

  return {
    getItem(key: string): string | null {
      return storage ? storage.getItem(key) : null;
    },
    setItem(key: string, value: string): boolean {
      if (!storage) return false;
      try {
        storage.setItem(key, value);
        return true;
      } catch (e) {
        console.warn(`[storage] setItem(${key}) 失败，这次没存上：`, e);
        return false;
      }
    },
    removeItem(key: string): void {
      storage?.removeItem(key);
    },
    clear(): void {
      storage?.clear();
    },
  };
}
