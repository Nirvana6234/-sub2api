<template>
  <div class="fixed inset-0 z-[125] flex items-center justify-center bg-gray-950/80 p-4" data-canvas-no-zoom @pointerdown.self="emit('close')">
    <section class="flex max-h-[calc(100%-2rem)] w-full max-w-5xl flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900" role="dialog" aria-modal="true" :aria-label="t('playground.canvasMaskImage')" data-image-mask-dialog>
      <header class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-dark-700"><div><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasMaskImage') }}</h2><p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('playground.canvasMaskDescription') }}</p></div><button type="button" class="btn btn-ghost btn-icon h-8 w-8 shrink-0 p-0" :title="t('common.close')" :disabled="busy" @click="emit('close')"><Icon name="x" size="sm" /><span class="sr-only">{{ t('common.close') }}</span></button></header>
      <div class="grid min-h-0 flex-1 gap-0 overflow-y-auto md:grid-cols-[minmax(0,1fr)_280px]">
        <div class="flex min-h-80 items-center justify-center bg-gray-950 p-6"><div ref="stage" class="relative max-h-[58vh] max-w-full overflow-hidden shadow-xl"><img ref="image" :src="imageUrl" :alt="t('playground.canvasMaskPreview')" class="block max-h-[58vh] max-w-full object-contain" draggable="false" @load="syncCanvas"><canvas ref="maskCanvas" class="absolute inset-0 h-full w-full cursor-crosshair opacity-70" @pointerdown="startStroke" @pointermove="drawStroke" @pointerup="stopStroke" @pointercancel="stopStroke" @pointerleave="stopStroke"></canvas></div></div>
        <aside class="space-y-4 border-t border-gray-200 p-5 dark:border-dark-700 md:border-l md:border-t-0">
          <div class="grid grid-cols-2 gap-2">
            <button type="button" class="btn h-9 gap-1.5 px-2 text-xs" :class="mode === 'erase' ? 'btn-primary' : 'btn-ghost border border-gray-200 dark:border-dark-600'" :disabled="busy" data-image-mask-erase @click="mode = 'erase'"><Icon name="edit" size="xs" />{{ t('playground.canvasMaskErase') }}</button>
            <button type="button" class="btn h-9 gap-1.5 px-2 text-xs" :class="mode === 'paint' ? 'btn-primary' : 'btn-ghost border border-gray-200 dark:border-dark-600'" :disabled="busy" data-image-mask-paint @click="mode = 'paint'"><Icon name="refresh" size="xs" />{{ t('playground.canvasMaskPaint') }}</button>
          </div>
          <div class="flex items-center justify-between rounded-md border border-gray-200 px-2 py-1 dark:border-dark-600">
            <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasMaskUndo')" :disabled="busy || !history.length" data-image-mask-undo @click="undoMask"><Icon name="undo" size="xs" /><span class="sr-only">{{ t('playground.canvasMaskUndo') }}</span></button>
            <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasMaskRedo')" :disabled="busy || !redo.length" data-image-mask-redo @click="redoMask"><Icon name="redo" size="xs" /><span class="sr-only">{{ t('playground.canvasMaskRedo') }}</span></button>
            <button type="button" class="btn btn-ghost h-8 gap-1 px-2 text-xs" :disabled="busy" data-image-mask-reset @click="resetMask"><Icon name="refresh" size="xs" />{{ t('playground.canvasMaskReset') }}</button>
          </div>
          <label class="block text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('playground.canvasMaskBrush') }}<input v-model.number="brushSize" type="range" min="8" max="180" step="4" class="mt-3 w-full accent-teal-600"><span class="mt-1 block text-[11px] text-gray-500 dark:text-dark-400">{{ brushSize }} px</span></label>
          <label class="block text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('playground.canvasMaskPromptLabel') }}<textarea v-model="prompt" rows="5" class="textarea mt-2 min-h-28 w-full resize-y text-xs leading-5" :placeholder="t('playground.canvasMaskPromptPlaceholder')" :disabled="busy" data-image-mask-prompt></textarea></label>
          <div class="rounded-md border border-teal-200 bg-teal-50 p-3 text-xs leading-5 text-teal-800 dark:border-teal-900/60 dark:bg-teal-950/30 dark:text-teal-200">{{ t('playground.canvasMaskHint') }}</div>
          <p v-if="error" class="text-xs font-medium text-red-600 dark:text-red-400" data-image-mask-error>{{ error }}</p>
        </aside>
      </div>
      <footer class="flex justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700"><button type="button" class="btn btn-ghost" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary gap-1.5" data-image-mask-apply :disabled="busy || !ready || !prompt.trim() || !hasPaint" @click="apply"><Icon name="sparkles" size="sm" :class="{ 'animate-pulse': busy }" />{{ busy ? t('playground.canvasImageApplying') : t('playground.canvasMaskApply') }}</button></footer>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'

const props = defineProps<{ imageUrl: string; busy?: boolean }>()
const emit = defineEmits<{ close: []; apply: [dataUrl: string, prompt: string] }>()
const { t } = useI18n()
const image = ref<HTMLImageElement | null>(null)
const maskCanvas = ref<HTMLCanvasElement | null>(null)
const stage = ref<HTMLElement | null>(null)
const brushSize = ref(48)
const ready = ref(false)
const drawing = ref(false)
const mode = ref<'erase' | 'paint'>('erase')
type MaskStroke = { mode: 'erase' | 'paint'; size: number; points: Array<{ x: number; y: number }> }
const history = ref<MaskStroke[]>([])
const redo = ref<MaskStroke[]>([])
const activeStroke = ref<MaskStroke | null>(null)
const prompt = ref('')
const error = ref('')
const maskRevision = ref(0)
const hasPaint = computed(() => {
  void maskRevision.value
  const canvas = maskCanvas.value
  const context = canvas?.getContext('2d', { willReadFrequently: true })
  if (!canvas || !context) return false
  const data = context.getImageData(0, 0, canvas.width, canvas.height).data
  for (let index = 3; index < data.length; index += 4) if (data[index] < 250) return true
  return false
})

function syncCanvas(): void {
  const img = image.value
  const canvas = maskCanvas.value
  if (!img || !canvas || !img.naturalWidth || !img.naturalHeight) return
  canvas.width = img.naturalWidth
  canvas.height = img.naturalHeight
  const context = canvas.getContext('2d')
  if (!context) return
  context.globalCompositeOperation = 'source-over'
  context.fillStyle = '#fff'
  context.fillRect(0, 0, canvas.width, canvas.height)
  history.value = []
  redo.value = []
  activeStroke.value = null
  error.value = ''
  maskRevision.value += 1
  ready.value = true
}
function point(event: PointerEvent): { x: number; y: number } | null {
  const canvas = maskCanvas.value
  if (!canvas) return null
  const rect = canvas.getBoundingClientRect()
  return { x: (event.clientX - rect.left) * canvas.width / rect.width, y: (event.clientY - rect.top) * canvas.height / rect.height }
}
function startStroke(event: PointerEvent): void {
  if (event.button !== 0 || !ready.value || props.busy) return
  drawing.value = true
  activeStroke.value = { mode: mode.value, size: brushSize.value, points: [] }
  maskCanvas.value?.setPointerCapture(event.pointerId)
  drawStroke(event)
}
function drawStroke(event: PointerEvent): void {
  if (!drawing.value) return
  const canvas = maskCanvas.value
  const position = point(event)
  const context = canvas?.getContext('2d')
  if (!canvas || !position || !context) return
  const stroke = activeStroke.value
  if (!stroke) return
  const last = stroke.points.at(-1) || position
  stroke.points.push(position)
  context.save()
  context.globalCompositeOperation = stroke.mode === 'erase' ? 'destination-out' : 'source-over'
  context.strokeStyle = '#fff'
  context.fillStyle = '#fff'
  context.lineCap = 'round'
  context.lineJoin = 'round'
  context.lineWidth = stroke.size
  context.beginPath()
  if (last.x === position.x && last.y === position.y) {
    context.arc(position.x, position.y, stroke.size / 2, 0, Math.PI * 2)
    context.fill()
  } else {
    context.moveTo(last.x, last.y)
    context.lineTo(position.x, position.y)
    context.stroke()
  }
  context.restore()
}
function stopStroke(): void {
  if (!drawing.value) return
  drawing.value = false
  if (activeStroke.value?.points.length) history.value = [...history.value, activeStroke.value]
  activeStroke.value = null
  redo.value = []
  maskRevision.value += 1
}
function replay(strokes: MaskStroke[]): void {
  const canvas = maskCanvas.value
  const context = canvas?.getContext('2d')
  if (!canvas || !context) return
  context.globalCompositeOperation = 'source-over'
  context.fillStyle = '#fff'
  context.fillRect(0, 0, canvas.width, canvas.height)
  for (const stroke of strokes) {
    context.save()
    context.globalCompositeOperation = stroke.mode === 'erase' ? 'destination-out' : 'source-over'
    context.strokeStyle = '#fff'
    context.fillStyle = '#fff'
    context.lineCap = 'round'
    context.lineJoin = 'round'
    context.lineWidth = stroke.size
    stroke.points.forEach((point, index) => {
      const previous = stroke.points[index - 1] || point
      context.beginPath()
      if (previous.x === point.x && previous.y === point.y) {
        context.arc(point.x, point.y, stroke.size / 2, 0, Math.PI * 2)
        context.fill()
      } else {
        context.moveTo(previous.x, previous.y)
        context.lineTo(point.x, point.y)
        context.stroke()
      }
    })
    context.restore()
  }
  maskRevision.value += 1
}
function undoMask(): void { const stroke = history.value.at(-1); if (!stroke) return; history.value = history.value.slice(0, -1); redo.value = [...redo.value, stroke]; replay(history.value) }
function redoMask(): void { const stroke = redo.value.at(-1); if (!stroke) return; redo.value = redo.value.slice(0, -1); history.value = [...history.value, stroke]; replay(history.value) }
function resetMask(): void { syncCanvas() }
function apply(): void {
  if (!ready.value || !maskCanvas.value) return
  if (!prompt.value.trim()) { error.value = t('playground.canvasMaskPromptRequired'); return }
  if (!hasPaint.value) { error.value = t('playground.canvasMaskSelectionRequired'); return }
  emit('apply', maskCanvas.value.toDataURL('image/png'), prompt.value.trim())
}
watch(() => prompt.value, () => { error.value = '' })
onMounted(() => { void nextTick(syncCanvas) })
</script>
