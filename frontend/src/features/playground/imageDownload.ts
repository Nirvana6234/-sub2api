import type { PlaygroundImageResult } from './types'

function normalizeBase64Image(value: string, outputFormat = 'png'): string | null {
  const trimmed = value.trim()
  if (trimmed.startsWith('data:image/')) return trimmed

  const base64 = trimmed.replace(/\s+/g, '')
  if (!base64 || base64.length % 4 !== 0 || !/^[A-Za-z0-9+/]+={0,2}$/.test(base64)) {
    return null
  }
  const normalizedFormat = outputFormat.trim().toLowerCase()
  const mimeType = normalizedFormat === 'jpg' || normalizedFormat === 'jpeg'
    ? 'image/jpeg'
    : normalizedFormat === 'webp'
      ? 'image/webp'
      : 'image/png'
  return `data:${mimeType};base64,${base64}`
}

// Prefer embedded image data. Some upstreams return both forms but expose a
// URL that is only reachable from their own network or expires immediately.
export function playgroundImageUrl(result: PlaygroundImageResult, outputFormat = 'png'): string | null {
  if (typeof result.b64_json === 'string') {
    const base64URL = normalizeBase64Image(result.b64_json, outputFormat)
    if (base64URL) return base64URL
  }

  if (typeof result.url === 'string' && result.url.trim()) return result.url.trim()
  return null
}

function triggerDownload(url: string, filename: string): void {
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.style.display = 'none'
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
}

function dataUrlToBlob(dataUrl: string): Blob {
  const match = /^data:([^;,]+)?(;base64)?,([\s\S]*)$/i.exec(dataUrl)
  if (!match) throw new Error('Image data is invalid.')

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
    throw new Error('Image data is invalid.')
  }
}

function isMobileBrowser(): boolean {
  return typeof navigator !== 'undefined' && /Android|iPhone|iPad|iPod/i.test(navigator.userAgent)
}

async function shareOnMobile(blob: Blob, filename: string): Promise<boolean> {
  if (!isMobileBrowser() || typeof navigator === 'undefined' || typeof navigator.share !== 'function' || typeof File === 'undefined') return false

  const file = new File([blob], filename, { type: blob.type || 'image/png' })
  if (typeof navigator.canShare === 'function' && !navigator.canShare({ files: [file] })) return false

  try {
    await navigator.share({ files: [file], title: filename })
    return true
  } catch (error) {
    // Closing the native share panel is not a download failure.
    if (error instanceof DOMException && error.name === 'AbortError') return true
    return false
  }
}

function downloadBlob(blob: Blob, filename: string): void {
  const objectUrl = URL.createObjectURL(blob)
  triggerDownload(objectUrl, filename)
  // Mobile browsers can start the download after the click handler returns.
  window.setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000)
}

export async function downloadPlaygroundImage(imageUrl: string, filename: string): Promise<void> {
  const blob = imageUrl.startsWith('data:')
    ? dataUrlToBlob(imageUrl)
    : await (async () => {
      const response = await fetch(imageUrl)
      if (!response.ok) {
        throw new Error(`Image download failed with status ${response.status}`)
      }
      return response.blob()
    })()
  if (blob.size === 0) {
    throw new Error('Image download returned an empty file')
  }

  if (await shareOnMobile(blob, filename)) return
  downloadBlob(blob, filename)
}

export function imageFilenameExtension(imageUrl: string, fallbackFormat = 'png'): string {
  const dataMimeType = /^data:image\/([a-z0-9.+-]+);/i.exec(imageUrl)?.[1]
  if (dataMimeType) return dataMimeType === 'jpeg' ? 'jpg' : dataMimeType

  const pathname = imageUrl.split(/[?#]/, 1)[0]
  const extension = /\.([a-z0-9]{2,5})$/i.exec(pathname)?.[1]
  if (extension) return extension.toLowerCase()
  const normalizedFallback = fallbackFormat.trim().toLowerCase()
  return normalizedFallback === 'jpeg' ? 'jpg' : normalizedFallback || 'png'
}
