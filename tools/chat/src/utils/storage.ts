export function safeLocalStorage(): {
  getItem: (key: string) => string | null;
  setItem: (key: string, value: string) => void;
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
    setItem(key: string, value: string): void {
      storage?.setItem(key, value);
    },
    removeItem(key: string): void {
      storage?.removeItem(key);
    },
    clear(): void {
      storage?.clear();
    },
  };
}
