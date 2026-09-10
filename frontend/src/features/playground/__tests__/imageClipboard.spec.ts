import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { copyPlaygroundImageToClipboard, isClipboardImageSupported } from '../imageClipboard'

describe('Playground image clipboard', () => {
  const write = vi.fn(async () => undefined)

  class FakeClipboardItem {
    constructor(public readonly items: Record<string, Blob>) {}
  }

  beforeEach(() => {
    write.mockClear()
    vi.stubGlobal('fetch', vi.fn())
    vi.stubGlobal('ClipboardItem', FakeClipboardItem)
    vi.stubGlobal('navigator', { clipboard: { write } })
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('writes an already-PNG image to the clipboard without transcoding', async () => {
    await copyPlaygroundImageToClipboard(new Blob(['png-bytes'], { type: 'image/png' }))

    expect(write).toHaveBeenCalledOnce()
    const [items] = write.mock.calls[0] as unknown as [FakeClipboardItem[]]
    expect(items[0].items['image/png'].type).toBe('image/png')
    // 已经是 PNG 时不该走 canvas 转码，省一次解码+编码。
    expect(fetch).not.toHaveBeenCalled()
  })

  it('resolves a data URL without a network request', async () => {
    await copyPlaygroundImageToClipboard('data:image/png;base64,AA==')

    expect(fetch).not.toHaveBeenCalled()
    expect(write).toHaveBeenCalledOnce()
  })

  it('fetches a remote image before writing it', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new Blob(['image']), { status: 200, headers: { 'Content-Type': 'image/png' } })))

    await copyPlaygroundImageToClipboard('https://cdn.example/image.png')

    expect(fetch).toHaveBeenCalledWith('https://cdn.example/image.png')
    expect(write).toHaveBeenCalledOnce()
  })

  it('transcodes a non-PNG image instead of writing an unsupported type', async () => {
    // 浏览器剪贴板只保证支持 image/png，webp/jpeg 必须先转码。
    const createImageBitmap = vi.fn(async () => { throw new Error('decode failed') })
    vi.stubGlobal('createImageBitmap', createImageBitmap)

    await expect(copyPlaygroundImageToClipboard(new Blob(['webp'], { type: 'image/webp' }))).rejects.toThrow()

    expect(createImageBitmap).toHaveBeenCalledOnce()
    expect(write).not.toHaveBeenCalled()
  })

  it('rejects instead of writing when the image cannot be fetched', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('missing', { status: 404 })))

    await expect(copyPlaygroundImageToClipboard('https://cdn.example/missing.png')).rejects.toThrow()
    expect(write).not.toHaveBeenCalled()
  })

  it('reports the clipboard as unsupported when the browser lacks the API', async () => {
    vi.stubGlobal('navigator', {})
    expect(isClipboardImageSupported()).toBe(false)
    // 调用方靠这个开关隐藏按钮，但直接调用也必须失败而不是静默什么都不做。
    await expect(copyPlaygroundImageToClipboard('data:image/png;base64,AA==')).rejects.toThrow()
  })

  it('reports the clipboard as supported when write and ClipboardItem exist', () => {
    expect(isClipboardImageSupported()).toBe(true)
  })
})
