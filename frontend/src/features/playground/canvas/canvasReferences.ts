import type { CanvasNode, CanvasProject } from './types'

export type CanvasReferenceKind = 'image' | 'video' | 'audio' | 'text'

export interface CanvasMentionReference {
  id: string
  nodeId: string
  kind: CanvasReferenceKind | 'group'
  title: string
  label: string
  previewUrl?: string
  text?: string
}

export interface CanvasReferenceItem {
  sourceNodeId: string
  nodeId: string
  kind: CanvasReferenceKind
  title: string
  label: string
  previewUrl?: string
  text?: string
}

function isUrlLike(value: string): boolean {
  return /^data:|^https?:\/\//i.test(value)
}

export function getCanvasNodeLabel(node: CanvasNode): string {
  return String(node.title || node.pluginTitle || node.prompt || node.textContent || node.model || node.type).trim() || node.type
}

export function getCanvasNodeText(node: CanvasNode): string {
  if (node.type === 'text') return String(node.textContent || node.prompt || '').trim()
  const content = typeof node.pluginMetadata?.content === 'string' ? node.pluginMetadata.content.trim() : ''
  if (content && !isUrlLike(content)) return content
  return String(node.prompt || '').trim()
}

export function getCanvasNodePreviewUrl(node: CanvasNode): string | undefined {
  if (node.type === 'image') return node.imageUrls?.[0] || node.imageUrl
  if (node.type === 'video') return node.videoUrl
  if (node.type === 'audio') return node.audioUrl
  const content = typeof node.pluginMetadata?.content === 'string' ? node.pluginMetadata.content.trim() : ''
  if (node.pluginRenderer === 'panorama' && content) return content
  if (node.pluginRenderer === 'svg' && content) return `data:image/svg+xml;utf8,${encodeURIComponent(content)}`
  if (node.pluginRenderer === 'html' && content) return undefined
  if (content && isUrlLike(content)) return content
  return node.imageUrl || node.imageUrls?.[0] || node.videoUrl || node.audioUrl
}

export function isCanvasResourceNode(node: CanvasNode | null | undefined, project?: CanvasProject): node is CanvasNode {
  if (!node) return false
  if (node.type === 'group') return Boolean(project?.nodes.some((candidate) => candidate.groupId === node.id && isCanvasResourceNode(candidate, project)))
  if (node.type === 'image') return Boolean(node.imageUrl || node.imageUrls?.length)
  if (node.type === 'video') return Boolean(node.videoUrl)
  if (node.type === 'audio') return Boolean(node.audioUrl)
  if (node.type === 'text') return Boolean(getCanvasNodeText(node))
  if (node.pluginRenderer) return Boolean(getCanvasNodePreviewUrl(node) || getCanvasNodeText(node))
  return false
}

export function expandCanvasGroupResourceNodes(source: CanvasNode, project: CanvasProject): CanvasNode[] {
  if (source.type === 'group') {
    return project.nodes.filter((node) => node.groupId === source.id && isCanvasResourceNode(node))
  }
  return isCanvasResourceNode(source) ? [source] : []
}

export function buildCanvasReferenceItems(project: CanvasProject, targetNodeId: string): CanvasReferenceItem[] {
  const items: CanvasReferenceItem[] = []
  const seen = new Set<string>()
  for (const connection of project.connections.filter((connection) => connection.to === targetNodeId)) {
    const source = project.nodes.find((node) => node.id === connection.from)
    if (!source) continue
    const sourceNodes = expandCanvasGroupResourceNodes(source, project)
    for (const node of sourceNodes) {
      const key = `${connection.from}:${node.id}`
      if (seen.has(key)) continue
      seen.add(key)
      const previewUrl = getCanvasNodePreviewUrl(node)
      items.push({
        sourceNodeId: source.id,
        nodeId: node.id,
        kind: node.type === 'video' ? 'video' : node.type === 'audio' ? 'audio' : node.type === 'text' ? 'text' : 'image',
        title: getCanvasNodeLabel(source),
        label: getCanvasNodeLabel(node),
        previewUrl: previewUrl && previewUrl.trim() ? previewUrl : undefined,
        text: getCanvasNodeText(node) || undefined,
      })
    }
  }
  return items
}

/**
 * Build the resources that may be inserted with @ in a node editor.
 *
 * A generator connected to a config node should see the config node's inputs
 * rather than the config node itself. This mirrors the reference app's
 * "config inputs first, direct inputs second" behavior while retaining a
 * group as a single expandable token.
 */
export function buildCanvasMentionReferences(project: CanvasProject, targetNodeId: string): CanvasMentionReference[] {
  const directSources = project.connections
    .filter((connection) => connection.to === targetNodeId)
    .map((connection) => project.nodes.find((node) => node.id === connection.from))
    .filter((node): node is CanvasNode => Boolean(node))

  const configSources = directSources.filter((node) => node.type === 'config')
  const configInputs = configSources.flatMap((config) => project.connections
    .filter((connection) => connection.to === config.id)
    .map((connection) => project.nodes.find((node) => node.id === connection.from))
    .filter((node): node is CanvasNode => Boolean(node)))
  const sources = configInputs.length ? configInputs : directSources
  const seen = new Set<string>()
  const references: CanvasMentionReference[] = []
  for (const source of sources) {
    if (source.id === targetNodeId) continue
    const candidates = source.type === 'group'
      ? [source]
      : isCanvasResourceNode(source, project) ? [source] : []
    for (const candidate of candidates) {
      if (seen.has(candidate.id) || !isCanvasResourceNode(candidate, project)) continue
      seen.add(candidate.id)
      const kind = candidate.type === 'group'
        ? 'group'
        : candidate.type === 'video'
          ? 'video'
          : candidate.type === 'audio'
            ? 'audio'
            : candidate.type === 'text'
              ? 'text'
              : 'image'
      references.push({
        id: candidate.id,
        nodeId: candidate.id,
        kind,
        title: getCanvasNodeLabel(candidate),
        label: `@ ${getCanvasNodeLabel(candidate)}`,
        previewUrl: getCanvasNodePreviewUrl(candidate),
        text: getCanvasNodeText(candidate) || undefined,
      })
    }
  }
  return references
}
