import { getPawLoginPath, resolvePawUrl } from "./config";
import {
  clearPawSession,
  loadPawSession,
  markPawSessionExpired,
  savePawSession,
} from "./auth";
import {
  accumulatePawCompletion,
  extractPawErrorMessage,
  extractPawStreamDelta,
  parsePawSSEChunk,
  parsePawSSEData,
} from "./sse";
import type {
  PawAttachmentResponse,
  PawChatRequest,
  PawCompletionResult,
  PawConfigResponse,
  PawErrorResponse,
  PawImageGenerationResponse,
  PawLoginResponse,
  PawRefreshResponse,
  PawSession,
} from "./types";

type PawRequestInit = RequestInit & {
  headers?: HeadersInit;
};

let refreshLock: Promise<PawSession | null> | null = null;

type PawAuthEnvelope<T> = T | { data?: T };

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function unwrapAuthResponse<T>(payload: PawAuthEnvelope<T>): T {
  if (
    payload &&
    typeof payload === "object" &&
    "data" in payload &&
    payload.data &&
    typeof payload.data === "object"
  ) {
    return payload.data;
  }
  return payload as T;
}

function unwrapPawConfigResponse(payload: unknown): PawConfigResponse {
  let data: unknown = payload;
  if (isRecord(data) && "data" in data) {
    data = data.data;
  }
  if (isRecord(data) && "data" in data && isRecord(data.data)) {
    data = data.data;
  }
  if (!isRecord(data) || !Array.isArray(data.groups)) {
    throw new Error("服务端返回的 Paw 配置无效");
  }

  const groups = data.groups.filter(isRecord).map((group) => ({
    id: typeof group.id === "number" ? group.id : 0,
    name: typeof group.name === "string" ? group.name : "",
    description: typeof group.description === "string" ? group.description : "",
    models: Array.isArray(group.models)
      ? group.models.filter(isRecord).map((model) => {
          const reasoning = isRecord(model.reasoning) ? model.reasoning : {};
          return {
            id: typeof model.id === "string" ? model.id : "",
            name: typeof model.name === "string" ? model.name : "",
            owned_by: typeof model.owned_by === "string" ? model.owned_by : "",
            reasoning: {
              supported: reasoning.supported === true,
              values: Array.isArray(reasoning.values)
                ? reasoning.values.filter((value): value is string => typeof value === "string")
                : [],
              default: typeof reasoning.default === "string" ? reasoning.default : "",
            },
            vision: model.vision === true,
            image_generation: model.image_generation === true,
            file_input: model.file_input === true,
          };
        })
      : [],
  }));
  const defaults = isRecord(data.defaults) ? data.defaults : {};

  return {
    data: {
      user: isRecord(data.user)
        ? {
            id: typeof data.user.id === "number" ? data.user.id : 0,
            name: typeof data.user.name === "string" ? data.user.name : "",
            email: typeof data.user.email === "string" ? data.user.email : "",
          }
        : { id: 0, name: "", email: "" },
      groups,
      defaults: {
        group_id: typeof defaults.group_id === "number" ? defaults.group_id : 0,
        model_id: typeof defaults.model_id === "string" ? defaults.model_id : "",
        reasoning: typeof defaults.reasoning === "string" ? defaults.reasoning : "",
      },
    },
  };
}

function getAuthHeader(): string | null {
  const session = loadPawSession();
  return session?.accessToken ? `Bearer ${session.accessToken}` : null;
}

function setStoredSessionFromAuthResponse(response: PawLoginResponse | PawRefreshResponse): PawSession {
  const session: PawSession = {
    accessToken: response.access_token,
    refreshToken: response.refresh_token,
    expiresAt: typeof response.expires_in === "number" && Number.isFinite(response.expires_in)
      ? Date.now() + response.expires_in * 1000
      : undefined,
  };
  savePawSession(session);
  return session;
}

export async function loginPaw(email: string, password: string): Promise<PawSession> {
  const response = await fetch(resolvePawUrl("/api/v1/auth/login"), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
    },
    body: JSON.stringify({ email, password }),
  });

  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }

  const payload = unwrapAuthResponse(
    (await response.json()) as PawAuthEnvelope<PawLoginResponse>,
  ) as PawLoginResponse & { user?: unknown };
  if (!payload.access_token) {
    throw new Error("登录失败");
  }
  const session = setStoredSessionFromAuthResponse(payload);
  if (payload.user && typeof payload.user === "object") {
    session.user = payload.user as PawSession["user"];
    savePawSession(session);
  }
  return session;
}

async function refreshPawSession(): Promise<PawSession | null> {
  if (!refreshLock) {
    refreshLock = (async () => {
      const session = loadPawSession();
      if (!session?.refreshToken) {
        return null;
      }

      const response = await fetch(resolvePawUrl("/api/v1/auth/refresh"), {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        body: JSON.stringify({ refresh_token: session.refreshToken }),
      });

      if (!response.ok) {
        throw new Error(await parsePawFailure(response));
      }

      const payload = unwrapAuthResponse(
        (await response.json()) as PawAuthEnvelope<PawRefreshResponse>,
      );
      if (!payload.access_token || !payload.refresh_token) {
        throw new Error("刷新失败");
      }
      const nextSession = setStoredSessionFromAuthResponse(payload);
      nextSession.refreshToken = payload.refresh_token;
      if (loadPawSession()?.user) {
        nextSession.user = loadPawSession()?.user;
      }
      savePawSession(nextSession);
      return nextSession;
    })().catch((error) => {
      clearPawSession();
      markPawSessionExpired();
      throw error;
    }).finally(() => {
      refreshLock = null;
    });
  }

  return refreshLock;
}

function expirePawSession(): never {
  clearPawSession();
  markPawSessionExpired();
  throw new Error("会话已过期，请重新登录。");
}

async function parsePawFailure(response: Response): Promise<string> {
  const contentType = response.headers.get("content-type") || "";
  try {
    if (contentType.includes("application/json")) {
      const payload = (await response.json()) as PawErrorResponse | { error?: unknown; message?: unknown; detail?: unknown };
      if (payload && typeof payload === "object") {
        const error = (payload as PawErrorResponse).error;
        if (error && typeof error === "object") {
          const code = typeof error.code === "string" ? error.code : "";
          const message = typeof error.message === "string" ? error.message : "";
          return [code, message].filter(Boolean).join(": ") || message || code || `HTTP ${response.status}`;
        }
        const message = (payload as { message?: unknown }).message;
        if (typeof message === "string" && message.trim()) {
          return message.trim();
        }
        const detail = (payload as { detail?: unknown }).detail;
        if (typeof detail === "string" && detail.trim()) {
          return detail.trim();
        }
      }
    }
    const text = (await response.text()).trim();
    if (/<(?:!doctype|html|head|body)\b/i.test(text)) {
      return `请求失败（HTTP ${response.status}）。请检查 sub2api 服务地址。`;
    }
    return text || `HTTP ${response.status}`;
  } catch {
    return `HTTP ${response.status}`;
  }
}

async function pawRequest(input: string, init: PawRequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers ?? {});
  const authHeader = getAuthHeader();
  if (authHeader) {
    headers.set("Authorization", authHeader);
  }

  const execute = async (): Promise<Response> => fetch(resolvePawUrl(input), { ...init, headers });
  let response = await execute();
  if (response.status !== 401) {
    return response;
  }

  try {
    await refreshPawSession();
  } catch {
    expirePawSession();
  }

  const refreshedSession = loadPawSession();
  if (!refreshedSession?.accessToken) {
    expirePawSession();
  }
  headers.set("Authorization", `Bearer ${refreshedSession.accessToken}`);
  response = await execute();
  if (response.status === 401) {
    expirePawSession();
  }
  return response;
}

export async function fetchPawConfig(): Promise<PawConfigResponse> {
  const response = await pawRequest("/api/v1/paw/config", {
    method: "GET",
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  return unwrapPawConfigResponse(await response.json());
}

export async function savePawDefaults(payload: Record<string, unknown>): Promise<void> {
  const response = await pawRequest("/api/v1/paw/config/defaults", {
    method: "PUT",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
}

export async function sendPawChat(
  request: PawChatRequest,
  options: {
    signal: AbortSignal;
    onDelta?: (delta: { contentDelta: string; reasoningDelta: string }) => void;
  },
): Promise<PawCompletionResult> {
  const response = await pawRequest("/api/v1/paw/chat/completions", {
    method: "POST",
    signal: options.signal,
    headers: {
      Accept: request.stream ? "text/event-stream" : "application/json",
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });

  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }

  if (!request.stream) {
    const payload = (await response.json()) as Record<string, unknown>;
    return extractPawCompletionResult(payload);
  }

  if (!response.body) {
    throw new Error("流式响应不可用");
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8");
  let buffer = "";
  let result: PawCompletionResult = {
    content: "",
    reasoningContent: "",
    finishReason: null,
  };

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const parsed = parsePawSSEChunk(buffer);
    buffer = parsed.remainder;

    for (const frame of parsed.frames) {
      let payload;
      try {
        payload = parsePawSSEData(frame.replace(/^data:\s*/gm, "").trim());
      } catch {
        throw new Error("流式响应格式错误");
      }

      if (payload === "[DONE]") {
        return result;
      }

      const errorMessage = extractPawErrorMessage(payload);
      if (errorMessage) {
        throw new Error(errorMessage);
      }

      const delta = extractPawStreamDelta(payload);
      result = accumulatePawCompletion(result, delta);
      options.onDelta?.({
        contentDelta: delta.contentDelta,
        reasoningDelta: delta.reasoningDelta,
      });
    }
  }

  buffer += decoder.decode();
  if (buffer.trim()) {
    const parsed = parsePawSSEChunk(`${buffer}\n\n`);
    for (const frame of parsed.frames) {
      const payload = parsePawSSEData(frame.replace(/^data:\s*/gm, "").trim());
      if (payload === "[DONE]") {
        return result;
      }
      const errorMessage = extractPawErrorMessage(payload);
      if (errorMessage) {
        throw new Error(errorMessage);
      }
      const delta = extractPawStreamDelta(payload);
      result = accumulatePawCompletion(result, delta);
      options.onDelta?.({
        contentDelta: delta.contentDelta,
        reasoningDelta: delta.reasoningDelta,
      });
    }
  }

  return result;
}

export async function uploadPawFile(file: File): Promise<PawAttachmentResponse> {
  const formData = new FormData();
  formData.append("file", file, file.name);
  const response = await pawRequest("/api/v1/paw/files", {
    method: "POST",
    body: formData,
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  return (await response.json()) as PawAttachmentResponse;
}

export async function generatePawImage(payload: Record<string, unknown>): Promise<PawImageGenerationResponse> {
  const response = await pawRequest("/api/v1/paw/images/generations", {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  return (await response.json()) as PawImageGenerationResponse;
}

export async function editPawImage(body: FormData): Promise<PawImageGenerationResponse> {
  const response = await pawRequest("/api/v1/paw/images/edits", {
    method: "POST",
    body,
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  return (await response.json()) as PawImageGenerationResponse;
}

function extractPawCompletionResult(payload: Record<string, unknown>): PawCompletionResult {
  const choice = Array.isArray(payload.choices) ? payload.choices[0] as Record<string, unknown> | undefined : undefined;
  const message = choice && typeof choice === "object" ? choice.message as Record<string, unknown> | undefined : undefined;
  const delta = choice && typeof choice === "object" ? choice.delta as Record<string, unknown> | undefined : undefined;
  return {
    content:
      (typeof message?.content === "string" ? message.content : "") ||
      (typeof delta?.content === "string" ? delta.content : "") ||
      "",
    reasoningContent:
      (typeof message?.reasoning_content === "string" ? message.reasoning_content : "") ||
      (typeof delta?.reasoning_content === "string" ? delta.reasoning_content : "") ||
      (typeof delta?.reasoning === "string" ? delta.reasoning : "") ||
      "",
    finishReason: typeof choice?.finish_reason === "string" ? choice.finish_reason : null,
  };
}

export { pawRequest, getPawLoginPath };
