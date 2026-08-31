// 画布的参考图只在持久化里保留元数据 + IndexedDB 键，不保留 base64。
//
// 一张 1024×1024 的参考图转成 data URL 约 1.5–2 MB，每个节点可挂 4 张，
// 若直接写进 localStorage（配额仅 5–10 MB）单个节点就能把它撑爆。
// 图片本体交给 imageCache（IndexedDB），这里只存 cacheKey，用时再取回。
// 与既有 playground 的做法一致：PlaygroundAttachment.dataUrl 只活在内存里。
export interface CanvasReferenceImage {
  id: string
  name: string
  mimeType: string
  size: number
  cacheKey: string
}

export type CanvasTool = 'select' | 'pan' | 'connect'
export type CanvasBackgroundMode = 'dots' | 'lines' | 'blank'
export type CanvasNodeType = 'image' | 'video' | 'audio' | 'text' | 'config' | 'group' | `${string}:${string}`
export type CanvasNodeKind = 'generator' | 'result'

export interface CanvasAssistantMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  errorMessage?: string
  createdAt: number
}

export interface CanvasPromptEntry {
  id: string
  title: string
  content: string
  tags: string[]
  source: string
  custom?: boolean
}

export interface CanvasImageTransform {
  crop: { x: number; y: number; width: number; height: number }
  rotation: 0 | 90 | 180 | 270
  flipX: boolean
  flipY: boolean
}

export interface CanvasTextVariant {
  id: string
  status: 'idle' | 'generating' | 'success' | 'error'
  content: string
  errorMessage?: string
}

export interface CanvasImageVariant {
  id: string
  status: 'pending' | 'success' | 'error'
  url?: string
  cacheKey?: string
  errorMessage?: string
}

export interface CanvasViewport {
  x: number
  y: number
  scale: number
}

export interface CanvasNode {
  id: string
  type: CanvasNodeType
  kind?: CanvasNodeKind
  /** Optional user-facing label shown above the node, independent of its prompt/content. */
  title?: string
  x: number
  y: number
  width: number
  height: number
  prompt: string
  textContent?: string
  textFontSize?: number
  textCount?: number
  textVariants?: CanvasTextVariant[]
  primaryTextId?: string
  videoUrl?: string
  audioUrl?: string
  pluginId?: string
  pluginTitle?: string
  pluginDescription?: string
  pluginRenderer?: 'generic' | 'markdown' | 'sticky-note' | 'svg' | 'html' | 'panorama'
  pluginMetadata?: Record<string, unknown>
  pluginHidePanel?: boolean
  pluginAutoOpenPanel?: boolean
  pluginTransparentBackground?: boolean
  pluginInteractionToggle?: boolean
  pluginUseBuiltinPanel?: { mode: 'image' | 'video' | 'text' | 'audio'; promptPrefix?: string; writeBackToSelf?: boolean }
  config?: {
    mode?: 'image' | 'text' | 'video' | 'audio'
    model?: string
    count?: string
    size?: string
    quality?: string
    background?: string
    resolution?: string
    duration?: string
    aspectRatio?: string
    audioVoice?: string
    audioFormat?: string
    audioSpeed?: string
    audioInstructions?: string
  }
  model: string
  imageUrl?: string
  imageCacheKey?: string
  imageCacheKeys?: string[]
  videoCacheKey?: string
  audioCacheKey?: string
  videoRequestId?: string
  imageTransform?: CanvasImageTransform
  /** Allow image nodes to be stretched without preserving their aspect ratio. */
  freeResize?: boolean
  imageUrls?: string[]
  imageVariants?: CanvasImageVariant[]
  primaryImageIndex?: number
  imageCount?: number
  outputFormat?: string
  sourceNodeId?: string
  groupId?: string
  resultIndex?: number
  status: 'idle' | 'generating' | 'success' | 'error'
  errorMessage?: string
  referenceImages?: CanvasReferenceImage[]
  createdAt: number
  updatedAt: number
}

export interface CanvasConnection {
  id: string
  from: string
  to: string
  kind?: 'result' | 'reference'
}

export interface CanvasProject {
  id: string
  title: string
  nodes: CanvasNode[]
  connections: CanvasConnection[]
  viewport: CanvasViewport
  backgroundMode: CanvasBackgroundMode
  assistantMessages?: CanvasAssistantMessage[]
  createdAt: number
  updatedAt: number
}

export interface CanvasPersistedState {
  version: 1
  activeProjectId: string | null
  projects: CanvasProject[]
}
