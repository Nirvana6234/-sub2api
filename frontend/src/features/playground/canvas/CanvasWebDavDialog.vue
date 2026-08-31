<template>
  <div class="absolute inset-0 z-[70] flex items-center justify-center bg-gray-950/45 p-4" data-canvas-no-zoom @pointerdown.self="emit('close')">
    <form class="w-full max-w-md rounded-xl border border-gray-200 bg-white p-5 shadow-2xl dark:border-dark-600 dark:bg-dark-900" @submit.prevent="submit('upload')">
      <div class="flex items-start justify-between gap-3">
        <div><h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasWebdavTitle') }}</h2><p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('playground.canvasWebdavDescription') }}</p></div>
        <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('common.close')" @click="emit('close')"><Icon name="x" size="xs" /><span class="sr-only">{{ t('common.close') }}</span></button>
      </div>
      <label class="mt-4 block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasWebdavUrl') }}<input v-model="draft.url" class="input mt-1.5 h-9 w-full text-sm" type="url" required placeholder="https://dav.example.com/remote.php/dav/files/user" /></label>
      <div class="mt-3 grid grid-cols-2 gap-2"><label class="block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasWebdavUsername') }}<input v-model="draft.username" class="input mt-1.5 h-9 w-full text-sm" autocomplete="username" /></label><label class="block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasWebdavPassword') }}<input v-model="draft.password" class="input mt-1.5 h-9 w-full text-sm" type="password" autocomplete="current-password" /></label></div>
      <label class="mt-3 block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasWebdavFile') }}<input v-model="draft.fileName" class="input mt-1.5 h-9 w-full text-sm" /></label>
      <p class="mt-3 text-[11px] leading-5 text-amber-700 dark:text-amber-300">{{ t('playground.canvasWebdavSecurity') }}</p>
      <div class="mt-5 flex flex-wrap justify-end gap-2"><button type="button" class="btn btn-ghost" :disabled="busy" @click="submit('download')"><Icon name="download" size="xs" />{{ t('playground.canvasWebdavRestore') }}</button><button type="submit" class="btn btn-primary" :disabled="busy"><Icon name="upload" size="xs" />{{ t('playground.canvasWebdavBackup') }}</button></div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { CanvasWebDavConfig } from './webdavSync'

const props = defineProps<{ config: CanvasWebDavConfig; busy?: boolean }>()
const emit = defineEmits<{ close: []; submit: [action: 'upload' | 'download', config: CanvasWebDavConfig] }>()
const { t } = useI18n()
const draft = reactive<CanvasWebDavConfig>({ ...props.config })
watch(() => props.config, (value) => Object.assign(draft, value), { deep: true })
function submit(action: 'upload' | 'download'): void { emit('submit', action, { ...draft }) }
</script>
