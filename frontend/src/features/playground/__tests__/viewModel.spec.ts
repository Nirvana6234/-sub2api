import { describe, expect, it } from 'vitest'
import type { ApiKey } from '@/types'
import { buildChatPayload, buildPlaygroundImageRequest, canStartPlaygroundCompletion, createMessage, DEFAULT_PLAYGROUND_PARAMETERS, deriveConversationTitle, formatPlaygroundKeyLabel, maskPlaygroundApiKey, normalizePlaygroundChatModels, normalizePlaygroundImageModels, normalizePlaygroundKeys, rebuildConversationAfterUserEdit, resolvePlaygroundDefaultKeyId, resolvePlaygroundDefaultModel, resolveSelectedKeyId, resolveSelectedModel, withSystemPrompt } from '../viewModel'

function createKey(overrides: Partial<ApiKey>): ApiKey {
  const baseGroup = {
    id: 11,
    name: 'Group A',
    description: null,
    platform: 'openai' as const,
    rate_multiplier: 1,
    is_exclusive: false,
    status: 'active' as const,
    subscription_type: 'standard' as const,
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    allow_image_generation: false,
    allow_batch_image_generation: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    batch_image_discount_multiplier: 1,
    batch_image_hold_multiplier: 1,
    image_price_1k: null,
    image_price_2k: null,
    image_price_4k: null,
    video_rate_independent: false,
    video_rate_multiplier: 1,
    video_price_480p: null,
    video_price_720p: null,
    video_price_1080p: null,
    web_search_price_per_call: null,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    allow_live: false,
    require_oauth_only: false,
    require_privacy_set: false,
    created_at: '',
    updated_at: '',
  }

  return {
    id: 1,
    user_id: 1,
    key: '',
    name: 'Key 1',
    group_id: 11,
    auto_group: false,
    auto_group_strategy: 'price',
    auto_group_ids: [],
    status: 'active',
    ip_whitelist: [],
    ip_blacklist: [],
    last_used_at: null,
    last_used_ip: null,
    quota: 0,
    quota_used: 0,
    expires_at: null,
    created_at: '',
    updated_at: '',
    current_concurrency: 0,
    group: baseGroup,
    rate_limit_5h: 0,
    rate_limit_1d: 0,
    rate_limit_7d: 0,
    usage_5h: 0,
    usage_1d: 0,
    usage_7d: 0,
    window_5h_start: null,
    window_1d_start: null,
    window_7d_start: null,
    reset_5h_at: null,
    reset_1d_at: null,
    reset_7d_at: null,
    ...overrides,
  }
}

describe('playground view model', () => {
  it('filters to active keys with a group assignment and preserves key and group labels', () => {
    const keys = normalizePlaygroundKeys([
      createKey({ id: 2, status: 'inactive' }),
      createKey({ id: 3, group_id: null }),
      createKey({ id: 4, group_id: 21, group: { ...createKey({ id: 4 }).group!, id: 21, name: 'Ops' } }),
      createKey({ id: 5, group: undefined }),
      createKey({ id: 6, group: { ...createKey({ id: 6 }).group!, status: 'inactive' } }),
    ])

    expect(keys).toEqual([
      { id: 4, name: 'Key 1', maskedKey: 'Hidden key', status: 'active', groupId: 21, groupName: 'Ops', autoGroup: false, autoGroupStrategy: 'price', platform: 'openai' },
    ])
  })

  it('keeps active automatic-group keys without a persisted fixed group', () => {
    const keys = normalizePlaygroundKeys([
      createKey({
        id: 8,
        name: '自动线路',
        group_id: null,
        group: undefined,
        auto_group: true,
        auto_group_strategy: 'balanced',
        auto_group_ids: [11, 12],
      }),
    ])

    expect(keys).toEqual([
      {
        id: 8,
        name: '自动线路',
        maskedKey: 'Hidden key',
        status: 'active',
        groupId: null,
        groupName: '',
        autoGroup: true,
        autoGroupStrategy: 'balanced',
      },
    ])
  })

  it('falls back to the first valid key and model when the saved selection is stale', () => {
    expect(resolveSelectedKeyId([{ id: 7, name: 'Key 7', maskedKey: 'sk-foo...1234', status: 'active', groupId: 1, groupName: 'A', autoGroup: false, autoGroupStrategy: 'price' }], 999)).toBe(7)
    expect(resolveSelectedModel([{ id: 'gpt-4.1' }, { id: 'gpt-4o-mini' }], 'missing')).toBe('gpt-4.1')
  })

  it('prefers the automatically provisioned key for each playground mode', () => {
    const keys = [
      { id: 3, name: 'Other key', maskedKey: 'sk...other', status: 'active' as const, groupId: 1, groupName: 'GPT', autoGroup: false, autoGroupStrategy: 'price' as const },
      { id: 7, name: 'Playground Chat', maskedKey: 'sk...chat', status: 'active' as const, groupId: 1, groupName: 'GPT', autoGroup: false, autoGroupStrategy: 'price' as const },
      { id: 9, name: 'Playground Images', maskedKey: 'sk...image', status: 'active' as const, groupId: 2, groupName: 'Images', autoGroup: false, autoGroupStrategy: 'price' as const },
    ]

    expect(resolvePlaygroundDefaultKeyId(keys, 'chat')).toBe(7)
    expect(resolvePlaygroundDefaultKeyId(keys, 'image')).toBe(9)
  })

  it('uses stable primary models as the zero-configuration defaults', () => {
    expect(resolvePlaygroundDefaultModel([
      { id: 'gpt-4.1', name: 'GPT-4.1' },
      { id: 'gpt-5.4', name: 'GPT-5.4' },
    ], 'chat')).toBe('gpt-5.4')
    expect(resolvePlaygroundDefaultModel([
      { id: 'dall-e-3', name: 'DALL-E 3' },
      { id: 'gpt-image-1', name: 'GPT Image 1' },
    ], 'image')).toBe('gpt-image-1')
    expect(resolvePlaygroundDefaultModel([
      { id: 'dall-e-3', name: 'DALL-E 3' },
      { id: 'gpt-image-2', name: 'GPT Image 2' },
    ], 'image')).toBe('gpt-image-2')
  })

  it('honors the administrator model before built-in fallbacks', () => {
    expect(resolvePlaygroundDefaultModel([
      { id: 'gpt-4.1', name: 'GPT-4.1' },
      { id: 'gpt-5.4', name: 'GPT-5.4' },
    ], 'chat', 'gpt-4.1')).toBe('gpt-4.1')
  })

  it('uses the API-key page masking format without retaining the full credential in labels', () => {
    expect(maskPlaygroundApiKey('sk-fa6-abcdef3735')).toBe('sk-fa6...3735')
    expect(maskPlaygroundApiKey('')).toBe('Hidden key')
  })

  it('uses the key name, ID, and assigned group in the workspace selector', () => {
    expect(formatPlaygroundKeyLabel({ id: 7, name: '生产调用', maskedKey: 'sk-foo...1234', status: 'active', groupId: 1, groupName: 'OpenAI', autoGroup: false, autoGroupStrategy: 'price' }))
      .toBe('生产调用 - #7 - OpenAI')
  })

  it('formats automatic keys with the provided localized routing label', () => {
    expect(formatPlaygroundKeyLabel({ id: 8, name: '自动线路', maskedKey: 'sk-auto...1234', status: 'active', groupId: null, groupName: '', autoGroup: true, autoGroupStrategy: 'speed' }, '自动 · 速度优先'))
      .toBe('自动线路 - #8 - 自动 · 速度优先')
  })

  it('keeps normal composing user-only while allowing an existing outbound turn to be regenerated', () => {
    const assistant = createMessage('assistant', 'Already answered.')
    const user = createMessage('user', 'Ask a follow-up.')

    expect(canStartPlaygroundCompletion([assistant], '')).toBe(false)
    expect(canStartPlaygroundCompletion([assistant], 'Continue.')).toBe(true)
    expect(canStartPlaygroundCompletion([user], '')).toBe(true)
  })

  it('replaces an edited user turn and removes its old answer and later branch', () => {
    const firstUser = createMessage('user', 'First question', { id: 'user-1' })
    const firstAssistant = createMessage('assistant', 'First answer', { id: 'assistant-1' })
    const editedUser = createMessage('user', 'Old question', { id: 'user-2' })
    const oldAssistant = createMessage('assistant', 'Old answer', { id: 'assistant-2' })
    const rewritten = rebuildConversationAfterUserEdit(
      [firstUser, firstAssistant, editedUser, oldAssistant],
      'user-2',
      'New question',
      123,
    )

    expect(rewritten).toEqual([
      firstUser,
      firstAssistant,
      expect.objectContaining({ id: 'user-2', content: 'New question', updatedAt: 123 }),
    ])
    expect(rebuildConversationAfterUserEdit([firstUser], 'user-1', '   ')).toBeNull()
    expect(rebuildConversationAfterUserEdit([firstAssistant], 'assistant-1', 'Edited')).toBeNull()
  })

  it('derives compact conversation titles from the first user message text', () => {
    expect(deriveConversationTitle('  Explain\n retry behavior   ')).toBe('Explain retry behavior')
    expect(deriveConversationTitle('')).toBe('')
    expect(deriveConversationTitle('x'.repeat(100))).toHaveLength(80)
  })

  it('prepends saved system instructions only to the request message list', () => {
    const userMessage = createMessage('user', 'Draft a release note.')
    const messages = withSystemPrompt([userMessage], '  Be concise.  ')

    expect(messages).toEqual([
      expect.objectContaining({ role: 'system', content: 'Be concise.' }),
      userMessage,
    ])
    expect(withSystemPrompt([userMessage], '   ')).toEqual([userMessage])
  })

  it('keeps only image-capable models for the image playground', () => {
    expect(normalizePlaygroundImageModels([
      { id: 'gpt-4.1' },
      { id: 'gpt-image-1.5' },
      { id: 'dall-e-3' },
      { id: 'gpt-image-1.5' },
    ])).toEqual([
      { id: 'dall-e-3' },
      { id: 'gpt-image-1.5' },
    ])
  })

  it('keeps chat and image model options mutually exclusive', () => {
    expect(normalizePlaygroundChatModels([
      { id: 'gpt-4.1' },
      { id: 'gpt-image-1.5' },
      { id: 'flux-pro' },
    ])).toEqual([{ id: 'gpt-4.1' }])
  })

  it('keeps model-name variants that are still chat models', () => {
    // sol / terra / luna / spark / codex 曾被当作非聊天模型误杀，
    // 而它们是网关上的主力对话模型。
    expect(normalizePlaygroundChatModels([
      { id: 'gpt-5.6-sol' },
      { id: 'gpt-5.6-terra' },
      { id: 'gpt-5.6-luna' },
      { id: 'gpt-5.3-codex-spark' },
    ]).map((model) => model.id)).toEqual([
      'gpt-5.3-codex-spark',
      'gpt-5.6-luna',
      'gpt-5.6-sol',
      'gpt-5.6-terra',
    ])
  })

  it('keeps current Anthropic model names', () => {
    // 实际命名是 claude-<族名>-<版本>，旧白名单按 claude-4-opus 书写，全部匹配不上。
    expect(normalizePlaygroundChatModels([
      { id: 'claude-opus-5' },
      { id: 'claude-sonnet-5' },
      { id: 'claude-opus-4-8' },
      { id: 'claude-fable-5' },
    ]).map((model) => model.id)).toEqual([
      'claude-fable-5',
      'claude-opus-4-8',
      'claude-opus-5',
      'claude-sonnet-5',
    ])
  })

  it('still drops non-chat capability models', () => {
    expect(normalizePlaygroundChatModels([
      { id: 'text-embedding-3-large' },
      { id: 'omni-moderation-latest' },
      { id: 'gpt-4o-realtime-preview' },
      { id: 'whisper-1' },
      { id: 'gpt-5.6' },
    ]).map((model) => model.id)).toEqual(['gpt-5.6'])
  })

  it('does not cap the chat model list', () => {
    const many = Array.from({ length: 12 }, (_, index) => ({ id: `gpt-5.${index}` }))
    expect(normalizePlaygroundChatModels(many)).toHaveLength(12)
  })

  it('keeps provider-specific image options out of Grok requests', () => {
    const input = {
      model: 'grok-imagine-image',
      prompt: 'a lighthouse',
      size: '1024x1024',
      quality: 'high',
      imageCount: 8,
      outputFormat: 'webp',
      outputCompression: 82,
      background: 'transparent',
      moderation: 'low',
      style: 'vivid',
      inputFidelity: 'high',
      hasReferenceImages: true,
    }

    const grok = buildPlaygroundImageRequest(input)
    expect(grok.body).not.toHaveProperty('output_format')
    expect(grok.body).not.toHaveProperty('background')
    expect(grok.body).not.toHaveProperty('input_fidelity')
    expect(grok.requestedImageCount).toBe(8)

    const gptImage = buildPlaygroundImageRequest({ ...input, model: 'gpt-image-1' })
    expect(gptImage.body).toMatchObject({ output_format: 'webp', output_compression: 82, background: 'transparent', moderation: 'low', style: 'vivid', input_fidelity: 'high' })
    expect(gptImage.body).toHaveProperty('response_format', 'b64_json')
  })

  it('keeps DALL-E 3 style while forcing one image', () => {
    const result = buildPlaygroundImageRequest({
      model: 'dall-e-3', prompt: 'a lighthouse', size: 'auto', quality: 'high', imageCount: 10,
      outputFormat: 'webp', outputCompression: 82, background: 'transparent', moderation: 'low',
      style: 'vivid', inputFidelity: 'high', hasReferenceImages: false,
    })
    expect(result.body).toMatchObject({ size: '1024x1024', quality: 'hd', style: 'vivid', response_format: 'b64_json' })
    expect(result.requestedImageCount).toBe(1)
  })

  it('only exposes primary chat models and hides operational model aliases', () => {
    expect(normalizePlaygroundChatModels([
      { id: 'text-embedding-3-large' },
      { id: 'gpt-4o' },
      { id: 'gpt-4o-realtime-preview' },
      { id: 'gpt-4.1-mini' },
      { id: 'claude-3-5-sonnet-20241022' },
      { id: 'gpt-4o-mini-transcribe' },
    ])).toEqual([
      { id: 'claude-3-5-sonnet-20241022' },
      { id: 'gpt-4.1-mini' },
      { id: 'gpt-4o' },
    ])
  })

  it('builds a standard chat payload without null-only fields or empty messages', () => {
    const payload = buildChatPayload({
      model: 'gpt-4.1',
      messages: [
        createMessage('system', '  Be concise  '),
        createMessage('user', '   '),
        createMessage('assistant', 'Ready.'),
      ],
      parameters: {
        temperature: 0.4,
        top_p: 0.9,
        max_tokens: null,
        frequency_penalty: 0.2,
        presence_penalty: 0.1,
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
    })

    expect(payload).toEqual({
      model: 'gpt-4.1',
      messages: [
        { role: 'system', content: 'Be concise' },
        { role: 'assistant', content: 'Ready.' },
      ],
      temperature: 0.4,
      top_p: 0.9,
      frequency_penalty: 0.2,
      presence_penalty: 0.1,
      stream: true,
    })
  })

  it('omits disabled parameters from the chat payload while always passing stream', () => {
    const payload = buildChatPayload({
      model: 'gpt-4.1',
      messages: [createMessage('user', 'Ping')],
      parameters: {
        ...DEFAULT_PLAYGROUND_PARAMETERS,
        temperature: 0.2,
        top_p: 0.8,
        max_tokens: 256,
        frequency_penalty: 0.5,
        presence_penalty: 0.4,
        seed: 1234,
        enabled: {
          temperature: false,
          top_p: true,
          max_tokens: false,
          frequency_penalty: false,
          presence_penalty: true,
          seed: true,
        },
      },
    })

    expect(payload).toEqual({
      model: 'gpt-4.1',
      messages: [{ role: 'user', content: 'Ping' }],
      top_p: 0.8,
      presence_penalty: 0.4,
      seed: 1234,
      stream: true,
    })
  })
})
