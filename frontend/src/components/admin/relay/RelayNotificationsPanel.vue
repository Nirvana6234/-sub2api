<template>
  <div class="space-y-5" data-test="notifications-panel">
    <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.relay.notify.hint') }}</p>

    <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
      <div class="flex items-center justify-between">
        <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.relay.notify.feishu') }}</h3>
        <Toggle v-model="feishuEnabled" />
      </div>
      <div class="grid gap-3 sm:grid-cols-2">
        <div>
          <label class="input-label">{{ t('admin.relay.notify.webhook') }}</label>
          <input
            v-model="webhook"
            class="input"
            data-test="webhook"
            autocomplete="off"
            :placeholder="cfg?.webhook_configured ? t('admin.relay.notify.configured', { host: cfg.webhook_host || '' }) : 'https://open.feishu.cn/open-apis/bot/v2/hook/...'"
          />
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.relay.notify.secretKeptHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.notify.secret') }}</label>
          <input
            v-model="secret"
            type="password"
            class="input"
            data-test="secret"
            autocomplete="new-password"
            :placeholder="cfg?.secret_configured ? t('admin.relay.notify.secretConfigured') : ''"
          />
        </div>
      </div>
      <div class="flex flex-wrap gap-2">
        <button v-if="cfg?.webhook_configured" type="button" class="btn btn-secondary btn-sm" @click="clearWebhook">
          {{ t('admin.relay.notify.clearWebhook') }}
        </button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="testing" data-test="send-test" @click="sendTest">
          {{ t('admin.relay.notify.sendTest') }}
        </button>
      </div>
    </section>

    <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
      <div class="flex items-center justify-between">
        <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.relay.notify.email') }}</h3>
        <Toggle v-model="emailEnabled" />
      </div>
      <p class="text-xs text-gray-500">{{ t('admin.relay.notify.emailHint') }}</p>
      <div class="max-w-xs">
        <label class="input-label">{{ t('admin.relay.notify.mergeWindow') }}</label>
        <input v-model.number="mergeWindow" type="number" min="0" class="input" />
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.relay.notify.mergeWindowHint') }}</p>
      </div>
    </section>

    <section class="rounded-lg border border-gray-200 dark:border-dark-700">
      <h3 class="border-b border-gray-200 px-4 py-2 font-medium text-gray-900 dark:border-dark-700 dark:text-white">
        {{ t('admin.relay.notify.events') }}
      </h3>
      <table class="min-w-full text-sm">
        <thead class="text-left text-xs text-gray-500">
          <tr>
            <th class="px-4 py-2">{{ t('admin.relay.notify.event') }}</th>
            <th class="px-2 py-2">{{ t('admin.relay.notify.severity') }}</th>
            <th class="px-2 py-2">{{ t('admin.relay.notify.enabled') }}</th>
            <th class="px-2 py-2">{{ t('admin.relay.notify.feishuShort') }}</th>
            <th class="px-2 py-2">{{ t('admin.relay.notify.emailShort') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="e in events" :key="e.kind" :data-test="`event-${e.kind}`">
            <td class="px-4 py-2">{{ e.title }}</td>
            <td class="px-2 py-2">
              <span class="badge" :class="severityClass(e.severity)">{{ e.severity }}</span>
            </td>
            <td class="px-2 py-2"><input v-model="e.enabled" type="checkbox" :data-test="`enabled-${e.kind}`" /></td>
            <td class="px-2 py-2"><input v-model="e.feishu" type="checkbox" :disabled="!e.enabled" /></td>
            <td class="px-2 py-2"><input v-model="e.email" type="checkbox" :disabled="!e.enabled" /></td>
          </tr>
        </tbody>
      </table>
    </section>

    <div class="flex justify-end">
      <button type="button" class="btn btn-primary" :disabled="saving" data-test="save-notifications" @click="save">
        {{ t('common.save') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { RelayNotificationConfig, RelayNotificationEvent, RelayNotificationUpdate } from '@/api/admin/relay'
import Toggle from '@/components/common/Toggle.vue'
import type { StepUpController } from '@/composables/useStepUp'
import { useAppStore } from '@/stores'
import { useRelayAction } from './useRelayAction'

const props = defineProps<{ config: RelayNotificationConfig | null; stepUp: StepUpController }>()
const emit = defineEmits<{ (e: 'saved', cfg: RelayNotificationConfig): void }>()

const { t } = useI18n()
const appStore = useAppStore()
const action = useRelayAction(props.stepUp)

const cfg = ref<RelayNotificationConfig | null>(null)
const feishuEnabled = ref(false)
const emailEnabled = ref(true)
const mergeWindow = ref(15)
const webhook = ref('')
const secret = ref('')
const events = ref<RelayNotificationEvent[]>([])
const saving = ref(false)
const testing = ref(false)
// The server never sends the address or secret back; these say "the admin asked to clear it".
let clearWebhookRequested = false

watch(
  () => props.config,
  (c) => {
    cfg.value = c
    feishuEnabled.value = c?.feishu_enabled ?? false
    emailEnabled.value = c?.email_enabled ?? true
    mergeWindow.value = c?.merge_window_seconds ?? 15
    events.value = (c?.events ?? []).map((e) => ({ ...e }))
    webhook.value = ''
    secret.value = ''
    clearWebhookRequested = false
  },
  { immediate: true }
)

function severityClass(s: string): string {
  switch (s) {
    case 'emergency':
    case 'critical':
      return 'badge-danger'
    case 'warning':
      return 'badge-warning'
    default:
      return 'badge-gray'
  }
}

function clearWebhook(): void {
  clearWebhookRequested = true
  webhook.value = ''
  secret.value = ''
  appStore.showInfo(t('admin.relay.notify.clearPending'))
}

/** Only events whose setting differs from what the server returned are sent. */
function changedEvents(): RelayNotificationUpdate['events'] {
  const out: NonNullable<RelayNotificationUpdate['events']> = {}
  const before = new Map((props.config?.events ?? []).map((e) => [e.kind, e]))
  for (const e of events.value) {
    const b = before.get(e.kind)
    if (b && b.enabled === e.enabled && b.feishu === e.feishu && b.email === e.email) continue
    out[e.kind] = { enabled: e.enabled, feishu: e.feishu, email: e.email }
  }
  return out
}

async function save(): Promise<void> {
  const upd: RelayNotificationUpdate = {
    feishu_enabled: feishuEnabled.value,
    email_enabled: emailEnabled.value,
    merge_window_seconds: Math.max(0, Math.floor(mergeWindow.value || 0)),
    events: changedEvents()
  }
  if (clearWebhookRequested) {
    upd.webhook_url = ''
    upd.secret = ''
  }
  if (webhook.value.trim()) upd.webhook_url = webhook.value.trim()
  if (secret.value) upd.secret = secret.value
  saving.value = true
  try {
    const saved = await action.run(() => adminAPI.relay.updateNotifications(upd), t('common.saved'))
    if (saved) emit('saved', saved)
  } finally {
    saving.value = false
  }
}

async function sendTest(): Promise<void> {
  testing.value = true
  try {
    const res = await action.load(() => adminAPI.relay.testNotification())
    if (res?.sent) appStore.showSuccess(t('admin.relay.notify.testSent'))
  } finally {
    testing.value = false
  }
}
</script>
