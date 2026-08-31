import { afterEach, describe, expect, it, vi } from 'vitest'
import { captureCanvasVideoFrame } from '../canvas/videoFrame'

type Listener = () => void

function installVideoDomMock() {
  const listeners = new Map<string, Listener>()
  const context = { drawImage: vi.fn() }
  const canvas = {
    width: 0,
    height: 0,
    getContext: vi.fn(() => context),
    toBlob: vi.fn((callback: BlobCallback) => callback(new Blob(['frame'], { type: 'image/png' }))),
  }
  let currentTime = 0
  const video = {
    crossOrigin: '',
    muted: false,
    playsInline: false,
    preload: '',
    src: '',
    videoWidth: 640,
    videoHeight: 360,
    duration: 8,
    readyState: 4,
    addEventListener: vi.fn((event: string, listener: Listener) => listeners.set(event, listener)),
    removeEventListener: vi.fn((event: string) => listeners.delete(event)),
    removeAttribute: vi.fn(),
    load: vi.fn(() => queueMicrotask(() => listeners.get('loadedmetadata')?.())),
    get currentTime() {
      return currentTime
    },
    set currentTime(value: number) {
      currentTime = value
      queueMicrotask(() => listeners.get('seeked')?.())
    },
  }
  const createElement = vi.spyOn(document, 'createElement').mockImplementation(((tagName: string) => {
    if (tagName === 'video') return video as unknown as HTMLVideoElement
    if (tagName === 'canvas') return canvas as unknown as HTMLCanvasElement
    return document.createElementNS('http://www.w3.org/1999/xhtml', tagName)
  }) as typeof document.createElement)

  return { canvas, context, createElement, video }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('captureCanvasVideoFrame', () => {
  it('captures the first frame after metadata is ready and releases the video source', async () => {
    const mock = installVideoDomMock()

    const frame = await captureCanvasVideoFrame('blob:test-video', 'first')

    expect(frame.type).toBe('image/png')
    expect(mock.video.crossOrigin).toBe('anonymous')
    expect(mock.video.muted).toBe(true)
    expect(mock.video.playsInline).toBe(true)
    expect(mock.canvas.width).toBe(640)
    expect(mock.canvas.height).toBe(360)
    expect(mock.context.drawImage).toHaveBeenCalledWith(mock.video, 0, 0, 640, 360)
    expect(mock.video.removeAttribute).toHaveBeenCalledWith('src')
    expect(mock.video.load).toHaveBeenCalledTimes(2)
  })

  it('seeks to the final drawable moment for a last-frame capture', async () => {
    const mock = installVideoDomMock()

    await captureCanvasVideoFrame('blob:test-video', 'last')

    expect(mock.video.currentTime).toBeCloseTo(7.999, 6)
    expect(mock.context.drawImage).toHaveBeenCalledTimes(1)
  })
})
