<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useElementSize } from '@vueuse/core'
import type { DashboardColorToken } from '../../types/dashboard'
import { buildChartGeometry } from '../../utils/chart'
import { useTrendDraw } from '../../composables/useTrendDraw'

const props = withDefaults(
  defineProps<{
    values: number[]
    labels: string[]
    color?: DashboardColorToken
    height?: number
    formatValue?: (value: number) => string
  }>(),
  {
    color: 'primary',
    height: 200,
  },
)

const PADDING = 14

const gradientId = `trend-gradient-${useId()}`
const containerEl = ref<HTMLDivElement | null>(null)
const pathEl = ref<SVGPathElement | null>(null)
const areaEl = ref<SVGPathElement | null>(null)

// 用容器真实像素宽度作为 viewBox 宽度，scale=1，描边均匀、坐标映射精确。
const { width: boxWidth } = useElementSize(containerEl)
const width = computed(() => Math.max(1, Math.round(boxWidth.value)))

const geometry = computed(() => buildChartGeometry(props.values, width.value, props.height, PADDING))

const strokeColor = computed(() => `hsl(var(--${props.color}))`)

useTrendDraw(pathEl, areaEl, () => geometry.value.linePath)

const hoveredIndex = ref<number | null>(null)

const onMove = (event: MouseEvent) => {
  const el = containerEl.value
  const count = props.values.length
  if (!el || count === 0) return
  const rect = el.getBoundingClientRect()
  const ratio = (event.clientX - rect.left) / rect.width
  hoveredIndex.value = Math.max(0, Math.min(count - 1, Math.round(ratio * (count - 1))))
}

const onLeave = () => {
  hoveredIndex.value = null
}

const hovered = computed(() => {
  if (hoveredIndex.value == null) return null
  const point = geometry.value.points[hoveredIndex.value]
  if (!point) return null
  const value = props.values[hoveredIndex.value]
  return {
    x: point.x,
    y: point.y,
    label: props.labels[hoveredIndex.value] ?? '',
    display: props.formatValue ? props.formatValue(value) : String(value),
    tooltipLeft: Math.min(width.value - 56, Math.max(56, point.x)),
  }
})
</script>

<template>
  <div
    ref="containerEl"
    class="relative w-full overflow-visible"
    :style="{ height: `${height}px` }"
    @mousemove="onMove"
    @mouseleave="onLeave"
  >
    <svg
      :viewBox="`0 0 ${width} ${height}`"
      class="absolute inset-0 h-full w-full"
      role="img"
    >
      <defs>
        <linearGradient :id="gradientId" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" :stop-color="strokeColor" stop-opacity="0.28" />
          <stop offset="100%" :stop-color="strokeColor" stop-opacity="0" />
        </linearGradient>
      </defs>

      <path ref="areaEl" :d="geometry.areaPath" :fill="`url(#${gradientId})`" stroke="none" />
      <path
        ref="pathEl"
        :d="geometry.linePath"
        fill="none"
        :stroke="strokeColor"
        stroke-width="2.5"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>

    <!-- hover 竖向参考线 -->
    <div
      v-if="hovered"
      class="pointer-events-none absolute top-0 bottom-0 w-px bg-border"
      :style="{ left: `${hovered.x}px` }"
    />
    <!-- hover 数据点 -->
    <div
      v-if="hovered"
      class="pointer-events-none absolute h-2.5 w-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-card"
      :class="`bg-${color}`"
      :style="{ left: `${hovered.x}px`, top: `${hovered.y}px` }"
    />
    <!-- tooltip -->
    <div
      v-if="hovered"
      class="pointer-events-none absolute z-10 -translate-x-1/2 -translate-y-full whitespace-nowrap rounded-lg border border-border/60 bg-card px-2.5 py-1.5 text-xs shadow-lg"
      :style="{ left: `${hovered.tooltipLeft}px`, top: `${hovered.y}px`, marginTop: '-10px' }"
    >
      <div class="font-semibold text-foreground">{{ hovered.display }}</div>
      <div class="text-muted-foreground">{{ hovered.label }}</div>
    </div>
  </div>
</template>
