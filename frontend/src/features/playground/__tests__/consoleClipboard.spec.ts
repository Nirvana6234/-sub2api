import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('Playground console clipboard support', () => {
  const source = readFileSync(resolve(__dirname, '../PlaygroundConsole.vue'), 'utf8')

  it('offers a copy button beside the download button for generated images', () => {
    expect(source).toContain("t('playground.copyImage')")
    expect(source).toContain('copyGeneratedImage(message, imageUrl, imageIndex)')
    expect(source).toContain('v-if="clipboardImageSupported"')
    // 复用共用实现，不要在这里再写一份 ClipboardItem 逻辑。
    expect(source).toContain("from './imageClipboard'")
    expect(source).not.toContain('new ClipboardItem(')
  })

  it('tracks per-image copy state so one copy cannot block another image', () => {
    expect(source).toContain('const copyingImageIds = ref<Set<string>>(new Set())')
    expect(source).toContain('function isCopyingImage(messageId: string, imageIndex: number)')
    expect(source).toContain('nextCopyingIds.delete(imageId)')
  })

  it('reads both files and items when pasting reference images', () => {
    // 只读 files 会漏掉部分浏览器从网页复制图片的情况，画布模式一直是两者都读。
    expect(source).toContain('async function handleComposerPaste(event: ClipboardEvent)')
    expect(source).toContain("Array.from(event.clipboardData?.items ?? [])")
    expect(source).toContain("Array.from(event.clipboardData?.files ?? [])")
    expect(source).toContain("item.kind === 'file'")
    expect(source).toContain('item.getAsFile()')
  })

  it('listens for paste globally so the composer need not be focused first', () => {
    expect(source).toContain("window.addEventListener('paste', handleComposerPaste)")
    expect(source).toContain("window.removeEventListener('paste', handleComposerPaste)")
    // 保留 textarea 上的 @paste 会让事件冒泡到 window 后被处理两次，附件加两遍。
    expect(source).not.toContain('@paste="handleComposerPaste"')
  })
})
