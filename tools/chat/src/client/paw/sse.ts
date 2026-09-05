import type { PawCompletionResult, PawErrorResponse, PawStreamDelta } from "./types";

type PawStreamChoice = {
  delta?: {
    content?: string;
    reasoning_content?: string;
    reasoning?: string;
  };
  finish_reason?: string | null;
};

type PawStreamPayload = {
  choices?: PawStreamChoice[];
  error?: PawErrorResponse["error"];
  message?: string;
};

export function parsePawSSEChunk(buffer: string): { frames: string[]; remainder: string } {
  const parts = buffer.split(/\n\n/);
  const remainder = parts.pop() ?? "";
  return {
    frames: parts.filter((frame) => frame.trim().length > 0),
    remainder,
  };
}

export function parsePawSSEData(payload: string): "[DONE]" | PawStreamPayload {
  const normalized = payload.trim();
  if (normalized === "[DONE]") return "[DONE]";
  return JSON.parse(normalized) as PawStreamPayload;
}

export function extractPawStreamDelta(payload: PawStreamPayload): PawStreamDelta {
  const choice = payload.choices?.[0];
  return {
    contentDelta: choice?.delta?.content ?? "",
    reasoningDelta: choice?.delta?.reasoning_content ?? choice?.delta?.reasoning ?? "",
    finishReason: choice?.finish_reason ?? null,
  };
}

export function extractPawErrorMessage(payload: PawStreamPayload | Record<string, unknown>): string | null {
  const error = (payload as PawStreamPayload).error;
  if (error && typeof error === "object") {
    const code = typeof error.code === "string" ? error.code : "";
    const message = typeof error.message === "string" ? error.message : "";
    return [code, message].filter(Boolean).join(": ") || message || code || null;
  }
  const message = (payload as { message?: unknown }).message;
  return typeof message === "string" && message.trim() ? message.trim() : null;
}

export function accumulatePawCompletion(
  current: PawCompletionResult,
  delta: PawStreamDelta,
): PawCompletionResult {
  return {
    content: current.content + delta.contentDelta,
    reasoningContent: current.reasoningContent + delta.reasoningDelta,
    finishReason: delta.finishReason ?? current.finishReason,
  };
}
