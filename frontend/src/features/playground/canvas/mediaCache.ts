const DATABASE_NAME = 'sub2api-playground-canvas-media'
const STORE_NAME = 'media'

function openDatabase(): Promise<IDBDatabase | null> {
  if (typeof indexedDB === 'undefined') return Promise.resolve(null)
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE_NAME, 1)
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(STORE_NAME)) request.result.createObjectStore(STORE_NAME, { keyPath: 'key' })
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error ?? new Error('Unable to open the canvas media cache.'))
  })
}

export function createCanvasMediaCacheKey(userId: number, keyId: number, nodeId: string): string {
  return `${userId}:${keyId}:${nodeId}`
}

export async function cacheCanvasMedia(key: string, blob: Blob): Promise<string | null> {
  const database = await openDatabase()
  if (!database) return URL.createObjectURL(blob)
  try {
    await new Promise<void>((resolve, reject) => {
      const transaction = database.transaction(STORE_NAME, 'readwrite')
      transaction.objectStore(STORE_NAME).put({ key, blob, createdAt: Date.now() })
      transaction.oncomplete = () => resolve()
      transaction.onerror = () => reject(transaction.error ?? new Error('Unable to cache canvas media.'))
    })
  } finally {
    database.close()
  }
  return URL.createObjectURL(blob)
}

export async function restoreCanvasMedia(key: string): Promise<string | null> {
  const database = await openDatabase()
  if (!database) return null
  try {
    const record = await new Promise<{ blob?: Blob } | undefined>((resolve, reject) => {
      const request = database.transaction(STORE_NAME, 'readonly').objectStore(STORE_NAME).get(key)
      request.onsuccess = () => resolve(request.result as { blob?: Blob } | undefined)
      request.onerror = () => reject(request.error ?? new Error('Unable to restore canvas media.'))
    })
    return record?.blob instanceof Blob ? URL.createObjectURL(record.blob) : null
  } finally {
    database.close()
  }
}

export async function readCachedCanvasMediaBlob(key: string): Promise<Blob | null> {
  const database = await openDatabase()
  if (!database) return null
  try {
    const record = await new Promise<{ blob?: Blob } | undefined>((resolve, reject) => {
      const request = database.transaction(STORE_NAME, 'readonly').objectStore(STORE_NAME).get(key)
      request.onsuccess = () => resolve(request.result as { blob?: Blob } | undefined)
      request.onerror = () => reject(request.error ?? new Error('Unable to read the canvas media cache.'))
    })
    return record?.blob instanceof Blob ? record.blob : null
  } finally {
    database.close()
  }
}
