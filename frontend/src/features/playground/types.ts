import type { ApiKey } from '@/types'

export type PlaygroundRole = 'system' | 'user' | 'assistant'
export type PlaygroundMode = 'chat' | 'image' | 'canvas'

export interface PlaygroundAttachment {
  id: string
  name: string
  mimeType: string
  size: number
  // Data URLs are kept in memory for the active page. Persistence strips the
  // payload so browser history cannot grow without bound.
  dataUrl?: string
}

export interface PlaygroundMessage {
  id: string
  role: PlaygroundRole
  content: string
  reasoningContent?: string
  errorMessage?: string
  imageUrls?: string[]
  imageCacheKeys?: string[]
  imageOutputFormat?: string
  revisedPrompt?: string
  attachments?: PlaygroundAttachment[]
  createdAt: number
  updatedAt: number
}

export interface PlaygroundProject {
  id: string
  name: string
  createdAt: number
  updatedAt: number
}

export interface PlaygroundConversation {
  id: string
  mode: PlaygroundMode
  projectId: string | null
  title: string
  systemPrompt: string
  draftContent: string
  messages: PlaygroundMessage[]
  keyId?: number
  model?: string
  parameters?: PlaygroundParameters
  imageSize?: string
  imageCustomWidth?: number
  imageCustomHeight?: number
  imageQuality?: string
  imageCount?: number
  imageOutputFormat?: string
  imageOutputCompression?: number
  imageBackground?: string
  imageModeration?: string
  imageInputFidelity?: string
  imageStyle?: string
  createdAt: number
  updatedAt: number
}

export interface PlaygroundConversationSummary {
  id: string
  mode: PlaygroundMode
  projectId: string | null
  title: string
  createdAt: number
  updatedAt: number
}

export interface PlaygroundParameterEnabledState {
  temperature: boolean
  top_p: boolean
  max_tokens: boolean
  frequency_penalty: boolean
  presence_penalty: boolean
  seed: boolean
}

export interface PlaygroundParameters {
  temperature: number
  top_p: number
  max_tokens: number | null
  frequency_penalty: number
  presence_penalty: number
  seed: number | null
  stream: boolean
  enabled: PlaygroundParameterEnabledState
}

export interface PlaygroundKeySummary {
  id: number
  name: string
  maskedKey: string
  status: ApiKey['status']
  groupId: number | null
  groupName: string
  platform?: string
  autoGroup: boolean
  autoGroupStrategy: ApiKey['auto_group_strategy']
}

export interface PlaygroundModel {
  id: string
  owned_by?: string
}

export interface PlaygroundImageResult {
  url?: string
  b64_json?: string
  revised_prompt?: string
}

export interface PlaygroundImageGenerationResponse {
  created?: number
  data: PlaygroundImageResult[]
}

export interface PlaygroundVideoResponse {
  id: string
  status?: string
  video_url?: string
  url?: string
}

export interface PlaygroundPersistedStateV1 {
  version: 1
  model: string
  draftRole: PlaygroundRole
  draftContent: string
  parameters: PlaygroundParameters
  messages: PlaygroundMessage[]
}

export interface PlaygroundPersistedState {
  version: 2
  model: string
  parameters: PlaygroundParameters
  activeConversationId: string | null
  projects: PlaygroundProject[]
  conversations: PlaygroundConversation[]
}

export interface PlaygroundPersistedIndexV2 {
  version: 2
  model: string
  parameters: PlaygroundParameters
  activeConversationId: string | null
  projects: PlaygroundProject[]
  conversations: PlaygroundConversationSummary[]
}

export interface PlaygroundPersistedConversationV2 extends PlaygroundConversation {
  version: 2
}

export interface PlaygroundChatPayload {
  model: string
  messages: Array<{
    role: PlaygroundRole
    content: string | Array<{
      type: 'text' | 'image_url' | 'file'
      text?: string
      image_url?: { url: string }
      file?: { filename: string; file_data: string }
    }>
  }>
  temperature?: number
  top_p?: number
  frequency_penalty?: number
  presence_penalty?: number
  stream: boolean
  max_tokens?: number
  seed?: number
}

export interface PlaygroundCompletionResult {
  content: string
  reasoningContent: string
  finishReason: string | null
}

export interface PlaygroundStreamDelta {
  contentDelta: string
  reasoningDelta: string
  finishReason: string | null
}
