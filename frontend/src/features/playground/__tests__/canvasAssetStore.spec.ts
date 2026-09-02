import { beforeEach, describe, expect, it } from 'vitest'
import { listCanvasSavedAssets, removeCanvasSavedAsset, saveCanvasAsset } from '../canvas/canvasAssetStore'

describe('canvas asset store', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('stores reusable text and media references per user', () => {
    const text = saveCanvasAsset(7, { kind: 'text', title: 'Prompt', text: 'A quiet lake' })
    const image = saveCanvasAsset(7, { kind: 'image', title: 'Lake image', cacheKey: 'image-cache', url: 'blob:image' })

    expect(listCanvasSavedAssets(7).map((asset) => asset.id)).toEqual([image.id, text.id])
    expect(listCanvasSavedAssets(8)).toEqual([])
    expect(listCanvasSavedAssets(7)[0]).toMatchObject({ kind: 'image', cacheKey: 'image-cache' })
  })

  it('removes only the requested saved asset', () => {
    const first = saveCanvasAsset(11, { kind: 'video', title: 'Clip', cacheKey: 'video-cache' })
    const second = saveCanvasAsset(11, { kind: 'audio', title: 'Voice', cacheKey: 'audio-cache' })

    removeCanvasSavedAsset(11, first.id)

    expect(listCanvasSavedAssets(11)).toEqual([expect.objectContaining({ id: second.id, kind: 'audio' })])
  })
})
