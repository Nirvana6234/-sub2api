import type { CanvasConnection, CanvasNode } from './types'

export interface CanvasNodePosition {
  x: number
  y: number
}

export interface CanvasLayoutOptions {
  columnGap?: number
  rowGap?: number
  padding?: number
}

/** Arrange nodes into left-to-right layers while keeping each layer readable. */
export function arrangeCanvasNodes(
  nodes: CanvasNode[],
  connections: CanvasConnection[],
  options: CanvasLayoutOptions = {},
): Record<string, CanvasNodePosition> {
  if (!nodes.length) return {}
  const columnGap = options.columnGap ?? 180
  const rowGap = options.rowGap ?? 48
  const padding = options.padding ?? 48
  const nodeIds = new Set(nodes.map((node) => node.id))
  const incoming = new Map<string, string[]>()
  for (const node of nodes) {
    incoming.set(node.id, [])
  }
  for (const connection of connections) {
    if (!nodeIds.has(connection.from) || !nodeIds.has(connection.to) || connection.from === connection.to) continue
    incoming.get(connection.to)?.push(connection.from)
  }

  // Longest-path ranks make reference/result lines flow in one direction. A
  // bounded relaxation also gives cycles a stable layout instead of looping.
  const ranks = new Map<string, number>(nodes.map((node) => [node.id, 0]))
  for (let pass = 0; pass < nodes.length; pass += 1) {
    let changed = false
    for (const node of nodes) {
      const rank = Math.max(0, ...(incoming.get(node.id) ?? []).map((parent) => (ranks.get(parent) ?? 0) + 1))
      if (rank > (ranks.get(node.id) ?? 0)) {
        ranks.set(node.id, rank)
        changed = true
      }
    }
    if (!changed) break
  }

  const columns = new Map<number, CanvasNode[]>()
  for (const node of nodes) {
    const rank = ranks.get(node.id) ?? 0
    const column = columns.get(rank) ?? []
    column.push(node)
    columns.set(rank, column)
  }
  const orderedRanks = Array.from(columns.keys()).sort((a, b) => a - b)
  const widths = new Map<number, number>()
  for (const rank of orderedRanks) widths.set(rank, Math.max(...(columns.get(rank) ?? []).map((node) => node.width)))

  const positions: Record<string, CanvasNodePosition> = {}
  let x = padding
  for (const rank of orderedRanks) {
    const column = columns.get(rank) ?? []
    column.sort((a, b) => a.y - b.y || a.x - b.x || a.id.localeCompare(b.id))
    let y = padding
    for (const node of column) {
      positions[node.id] = { x, y }
      y += node.height + rowGap
    }
    x += (widths.get(rank) ?? 0) + columnGap
  }
  return positions
}
