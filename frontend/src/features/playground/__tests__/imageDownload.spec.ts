import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { downloadPlaygroundImage, imageFilenameExtension, playgroundImageUrl } from '../imageDownload'

describe('Playground image downloads', () => {
  const click = vi.fn()
  const createObjectURL = vi.fn(() => 'blob:generated-image')
  const revokeObjectURL = vi.fn()

  beforeEach(() => {
    click.mockClear()
    createObjectURL.mockClear()
    revokeObjectURL.mockClear()
    vi.stubGlobal('fetch', vi.fn())
    vi.stubGlobal('URL', {
      ...URL,
      createObjectURL,
      revokeObjectURL,
    })
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(click)
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('converts base64 image results to a Blob without a network request', async () => {
    await downloadPlaygroundImage('data:image/webp;base64,AA==', 'sub2api-image.webp')

    expect(fetch).not.toHaveBeenCalled()
    expect(click).toHaveBeenCalledOnce()
    expect(createObjectURL).toHaveBeenCalledOnce()
  })

  it('converts remote image results to a Blob before downloading', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new Blob(['image'], { type: 'image/png' }), { status: 200 })))

    await downloadPlaygroundImage('https://cdn.example/image.png', 'sub2api-image.png')

    expect(fetch).toHaveBeenCalledWith('https://cdn.example/image.png')
    expect(createObjectURL).toHaveBeenCalledOnce()
    expect(revokeObjectURL).not.toHaveBeenCalled()
    expect(click).toHaveBeenCalledOnce()
  })

  it('keeps the image format in the suggested filename where available', () => {
    expect(imageFilenameExtension('data:image/jpeg;base64,AA==')).toBe('jpg')
    expect(imageFilenameExtension('https://cdn.example/image.webp?token=abc')).toBe('webp')
    expect(imageFilenameExtension('https://cdn.example/generated')).toBe('png')
    expect(imageFilenameExtension('blob:generated-image', 'webp')).toBe('webp')
    expect(imageFilenameExtension('blob:generated-image', 'jpeg')).toBe('jpg')
  })

  it('prefers a valid embedded image over an upstream URL', () => {
    expect(playgroundImageUrl({
      url: 'https://upstream.example/short-lived.png',
      b64_json: 'aW1hZ2U=',
    })).toBe('data:image/png;base64,aW1hZ2U=')
  })

  it('uses the requested MIME type for bare JPEG and WebP image data', () => {
    expect(playgroundImageUrl({ b64_json: 'aGVsbG8=' }, 'jpeg')).toBe('data:image/jpeg;base64,aGVsbG8=')
    expect(playgroundImageUrl({ b64_json: 'aGVsbG8=' }, 'webp')).toBe('data:image/webp;base64,aGVsbG8=')
  })

  it('uses the upstream URL only when embedded image data is unavailable or malformed', () => {
    expect(playgroundImageUrl({
      url: 'https://upstream.example/image.png',
      b64_json: 'not valid base64!',
    })).toBe('https://upstream.example/image.png')
  })
})
