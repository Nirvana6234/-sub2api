<template>
  <aside class="absolute inset-y-0 right-0 z-[75] flex w-[min(420px,calc(100%-1rem))] flex-col border-l border-gray-200 bg-white/98 shadow-2xl backdrop-blur dark:border-dark-700 dark:bg-dark-900/98" data-canvas-plugin-dialog data-canvas-no-zoom>
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-gray-100 px-4 dark:border-dark-700">
      <span class="flex h-8 w-8 items-center justify-center rounded bg-teal-50 text-teal-700 dark:bg-teal-900/40 dark:text-teal-200"><Icon name="cube" size="sm" /></span>
      <div class="min-w-0 flex-1"><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasPluginsTitle') }}</h2><p class="truncate text-[11px] text-gray-400">{{ t('playground.canvasPluginsCount', { count: plugins.length }) }}</p></div>
      <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('common.close')" @click="emit('close')"><Icon name="x" size="xs" /><span class="sr-only">{{ t('common.close') }}</span></button>
    </header>
    <div class="min-h-0 flex-1 overflow-y-auto px-4 py-4">
      <form class="space-y-2" @submit.prevent="submitInstall">
        <label class="block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasPluginUrl') }}
          <input v-model.trim="url" class="input mt-1.5 h-9 w-full text-xs" placeholder="https://example.com/plugin.json" :disabled="busy">
        </label>
        <button type="submit" class="btn btn-primary w-full gap-1.5" :disabled="busy || !url"><Icon name="download" size="sm" />{{ busy ? t('playground.canvasPluginInstalling') : t('playground.canvasPluginInstall') }}</button>
      </form>
      <p class="mt-3 rounded border border-amber-200 bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200">{{ t('playground.canvasPluginSafety') }}</p>
      <p v-if="errorMessage" class="mt-3 rounded border border-red-200 bg-red-50 p-3 text-xs leading-5 text-red-700 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-300">{{ errorMessage }}</p>
      <div v-if="officialPlugins.length" class="mt-5">
        <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-400">{{ t('playground.canvasOfficialPlugins') }}</h3>
        <div class="space-y-2">
          <article v-for="plugin in officialPlugins" :key="plugin.id" class="flex items-center gap-3 rounded border border-gray-200 p-2.5 dark:border-dark-700" :data-plugin-catalog-id="plugin.id">
            <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded bg-teal-50 text-lg dark:bg-teal-900/40">{{ plugin.icon || '◇' }}</span>
            <div class="min-w-0 flex-1"><p class="truncate text-xs font-medium text-gray-900 dark:text-white">{{ plugin.name }}</p><p class="truncate text-[10px] text-gray-400">{{ plugin.description || plugin.id }}</p></div>
            <button type="button" class="btn btn-ghost h-7 shrink-0 px-2 text-[11px]" :disabled="busy || plugins.some((installed) => installed.id === plugin.id && installed.url === plugin.url)" @click="emit('install', plugin.url)">{{ plugins.some((installed) => installed.id === plugin.id && installed.url === plugin.url) ? t('playground.canvasPluginInstalledLabel') : plugins.some((installed) => installed.id === plugin.id) ? t('playground.canvasPluginUpdate') : t('playground.canvasPluginInstall') }}</button>
          </article>
        </div>
      </div>
      <div class="mt-5 space-y-2">
        <article v-for="plugin in plugins" :key="plugin.id" class="rounded border border-gray-200 p-3 dark:border-dark-700" :data-plugin-installed-id="plugin.id">
          <div class="flex items-start gap-3">
            <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-dark-200"><Icon name="cube" size="sm" /></span>
            <div class="min-w-0 flex-1"><p class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ plugin.name }}</p><p class="text-[11px] text-gray-400">{{ plugin.id }} · v{{ plugin.version }}</p><p v-if="plugin.description" class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ plugin.description }}</p></div>
            <input type="checkbox" :checked="plugin.enabled" :disabled="busy" :aria-label="plugin.name" @change="emit('toggle', { id: plugin.id, enabled: ($event.target as HTMLInputElement).checked })">
          </div>
          <div class="mt-2 flex items-center justify-between gap-2 text-[11px] text-gray-400"><span>{{ t('playground.canvasPluginNodes', { count: plugin.nodes.length }) }}</span><button type="button" class="text-red-600 hover:underline dark:text-red-300" :disabled="busy" @click="emit('uninstall', plugin.id)">{{ t('playground.canvasPluginUninstall') }}</button></div>
        </article>
        <p v-if="!plugins.length" class="py-8 text-center text-xs text-gray-400">{{ t('playground.canvasPluginEmpty') }}</p>
      </div>
    </div>
    <footer class="shrink-0 border-t border-gray-100 p-3 text-[11px] leading-5 text-gray-400 dark:border-dark-700">{{ t('playground.canvasPluginNodeHint') }}</footer>
  </aside>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { CanvasPluginCatalogEntry, CanvasPluginManifest } from './canvasPlugins'

defineProps<{ plugins: CanvasPluginManifest[]; officialPlugins: CanvasPluginCatalogEntry[]; busy: boolean; errorMessage?: string }>()
const emit = defineEmits<{
  close: []
  install: [url: string]
  toggle: [value: { id: string; enabled: boolean }]
  uninstall: [id: string]
}>()
const { t } = useI18n()
const url = ref('')
function submitInstall(): void {
  if (!url.value.trim()) return
  emit('install', url.value.trim())
  url.value = ''
}
</script>
