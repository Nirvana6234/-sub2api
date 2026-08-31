import { describe, expect, it } from 'vitest'
import { buildCanvasMentionReferences, buildCanvasReferenceItems, expandCanvasGroupResourceNodes, getCanvasNodePreviewUrl, getCanvasNodeText, isCanvasResourceNode } from '../canvas/canvasReferences'
import type { CanvasNode, CanvasProject } from '../canvas/types'

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

function project(nodes: CanvasNode[], connections: CanvasProject['connections'] = []): CanvasProject {
  return {
    id: 'project-1',
    title: 'Canvas',
    nodes,
    connections,
    viewport: { x: 0, y: 0, scale: 1 },
    backgroundMode: 'dots',
    createdAt: 1,
    updatedAt: 1,
  }
}

describe('canvas reference helpers', () => {
  it('expands a group connection into the concrete grouped resources', () => {
    const canvas = project([
      node('group-1', { type: 'group', prompt: 'Storyboard' }),
      node('image-1', { groupId: 'group-1', imageUrl: 'data:image/png;base64,a', prompt: 'Hero frame' }),
      node('text-1', { type: 'text', groupId: 'group-1', textContent: 'Use dusk lighting', prompt: 'Lighting note' }),
      node('outside-1', { imageUrl: 'data:image/png;base64,b', prompt: 'Outside' }),
      node('target-1', { prompt: 'Target' }),
    ], [{ id: 'edge-1', from: 'group-1', to: 'target-1', kind: 'reference' }])

    expect(expandCanvasGroupResourceNodes(canvas.nodes[0], canvas).map((item) => item.id)).toEqual(['image-1', 'text-1'])

    const references = buildCanvasReferenceItems(canvas, 'target-1')
    expect(references).toEqual([
      {
        sourceNodeId: 'group-1',
        nodeId: 'image-1',
        kind: 'image',
        title: 'Storyboard',
        label: 'Hero frame',
        previewUrl: 'data:image/png;base64,a',
        text: 'Hero frame',
      },
      {
        sourceNodeId: 'group-1',
        nodeId: 'text-1',
        kind: 'text',
        title: 'Storyboard',
        label: 'Lighting note',
        text: 'Use dusk lighting',
      },
    ])
  })

  it('turns plugin SVG content into an image preview while keeping text content as text', () => {
    const svgNode = node('svg-1', {
      type: 'diagram:svg',
      pluginRenderer: 'svg',
      pluginMetadata: { content: '<svg><rect width="10" height="10" /></svg>' },
    })
    const textPlugin = node('plugin-text', {
      type: 'notes:plain',
      pluginRenderer: 'html',
      pluginMetadata: { content: 'A reusable mood-board note.' },
    })

    expect(getCanvasNodePreviewUrl(svgNode)).toMatch(/^data:image\/svg\+xml;utf8,/)
    expect(getCanvasNodeText(textPlugin)).toBe('A reusable mood-board note.')
  })

  it('uses config inputs as mention resources for the downstream generator', () => {
    const text = node('text-1', { type: 'text', textContent: 'A rainy street', prompt: 'A rainy street' })
    const config = node('config-1', { type: 'config', prompt: '@[node:text-1]' })
    const target = node('target-1', { prompt: 'cinematic' })
    const canvas = project([text, config, target], [
      { id: 'edge-text-config', from: 'text-1', to: 'config-1', kind: 'reference' },
      { id: 'edge-config-target', from: 'config-1', to: 'target-1', kind: 'reference' },
    ])

    expect(buildCanvasMentionReferences(canvas, 'target-1').map((item) => item.nodeId)).toEqual(['text-1'])
    expect(buildCanvasMentionReferences(canvas, 'target-1')[0]?.label).toContain('A rainy street')
    expect(isCanvasResourceNode(config, canvas)).toBe(false)
  })

  it('prefers an explicit node title when building reference labels', () => {
    const source = node('image-1', {
      title: 'Hero reference',
      imageUrl: 'data:image/png;base64,a',
      prompt: 'A cinematic portrait',
    })
    const target = node('target-1', { prompt: 'Generate a variation' })
    const canvas = project([source, target], [{ id: 'edge-1', from: source.id, to: target.id, kind: 'reference' }])

    expect(buildCanvasReferenceItems(canvas, target.id)[0]?.label).toBe('Hero reference')
    expect(buildCanvasMentionReferences(canvas, target.id)[0]?.title).toBe('Hero reference')
  })
})
