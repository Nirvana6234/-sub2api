<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ChartNoAxesCombined, Loader2, X } from 'lucide-vue-next'
import { getUpstreamKeyMultiplierHistory } from '../../api/connectionHealth'
import type { UpstreamKeyMultiplierHistoryPoint } from '../../types/connectionHealth'

const props = defineProps<{ open: boolean; targetId: string; accountName: string }>()
const emit = defineEmits<{ (event: 'close'): void }>()
const range = ref<'hour' | 'day' | 'week'>('hour')
const loading = ref(false)
const error = ref('')
const points = ref<UpstreamKeyMultiplierHistoryPoint[]>([])

const load = async () => {
  if (!props.open || !props.targetId) return
  loading.value = true
  error.value = ''
  try { points.value = await getUpstreamKeyMultiplierHistory(props.targetId, range.value) }
  catch { error.value = '历史倍率暂时读取失败。' }
  finally { loading.value = false }
}
watch(() => props.open, (open) => { if (open) void load() })
watch(range, () => void load())

const formatAxisTime = (value: string) => {
  const date = new Date(value)
  const month = date.getMonth() + 1
  const day = date.getDate()
  if (range.value === 'hour') {
    const hour = String(date.getHours()).padStart(2, '0')
    return `${month}/${day} ${hour}:00`
  }
  return `${month}/${day}`
}

const chart = computed(() => {
  if (points.value.length === 0) return ''
  const values = points.value.map(point => point.multiplier)
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min
  const denominator = Math.max(1, points.value.length - 1)
  return points.value.map((point, index) => {
    const x = points.value.length === 1 ? 50 : 6 + (index / denominator) * 88
    const y = span === 0 ? 50 : 92 - ((point.multiplier - min) / span) * 84
    return `${x},${y}`
  }).join(' ')
})

const axisTicks = computed(() => {
  if (points.value.length === 0) return []
  const count = Math.min(5, points.value.length)
  const denominator = Math.max(1, count - 1)
  const pointDenominator = Math.max(1, points.value.length - 1)
  return Array.from({ length: count }, (_, index) => {
    const pointIndex = Math.round(index * pointDenominator / denominator)
    return {
      key: points.value[pointIndex].observedAt,
      label: formatAxisTime(points.value[pointIndex].observedAt),
      position: points.value.length === 1 ? 50 : 6 + (pointIndex / pointDenominator) * 88,
    }
  })
})

const formatTime = (value: string) => new Intl.DateTimeFormat('zh-CN', range.value === 'hour'
  ? { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }
  : { year: 'numeric', month: 'numeric', day: 'numeric' }).format(new Date(value))
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="fixed inset-0 z-[155] flex items-center justify-center p-4">
      <button type="button" class="absolute inset-0 cursor-default bg-background/60 backdrop-blur-sm" aria-label="关闭" @click="emit('close')" />
      <section role="dialog" aria-modal="true" class="relative w-full max-w-2xl overflow-hidden rounded-lg border border-border/60 bg-card shadow-2xl">
        <header class="flex items-center justify-between gap-3 border-b border-border/60 px-5 py-4">
          <div class="min-w-0"><h3 class="text-sm font-semibold text-foreground">上游 API Key 倍率历史</h3><p class="mt-0.5 truncate text-xs text-muted-foreground">{{ accountName }}</p></div>
          <button type="button" class="rounded-md p-1 text-muted-foreground hover:bg-surface hover:text-foreground" aria-label="关闭" @click="emit('close')"><X class="h-4 w-4" /></button>
        </header>
        <div class="px-5 py-4">
          <div class="flex items-center justify-between gap-3"><div class="inline-flex overflow-hidden rounded-md border border-border/60"><button v-for="option in [['hour','小时'],['day','天'],['week','周']] as const" :key="option[0]" type="button" class="px-3 py-1.5 text-xs" :class="range === option[0] ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-surface'" @click="range = option[0]">{{ option[1] }}</button></div><span class="text-xs text-muted-foreground">天和周均按小时采样平均</span></div>
          <div v-if="loading" class="flex h-64 items-center justify-center text-sm text-muted-foreground"><Loader2 class="mr-2 h-4 w-4 animate-spin" />读取中</div>
          <div v-else-if="error" class="flex h-64 items-center justify-center text-sm text-destructive">{{ error }}</div>
          <div v-else-if="points.length === 0" class="flex h-64 flex-col items-center justify-center text-sm text-muted-foreground"><ChartNoAxesCombined class="mb-3 h-7 w-7 opacity-40" />暂无历史采样，系统会每小时自动记录。</div>
          <div v-else class="mt-5">
            <div class="relative h-56">
              <svg viewBox="0 0 100 100" preserveAspectRatio="none" class="h-44 w-full overflow-visible" aria-label="Multiplier over time">
                <line x1="6" x2="94" y1="92" y2="92" stroke="currentColor" stroke-width="0.5" class="text-border" vector-effect="non-scaling-stroke" />
                <polyline :points="chart" fill="none" stroke="currentColor" stroke-width="1.5" vector-effect="non-scaling-stroke" class="text-primary" />
              </svg>
              <div class="pointer-events-none absolute inset-x-0 bottom-0 h-8">
                <span
                  v-for="tick in axisTicks"
                  :key="tick.key"
                  class="absolute whitespace-nowrap text-[10px] tabular-nums text-muted-foreground"
                  :style="{ left: `${tick.position}%`, transform: 'translateX(-50%)' }"
                >{{ tick.label }}</span>
              </div>
            </div>
            <div class="mt-3 max-h-40 overflow-y-auto rounded-md border border-border/50"><div v-for="point in points.slice().reverse()" :key="point.observedAt" class="flex items-center justify-between border-b border-border/40 px-3 py-2 text-xs last:border-b-0"><span class="text-muted-foreground">{{ formatTime(point.observedAt) }}</span><span class="font-medium tabular-nums text-foreground">{{ point.multiplier }}x <span v-if="point.source === 'manual'" class="ml-1 text-amber-600">手工</span></span></div></div>
          </div>
        </div>
      </section>
    </div>
  </Teleport>
</template>
