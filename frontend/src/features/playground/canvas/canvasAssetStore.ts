export type CanvasSavedAssetKind = 'image' | 'video' | 'audio' | 'text'

export interface CanvasSavedAsset {
  id: string
  kind: CanvasSavedAssetKind
  title: string
  text?: string
  url?: string
  cacheKey?: string
  sourceNodeId?: string
  imageIndex?: number
  createdAt: number
}

function storageKey(userId: number): string {
  return `sub2api.playground.canvas.assets.user.${userId}`
}

function createId(): string {
  return `asset-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

function normalizeAsset(value: unknown): CanvasSavedAsset | null {
  if (!value || typeof value !== 'object') return null
  const source = value as Partial<CanvasSavedAsset>
  if (typeof source.id !== 'string' || typeof source.title !== 'string') return null
  if (source.kind !== 'image' && source.kind !== 'video' && source.kind !== 'audio' && source.kind !== 'text') return null
  const text = typeof source.text === 'string' ? source.text.slice(0, 24000) : undefined
  const url = typeof source.url === 'string' ? source.url.slice(0, 4000) : undefined
  const cacheKey = typeof source.cacheKey === 'string' ? source.cacheKey.slice(0, 400) : undefined
  return {
    id: source.id.slice(0, 160),
    kind: source.kind,
    title: source.title.slice(0, 240),
    text,
    url,
    cacheKey,
    sourceNodeId: typeof source.sourceNodeId === 'string' ? source.sourceNodeId.slice(0, 160) : undefined,
    imageIndex: Number.isFinite(source.imageIndex) ? Math.max(0, Math.floor(Number(source.imageIndex))) : undefined,
    createdAt: Number.isFinite(source.createdAt) ? Number(source.createdAt) : Date.now(),
  }
}

export function listCanvasSavedAssets(userId: number): CanvasSavedAsset[] {
  if (typeof localStorage === 'undefined') return []
  try {
    const parsed = JSON.parse(localStorage.getItem(storageKey(userId)) || '[]')
    if (!Array.isArray(parsed)) return []
    return parsed.map(normalizeAsset).filter((asset): asset is CanvasSavedAsset => asset !== null).slice(0, 200)
  } catch {
    return []
  }
}

function writeAssets(userId: number, assets: CanvasSavedAsset[]): void {
  if (typeof localStorage === 'undefined') return
  localStorage.setItem(storageKey(userId), JSON.stringify(assets.slice(0, 200)))
}

export function saveCanvasAsset(userId: number, asset: Omit<CanvasSavedAsset, 'id' | 'createdAt'>): CanvasSavedAsset {
  const saved: CanvasSavedAsset = { ...asset, id: createId(), createdAt: Date.now() }
  writeAssets(userId, [saved, ...listCanvasSavedAssets(userId)])
  return saved
}

export function removeCanvasSavedAsset(userId: number, id: string): void {
  writeAssets(userId, listCanvasSavedAssets(userId).filter((asset) => asset.id !== id))
}
