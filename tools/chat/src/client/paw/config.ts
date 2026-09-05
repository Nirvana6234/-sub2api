import { getClientConfig } from "../../config/client";

export const PAW_SESSION_STORAGE_KEY = "paw-session";
export const PAW_SESSION_EXPIRED_KEY = "paw-session-expired";

export function getPawServiceBaseUrl(): string {
  return getClientConfig().pawServiceUrl.trim().replace(/\/+$/, "");
}

export function resolvePawUrl(path: string): string {
  const trimmedPath = path.startsWith("/") ? path : `/${path}`;
  const baseUrl = getPawServiceBaseUrl();
  if (!baseUrl) {
    return trimmedPath;
  }
  return new URL(trimmedPath, baseUrl.endsWith("/") ? baseUrl : `${baseUrl}/`).toString();
}

export function getPawLoginPath(): string {
  return "/auth";
}
