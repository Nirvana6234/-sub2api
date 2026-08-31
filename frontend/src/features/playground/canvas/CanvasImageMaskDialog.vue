<template>
  <div class="fixed inset-0 z-[125] flex items-center justify-center bg-gray-950/80 p-4" data-canvas-no-zoom @pointerdown.self="emit('close')">
    <section class="flex max-h-[calc(100%-2rem)] w-full max-w-5xl flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900" role="dialog" aria-modal="true" :aria-label="t('playground.canvasMaskImage')" data-image-mask-dialog>
      <header class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-dark-700"><div><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasMaskImage') }}</h2><p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('playground.canvasMaskDescription') }}</p></div><button type="button" class="btn btn-ghost btn-icon h-8 w-8 shrink-0 p-0" :title="t('common.close')" :disabled="busy" @click="emit('close')"><Icon name="x" size="sm" /><span class="sr-only">{{ t('common.close') }}</span></button></header>
      <div class="grid min-h-0 flex-1 gap-0 overflow-y-auto md:grid-cols-[minmax(0,1fr)_240px]">
        <div class="flex min-h-80 items-center justify-center bg-gray-950 p-6"><div ref="stage" class="relative max-h-[58vh] max-w-full overflow-hidden shadow-xl"><img ref="image" :src="imageUrl" :alt="t('playground.canvasMaskPreview')" class="block max-h-[58vh] max-w-full object-contain" draggable="false" @load="syncCanvas"><canvas ref="maskCanvas" class="absolute inset-0 h-full w-full cursor-crosshair" @pointerdown="startStroke" @pointermove="drawStroke" @pointerup="stopStroke" @pointerleave="stopStroke"></canvas></div></div>
        <aside class="space-y-5 border-t border-gray-200 p-5 dark:border-dark-700 md:border-l md:border-t-0"><label class="block text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('playground.canvasMaskBrush') }}<input v-model.number="brushSize" type="range" min="8" max="180" step="4" class="mt-3 w-full accent-teal-600"><span class="mt-1 block text-[11px] text-gray-500 dark:text-dark-400">{{ brushSize }} px</span></label><button type="button" class="btn btn-ghost w-full gap-1.5 border border-gray-200 dark:border-dark-600" :disabled="busy" @click="resetMask"><Icon name="refresh" size="xs" />{{ t('playground.canvasMaskReset') }}</button><div class="rounded-md border border-teal-200 bg-teal-50 p-3 text-xs leading-5 text-teal-800 dark:border-teal-900/60 dark:bg-teal-950/30 dark:text-teal-200">{{ t('playground.canvasMaskHint') }}</div></aside>
      </div>
      <footer class="flex justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700"><button type="button" class="btn btn-ghost" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary gap-1.5" data-image-mask-apply :disabled="busy || !ready" @click="apply"><Icon name="sparkles" size="sm" :class="{ 'animate-pulse': busy }" />{{ busy ? t('playground.canvasImageApplying') : t('playground.canvasMaskApply') }}</button></footer>
    </section>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'

defineProps<{ imageUrl: string; busy?: boolean }>()
const emit = defineEmits<{ close: []; apply: [dataUrl: string] }>()
const { t } = useI18n()
const image = ref<HTMLImageElement | null>(null)
const maskCanvas = ref<HTMLCanvasElement | null>(null)
const stage = ref<HTMLElement | null>(null)
const brushSize = ref(48)
const ready = ref(false)
const drawing = ref(false)

function syncCanvas(): void {
  const img = image.value
  const canvas = maskCanvas.value
  if (!img || !canvas || !img.naturalWidth || !img.naturalHeight) return
  canvas.width = img.naturalWidth
  canvas.height = img.naturalHeight
  const context = canvas.getContext('2d')
  if (!context) return
  context.fillStyle = 'rgba(255,255,255,0.72)'
  context.fillRect(0, 0, canvas.width, canvas.height)
  ready.value = true
}
function point(event: PointerEvent): { x: number; y: number } | null {
  const canvas = maskCanvas.value
  if (!canvas) return null
  const rect = canvas.getBoundingClientRect()
  return { x: (event.clientX - rect.left) * canvas.width / rect.width, y: (event.clientY - rect.top) * canvas.height / rect.height }
}
function startStroke(event: PointerEvent): void { drawing.value = true; maskCanvas.value?.setPointerCapture(event.pointerId); drawStroke(event) }
function drawStroke(event: PointerEvent): void {
  if (!drawing.value) return
  const canvas = maskCanvas.value
  const position = point(event)
  const context = canvas?.getContext('2d')
  if (!canvas || !position || !context) return
  context.save()
  context.globalCompositeOperation = 'destination-out'
  context.beginPath()
  context.arc(position.x, position.y, brushSize.value / 2, 0, Math.PI * 2)
  context.fill()
  context.restore()
}
function stopStroke(): void { drawing.value = false }
function resetMask(): void { syncCanvas() }
function apply(): void { if (ready.value && maskCanvas.value) emit('apply', maskCanvas.value.toDataURL('image/png')) }
onMounted(() => { void nextTick(syncCanvas) })
</script>
