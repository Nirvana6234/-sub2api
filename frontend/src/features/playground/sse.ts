import type { PlaygroundCompletionResult, PlaygroundStreamDelta } from './types'

export interface SSEFrame {
  data: string
  event?: string
  id?: string
}

export interface ParsedSSEChunk {
  frames: SSEFrame[]
  remainder: string
}

function normalizeSSEText(value: string): string {
  return value.replace(/\r\n/g, '\n').replace(/\r/g, '\n')
}

export function parseSSEChunk(buffer: string): ParsedSSEChunk {
  const normalized = normalizeSSEText(buffer)
  const parts = normalized.split('\n\n')
  const remainder = parts.pop() ?? ''
  const frames: SSEFrame[] = []

  for (const part of parts) {
    const lines = part.split('\n')
    const dataLines: string[] = []
    let event: string | undefined
    let id: string | undefined

    for (const line of lines) {
      if (!line || line.startsWith(':')) continue
      if (line.startsWith('data:')) {
        dataLines.push(line.slice(5).trimStart())
        continue
      }
      if (line === 'data') {
        dataLines.push('')
        continue
      }
      if (line.startsWith('event:')) {
        event = line.slice(6).trimStart()
        continue
      }
      if (line.startsWith('id:')) {
        id = line.slice(3).trimStart()
      }
    }

    if (dataLines.length > 0) {
      frames.push({
        data: dataLines.join('\n'),
        event,
        id,
      })
    }
  }

  return { frames, remainder }
}

export function parseSSEData(data: string): '[DONE]' | Record<string, unknown> {
  if (data.trim() === '[DONE]') {
    return '[DONE]'
  }
  return JSON.parse(data) as Record<string, unknown>
}

function extractText(value: unknown): string {
  if (typeof value === 'string') return value
  if (!Array.isArray(value)) return ''

  return value
    .map((part) => {
      if (typeof part === 'string') return part
      if (!part || typeof part !== 'object') return ''
      const typedPart = part as { text?: unknown; type?: unknown }
      if (typeof typedPart.text === 'string') return typedPart.text
      if (typedPart.type === 'text' && typeof (part as { content?: unknown }).content === 'string') {
        return (part as { content: string }).content
      }
      return ''
    })
    .join('')
}

function firstChoice(payload: Record<string, unknown>): Record<string, unknown> {
  const choices = Array.isArray(payload.choices) ? payload.choices : []
  if (choices.length > 1) {
    throw new Error('Playground supports exactly one completion choice')
  }
  return (choices[0] as Record<string, unknown> | undefined) ?? {}
}

export function extractStreamDelta(payload: Record<string, unknown>): PlaygroundStreamDelta {
  const choice = firstChoice(payload)
  const delta = (typeof choice.delta === 'object' && choice.delta !== null
    ? choice.delta
    : {}) as Record<string, unknown>

  return {
    contentDelta: extractText(delta.content),
    reasoningDelta: extractText(delta.reasoning_content) || extractText(delta.reasoning) || extractText(choice.reasoning),
    finishReason: typeof choice.finish_reason === 'string' ? choice.finish_reason : null,
  }
}

export function extractCompletionResult(payload: Record<string, unknown>): PlaygroundCompletionResult {
  const choice = firstChoice(payload)
  const message = (typeof choice.message === 'object' && choice.message !== null
    ? choice.message
    : {}) as Record<string, unknown>

  return {
    content: extractText(message.content),
    reasoningContent: extractText(message.reasoning_content) || extractText(message.reasoning) || extractText(choice.reasoning),
    finishReason: typeof choice.finish_reason === 'string' ? choice.finish_reason : null,
  }
}

export function extractErrorMessage(payload: Record<string, unknown>): string | null {
  const error = payload.error
  if (typeof error === 'string') return error
  if (!error || typeof error !== 'object') return null

  const typedError = error as { message?: unknown; type?: unknown; code?: unknown }
  const segments = [typedError.message, typedError.type, typedError.code]
    .filter((value): value is string => typeof value === 'string' && value.trim().length > 0)
    .map((value) => value.trim())

  return segments.length > 0 ? segments.join(' · ') : null
}

