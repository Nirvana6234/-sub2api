export interface CanvasCropRect {
  x: number
  y: number
  width: number
  height: number
}

export interface CanvasImageTransform {
  crop: CanvasCropRect
  rotation: 0 | 90 | 180 | 270
  flipX: boolean
  flipY: boolean
}

export interface CanvasImageGrid {
  rows: number
  columns: number
}

export type CanvasImageUpscaleAlgorithm = 'nearest' | 'bilinear' | 'high'
export interface CanvasImageUpscaleParams {
  targetLongEdge: number
  algorithm: CanvasImageUpscaleAlgorithm
}

export interface CanvasImageAngleParams {
  horizontalAngle: number
  pitchAngle: number
  cameraDistance: number
  wideAngle: boolean
}

export const MAX_CANVAS_UPSCALE_LONG_EDGE = 4096

export function resolveCanvasUpscaleSize(width: number, height: number, targetLongEdge: number): { width: number; height: number } {
  const longEdge = Math.max(1, width, height)
  const target = Math.min(MAX_CANVAS_UPSCALE_LONG_EDGE, Math.max(1, Math.round(targetLongEdge)))
  const scale = target / longEdge
  return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) }
}

export function normalizeCanvasImageGrid(grid: CanvasImageGrid): CanvasImageGrid {
  const normalize = (value: number) => Math.min(6, Math.max(1, Number.isFinite(value) ? Math.round(value) : 1))
  return { rows: normalize(grid.rows), columns: normalize(grid.columns) }
}

export function normalizeCanvasCropRect(rect: CanvasCropRect): CanvasCropRect {
  const width = Math.min(1, Math.max(0.01, Number.isFinite(rect.width) ? rect.width : 1))
  const height = Math.min(1, Math.max(0.01, Number.isFinite(rect.height) ? rect.height : 1))
  return {
    x: Math.min(1 - width, Math.max(0, Number.isFinite(rect.x) ? rect.x : 0)),
    y: Math.min(1 - height, Math.max(0, Number.isFinite(rect.y) ? rect.y : 0)),
    width,
    height,
  }
}

async function decodeImage(blob: Blob): Promise<ImageBitmap | HTMLImageElement> {
  if (typeof createImageBitmap === 'function') return createImageBitmap(blob)
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(blob)
    const image = new Image()
    image.onload = () => {
      URL.revokeObjectURL(url)
      resolve(image)
    }
    image.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error('Unable to decode the image.'))
    }
    image.src = url
  })
}

export async function transformCanvasImage(blob: Blob, transform: CanvasImageTransform): Promise<{ blob: Blob; width: number; height: number }> {
  const image = await decodeImage(blob)
  const sourceWidth = image.width
  const sourceHeight = image.height
  const crop = normalizeCanvasCropRect(transform.crop)
  const sx = Math.round(crop.x * sourceWidth)
  const sy = Math.round(crop.y * sourceHeight)
  const sw = Math.max(1, Math.round(crop.width * sourceWidth))
  const sh = Math.max(1, Math.round(crop.height * sourceHeight))
  const sideways = transform.rotation === 90 || transform.rotation === 270
  const width = sideways ? sh : sw
  const height = sideways ? sw : sh
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const context = canvas.getContext('2d')
  if (!context) throw new Error('Canvas rendering is unavailable.')
  context.translate(width / 2, height / 2)
  context.rotate(transform.rotation * Math.PI / 180)
  context.scale(transform.flipX ? -1 : 1, transform.flipY ? -1 : 1)
  context.drawImage(image, sx, sy, sw, sh, -sw / 2, -sh / 2, sw, sh)
  if ('close' in image && typeof image.close === 'function') image.close()
  const output = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png', 0.95))
  if (!output) throw new Error('Unable to encode the edited image.')
  return { blob: output, width, height }
}

export async function splitCanvasImage(blob: Blob, grid: CanvasImageGrid): Promise<Array<{ blob: Blob; width: number; height: number; row: number; column: number }>> {
  const normalized = normalizeCanvasImageGrid(grid)
  const cells: Array<Promise<{ blob: Blob; width: number; height: number; row: number; column: number }>> = []
  for (let row = 0; row < normalized.rows; row += 1) {
    for (let column = 0; column < normalized.columns; column += 1) {
      cells.push(transformCanvasImage(blob, {
        crop: { x: column / normalized.columns, y: row / normalized.rows, width: 1 / normalized.columns, height: 1 / normalized.rows },
        rotation: 0,
        flipX: false,
        flipY: false,
      }).then((result) => ({ ...result, row, column })))
    }
  }
  return Promise.all(cells)
}

export async function upscaleCanvasImage(blob: Blob, params: CanvasImageUpscaleParams): Promise<{ blob: Blob; width: number; height: number }> {
  const image = await decodeImage(blob)
  const output = resolveCanvasUpscaleSize(image.width, image.height, params.targetLongEdge)
  const canvas = document.createElement('canvas')
  canvas.width = output.width
  canvas.height = output.height
  const context = canvas.getContext('2d')
  if (!context) throw new Error('Canvas rendering is unavailable.')
  context.imageSmoothingEnabled = params.algorithm !== 'nearest'
  context.imageSmoothingQuality = params.algorithm === 'bilinear' ? 'medium' : 'high'
  context.drawImage(image, 0, 0, image.width, image.height, 0, 0, output.width, output.height)
  if ('close' in image && typeof image.close === 'function') image.close()
  const encoded = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png', 0.95))
  if (!encoded) throw new Error('Unable to encode the upscaled image.')
  return { blob: encoded, ...output }
}

export async function transformCanvasImageAngle(blob: Blob, params: CanvasImageAngleParams): Promise<{ blob: Blob; width: number; height: number }> {
  const image = await decodeImage(blob)
  const padding = Math.round(Math.max(image.width, image.height) * 0.18)
  const canvas = document.createElement('canvas')
  canvas.width = image.width + padding * 2
  canvas.height = image.height + padding * 2
  const context = canvas.getContext('2d')
  if (!context) throw new Error('Canvas rendering is unavailable.')
  const horizontal = Math.max(-1, Math.min(1, params.horizontalAngle / 60))
  const pitch = Math.max(-1, Math.min(1, params.pitchAngle / 45))
  const distanceScale = 1.12 - Math.max(0, Math.min(10, params.cameraDistance)) * 0.035
  const scale = Math.max(0.64, Math.min(1.1, distanceScale * (params.wideAngle ? 0.88 : 1)))
  const width = image.width * scale * (1 - Math.abs(horizontal) * 0.28)
  const height = image.height * scale * (1 - Math.abs(pitch) * 0.18)
  const cx = canvas.width / 2
  const cy = canvas.height / 2
  const skewX = horizontal * image.width * 0.18
  const skewY = pitch * image.height * 0.12
  context.save()
  context.setTransform(1, pitch * 0.08, horizontal * -0.1, 1, 0, 0)
  context.drawImage(image, cx - width / 2 + horizontal * padding * 0.5 + skewX, cy - height / 2 + pitch * padding * 0.45 + skewY, width, height)
  context.restore()
  if (params.wideAngle) {
    const gradient = context.createRadialGradient(cx, cy, Math.min(canvas.width, canvas.height) * 0.2, cx, cy, Math.max(canvas.width, canvas.height) * 0.62)
    gradient.addColorStop(0, 'rgba(255,255,255,0)')
    gradient.addColorStop(1, 'rgba(0,0,0,0.18)')
    context.fillStyle = gradient
    context.fillRect(0, 0, canvas.width, canvas.height)
  }
  if ('close' in image && typeof image.close === 'function') image.close()
  const encoded = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png', 0.95))
  if (!encoded) throw new Error('Unable to encode the angle transform.')
  return { blob: encoded, width: canvas.width, height: canvas.height }
}
