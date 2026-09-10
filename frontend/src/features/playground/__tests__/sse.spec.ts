import { describe, expect, it } from 'vitest'
import { extractCompletionResult, extractStreamDelta, parseSSEChunk, parseSSEData } from '../sse'

describe('playground sse helpers', () => {
  it('parses framed SSE chunks and preserves the trailing remainder', () => {
    const parsed = parseSSEChunk(
      'data: {"choices":[{"delta":{"content":"Hel"}}]}\n\n' +
      'event: meta\ndata: {"choices":[{"delta":{"content":"lo"}}]}\n\n' +
      'data: {"partial":true}',
    )

    expect(parsed.frames).toHaveLength(2)
    expect(parsed.frames[0].data).toContain('"Hel"')
    expect(parsed.frames[1].event).toBe('meta')
    expect(parsed.remainder).toContain('"partial":true')
  })

  it('extracts both reasoning_content and reasoning payloads', () => {
    expect(extractStreamDelta({
      choices: [{ delta: { content: 'A', reasoning_content: 'think-1' }, finish_reason: null }],
    })).toEqual({
      contentDelta: 'A',
      reasoningDelta: 'think-1',
      finishReason: null,
    })

    expect(extractStreamDelta({
      choices: [{ delta: { content: 'B', reasoning: 'think-2' }, finish_reason: 'stop' }],
    })).toEqual({
      contentDelta: 'B',
      reasoningDelta: 'think-2',
      finishReason: 'stop',
    })

    expect(extractCompletionResult({
      choices: [{ message: { content: 'done', reasoning: 'trace' }, finish_reason: 'stop' }],
    })).toEqual({
      content: 'done',
      reasoningContent: 'trace',
      finishReason: 'stop',
    })
  })

  it('rejects multiple choices instead of silently truncating a response', () => {
    const choices = [
      { delta: { content: 'first' }, message: { content: 'first' } },
      { delta: { content: 'second' }, message: { content: 'second' } },
    ]

    expect(() => extractStreamDelta({ choices })).toThrow('Playground supports exactly one completion choice')
    expect(() => extractCompletionResult({ choices })).toThrow('Playground supports exactly one completion choice')
  })

  it('recognizes [DONE] and still throws for malformed JSON frames', () => {
    expect(parseSSEData('[DONE]')).toBe('[DONE]')
    expect(() => parseSSEData('{oops')).toThrow()
  })
})
