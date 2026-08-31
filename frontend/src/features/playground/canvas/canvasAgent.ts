import type { CanvasNode, CanvasProject } from './types'
import { isPluginNodeType } from './canvasPlugins'

export interface CanvasAgentSnapshot {
  projectId: string
  title: string
  nodes: Array<{
    id: string
    type: CanvasNode['type']
    title: string
    position: { x: number; y: number }
    width: number
    height: number
    metadata: Record<string, unknown>
  }>
  connections: Array<{ id: string; fromNodeId: string; toNodeId: string }>
  selectedNodeIds: string[]
  viewport: { x: number; y: number; k: number }
}

export interface CanvasAgentToolCall {
  requestId: string
  name: string
  input: Record<string, unknown>
}

export interface CanvasAgentConversation {
  revision: number
  conversationId: string
  threadId: string
  status: 'idle' | 'preparing' | 'ready' | 'warning' | 'running' | 'failed'
  mcpStatuses: Record<string, { status: string; error?: string | null }>
  error?: string
}

export interface CanvasAgentThread {
  id: string
  preview: string
  name?: string | null
  updatedAt?: number
}

export interface CanvasAgentApproval {
  requestId: string
  reason?: string
  method?: string
  command?: unknown
}

export interface CanvasAgentSkill {
  name: string
  description: string
  path: string
  scope?: string
  enabled: boolean
  managed?: boolean
}

export type CanvasAgentEvent = {
  type: 'connected' | 'disconnected' | 'error' | 'hello' | 'codex_state' | 'conversation_changed' | 'chat_message' | 'agent_event' | 'agent_error' | 'codex_approval' | 'codex_approval_resolved' | 'skills_changed'
  payload?: Record<string, unknown>
  message?: string
}

export function normalizeCanvasAgentEndpoint(value: string): string {
  return value.trim().replace(/\/+$/, '')
}

export function createCanvasAgentSnapshot(project: CanvasProject | undefined, selectedNodeIds: Set<string>): CanvasAgentSnapshot | null {
  if (!project) return null
  return {
    projectId: project.id,
    title: project.title,
    nodes: project.nodes.map((node) => ({
      id: node.id,
      type: node.type,
      title: node.title || node.prompt || node.type,
      position: { x: node.x, y: node.y },
      width: node.width,
      height: node.height,
      metadata: {
        title: node.title,
        prompt: node.prompt,
        content: node.textContent,
        model: node.model,
        status: node.status,
        imageUrl: node.imageUrl,
        videoUrl: node.videoUrl,
        audioUrl: node.audioUrl,
        imageUrls: node.imageUrls,
        imageVariants: node.imageVariants,
        sourceNodeId: node.sourceNodeId,
        groupId: node.groupId,
        pluginId: node.pluginId,
        pluginTitle: node.pluginTitle,
        pluginDescription: node.pluginDescription,
        pluginRenderer: node.pluginRenderer,
        pluginMetadata: node.pluginMetadata,
        pluginHidePanel: node.pluginHidePanel,
        pluginAutoOpenPanel: node.pluginAutoOpenPanel,
        pluginTransparentBackground: node.pluginTransparentBackground,
        pluginInteractionToggle: node.pluginInteractionToggle,
        pluginUseBuiltinPanel: node.pluginUseBuiltinPanel,
        ...(node.pluginMetadata ?? {}),
        generationMode: node.type,
        ...(node.config ?? {}),
      },
    })),
    connections: project.connections.map((connection) => ({ id: connection.id, fromNodeId: connection.from, toNodeId: connection.to })),
    selectedNodeIds: [...selectedNodeIds],
    viewport: { x: project.viewport.x, y: project.viewport.y, k: project.viewport.scale },
  }
}

export function agentNodeToCanvasNode(node: Record<string, unknown>, fallbackType: CanvasNode['type'] = 'text'): CanvasNode {
  const metadata = node.metadata && typeof node.metadata === 'object' && !Array.isArray(node.metadata) ? node.metadata as Record<string, unknown> : {}
  const position = node.position && typeof node.position === 'object' ? node.position as Record<string, unknown> : {}
  const rawType = typeof node.type === 'string' ? node.type : ''
  const type = rawType === 'image' || rawType === 'video' || rawType === 'audio' || rawType === 'config' || rawType === 'group' || rawType === 'text' || isPluginNodeType(rawType as CanvasNode['type']) ? rawType as CanvasNode['type'] : fallbackType
  const now = Date.now()
  const prompt = typeof metadata.prompt === 'string' ? metadata.prompt : typeof metadata.content === 'string' ? metadata.content : typeof node.title === 'string' ? node.title : ''
  return {
    id: typeof node.id === 'string' && node.id ? node.id : `${type}-${now}-${Math.random().toString(36).slice(2, 8)}`,
    type,
    kind: 'generator',
    title: typeof node.title === 'string' ? node.title.slice(0, 64) : typeof metadata.title === 'string' ? metadata.title.slice(0, 64) : undefined,
    x: finite(position.x, 80),
    y: finite(position.y, 80),
    width: finite(node.width, type === 'text' ? 340 : type === 'config' ? 300 : type === 'audio' ? 360 : 360),
    height: finite(node.height, type === 'text' ? 300 : type === 'config' ? 260 : type === 'audio' ? 220 : 420),
    prompt,
    textContent: typeof metadata.content === 'string' ? metadata.content : type === 'text' ? prompt : undefined,
    model: typeof metadata.model === 'string' ? metadata.model : '',
    imageUrl: typeof metadata.imageUrl === 'string' ? metadata.imageUrl : undefined,
    videoUrl: typeof metadata.videoUrl === 'string' ? metadata.videoUrl : undefined,
    audioUrl: typeof metadata.audioUrl === 'string' ? metadata.audioUrl : undefined,
    imageUrls: Array.isArray(metadata.imageUrls) ? metadata.imageUrls.filter((item): item is string => typeof item === 'string') : undefined,
    imageVariants: Array.isArray(metadata.imageVariants) ? metadata.imageVariants.filter((item): item is NonNullable<CanvasNode['imageVariants']>[number] => Boolean(item && typeof item === 'object')) : undefined,
    groupId: typeof metadata.groupId === 'string' ? metadata.groupId : undefined,
    config: type === 'config' ? {
      mode: metadata.generationMode === 'text' || metadata.generationMode === 'video' || metadata.generationMode === 'audio' ? metadata.generationMode : 'image',
      model: stringValue(metadata.configModel) || stringValue(metadata.model) || undefined,
      count: stringValue(metadata.count),
      size: stringValue(metadata.size),
      quality: stringValue(metadata.quality),
      background: stringValue(metadata.background),
      resolution: stringValue(metadata.resolution),
      duration: stringValue(metadata.duration),
      aspectRatio: stringValue(metadata.aspectRatio),
      audioVoice: stringValue(metadata.audioVoice),
      audioFormat: stringValue(metadata.audioFormat),
      audioSpeed: stringValue(metadata.audioSpeed),
      audioInstructions: stringValue(metadata.audioInstructions),
    } : undefined,
    status: metadata.status === 'success' || metadata.status === 'generating' || metadata.status === 'error' ? metadata.status : 'idle',
    sourceNodeId: typeof metadata.sourceNodeId === 'string' ? metadata.sourceNodeId : undefined,
    pluginId: typeof metadata.pluginId === 'string' ? metadata.pluginId : undefined,
    pluginTitle: typeof metadata.pluginTitle === 'string' ? metadata.pluginTitle : undefined,
    pluginDescription: typeof metadata.pluginDescription === 'string' ? metadata.pluginDescription : undefined,
    pluginRenderer: metadata.pluginRenderer === 'markdown' || metadata.pluginRenderer === 'sticky-note' || metadata.pluginRenderer === 'svg' || metadata.pluginRenderer === 'html' || metadata.pluginRenderer === 'panorama' ? metadata.pluginRenderer : isPluginNodeType(type) ? 'generic' : undefined,
    pluginMetadata: metadata.pluginMetadata && typeof metadata.pluginMetadata === 'object' && !Array.isArray(metadata.pluginMetadata) ? metadata.pluginMetadata as Record<string, unknown> : undefined,
    pluginHidePanel: Boolean(metadata.pluginHidePanel),
    pluginAutoOpenPanel: Boolean(metadata.pluginAutoOpenPanel),
    pluginTransparentBackground: Boolean(metadata.pluginTransparentBackground),
    pluginInteractionToggle: Boolean(metadata.pluginInteractionToggle),
    pluginUseBuiltinPanel: metadata.pluginUseBuiltinPanel && typeof metadata.pluginUseBuiltinPanel === 'object' ? metadata.pluginUseBuiltinPanel as CanvasNode['pluginUseBuiltinPanel'] : undefined,
    createdAt: now,
    updatedAt: now,
  }
}

export class CanvasAgentBridge {
  private endpoint = ''
  private token = ''
  private clientId = randomId()
  private events: EventSource | null = null
  private hello: Promise<void> | null = null

  get connected(): boolean {
    return this.events?.readyState === EventSource.OPEN
  }

  get connectionInfo(): { endpoint: string; token: string; clientId: string } {
    return { endpoint: this.endpoint, token: this.token, clientId: this.clientId }
  }

  async connect(endpoint: string, token: string, onEvent: (event: CanvasAgentEvent) => void, onToolCall: (call: CanvasAgentToolCall) => Promise<unknown>): Promise<void> {
    this.disconnect()
    this.endpoint = normalizeCanvasAgentEndpoint(endpoint)
    this.token = token.trim()
    if (!this.endpoint || !this.token) throw new Error('Agent 地址和连接令牌不能为空。')
    this.clientId = randomId()
    const params = new URLSearchParams({ token: this.token, clientId: this.clientId })
    const source = new EventSource(`${this.endpoint}/events?${params.toString()}`)
    this.events = source
    this.hello = new Promise<void>((resolve, reject) => {
      const timeout = window.setTimeout(() => reject(new Error('Agent 连接超时。')), 8000)
      source.addEventListener('hello', (event) => {
        window.clearTimeout(timeout)
        const payload = parseEvent(event)
        onEvent({ type: 'hello', payload })
        resolve()
      }, { once: true })
      source.onerror = () => {
        onEvent({ type: 'error', message: '无法连接本地 Canvas Agent。' })
        reject(new Error('无法连接本地 Canvas Agent。'))
      }
    })
    source.addEventListener('tool_call', (event) => {
      const payload = parseEvent(event)
      void onToolCall({ requestId: String(payload.requestId || ''), name: String(payload.name || ''), input: record(payload.input) })
        .then((result) => this.postResult({ requestId: String(payload.requestId || ''), result }))
        .catch((error: unknown) => this.postResult({ requestId: String(payload.requestId || ''), error: error instanceof Error ? error.message : String(error) }))
    })
    for (const type of ['codex_state', 'conversation_changed', 'chat_message', 'agent_event', 'agent_error', 'codex_approval', 'codex_approval_resolved', 'skills_changed'] as const) {
      source.addEventListener(type, (event) => onEvent({ type, payload: parseEvent(event) }))
    }
    source.addEventListener('ping', () => undefined)
    source.addEventListener('error', () => onEvent({ type: 'error', message: 'Agent 连接发生错误。' }))
    source.addEventListener('open', () => onEvent({ type: 'connected' }))
    source.addEventListener('error', () => {
      if (this.events === source && source.readyState === EventSource.CLOSED) {
        onEvent({ type: 'disconnected' })
      }
    })
    await this.hello
  }

  disconnect(): void {
    this.events?.close()
    this.events = null
    this.hello = null
  }

  async publish(snapshot: CanvasAgentSnapshot | null): Promise<boolean> {
    if (!this.endpoint || !this.token || !this.connected) return false
    try {
      const response = await fetch(`${this.endpoint}/canvas/state?token=${encodeURIComponent(this.token)}&clientId=${encodeURIComponent(this.clientId)}`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(snapshot ? { ...snapshot, hasCanvas: true } : { hasCanvas: false }),
      })
      return response.ok
    } catch {
      return false
    }
  }

  async activate(): Promise<void> {
    if (!this.endpoint || !this.token) return
    await fetch(`${this.endpoint}/canvas/activate?token=${encodeURIComponent(this.token)}&clientId=${encodeURIComponent(this.clientId)}`, { method: 'POST' }).catch(() => undefined)
  }

  async startThread(permissionMode: 'request' | 'automatic' | 'full' = 'request'): Promise<Record<string, unknown>> {
    return this.requestJson('/agent/codex/threads/new', { clientId: this.clientId, permissionMode })
  }

  async listThreads(): Promise<Record<string, unknown>> {
    return this.fetchJson('/agent/codex/threads')
  }

  async resumeThread(threadId: string, permissionMode: 'request' | 'automatic' | 'full' = 'request'): Promise<Record<string, unknown>> {
    return this.requestJson(`/agent/codex/threads/${encodeURIComponent(threadId)}/resume`, { clientId: this.clientId, permissionMode })
  }

  async listSkills(forceReload = false): Promise<Record<string, unknown>> {
    return this.fetchJson(`/agent/codex/skills${forceReload ? '?forceReload=1' : ''}`)
  }

  async setSkillEnabled(skill: Pick<CanvasAgentSkill, 'name' | 'path'>, enabled: boolean): Promise<Record<string, unknown>> {
    return this.requestJson(`/agent/codex/skills/${encodeURIComponent(skill.name)}/enabled`, { name: skill.name, path: skill.path, enabled })
  }

  async sendTurn(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.requestJson('/agent/codex/turn', { ...body, clientId: this.clientId })
  }

  async interrupt(threadId = ''): Promise<Record<string, unknown>> {
    return this.requestJson('/agent/codex/interrupt', { threadId })
  }

  async resolveApproval(requestId: string, decision: 'accept' | 'acceptForSession' | 'decline'): Promise<Record<string, unknown>> {
    return this.requestJson('/agent/codex/approval', { requestId, decision })
  }

  private async postResult(body: { requestId: string; result?: unknown; error?: string }): Promise<void> {
    if (!body.requestId || !this.endpoint || !this.token) return
    await fetch(`${this.endpoint}/canvas/result?token=${encodeURIComponent(this.token)}&clientId=${encodeURIComponent(this.clientId)}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    }).catch(() => undefined)
  }

  private async requestJson(path: string, body: Record<string, unknown>): Promise<Record<string, unknown>> {
    if (!this.endpoint || !this.token) throw new Error('Agent 未连接。')
    const response = await fetch(`${this.endpoint}${path}?token=${encodeURIComponent(this.token)}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    })
    const payload = record(await response.json().catch(() => ({})))
    if (!response.ok) throw new Error(typeof payload.error === 'string' ? payload.error : `Agent 请求失败（${response.status}）。`)
    return payload
  }

  private async fetchJson(path: string): Promise<Record<string, unknown>> {
    if (!this.endpoint || !this.token) throw new Error('Agent 未连接。')
    const response = await fetch(`${this.endpoint}${path}${path.includes('?') ? '&' : '?'}token=${encodeURIComponent(this.token)}`)
    const payload = record(await response.json().catch(() => ({})))
    if (!response.ok) throw new Error(typeof payload.error === 'string' ? payload.error : `Agent 请求失败（${response.status}）。`)
    return payload
  }
}

function parseEvent(event: Event): Record<string, unknown> {
  try {
    const data = JSON.parse((event as MessageEvent<string>).data || '{}')
    return record(data)
  } catch {
    return {}
  }
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function finite(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function stringValue(value: unknown): string | undefined {
  return typeof value === 'string' && value ? value : undefined
}

function randomId(): string {
  return typeof crypto?.randomUUID === 'function' ? crypto.randomUUID() : `canvas-client-${Date.now()}-${Math.random().toString(36).slice(2)}`
}
