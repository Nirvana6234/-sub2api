<template>
  <div class="fixed inset-0 z-[125] flex items-center justify-center bg-gray-950/90 p-4" data-canvas-no-zoom data-image-viewer @pointerdown.self="emit('close')">
    <section class="flex h-full w-full max-w-6xl flex-col overflow-hidden rounded-lg border border-white/15 bg-gray-900 shadow-2xl" role="dialog" aria-modal="true" :aria-label="t('playground.canvasViewImage')">
      <header class="flex items-center justify-between border-b border-white/10 px-4 py-3 text-white"><h2 class="truncate text-sm font-medium">{{ title || t('playground.generatedImage') }}</h2><div class="flex items-center gap-1"><button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0 text-white hover:bg-white/10" :title="t('playground.canvasZoomOut')" @click="zoom = Math.max(0.25, zoom - 0.25)"><Icon name="minus" size="xs" /><span class="sr-only">{{ t('playground.canvasZoomOut') }}</span></button><span class="w-12 text-center text-xs text-white/70">{{ Math.round(zoom * 100) }}%</span><button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0 text-white hover:bg-white/10" :title="t('playground.canvasZoomIn')" @click="zoom = Math.min(4, zoom + 0.25)"><Icon name="plus" size="xs" /><span class="sr-only">{{ t('playground.canvasZoomIn') }}</span></button><button type="button" class="btn btn-ghost btn-icon ml-2 h-8 w-8 p-0 text-white hover:bg-white/10" :title="t('common.close')" @click="emit('close')"><Icon name="x" size="sm" /><span class="sr-only">{{ t('common.close') }}</span></button></div></header>
      <div class="flex min-h-0 flex-1 items-center justify-center overflow-auto p-6"><img :src="imageUrl" :alt="title || t('playground.generatedImage')" class="max-h-full max-w-full object-contain transition-transform" :style="{ transform: `scale(${zoom})` }" draggable="false"></div>
    </section>
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
defineProps<{ imageUrl: string; title?: string }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const zoom = ref(1)
</script>
