<template>
  <aside class="absolute inset-y-0 right-0 z-[70] flex w-[min(390px,calc(100%-1rem))] flex-col border-l border-gray-200 bg-white/98 shadow-2xl backdrop-blur dark:border-dark-700 dark:bg-dark-900/98" data-canvas-no-zoom>
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-gray-100 px-4 dark:border-dark-700">
      <span class="flex h-8 w-8 items-center justify-center rounded bg-teal-50 text-teal-700 dark:bg-teal-900/40 dark:text-teal-200"><Icon name="chatBubble" size="sm" /></span>
      <div class="min-w-0 flex-1"><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasAssistantTitle') }}</h2><p class="truncate text-[11px] text-gray-400">{{ t('playground.canvasAssistantContext', { count: contextCount }) }}</p></div>
      <button v-if="messages.length" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasAssistantClear')" @click="emit('clear')"><Icon name="trash" size="xs" /><span class="sr-only">{{ t('playground.canvasAssistantClear') }}</span></button>
      <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('common.close')" @click="emit('close')"><Icon name="x" size="xs" /><span class="sr-only">{{ t('common.close') }}</span></button>
    </header>

    <div ref="messageList" class="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4">
      <div v-if="!messages.length" class="flex h-full min-h-64 flex-col items-center justify-center px-5 text-center">
        <span class="flex h-11 w-11 items-center justify-center rounded border border-dashed border-teal-300 bg-teal-50 text-teal-700 dark:border-teal-800 dark:bg-teal-950/40 dark:text-teal-300"><Icon name="sparkles" size="md" /></span>
        <h3 class="mt-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasAssistantEmptyTitle') }}</h3>
        <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('playground.canvasAssistantEmptyDescription') }}</p>
      </div>
      <article v-for="message in messages" :key="message.id" class="group" :class="message.role === 'user' ? 'pl-8' : 'pr-8'">
        <p class="mb-1 text-[10px] font-medium uppercase text-gray-400">{{ message.role === 'user' ? t('playground.canvasAssistantYou') : t('playground.canvasAssistantName') }}</p>
        <div class="rounded-lg px-3 py-2.5 text-sm leading-6" :class="message.role === 'user' ? 'bg-teal-600 text-white' : 'border border-gray-200 bg-gray-50 text-gray-800 dark:border-dark-700 dark:bg-dark-950 dark:text-gray-100'">
          <CanvasMentionContent
            v-if="message.content"
            :content="message.content"
            :references="references"
            @activate="emit('activate', $event)"
          />
          <p v-else-if="loading && message.role === 'assistant'" class="whitespace-pre-wrap break-words">{{ t('playground.canvasAssistantThinking') }}</p>
          <p v-if="message.errorMessage" class="mt-2 text-xs text-red-500">{{ message.errorMessage }}</p>
        </div>
        <button v-if="message.role === 'assistant' && message.content" type="button" class="mt-1.5 inline-flex items-center gap-1 text-[11px] text-teal-700 opacity-0 transition-opacity group-hover:opacity-100 dark:text-teal-300" @click="emit('insert', message.id)"><Icon name="plus" size="xs" />{{ t('playground.canvasAssistantInsert') }}</button>
      </article>
    </div>

    <footer class="shrink-0 border-t border-gray-100 p-3 dark:border-dark-700">
      <select :value="model" class="select mb-2 h-8 w-full text-xs" :disabled="loading || !models.length" @change="emit('update:model', ($event.target as HTMLSelectElement).value)"><option v-if="!models.length" value="">{{ t('playground.noModels') }}</option><option v-for="item in models" :key="item.id" :value="item.id">{{ item.id }}</option></select>
      <div class="relative rounded-lg border border-gray-200 bg-white focus-within:border-teal-400 dark:border-dark-600 dark:bg-dark-950">
        <CanvasMentionEditor
          v-model="draft"
          :references="references"
          :placeholder="t('playground.canvasAssistantPlaceholder')"
          :aria-label="t('playground.canvasAssistantPlaceholder')"
          :disabled="loading"
          class="min-h-24"
          :on-submit="submit"
        />
        <button type="button" class="btn btn-icon absolute bottom-2 right-2 h-8 w-8 p-0" :class="loading ? 'btn-ghost text-red-600' : 'btn-primary'" :disabled="!loading && (!draft.trim() || !model)" :title="loading ? t('playground.canvasCancelGeneration') : t('playground.send')" @click="loading ? emit('cancel') : submit()"><Icon :name="loading ? 'x' : 'arrowUp'" size="xs" /><span class="sr-only">{{ loading ? t('playground.canvasCancelGeneration') : t('playground.send') }}</span></button>
      </div>
    </footer>
  </aside>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { PlaygroundModel } from '@/features/playground/types'
import type { CanvasAssistantMessage } from './types'
import CanvasMentionEditor from './CanvasMentionEditor.vue'
import CanvasMentionContent from './CanvasMentionContent.vue'
import type { CanvasMentionReference } from './canvasReferences'

const props = defineProps<{ messages: CanvasAssistantMessage[]; contextCount: number; models: PlaygroundModel[]; model: string; loading: boolean; references?: CanvasMentionReference[] }>()
const emit = defineEmits<{ 'update:model': [value: string]; send: [value: string]; cancel: []; insert: [id: string]; activate: [nodeId: string]; clear: []; close: [] }>()
const { t } = useI18n()
const draft = ref('')
const references = computed(() => props.references ?? [])
const messageList = ref<HTMLElement | null>(null)

function submit(): void {
  const value = draft.value.trim()
  if (!value || props.loading || !props.model) return
  draft.value = ''
  emit('send', value)
}

watch(() => props.messages.map((message) => `${message.id}:${message.content.length}`).join('|'), async () => {
  await nextTick()
  if (messageList.value) messageList.value.scrollTop = messageList.value.scrollHeight
})
</script>
