<template>
  <div class="fixed inset-0 z-[100] flex items-center justify-center bg-gray-950/55 p-4" role="dialog" aria-modal="true" :aria-label="t('playground.canvasAssets')" @pointerdown.self="emit('close')">
    <section class="flex max-h-[78vh] w-full max-w-3xl flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900" data-canvas-no-zoom>
      <header class="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-dark-700">
        <div><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasAssets') }}</h2><p class="mt-0.5 text-xs text-gray-400">{{ t('playground.canvasAssetsDescription') }}</p></div>
        <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.close')" @click="emit('close')"><Icon name="x" size="sm" /><span class="sr-only">{{ t('common.close') }}</span></button>
      </header>
      <div v-if="assets.length" class="grid min-h-0 flex-1 grid-cols-2 gap-3 overflow-y-auto p-4 sm:grid-cols-3 md:grid-cols-4">
        <button v-for="asset in assets" :key="asset.key" type="button" class="group overflow-hidden rounded-lg border border-gray-200 bg-gray-50 text-left transition hover:border-teal-400 hover:shadow-md dark:border-dark-700 dark:bg-dark-950" @click="emit('insert', asset.key)">
          <div class="relative aspect-square overflow-hidden bg-gray-100 dark:bg-dark-800">
            <img v-if="asset.kind === 'image' && asset.url" :src="asset.url" :alt="asset.title" class="h-full w-full object-cover transition-transform group-hover:scale-[1.03]">
            <video v-else-if="asset.kind === 'video' && asset.url" :src="asset.url" muted preload="metadata" class="h-full w-full object-cover"></video>
            <audio v-else-if="asset.kind === 'audio' && asset.url" :src="asset.url" controls class="absolute inset-x-1 bottom-2 w-[calc(100%-0.5rem)]" @pointerdown.stop @click.stop></audio>
            <div v-else class="flex h-full items-center justify-center p-3 text-center text-xs leading-5 text-gray-500 dark:text-dark-400">{{ asset.text || asset.title }}</div>
            <span class="absolute left-2 top-2 rounded bg-gray-950/65 px-1.5 py-0.5 text-[10px] font-medium text-white">{{ t(`playground.canvasNodeType.${asset.kind}`) }}</span>
          </div>
          <div class="p-2"><p class="truncate text-xs font-medium text-gray-700 dark:text-gray-200">{{ asset.title }}</p><p class="mt-0.5 truncate text-[10px] text-gray-400">{{ asset.subtitle || asset.kind }}</p></div>
        </button>
      </div>
      <div v-else class="flex min-h-64 items-center justify-center px-6 text-center text-sm text-gray-400">{{ t('playground.canvasAssetsEmpty') }}</div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
export type CanvasAssetPickerItem = {
  key: string
  kind: 'image' | 'video' | 'audio' | 'text'
  title: string
  subtitle?: string
  url?: string
  text?: string
}

defineProps<{ assets: CanvasAssetPickerItem[] }>()
const emit = defineEmits<{ close: []; insert: [key: string] }>()
const { t } = useI18n()
</script>
