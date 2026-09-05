<template>
  <div class="fixed inset-0 z-[80] flex items-center justify-center bg-gray-950/35 p-4" data-canvas-no-zoom @pointerdown.self="emit('close')">
    <section class="flex max-h-[min(720px,calc(100vh-2rem))] w-full max-w-2xl flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900">
      <header class="flex shrink-0 items-center gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
        <span class="flex h-9 w-9 items-center justify-center rounded bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-200"><Icon name="lightbulb" size="sm" /></span>
        <div class="min-w-0 flex-1"><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasPromptLibrary') }}</h2><p class="text-[11px] text-gray-400">{{ t('playground.canvasPromptLibraryDescription') }} · {{ prompts.length }}</p></div>
        <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.close')" @click="emit('close')"><Icon name="x" size="xs" /><span class="sr-only">{{ t('common.close') }}</span></button>
      </header>
      <div class="flex shrink-0 flex-wrap items-center gap-2 border-b border-gray-100 px-5 py-3 dark:border-dark-700">
        <div class="relative min-w-0 flex-1"><Icon name="search" size="xs" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" /><input v-model="query" class="input h-9 w-full pl-8 text-sm" :placeholder="t('playground.canvasPromptSearch')" /></div>
        <button type="button" class="btn btn-ghost btn-icon h-9 w-9 p-0" :title="t('playground.canvasPromptRefresh')" :disabled="refreshing" @click="emit('refresh')"><Icon name="refresh" size="xs" :class="{ 'animate-spin': refreshing }" /><span class="sr-only">{{ t('playground.canvasPromptRefresh') }}</span></button>
        <button type="button" class="btn btn-secondary h-9 gap-1.5 text-xs" @click="showCreate = !showCreate"><Icon name="plus" size="xs" />{{ t('playground.canvasPromptAdd') }}</button>
      </div>
      <form v-if="showCreate" class="grid shrink-0 gap-2 border-b border-gray-100 bg-gray-50/70 px-5 py-3 dark:border-dark-700 dark:bg-dark-950/60" @submit.prevent="createPrompt">
        <input v-model="draftTitle" class="input h-9 text-sm" :placeholder="t('playground.canvasPromptTitlePlaceholder')" required>
        <textarea v-model="draftContent" rows="3" class="input min-h-20 resize-y text-sm" :placeholder="t('playground.canvasPromptContentPlaceholder')" required></textarea>
        <div class="flex justify-end gap-2"><button type="button" class="btn btn-ghost h-8 text-xs" @click="showCreate = false">{{ t('common.cancel') }}</button><button type="submit" class="btn btn-primary h-8 text-xs">{{ t('common.save') }}</button></div>
      </form>
      <div class="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        <div v-if="filteredPrompts.length" class="grid gap-2 sm:grid-cols-2">
          <article v-for="prompt in filteredPrompts" :key="prompt.id" class="group rounded-lg border border-gray-200 bg-white p-3 transition-colors hover:border-teal-300 hover:bg-teal-50/30 dark:border-dark-700 dark:bg-dark-900 dark:hover:border-teal-700 dark:hover:bg-teal-950/20">
            <div class="flex items-start gap-2"><div class="min-w-0 flex-1"><h3 class="truncate text-xs font-semibold text-gray-800 dark:text-gray-100">{{ prompt.title }}</h3><div class="mt-1 flex flex-wrap gap-1"><span v-for="tag in prompt.tags" :key="tag" class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-500 dark:bg-dark-800 dark:text-dark-400">{{ tag }}</span></div></div><button v-if="prompt.custom" type="button" class="btn btn-ghost btn-icon h-6 w-6 p-0 text-gray-400 hover:text-red-500" :title="t('playground.canvasPromptDelete')" @click="removePrompt(prompt.id)"><Icon name="trash" size="xs" /><span class="sr-only">{{ t('playground.canvasPromptDelete') }}</span></button></div>
            <p class="mt-2 line-clamp-4 whitespace-pre-wrap text-xs leading-5 text-gray-600 dark:text-dark-300">{{ prompt.content }}</p>
            <div class="mt-3 flex items-center justify-between gap-2"><span class="text-[10px] text-gray-400">{{ prompt.source }}</span><button type="button" class="btn btn-primary h-7 gap-1 px-2 text-[11px]" @click="emit('insert', prompt.content)"><Icon name="plus" size="xs" />{{ t('playground.canvasPromptInsert') }}</button></div>
          </article>
        </div>
        <p v-else class="py-14 text-center text-xs text-gray-400">{{ t('playground.canvasPromptEmpty') }}</p>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { CanvasPromptEntry } from './types'

const props = defineProps<{ prompts: CanvasPromptEntry[]; refreshing?: boolean }>()
const emit = defineEmits<{ close: []; insert: [content: string]; add: [prompt: CanvasPromptEntry]; remove: [id: string]; refresh: [] }>()
const { t } = useI18n()
const query = ref('')
const showCreate = ref(false)
const draftTitle = ref('')
const draftContent = ref('')
const filteredPrompts = computed(() => {
  const normalized = query.value.trim().toLowerCase()
  if (!normalized) return props.prompts
  return props.prompts.filter((prompt) => `${prompt.title} ${prompt.content} ${prompt.tags.join(' ')}`.toLowerCase().includes(normalized))
})

function createPrompt(): void {
  const title = draftTitle.value.trim()
  const content = draftContent.value.trim()
  if (!title || !content) return
  emit('add', { id: `custom-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`, title, content, tags: ['custom'], source: t('playground.canvasPromptCustom'), custom: true })
  draftTitle.value = ''
  draftContent.value = ''
  showCreate.value = false
}

function removePrompt(id: string): void {
  if (window.confirm(t('playground.canvasPromptDeleteConfirm'))) emit('remove', id)
}
</script>
