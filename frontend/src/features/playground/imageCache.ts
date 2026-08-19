const DATABASE_NAME = 'sub2api-playground-images'
const DATABASE_VERSION = 2
const STORE_NAME = 'images'

export interface PlaygroundGalleryImage {
  key: string
  scope: string
  blob: Blob
  createdAt: number
  prompt: string
  model: string
  size?: string
  quality?: string
  outputFormat?: string
  sourceImageCount?: number
  url?: string
}

function cacheScope(userId: number, keyId: number): string {
  return `${userId}:${keyId}`
}

export function createPlaygroundImageCacheKey(
  userId: number,
  keyId: number,
  messageId: string,
  imageIndex: number,
): string {
  return `${cacheScope(userId, keyId)}:${messageId}:${imageIndex}`
}

function supportsIndexedDb(): boolean {
  return typeof indexedDB !== 'undefined'
}

function openDatabase(): Promise<IDBDatabase | null> {
  if (!supportsIndexedDb()) return Promise.resolve(null)

  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE_NAME, DATABASE_VERSION)
    request.onupgradeneeded = () => {
      const database = request.result
      if (!database.objectStoreNames.contains(STORE_NAME)) {
        const store = database.createObjectStore(STORE_NAME, { keyPath: 'key' })
        store.createIndex('scope', 'scope', { unique: false })
        store.createIndex('createdAt', 'createdAt', { unique: false })
      } else {
        const store = request.transaction?.objectStore(STORE_NAME)
        if (store && !store.indexNames.contains('createdAt')) {
          store.createIndex('createdAt', 'createdAt', { unique: false })
        }
      }
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error ?? new Error('Unable to open the image cache.'))
  })
}

function dataUrlToBlob(dataUrl: string): Blob | null {
  const match = /^data:([^;,]+)?(;base64)?,([\s\S]*)$/i.exec(dataUrl)
  if (!match) return null

  const mimeType = match[1] || 'application/octet-stream'
  try {
    if (match[2]) {
      const decoded = atob(match[3])
      const bytes = new Uint8Array(decoded.length)
      for (let index = 0; index < decoded.length; index += 1) {
        bytes[index] = decoded.charCodeAt(index)
      }
      return new Blob([bytes], { type: mimeType })
    }
    return new Blob([decodeURIComponent(match[3])], { type: mimeType })
  } catch {
    return null
  }
}

function createObjectUrl(blob: Blob): string | null {
  if (typeof URL === 'undefined' || typeof URL.createObjectURL !== 'function') return null
  return URL.createObjectURL(blob)
}

export async function cachePlaygroundImage(
  cacheKey: string,
  userId: number,
  keyId: number,
  imageUrl: string,
  metadata: {
    prompt?: string
    model?: string
    size?: string
    quality?: string
    outputFormat?: string
    sourceImageCount?: number
  } = {},
): Promise<string | null> {
  let blob = imageUrl.startsWith('data:') ? dataUrlToBlob(imageUrl) : null
  if (!blob && /^https?:\/\//i.test(imageUrl)) {
    try {
      const response = await fetch(imageUrl)
      if (response.ok) blob = await response.blob()
    } catch {
      // The image stays available through its upstream URL when CORS blocks caching.
    }
  }
  if (!blob) return null

  const database = await openDatabase()
  if (!database) return createObjectUrl(blob)

  try {
    await new Promise<void>((resolve, reject) => {
      const transaction = database.transaction(STORE_NAME, 'readwrite')
      transaction.objectStore(STORE_NAME).put({
        key: cacheKey,
        scope: cacheScope(userId, keyId),
        blob,
        createdAt: Date.now(),
        prompt: metadata.prompt?.trim().slice(0, 1200) ?? '',
        model: metadata.model?.trim().slice(0, 200) ?? '',
        size: metadata.size?.trim().slice(0, 32) ?? '',
        quality: metadata.quality?.trim().slice(0, 32) ?? '',
        outputFormat: metadata.outputFormat?.trim().slice(0, 16) ?? '',
        sourceImageCount: Math.max(0, Math.floor(metadata.sourceImageCount ?? 0)),
      } satisfies PlaygroundGalleryImage)
      transaction.oncomplete = () => resolve()
      transaction.onerror = () => reject(transaction.error ?? new Error('Unable to cache the generated image.'))
      transaction.onabort = () => reject(transaction.error ?? new Error('Unable to cache the generated image.'))
    })
  } finally {
    database.close()
  }

  return createObjectUrl(blob)
}

export async function restoreCachedPlaygroundImage(cacheKey: string): Promise<string | null> {
  const database = await openDatabase()
  if (!database) return null

  try {
    const record = await new Promise<PlaygroundGalleryImage | undefined>((resolve, reject) => {
      const request = database.transaction(STORE_NAME, 'readonly').objectStore(STORE_NAME).get(cacheKey)
      request.onsuccess = () => resolve(request.result as PlaygroundGalleryImage | undefined)
      request.onerror = () => reject(request.error ?? new Error('Unable to read the generated image cache.'))
    })
    return record?.blob instanceof Blob ? createObjectUrl(record.blob) : null
  } finally {
    database.close()
  }
}

export async function clearCachedPlaygroundImages(userId: number, keyId: number): Promise<void> {
  const database = await openDatabase()
  if (!database) return

  try {
    await new Promise<void>((resolve, reject) => {
      const transaction = database.transaction(STORE_NAME, 'readwrite')
      const index = transaction.objectStore(STORE_NAME).index('scope')
      const request = index.openCursor(IDBKeyRange.only(cacheScope(userId, keyId)))
      request.onsuccess = () => {
        const cursor = request.result
        if (!cursor) return
        cursor.delete()
        cursor.continue()
      }
      transaction.oncomplete = () => resolve()
      transaction.onerror = () => reject(transaction.error ?? new Error('Unable to clear the generated image cache.'))
      transaction.onabort = () => reject(transaction.error ?? new Error('Unable to clear the generated image cache.'))
    })
  } finally {
    database.close()
  }
}

export async function removeCachedPlaygroundImages(cacheKeys: string[]): Promise<void> {
  const keys = [...new Set(cacheKeys.filter((key) => key.trim()))]
  if (!keys.length) return

  const database = await openDatabase()
  if (!database) return

  try {
    await new Promise<void>((resolve, reject) => {
      const transaction = database.transaction(STORE_NAME, 'readwrite')
      const store = transaction.objectStore(STORE_NAME)
      for (const key of keys) store.delete(key)
      transaction.oncomplete = () => resolve()
      transaction.onerror = () => reject(transaction.error ?? new Error('Unable to clear generated images.'))
      transaction.onabort = () => reject(transaction.error ?? new Error('Unable to clear generated images.'))
    })
  } finally {
    database.close()
  }
}

export async function listPlaygroundGalleryImages(userId: number): Promise<Array<PlaygroundGalleryImage & { url: string }>> {
  const database = await openDatabase()
  if (!database) return []

  try {
    const records = await new Promise<PlaygroundGalleryImage[]>((resolve, reject) => {
      const transaction = database.transaction(STORE_NAME, 'readwrite')
      const store = transaction.objectStore(STORE_NAME)
      const request = store.openCursor()
      const results: PlaygroundGalleryImage[] = []
      request.onsuccess = () => {
        const cursor = request.result
        if (!cursor) return
        const record = cursor.value as PlaygroundGalleryImage
        if (record.scope.startsWith(`${userId}:`) && record.blob instanceof Blob) results.push(record)
        cursor.continue()
      }
      transaction.oncomplete = () => resolve(results)
      transaction.onerror = () => reject(transaction.error ?? new Error('Unable to read the image gallery.'))
      transaction.onabort = () => reject(transaction.error ?? new Error('Unable to read the image gallery.'))
    })
    return records
      .sort((left, right) => right.createdAt - left.createdAt)
      .map((record) => ({ ...record, url: createObjectUrl(record.blob) ?? '' }))
      .filter((record) => Boolean(record.url))
  } finally {
    database.close()
  }
}
