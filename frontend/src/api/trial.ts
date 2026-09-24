/**
 * 未注册访客的网页版试用接口：状态查询、人机验证、文字聊天（流式）。
 * 访客没有登录态，按浏览器本地生成的设备标识计数。
 */
import { apiClient } from './client'
import { buildApiUrl } from './url'
import { extractErrorMessage, extractStreamDelta, parseSSEChunk, parseSSEData } from '@/features/playground/sse'

export const GUEST_TRIAL_DEVICE_HEADER = 'X-Guest-Trial-Device'
const DEVICE_STORAGE_KEY = 'guest_trial_device'

export interface GuestTrialState {
  enabled: boolean
  models: string[]
  default_model: string
  daily_limit: number
  remaining: number
  max_input_chars: number
  captcha_required: boolean
}

export interface GuestTrialMessage {
  role: 'user' | 'assistant'
  content: string
}

/** 试用接口返回的业务错误：reason 用于区分「次数用完 / 需要验证 / 暂未开放」等情况。 */
export class GuestTrialError extends Error {
  constructor(message: string, readonly reason: string, readonly status: number) {
    super(message)
  }
}

function randomDeviceId(): string {
  const bytes = new Uint8Array(18)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

/** 本浏览器的试用设备标识；首次访问时生成并保存在本地。 */
export function getGuestTrialDeviceId(): string {
  try {
    const saved = localStorage.getItem(DEVICE_STORAGE_KEY)
    if (saved && /^[A-Za-z0-9_-]{16,64}$/.test(saved)) return saved
    const created = randomDeviceId()
    localStorage.setItem(DEVICE_STORAGE_KEY, created)
    return created
  } catch {
    return randomDeviceId()
  }
}

function deviceHeaders(): Record<string, string> {
  return { [GUEST_TRIAL_DEVICE_HEADER]: getGuestTrialDeviceId() }
}

export async function fetchGuestTrialState(): Promise<GuestTrialState> {
  const { data } = await apiClient.get<GuestTrialState>('/trial/config', { headers: deviceHeaders() })
  return data
}

export interface GuestTrialCaptchaProof {
  turnstile_token?: string
  tencent_ticket?: string
  tencent_randstr?: string
}

export async function verifyGuestTrial(proof: GuestTrialCaptchaProof): Promise<void> {
  await apiClient.post('/trial/verify', proof, { headers: deviceHeaders() })
}

async function readFailure(response: Response): Promise<GuestTrialError> {
  try {
    const payload = (await response.json()) as { message?: string; reason?: string; error?: { message?: string } }
    const message = payload.message || payload.error?.message || `HTTP ${response.status}`
    return new GuestTrialError(message, payload.reason || '', response.status)
  } catch {
    return new GuestTrialError(`HTTP ${response.status}`, '', response.status)
  }
}

export interface GuestTrialChatResult {
  content: string
  remaining: number | null
}

/** 发送一次试用聊天并流式回调增量文本；返回完整回复和服务端回写的剩余次数。 */
export async function sendGuestTrialChat(
  model: string,
  messages: GuestTrialMessage[],
  options: { signal: AbortSignal; onDelta?: (text: string) => void },
): Promise<GuestTrialChatResult> {
  const response = await fetch(buildApiUrl('/trial/chat/completions'), {
    method: 'POST',
    signal: options.signal,
    headers: { Accept: 'text/event-stream', 'Content-Type': 'application/json', ...deviceHeaders() },
    body: JSON.stringify({ model, messages, stream: true }),
  })
  if (!response.ok) throw await readFailure(response)

  const remainingHeader = response.headers.get('X-Guest-Trial-Remaining')
  const remaining = remainingHeader !== null && remainingHeader !== '' ? Number(remainingHeader) : null
  if (!response.body) throw new GuestTrialError('Streaming response body is unavailable', '', 500)

  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8')
  let buffer = ''
  let content = ''
  const consume = (chunk: string): boolean => {
    const parsed = parseSSEChunk(chunk)
    buffer = parsed.remainder
    for (const frame of parsed.frames) {
      const payload = parseSSEData(frame.data)
      if (payload === '[DONE]') return true
      const errorMessage = extractErrorMessage(payload)
      if (errorMessage) throw new GuestTrialError(errorMessage, '', 502)
      const delta = extractStreamDelta(payload).contentDelta
      if (delta) {
        content += delta
        options.onDelta?.(delta)
      }
    }
    return false
  }

  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    if (consume(buffer + decoder.decode(value, { stream: true }))) return { content, remaining }
  }
  const rest = buffer + decoder.decode()
  if (rest.trim()) consume(`${rest}\n\n`)
  return { content, remaining }
}

/**
 * 按字数上限从最近的消息往前保留历史（总字数含本次提问），保证请求不会因超长被拒。
 * 最新一条用户消息始终保留。
 */
export function trimGuestTrialHistory(messages: GuestTrialMessage[], maxChars: number): GuestTrialMessage[] {
  const kept: GuestTrialMessage[] = []
  let total = 0
  for (let i = messages.length - 1; i >= 0; i--) {
    const length = [...messages[i].content].length
    if (kept.length > 0 && total + length > maxChars) break
    kept.unshift(messages[i])
    total += length
  }
  // 以用户消息开头，避免上下文从一条孤立的助手回复开始
  while (kept.length > 1 && kept[0].role !== 'user') kept.shift()
  return kept
}
