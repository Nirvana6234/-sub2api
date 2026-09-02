<template>
  <aside class="absolute inset-y-0 right-0 z-[75] flex w-[min(390px,calc(100%-1rem))] flex-col border-l border-gray-200 bg-white/98 shadow-2xl backdrop-blur dark:border-dark-700 dark:bg-dark-900/98" data-canvas-agent-dialog data-canvas-no-zoom>
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-gray-100 px-4 dark:border-dark-700">
      <span class="flex h-8 w-8 items-center justify-center rounded bg-teal-50 text-teal-700 dark:bg-teal-900/40 dark:text-teal-200"><Icon name="sparkles" size="sm" /></span>
      <div class="min-w-0 flex-1"><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasAgentTitle') }}</h2><p class="truncate text-[11px] text-gray-400">{{ statusLabel }}</p></div>
      <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('common.close')" @click="emit('close')"><Icon name="x" size="xs" /><span class="sr-only">{{ t('common.close') }}</span></button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto px-4 py-4">
      <div class="mb-4 flex items-center gap-2">
        <select class="select h-8 min-w-0 flex-1 text-xs" :value="activeThreadId" :disabled="busy || sending || !connected" :aria-label="t('playground.canvasAgentHistory')" @change="emit('resume', ($event.target as HTMLSelectElement).value)">
          <option value="">{{ t('playground.canvasAgentNewThread') }}</option>
          <option v-for="thread in threads" :key="thread.id" :value="thread.id">{{ thread.name || thread.preview || thread.id.slice(0, 12) }}</option>
        </select>
      </div>
      <div v-if="approval" class="mb-4 rounded border border-amber-300 bg-amber-50 p-3 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200">
        <p class="font-semibold">{{ t('playground.canvasAgentApprovalTitle') }}</p>
        <p class="mt-1 whitespace-pre-wrap break-words">{{ approval.reason || approval.method || t('playground.canvasAgentApprovalFallback') }}</p>
        <div class="mt-3 flex gap-2">
          <button type="button" class="btn btn-primary h-8 px-2.5 text-xs" @click="emit('approve', { requestId: approval.requestId, decision: 'accept' })">{{ t('playground.canvasAgentApprove') }}</button>
          <button type="button" class="btn btn-ghost h-8 px-2.5 text-xs" @click="emit('approve', { requestId: approval.requestId, decision: 'decline' })">{{ t('playground.canvasAgentDecline') }}</button>
        </div>
      </div>
      <details v-if="connected" class="mb-4 rounded border border-gray-200 dark:border-dark-700">
        <summary class="cursor-pointer px-3 py-2 text-xs font-semibold text-gray-700 dark:text-dark-200">{{ t('playground.canvasAgentSkills') }} ({{ skills.length }})</summary>
        <div class="max-h-48 space-y-2 overflow-y-auto border-t border-gray-100 px-3 py-2 dark:border-dark-700">
          <label v-for="skill in skills" :key="skill.name" class="flex items-start gap-2 text-xs text-gray-600 dark:text-dark-300">
            <input type="checkbox" class="mt-0.5" :checked="skill.enabled" :disabled="busy" @change="emit('toggle-skill', { name: skill.name, path: skill.path, enabled: ($event.target as HTMLInputElement).checked })">
            <span class="min-w-0"><span class="block truncate font-medium text-gray-800 dark:text-dark-100">{{ skill.name }}</span><span class="block line-clamp-2 text-[11px] text-gray-400">{{ skill.description }}</span></span>
          </label>
          <p v-if="!skills.length" class="py-2 text-xs text-gray-400">{{ t('playground.canvasAgentNoSkills') }}</p>
        </div>
      </details>
      <div v-if="messages.length" class="mb-5 space-y-3">
        <article v-for="message in messages" :key="message.id" class="rounded-lg px-3 py-2.5 text-sm leading-5" :class="message.role === 'user' ? 'ml-6 bg-teal-600 text-white' : 'mr-6 border border-gray-200 bg-gray-50 text-gray-800 dark:border-dark-700 dark:bg-dark-950 dark:text-gray-100'">
          <p class="whitespace-pre-wrap break-words">{{ message.text }}</p>
        </article>
      </div>
      <div class="space-y-4">
        <label class="block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasAgentEndpoint') }}
          <input v-model.trim="draftEndpoint" class="input mt-1.5 h-9 w-full text-xs" placeholder="http://127.0.0.1:17371" :disabled="connected || busy">
        </label>
        <label class="block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasAgentToken') }}
          <input v-model="draftToken" type="password" class="input mt-1.5 h-9 w-full text-xs" autocomplete="off" :disabled="connected || busy">
        </label>
        <p class="rounded border border-gray-200 bg-gray-50 p-3 text-xs leading-5 text-gray-500 dark:border-dark-700 dark:bg-dark-950 dark:text-dark-400">{{ t('playground.canvasAgentHint') }}</p>
        <p v-if="errorMessage" class="rounded border border-red-200 bg-red-50 p-3 text-xs leading-5 text-red-700 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-300">{{ errorMessage }}</p>
        <label class="block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasAgentPrompt') }}
          <textarea v-model="draft" rows="4" class="input mt-1.5 min-h-24 w-full resize-y text-sm" :placeholder="t('playground.canvasAgentPromptPlaceholder')" :disabled="!connected || sending"></textarea>
        </label>
      </div>
    </div>

    <footer class="flex shrink-0 gap-2 border-t border-gray-100 p-3 dark:border-dark-700">
      <button v-if="sending" type="button" class="btn btn-ghost gap-1.5" @click="emit('cancel')"><Icon name="x" size="sm" />{{ t('playground.canvasAgentStop') }}</button>
      <button v-else type="button" class="btn btn-primary flex-1 gap-1.5" :disabled="!connected || !draft.trim() || busy" @click="submitPrompt"><Icon name="arrowUp" size="sm" />{{ t('playground.canvasAgentSend') }}</button>
      <button v-if="connected" type="button" class="btn btn-ghost flex-1 gap-1.5" @click="emit('disconnect')"><Icon name="xCircle" size="sm" />{{ t('playground.canvasAgentDisconnect') }}</button>
      <button v-else type="button" class="btn btn-primary flex-1 gap-1.5" :disabled="busy || !draftEndpoint || !draftToken" @click="emit('connect', { endpoint: draftEndpoint, token: draftToken })"><Icon name="link" size="sm" />{{ busy ? t('playground.canvasAgentConnecting') : t('playground.canvasAgentConnect') }}</button>
      <button type="button" class="btn btn-ghost gap-1.5" :disabled="!connected || busy" @click="emit('sync')"><Icon name="refresh" size="sm" />{{ t('playground.canvasAgentSync') }}</button>
    </footer>
  </aside>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'

const props = defineProps<{ endpoint: string; token: string; connected: boolean; busy: boolean; sending: boolean; conversationStatus: string; activeThreadId: string; threads: Array<{ id: string; preview: string; name?: string | null }>; skills: Array<{ name: string; description: string; path: string; enabled: boolean }>; approval?: { requestId: string; reason?: string; method?: string } | null; messages: Array<{ id: string; role: 'user' | 'assistant' | 'system' | 'error'; text: string }>; errorMessage?: string }>()
const emit = defineEmits<{
  close: []
  disconnect: []
  sync: []
  connect: [value: { endpoint: string; token: string }]
  send: [value: string]
  cancel: []
  resume: [threadId: string]
  approve: [value: { requestId: string; decision: 'accept' | 'decline' | 'acceptForSession' }]
  'toggle-skill': [value: { name: string; path: string; enabled: boolean }]
}>()
const { t } = useI18n()
const draftEndpoint = ref(props.endpoint || 'http://127.0.0.1:17371')
const draftToken = ref(props.token)
const draft = ref('')
watch(() => props.endpoint, (value) => { if (value) draftEndpoint.value = value })
watch(() => props.token, (value) => { if (value) draftToken.value = value })
const statusLabel = computed(() => props.connected ? t('playground.canvasAgentConnected') : props.busy ? t('playground.canvasAgentConnecting') : t('playground.canvasAgentDisconnected'))
function submitPrompt(): void {
  const value = draft.value.trim()
  if (!value || props.sending || !props.connected) return
  draft.value = ''
  emit('send', value)
}
</script>
