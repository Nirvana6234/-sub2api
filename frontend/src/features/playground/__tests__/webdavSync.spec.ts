import { afterEach, describe, expect, it, vi } from 'vitest'
import { downloadCanvasState, uploadCanvasState } from '../canvas/webdavSync'
import type { CanvasPersistedState } from '../canvas/types'

const config = { url: 'https://dav.example.com/root/', username: 'user', password: 'secret', fileName: '/canvas.json' }
const state: CanvasPersistedState = { version: 1, activeProjectId: null, projects: [] }

describe('canvas WebDAV sync', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('uploads a versioned canvas snapshot to the configured file', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    await uploadCanvasState(config, state)
    expect(fetchMock).toHaveBeenCalledWith('https://dav.example.com/root/canvas.json', expect.objectContaining({ method: 'PUT' }))
    const request = fetchMock.mock.calls[0][1] as RequestInit
    expect(JSON.parse(String(request.body))).toMatchObject({ version: 1, state })
  })

  it('downloads wrapped backups and rejects invalid responses', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ state }), { status: 200 })))
    await expect(downloadCanvasState(config)).resolves.toEqual(state)

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('missing', { status: 404 })))
    await expect(downloadCanvasState(config)).rejects.toThrow('404')
  })
})
