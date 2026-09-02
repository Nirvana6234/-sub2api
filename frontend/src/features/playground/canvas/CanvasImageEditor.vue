<template>
  <div class="fixed inset-0 z-[120] flex items-center justify-center bg-gray-950/60 p-4 backdrop-blur-[1px]" data-canvas-no-zoom @pointerdown.self="emit('close')">
    <section class="flex max-h-[calc(100%-2rem)] w-full max-w-4xl flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900" role="dialog" aria-modal="true" :aria-label="t('playground.canvasImageEditor')" data-image-editor>
      <header class="flex items-center justify-between gap-3 border-b border-gray-200 px-5 py-3 dark:border-dark-700">
        <div><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasImageEditor') }}</h2><p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('playground.canvasImageEditorDescription') }}</p></div>
        <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.close')" @click="emit('close')"><Icon name="x" size="sm" /><span class="sr-only">{{ t('common.close') }}</span></button>
      </header>

      <div class="grid min-h-0 flex-1 lg:grid-cols-[1fr_220px]">
        <div ref="stageRef" class="relative flex min-h-[360px] items-center justify-center overflow-hidden bg-[color:var(--canvas-editor-bg,#17191c)] p-6">
          <img ref="imageRef" :src="imageUrl" :alt="t('playground.generatedImage')" class="max-h-[62vh] max-w-full select-none object-contain" :style="previewStyle" draggable="false" @load="handleImageLoad">
          <div v-if="ready" class="pointer-events-none absolute inset-0 bg-gray-950/45" :style="maskStyle"></div>
          <div v-if="ready" class="absolute cursor-move border-2 border-white shadow-[0_0_0_1px_rgba(0,0,0,.45)]" :style="cropStyle" @pointerdown.stop="startDrag('move', $event)">
            <span class="pointer-events-none absolute inset-0 grid grid-cols-3 grid-rows-3 opacity-55"><i v-for="index in 9" :key="index" class="border-b border-r border-white/60" /></span>
            <button v-for="handle in handles" :key="handle" type="button" class="absolute h-4 w-4 rounded-sm border-2 border-white bg-teal-500 shadow" :class="handleClasses[handle]" :aria-label="t('playground.canvasCropResize')" @pointerdown.stop="startDrag(handle, $event)"></button>
          </div>
        </div>

        <aside class="overflow-y-auto border-l border-gray-200 p-4 dark:border-dark-700">
          <h3 class="text-xs font-semibold uppercase text-gray-500 dark:text-dark-400">{{ t('playground.canvasCropRatio') }}</h3>
          <div class="mt-2 grid grid-cols-3 gap-1.5"><button v-for="option in aspectOptions" :key="option.value" type="button" class="btn h-8 px-2 text-[11px]" :class="aspect === option.value ? 'bg-teal-50 text-teal-700 dark:bg-teal-900/40 dark:text-teal-200' : 'btn-ghost'" @click="setAspect(option.value)">{{ option.label }}</button></div>
          <h3 class="mt-5 text-xs font-semibold uppercase text-gray-500 dark:text-dark-400">{{ t('playground.canvasTransform') }}</h3>
          <div class="mt-2 grid grid-cols-2 gap-1.5">
            <button type="button" class="btn btn-ghost h-9 gap-1.5 px-2 text-xs" @click="rotate(-90)"><Icon name="rotateLeft" size="sm" />{{ t('playground.canvasRotateLeft') }}</button>
            <button type="button" class="btn btn-ghost h-9 gap-1.5 px-2 text-xs" @click="rotate(90)"><Icon name="rotateRight" size="sm" />{{ t('playground.canvasRotateRight') }}</button>
            <button type="button" class="btn h-9 gap-1.5 px-2 text-xs" :class="flipX ? 'bg-teal-50 text-teal-700 dark:bg-teal-900/40' : 'btn-ghost'" @click="flipX = !flipX"><Icon name="flipHorizontal" size="sm" />{{ t('playground.canvasFlipHorizontal') }}</button>
            <button type="button" class="btn h-9 gap-1.5 px-2 text-xs" :class="flipY ? 'bg-teal-50 text-teal-700 dark:bg-teal-900/40' : 'btn-ghost'" @click="flipY = !flipY"><Icon name="flipVertical" size="sm" />{{ t('playground.canvasFlipVertical') }}</button>
          </div>
          <dl class="mt-5 space-y-2 border-t border-gray-100 pt-4 text-xs dark:border-dark-700"><div class="flex justify-between gap-2"><dt class="text-gray-500">{{ t('playground.canvasCropSource') }}</dt><dd>{{ naturalWidth }} × {{ naturalHeight }}</dd></div><div class="flex justify-between gap-2"><dt class="text-gray-500">{{ t('playground.canvasCropOutput') }}</dt><dd>{{ outputSize.width }} × {{ outputSize.height }}</dd></div><div class="flex justify-between gap-2"><dt class="text-gray-500">{{ t('playground.canvasCropRotation') }}</dt><dd>{{ rotation }}°</dd></div></dl>
        </aside>
      </div>

      <footer class="flex justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700"><button type="button" class="btn btn-ghost" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary gap-1.5" :disabled="!ready || busy" data-image-editor-apply @click="apply"><Icon name="check" size="sm" />{{ busy ? t('playground.canvasImageApplying') : t('playground.canvasImageApply') }}</button></footer>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import { normalizeCanvasCropRect, type CanvasCropRect, type CanvasImageTransform } from './imageTransform'

defineProps<{ imageUrl: string; busy?: boolean }>()
const emit = defineEmits<{ close: []; apply: [transform: CanvasImageTransform] }>()
const { t } = useI18n()
const stageRef = ref<HTMLElement | null>(null)
const imageRef = ref<HTMLImageElement | null>(null)
const naturalWidth = ref(0)
const naturalHeight = ref(0)
const crop = ref<CanvasCropRect>({ x: 0, y: 0, width: 1, height: 1 })
const aspect = ref('free')
const rotation = ref<0 | 90 | 180 | 270>(0)
const flipX = ref(false)
const flipY = ref(false)
const handles = ['top-left', 'top-right', 'bottom-left', 'bottom-right'] as const
type Handle = typeof handles[number]
type DragKind = Handle | 'move'
const drag = ref<{ kind: DragKind; x: number; y: number; crop: CanvasCropRect } | null>(null)
const layoutVersion = ref(0)
const ready = computed(() => naturalWidth.value > 0 && naturalHeight.value > 0)
const previewStyle = computed(() => {
  if (!ready.value) return undefined
  const longest = Math.max(naturalWidth.value, naturalHeight.value)
  if (longest >= 320) return undefined
  const scale = 320 / longest
  return { width: `${Math.round(naturalWidth.value * scale)}px`, height: `${Math.round(naturalHeight.value * scale)}px` }
})
const imageBox = computed(() => {
  void layoutVersion.value
  const image = imageRef.value
  const stage = stageRef.value
  return image && stage ? { left: image.offsetLeft, top: image.offsetTop, width: image.clientWidth, height: image.clientHeight } : { left: 0, top: 0, width: 0, height: 0 }
})
const cropStyle = computed(() => ({ left: `${imageBox.value.left + crop.value.x * imageBox.value.width}px`, top: `${imageBox.value.top + crop.value.y * imageBox.value.height}px`, width: `${crop.value.width * imageBox.value.width}px`, height: `${crop.value.height * imageBox.value.height}px` }))
const maskStyle = computed(() => ({ clipPath: `polygon(0 0, 100% 0, 100% 100%, 0 100%, 0 0, ${cropStyle.value.left} ${cropStyle.value.top}, ${cropStyle.value.left} calc(${cropStyle.value.top} + ${cropStyle.value.height}), calc(${cropStyle.value.left} + ${cropStyle.value.width}) calc(${cropStyle.value.top} + ${cropStyle.value.height}), calc(${cropStyle.value.left} + ${cropStyle.value.width}) ${cropStyle.value.top}, ${cropStyle.value.left} ${cropStyle.value.top})` }))
const outputSize = computed(() => {
  const width = Math.max(1, Math.round(crop.value.width * naturalWidth.value))
  const height = Math.max(1, Math.round(crop.value.height * naturalHeight.value))
  return rotation.value === 90 || rotation.value === 270 ? { width: height, height: width } : { width, height }
})
const aspectOptions = [{ value: 'free', label: t('playground.canvasCropFree') }, { value: '1', label: '1:1' }, { value: '1.333333', label: '4:3' }, { value: '1.5', label: '3:2' }, { value: '1.777778', label: '16:9' }, { value: '0.5625', label: '9:16' }]
const handleClasses: Record<Handle, string> = { 'top-left': '-left-2 -top-2 cursor-nwse-resize', 'top-right': '-right-2 -top-2 cursor-nesw-resize', 'bottom-left': '-bottom-2 -left-2 cursor-nesw-resize', 'bottom-right': '-bottom-2 -right-2 cursor-nwse-resize' }

function handleImageLoad(): void { naturalWidth.value = imageRef.value?.naturalWidth ?? 0; naturalHeight.value = imageRef.value?.naturalHeight ?? 0; layoutVersion.value += 1 }
function setAspect(value: string): void {
  aspect.value = value
  if (value === 'free' || !ready.value) return
  const ratio = Number(value) * naturalHeight.value / naturalWidth.value
  let width = 1
  let height = width / ratio
  if (height > 1) { height = 1; width = ratio }
  crop.value = { x: (1 - width) / 2, y: (1 - height) / 2, width, height }
}
function rotate(offset: number): void { rotation.value = ((rotation.value + offset + 360) % 360) as 0 | 90 | 180 | 270 }
function startDrag(kind: DragKind, event: PointerEvent): void { drag.value = { kind, x: event.clientX, y: event.clientY, crop: { ...crop.value } }; window.addEventListener('pointermove', handlePointerMove); window.addEventListener('pointerup', stopDrag, { once: true }) }
function handlePointerMove(event: PointerEvent): void {
  const active = drag.value
  const box = imageBox.value
  if (!active || !box.width || !box.height) return
  const dx = (event.clientX - active.x) / box.width
  const dy = (event.clientY - active.y) / box.height
  const next = { ...active.crop }
  if (active.kind === 'move') { next.x += dx; next.y += dy } else {
    const left = active.kind.includes('left')
    const top = active.kind.includes('top')
    if (left) { next.x += dx; next.width -= dx } else next.width += dx
    if (top) { next.y += dy; next.height -= dy } else next.height += dy
    if (aspect.value !== 'free') {
      const normalizedRatio = Number(aspect.value) * naturalHeight.value / naturalWidth.value
      next.height = next.width / normalizedRatio
      if (top) next.y = active.crop.y + active.crop.height - next.height
    }
  }
  crop.value = normalizeCanvasCropRect(next)
}
function stopDrag(): void { drag.value = null; window.removeEventListener('pointermove', handlePointerMove) }
function apply(): void { emit('apply', { crop: normalizeCanvasCropRect(crop.value), rotation: rotation.value, flipX: flipX.value, flipY: flipY.value }) }
onBeforeUnmount(stopDrag)
</script>
