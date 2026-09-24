import { describe, expect, it } from 'vitest'
import { describeModel } from '../modelKind'

describe('describeModel', () => {
  it.each([
    ['gpt-5.5', 'OpenAI', 'chat'],
    ['gpt-5.5-codex', 'OpenAI', 'code'],
    ['gpt-image-2', 'OpenAI', 'image'],
    ['o3-mini', 'OpenAI', 'chat'],
    ['claude-sonnet-5', 'Claude', 'chat'],
    ['gemini-3-pro', 'Gemini', 'chat'],
    ['imagen-4', 'Gemini', 'image'],
    ['deepseek-coder', 'DeepSeek', 'code'],
    ['qwen3-max', 'Qwen', 'chat'],
  ])('%s → %s / %s', (model, vendor, kind) => {
    expect(describeModel(model)).toEqual({ vendor, kind })
  })

  it('falls back to a plain chat model without a vendor label', () => {
    expect(describeModel('some-custom-model')).toEqual({ vendor: null, kind: 'chat' })
    expect(describeModel(undefined)).toEqual({ vendor: null, kind: 'chat' })
  })
})
