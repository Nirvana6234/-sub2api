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

export function savePawSession(session: PawSession): void {
  const storage = getStorage();
  storage.setItem(PAW_SESSION_STORAGE_KEY, JSON.stringify(session));
  storage.removeItem(PAW_SESSION_EXPIRED_KEY);
}

export function clearPawSession(): void {
  const storage = getStorage();
  storage.removeItem(PAW_SESSION_STORAGE_KEY);
}

export function markPawSessionExpired(): void {
  const storage = getStorage();
  storage.setItem(PAW_SESSION_EXPIRED_KEY, "1");
}

export function wasPawSessionExpired(): boolean {
  const storage = getStorage();
  return storage.getItem(PAW_SESSION_EXPIRED_KEY) === "1";
}
