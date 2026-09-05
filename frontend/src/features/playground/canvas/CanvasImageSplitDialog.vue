<template>
  <div class="fixed inset-0 z-[120] flex items-center justify-center bg-gray-950/70 p-4 backdrop-blur-sm" data-canvas-no-zoom @pointerdown.self="emit('close')">
    <section class="flex max-h-[calc(100%-2rem)] w-full max-w-3xl flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900" role="dialog" aria-modal="true" :aria-label="t('playground.canvasSplitImage')" data-image-split-dialog>
      <header class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
        <div><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasSplitImage') }}</h2><p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('playground.canvasSplitImageDescription') }}</p></div>
        <button type="button" class="btn btn-ghost btn-icon h-8 w-8 shrink-0 p-0" :title="t('common.close')" :disabled="busy" @click="emit('close')"><Icon name="x" size="sm" /><span class="sr-only">{{ t('common.close') }}</span></button>
      </header>
      <div class="grid min-h-0 flex-1 gap-0 overflow-y-auto md:grid-cols-[minmax(0,1fr)_240px]">
        <div class="flex min-h-80 items-center justify-center bg-gray-950 p-6">
          <div class="relative max-h-[54vh] max-w-full overflow-hidden shadow-xl">
            <img :src="imageUrl" :alt="t('playground.canvasSplitPreview')" class="block max-h-[54vh] max-w-full object-contain" draggable="false">
            <div class="pointer-events-none absolute inset-0 grid" :style="gridStyle" aria-hidden="true">
              <span v-for="cell in cellCount" :key="cell" class="border border-white/80 bg-teal-400/5 shadow-[inset_0_0_0_1px_rgba(0,0,0,0.25)]"></span>
            </div>
          </div>
        </div>
        <aside class="space-y-5 border-t border-gray-200 p-5 dark:border-dark-700 md:border-l md:border-t-0">
          <label class="block text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('playground.canvasSplitRows') }}
            <div class="mt-2 flex items-center gap-3"><input v-model.number="rows" type="range" min="1" max="6" step="1" class="min-w-0 flex-1 accent-teal-600"><input v-model.number="rows" type="number" min="1" max="6" class="input h-9 w-16 text-center"></div>
          </label>
          <label class="block text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('playground.canvasSplitColumns') }}
            <div class="mt-2 flex items-center gap-3"><input v-model.number="columns" type="range" min="1" max="6" step="1" class="min-w-0 flex-1 accent-teal-600"><input v-model.number="columns" type="number" min="1" max="6" class="input h-9 w-16 text-center"></div>
          </label>
          <div class="rounded-md border border-gray-200 bg-gray-50 p-3 text-xs text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-300">
            {{ t('playground.canvasSplitCount', { count: cellCount }) }}
          </div>
        </aside>
      </div>
      <footer class="flex justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700">
        <button type="button" class="btn btn-ghost" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary gap-1.5" data-image-split-apply :disabled="busy" @click="apply"><Icon name="grid" size="sm" :class="{ 'animate-pulse': busy }" />{{ busy ? t('playground.canvasImageApplying') : t('playground.canvasSplitApply') }}</button>
      </footer>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import { normalizeCanvasImageGrid, type CanvasImageGrid } from './imageTransform'

defineProps<{ imageUrl: string; busy?: boolean }>()
const emit = defineEmits<{ close: []; apply: [grid: CanvasImageGrid] }>()
const { t } = useI18n()
const rows = ref(2)
const columns = ref(2)
watch(rows, (value) => { rows.value = normalizeCanvasImageGrid({ rows: value, columns: columns.value }).rows })
watch(columns, (value) => { columns.value = normalizeCanvasImageGrid({ rows: rows.value, columns: value }).columns })
const cellCount = computed(() => rows.value * columns.value)
const gridStyle = computed(() => ({ gridTemplateRows: `repeat(${rows.value}, minmax(0, 1fr))`, gridTemplateColumns: `repeat(${columns.value}, minmax(0, 1fr))` }))
function apply(): void { emit('apply', normalizeCanvasImageGrid({ rows: rows.value, columns: columns.value })) }
</script>
