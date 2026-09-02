import { describe, expect, it } from 'vitest'
import { normalizeCanvasPromptRegistry } from '../canvas/promptRegistry'

describe('canvas prompt registry', () => {
  it('normalizes valid records, preserves source metadata, and drops malformed entries', () => {
    const prompts = normalizeCanvasPromptRegistry([
      { id: 'source:1', sourceId: 'source', title: 'Editorial', prompt: '  Make an editorial portrait. ', tags: ['portrait', ' portrait '] },
      { id: 'source:1', sourceId: 'source', title: 'Duplicate', prompt: 'ignored' },
      { id: 'source:2', sourceId: 'source', description: 'Fallback title', prompt: 'Make a product image.', tags: ['product'] },
      { id: 'bad', prompt: '' },
      null,
    ])

    expect(prompts).toEqual([
      { id: 'source:1', title: 'Editorial', content: 'Make an editorial portrait.', tags: ['portrait', 'portrait'], source: 'source' },
      { id: 'source:2', title: 'Fallback title', content: 'Make a product image.', tags: ['product'], source: 'source' },
    ])
  })
})
