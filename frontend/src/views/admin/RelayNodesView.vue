<template>
  <AppLayout>
    <div class="space-y-6">
      <section class="flex justify-end border-b border-gray-200 pb-3 dark:border-dark-700">
        <div class="flex flex-shrink-0 items-center gap-3">
          <span class="badge" :class="runtimeClass" data-test="runtime-state">{{ t(`admin.relay.runtime.${runtimeState}`) }}</span>
          <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
            {{ t('admin.relay.enabled') }}
            <Toggle :model-value="enabled" data-test="enabled-toggle" @update:model-value="requestToggle" />
          </label>
          <button type="button" class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="refreshAll">
            <Icon name="refresh" size="sm" />
            <span class="sr-only">{{ t('common.refresh') }}</span>
          </button>
        </div>
      </section>

      <div
        v-if="status?.runtime.reason"
        class="border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200"
        data-test="runtime-reason"
      >
        {{ status.runtime.reason }}
      </div>
      <div
        v-if="runtimeState === 'not_master'"
        class="border border-blue-200 bg-blue-50 px-4 py-3 text-sm text-blue-800 dark:border-blue-900/60 dark:bg-blue-950/30 dark:text-blue-200"
      >
        {{ t('admin.relay.notMasterHint') }}
      </div>

      <nav class="flex flex-wrap gap-1 border-b border-gray-200 dark:border-dark-700" role="tablist">
        <button
          v-for="tab in tabs"
          :key="tab"
          type="button"
          role="tab"
          :aria-selected="activeTab === tab"
          :data-test="`tab-${tab}`"
          class="-mb-px border-b-2 px-3 py-2 text-sm"
          :class="activeTab === tab ? 'border-primary-600 font-medium text-primary-700 dark:text-primary-300' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400'"
          @click="activeTab = tab"
        >
          {{ t(`admin.relay.tabs.${tab}`) }}
        </button>
      </nav>

      <div v-if="loading && !loaded" class="flex min-h-48 items-center justify-center text-sm text-gray-500">{{ t('common.loading') }}</div>
      <template v-else>
        <RelayNodesPanel
          v-if="activeTab === 'nodes'"
          :nodes="nodes"
          :healths="healths"
          :master-ratio="masterRatio"
          :key-summary="keySummary"
          :user-summary="userSummary"
          :step-up="stepUp"
          @refresh="refreshAll"
        />
        <RelayAssignmentPanel
          v-else-if="activeTab === 'assignment'"
          :nodes="nodes"
          :key-summary="keySummary"
          :user-summary="userSummary"
          :master-ratio="masterRatio"
          :step-up="stepUp"
          @refresh="refreshAll"
        />
        <RelayConfigPanel
          v-else-if="activeTab === 'config'"
          :config="generalConfig"
          :key-summary="keySummary"
          :user-summary="userSummary"
          :step-up="stepUp"
          @saved="onConfigSaved"
        />
        <RelayNotificationsPanel
          v-else-if="activeTab === 'notifications'"
          :config="notifications"
          :step-up="stepUp"
          @saved="(c) => (notifications = c)"
        />
        <RelayLogsPanel v-else-if="activeTab === 'logs'" :nodes="nodes" />
        <RelayKeysPanel v-else-if="activeTab === 'keys'" :step-up="stepUp" />
      </template>

      <TotpStepUpDialog :controller="stepUp" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type {
  RelayGeneralConfig,
  RelayKeyAssignmentSummary,
  RelayNode,
  RelayNodeHealth,
  RelayNotificationConfig,
  RelayStatus
} from '@/api/admin/relay'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import RelayNodesPanel from '@/components/admin/relay/RelayNodesPanel.vue'
import RelayAssignmentPanel from '@/components/admin/relay/RelayAssignmentPanel.vue'
import RelayConfigPanel from '@/components/admin/relay/RelayConfigPanel.vue'
import RelayNotificationsPanel from '@/components/admin/relay/RelayNotificationsPanel.vue'
import RelayLogsPanel from '@/components/admin/relay/RelayLogsPanel.vue'
import RelayKeysPanel from '@/components/admin/relay/RelayKeysPanel.vue'
import { useRelayAction } from '@/components/admin/relay/useRelayAction'
import { useStepUp } from '@/composables/useStepUp'

const tabs = ['nodes', 'assignment', 'config', 'notifications', 'logs', 'keys'] as const
type Tab = (typeof tabs)[number]

// Node health changes by the minute; the page refreshes itself while it is open.
const REFRESH_MS = 10_000

const { t } = useI18n()
const stepUp = useStepUp()
const action = useRelayAction(stepUp)

const activeTab = ref<Tab>('nodes')
const loading = ref(false)
const loaded = ref(false)
const status = ref<RelayStatus | null>(null)
const nodes = ref<RelayNode[]>([])
const healths = ref<RelayNodeHealth[]>([])
const generalConfig = ref<RelayGeneralConfig | null>(null)
const keySummary = ref<RelayKeyAssignmentSummary | null>(null)
const userSummary = ref<Record<string, number>>({})
const notifications = ref<RelayNotificationConfig | null>(null)
let timer: ReturnType<typeof setInterval> | null = null

const enabled = computed(() => status.value?.enabled ?? false)
const runtimeState = computed(() => status.value?.runtime.state ?? 'off')
const masterRatio = computed(() => generalConfig.value?.master_ratio_percent ?? 10)
const runtimeClass = computed(() => {
  switch (runtimeState.value) {
    case 'running':
      return 'badge-success'
    case 'failed':
      return 'badge-danger'
    case 'not_configured':
      return 'badge-warning'
    default:
      return 'badge-gray'
  }
})

/** Reads that only make sense while the runtime is running fail with 409 otherwise; that is not an error here. */
async function quiet<T>(fn: () => Promise<T>): Promise<T | undefined> {
  try {
    return await fn()
  } catch {
    return undefined
  }
}

async function refreshAll(): Promise<void> {
  loading.value = true
  try {
    const [st, ns, hs, cfg, ks, us, nt] = await Promise.all([
      action.load(() => adminAPI.relay.getStatus()),
      action.load(() => adminAPI.relay.listNodes()),
      quiet(() => adminAPI.relay.nodeHealths()),
      quiet(() => adminAPI.relay.getGeneralConfig()),
      quiet(() => adminAPI.relay.keyAssignmentSummary()),
      quiet(() => adminAPI.relay.userAssignmentSummary()),
      quiet(() => adminAPI.relay.getNotifications())
    ])
    if (st) status.value = st
    if (ns) nodes.value = ns
    healths.value = hs ?? []
    if (cfg) generalConfig.value = cfg
    keySummary.value = ks ?? null
    userSummary.value = us ?? {}
    if (nt) notifications.value = nt
    loaded.value = true
  } finally {
    loading.value = false
  }
}

/** The background tick only refreshes what changes by itself, and never while a dialog could be open on stale data. */
async function tick(): Promise<void> {
  if (loading.value || activeTab.value !== 'nodes') return
  const [ns, hs] = await Promise.all([quiet(() => adminAPI.relay.listNodes()), quiet(() => adminAPI.relay.nodeHealths())])
  if (ns) nodes.value = ns
  if (hs) healths.value = hs
}

function onConfigSaved(cfg: RelayGeneralConfig): void {
  generalConfig.value = cfg
}

async function requestToggle(on: boolean): Promise<void> {
  const res = await action.run(() => adminAPI.relay.setEnabled(on), t(on ? 'admin.relay.turnedOn' : 'admin.relay.turnedOff'))
  if (res) status.value = res
  // Refresh either way: a refused change (e.g. nodes still serving) must snap the toggle back.
  await refreshAll()
}

onMounted(() => {
  void refreshAll()
  timer = setInterval(() => void tick(), REFRESH_MS)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>
