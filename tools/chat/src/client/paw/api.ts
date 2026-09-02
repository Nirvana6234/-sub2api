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
  PawPaymentCheckoutInfo,
  PawPaymentCreateOrderRequest,
  PawPaymentOrder,
  PawPaymentOrderCreateResult,
  PawPublicSettings,
  PawRegisterRequest,
  PawRefreshResponse,
  PawSendVerifyCodeRequest,
  PawSendVerifyCodeResponse,
  PawSession,
  PawUser,
  PawUsageDashboardSnapshot,
  PawUsageDashboardStats,
  PawUsageLog,
} from "./types";

type PawRequestInit = RequestInit & {
  headers?: HeadersInit;
};

let refreshLock: Promise<PawSession | null> | null = null;

type PawAuthEnvelope<T> = T | { data?: T };

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function readFiniteNumber(value: unknown): number | undefined {
  const parsed =
    typeof value === "number"
      ? value
      : typeof value === "string" && value.trim()
        ? Number(value)
        : NaN;
  return Number.isFinite(parsed) ? parsed : undefined;
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

function unwrapData<T>(payload: unknown): T {
  if (isRecord(payload) && "data" in payload) {
    return payload.data as T;
  }
  return payload as T;
}

function normalizePawUser(value: unknown): PawUser | null {
  if (!isRecord(value)) return null;
  const source = isRecord(value.user) ? value.user : value;
  if (typeof source.id !== "number") return null;
  return {
    id: source.id,
    name:
      typeof source.name === "string"
        ? source.name
        : typeof source.username === "string"
          ? source.username
          : "",
    email: typeof source.email === "string" ? source.email : "",
    balance: typeof source.balance === "number" ? source.balance : undefined,
    frozen_balance:
      typeof source.frozen_balance === "number" ? source.frozen_balance : undefined,
    total_recharged:
      typeof source.total_recharged === "number" ? source.total_recharged : undefined,
  };
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
    platform: typeof group.platform === "string" ? group.platform : "",
    rate_multiplier: readFiniteNumber(group.rate_multiplier),
    user_rate_multiplier: readFiniteNumber(group.user_rate_multiplier) ?? null,
    subscription_type:
      typeof group.subscription_type === "string" ? group.subscription_type : "",
    peak_rate_enabled: group.peak_rate_enabled === true,
    peak_start: typeof group.peak_start === "string" ? group.peak_start : "",
    peak_end: typeof group.peak_end === "string" ? group.peak_end : "",
    peak_rate_multiplier: readFiniteNumber(group.peak_rate_multiplier),
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
            balance:
              typeof data.user.balance === "number" ? data.user.balance : undefined,
            frozen_balance:
              typeof data.user.frozen_balance === "number"
                ? data.user.frozen_balance
                : undefined,
            total_recharged:
              typeof data.user.total_recharged === "number"
                ? data.user.total_recharged
                : undefined,
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

export async function registerPaw(request: PawRegisterRequest): Promise<PawSession> {
  const response = await fetch(resolvePawUrl("/api/v1/auth/register"), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
    },
    body: JSON.stringify(request),
  });

  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }

  const payload = unwrapAuthResponse(
    (await response.json()) as PawAuthEnvelope<PawLoginResponse>,
  ) as PawLoginResponse & { user?: unknown };
  if (!payload.access_token) {
    throw new Error("注册失败");
  }
  const session = setStoredSessionFromAuthResponse(payload);
  if (payload.user && typeof payload.user === "object") {
    session.user = payload.user as PawSession["user"];
    savePawSession(session);
  }
  return session;
}

export async function fetchPawPublicSettings(): Promise<PawPublicSettings> {
  const response = await fetch(resolvePawUrl("/api/v1/settings/public"), {
    method: "GET",
    headers: {
      Accept: "application/json",
    },
  });

  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }

  const payload = unwrapAuthResponse(
    (await response.json()) as PawAuthEnvelope<Record<string, unknown>>,
  );
  if (!isRecord(payload)) {
    throw new Error("服务端公开设置无效");
  }

  return {
    registration_enabled: payload.registration_enabled === true,
    email_verify_enabled: payload.email_verify_enabled === true,
    registration_email_suffix_whitelist: Array.isArray(payload.registration_email_suffix_whitelist)
      ? payload.registration_email_suffix_whitelist.filter(
          (value): value is string => typeof value === "string",
        )
      : [],
    promo_code_enabled: payload.promo_code_enabled === true,
    invitation_code_enabled: payload.invitation_code_enabled === true,
    turnstile_enabled: payload.turnstile_enabled === true,
    turnstile_site_key:
      typeof payload.turnstile_site_key === "string" ? payload.turnstile_site_key : "",
    tencent_captcha_enabled: payload.tencent_captcha_enabled === true,
    tencent_captcha_app_id:
      typeof payload.tencent_captcha_app_id === "string"
        ? payload.tencent_captcha_app_id
        : "",
    aliyun_captcha_enabled: payload.aliyun_captcha_enabled === true,
    aliyun_captcha_scene_id:
      typeof payload.aliyun_captcha_scene_id === "string"
        ? payload.aliyun_captcha_scene_id
        : "",
    aliyun_captcha_prefix:
      typeof payload.aliyun_captcha_prefix === "string"
        ? payload.aliyun_captcha_prefix
        : "",
    site_name: typeof payload.site_name === "string" ? payload.site_name : "",
  };
}

export async function sendPawVerifyCode(
  request: PawSendVerifyCodeRequest,
): Promise<PawSendVerifyCodeResponse> {
  const response = await fetch(resolvePawUrl("/api/v1/auth/send-verify-code"), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
    },
    body: JSON.stringify(request),
  });

  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }

  const payload = unwrapAuthResponse(
    (await response.json()) as PawAuthEnvelope<PawSendVerifyCodeResponse>,
  );
  if (!payload || typeof payload.countdown !== "number") {
    throw new Error("服务端验证码响应无效");
  }
  return payload;
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

export async function fetchPawCurrentUser(): Promise<PawUser> {
  const response = await pawRequest("/api/v1/auth/me", {
    method: "GET",
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  const user = normalizePawUser(unwrapData(await response.json()));
  if (!user) {
    throw new Error("服务端返回的账户信息无效");
  }
  return user;
}

export async function fetchPawUsageDashboardStats(): Promise<PawUsageDashboardStats> {
  const response = await pawRequest("/api/v1/usage/dashboard/stats", {
    method: "GET",
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  return unwrapData(await response.json()) as PawUsageDashboardStats;
}

export async function fetchPawUsageDashboardSnapshot(
  days = 30,
): Promise<PawUsageDashboardSnapshot> {
  const end = new Date();
  const start = new Date(end);
  start.setDate(start.getDate() - days + 1);
  const formatDate = (value: Date) => value.toISOString().slice(0, 10);
  const params = new URLSearchParams({
    start_date: formatDate(start),
    end_date: formatDate(end),
    granularity: "day",
    include_trend: "true",
    include_model_stats: "true",
    include_group_stats: "false",
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
  });
  const response = await pawRequest(
    `/api/v1/usage/dashboard/snapshot-v2?${params.toString()}`,
    {
      method: "GET",
      headers: {
        Accept: "application/json",
      },
    },
  );
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  return unwrapData(await response.json()) as PawUsageDashboardSnapshot;
}

export async function fetchPawUsageLogs(
  pageSize = 8,
): Promise<{ items: PawUsageLog[]; total: number }> {
  const params = new URLSearchParams({
    page: "1",
    page_size: String(pageSize),
    sort_by: "created_at",
    sort_order: "desc",
  });
  const response = await pawRequest(`/api/v1/usage?${params.toString()}`, {
    method: "GET",
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  const payload = unwrapData(await response.json());
  if (!isRecord(payload)) {
    return { items: [], total: 0 };
  }
  return {
    items: Array.isArray(payload.items) ? (payload.items as PawUsageLog[]) : [],
    total: typeof payload.total === "number" ? payload.total : 0,
  };
}

export async function fetchPawPaymentCheckoutInfo(): Promise<PawPaymentCheckoutInfo> {
  const response = await pawRequest("/api/v1/payment/checkout-info", {
    method: "GET",
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  const raw = unwrapData(await response.json());
  if (!isRecord(raw)) {
    throw new Error("服务端返回的支付配置无效");
  }
  const methods: Record<string, PawPaymentCheckoutInfo["methods"][string]> = {};
  if (isRecord(raw.methods)) {
    for (const [type, value] of Object.entries(raw.methods)) {
      if (!isRecord(value)) continue;
      methods[type] = {
        currency: typeof value.currency === "string" ? value.currency : "",
        display_name:
          typeof value.display_name === "string" ? value.display_name : type,
        single_min:
          typeof value.single_min === "number" ? value.single_min : 0,
        single_max:
          typeof value.single_max === "number" ? value.single_max : 0,
        fee_rate: typeof value.fee_rate === "number" ? value.fee_rate : 0,
        available: value.available !== false,
      };
    }
  }
  return {
    methods,
    global_min: typeof raw.global_min === "number" ? raw.global_min : 0,
    global_max: typeof raw.global_max === "number" ? raw.global_max : 0,
    balance_disabled: raw.balance_disabled === true,
    balance_recharge_multiplier:
      typeof raw.balance_recharge_multiplier === "number"
        ? raw.balance_recharge_multiplier
        : 1,
    recharge_fee_rate:
      typeof raw.recharge_fee_rate === "number" ? raw.recharge_fee_rate : 0,
    help_text: typeof raw.help_text === "string" ? raw.help_text : "",
    help_image_url:
      typeof raw.help_image_url === "string" ? raw.help_image_url : "",
  };
}

export async function createPawPaymentOrder(
  request: PawPaymentCreateOrderRequest,
): Promise<PawPaymentOrderCreateResult> {
  const response = await pawRequest("/api/v1/payment/orders", {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  const result = unwrapData(await response.json());
  if (!isRecord(result) || typeof result.order_id !== "number") {
    throw new Error("服务端返回的支付订单无效");
  }
  return result as unknown as PawPaymentOrderCreateResult;
}

export async function fetchPawPaymentOrder(orderId: number): Promise<PawPaymentOrder> {
  const response = await pawRequest(`/api/v1/payment/orders/${orderId}`, {
    method: "GET",
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
  const order = unwrapData(await response.json());
  if (!isRecord(order) || typeof order.id !== "number") {
    throw new Error("服务端返回的支付订单无效");
  }
  return order as unknown as PawPaymentOrder;
}

export async function cancelPawPaymentOrder(orderId: number): Promise<void> {
  const response = await pawRequest(`/api/v1/payment/orders/${orderId}/cancel`, {
    method: "POST",
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(await parsePawFailure(response));
  }
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
