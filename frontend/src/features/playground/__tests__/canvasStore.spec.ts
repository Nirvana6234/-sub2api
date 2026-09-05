import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useCanvasStore } from '../canvas/canvasStore'
import type { CanvasNode } from '../canvas/types'

function node(id: string, overrides: Partial<CanvasNode> = {}): CanvasNode {
  return {
    id,
    type: 'image',
    kind: 'generator',
    x: 0,
    y: 0,
    width: 360,
    height: 420,
    prompt: id,
    model: 'gpt-image-1',
    status: 'idle',
    createdAt: 1,
    updatedAt: 1,
    ...overrides,
  }
}

describe('playground canvas store', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('normalizes legacy image nodes as generator nodes with four results', () => {
    localStorage.setItem('sub2api.playground.canvas.v1.user.101', JSON.stringify({
      version: 1,
      activeProjectId: 'legacy-project',
      projects: [{
        id: 'legacy-project',
        title: 'Legacy',
        nodes: [node('legacy-node', { kind: undefined, imageCount: undefined })],
        connections: [],
        viewport: { x: 0, y: 0, scale: 1 },
        backgroundMode: 'dots',
        createdAt: 1,
        updatedAt: 1,
      }],
    }))

    const store = useCanvasStore(101)
    const restored = store.activeProject.value?.nodes[0]

    expect(restored?.kind).toBe('generator')
    expect(restored?.imageCount).toBe(4)
  })

  it('rejects self-connections and duplicate connections', () => {
    const store = useCanvasStore(102)
    const project = store.activeProject.value!
    store.addNode(node('source'))
    store.addNode(node('target'))

    const connection = { id: 'connection-1', from: 'source', to: 'target', kind: 'reference' as const }
    expect(store.addConnection(connection)).toBe(true)
    expect(store.addConnection({ ...connection, id: 'connection-2' })).toBe(false)
    expect(store.addConnection({ id: 'connection-self', from: 'source', to: 'source' })).toBe(false)
    expect(project.connections).toEqual([connection])

    store.removeConnection('connection-1')
    expect(project.connections).toEqual([])
  })

  it('removes result descendants and their connections with the generator node', () => {
    const store = useCanvasStore(103)
    store.addNode(node('generator'))
    store.addNode(node('result-1', { kind: 'result', sourceNodeId: 'generator', resultIndex: 0 }))
    store.addNode(node('result-2', { kind: 'result', sourceNodeId: 'result-1', resultIndex: 1 }))
    store.addNode(node('other'))
    store.addConnection({ id: 'result-link', from: 'generator', to: 'result-1', kind: 'result' })
    store.addConnection({ id: 'nested-link', from: 'result-1', to: 'result-2', kind: 'result' })
    store.addConnection({ id: 'other-link', from: 'generator', to: 'other', kind: 'reference' })

    store.removeNode('generator')

    expect(store.activeProject.value?.nodes.map((item) => item.id)).toEqual(['other'])
    expect(store.activeProject.value?.connections.map((item) => item.id)).toEqual([])
  })

  it('undoes and redoes a canvas mutation without persisting history metadata', () => {
    const store = useCanvasStore(104)
    store.checkpoint()
    store.addNode(node('history-node'))

    expect(store.activeProject.value?.nodes.map((item) => item.id)).toEqual(['history-node'])
    store.undo()
    expect(store.activeProject.value?.nodes).toEqual([])
    expect(JSON.stringify(store.state)).not.toContain('historyPast')

    store.redo()
    expect(store.activeProject.value?.nodes.map((item) => item.id)).toEqual(['history-node'])
  })

  it('duplicates projects with remapped node and connection ids', () => {
    const store = useCanvasStore(105)
    store.addNode(node('source'))
    store.addNode(node('target'))
    store.addConnection({ id: 'link', from: 'source', to: 'target', kind: 'reference' })
    const originalId = store.activeProject.value!.id
    const copy = store.duplicateProject(originalId)

    expect(copy).not.toBeNull()
    expect(copy?.id).not.toBe(originalId)
    expect(copy?.nodes.map((item) => item.id)).not.toContain('source')
    expect(copy?.connections[0]?.from).not.toBe('source')
    expect(copy?.connections[0]?.to).not.toBe('target')
  })

  it('restores extended node data and bounds cached result arrays', () => {
    localStorage.setItem('sub2api.playground.canvas.v1.user.106', JSON.stringify({
      version: 1,
      activeProjectId: 'extended-project',
      projects: [{
        id: 'extended-project',
        title: 'Extended',
        nodes: [
          node('text-node', { type: 'text', textContent: 'Canvas notes' }),
          node('config-node', { type: 'config', config: { size: '1536x1024', quality: 'high' } }),
          node('batch-node', {
            kind: 'result',
            imageCacheKey: 'cache-0',
            imageCacheKeys: Array.from({ length: 12 }, (_, index) => `cache-${index}`),
            imageUrls: Array.from({ length: 12 }, (_, index) => `blob:image-${index}`),
            imageVariants: [
              { id: 'slot-1', status: 'success', cacheKey: 'cache-1', url: 'blob:image-1', revisionPrompt: 'make it warmer' },
              { id: 'slot-2', status: 'error', errorMessage: 'timeout' },
            ],
            primaryImageIndex: 4,
            imageRevisionCount: 3,
            imagePrompts: ['keyword one banner', '', 'keyword three banner'],
          }),
        ],
        connections: [],
        viewport: { x: 0, y: 0, scale: 1 },
        backgroundMode: 'dots',
        createdAt: 1,
        updatedAt: 1,
      }],
    }))

    const store = useCanvasStore(106)
    const [textNode, configNode, batchNode] = store.activeProject.value!.nodes

    expect(textNode?.textContent).toBe('Canvas notes')
    expect(configNode?.config).toEqual({ size: '1536x1024', quality: 'high' })
    expect(batchNode?.imageCacheKeys).toHaveLength(10)
    expect(batchNode?.imageUrls).toHaveLength(10)
    expect(batchNode?.primaryImageIndex).toBe(4)
    expect(batchNode?.imageVariants).toHaveLength(2)
    expect(batchNode?.imageVariants?.[0]?.revisionPrompt).toBe('make it warmer')
    expect(batchNode?.imageRevisionCount).toBe(3)
    expect(batchNode?.imageVariants?.[1]?.errorMessage).toBe('timeout')
    // 批量生成的逐图提示词要连空位一起原样存回——空位代表「这张回落到共享提示词」，
    // 不是脏数据，压缩掉会让 index 对不上后面对应的图片槽位。
    expect(batchNode?.imagePrompts).toEqual(['keyword one banner', '', 'keyword three banner'])
  })

  it('drops an all-blank imagePrompts array instead of persisting empty noise', () => {
    localStorage.setItem('sub2api.playground.canvas.v1.user.113', JSON.stringify({
      version: 1,
      activeProjectId: 'blank-prompts-project',
      projects: [{
        id: 'blank-prompts-project',
        title: 'Blank prompts',
        nodes: [node('blank-prompts-node', { imagePrompts: ['', '  ', ''] })],
        connections: [],
        viewport: { x: 0, y: 0, scale: 1 },
        backgroundMode: 'dots',
        createdAt: 1,
        updatedAt: 1,
      }],
    }))

    const store = useCanvasStore(113)
    expect(store.activeProject.value!.nodes[0]?.imagePrompts).toBeUndefined()
  })

  it('persists the image free-resize preference', () => {
    localStorage.setItem('sub2api.playground.canvas.v1.user.111', JSON.stringify({
      version: 1,
      activeProjectId: 'resize-project',
      projects: [{
        id: 'resize-project',
        title: 'Resize',
        nodes: [node('image-node', { freeResize: true })],
        connections: [],
        viewport: { x: 0, y: 0, scale: 1 },
        backgroundMode: 'dots',
        createdAt: 1,
        updatedAt: 1,
      }],
    }))

    const restored = useCanvasStore(111).activeProject.value?.nodes[0]
    expect(restored?.freeResize).toBe(true)
  })

  it('restores text generation counts and alternate text results', () => {
    localStorage.setItem('sub2api.playground.canvas.v1.user.107', JSON.stringify({
      version: 1,
      activeProjectId: 'text-project',
      projects: [{
        id: 'text-project',
        title: 'Text',
        nodes: [node('text-node', {
          type: 'text',
          textCount: 4,
          textVariants: [
            { id: 'variant-1', status: 'success', content: 'First draft' },
            { id: 'variant-2', status: 'error', content: '', errorMessage: 'Temporary failure' },
          ],
          primaryTextId: 'variant-1',
          textContent: 'First draft',
        })],
        connections: [],
        viewport: { x: 0, y: 0, scale: 1 },
        backgroundMode: 'dots',
        createdAt: 1,
        updatedAt: 1,
      }],
    }))

    const store = useCanvasStore(107)
    const restored = store.activeProject.value?.nodes[0]

    expect(restored?.textCount).toBe(4)
    expect(restored?.primaryTextId).toBe('variant-1')
    expect(restored?.textVariants).toHaveLength(2)
    expect(restored?.textVariants?.[1]?.errorMessage).toBe('Temporary failure')
  })

  it('restores group membership and clears it when the group node is removed', () => {
    localStorage.setItem('sub2api.playground.canvas.v1.user.110', JSON.stringify({
      version: 1,
      activeProjectId: 'group-project',
      projects: [{
        id: 'group-project',
        title: 'Group',
        nodes: [
          node('group-node', { type: 'group', prompt: 'Scene group' }),
          node('child-node', { groupId: 'group-node', prompt: 'Grouped child' }),
        ],
        connections: [],
        viewport: { x: 0, y: 0, scale: 1 },
        backgroundMode: 'dots',
        createdAt: 1,
        updatedAt: 1,
      }],
    }))

    const store = useCanvasStore(110)
    expect(store.activeProject.value?.nodes.find((item) => item.id === 'child-node')?.groupId).toBe('group-node')

    store.removeNode('group-node')

    expect(store.activeProject.value?.nodes.map((item) => item.id)).toEqual(['child-node'])
    expect(store.activeProject.value?.nodes[0]?.groupId).toBeUndefined()
  })

  it('restores built-in plugin composer settings without accepting arbitrary nested metadata', () => {
    localStorage.setItem('sub2api.playground.canvas.v1.user.109', JSON.stringify({
      version: 1,
      activeProjectId: 'plugin-project',
      projects: [{
        id: 'plugin-project',
        title: 'Plugin',
        nodes: [node('plugin-node', {
          type: 'panorama:viewer',
          pluginMetadata: {
            composerContent: 'A coastal panorama',
            composerOptions: { size: '1024x1024', quality: 'high', count: 2 },
            untrusted: { deeply: { nested: true } },
          },
        })],
        connections: [],
        viewport: { x: 0, y: 0, scale: 1 },
        backgroundMode: 'dots',
        createdAt: 1,
        updatedAt: 1,
      }],
    }))

    const restored = useCanvasStore(109).activeProject.value?.nodes[0]
    expect(restored?.pluginMetadata).toMatchObject({
      composerContent: 'A coastal panorama',
      composerOptions: { size: '1024x1024', quality: 'high', count: 2 },
    })
    expect(restored?.pluginMetadata).not.toHaveProperty('untrusted')
  })

  it('clears the active canvas and can undo the operation', () => {
    const store = useCanvasStore(107)
    store.addNode(node('source'))
    store.addNode(node('target'))
    store.addConnection({ id: 'link', from: 'source', to: 'target', kind: 'reference' })
    store.checkpoint()

    store.clearProject()
    expect(store.activeProject.value?.nodes).toEqual([])
    expect(store.activeProject.value?.connections).toEqual([])

    store.undo()
    expect(store.activeProject.value?.nodes.map((item) => item.id)).toEqual(['source', 'target'])
    expect(store.activeProject.value?.connections).toHaveLength(1)
  })

  it('persists assistant history with the active canvas project', () => {
    const store = useCanvasStore(108)
    store.updateProject({ assistantMessages: [
      { id: 'u1', role: 'user', content: 'Refine this concept', createdAt: 1 },
      { id: 'a1', role: 'assistant', content: 'Use a colder palette.', createdAt: 2 },
    ] })

    const restored = useCanvasStore(108)
    expect(restored.activeProject.value?.assistantMessages).toEqual([
      { id: 'u1', role: 'user', content: 'Refine this concept', createdAt: 1 },
      { id: 'a1', role: 'assistant', content: 'Use a colder palette.', createdAt: 2 },
    ])
  })

  it('coalesces rapid viewport writes and flushes them after the debounce window', () => {
    vi.useFakeTimers()
    const store = useCanvasStore(112)
    expect(localStorage.getItem('sub2api.playground.canvas.v1.user.112')).toBeNull()

    store.updateViewport({ x: 10, y: 20, scale: 1.1 })
    expect(localStorage.getItem('sub2api.playground.canvas.v1.user.112')).toBeNull()

    vi.advanceTimersByTime(180)
    const persisted = JSON.parse(localStorage.getItem('sub2api.playground.canvas.v1.user.112') || '{}') as { projects?: Array<{ viewport?: { x?: number; y?: number } }> }
    expect(persisted.projects?.[0]?.viewport).toMatchObject({ x: 10, y: 20 })
  })
})
