import type {
  PlaygroundConversation,
  PlaygroundConversationSummary,
  PlaygroundMessage,
  PlaygroundParameterEnabledState,
  PlaygroundParameters,
  PlaygroundPersistedConversationV2,
  PlaygroundPersistedIndexV2,
  PlaygroundPersistedState,
  PlaygroundPersistedStateV1,
  PlaygroundProject,
  PlaygroundMode,
} from './types'
import {
  cloneParameters,
  createConversation,
  createEmptyPersistedState,
  createProject,
  DEFAULT_PLAYGROUND_PARAMETER_ENABLED,
  DEFAULT_PLAYGROUND_PARAMETERS,
  deriveConversationTitle,
  normalizeConversationTitle,
  normalizeMessageText,
  normalizePersistedMessages,
  normalizeProjectName,
  PLAYGROUND_LEGACY_STORAGE_VERSION,
  PLAYGROUND_MAX_CONVERSATIONS,
  PLAYGROUND_MAX_DRAFT_CHARS,
  PLAYGROUND_MAX_MODEL_CHARS,
  PLAYGROUND_MAX_PERSIST_BYTES,
  PLAYGROUND_MAX_PROJECTS,
  PLAYGROUND_MAX_SYSTEM_PROMPT_CHARS,
  PLAYGROUND_MAX_TOTAL_PERSIST_BYTES,
  PLAYGROUND_STORAGE_VERSION,
} from './viewModel'

export const PLAYGROUND_CHAT_RETENTION_MS = 30 * 24 * 60 * 60 * 1000

function storageKeyPrefix(userId?: number, version = PLAYGROUND_STORAGE_VERSION): string {
  const userSegment = typeof userId === 'number' && userId > 0 ? `user.${userId}.` : ''
  return `sub2api.playground.v${version}.${userSegment}`
}

function userStatePrefix(userId: number, version = PLAYGROUND_STORAGE_VERSION): string {
  return storageKeyPrefix(userId, version).replace(/\.$/, '')
}

function legacyKeyStatePrefix(userId: number, keyId: number, version = PLAYGROUND_STORAGE_VERSION): string {
  return `${storageKeyPrefix(userId, version)}key.${keyId}`
}

function legacyStateKey(userId: number, keyId: number): string {
  return legacyKeyStatePrefix(userId, keyId, PLAYGROUND_LEGACY_STORAGE_VERSION)
}

function indexStorageKey(userId: number): string {
  return `${userStatePrefix(userId)}.index`
}

function conversationStorageKey(userId: number, conversationId: string): string {
  return `${userStatePrefix(userId)}.conversation.${conversationId}`
}

function legacyV2IndexStorageKey(userId: number, keyId: number): string {
  return `${legacyKeyStatePrefix(userId, keyId)}.index`
}

function legacyV2ConversationStorageKey(userId: number, keyId: number, conversationId: string): string {
  return `${legacyKeyStatePrefix(userId, keyId)}.conversation.${conversationId}`
}

function byteLength(value: unknown): number {
  return new TextEncoder().encode(JSON.stringify(value)).length
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isStorageId(value: unknown): value is string {
  return typeof value === 'string' && value.trim().length > 0 && value.trim().length <= 160
}

function normalizeTimestamp(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function clampNumber(value: unknown, min: number, max: number, fallback: number): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) return fallback
  return Math.min(max, Math.max(min, value))
}

function parseNullableInt(value: unknown): number | null {
  if (typeof value !== 'number' || !Number.isFinite(value)) return null
  return Math.trunc(value)
}

function normalizeEnabledFlags(source: Partial<PlaygroundParameters>): PlaygroundParameterEnabledState {
  const enabledSource = isRecord(source.enabled) ? source.enabled : null

  return {
    temperature: typeof enabledSource?.temperature === 'boolean'
      ? enabledSource.temperature
      : DEFAULT_PLAYGROUND_PARAMETER_ENABLED.temperature,
    top_p: typeof enabledSource?.top_p === 'boolean'
      ? enabledSource.top_p
      : DEFAULT_PLAYGROUND_PARAMETER_ENABLED.top_p,
    max_tokens: typeof enabledSource?.max_tokens === 'boolean'
      ? enabledSource.max_tokens
      : typeof source.max_tokens === 'number' && Number.isFinite(source.max_tokens) && Math.trunc(source.max_tokens) > 0,
    frequency_penalty: typeof enabledSource?.frequency_penalty === 'boolean'
      ? enabledSource.frequency_penalty
      : DEFAULT_PLAYGROUND_PARAMETER_ENABLED.frequency_penalty,
    presence_penalty: typeof enabledSource?.presence_penalty === 'boolean'
      ? enabledSource.presence_penalty
      : DEFAULT_PLAYGROUND_PARAMETER_ENABLED.presence_penalty,
    seed: typeof enabledSource?.seed === 'boolean'
      ? enabledSource.seed
      : typeof source.seed === 'number' && Number.isFinite(source.seed),
  }
}

function normalizeParameters(value: unknown): PlaygroundParameters {
  const source = (isRecord(value) ? value : {}) as Partial<PlaygroundParameters>
  return {
    temperature: clampNumber(source.temperature, 0, 2, DEFAULT_PLAYGROUND_PARAMETERS.temperature),
    top_p: clampNumber(source.top_p, 0, 1, DEFAULT_PLAYGROUND_PARAMETERS.top_p),
    max_tokens: parseNullableInt(source.max_tokens),
    frequency_penalty: clampNumber(source.frequency_penalty, -2, 2, DEFAULT_PLAYGROUND_PARAMETERS.frequency_penalty),
    presence_penalty: clampNumber(source.presence_penalty, -2, 2, DEFAULT_PLAYGROUND_PARAMETERS.presence_penalty),
    seed: parseNullableInt(source.seed),
    stream: typeof source.stream === 'boolean' ? source.stream : DEFAULT_PLAYGROUND_PARAMETERS.stream,
    enabled: normalizeEnabledFlags(source),
  }
}

function normalizeMessages(value: unknown): PlaygroundMessage[] {
  return Array.isArray(value) ? normalizePersistedMessages(value as PlaygroundMessage[]) : []
}

function normalizeProjects(value: unknown): PlaygroundProject[] {
  if (!Array.isArray(value)) return []

  const seen = new Set<string>()
  return value
    .flatMap((entry) => {
      if (!isRecord(entry) || !isStorageId(entry.id) || typeof entry.name !== 'string') return []
      const project = createProject(normalizeProjectName(entry.name), {
        id: entry.id.trim(),
        createdAt: normalizeTimestamp(entry.createdAt, Date.now()),
        updatedAt: normalizeTimestamp(entry.updatedAt, Date.now()),
      })
      if (!project.name || seen.has(project.id)) return []
      seen.add(project.id)
      return [project]
    })
    .sort((left, right) => right.updatedAt - left.updatedAt)
    .slice(0, PLAYGROUND_MAX_PROJECTS)
}

function normalizeConversation(value: unknown, projectIds: Set<string>): PlaygroundConversation | null {
  if (!isRecord(value) || !isStorageId(value.id)) return null

  const messages = normalizeMessages(value.messages)
  const systemPrompt = typeof value.systemPrompt === 'string'
    ? normalizeMessageText(value.systemPrompt, PLAYGROUND_MAX_SYSTEM_PROMPT_CHARS)
    : ''
  const draftContent = typeof value.draftContent === 'string'
    ? normalizeMessageText(value.draftContent, PLAYGROUND_MAX_DRAFT_CHARS)
    : ''
  const firstUserMessage = messages.find((message) => message.role === 'user')?.content ?? ''
  const title = typeof value.title === 'string'
    ? normalizeConversationTitle(value.title) || deriveConversationTitle(firstUserMessage || systemPrompt)
    : deriveConversationTitle(firstUserMessage || systemPrompt)
  const latestMessageAt = messages.reduce((latest, message) => Math.max(latest, message.updatedAt), 0)
  const createdAt = normalizeTimestamp(value.createdAt, Date.now())
  const updatedAt = Math.max(normalizeTimestamp(value.updatedAt, createdAt), latestMessageAt)
  const projectId = isStorageId(value.projectId) && projectIds.has(value.projectId.trim())
    ? value.projectId.trim()
    : null

  return createConversation({
    id: value.id.trim(),
    mode: value.mode === 'chat' ? 'chat' : 'image',
    projectId,
    title,
    systemPrompt,
    draftContent,
    messages,
    keyId: typeof value.keyId === 'number' ? value.keyId : undefined,
    model: typeof value.model === 'string' ? value.model : undefined,
    parameters: isRecord(value.parameters) ? normalizeParameters(value.parameters) : undefined,
    imageSize: typeof value.imageSize === 'string' ? value.imageSize : undefined,
    imageCustomWidth: typeof value.imageCustomWidth === 'number' ? value.imageCustomWidth : undefined,
    imageCustomHeight: typeof value.imageCustomHeight === 'number' ? value.imageCustomHeight : undefined,
    imageQuality: typeof value.imageQuality === 'string' ? value.imageQuality : undefined,
    imageCount: typeof value.imageCount === 'number' ? value.imageCount : undefined,
    imageOutputFormat: typeof value.imageOutputFormat === 'string' ? value.imageOutputFormat : undefined,
    imageOutputCompression: typeof value.imageOutputCompression === 'number' ? value.imageOutputCompression : undefined,
    imageBackground: typeof value.imageBackground === 'string' ? value.imageBackground : undefined,
    imageModeration: typeof value.imageModeration === 'string' ? value.imageModeration : undefined,
    imageInputFidelity: typeof value.imageInputFidelity === 'string' ? value.imageInputFidelity : undefined,
    imageStyle: typeof value.imageStyle === 'string' ? value.imageStyle : undefined,
    createdAt,
    updatedAt,
  })
}

function normalizeConversationSummaries(value: unknown, projectIds: Set<string>): PlaygroundConversationSummary[] {
  if (!Array.isArray(value)) return []

  const seen = new Set<string>()
  return value
    .flatMap((entry) => {
      if (!isRecord(entry) || !isStorageId(entry.id)) return []
      const id = entry.id.trim()
      if (seen.has(id)) return []
      seen.add(id)
      const createdAt = normalizeTimestamp(entry.createdAt, Date.now())
      const mode: PlaygroundMode = entry.mode === 'chat' ? 'chat' : 'image'
      return [{
        id,
        mode,
        projectId: isStorageId(entry.projectId) && projectIds.has(entry.projectId.trim())
          ? entry.projectId.trim()
          : null,
        title: typeof entry.title === 'string' ? normalizeConversationTitle(entry.title) : '',
        createdAt,
        updatedAt: normalizeTimestamp(entry.updatedAt, createdAt),
      }]
    })
    .sort((left, right) => right.updatedAt - left.updatedAt)
    .slice(0, PLAYGROUND_MAX_CONVERSATIONS)
}

function normalizePersistedStateV2(value: unknown): PlaygroundPersistedState {
  const source = isRecord(value) ? value : {}
  const projects = normalizeProjects(source.projects)
  const projectIds = new Set(projects.map((project) => project.id))
  const conversations = (Array.isArray(source.conversations) ? source.conversations : [])
    .flatMap((conversation) => normalizeConversation(conversation, projectIds) ?? [])
    .filter((conversation) => (
      (conversation.mode !== 'chat' || conversation.updatedAt >= Date.now() - PLAYGROUND_CHAT_RETENTION_MS)
      && Boolean(conversation.messages.length || conversation.draftContent.trim() || conversation.systemPrompt.trim())
    ))
    .sort((left, right) => right.updatedAt - left.updatedAt)
    .slice(0, PLAYGROUND_MAX_CONVERSATIONS)
  const requestedActiveConversationId = isStorageId(source.activeConversationId)
    ? source.activeConversationId.trim()
    : null
  const activeConversationId = requestedActiveConversationId
    && conversations.some((conversation) => conversation.id === requestedActiveConversationId)
    ? requestedActiveConversationId
    : conversations[0]?.id ?? null

  return {
    version: PLAYGROUND_STORAGE_VERSION,
    model: typeof source.model === 'string'
      ? normalizeMessageText(source.model, PLAYGROUND_MAX_MODEL_CHARS)
      : '',
    parameters: normalizeParameters(source.parameters),
    activeConversationId,
    projects,
    conversations,
  }
}

function migrateLegacyState(value: unknown): PlaygroundPersistedState {
  const source = (isRecord(value) ? value : {}) as Partial<PlaygroundPersistedStateV1>
  const messages = normalizeMessages(source.messages)
  const leadingSystemMessages: string[] = []
  while (messages[0]?.role === 'system') {
    leadingSystemMessages.push(messages.shift()!.content)
  }

  const legacyDraft = typeof source.draftContent === 'string'
    ? normalizeMessageText(source.draftContent, PLAYGROUND_MAX_DRAFT_CHARS)
    : ''
  const systemDraft = source.draftRole === 'system' ? legacyDraft : ''
  const systemPrompt = normalizeMessageText(
    [...leadingSystemMessages, systemDraft].filter(Boolean).join('\n\n'),
    PLAYGROUND_MAX_SYSTEM_PROMPT_CHARS,
  )
  const draftContent = source.draftRole === 'system' ? '' : legacyDraft
  const firstUserMessage = messages.find((message) => message.role === 'user')?.content ?? ''
  const hasConversation = messages.length > 0 || Boolean(systemPrompt) || Boolean(draftContent)
  const conversation = hasConversation
    ? createConversation({
      title: deriveConversationTitle(firstUserMessage || systemPrompt),
      systemPrompt,
      draftContent,
      messages,
    })
    : null

  return {
    version: PLAYGROUND_STORAGE_VERSION,
    model: typeof source.model === 'string'
      ? normalizeMessageText(source.model, PLAYGROUND_MAX_MODEL_CHARS)
      : '',
    parameters: normalizeParameters(source.parameters),
    activeConversationId: conversation?.id ?? null,
    projects: [],
    conversations: conversation ? [conversation] : [],
  }
}

function normalizeIndex(value: unknown): PlaygroundPersistedIndexV2 {
  const source = isRecord(value) ? value : {}
  const projects = normalizeProjects(source.projects)
  const projectIds = new Set(projects.map((project) => project.id))
  const conversations = normalizeConversationSummaries(source.conversations, projectIds)
  const requestedActiveConversationId = isStorageId(source.activeConversationId)
    ? source.activeConversationId.trim()
    : null
  const activeConversationId = requestedActiveConversationId
    && conversations.some((conversation) => conversation.id === requestedActiveConversationId)
    ? requestedActiveConversationId
    : conversations[0]?.id ?? null

  return {
    version: PLAYGROUND_STORAGE_VERSION,
    model: typeof source.model === 'string'
      ? normalizeMessageText(source.model, PLAYGROUND_MAX_MODEL_CHARS)
      : '',
    parameters: normalizeParameters(source.parameters),
    activeConversationId,
    projects,
    conversations,
  }
}

function toPersistedIndex(state: PlaygroundPersistedState): PlaygroundPersistedIndexV2 {
  return {
    version: PLAYGROUND_STORAGE_VERSION,
    model: state.model,
    parameters: cloneParameters(state.parameters),
    activeConversationId: state.activeConversationId,
    projects: state.projects.map((project) => ({ ...project })),
    conversations: state.conversations.map((conversation) => ({
      id: conversation.id,
      mode: conversation.mode,
      projectId: conversation.projectId,
      title: conversation.title,
      createdAt: conversation.createdAt,
      updatedAt: conversation.updatedAt,
    })),
  }
}

function toPersistedConversation(conversation: PlaygroundConversation): PlaygroundPersistedConversationV2 {
  return {
    version: PLAYGROUND_STORAGE_VERSION,
    ...createConversation(conversation),
  }
}

function trimConversationToFit(conversation: PlaygroundConversation): PlaygroundConversation {
  const working = createConversation(conversation)

  while (byteLength(working) > PLAYGROUND_MAX_PERSIST_BYTES && working.messages.length > 0) {
    working.messages.shift()
  }

  if (byteLength(working) > PLAYGROUND_MAX_PERSIST_BYTES) {
    working.draftContent = ''
  }

  if (byteLength(working) > PLAYGROUND_MAX_PERSIST_BYTES) {
    working.systemPrompt = ''
  }

  if (byteLength(working) > PLAYGROUND_MAX_PERSIST_BYTES) {
    return createConversation({
      id: working.id,
      projectId: working.projectId,
      title: working.title,
      createdAt: working.createdAt,
      updatedAt: working.updatedAt,
    })
  }

  return working
}

function trimToFit(state: PlaygroundPersistedState): PlaygroundPersistedState {
  const normalized = normalizePersistedStateV2(state)
  const trimmed = normalizePersistedStateV2({
    ...normalized,
    conversations: normalized.conversations.map(trimConversationToFit),
  })

  // Keep the complete browser snapshot below a predictable quota. Conversations
  // are already newest-first, so dropping from the end retains recent work.
  while (byteLength(trimmed) > PLAYGROUND_MAX_TOTAL_PERSIST_BYTES && trimmed.conversations.length > 0) {
    trimmed.conversations.pop()
  }

  return normalizePersistedStateV2(trimmed)
}

function loadV2State(userId: number): PlaygroundPersistedState | null {
  const rawIndex = localStorage.getItem(indexStorageKey(userId))
  if (!rawIndex) return null

  try {
    const index = normalizeIndex(JSON.parse(rawIndex) as unknown)
    const projectIds = new Set(index.projects.map((project) => project.id))
    const conversations = index.conversations.flatMap((summary) => {
      const rawConversation = localStorage.getItem(conversationStorageKey(userId, summary.id))
      if (!rawConversation) return []

      try {
        const conversation = normalizeConversation(JSON.parse(rawConversation) as unknown, projectIds)
        if (!conversation || conversation.id !== summary.id) return []
        return [{
          ...conversation,
          projectId: summary.projectId,
          title: summary.title || conversation.title,
          createdAt: summary.createdAt,
          updatedAt: Math.max(summary.updatedAt, conversation.updatedAt),
        }]
      } catch {
        return []
      }
    })

    return trimToFit({
      version: PLAYGROUND_STORAGE_VERSION,
      model: index.model,
      parameters: index.parameters,
      activeConversationId: index.activeConversationId,
      projects: index.projects,
      conversations,
    })
  } catch {
    removeUserState(userId, PLAYGROUND_STORAGE_VERSION)
    return null
  }
}

function existingConversationIds(userId: number): string[] {
  const raw = localStorage.getItem(indexStorageKey(userId))
  if (!raw) return []

  try {
    return normalizeIndex(JSON.parse(raw) as unknown).conversations.map((conversation) => conversation.id)
  } catch {
    return []
  }
}

function removeUserState(userId: number, version: number): void {
  const prefix = userStatePrefix(userId, version)
  const keys = Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index))
    .filter((key): key is string => typeof key === 'string' && (key === prefix || key.startsWith(`${prefix}.`)))

  for (const key of keys) {
    localStorage.removeItem(key)
  }
}

function loadLegacyV2State(userId: number, keyId: number): PlaygroundPersistedState | null {
  const rawIndex = localStorage.getItem(legacyV2IndexStorageKey(userId, keyId))
  if (!rawIndex) return null

  try {
    const index = normalizeIndex(JSON.parse(rawIndex) as unknown)
    const projectIds = new Set(index.projects.map((project) => project.id))
    const conversations = index.conversations.flatMap((summary) => {
      const rawConversation = localStorage.getItem(legacyV2ConversationStorageKey(userId, keyId, summary.id))
      if (!rawConversation) return []
      try {
        const conversation = normalizeConversation(JSON.parse(rawConversation) as unknown, projectIds)
        if (!conversation || conversation.id !== summary.id) return []
        return [{
          ...conversation,
          projectId: summary.projectId,
          title: summary.title || conversation.title,
          createdAt: summary.createdAt,
          updatedAt: Math.max(summary.updatedAt, conversation.updatedAt),
        }]
      } catch {
        return []
      }
    })
    return trimToFit({
      version: PLAYGROUND_STORAGE_VERSION,
      model: index.model,
      parameters: index.parameters,
      activeConversationId: index.activeConversationId,
      projects: index.projects,
      conversations,
    })
  } catch {
    return null
  }
}

function legacyV2KeyIds(userId: number): number[] {
  const prefix = `${storageKeyPrefix(userId)}key.`
  const keyIds = new Set<number>()
  for (const key of Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index))) {
    if (!key?.startsWith(prefix) || !key.endsWith('.index')) continue
    const keyId = Number(key.slice(prefix.length, -'.index'.length))
    if (Number.isInteger(keyId) && keyId > 0) keyIds.add(keyId)
  }
  return [...keyIds]
}

export function mergePlaygroundStates(states: PlaygroundPersistedState[]): PlaygroundPersistedState {
  if (states.length === 0) return createEmptyPersistedState()
  const newestFirst = [...states].sort((left, right) => {
    const latest = (state: PlaygroundPersistedState) => Math.max(
      0,
      ...state.projects.map((project) => project.updatedAt),
      ...state.conversations.map((conversation) => conversation.updatedAt),
    )
    return latest(right) - latest(left)
  })
  const projects = new Map<string, PlaygroundProject>()
  const conversations = new Map<string, PlaygroundConversation>()
  for (const state of newestFirst) {
    for (const project of state.projects) {
      if (!projects.has(project.id)) projects.set(project.id, project)
    }
    for (const conversation of state.conversations) {
      if (!conversations.has(conversation.id)) conversations.set(conversation.id, conversation)
    }
  }
  const newest = newestFirst[0]
  return trimToFit({
    version: PLAYGROUND_STORAGE_VERSION,
    model: newest.model,
    parameters: newest.parameters,
    activeConversationId: newest.activeConversationId,
    projects: [...projects.values()],
    conversations: [...conversations.values()],
  })
}

export function normalizePersistedState(value: unknown): PlaygroundPersistedState {
  const source = isRecord(value) ? value : {}
  if (source.version === PLAYGROUND_LEGACY_STORAGE_VERSION || Array.isArray(source.messages)) {
    return migrateLegacyState(source)
  }
  return normalizePersistedStateV2(source)
}

type PlaygroundStatePersister = (
  userId: number,
  keyId: number,
  state: PlaygroundPersistedState,
) => unknown

interface PendingPlaygroundPersist {
  userId: number
  keyId: number
  state: PlaygroundPersistedState
}

export interface PlaygroundPersistScheduler {
  schedule(userId: number, keyId: number, state: PlaygroundPersistedState): void
  flush(): void
  cancel(): void
}

export function createPlaygroundPersistScheduler(
  delayMs = 150,
  persist: PlaygroundStatePersister = savePlaygroundState,
): PlaygroundPersistScheduler {
  let timer: ReturnType<typeof setTimeout> | null = null
  let pending: PendingPlaygroundPersist | null = null

  function clearTimer(): void {
    if (timer) clearTimeout(timer)
    timer = null
  }

  function persistPending(): void {
    if (!pending) return
    const task = pending
    pending = null
    timer = null
    void persist(task.userId, task.keyId, task.state)
  }

  return {
    schedule(userId, keyId, state) {
      clearTimer()
      pending = {
        userId,
        keyId,
        state: normalizePersistedState(state),
      }
      timer = setTimeout(persistPending, delayMs)
    },
    flush() {
      clearTimer()
      persistPending()
    },
    cancel() {
      clearTimer()
      pending = null
    },
  }
}

export function loadPlaygroundState(userId: number, keyId: number): PlaygroundPersistedState {
  const v2State = loadV2State(userId)
  if (v2State) return v2State

  const oldV2States = legacyV2KeyIds(userId)
    .map((oldKeyId) => loadLegacyV2State(userId, oldKeyId))
    .filter((state): state is PlaygroundPersistedState => state !== null)
  if (oldV2States.length > 0) {
    const migrated = mergePlaygroundStates(oldV2States)
    savePlaygroundState(userId, keyId, migrated)
    return migrated
  }

  const legacyKey = legacyStateKey(userId, keyId)
  const legacyRaw = localStorage.getItem(legacyKey)
  if (!legacyRaw) return createEmptyPersistedState()

  try {
    const migrated = trimToFit(normalizePersistedState(JSON.parse(legacyRaw) as unknown))
    savePlaygroundState(userId, keyId, migrated)
    if (localStorage.getItem(indexStorageKey(userId))) {
      localStorage.removeItem(legacyKey)
    }
    return migrated
  } catch {
    localStorage.removeItem(legacyKey)
    return createEmptyPersistedState()
  }
}

export function savePlaygroundState(
  userId: number,
  keyId: number,
  state: PlaygroundPersistedState,
): PlaygroundPersistedState {
  const normalized = trimToFit(state)
  const previousConversationIds = existingConversationIds(userId)

  try {
    for (const conversation of normalized.conversations) {
      localStorage.setItem(
        conversationStorageKey(userId, conversation.id),
        JSON.stringify(toPersistedConversation(conversation)),
      )
    }

    localStorage.setItem(indexStorageKey(userId), JSON.stringify(toPersistedIndex(normalized)))

    const nextConversationIds = new Set(normalized.conversations.map((conversation) => conversation.id))
    for (const conversationId of previousConversationIds) {
      if (!nextConversationIds.has(conversationId)) {
        localStorage.removeItem(conversationStorageKey(userId, conversationId))
      }
    }

    localStorage.removeItem(legacyStateKey(userId, keyId))
  } catch {
    // LocalStorage can be disabled or quota-limited. The in-memory chat stays usable.
  }

  return normalized
}

export function clearPlaygroundState(userId: number, keyId: number): void {
  void keyId
  removeUserState(userId, PLAYGROUND_STORAGE_VERSION)
  removeUserState(userId, PLAYGROUND_LEGACY_STORAGE_VERSION)
}

export function clearPlaygroundStatesForUser(userId: number): void {
  const prefixes = [
    storageKeyPrefix(userId, PLAYGROUND_STORAGE_VERSION),
    storageKeyPrefix(userId, PLAYGROUND_LEGACY_STORAGE_VERSION),
  ]
  const keys = Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index))
    .filter((key): key is string => typeof key === 'string' && prefixes.some((prefix) => key.startsWith(prefix)))

  for (const key of keys) {
    localStorage.removeItem(key)
  }
}

export function clearAllPlaygroundStates(): void {
  const prefix = 'sub2api.playground.'
  const keys = Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index))
    .filter((key): key is string => typeof key === 'string' && key.startsWith(prefix))

  for (const key of keys) {
    localStorage.removeItem(key)
  }
}

export function toPersistedState(input: {
  model: string
  parameters: PlaygroundParameters
  activeConversationId: string | null
  projects: PlaygroundProject[]
  conversations: PlaygroundConversation[]
}): PlaygroundPersistedState {
  return normalizePersistedState({
    version: PLAYGROUND_STORAGE_VERSION,
    model: input.model,
    parameters: cloneParameters(input.parameters),
    activeConversationId: input.activeConversationId,
    projects: input.projects,
    conversations: input.conversations,
  })
}
