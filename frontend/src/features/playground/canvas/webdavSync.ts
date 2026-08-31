import type { CanvasPersistedState } from './types'

export interface CanvasWebDavConfig {
  url: string
  username: string
  password: string
  fileName?: string
}

function targetUrl(config: CanvasWebDavConfig): string {
  const base = config.url.trim().replace(/\/+$/, '')
  const fileName = (config.fileName || 'sub2api-canvas.json').replace(/^\/+/, '')
  return `${base}/${fileName}`
}

function headers(config: CanvasWebDavConfig): HeadersInit {
  const value = `${config.username}:${config.password}`
  return { Authorization: `Basic ${btoa(unescape(encodeURIComponent(value)))}` }
}

export async function uploadCanvasState(config: CanvasWebDavConfig, state: CanvasPersistedState, signal?: AbortSignal): Promise<void> {
  if (!config.url.trim()) throw new Error('WebDAV URL is required')
  const response = await fetch(targetUrl(config), {
    method: 'PUT',
    headers: { ...headers(config), 'Content-Type': 'application/json' },
    body: JSON.stringify({ version: 1, exportedAt: new Date().toISOString(), state }),
    signal,
  })
  if (!response.ok) throw new Error(`WebDAV upload failed (${response.status})`)
}

export async function downloadCanvasState(config: CanvasWebDavConfig, signal?: AbortSignal): Promise<CanvasPersistedState> {
  if (!config.url.trim()) throw new Error('WebDAV URL is required')
  const response = await fetch(targetUrl(config), { headers: headers(config), signal })
  if (!response.ok) throw new Error(`WebDAV download failed (${response.status})`)
  const payload = await response.json() as unknown
  if (!payload || typeof payload !== 'object') throw new Error('Invalid canvas backup')
  const record = payload as Record<string, unknown>
  const state = record.state && typeof record.state === 'object' ? record.state as Record<string, unknown> : record
  if (state.version !== 1 || !Array.isArray(state.projects)) throw new Error('Invalid canvas backup')
  return state as unknown as CanvasPersistedState
}
