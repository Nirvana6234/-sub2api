import { authAPI } from '@/api'
import { clearAuthToken, getAuthToken } from '@/api/auth'
import { buildApiUrl } from '@/api/url'
import type { PlaygroundAttachment, PlaygroundCompletionResult, PlaygroundImageGenerationResponse, PlaygroundModel, PlaygroundPersistedState, PlaygroundVideoResponse } from './types'
import { extractCompletionResult, extractErrorMessage, extractStreamDelta, parseSSEChunk, parseSSEData } from './sse'
import { clearAllPlaygroundStates, normalizePersistedState } from './persistence'
import { normalizePlaygroundModels } from './viewModel'

let refreshLock: Promise<void> | null = null

function getAuthorizationHeader(): string {
  const token = getAuthToken()
  if (!token) {
    throw new Error('Missing authentication token')
  }
  return `Bearer ${token}`
}

async function refreshAccessToken(): Promise<void> {
  if (!refreshLock) {
    refreshLock = authAPI.refreshToken()
      .then(() => undefined)
      .finally(() => {
        refreshLock = null
      })
  }
  return refreshLock
}

function expirePanelSession(): void {
  clearAllPlaygroundStates()
  clearAuthToken()
  sessionStorage.setItem('auth_expired', '1')

  if (typeof window !== 'undefined' && !window.location.pathname.includes('/login')) {
    window.location.href = '/login'
  }
}

async function parseFailure(response: Response): Promise<string> {
  const responseType = response.headers.get('content-type') || ''

  try {
    if (responseType.includes('application/json')) {
      const payload = await response.json() as Record<string, unknown>
      return extractErrorMessage(payload)
        || (typeof payload.message === 'string' ? payload.message : null)
        || (typeof payload.detail === 'string' ? payload.detail : null)
        || `HTTP ${response.status}`
    }

    const text = (await response.text()).trim()
    return text || `HTTP ${response.status}`
  } catch {
    return `HTTP ${response.status}`
  }
}

async function authorizedFetch(
  input: RequestInfo | URL,
  init: RequestInit & { headers: Record<string, string> },
): Promise<Response> {
  const execute = async (): Promise<Response> => {
    const headers = {
      ...init.headers,
      Authorization: getAuthorizationHeader(),
    }

    return fetch(input, {
      ...init,
      headers,
    })
  }

  let response = await execute()

  if (response.status === 401) {
    try {
      await refreshAccessToken()
    } catch {
      expirePanelSession()
      throw new Error('Session expired. Please log in again.')
    }
    response = await execute()
    if (response.status === 401) {
      expirePanelSession()
      throw new Error('Session expired. Please log in again.')
    }
  }

  return response
}

export async function fetchPlaygroundModels(keyId: number, signal?: AbortSignal): Promise<PlaygroundModel[]> {
  const response = await authorizedFetch(buildApiUrl('/playground/models'), {
    method: 'GET',
    signal,
    headers: {
      Accept: 'application/json',
      'X-Sub2API-Playground-Key-ID': String(keyId),
    },
  })

  if (!response.ok) {
    throw new Error(await parseFailure(response))
  }

  const payload = await response.json() as { data?: PlaygroundModel[] }
  return normalizePlaygroundModels(Array.isArray(payload.data) ? payload.data : [])
}

export async function fetchPlaygroundHistory(userId: number, signal?: AbortSignal): Promise<PlaygroundPersistedState | null> {
  void userId
  const response = await authorizedFetch(buildApiUrl('/playground/history'), {
    method: 'GET',
    signal,
    headers: {
      Accept: 'application/json',
    },
  })

  // A rolling deployment may briefly serve a backend without the history
  // endpoint. Local persistence remains the compatible fallback in that case.
  if (response.status === 404 || response.status === 405) return null
  if (!response.ok) {
    throw new Error(await parseFailure(response))
  }

  const payload = await response.json() as { data?: unknown }
  if (!payload.data || typeof payload.data !== 'object') return null
  return normalizePersistedState(payload.data)
}

export async function savePlaygroundHistory(userId: number, state: PlaygroundPersistedState): Promise<boolean> {
  void userId
  const response = await authorizedFetch(buildApiUrl('/playground/history'), {
    method: 'PUT',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(state),
  })

  if (response.status === 404 || response.status === 405) return false
  if (!response.ok) {
    throw new Error(await parseFailure(response))
  }
  return true
}

export async function sendPlaygroundChat(
  keyId: number,
  body: Record<string, unknown>,
  options: {
    signal: AbortSignal
    onDelta?: (delta: { contentDelta: string; reasoningDelta: string }) => void
  },
): Promise<PlaygroundCompletionResult> {
  const isStreaming = body.stream === true
  const response = await authorizedFetch(buildApiUrl('/playground/chat/completions'), {
    method: 'POST',
    signal: options.signal,
    headers: {
      Accept: isStreaming ? 'text/event-stream' : 'application/json',
      'Content-Type': 'application/json',
      'X-Sub2API-Playground-Key-ID': String(keyId),
    },
    body: JSON.stringify(body),
  })

  if (!response.ok) {
    throw new Error(await parseFailure(response))
  }

  if (!isStreaming) {
    const payload = await response.json() as Record<string, unknown>
    const errorMessage = extractErrorMessage(payload)
    if (errorMessage) {
      throw new Error(errorMessage)
    }
    return extractCompletionResult(payload)
  }

  if (!response.body) {
    throw new Error('Streaming response body is unavailable')
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8')
  let buffer = ''
  let content = ''
  let reasoningContent = ''
  let finishReason: string | null = null

  while (true) {
    const { done, value } = await reader.read()
    if (done) break

    buffer += decoder.decode(value, { stream: true })
    const parsed = parseSSEChunk(buffer)
    buffer = parsed.remainder

    for (const frame of parsed.frames) {
      let eventPayload: '[DONE]' | Record<string, unknown>
      try {
        eventPayload = parseSSEData(frame.data)
      } catch {
        throw new Error('Malformed streaming response')
      }
      if (eventPayload === '[DONE]') {
        return { content, reasoningContent, finishReason }
      }

      const errorMessage = extractErrorMessage(eventPayload)
      if (errorMessage) {
        throw new Error(errorMessage)
      }

      const delta = extractStreamDelta(eventPayload)
      if (delta.contentDelta) {
        content += delta.contentDelta
      }
      if (delta.reasoningDelta) {
        reasoningContent += delta.reasoningDelta
      }
      if (delta.finishReason) {
        finishReason = delta.finishReason
      }
      options.onDelta?.({
        contentDelta: delta.contentDelta,
        reasoningDelta: delta.reasoningDelta,
      })
    }
  }

  buffer += decoder.decode()
  if (buffer.trim()) {
    const parsed = parseSSEChunk(`${buffer}\n\n`)
    for (const frame of parsed.frames) {
      let eventPayload: '[DONE]' | Record<string, unknown>
      try {
        eventPayload = parseSSEData(frame.data)
      } catch {
        throw new Error('Malformed streaming response')
      }
      if (eventPayload === '[DONE]') {
        return { content, reasoningContent, finishReason }
      }

      const errorMessage = extractErrorMessage(eventPayload)
      if (errorMessage) {
        throw new Error(errorMessage)
      }

      const delta = extractStreamDelta(eventPayload)
      content += delta.contentDelta
      reasoningContent += delta.reasoningDelta
      if (delta.finishReason) {
        finishReason = delta.finishReason
      }
      options.onDelta?.({
        contentDelta: delta.contentDelta,
        reasoningDelta: delta.reasoningDelta,
      })
    }
  }

  throw new Error('Streaming response ended before [DONE]')
}

export async function sendPlaygroundImageGeneration(
  keyId: number,
  body: Record<string, unknown>,
  options: {
    signal?: AbortSignal
    referenceImages?: PlaygroundAttachment[]
    mask?: PlaygroundAttachment
  } = {},
): Promise<PlaygroundImageGenerationResponse> {
  const referenceImages = (options.referenceImages ?? []).filter((attachment) => (
    attachment.mimeType.startsWith('image/') && typeof attachment.dataUrl === 'string' && attachment.dataUrl.startsWith('data:image/')
  ))
  const multipartBody = referenceImages.length > 0
    ? buildImageEditFormData(body, referenceImages, options.mask)
    : null
  const response = await authorizedFetch(buildApiUrl(multipartBody ? '/playground/images/edits' : '/playground/images/generations'), {
    method: 'POST',
    signal: options.signal,
    headers: {
      Accept: 'application/json',
      'X-Sub2API-Playground-Key-ID': String(keyId),
      ...(multipartBody ? {} : { 'Content-Type': 'application/json' }),
    },
    body: multipartBody ?? JSON.stringify(body),
  })

  if (!response.ok) {
    throw new Error(await parseFailure(response))
  }

  const payload = await response.json() as Record<string, unknown>
  const errorMessage = extractErrorMessage(payload)
  if (errorMessage) throw new Error(errorMessage)

  const data = Array.isArray(payload.data)
    ? payload.data.filter((item): item is PlaygroundImageGenerationResponse['data'][number] => (
      typeof item === 'object' && item !== null
      && ((typeof (item as { url?: unknown }).url === 'string' && Boolean((item as { url: string }).url.trim()))
        || (typeof (item as { b64_json?: unknown }).b64_json === 'string' && Boolean((item as { b64_json: string }).b64_json.trim())))
    ))
    : []
  if (data.length === 0) throw new Error('No image was returned by the selected model.')
  return {
    created: typeof payload.created === 'number' ? payload.created : undefined,
    data,
  }
}

export async function sendPlaygroundVideoGeneration(
  keyId: number,
  body: Record<string, unknown>,
  options: { signal?: AbortSignal } = {},
): Promise<PlaygroundVideoResponse> {
  const response = await authorizedFetch(buildApiUrl('/playground/videos/generations'), {
    method: 'POST',
    signal: options.signal,
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Sub2API-Playground-Key-ID': String(keyId),
    },
    body: JSON.stringify(body),
  })
  if (!response.ok) throw new Error(await parseFailure(response))
  const payload = await response.json() as Record<string, unknown>
  const errorMessage = extractErrorMessage(payload)
  if (errorMessage) throw new Error(errorMessage)
  const nested = payload.data && typeof payload.data === 'object' ? payload.data as Record<string, unknown> : undefined
  const id = typeof payload.id === 'string'
    ? payload.id
    : typeof payload.request_id === 'string'
      ? payload.request_id
      : typeof nested?.id === 'string'
        ? nested.id
        : typeof nested?.request_id === 'string' ? nested.request_id : ''
  if (!id) throw new Error('No video request id was returned.')
  return {
    id,
    status: typeof payload.status === 'string' ? payload.status : undefined,
    video_url: typeof payload.video_url === 'string' ? payload.video_url : undefined,
    url: typeof payload.url === 'string' ? payload.url : undefined,
  }
}

export async function sendPlaygroundSpeech(
  keyId: number,
  body: Record<string, unknown>,
  options: { signal?: AbortSignal } = {},
): Promise<Blob> {
  const response = await authorizedFetch(buildApiUrl('/playground/audio/speech'), {
    method: 'POST',
    signal: options.signal,
    headers: {
      Accept: 'audio/*',
      'Content-Type': 'application/json',
      'X-Sub2API-Playground-Key-ID': String(keyId),
    },
    body: JSON.stringify(body),
  })
  if (!response.ok) throw new Error(await parseFailure(response))
  return response.blob()
}

export async function sendPlaygroundTranscription(
  keyId: number,
  audio: Blob,
  fileName = 'audio.webm',
  options: { model?: string; language?: string; signal?: AbortSignal } = {},
): Promise<string> {
  const formData = new FormData()
  formData.append('file', audio, fileName)
  if (options.model) formData.append('model', options.model)
  if (options.language) formData.append('language', options.language)
  const response = await authorizedFetch(buildApiUrl('/playground/audio/transcriptions'), {
    method: 'POST',
    signal: options.signal,
    headers: {
      Accept: 'application/json',
      'X-Sub2API-Playground-Key-ID': String(keyId),
    },
    body: formData,
  })
  if (!response.ok) throw new Error(await parseFailure(response))
  const payload = await response.json() as Record<string, unknown>
  const nested = payload.data && typeof payload.data === 'object' ? payload.data as Record<string, unknown> : undefined
  const text = typeof payload.text === 'string' ? payload.text : typeof nested?.text === 'string' ? nested.text : ''
  if (!text.trim()) throw new Error('No transcription text was returned.')
  return text.trim()
}

export async function fetchPlaygroundVideo(keyId: number, requestId: string, options: { signal?: AbortSignal } = {}): Promise<PlaygroundVideoResponse> {
  const response = await authorizedFetch(buildApiUrl(`/playground/videos/${encodeURIComponent(requestId)}`), {
    signal: options.signal,
    headers: { Accept: 'application/json', 'X-Sub2API-Playground-Key-ID': String(keyId) },
  })
  if (!response.ok) throw new Error(await parseFailure(response))
  const payload = await response.json() as Record<string, unknown>
  const errorMessage = extractErrorMessage(payload)
  if (errorMessage) throw new Error(errorMessage)
  const video = payload.video && typeof payload.video === 'object' ? payload.video as Record<string, unknown> : undefined
  return {
    id: requestId,
    status: typeof payload.status === 'string' ? payload.status : undefined,
    video_url: typeof payload.video_url === 'string' ? payload.video_url : typeof video?.url === 'string' ? video.url : undefined,
    url: typeof payload.url === 'string' ? payload.url : undefined,
  }
}

export async function fetchPlaygroundVideoContent(keyId: number, requestId: string, options: { signal?: AbortSignal } = {}): Promise<Blob> {
  const response = await authorizedFetch(buildApiUrl(`/playground/videos/${encodeURIComponent(requestId)}/content`), {
    signal: options.signal,
    headers: { Accept: 'video/*', 'X-Sub2API-Playground-Key-ID': String(keyId) },
  })
  if (!response.ok) throw new Error(await parseFailure(response))
  return response.blob()
}

function buildImageEditFormData(body: Record<string, unknown>, referenceImages: PlaygroundAttachment[], mask?: PlaygroundAttachment): FormData {
  const formData = new FormData()
  for (const [key, value] of Object.entries(body)) {
    if (value !== undefined && value !== null) formData.append(key, String(value))
  }
  for (const attachment of referenceImages) {
    formData.append('image', dataUrlToBlob(attachment.dataUrl as string, attachment.mimeType), attachment.name)
  }
  if (mask?.dataUrl?.startsWith('data:image/')) {
    formData.append('mask', dataUrlToBlob(mask.dataUrl, mask.mimeType), mask.name || 'mask.png')
  }
  return formData
}

function dataUrlToBlob(dataUrl: string, fallbackType: string): Blob {
  const [header, encoded = ''] = dataUrl.split(',', 2)
  const mimeType = /^data:([^;,]+)/i.exec(header)?.[1] || fallbackType
  const binary = atob(encoded)
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
  return new Blob([bytes], { type: mimeType })
}
