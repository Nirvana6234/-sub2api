import { resolvePlaygroundImageBlob } from './imageDownload'

// 浏览器剪贴板对图片格式的支持很窄：Chrome / Safari 只保证 image/png 能写入，
// 上游返回的 webp / jpeg 直接写会被拒绝，所以统一转码成 PNG 再写。
export async function convertBlobToPng(blob: Blob): Promise<Blob> {
  if (blob.type === 'image/png') return blob
  const bitmap = await createImageBitmap(blob)
  try {
    const canvas = document.createElement('canvas')
    canvas.width = bitmap.width
    canvas.height = bitmap.height
    canvas.getContext('2d')?.drawImage(bitmap, 0, 0)
    return await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob((result) => result ? resolve(result) : reject(new Error('Unable to encode image')), 'image/png')
    })
  } finally {
    bitmap.close()
  }
}

export function isClipboardImageSupported(): boolean {
  return typeof navigator !== 'undefined'
    && typeof navigator.clipboard?.write === 'function'
    && typeof ClipboardItem !== 'undefined'
}

// 把图片写入系统剪贴板。来源可以是 data URL、远端 URL 或已有 Blob。
export async function copyPlaygroundImageToClipboard(image: string | Blob): Promise<void> {
  if (!isClipboardImageSupported()) throw new Error('Clipboard image write is unavailable')
  const blob = await resolvePlaygroundImageBlob(image)
  const pngBlob = await convertBlobToPng(blob)
  await navigator.clipboard.write([new ClipboardItem({ 'image/png': pngBlob })])
}
