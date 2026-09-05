import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  clearAllPlaygroundStates,
  clearPlaygroundState,
  clearPlaygroundStatesForUser,
  createPlaygroundPersistScheduler,
  loadPlaygroundState,
  mergePlaygroundStates,
  savePlaygroundState,
  toPersistedState,
} from '../persistence'
import {
  createConversation,
  createMessage,
  createProject,
  DEFAULT_PLAYGROUND_PARAMETERS,
} from '../viewModel'

const defaultParameters = {
  ...DEFAULT_PLAYGROUND_PARAMETERS,
  enabled: { ...DEFAULT_PLAYGROUND_PARAMETERS.enabled },
}

function createState(overrides: Partial<ReturnType<typeof toPersistedState>> = {}) {
  return toPersistedState({
    model: 'gpt-4.1',
    parameters: defaultParameters,
    activeConversationId: null,
    projects: [],
    conversations: [],
    ...overrides,
  })
}

describe('playground persistence', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('stores projects and conversations independently while keeping secrets and transient output out of LocalStorage', () => {
    const project = createProject('Research', { id: 'project-research', createdAt: 1, updatedAt: 2 })
    const conversation = createConversation({
      id: 'conversation-research',
      projectId: project.id,
      title: 'Evaluate options',
      draftContent: 'unsent draft',
      keyId: 21,
      model: 'gpt-image-1.5',
      parameters: {
        ...defaultParameters,
        temperature: 0.35,
        enabled: { ...defaultParameters.enabled, temperature: true },
      },
      imageSize: '1536x1024',
      imageCustomWidth: 1920,
      imageCustomHeight: 1088,
      imageQuality: 'high',
      imageCount: 8,
      imageOutputFormat: 'webp',
      imageOutputCompression: 82,
      imageBackground: 'opaque',
      imageModeration: 'low',
      imageInputFidelity: 'high',
      imageStyle: 'natural',
      messages: [createMessage('assistant', 'hi', {
        reasoningContent: 'do not persist this trace',
        errorMessage: 'do not persist this error',
        imageUrls: ['data:image/png;base64,do-not-store', 'https://images.example/generated.png'],
        imageCacheKeys: ['9:21:assistant-image:0'],
      })],
      createdAt: 3,
      updatedAt: 4,
    })

    const saved = savePlaygroundState(9, 21, createState({
      activeConversationId: conversation.id,
      projects: [project],
      conversations: [conversation],
    }))

    const index = localStorage.getItem('sub2api.playground.v2.user.9.index')
    const record = localStorage.getItem('sub2api.playground.v2.user.9.conversation.conversation-research')
    expect(index).toContain('"model":"gpt-4.1"')
    expect(index).toContain('"projectId":"project-research"')
    expect(record).toContain('"draftContent":"unsent draft"')
    expect(`${index}${record}`).not.toContain('auth_token')
    expect(`${index}${record}`).not.toContain('"key":')
    expect(`${index}${record}`).not.toContain('do not persist this trace')
    expect(`${index}${record}`).not.toContain('do not persist this error')
    expect(`${index}${record}`).not.toContain('data:image/png;base64,do-not-store')
    expect(record).toContain('https://images.example/generated.png')
    expect(record).toContain('9:21:assistant-image:0')
    expect(record).toContain('"model":"gpt-image-1.5"')
    expect(record).toContain('"imageSize":"1536x1024"')
    expect(loadPlaygroundState(9, 21).conversations[0]).toEqual(expect.objectContaining({
      keyId: 21,
      model: 'gpt-image-1.5',
      imageSize: '1536x1024',
      imageCustomWidth: 1920,
      imageCustomHeight: 1088,
      imageQuality: 'high',
      imageCount: 8,
      imageOutputFormat: 'webp',
      imageOutputCompression: 82,
      imageBackground: 'opaque',
      imageModeration: 'low',
      imageInputFidelity: 'high',
      imageStyle: 'natural',
      parameters: expect.objectContaining({ temperature: 0.35 }),
    }))
    expect(saved.conversations[0].messages[0].reasoningContent).toBeUndefined()
    expect(saved.conversations[0].messages[0].errorMessage).toBeUndefined()
    expect(loadPlaygroundState(9, 21).conversations[0].messages[0].imageCacheKeys).toEqual(['9:21:assistant-image:0'])
    expect(loadPlaygroundState(9, 22).conversations).toHaveLength(1)
  })

  it('restores all ten generated image references instead of truncating the result set', () => {
    const imageUrls = Array.from({ length: 10 }, (_, index) => `https://images.example/generated-${index}.png`)
    const imageCacheKeys = imageUrls.map((_, index) => `9:21:assistant-image:${index}`)
    const conversation = createConversation({
      id: 'ten-images',
      messages: [
        createMessage('user', 'make ten images'),
        createMessage('assistant', '', { imageUrls, imageCacheKeys, imageOutputFormat: 'webp' }),
      ],
    })

    savePlaygroundState(9, 21, createState({ activeConversationId: conversation.id, conversations: [conversation] }))
    const restoredMessage = loadPlaygroundState(9, 21).conversations[0].messages[1]
    expect(restoredMessage.imageUrls).toHaveLength(10)
    expect(restoredMessage.imageCacheKeys).toHaveLength(10)
    expect(restoredMessage.imageOutputFormat).toBe('webp')
  })

  it('drops chat conversations older than the 30-day retention window', () => {
    const now = Date.now()
    const expired = createConversation({
      id: 'expired-chat',
      mode: 'chat',
      updatedAt: now - (31 * 24 * 60 * 60 * 1000),
      createdAt: now - (31 * 24 * 60 * 60 * 1000),
      messages: [createMessage('user', 'old message', {
        createdAt: now - (31 * 24 * 60 * 60 * 1000),
        updatedAt: now - (31 * 24 * 60 * 60 * 1000),
      })],
    })

    expect(createState({ conversations: [expired] }).conversations).toHaveLength(0)
  })

  it('does not persist empty conversations created only while opening the playground', () => {
    const empty = createConversation({ id: 'empty-conversation', mode: 'chat' })
    const draft = createConversation({ id: 'draft-conversation', mode: 'chat', draftContent: 'keep this draft' })

    const saved = createState({
      activeConversationId: empty.id,
      conversations: [empty, draft],
    })

    expect(saved.conversations.map((conversation) => conversation.id)).toEqual(['draft-conversation'])
    expect(saved.activeConversationId).toBe('draft-conversation')
  })

  it('migrates v1 history into one v2 conversation and turns leading system data into conversation settings', () => {
    localStorage.setItem('sub2api.playground.v1.user.5.key.8', JSON.stringify({
      version: 1,
      model: 'gpt-4.1',
      draftRole: 'system',
      draftContent: 'Keep answers concise.',
      parameters: {
        temperature: 0.4,
        top_p: 0.9,
        max_tokens: 256,
        frequency_penalty: 0.2,
        presence_penalty: 0.1,
        seed: 42,
        stream: false,
      },
      messages: [
        createMessage('system', 'Use bullet points.', { id: 'legacy-system' }),
        createMessage('user', 'Explain retries.', { id: 'legacy-user' }),
      ],
    }))

    const migrated = loadPlaygroundState(5, 8)
    expect(migrated.version).toBe(2)
    expect(migrated.conversations).toHaveLength(1)
    expect(migrated.conversations[0].systemPrompt).toBe('Use bullet points.\n\nKeep answers concise.')
    expect(migrated.conversations[0].messages).toEqual([
      expect.objectContaining({ id: 'legacy-user', role: 'user', content: 'Explain retries.' }),
    ])
    expect(migrated.parameters).toEqual({
      temperature: 0.4,
      top_p: 0.9,
      max_tokens: 256,
      frequency_penalty: 0.2,
      presence_penalty: 0.1,
      seed: 42,
      stream: false,
      enabled: {
        temperature: true,
        top_p: true,
        max_tokens: true,
        frequency_penalty: true,
        presence_penalty: true,
        seed: true,
      },
    })
    expect(localStorage.getItem('sub2api.playground.v1.user.5.key.8')).toBeNull()
    expect(localStorage.getItem('sub2api.playground.v2.user.5.index')).not.toBeNull()
  })

  it('trims only an oversized conversation and leaves sibling history intact', () => {
    const oversized = createConversation({
      id: 'oversized',
      title: 'Large chat',
      messages: Array.from({ length: 48 }, (_, index) => createMessage('user', `message-${index}-${'x'.repeat(50_000)}`)),
    })
    const sibling = createConversation({
      id: 'sibling',
      title: 'Keep me',
      messages: [createMessage('user', 'keep this conversation')],
    })

    const saved = savePlaygroundState(3, 5, createState({
      activeConversationId: sibling.id,
      conversations: [oversized, sibling],
    }))
    const savedOversized = saved.conversations.find((conversation) => conversation.id === oversized.id)!
    const reloaded = loadPlaygroundState(3, 5)

    expect(savedOversized.messages.length).toBeLessThan(oversized.messages.length)
    expect(reloaded.conversations.find((conversation) => conversation.id === sibling.id)?.messages).toEqual([
      expect.objectContaining({ content: 'keep this conversation' }),
    ])
  })

  it('drops the oldest conversations when the complete browser snapshot exceeds 4 MiB', () => {
    const conversations = Array.from({ length: 16 }, (_, index) => createConversation({
      id: `conversation-${index}`,
      title: `Conversation ${index}`,
      messages: Array.from({ length: 48 }, (__, messageIndex) => (
        createMessage('user', `message-${messageIndex}-${'x'.repeat(8000)}`, {
          createdAt: index,
          updatedAt: index,
        })
      )),
      createdAt: index,
      updatedAt: index,
    }))

    const saved = savePlaygroundState(10, 11, createState({
      activeConversationId: 'conversation-15',
      conversations,
    }))

    expect(saved.conversations.length).toBeLessThan(conversations.length)
    expect(saved.conversations[0].id).toBe('conversation-15')
    expect(saved.conversations[saved.conversations.length - 1]?.id).not.toBe('conversation-0')
  })

  it('clears both v1 and v2 data for one user without touching another user', () => {
    const conversation = createConversation({ id: 'user-one', messages: [createMessage('user', 'remove')] })
    savePlaygroundState(1, 1, createState({ activeConversationId: conversation.id, conversations: [conversation] }))
    localStorage.setItem('sub2api.playground.v1.user.1.key.99', 'legacy')

    const otherConversation = createConversation({ id: 'user-two', messages: [createMessage('user', 'keep')] })
    savePlaygroundState(2, 1, createState({ activeConversationId: otherConversation.id, conversations: [otherConversation] }))

    clearPlaygroundStatesForUser(1)

    expect(loadPlaygroundState(1, 1).conversations).toEqual([])
    expect(localStorage.getItem('sub2api.playground.v1.user.1.key.99')).toBeNull()
    expect(loadPlaygroundState(2, 1).conversations).toHaveLength(1)
    clearAllPlaygroundStates()
    expect(loadPlaygroundState(2, 1).conversations).toEqual([])
  })

  it('clears the unified user state regardless of the selected key', () => {
    const first = createConversation({ id: 'first', messages: [createMessage('user', 'keep')] })
    const second = createConversation({ id: 'second', messages: [createMessage('user', 'stay')] })
    savePlaygroundState(1, 1, createState({ activeConversationId: first.id, conversations: [first] }))
    savePlaygroundState(1, 2, createState({ activeConversationId: second.id, conversations: [second] }))

    clearPlaygroundState(1, 1)

    expect(loadPlaygroundState(1, 1).conversations).toEqual([])
    expect(loadPlaygroundState(1, 2).conversations).toHaveLength(0)
  })

  it('flushes the captured key state before a rapid key switch', async () => {
    vi.useFakeTimers()
    try {
      const writes: Array<{ userId: number; keyId: number; activeConversationId: string | null }> = []
      const scheduler = createPlaygroundPersistScheduler(150, (userId, keyId, state) => {
        writes.push({ userId, keyId, activeConversationId: state.activeConversationId })
      })
      const first = createConversation({ id: 'one', messages: [createMessage('user', 'first')] })
      const second = createConversation({ id: 'two', messages: [createMessage('user', 'second')] })

      scheduler.schedule(7, 1, createState({ activeConversationId: first.id, conversations: [first] }))
      scheduler.flush()
      scheduler.schedule(7, 2, createState({ activeConversationId: second.id, conversations: [second] }))
      await vi.advanceTimersByTimeAsync(150)

      expect(writes).toEqual([
        { userId: 7, keyId: 1, activeConversationId: 'one' },
        { userId: 7, keyId: 2, activeConversationId: 'two' },
      ])
    } finally {
      vi.useRealTimers()
    }
  })

  it('keeps the newer local conversation when the remote history is stale', () => {
    const localConversation = createConversation({
      id: 'in-flight',
      updatedAt: 200,
      messages: [createMessage('assistant', 'completed locally', { createdAt: 200, updatedAt: 200 })],
    })
    const remoteConversation = createConversation({
      id: 'in-flight',
      updatedAt: 100,
      messages: [createMessage('assistant', 'still waiting', { createdAt: 100, updatedAt: 100 })],
    })

    const merged = mergePlaygroundStates([
      createState({ activeConversationId: localConversation.id, conversations: [localConversation] }),
      createState({ activeConversationId: remoteConversation.id, conversations: [remoteConversation] }),
    ])

    expect(merged.conversations).toHaveLength(1)
    expect(merged.conversations[0].messages[0].content).toBe('completed locally')
  })
  it('preserves long assistant replies when saving and restoring history', () => {
    const longReply = 'x'.repeat(50_000)
    const conversation = createConversation({
      id: 'long-reply',
      mode: 'chat',
      messages: [
        createMessage('user', 'Give me a long answer.'),
        createMessage('assistant', longReply),
      ],
    })

    savePlaygroundState(7, 1, createState({
      activeConversationId: conversation.id,
      conversations: [conversation],
    }))

    const restored = loadPlaygroundState(7, 1)
    expect(restored.conversations[0]?.messages[1]?.content).toBe(longReply)
  })

  it('serializes asynchronous persistence so an older write cannot finish last', async () => {
    vi.useFakeTimers()
    try {
      const writes: string[] = []
      let resolveFirst: (() => void) | undefined
      const firstWrite = new Promise<void>((resolve) => {
        resolveFirst = resolve
      })
      const scheduler = createPlaygroundPersistScheduler(150, async (_userId, _keyId, state) => {
        writes.push(state.activeConversationId ?? '')
        if (writes.length === 1) await firstWrite
      })
      const first = createConversation({ id: 'first', messages: [createMessage('user', 'first')] })
      const second = createConversation({ id: 'second', messages: [createMessage('user', 'second')] })

      scheduler.schedule(7, 1, createState({ activeConversationId: first.id, conversations: [first] }))
      scheduler.flush()
      scheduler.schedule(7, 1, createState({ activeConversationId: second.id, conversations: [second] }))
      await vi.advanceTimersByTimeAsync(150)

      expect(writes).toEqual(['first'])
      resolveFirst?.()
      await vi.runAllTimersAsync()
      expect(writes).toEqual(['first', 'second'])
    } finally {
      vi.useRealTimers()
    }
  })
})
