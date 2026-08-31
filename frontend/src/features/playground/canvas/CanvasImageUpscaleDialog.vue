<template>
  <div class="fixed inset-0 z-[120] flex items-center justify-center bg-gray-950/70 p-4 backdrop-blur-sm" data-canvas-no-zoom @pointerdown.self="emit('close')">
    <section class="flex max-h-[calc(100%-2rem)] w-full max-w-4xl flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900" role="dialog" aria-modal="true" :aria-label="t('playground.canvasUpscaleImage')" data-image-upscale-dialog>
      <header class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
        <div><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasUpscaleImage') }}</h2><p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('playground.canvasUpscaleImageDescription') }}</p></div>
        <button type="button" class="btn btn-ghost btn-icon h-8 w-8 shrink-0 p-0" :title="t('common.close')" :disabled="busy" @click="emit('close')"><Icon name="x" size="sm" /><span class="sr-only">{{ t('common.close') }}</span></button>
      </header>
      <div class="grid min-h-0 flex-1 gap-0 overflow-y-auto md:grid-cols-[minmax(0,1fr)_260px]">
        <div class="flex min-h-80 items-center justify-center bg-gray-950 p-6"><img :src="imageUrl" :alt="t('playground.canvasUpscalePreview')" class="max-h-[56vh] max-w-full object-contain shadow-xl" draggable="false"></div>
        <aside class="space-y-5 border-t border-gray-200 p-5 dark:border-dark-700 md:border-l md:border-t-0">
          <div class="space-y-2"><p class="text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('playground.canvasUpscaleTarget') }}</p><div class="grid grid-cols-3 gap-1.5"><button v-for="option in targetOptions" :key="option.value" type="button" class="btn h-10 px-2 text-xs" :class="targetLongEdge === option.value ? 'btn-primary' : 'btn-ghost border border-gray-200 dark:border-dark-600'" :disabled="sourceLongEdge >= option.value" @click="targetLongEdge = option.value">{{ option.label }}</button></div></div>
          <div class="space-y-2"><p class="text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('playground.canvasUpscaleAlgorithm') }}</p><select v-model="algorithm" class="select h-9 w-full text-xs"><option value="high">{{ t('playground.canvasUpscaleHigh') }}</option><option value="bilinear">{{ t('playground.canvasUpscaleBilinear') }}</option><option value="nearest">{{ t('playground.canvasUpscaleNearest') }}</option></select></div>
          <div class="rounded-md border border-gray-200 bg-gray-50 p-3 text-xs text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-300"><div class="flex justify-between gap-2"><span>{{ t('playground.canvasUpscaleSource') }}</span><span>{{ sourceSize }}</span></div><div class="mt-1 flex justify-between gap-2 font-medium"><span>{{ t('playground.canvasUpscaleOutput') }}</span><span>{{ outputSize }}</span></div></div>
        </aside>
      </div>
      <footer class="flex justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700"><button type="button" class="btn btn-ghost" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary gap-1.5" data-image-upscale-apply :disabled="busy || !canUpscale" @click="apply"><Icon name="sparkles" size="sm" :class="{ 'animate-pulse': busy }" />{{ busy ? t('playground.canvasImageApplying') : t('playground.canvasUpscaleApply') }}</button></footer>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import { MAX_CANVAS_UPSCALE_LONG_EDGE, resolveCanvasUpscaleSize, type CanvasImageUpscaleAlgorithm, type CanvasImageUpscaleParams } from './imageTransform'

const props = defineProps<{ imageUrl: string; sourceWidth?: number; sourceHeight?: number; busy?: boolean }>()
const emit = defineEmits<{ close: []; apply: [params: CanvasImageUpscaleParams] }>()
const { t } = useI18n()
const width = ref(props.sourceWidth ?? 0)
const height = ref(props.sourceHeight ?? 0)
const targetLongEdge = ref(2048)
const algorithm = ref<CanvasImageUpscaleAlgorithm>('high')
const targetOptions = [{ label: '1K', value: 1024 }, { label: '2K', value: 2048 }, { label: '4K', value: MAX_CANVAS_UPSCALE_LONG_EDGE }]
const sourceLongEdge = computed(() => Math.max(width.value, height.value))
const canUpscale = computed(() => Boolean(sourceLongEdge.value && sourceLongEdge.value < targetLongEdge.value))
const output = computed(() => width.value && height.value ? resolveCanvasUpscaleSize(width.value, height.value, targetLongEdge.value) : null)
const sourceSize = computed(() => width.value && height.value ? `${width.value} × ${height.value}` : '...')
const outputSize = computed(() => output.value ? `${output.value.width} × ${output.value.height}` : '...')
onMounted(() => {
  const image = new Image()
  image.onload = () => {
    width.value = image.naturalWidth
    height.value = image.naturalHeight
    targetLongEdge.value = targetOptions.find((option) => image.naturalWidth < option.value || image.naturalHeight < option.value)?.value ?? MAX_CANVAS_UPSCALE_LONG_EDGE
  }
  image.src = props.imageUrl
})
function apply(): void { emit('apply', { targetLongEdge: targetLongEdge.value, algorithm: algorithm.value }) }
</script>
