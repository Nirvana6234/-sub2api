import { computed, reactive, ref, watch } from 'vue'
import type { CanvasAssistantMessage, CanvasConnection, CanvasImageTransform, CanvasImageVariant, CanvasNode, CanvasPersistedState, CanvasProject, CanvasReferenceImage, CanvasTextVariant, CanvasViewport } from './types'

const STORAGE_PREFIX = 'sub2api.playground.canvas.v1.user.'
const DEFAULT_VIEWPORT: CanvasViewport = { x: 0, y: 0, scale: 1 }
const PERSIST_DEBOUNCE_MS = 180
// 与 PlaygroundCanvasView 的上传上限保持一致，避免两处不同步。
const MAX_REFERENCE_IMAGES = 4

function createId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

function createProject(title = 'Untitled canvas'): CanvasProject {
  const now = Date.now()
  return {
    id: createId('project'),
    title,
    nodes: [],
    connections: [],
    viewport: { ...DEFAULT_VIEWPORT },
    backgroundMode: 'dots',
    assistantMessages: [],
    createdAt: now,
    updatedAt: now,
  }
}

function normalizeAssistantMessages(value: unknown): CanvasAssistantMessage[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((entry) => {
    if (!entry || typeof entry !== 'object') return []
    const source = entry as Partial<CanvasAssistantMessage>
    if (source.role !== 'user' && source.role !== 'assistant') return []
    const content = typeof source.content === 'string' ? source.content.slice(0, 24000) : ''
    return [{
      id: typeof source.id === 'string' && source.id ? source.id : createId('assistant-message'),
      role: source.role,
      content,
      errorMessage: typeof source.errorMessage === 'string' ? source.errorMessage.slice(0, 1000) : undefined,
      createdAt: Number.isFinite(source.createdAt) ? Number(source.createdAt) : Date.now(),
    }]
  }).slice(-100)
}

// 按白名单重建参考图对象：只保留元数据与 IndexedDB 键。
//
// 这一步是刻意的，不要图省事直接透传 source.referenceImages —— 运行时对象上挂着
// dataUrl（base64），一旦被 JSON.stringify 写进 localStorage，几张图就能超出配额。
// 图片本体由 imageCache 存在 IndexedDB 里，这里只留 cacheKey。
function normalizeReferenceImages(value: unknown): CanvasReferenceImage[] | undefined {
  if (!Array.isArray(value)) return undefined
  const items = value.flatMap((entry) => {
    if (!entry || typeof entry !== 'object') return []
    const source = entry as Partial<CanvasReferenceImage>
    const cacheKey = typeof source.cacheKey === 'string' ? source.cacheKey.trim() : ''
    if (!cacheKey) return []
    return [{
      id: typeof source.id === 'string' && source.id.trim() ? source.id.trim() : cacheKey,
      name: typeof source.name === 'string' ? source.name.slice(0, 200) : '',
      mimeType: typeof source.mimeType === 'string' ? source.mimeType.slice(0, 100) : '',
      size: Number.isFinite(source.size) ? Number(source.size) : 0,
      cacheKey: cacheKey.slice(0, 320),
    }]
  }).slice(0, MAX_REFERENCE_IMAGES)
  return items.length ? items : undefined
}

function normalizeTextVariants(value: unknown): CanvasTextVariant[] | undefined {
  if (!Array.isArray(value)) return undefined
  const variants = value.flatMap((entry) => {
    if (!entry || typeof entry !== 'object') return []
    const source = entry as Partial<CanvasTextVariant>
    if (typeof source.content !== 'string') return []
    return [{
      id: typeof source.id === 'string' && source.id.trim() ? source.id.trim().slice(0, 160) : createId('text-variant'),
      status: source.status === 'success' || source.status === 'generating' || source.status === 'error' ? source.status : 'idle',
      content: source.content.slice(0, 24000),
      errorMessage: typeof source.errorMessage === 'string' ? source.errorMessage.slice(0, 1000) : undefined,
    } satisfies CanvasTextVariant]
  }).slice(0, 10)
  return variants.length ? variants : undefined
}

function normalizeImageVariants(value: unknown): CanvasImageVariant[] | undefined {
  if (!Array.isArray(value)) return undefined
  const variants = value.flatMap((entry) => {
    if (!entry || typeof entry !== 'object') return []
    const source = entry as Partial<CanvasImageVariant>
    const status = source.status === 'success' || source.status === 'error' ? source.status : 'pending'
    const url = typeof source.url === 'string' && source.url ? source.url : undefined
    const cacheKey = typeof source.cacheKey === 'string' && source.cacheKey.trim() ? source.cacheKey.trim().slice(0, 320) : undefined
    if (!url && !cacheKey && status === 'success') return []
    return [{
      id: typeof source.id === 'string' && source.id.trim() ? source.id.trim().slice(0, 160) : createId('image-variant'),
      status,
      url,
      cacheKey,
      errorMessage: typeof source.errorMessage === 'string' ? source.errorMessage.slice(0, 1000) : undefined,
    } satisfies CanvasImageVariant]
  }).slice(0, 10)
  return variants.length ? variants : undefined
}

function normalizePluginMetadata(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  const metadata: Record<string, unknown> = {}
  for (const [rawKey, rawValue] of Object.entries(value).slice(0, 30)) {
    const key = rawKey.slice(0, 80)
    if (typeof rawValue === 'string') {
      metadata[key] = rawValue.slice(0, 24000)
      continue
    }
    if (typeof rawValue === 'number' || typeof rawValue === 'boolean') {
      metadata[key] = rawValue
      continue
    }
    // Built-in composer controls are a small scalar map. Keep this one nested
    // value so a canvas reload does not silently reset the user's settings.
    if (key === 'composerOptions' && rawValue && typeof rawValue === 'object' && !Array.isArray(rawValue)) {
      const options: Record<string, string | number | boolean> = {}
      for (const [optionKey, optionValue] of Object.entries(rawValue).slice(0, 12)) {
        if (typeof optionValue === 'string') options[optionKey.slice(0, 80)] = optionValue.slice(0, 240)
        else if (typeof optionValue === 'number' || typeof optionValue === 'boolean') options[optionKey.slice(0, 80)] = optionValue
      }
      if (Object.keys(options).length) metadata[key] = options
    }
  }
  return Object.keys(metadata).length ? metadata : undefined
}

function normalizeNodeConfig(value: unknown): CanvasNode['config'] | undefined {
  if (!value || typeof value !== 'object') return undefined
  const source = value as NonNullable<CanvasNode['config']>
  const config: NonNullable<CanvasNode['config']> = {}
  if (source.mode === 'text' || source.mode === 'video' || source.mode === 'audio' || source.mode === 'image') config.mode = source.mode
  if (typeof source.model === 'string') config.model = source.model.slice(0, 200)
  if (typeof source.count === 'string') config.count = source.count.slice(0, 8)
  if (typeof source.size === 'string') config.size = source.size.slice(0, 32)
  if (typeof source.quality === 'string') config.quality = source.quality.slice(0, 32)
  if (typeof source.background === 'string') config.background = source.background.slice(0, 32)
  if (typeof source.resolution === 'string') config.resolution = source.resolution.slice(0, 32)
  if (typeof source.duration === 'string') config.duration = source.duration.slice(0, 16)
  if (typeof source.aspectRatio === 'string') config.aspectRatio = source.aspectRatio.slice(0, 32)
  if (typeof source.audioVoice === 'string') config.audioVoice = source.audioVoice.slice(0, 32)
  if (typeof source.audioFormat === 'string') config.audioFormat = source.audioFormat.slice(0, 16)
  if (typeof source.audioSpeed === 'string') config.audioSpeed = source.audioSpeed.slice(0, 16)
  if (typeof source.audioInstructions === 'string') config.audioInstructions = source.audioInstructions.slice(0, 4000)
  if (source.reasoningEffort === 'none' || source.reasoningEffort === 'low' || source.reasoningEffort === 'medium' || source.reasoningEffort === 'high') config.reasoningEffort = source.reasoningEffort
  return Object.keys(config).length ? config : undefined
}

function normalizeNode(value: unknown): CanvasNode | null {
  if (!value || typeof value !== 'object') return null
  const source = value as Partial<CanvasNode>
  if (typeof source.id !== 'string' || typeof source.prompt !== 'string') return null
  const transformSource = source.imageTransform as Partial<CanvasImageTransform> | undefined
  return {
    id: source.id,
    type: normalizeNodeType(source.type),
    kind: source.kind === 'result' ? 'result' : 'generator',
    title: typeof source.title === 'string' ? source.title.slice(0, 64) : undefined,
    x: Number.isFinite(source.x) ? Number(source.x) : 80,
    y: Number.isFinite(source.y) ? Number(source.y) : 80,
    width: Number.isFinite(source.width) ? Number(source.width) : 360,
    height: Number.isFinite(source.height) ? Number(source.height) : 420,
    prompt: source.prompt.slice(0, 12000),
    textContent: typeof source.textContent === 'string' ? source.textContent.slice(0, 24000) : undefined,
    textFontSize: Number.isFinite(source.textFontSize) ? Math.min(48, Math.max(10, Math.round(Number(source.textFontSize)))) : 16,
    textCount: Number.isFinite(source.textCount) ? Math.min(10, Math.max(1, Math.floor(Number(source.textCount)))) : 1,
    textVariants: normalizeTextVariants(source.textVariants),
    primaryTextId: typeof source.primaryTextId === 'string' && source.primaryTextId.trim() ? source.primaryTextId.trim().slice(0, 160) : undefined,
    videoUrl: typeof source.videoUrl === 'string' ? source.videoUrl : undefined,
    audioUrl: typeof source.audioUrl === 'string' ? source.audioUrl : undefined,
    pluginId: typeof source.pluginId === 'string' ? source.pluginId.slice(0, 120) : undefined,
    pluginTitle: typeof source.pluginTitle === 'string' ? source.pluginTitle.slice(0, 200) : undefined,
    pluginDescription: typeof source.pluginDescription === 'string' ? source.pluginDescription.slice(0, 1000) : undefined,
    pluginRenderer: source.pluginRenderer === 'markdown' || source.pluginRenderer === 'sticky-note' || source.pluginRenderer === 'svg' || source.pluginRenderer === 'html' || source.pluginRenderer === 'panorama' ? source.pluginRenderer : source.type && typeof source.type === 'string' && source.type.includes(':') ? 'generic' : undefined,
    pluginMetadata: normalizePluginMetadata(source.pluginMetadata),
    pluginHidePanel: Boolean(source.pluginHidePanel),
    pluginAutoOpenPanel: Boolean(source.pluginAutoOpenPanel),
    pluginTransparentBackground: Boolean(source.pluginTransparentBackground),
    pluginInteractionToggle: Boolean(source.pluginInteractionToggle),
    pluginUseBuiltinPanel: source.pluginUseBuiltinPanel && typeof source.pluginUseBuiltinPanel === 'object' && ['image', 'video', 'text', 'audio'].includes(source.pluginUseBuiltinPanel.mode) ? {
      mode: source.pluginUseBuiltinPanel.mode as 'image' | 'video' | 'text' | 'audio',
      promptPrefix: typeof source.pluginUseBuiltinPanel.promptPrefix === 'string' ? source.pluginUseBuiltinPanel.promptPrefix.slice(0, 500) : undefined,
      writeBackToSelf: source.pluginUseBuiltinPanel.writeBackToSelf === true,
    } : undefined,
    config: normalizeNodeConfig(source.config),
    model: typeof source.model === 'string' ? source.model.slice(0, 200) : '',
    imageUrl: typeof source.imageUrl === 'string' ? source.imageUrl : undefined,
    imageCacheKey: typeof source.imageCacheKey === 'string' ? source.imageCacheKey : undefined,
    imageCacheKeys: Array.isArray(source.imageCacheKeys) ? source.imageCacheKeys.filter((item): item is string => typeof item === 'string').slice(0, 10) : undefined,
    videoCacheKey: typeof source.videoCacheKey === 'string' ? source.videoCacheKey : undefined,
    audioCacheKey: typeof source.audioCacheKey === 'string' ? source.audioCacheKey : undefined,
    videoRequestId: typeof source.videoRequestId === 'string' ? source.videoRequestId.slice(0, 320) : undefined,
    imageTransform: transformSource && typeof transformSource === 'object' ? {
      crop: {
        x: Number(transformSource.crop?.x) || 0,
        y: Number(transformSource.crop?.y) || 0,
        width: Number(transformSource.crop?.width) || 1,
        height: Number(transformSource.crop?.height) || 1,
      },
      rotation: [0, 90, 180, 270].includes(Number(transformSource.rotation)) ? Number(transformSource.rotation) as 0 | 90 | 180 | 270 : 0,
      flipX: Boolean(transformSource.flipX),
      flipY: Boolean(transformSource.flipY),
    } : undefined,
    freeResize: source.freeResize === true,
    imageUrls: Array.isArray(source.imageUrls) ? source.imageUrls.filter((item): item is string => typeof item === 'string').slice(0, 10) : undefined,
    imageVariants: normalizeImageVariants(source.imageVariants),
    primaryImageIndex: Number.isFinite(source.primaryImageIndex) ? Math.min(9, Math.max(0, Math.floor(Number(source.primaryImageIndex)))) : 0,
    imageCount: Number.isFinite(source.imageCount) ? Math.min(10, Math.max(1, Math.floor(Number(source.imageCount)))) : 4,
    outputFormat: typeof source.outputFormat === 'string' ? source.outputFormat.slice(0, 16) : undefined,
    sourceNodeId: typeof source.sourceNodeId === 'string' ? source.sourceNodeId : undefined,
    groupId: typeof source.groupId === 'string' && source.groupId ? source.groupId : undefined,
    resultIndex: Number.isFinite(source.resultIndex) ? Math.max(0, Math.floor(Number(source.resultIndex))) : undefined,
    status: source.status === 'success' || source.status === 'generating' || source.status === 'error' ? source.status : 'idle',
    errorMessage: typeof source.errorMessage === 'string' ? source.errorMessage.slice(0, 1000) : undefined,
    referenceImages: normalizeReferenceImages(source.referenceImages),
    createdAt: Number.isFinite(source.createdAt) ? Number(source.createdAt) : Date.now(),
    updatedAt: Number.isFinite(source.updatedAt) ? Number(source.updatedAt) : Date.now(),
  }
}

function normalizeNodeType(value: unknown): CanvasNode['type'] {
  if (value === 'video' || value === 'audio' || value === 'text' || value === 'config' || value === 'image' || value === 'group') return value
  if (typeof value === 'string' && /^[a-z0-9][a-z0-9_-]{0,60}:[a-z0-9][a-z0-9_-]{0,60}$/i.test(value)) return value as CanvasNode['type']
  return 'image'
}

function normalizeProject(value: unknown): CanvasProject | null {
  if (!value || typeof value !== 'object') return null
  const source = value as Partial<CanvasProject>
  if (typeof source.id !== 'string') return null
  const viewport = source.viewport
  return {
    id: source.id,
    title: typeof source.title === 'string' && source.title.trim() ? source.title.slice(0, 80) : 'Untitled canvas',
    nodes: Array.isArray(source.nodes) ? source.nodes.map(normalizeNode).filter((node): node is CanvasNode => node !== null).slice(0, 100) : [],
    connections: Array.isArray(source.connections)
      ? source.connections.filter((connection): connection is CanvasProject['connections'][number] => (
        Boolean(connection)
        && typeof connection === 'object'
        && typeof (connection as CanvasProject['connections'][number]).id === 'string'
        && typeof (connection as CanvasProject['connections'][number]).from === 'string'
        && typeof (connection as CanvasProject['connections'][number]).to === 'string'
      )).slice(0, 200)
      : [],
    viewport: {
      x: viewport && Number.isFinite(viewport.x) ? Number(viewport.x) : 0,
      y: viewport && Number.isFinite(viewport.y) ? Number(viewport.y) : 0,
      scale: viewport && Number.isFinite(viewport.scale) ? Math.min(3, Math.max(0.2, Number(viewport.scale))) : 1,
    },
    backgroundMode: source.backgroundMode === 'lines' || source.backgroundMode === 'blank' ? source.backgroundMode : 'dots',
    assistantMessages: normalizeAssistantMessages(source.assistantMessages),
    createdAt: Number.isFinite(source.createdAt) ? Number(source.createdAt) : Date.now(),
    updatedAt: Number.isFinite(source.updatedAt) ? Number(source.updatedAt) : Date.now(),
  }
}

function readState(userId: number): CanvasPersistedState {
  const fallbackProject = createProject()
  if (typeof localStorage === 'undefined') return { version: 1, activeProjectId: fallbackProject.id, projects: [fallbackProject] }
  try {
    const parsed = JSON.parse(localStorage.getItem(`${STORAGE_PREFIX}${userId}`) || '') as Partial<CanvasPersistedState>
    const projects = Array.isArray(parsed.projects)
      ? parsed.projects.map(normalizeProject).filter((project): project is CanvasProject => project !== null)
      : []
    const safeProjects = projects.length ? projects : [fallbackProject]
    return {
      version: 1,
      activeProjectId: safeProjects.some((project) => project.id === parsed.activeProjectId) ? parsed.activeProjectId ?? null : safeProjects[0].id,
      projects: safeProjects,
    }
  } catch {
    return { version: 1, activeProjectId: fallbackProject.id, projects: [fallbackProject] }
  }
}

export function useCanvasStore(userId: number) {
  const state = reactive(readState(userId))
  const activeProject = computed(() => state.projects.find((project) => project.id === state.activeProjectId) ?? state.projects[0])
  const historyPast = ref<string[]>([])
  const historyFuture = ref<string[]>([])
  let persistTimer: number | null = null

  function snapshot(): string {
    return JSON.stringify(state)
  }

  function checkpoint(): void {
    historyPast.value = [...historyPast.value.slice(-49), snapshot()]
    historyFuture.value = []
  }

  function restore(serialized: string): void {
    const parsed = JSON.parse(serialized) as CanvasPersistedState
    const normalized = readStateFromValue(parsed)
    state.version = normalized.version
    state.activeProjectId = normalized.activeProjectId
    state.projects = normalized.projects
    persist()
  }

  function readStateFromValue(value: unknown): CanvasPersistedState {
    const parsed = value && typeof value === 'object' ? value as Partial<CanvasPersistedState> : {}
    const projects = Array.isArray(parsed.projects)
      ? parsed.projects.map(normalizeProject).filter((project): project is CanvasProject => project !== null)
      : []
    const fallback = createProject()
    const safeProjects = projects.length ? projects : [fallback]
    return {
      version: 1,
      activeProjectId: safeProjects.some((project) => project.id === parsed.activeProjectId) ? parsed.activeProjectId ?? null : safeProjects[0].id,
      projects: safeProjects,
    }
  }

  function undo(): void {
    const previous = historyPast.value.at(-1)
    if (!previous) return
    historyPast.value = historyPast.value.slice(0, -1)
    historyFuture.value = [...historyFuture.value, snapshot()]
    restore(previous)
  }

  function redo(): void {
    const next = historyFuture.value.at(-1)
    if (!next) return
    historyFuture.value = historyFuture.value.slice(0, -1)
    historyPast.value = [...historyPast.value, snapshot()]
    restore(next)
  }

  function persistNow(): void {
    if (typeof localStorage === 'undefined') return
    try {
      localStorage.setItem(`${STORAGE_PREFIX}${userId}`, JSON.stringify(state))
    } catch (error) {
      // persist 挂在 watch(deep) 上，配额超限时抛出会打断响应式更新链，
      // 让画布在用户毫无察觉的情况下停止工作。宁可丢一次持久化也不能中断交互。
      console.warn('[canvas] failed to persist canvas state', error)
    }
  }

  function persist(): void {
    if (persistTimer !== null) {
      clearTimeout(persistTimer)
      persistTimer = null
    }
    persistNow()
  }

  function schedulePersist(): void {
    if (typeof window === 'undefined' || persistTimer !== null) return
    persistTimer = window.setTimeout(() => {
      persistTimer = null
      persistNow()
    }, PERSIST_DEBOUNCE_MS)
  }

  function touch(project: CanvasProject): void {
    project.updatedAt = Date.now()
  }

  function addProject(title = 'Untitled canvas'): CanvasProject {
    const project = createProject(title)
    state.projects.unshift(project)
    state.activeProjectId = project.id
    persist()
    return project
  }

  function duplicateProject(id: string): CanvasProject | null {
    const source = state.projects.find((project) => project.id === id)
    if (!source) return null
    const copy = normalizeProject(JSON.parse(JSON.stringify(source))) ?? createProject(`${source.title} copy`)
    copy.id = createId('project')
    copy.title = `${source.title} copy`.slice(0, 80)
    const nodeIds = new Map<string, string>()
    copy.nodes.forEach((node) => nodeIds.set(node.id, createId(node.type)))
    copy.nodes = copy.nodes.map((node) => {
      const mappedId = nodeIds.get(node.id) ?? createId(node.type)
      return { ...node, id: mappedId, sourceNodeId: node.sourceNodeId ? nodeIds.get(node.sourceNodeId) : undefined, createdAt: Date.now(), updatedAt: Date.now() }
    })
    copy.connections = copy.connections.flatMap((connection) => {
      const from = nodeIds.get(connection.from)
      const to = nodeIds.get(connection.to)
      return from && to ? [{ ...connection, id: createId('connection'), from, to }] : []
    })
    state.projects.unshift(copy)
    state.activeProjectId = copy.id
    persist()
    return copy
  }

  function deleteProject(id: string): void {
    if (state.projects.length <= 1) {
      state.projects[0].nodes = []
      state.projects[0].connections = []
      state.projects[0].title = 'Untitled canvas'
      state.activeProjectId = state.projects[0].id
    } else {
      state.projects = state.projects.filter((project) => project.id !== id)
      if (state.activeProjectId === id) state.activeProjectId = state.projects[0].id
    }
    persist()
  }

  function updateProject(patch: Partial<CanvasProject>): void {
    const project = activeProject.value
    if (!project) return
    Object.assign(project, patch)
    touch(project)
    persist()
  }

  function updateViewport(viewport: CanvasViewport, options?: { checkpoint?: boolean }): void {
    if (options?.checkpoint) checkpoint()
    const project = activeProject.value
    if (!project) return
    project.viewport = viewport
    touch(project)
    schedulePersist()
  }

  function addNode(node: CanvasNode): void {
    const project = activeProject.value
    if (!project) return
    project.nodes.push(node)
    touch(project)
    persist()
  }

  function updateNode(id: string, patch: Partial<CanvasNode>): void {
    const project = activeProject.value
    const node = project?.nodes.find((item) => item.id === id)
    if (!node) return
    Object.assign(node, patch, { updatedAt: Date.now() })
    touch(project)
    schedulePersist()
  }

  function removeNode(id: string): void {
    const project = activeProject.value
    if (!project) return
    const target = project.nodes.find((node) => node.id === id)
    if (target?.type === 'group') {
      for (const node of project.nodes) {
        if (node.groupId === id) node.groupId = undefined
      }
    }
    const removedIds = new Set<string>([id])
    let changed = true
    while (changed) {
      changed = false
      for (const node of project.nodes) {
        if (node.sourceNodeId && removedIds.has(node.sourceNodeId) && !removedIds.has(node.id)) {
          removedIds.add(node.id)
          changed = true
        }
      }
    }
    project.nodes = project.nodes.filter((node) => !removedIds.has(node.id))
    project.connections = project.connections.filter((connection) => (
      !removedIds.has(connection.from) && !removedIds.has(connection.to)
    ))
    touch(project)
    persist()
  }

  function addConnection(connection: CanvasConnection): boolean {
    const project = activeProject.value
    if (!project || connection.from === connection.to) return false
    if (!project.nodes.some((node) => node.id === connection.from) || !project.nodes.some((node) => node.id === connection.to)) return false
    if (project.connections.some((item) => item.from === connection.from && item.to === connection.to)) return false
    project.connections.push(connection)
    touch(project)
    persist()
    return true
  }

  function removeConnection(id: string): void {
    const project = activeProject.value
    if (!project) return
    project.connections = project.connections.filter((connection) => connection.id !== id)
    touch(project)
    persist()
  }

  function removeDerivedNodes(sourceNodeId: string): void {
    const project = activeProject.value
    if (!project) return
    const derivedIds = new Set(project.nodes.filter((node) => node.sourceNodeId === sourceNodeId).map((node) => node.id))
    if (!derivedIds.size) return
    project.nodes = project.nodes.filter((node) => !derivedIds.has(node.id))
    project.connections = project.connections.filter((connection) => (
      !derivedIds.has(connection.from) && !derivedIds.has(connection.to)
    ))
    touch(project)
    persist()
  }

  function clearProject(): void {
    const project = activeProject.value
    if (!project) return
    project.nodes = []
    project.connections = []
    project.viewport = { ...DEFAULT_VIEWPORT }
    touch(project)
    persist()
  }

  function exportState(): string {
    return JSON.stringify(state, null, 2)
  }

  function importProject(json: string): CanvasProject {
    const parsed = JSON.parse(json) as CanvasPersistedState | CanvasProject
    const source = 'projects' in parsed ? parsed.projects?.[0] : parsed
    const project = normalizeProject(source) ?? createProject('Imported canvas')
    project.id = createId('project')
    state.projects.unshift(project)
    state.activeProjectId = project.id
    persist()
    return project
  }

  watch(state, schedulePersist, { deep: true })

  return {
    state,
    activeProject,
    addProject,
    duplicateProject,
    deleteProject,
    updateProject,
    updateViewport,
    addNode,
    updateNode,
    removeNode,
    addConnection,
    removeConnection,
    removeDerivedNodes,
    clearProject,
    exportState,
    importProject,
    persist,
    checkpoint,
    undo,
    redo,
    canUndo: computed(() => historyPast.value.length > 0),
    canRedo: computed(() => historyFuture.value.length > 0),
  }
}

export type CanvasStore = ReturnType<typeof useCanvasStore>
