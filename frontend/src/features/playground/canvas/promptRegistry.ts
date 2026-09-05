import type { CanvasPromptEntry } from './types'

const DATABASE_NAME = 'sub2api-playground-prompt-registry'
const STORE_NAME = 'cache'
const CACHE_KEY = 'combined-v1'
const REGISTRY_URL = 'https://raw.githubusercontent.com/yukkcat/image-prompts/main/dist/prompts.json'

interface PromptRegistryRecord {
  id?: unknown
  sourceId?: unknown
  title?: unknown
  prompt?: unknown
  description?: unknown
  tags?: unknown
}

export function normalizeCanvasPromptRegistry(value: unknown): CanvasPromptEntry[] {
  if (!Array.isArray(value)) return []
  const seen = new Set<string>()
  return value.flatMap((entry) => {
    if (!entry || typeof entry !== 'object') return []
    const source = entry as PromptRegistryRecord
    const content = typeof source.prompt === 'string' ? source.prompt.trim().slice(0, 12000) : ''
    if (!content) return []
    const rawId = typeof source.id === 'string' ? source.id.trim() : ''
    const id = rawId || `registry-${content.slice(0, 80)}`
    if (seen.has(id)) return []
    seen.add(id)
    const title = typeof source.title === 'string' && source.title.trim()
      ? source.title.trim().slice(0, 200)
      : typeof source.description === 'string' && source.description.trim()
        ? source.description.trim().slice(0, 200)
        : content.slice(0, 80)
    const tags = Array.isArray(source.tags)
      ? source.tags.filter((tag): tag is string => typeof tag === 'string' && Boolean(tag.trim())).map((tag) => tag.trim().slice(0, 50)).slice(0, 8)
      : []
    return [{ id, title, content, tags, source: typeof source.sourceId === 'string' ? source.sourceId.slice(0, 100) : 'Image Prompt Registry' }]
  }).slice(0, 2000)
}

function openDatabase(): Promise<IDBDatabase | null> {
  if (typeof indexedDB === 'undefined') return Promise.resolve(null)
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE_NAME, 1)
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(STORE_NAME)) request.result.createObjectStore(STORE_NAME, { keyPath: 'key' })
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error ?? new Error('Unable to open the prompt registry cache.'))
  })
}

export async function loadCachedCanvasPrompts(): Promise<CanvasPromptEntry[]> {
  const database = await openDatabase()
  if (!database) return []
  try {
    const record = await new Promise<{ prompts?: unknown } | undefined>((resolve, reject) => {
      const request = database.transaction(STORE_NAME, 'readonly').objectStore(STORE_NAME).get(CACHE_KEY)
      request.onsuccess = () => resolve(request.result as { prompts?: unknown } | undefined)
      request.onerror = () => reject(request.error ?? new Error('Unable to read the prompt registry cache.'))
    })
    return normalizeCanvasPromptRegistry(record?.prompts)
  } finally {
    database.close()
  }
}

async function saveCachedCanvasPrompts(prompts: CanvasPromptEntry[]): Promise<void> {
  const database = await openDatabase()
  if (!database) return
  try {
    await new Promise<void>((resolve, reject) => {
      const transaction = database.transaction(STORE_NAME, 'readwrite')
      transaction.objectStore(STORE_NAME).put({ key: CACHE_KEY, prompts: prompts.map((prompt) => ({ id: prompt.id, title: prompt.title, prompt: prompt.content, tags: prompt.tags, sourceId: prompt.source })), updatedAt: Date.now() })
      transaction.oncomplete = () => resolve()
      transaction.onerror = () => reject(transaction.error ?? new Error('Unable to cache the prompt registry.'))
    })
  } finally {
    database.close()
  }
}

export async function refreshCanvasPromptRegistry(signal?: AbortSignal): Promise<CanvasPromptEntry[]> {
  const response = await fetch(REGISTRY_URL, { signal, headers: { Accept: 'application/json' } })
  if (!response.ok) throw new Error(`Prompt registry request failed (${response.status})`)
  const prompts = normalizeCanvasPromptRegistry(await response.json())
  if (!prompts.length) throw new Error('Prompt registry returned no valid prompts.')
  await saveCachedCanvasPrompts(prompts)
  return prompts
}
