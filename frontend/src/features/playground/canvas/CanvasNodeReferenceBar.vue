<template>
  <section class="border-b border-gray-100 bg-white/95 px-3 py-2 dark:border-dark-700 dark:bg-dark-900/95" data-canvas-node-reference-bar data-canvas-no-zoom @pointerdown.stop>
    <div class="mb-1.5 text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.canvasReferencesLabel') }}</div>
    <div class="thin-scrollbar flex min-h-12 gap-2 overflow-x-auto pb-0.5">
      <div
        v-for="reference in references"
        :key="`${reference.sourceNodeId}:${reference.nodeId}`"
        class="group relative grid h-12 w-12 shrink-0 place-items-center overflow-hidden rounded-xl border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-950"
        :title="reference.text || reference.label || reference.title"
      >
        <button type="button" class="grid h-full w-full place-items-center" :aria-label="reference.label || reference.title" @click.stop="emit('focus-reference', reference.nodeId)">
          <img v-if="reference.previewUrl && reference.kind === 'image'" :src="reference.previewUrl" alt="" class="h-full w-full object-cover">
          <video v-else-if="reference.previewUrl && reference.kind === 'video'" :src="reference.previewUrl" class="h-full w-full object-cover" muted playsinline preload="metadata"></video>
          <audio v-else-if="reference.previewUrl && reference.kind === 'audio'" :src="reference.previewUrl" class="w-full" controls @click.stop></audio>
          <span v-else class="grid h-full w-full place-items-center px-1 text-center text-[9px] font-medium uppercase text-teal-700 dark:text-teal-200">{{ reference.kind === 'text' ? 'TXT' : reference.kind }}</span>
        </button>
        <button
          type="button"
          class="absolute right-0 top-0 grid h-5 w-5 translate-x-1/4 -translate-y-1/4 place-items-center rounded-full border border-gray-200 bg-white text-gray-500 opacity-0 shadow-sm transition-opacity group-hover:opacity-100 focus-visible:opacity-100 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-200"
          :aria-label="t('playground.canvasDisconnectReference')"
          :title="t('playground.canvasDisconnectReference')"
          @pointerdown.stop
          @click.stop="emit('disconnect-reference', reference.sourceNodeId)"
        >
          <Icon name="x" size="xs" />
        </button>
      </div>
      <button
        type="button"
        class="grid h-12 w-12 shrink-0 place-items-center rounded-xl border border-dashed border-gray-300 text-gray-500 transition hover:border-teal-400 hover:text-teal-700 dark:border-dark-600 dark:text-dark-400 dark:hover:border-teal-600 dark:hover:text-teal-200"
        :title="t('playground.canvasAddReference')"
        :aria-label="t('playground.canvasAddReference')"
        @click.stop="emit('start-selection')"
      >
        <Icon name="plus" size="sm" />
      </button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { CanvasReferenceItem } from './canvasReferences'

defineProps<{
  references: CanvasReferenceItem[]
}>()

const emit = defineEmits<{
  'start-selection': []
  'disconnect-reference': [sourceId: string]
  'focus-reference': [nodeId: string]
}>()

const { t } = useI18n()
</script>
