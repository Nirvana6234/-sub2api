<template>
  <svg class="absolute inset-0 h-full w-full overflow-visible">
    <path
      v-for="connection in connections"
      :key="connection.id"
      :d="pathFor(connection)"
      fill="none"
      stroke="currentColor"
      :stroke-width="connection.id === selectedConnectionId ? 3 : activeConnectionIds?.has(connection.id) ? 2.75 : 2"
      stroke-linecap="round"
      :class="connection.id === selectedConnectionId ? 'pointer-events-none text-teal-700 dark:text-teal-100' : activeConnectionIds?.has(connection.id) ? 'pointer-events-none text-teal-700/90 dark:text-teal-100/90' : 'pointer-events-none text-teal-600/70 dark:text-teal-300/70'"
    />
    <path
      v-for="connection in connections"
      :key="`${connection.id}-hit`"
      :d="pathFor(connection)"
      fill="none"
      stroke="transparent"
      stroke-width="14"
      stroke-linecap="round"
      class="pointer-events-auto cursor-pointer"
      :data-canvas-connection-hit="connection.id"
      @pointerdown.stop="emit('select', connection.id)"
      @click.stop="emit('select', connection.id)"
      @dblclick.stop="emit('delete', connection.id)"
      @contextmenu.stop.prevent="emit('context-menu', connection.id, $event)"
    />
    <path
      v-if="draftPath"
      :d="draftPath"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-dasharray="6 4"
      class="pointer-events-none text-teal-500/80 dark:text-teal-200/80"
    />
  </svg>
</template>

<script setup lang="ts">
import type { CanvasConnection, CanvasNode } from './types'

const props = defineProps<{
  connections: CanvasConnection[]
  nodes: CanvasNode[]
  draftPath?: string
  selectedConnectionId?: string | null
  activeConnectionIds?: Set<string>
}>()

const emit = defineEmits<{
  select: [id: string]
  delete: [id: string]
  'context-menu': [id: string, event: MouseEvent]
}>()

function pathFor(connection: CanvasConnection): string {
  const from = props.nodes.find((node) => node.id === connection.from)
  const to = props.nodes.find((node) => node.id === connection.to)
  if (!from || !to) return ''
  const startX = from.x + from.width
  const startY = from.y + from.height / 2
  const endX = to.x
  const endY = to.y + to.height / 2
  const bend = Math.max(80, Math.abs(endX - startX) * 0.45)
  return `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`
}
</script>
