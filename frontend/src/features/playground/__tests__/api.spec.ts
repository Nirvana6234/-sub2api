import { beforeEach, describe, expect, it, vi } from 'vitest'

const refreshToken = vi.fn(async () => ({
  access_token: 'fresh-token',
  refresh_token: 'fresh-refresh',
  expires_in: 3600,
}))
const getAuthToken = vi.fn(() => 'panel-token')
const clearAuthToken = vi.fn()

vi.mock('@/api', () => ({
  authAPI: {
    refreshToken,
  },
}))

vi.mock('@/api/auth', () => ({
  clearAuthToken,
  getAuthToken,
}))

vi.mock('@/api/url', () => ({
  buildApiUrl: (path: string) => `/api/v1${path}`,
}))

describe('playground api transport', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    refreshToken.mockClear()
    getAuthToken.mockClear()
    clearAuthToken.mockClear()
  })

  it('rejects malformed SSE frames instead of committing a partial completion', async () => {
    const { sendPlaygroundChat } = await import('../api')
    const encoder = new TextEncoder()
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('data: {"choices":[{"delta":{"content":"Hel","reasoning":"trace"}}]}\n\n'))
        controller.enqueue(encoder.encode('data: {"broken"\n\n'))
        controller.close()
      },
    })

    vi.stubGlobal('fetch', vi.fn(async () => new Response(stream, {
      status: 200,
      headers: { 'Content-Type': 'text/event-stream' },
    })))

    await expect(sendPlaygroundChat(7, { model: 'gpt-4.1', stream: true }, {
      signal: new AbortController().signal,
    })).rejects.toThrow('Malformed streaming response')
  })

  it('accepts a final [DONE] frame without a trailing separator', async () => {
    const { sendPlaygroundChat } = await import('../api')
    const encoder = new TextEncoder()
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('data: {"choices":[{"delta":{"content":"Hi"}}]}\n\n'))
        controller.enqueue(encoder.encode('data: [DONE]'))
        controller.close()
      },
    })

    vi.stubGlobal('fetch', vi.fn(async () => new Response(stream, {
      status: 200,
      headers: { 'Content-Type': 'text/event-stream' },
    })))

    await expect(sendPlaygroundChat(7, { model: 'gpt-4.1', stream: true }, {
      signal: new AbortController().signal,
    })).resolves.toEqual({
      content: 'Hi',
      reasoningContent: '',
      finishReason: null,
    })
  })

  it('loads and normalizes a server-side history snapshot', async () => {
    const { fetchPlaygroundHistory } = await import('../api')
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      code: 0,
      data: {
        version: 2,
        model: 'gpt-4.1',
        parameters: { stream: true },
        activeConversationId: 'conversation-1',
        projects: [{ id: 'project-1', name: 'Research', createdAt: 1, updatedAt: 2 }],
        conversations: [{
          id: 'conversation-1',
          projectId: 'project-1',
          title: 'Review',
          systemPrompt: '',
          draftContent: '',
          messages: [{ id: 'message-1', role: 'user', content: 'Compare.', createdAt: 1, updatedAt: 2 }],
          createdAt: 1,
          updatedAt: 2,
        }],
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(fetchPlaygroundHistory(7)).resolves.toMatchObject({
      model: 'gpt-4.1',
      activeConversationId: 'conversation-1',
      projects: [{ id: 'project-1' }],
      conversations: [{ id: 'conversation-1', messages: [{ content: 'Compare.' }] }],
    })
    expect(fetchMock.mock.calls[0][1]).toMatchObject({
      headers: expect.not.objectContaining({ 'X-Sub2API-Playground-Key-ID': expect.anything() }),
    })
  })

  it('sends image generation requests to the images endpoint with the selected key', async () => {
    const { sendPlaygroundImageGeneration } = await import('../api')
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      created: 1,
      data: [{ url: 'https://cdn.example/image.png', revised_prompt: 'revised' }],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(sendPlaygroundImageGeneration(42, {
      model: 'gpt-image-1.5',
      prompt: 'a blue bird',
      size: '1920x1088',
      quality: 'high',
      output_format: 'webp',
      output_compression: 82,
      background: 'opaque',
      moderation: 'low',
    })).resolves.toMatchObject({
      data: [{ url: 'https://cdn.example/image.png' }],
    })

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/playground/images/generations', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({
        model: 'gpt-image-1.5',
        prompt: 'a blue bird',
        size: '1920x1088',
        quality: 'high',
        output_format: 'webp',
        output_compression: 82,
        background: 'opaque',
        moderation: 'low',
      }),
    }))
    expect(fetchMock.mock.calls[0][1]).toMatchObject({
      headers: expect.objectContaining({
        'X-Sub2API-Playground-Key-ID': '42',
      }),
    })
  })

  it('sends reference images to the image edits endpoint as multipart data', async () => {
    const { sendPlaygroundImageGeneration } = await import('../api')
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      created: 1,
      data: [{ b64_json: 'aGVsbG8=' }],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(sendPlaygroundImageGeneration(42, {
      model: 'gpt-image-1.5',
      prompt: 'turn this into a watercolor',
      size: '1024x1024',
      input_fidelity: 'high',
    }, {
      referenceImages: [{
        id: 'reference-1',
        name: 'source.png',
        mimeType: 'image/png',
        size: 5,
        dataUrl: 'data:image/png;base64,aGVsbG8=',
      }],
      mask: {
        id: 'mask-1',
        name: 'mask.png',
        mimeType: 'image/png',
        size: 5,
        dataUrl: 'data:image/png;base64,aGVsbG8=',
      },
    })).resolves.toMatchObject({
      data: [{ b64_json: 'aGVsbG8=' }],
    })

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/playground/images/edits', expect.objectContaining({
      method: 'POST',
      body: expect.any(FormData),
    }))
    const request = fetchMock.mock.calls[0][1] as RequestInit
    const form = request.body as FormData
    expect(form.get('model')).toBe('gpt-image-1.5')
    expect(form.get('prompt')).toBe('turn this into a watercolor')
    expect(form.get('input_fidelity')).toBe('high')
    expect(form.getAll('image')).toHaveLength(1)
    expect(form.get('mask')).toBeInstanceOf(Blob)
    expect((request.headers as Record<string, string>)['Content-Type']).toBeUndefined()
  })

  it('creates, polls, and downloads video tasks through the playground routes', async () => {
    const { sendPlaygroundVideoGeneration, fetchPlaygroundVideo, fetchPlaygroundVideoContent } = await import('../api')
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 'video-task-1', status: 'queued' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 'video-task-1', status: 'done', video: { url: 'https://cdn.example/video.mp4' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(new Blob(['video']), { status: 200, headers: { 'Content-Type': 'video/mp4' } }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(sendPlaygroundVideoGeneration(42, { model: 'grok-imagine-video-1.5', prompt: 'waves', duration: 8 })).resolves.toMatchObject({ id: 'video-task-1' })
    await expect(fetchPlaygroundVideo(42, 'video-task-1')).resolves.toMatchObject({ status: 'done', video_url: 'https://cdn.example/video.mp4' })
    await expect(fetchPlaygroundVideoContent(42, 'video-task-1')).resolves.toBeInstanceOf(Blob)
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/playground/videos/generations',
      '/api/v1/playground/videos/video-task-1',
      '/api/v1/playground/videos/video-task-1/content',
    ])
  })

  it('sends speech synthesis through the authenticated playground route', async () => {
    const { sendPlaygroundSpeech } = await import('../api')
    const fetchMock = vi.fn(async () => new Response(new Blob(['audio']), { status: 200, headers: { 'Content-Type': 'audio/mpeg' } }))
    vi.stubGlobal('fetch', fetchMock)
    const result = await sendPlaygroundSpeech(42, { model: 'grok-voice-latest', input: 'hello', voice: 'Ara' })
    expect(result).toBeInstanceOf(Blob)
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/playground/audio/speech', expect.objectContaining({ method: 'POST' }))
  })

  it('sends audio files to the transcription route as multipart data', async () => {
    const { sendPlaygroundTranscription } = await import('../api')
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ text: 'hello from audio' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const result = await sendPlaygroundTranscription(42, new Blob(['audio'], { type: 'audio/mpeg' }), 'clip.mp3', { model: 'grok-voice-latest', language: 'en' })
    expect(result).toBe('hello from audio')
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/playground/audio/transcriptions', expect.objectContaining({ method: 'POST', body: expect.any(FormData) }))
    const request = fetchMock.mock.calls[0][1] as RequestInit
    const form = request.body as FormData
    expect(form.get('file')).toBeInstanceOf(Blob)
    expect(form.get('model')).toBe('grok-voice-latest')
    expect(form.get('language')).toBe('en')
    expect((request.headers as Record<string, string>)['Content-Type']).toBeUndefined()
  })

  it('keeps LocalStorage fallback compatibility when history endpoints are absent', async () => {
    const { fetchPlaygroundHistory, savePlaygroundHistory } = await import('../api')
    const response = vi.fn(async () => new Response(null, { status: 404 }))
    vi.stubGlobal('fetch', response)

    await expect(fetchPlaygroundHistory(7)).resolves.toBeNull()
    await expect(savePlaygroundHistory(7, {
      version: 2,
      model: '',
      parameters: {
        temperature: 0.7,
        top_p: 1,
        max_tokens: null,
        frequency_penalty: 0,
        presence_penalty: 0,
        seed: null,
        stream: true,
        enabled: {
          temperature: true,
          top_p: true,
          max_tokens: false,
          frequency_penalty: true,
          presence_penalty: true,
          seed: false,
        },
      },
      activeConversationId: null,
      projects: [],
      conversations: [],
    })).resolves.toBe(false)
    expect(response).toHaveBeenCalledTimes(2)
  })

  it('clears the panel session when token refresh fails', async () => {
    const { fetchPlaygroundModels } = await import('../api')
    refreshToken.mockRejectedValueOnce(new Error('refresh rejected'))
    localStorage.setItem('sub2api.playground.v1.user.7.key.9', 'history')
    window.history.pushState({}, '', '/login')
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 401 })))

    await expect(fetchPlaygroundModels(7)).rejects.toThrow('Session expired')
    expect(clearAuthToken).toHaveBeenCalledOnce()
    expect(localStorage.getItem('sub2api.playground.v1.user.7.key.9')).toBeNull()
    expect(sessionStorage.getItem('auth_expired')).toBe('1')
    window.history.pushState({}, '', '/')
  })
})
