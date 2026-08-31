import { describe, expect, it } from 'vitest'
import { normalizeCanvasCropRect, normalizeCanvasImageGrid, resolveCanvasUpscaleSize } from '../canvas/imageTransform'

describe('normalizeCanvasCropRect', () => {
  it('keeps a valid crop unchanged', () => {
    expect(normalizeCanvasCropRect({ x: 0.1, y: 0.2, width: 0.5, height: 0.4 })).toEqual({ x: 0.1, y: 0.2, width: 0.5, height: 0.4 })
  })

  it('clamps size and position inside the source image', () => {
    expect(normalizeCanvasCropRect({ x: 0.9, y: -1, width: 0.4, height: 2 })).toEqual({ x: 0.6, y: 0, width: 0.4, height: 1 })
  })

  it('recovers from non-finite values', () => {
    expect(normalizeCanvasCropRect({ x: Number.NaN, y: Infinity, width: Number.NaN, height: -1 })).toEqual({ x: 0, y: 0, width: 1, height: 0.01 })
  })
})

describe('normalizeCanvasImageGrid', () => {
  it('clamps grid dimensions to the supported range', () => {
    expect(normalizeCanvasImageGrid({ rows: 9.8, columns: 0 })).toEqual({ rows: 6, columns: 1 })
  })
})

describe('resolveCanvasUpscaleSize', () => {
  it('preserves aspect ratio and caps the long edge', () => {
    expect(resolveCanvasUpscaleSize(800, 600, 2048)).toEqual({ width: 2048, height: 1536 })
    expect(resolveCanvasUpscaleSize(5000, 3000, 8192)).toEqual({ width: 4096, height: 2458 })
  })
})
