import type { ApiKey } from '@/types'
import type {
  PlaygroundChatPayload,
  PlaygroundAttachment,
  PlaygroundConversation,
  PlaygroundKeySummary,
  PlaygroundMessage,
  PlaygroundMode,
  PlaygroundModel,
  PlaygroundParameterEnabledState,
  PlaygroundParameters,
  PlaygroundPersistedState,
  PlaygroundProject,
  PlaygroundRole,
} from './types'

export const PLAYGROUND_STORAGE_VERSION = 2
export const PLAYGROUND_LEGACY_STORAGE_VERSION = 1
export const PLAYGROUND_MAX_PERSIST_BYTES = 96 * 1024
export const PLAYGROUND_MAX_TOTAL_PERSIST_BYTES = 1024 * 1024
export const PLAYGROUND_MAX_MESSAGES = 48
export const PLAYGROUND_MAX_MESSAGE_CHARS = 12000
export const PLAYGROUND_MAX_CONVERSATIONS = 48
export const PLAYGROUND_MAX_PROJECTS = 24
export const PLAYGROUND_MAX_PROJECT_NAME_CHARS = 80
export const PLAYGROUND_MAX_CONVERSATION_TITLE_CHARS = 80
export const PLAYGROUND_MAX_SYSTEM_PROMPT_CHARS = 8000
export const PLAYGROUND_MAX_DRAFT_CHARS = 8000
export const PLAYGROUND_MAX_ATTACHMENTS = 10
export const PLAYGROUND_MAX_ATTACHMENT_BYTES = 8 * 1024 * 1024
export const PLAYGROUND_MAX_MODEL_CHARS = 200
export const PLAYGROUND_MAX_IMAGE_COUNT = 10

export const DEFAULT_PLAYGROUND_PARAMETER_ENABLED: PlaygroundParameterEnabledState = {
  temperature: true,
  top_p: true,
  max_tokens: false,
  frequency_penalty: true,
  presence_penalty: true,
  seed: false,
}

export const DEFAULT_PLAYGROUND_PARAMETERS: PlaygroundParameters = {
  temperature: 0.7,
  top_p: 1,
  max_tokens: null,
  frequency_penalty: 0,
  presence_penalty: 0,
  seed: null,
  stream: true,
  enabled: { ...DEFAULT_PLAYGROUND_PARAMETER_ENABLED },
}

function createEntityId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

function normalizeTimestamp(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

export function normalizeMessageText(value: string, maxLength: number): string {
  return value.trim().slice(0, maxLength)
}

export function normalizeProjectName(value: string): string {
  return normalizeMessageText(value.replace(/\s+/g, ' '), PLAYGROUND_MAX_PROJECT_NAME_CHARS)
}

export function normalizeConversationTitle(value: string): string {
  return normalizeMessageText(value.replace(/\s+/g, ' '), PLAYGROUND_MAX_CONVERSATION_TITLE_CHARS)
}

export function deriveConversationTitle(value: string): string {
  return normalizeConversationTitle(value)
}

export function createMessage(
  role: PlaygroundRole,
  content: string,
  overrides: Partial<PlaygroundMessage> = {},
): PlaygroundMessage {
  const now = Date.now()
  return {
    id: overrides.id ?? createEntityId(role),
    role,
    content,
    reasoningContent: overrides.reasoningContent,
    errorMessage: overrides.errorMessage,
    imageUrls: overrides.imageUrls,
    imageCacheKeys: overrides.imageCacheKeys,
    imageOutputFormat: overrides.imageOutputFormat,
    revisedPrompt: overrides.revisedPrompt,
    attachments: overrides.attachments?.slice(0, PLAYGROUND_MAX_ATTACHMENTS),
    createdAt: normalizeTimestamp(overrides.createdAt, now),
    updatedAt: normalizeTimestamp(overrides.updatedAt, now),
  }
}

export function createProject(
  name: string,
  overrides: Partial<PlaygroundProject> = {},
): PlaygroundProject {
  const now = Date.now()
  return {
    id: typeof overrides.id === 'string' && overrides.id.trim() ? overrides.id.trim() : createEntityId('project'),
    name: normalizeProjectName(name),
    createdAt: normalizeTimestamp(overrides.createdAt, now),
    updatedAt: normalizeTimestamp(overrides.updatedAt, now),
  }
}

export function createConversation(
  overrides: Partial<PlaygroundConversation> = {},
): PlaygroundConversation {
  const now = Date.now()
  return {
    id: typeof overrides.id === 'string' && overrides.id.trim() ? overrides.id.trim() : createEntityId('conversation'),
    // The pre-split Playground only generated images, so legacy records belong
    // to the image workspace unless a newer record explicitly identifies chat.
    mode: overrides.mode === 'chat' ? 'chat' : 'image',
    projectId: typeof overrides.projectId === 'string' && overrides.projectId.trim()
      ? overrides.projectId.trim()
      : null,
    title: normalizeConversationTitle(overrides.title ?? ''),
    systemPrompt: normalizeMessageText(overrides.systemPrompt ?? '', PLAYGROUND_MAX_SYSTEM_PROMPT_CHARS),
    draftContent: normalizeMessageText(overrides.draftContent ?? '', PLAYGROUND_MAX_DRAFT_CHARS),
    messages: normalizePersistedMessages(overrides.messages ?? []),
    keyId: typeof overrides.keyId === 'number' && Number.isInteger(overrides.keyId) && overrides.keyId > 0
      ? overrides.keyId
      : undefined,
    model: typeof overrides.model === 'string'
      ? normalizeMessageText(overrides.model, PLAYGROUND_MAX_MODEL_CHARS)
      : undefined,
    parameters: overrides.parameters ? cloneParameters(overrides.parameters) : undefined,
    imageSize: typeof overrides.imageSize === 'string' ? overrides.imageSize.slice(0, 32) : undefined,
    imageCustomWidth: typeof overrides.imageCustomWidth === 'number' && Number.isInteger(overrides.imageCustomWidth)
      ? Math.min(3840, Math.max(16, overrides.imageCustomWidth))
      : undefined,
    imageCustomHeight: typeof overrides.imageCustomHeight === 'number' && Number.isInteger(overrides.imageCustomHeight)
      ? Math.min(3840, Math.max(16, overrides.imageCustomHeight))
      : undefined,
    imageQuality: typeof overrides.imageQuality === 'string' ? overrides.imageQuality.slice(0, 32) : undefined,
    imageCount: typeof overrides.imageCount === 'number' && Number.isInteger(overrides.imageCount)
      ? Math.min(PLAYGROUND_MAX_IMAGE_COUNT, Math.max(1, overrides.imageCount))
      : undefined,
    imageOutputFormat: typeof overrides.imageOutputFormat === 'string' ? overrides.imageOutputFormat.slice(0, 16) : undefined,
    imageOutputCompression: typeof overrides.imageOutputCompression === 'number' && Number.isInteger(overrides.imageOutputCompression)
      ? Math.min(100, Math.max(0, overrides.imageOutputCompression))
      : undefined,
    imageBackground: typeof overrides.imageBackground === 'string' ? overrides.imageBackground.slice(0, 16) : undefined,
    imageModeration: typeof overrides.imageModeration === 'string' ? overrides.imageModeration.slice(0, 16) : undefined,
    imageInputFidelity: typeof overrides.imageInputFidelity === 'string' ? overrides.imageInputFidelity.slice(0, 16) : undefined,
    imageStyle: typeof overrides.imageStyle === 'string' ? overrides.imageStyle.slice(0, 16) : undefined,
    createdAt: normalizeTimestamp(overrides.createdAt, now),
    updatedAt: normalizeTimestamp(overrides.updatedAt, now),
  }
}

export function cloneParameters(parameters: PlaygroundParameters): PlaygroundParameters {
  return {
    temperature: parameters.temperature,
    top_p: parameters.top_p,
    max_tokens: parameters.max_tokens,
    frequency_penalty: parameters.frequency_penalty,
    presence_penalty: parameters.presence_penalty,
    seed: parameters.seed,
    stream: parameters.stream,
    enabled: {
      temperature: parameters.enabled.temperature,
      top_p: parameters.enabled.top_p,
      max_tokens: parameters.enabled.max_tokens,
      frequency_penalty: parameters.enabled.frequency_penalty,
      presence_penalty: parameters.enabled.presence_penalty,
      seed: parameters.enabled.seed,
    },
  }
}

export function isPlaygroundEligibleKey(key: Pick<ApiKey, 'id' | 'status' | 'group_id' | 'group' | 'auto_group'>): boolean {
  return key.id > 0
    && key.status === 'active'
    && (key.auto_group || (
      typeof key.group_id === 'number'
      && key.group_id > 0
      && key.group?.status === 'active'
    ))
}

export function maskPlaygroundApiKey(key: string): string {
  const normalized = key.trim()
  if (!normalized) return 'Hidden key'
  if (normalized.length <= 10) return `${normalized.slice(0, 3)}...`
  return `${normalized.slice(0, 6)}...${normalized.slice(-4)}`
}

export function normalizePlaygroundKeys(keys: ApiKey[]): PlaygroundKeySummary[] {
  return keys
    .filter((key) => isPlaygroundEligibleKey(key))
    .map((key) => ({
      id: key.id,
      name: key.name.trim() || `API Key #${key.id}`,
      maskedKey: maskPlaygroundApiKey(key.key),
      status: key.status,
      groupId: key.auto_group ? null : key.group_id,
      groupName: key.auto_group ? '' : key.group?.name?.trim() || `Group #${key.group_id}`,
      platform: key.auto_group ? undefined : key.group?.platform,
      autoGroup: key.auto_group,
      autoGroupStrategy: key.auto_group_strategy || 'price',
    }))
    .sort((left, right) => left.id - right.id)
}

export function resolveSelectedKeyId(
  keys: PlaygroundKeySummary[],
  preferredKeyId: number | null | undefined,
): number | null {
  if (preferredKeyId && keys.some((key) => key.id === preferredKeyId)) {
    return preferredKeyId
  }
  return keys[0]?.id ?? null
}

export function resolvePlaygroundDefaultKeyId(
  keys: PlaygroundKeySummary[],
  mode: PlaygroundMode,
): number | null {
  const name = mode === 'image' ? 'Playground Images' : 'Playground Chat'
  return keys.find((key) => key.name === name)?.id ?? null
}

export function normalizePlaygroundModels(models: PlaygroundModel[]): PlaygroundModel[] {
  const seen = new Set<string>()
  return [...models]
    .filter((model) => typeof model.id === 'string' && model.id.trim().length > 0)
    .map((model) => ({ ...model, id: model.id.trim() }))
    .filter((model) => {
      if (seen.has(model.id)) return false
      seen.add(model.id)
      return true
    })
    .sort((left, right) => left.id.localeCompare(right.id))
}

export function isPlaygroundImageModel(model: Pick<PlaygroundModel, 'id'>): boolean {
  const id = model.id.trim().toLowerCase()
  return /^(gpt-image-|dall-e-|grok-imagine|imagen-|flux-|sdxl|stable-diffusion|stable-image|seedream|wanx|hunyuan)/.test(id)
}

// 只排除确实不能用于对话补全的能力型模型（向量、审核、语音、实时、计算机使用等）。
//
// 这里刻意不再维护"允许哪些聊天模型"的白名单：网关返回的模型集合已经跟随账号与
// 分组实时变化，前端再叠一层按名字写死的白名单，只会让上游每次推出新命名都要改
// 前端才能选到。历史上这层白名单造成过两类线上问题：
//   * gpt-5.6-sol / -terra / -luna 因名字里含 sol/terra/luna 被误判为非聊天模型；
//   * claude 正则按 claude-4-opus 的旧命名书写，与实际的 claude-opus-4-8 /
//     claude-sonnet-5 / claude-fable-5 全部不匹配，导致 Anthropic 分组下没有任何
//     可选模型。
// 因此改为纯黑名单：后端给什么就展示什么，新模型零改动即可选用。
const PLAYGROUND_NON_CHAT_MODEL_PATTERNS = /(?:embedding|moderation|rerank|realtime|transcri(?:be|ption)|whisper|tts|audio|search-preview|computer-use)/i

export function normalizePlaygroundImageModels(models: PlaygroundModel[]): PlaygroundModel[] {
  return normalizePlaygroundModels(models)
    .filter(isPlaygroundImageModel)
    .filter((model) => /^(?:gpt-image-|dall-e-|grok-imagine|imagen-)/i.test(model.id))
    .slice(0, 6)
}

export function normalizePlaygroundChatModels(models: PlaygroundModel[]): PlaygroundModel[] {
  // 不再截断：可选模型数量由网关按分组决定，前端截断会让排序靠后的模型永远选不到。
  return normalizePlaygroundModels(models)
    .filter((model) => !isPlaygroundImageModel(model))
    .filter((model) => !PLAYGROUND_NON_CHAT_MODEL_PATTERNS.test(model.id))
}

export function normalizePlaygroundVideoModels(models: PlaygroundModel[]): PlaygroundModel[] {
  return normalizePlaygroundModels(models).filter((model) => /(?:video|sora|veo|kling|runway|seedance|wan)/i.test(model.id))
}

export function formatPlaygroundKeyLabel(key: PlaygroundKeySummary, autoGroupLabel = 'Auto'): string {
  const scope = key.autoGroup ? autoGroupLabel : key.groupName
  return `${key.name} - #${key.id} - ${scope}`
}

export function resolveSelectedModel(
  models: PlaygroundModel[],
  preferredModel: string | null | undefined,
): string {
  const normalizedPreferred = preferredModel?.trim() || ''
  if (normalizedPreferred && models.some((model) => model.id === normalizedPreferred)) {
    return normalizedPreferred
  }
  return models[0]?.id ?? ''
}

const PLAYGROUND_DEFAULT_CHAT_MODELS = [
  'gpt-5.4',
  'gpt-5.3',
  'gpt-5',
  'gpt-4.1',
  'claude-sonnet-4-6',
  'claude-sonnet-4',
  'gemini-2.5-pro',
  'grok-4',
  'deepseek-chat',
]

const PLAYGROUND_DEFAULT_IMAGE_MODELS = [
  'gpt-image-2',
  'gpt-image-1',
  'gpt-image-1.5',
  'dall-e-3',
  'grok-imagine-image',
  'imagen-4',
]

export function resolvePlaygroundDefaultModel(
  models: PlaygroundModel[],
  mode: PlaygroundMode,
  administratorDefault?: string | null,
): string {
  const normalizedAdministratorDefault = administratorDefault?.trim() || ''
  if (normalizedAdministratorDefault && models.some((model) => model.id === normalizedAdministratorDefault)) {
    return normalizedAdministratorDefault
  }
  const preferredModels = mode === 'image'
    ? PLAYGROUND_DEFAULT_IMAGE_MODELS
    : PLAYGROUND_DEFAULT_CHAT_MODELS
  for (const preferredModel of preferredModels) {
    const match = models.find((model) => model.id === preferredModel)
    if (match) return match.id
  }
  return models[0]?.id ?? ''
}

export function canStartPlaygroundCompletion(
  messages: PlaygroundMessage[],
  draftContent: string,
): boolean {
  if (draftContent.trim()) return true

  const lastMessage = [...messages]
    .reverse()
    .find((message) => message.content.trim() || message.reasoningContent?.trim())

  return lastMessage?.role === 'system' || lastMessage?.role === 'user'
}

export function rebuildConversationAfterUserEdit(
  messages: PlaygroundMessage[],
  messageId: string,
  content: string,
  updatedAt: number = Date.now(),
): PlaygroundMessage[] | null {
  const messageIndex = messages.findIndex((message) => message.id === messageId && message.role === 'user')
  if (messageIndex < 0 || !content.trim()) return null

  return messages.slice(0, messageIndex + 1).map((message, index) => (
    index === messageIndex
      ? { ...message, content: content.trim(), updatedAt }
      : { ...message }
  ))
}

export function withSystemPrompt(
  messages: PlaygroundMessage[],
  systemPrompt: string,
): PlaygroundMessage[] {
  const normalizedPrompt = normalizeMessageText(systemPrompt, PLAYGROUND_MAX_SYSTEM_PROMPT_CHARS)
  if (!normalizedPrompt) return messages
  return [createMessage('system', normalizedPrompt), ...messages]
}

export function normalizePersistedMessages(messages: PlaygroundMessage[]): PlaygroundMessage[] {
  return messages
    .filter((message): message is PlaygroundMessage => (
      typeof message === 'object'
      && message !== null
      && (message.role === 'system' || message.role === 'user' || message.role === 'assistant')
      && typeof message.content === 'string'
    ))
    .map((message) => ({
      id: typeof message.id === 'string' && message.id.trim() ? message.id.trim() : createMessage(message.role, '').id,
      role: message.role,
      content: normalizeMessageText(message.content, PLAYGROUND_MAX_MESSAGE_CHARS),
      imageUrls: Array.isArray(message.imageUrls)
        ? message.imageUrls.filter((url): url is string => typeof url === 'string' && /^https?:\/\//i.test(url)).slice(0, PLAYGROUND_MAX_IMAGE_COUNT)
        : undefined,
      imageCacheKeys: Array.isArray(message.imageCacheKeys)
        ? message.imageCacheKeys.filter((key): key is string => typeof key === 'string' && key.trim().length > 0 && key.length <= 320).slice(0, PLAYGROUND_MAX_IMAGE_COUNT)
        : undefined,
      imageOutputFormat: typeof message.imageOutputFormat === 'string'
        ? message.imageOutputFormat.trim().slice(0, 16)
        : undefined,
      revisedPrompt: typeof message.revisedPrompt === 'string'
        ? normalizeMessageText(message.revisedPrompt, PLAYGROUND_MAX_MESSAGE_CHARS)
        : undefined,
      attachments: normalizePersistedAttachments(message.attachments),
      createdAt: Number.isFinite(message.createdAt) ? message.createdAt : Date.now(),
      updatedAt: Number.isFinite(message.updatedAt) ? message.updatedAt : Date.now(),
    }))
    .filter((message) => (
      message.content.length > 0
      || (message.attachments?.length ?? 0) > 0
      || (message.imageUrls?.length ?? 0) > 0
      || (message.imageCacheKeys?.length ?? 0) > 0
    ))
    .slice(-PLAYGROUND_MAX_MESSAGES)
}

function normalizePersistedAttachments(value: unknown): PlaygroundAttachment[] | undefined {
  if (!Array.isArray(value)) return undefined
  const attachments = value
    .filter((attachment): attachment is PlaygroundAttachment => (
      typeof attachment === 'object'
      && attachment !== null
      && typeof (attachment as PlaygroundAttachment).name === 'string'
      && typeof (attachment as PlaygroundAttachment).mimeType === 'string'
    ))
    .map((attachment) => ({
      id: typeof attachment.id === 'string' && attachment.id ? attachment.id : `${attachment.name}-${Date.now()}`,
      name: attachment.name.slice(0, 160),
      mimeType: attachment.mimeType.slice(0, 120),
      size: Number.isFinite(attachment.size) ? Math.max(0, attachment.size) : 0,
      // Do not persist base64 payloads in localStorage.
    }))
    .slice(0, PLAYGROUND_MAX_ATTACHMENTS)
  return attachments.length ? attachments : undefined
}

export interface PlaygroundImageRequestInput {
  model: string
  prompt: string
  size: string
  quality: string
  imageCount: number
  outputFormat: string
  outputCompression: number
  background: string
  moderation: string
  style: string
  inputFidelity: string
  hasReferenceImages: boolean
}

export function buildPlaygroundImageRequest(input: PlaygroundImageRequestInput): {
  body: Record<string, unknown>
  requestedImageCount: number
  supportsNativeOptions: boolean
  isDallE3: boolean
} {
  const model = input.model.trim()
  const isDallE = /^dall-e-/i.test(model)
  const isDallE3 = /^dall-e-3$/i.test(model)
  const supportsNativeOptions = /^gpt-image-/i.test(model)
  const body: Record<string, unknown> = {
    model,
    prompt: input.prompt.trim(),
    size: isDallE && input.size === 'auto' ? '1024x1024' : input.size,
    quality: isDallE
      ? (input.quality === 'high' ? 'hd' : 'standard')
      : input.quality,
    n: 1,
  }

  if (supportsNativeOptions) {
    body.output_format = input.outputFormat
    if (input.outputFormat !== 'png') body.output_compression = input.outputCompression
    if (input.background !== 'auto') body.background = input.background
    if (input.moderation !== 'auto') body.moderation = input.moderation
    if (input.style !== 'auto') body.style = input.style
    if (input.hasReferenceImages && input.inputFidelity !== 'auto') body.input_fidelity = input.inputFidelity
  } else if (isDallE3 && input.style !== 'auto') {
    body.style = input.style
  }

  // GPT Image endpoints always return base64 and reject response_format.
  // DALL-E still requires the explicit response format for the compatible
  // image route.
  if (isDallE) body.response_format = 'b64_json'

  return {
    body,
    requestedImageCount: isDallE ? 1 : Math.min(PLAYGROUND_MAX_IMAGE_COUNT, Math.max(1, Math.trunc(input.imageCount))),
    supportsNativeOptions,
    isDallE3,
  }
}

export function buildChatPayload(input: {
  model: string
  messages: PlaygroundMessage[]
  parameters: PlaygroundParameters
}): PlaygroundChatPayload {
  const payload: PlaygroundChatPayload = {
    model: input.model.trim(),
    messages: input.messages
      .map((message) => ({
        role: message.role,
        content: buildChatMessageContent(message),
      }))
      .filter((message) => message.content.length > 0),
    stream: input.parameters.stream,
  }

  if (input.parameters.enabled.temperature) {
    payload.temperature = input.parameters.temperature
  }

  if (input.parameters.enabled.top_p) {
    payload.top_p = input.parameters.top_p
  }

  if (input.parameters.enabled.frequency_penalty) {
    payload.frequency_penalty = input.parameters.frequency_penalty
  }

  if (input.parameters.enabled.presence_penalty) {
    payload.presence_penalty = input.parameters.presence_penalty
  }

  if (
    input.parameters.enabled.max_tokens
    && typeof input.parameters.max_tokens === 'number'
    && input.parameters.max_tokens > 0
  ) {
    payload.max_tokens = Math.trunc(input.parameters.max_tokens)
  }

  if (
    input.parameters.enabled.seed
    && typeof input.parameters.seed === 'number'
    && Number.isFinite(input.parameters.seed)
  ) {
    payload.seed = Math.trunc(input.parameters.seed)
  }

  return payload
}

function buildChatMessageContent(message: PlaygroundMessage): PlaygroundChatPayload['messages'][number]['content'] {
  const text = normalizeMessageText(message.content, PLAYGROUND_MAX_MESSAGE_CHARS)
  if (!message.attachments?.length) return text

  const parts: Exclude<PlaygroundChatPayload['messages'][number]['content'], string> = []
  if (text) parts.push({ type: 'text', text })
  for (const attachment of message.attachments.slice(0, PLAYGROUND_MAX_ATTACHMENTS)) {
    if (!attachment.dataUrl) continue
    if (attachment.mimeType.toLowerCase().startsWith('image/')) {
      parts.push({ type: 'image_url', image_url: { url: attachment.dataUrl } })
    } else {
      parts.push({ type: 'file', file: { filename: attachment.name, file_data: attachment.dataUrl } })
    }
  }
  return parts
}

export function createEmptyPersistedState(): PlaygroundPersistedState {
  return {
    version: PLAYGROUND_STORAGE_VERSION,
    model: '',
    parameters: cloneParameters(DEFAULT_PLAYGROUND_PARAMETERS),
    activeConversationId: null,
    projects: [],
    conversations: [],
  }
}
