<template>
  <div
    ref="rootRef"
    class="pointer-events-auto absolute bottom-4 right-4 z-40 hidden h-36 w-56 overflow-hidden rounded-xl border border-gray-200/80 bg-white/85 p-2 shadow-lg backdrop-blur dark:border-dark-600 dark:bg-dark-900/85 md:block"
    data-canvas-no-zoom
    data-canvas-minimap
    :aria-label="t('playground.canvasMiniMap')"
    @pointerdown.stop
  >
    <div
      ref="mapRef"
      class="relative h-full w-full cursor-crosshair"
      @pointerdown.stop.prevent="startMapDrag"
      @pointermove.stop="moveMapDrag"
      @pointerup.stop="endMapDrag"
      @pointercancel.stop="endMapDrag"
      @pointerleave.stop="endMapDrag"
    >
      <span
        v-for="node in nodes"
        :key="node.id"
        class="absolute rounded-[2px] transition-colors"
        :class="node.type === 'group' ? 'bg-teal-300/35 ring-1 ring-inset ring-teal-500/35' : 'bg-teal-500/75'"
        :style="nodeStyle(node)"
      ></span>
      <span class="pointer-events-none absolute rounded border border-teal-600/80 bg-teal-500/10" :style="viewportStyle"></span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CanvasNode, CanvasViewport } from './types'

const props = defineProps<{
  nodes: CanvasNode[]
  viewport: CanvasViewport
}>()

const emit = defineEmits<{
  'update-viewport': [viewport: CanvasViewport]
}>()

const { t } = useI18n()
const rootRef = ref<HTMLElement | null>(null)
const mapRef = ref<HTMLElement | null>(null)
const canvasSize = ref({ width: 960, height: 650 })
const dragging = ref(false)
let resizeObserver: ResizeObserver | null = null

const MAP_WIDTH = 208
const MAP_HEIGHT = 112
const mapDimensions = computed(() => ({
  width: Math.max(1, mapRef.value?.clientWidth ?? MAP_WIDTH),
  height: Math.max(1, mapRef.value?.clientHeight ?? MAP_HEIGHT),
}))
const bounds = computed(() => {
  if (!props.nodes.length) return { minX: -500, minY: -500, width: 1000, height: 1000 }
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  for (const node of props.nodes) {
    minX = Math.min(minX, node.x)
    minY = Math.min(minY, node.y)
    maxX = Math.max(maxX, node.x + node.width)
    maxY = Math.max(maxY, node.y + node.height)
  }
  minX -= 500
  minY -= 500
  maxX += 500
  maxY += 500
  return { minX, minY, width: Math.max(1, maxX - minX), height: Math.max(1, maxY - minY) }
})

const mapTransform = computed(() => {
  const scale = Math.min(mapDimensions.value.width / bounds.value.width, mapDimensions.value.height / bounds.value.height)
  const contentWidth = bounds.value.width * scale
  const contentHeight = bounds.value.height * scale
  return {
    scale,
    offsetX: (mapDimensions.value.width - contentWidth) / 2,
    offsetY: (mapDimensions.value.height - contentHeight) / 2,
  }
})

function toMap(worldX: number, worldY: number): { x: number; y: number } {
  return {
    x: (worldX - bounds.value.minX) * mapTransform.value.scale + mapTransform.value.offsetX,
    y: (worldY - bounds.value.minY) * mapTransform.value.scale + mapTransform.value.offsetY,
  }
}

function toWorld(mapX: number, mapY: number): { x: number; y: number } {
  return {
    x: (mapX - mapTransform.value.offsetX) / mapTransform.value.scale + bounds.value.minX,
    y: (mapY - mapTransform.value.offsetY) / mapTransform.value.scale + bounds.value.minY,
  }
}

function nodeStyle(node: CanvasNode): Record<string, string> {
  const position = toMap(node.x, node.y)
  return {
    left: `${position.x}px`,
    top: `${position.y}px`,
    width: `${Math.max(2, node.width * mapTransform.value.scale)}px`,
    height: `${Math.max(2, node.height * mapTransform.value.scale)}px`,
  }
}

const viewportStyle = computed(() => {
  const leftTop = toMap(-props.viewport.x / props.viewport.scale, -props.viewport.y / props.viewport.scale)
  return {
    left: `${leftTop.x}px`,
    top: `${leftTop.y}px`,
    width: `${Math.max(5, canvasSize.value.width / props.viewport.scale * mapTransform.value.scale)}px`,
    height: `${Math.max(5, canvasSize.value.height / props.viewport.scale * mapTransform.value.scale)}px`,
  }
})

function mapPoint(event: PointerEvent): { x: number; y: number } | null {
  const rect = mapRef.value?.getBoundingClientRect()
  if (!rect) return null
  return { x: event.clientX - rect.left, y: event.clientY - rect.top }
}

function updateViewportFromMap(event: PointerEvent): void {
  const point = mapPoint(event)
  if (!point) return
  const world = toWorld(point.x, point.y)
  emit('update-viewport', {
    x: canvasSize.value.width / 2 - world.x * props.viewport.scale,
    y: canvasSize.value.height / 2 - world.y * props.viewport.scale,
    scale: props.viewport.scale,
  })
}

function startMapDrag(event: PointerEvent): void {
  dragging.value = true
  mapRef.value?.setPointerCapture(event.pointerId)
  updateViewportFromMap(event)
}

function moveMapDrag(event: PointerEvent): void {
  if (dragging.value) updateViewportFromMap(event)
}

function endMapDrag(event?: PointerEvent): void {
  dragging.value = false
  if (event && mapRef.value?.hasPointerCapture(event.pointerId)) mapRef.value.releasePointerCapture(event.pointerId)
}

function updateCanvasSize(): void {
  const surface = rootRef.value?.parentElement
  if (!surface) return
  canvasSize.value = { width: surface.clientWidth, height: surface.clientHeight }
}

onMounted(() => {
  updateCanvasSize()
  const surface = rootRef.value?.parentElement
  if (surface && typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(updateCanvasSize)
    resizeObserver.observe(surface)
  }
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
})
</script>
