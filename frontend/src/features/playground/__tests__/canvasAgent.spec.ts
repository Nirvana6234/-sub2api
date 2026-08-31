import { describe, expect, it } from 'vitest'
import { agentNodeToCanvasNode, createCanvasAgentSnapshot, normalizeCanvasAgentEndpoint } from '../canvas/canvasAgent'
import type { CanvasProject } from '../canvas/types'

const project: CanvasProject = {
  id: 'project-1',
  title: 'Demo',
  nodes: [{ id: 'text-1', type: 'text', kind: 'generator', x: 10, y: 20, width: 340, height: 300, prompt: 'hello', textContent: 'hello', model: 'text-model', status: 'idle', createdAt: 1, updatedAt: 1 }],
  connections: [],
  viewport: { x: 4, y: 5, scale: 1.25 },
  backgroundMode: 'dots',
  createdAt: 1,
  updatedAt: 1,
}

describe('canvas agent bridge mapping', () => {
  it('normalizes endpoint slashes', () => {
    expect(normalizeCanvasAgentEndpoint(' http://127.0.0.1:17371/// ')).toBe('http://127.0.0.1:17371')
  })

  it('maps the local project to the upstream snapshot shape', () => {
    const snapshot = createCanvasAgentSnapshot(project, new Set(['text-1']))
    expect(snapshot).toMatchObject({
      projectId: 'project-1',
      selectedNodeIds: ['text-1'],
      viewport: { x: 4, y: 5, k: 1.25 },
      nodes: [{ id: 'text-1', title: 'hello', position: { x: 10, y: 20 }, metadata: { content: 'hello', model: 'text-model' } }],
    })
  })

  it('maps agent nodes into safe local generator nodes', () => {
    const node = agentNodeToCanvasNode({ id: 'audio-1', type: 'audio', title: 'voice', position: { x: 2, y: 3 }, metadata: { content: 'speak this', model: 'voice-model', status: 'success' } })
    expect(node).toMatchObject({ id: 'audio-1', type: 'audio', x: 2, y: 3, title: 'voice', prompt: 'speak this', model: 'voice-model', status: 'success' })
  })
})
