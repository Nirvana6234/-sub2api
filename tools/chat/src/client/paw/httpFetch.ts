import { getClientConfig } from "../../config/client";

/**
 * The `fetch` this client's HTTP calls should use.
 *
 * On the web/PWA build this is the plain global `fetch` — nothing to work
 * around, the page and the relay can be cross-origin and CORS is exactly the
 * browser doing its job.
 *
 * On the desktop build (Tauri) the page itself runs inside a webview whose
 * own origin (`https://tauri.localhost` on Windows) is never the relay's
 * origin, so every request is cross-origin by construction — and unlike a
 * native HTTP client (the .NET relay client's `HttpClient`, say), the
 * browser engine backing this webview enforces CORS on it regardless of
 * whether a person is actually looking at a browser chrome. Getting the
 * relay to allow-list every desktop build's webview origin is a losing
 * game (it can differ by OS/webview version), so instead the desktop build
 * routes through `@tauri-apps/plugin-http`, whose `fetch` is backed by a
 * Rust HTTP client (reqwest) — no browser, no same-origin policy to satisfy.
 * See `src-tauri/capabilities/default.json` for the two origins that plugin
 * is allowed to reach.
 *
 * Loaded lazily and only in the app build: importing
 * `@tauri-apps/plugin-http` outside a Tauri webview has no working IPC
 * bridge underneath it, so the web/PWA build must never evaluate that
 * import — `getClientConfig().isApp` is the build-time flag that keeps it
 * out of that bundle's code path entirely (the import only executes, and
 * so only needs to resolve, when this file's own module is loaded inside an
 * actual Tauri window).
 */
let appFetchPromise: Promise<typeof fetch> | null = null;

function resolveFetch(): Promise<typeof fetch> {
  if (!getClientConfig().isApp) {
    return Promise.resolve(fetch);
  }
  if (!appFetchPromise) {
    appFetchPromise = import("@tauri-apps/plugin-http").then((mod) => mod.fetch);
  }
  return appFetchPromise;
}

export async function pawFetch(
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> {
  const impl = await resolveFetch();
  return impl(input, init);
}
