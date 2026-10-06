<template>
  <div class="space-y-4" data-test="logs-panel">
    <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.relay.logs.hint') }}</p>

    <div class="grid gap-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700 sm:grid-cols-3 lg:grid-cols-4">
      <div>
        <label class="input-label">{{ t('admin.relay.logs.node') }}</label>
        <Select v-model="nodeId" :options="nodeOptions" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.relay.logs.kind') }}</label>
        <Select v-model="filters.kind" :options="kindOptions" />
      </div>
      <div v-if="filters.kind !== 'moderation'">
        <label class="input-label">{{ t('admin.relay.logs.level') }}</label>
        <Select v-model="filters.level" :options="levelOptions" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.relay.logs.keyword') }}</label>
        <input v-model="filters.keyword" class="input" data-test="keyword" @keyup.enter="search" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.relay.logs.requestId') }}</label>
        <input v-model="filters.request_id" class="input" @keyup.enter="search" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.relay.logs.userId') }}</label>
        <input v-model.number="userId" type="number" min="1" class="input" @keyup.enter="search" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.relay.logs.apiKeyId') }}</label>
        <input v-model.number="apiKeyId" type="number" min="1" class="input" @keyup.enter="search" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.relay.logs.accountId') }}</label>
        <input v-model.number="accountId" type="number" min="1" class="input" @keyup.enter="search" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.relay.logs.platform') }}</label>
        <input v-model="filters.platform" class="input" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.relay.logs.model') }}</label>
        <input v-model="filters.model" class="input" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.relay.logs.from') }}</label>
        <input v-model="from" type="datetime-local" class="input" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.relay.logs.to') }}</label>
        <input v-model="to" type="datetime-local" class="input" />
      </div>
    </div>

    <div class="flex gap-2">
      <button type="button" class="btn btn-primary" :disabled="loading" data-test="search" @click="search">
        {{ t('common.search') }}
      </button>
      <button type="button" class="btn btn-secondary" @click="reset">{{ t('common.reset') }}</button>
    </div>

    <!-- per-node outcome: a node that is offline or timed out is shown, never silently dropped -->
    <ul v-if="result" class="flex flex-wrap gap-2 text-xs" data-test="node-outcomes">
      <li v-for="n in result.nodes" :key="n.node_id" class="rounded border px-2 py-1" :class="n.status === 'ok' ? 'border-gray-200 text-gray-600 dark:border-dark-600 dark:text-gray-300' : 'border-red-300 text-red-600'">
        {{ n.node_name || `#${n.node_id}` }}:
        {{ n.status === 'ok' ? t('admin.relay.logs.records', { count: n.records.length }) : n.error || t(`admin.relay.logs.nodeStatus.${n.status}`) }}
        <span v-if="n.truncated"> · {{ t('admin.relay.logs.truncated') }}</span>
        <span v-if="n.scan_limited"> · {{ t('admin.relay.logs.scanLimited') }}</span>
        <span v-if="n.skipped"> · {{ t('admin.relay.logs.skipped', { count: n.skipped }) }}</span>
      </li>
    </ul>

    <div v-if="result" class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
      <table class="min-w-full divide-y divide-gray-200 text-xs dark:divide-dark-700">
        <thead class="bg-gray-50 text-left text-gray-500 dark:bg-dark-800">
          <tr>
            <th class="px-2 py-2">{{ t('admin.relay.logs.time') }}</th>
            <th class="px-2 py-2">{{ t('admin.relay.logs.node') }}</th>
            <th class="px-2 py-2">{{ t('admin.relay.logs.level') }}</th>
            <th class="px-2 py-2">{{ t('admin.relay.logs.message') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-if="result.records.length === 0">
            <td colspan="4" class="px-2 py-6 text-center text-gray-500">{{ t('admin.relay.logs.empty') }}</td>
          </tr>
          <tr v-for="(r, i) in result.records" :key="i" class="align-top">
            <td class="whitespace-nowrap px-2 py-1">{{ r.ts ? formatTime(r.ts) : formatTime(field(r, 'created_at')) }}</td>
            <td class="whitespace-nowrap px-2 py-1">{{ r.node_name || `#${r.node_id}` }}</td>
            <td class="px-2 py-1">{{ field(r, 'level') || field(r, 'result') }}</td>
            <td class="px-2 py-1">
              <div>
                <span v-if="field(r, 'component')" class="text-gray-500">[{{ field(r, 'component') }}] </span>{{ field(r, 'message') || summary(r) }}
              </div>
              <div v-if="field(r, 'request_id')" class="text-gray-400">request_id={{ field(r, 'request_id') }}</div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="result && canLoadMore" class="flex justify-center">
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" data-test="load-more" @click="loadMore">
        {{ t('admin.relay.logs.loadMore') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { RelayLogFilters, RelayLogsResponse, RelayMergedLogRecord, RelayNode } from '@/api/admin/relay'
import Select from '@/components/common/Select.vue'
import { formatTime, relayErrorMessage } from './useRelayAction'
import { useAppStore } from '@/stores'

const props = defineProps<{ nodes: RelayNode[] }>()

const { t } = useI18n()
const appStore = useAppStore()

const PAGE = 50
const nodeId = ref<number>(0)
const userId = ref<number | null>(null)
const apiKeyId = ref<number | null>(null)
const accountId = ref<number | null>(null)
const from = ref('')
const to = ref('')
const filters = reactive<RelayLogFilters>({ kind: 'app', level: '' })
const loading = ref(false)
const result = ref<RelayLogsResponse | null>(null)
const lastBatch = ref(0)

const nodeOptions = computed(() => [
  { value: 0, label: t('admin.relay.logs.allNodes') },
  ...props.nodes
    .filter((n) => n.status === 'active' || n.status === 'draining')
    .map((n) => ({ value: n.id, label: n.name || n.hostname || `#${n.id}` }))
])
const kindOptions = computed(() => [
  { value: 'app', label: t('admin.relay.logs.kinds.app') },
  { value: 'error', label: t('admin.relay.logs.kinds.error') },
  { value: 'moderation', label: t('admin.relay.logs.kinds.moderation') }
])
const levelOptions = computed(() => [
  { value: '', label: t('admin.relay.logs.anyLevel') },
  ...['debug', 'info', 'warn', 'error'].map((l) => ({ value: l, label: l }))
])

// "Load more" is offered while the last batch came back full.
const canLoadMore = computed(() => lastBatch.value >= PAGE)

function ms(v: string): number | undefined {
  if (!v) return undefined
  const t0 = new Date(v).getTime()
  return Number.isNaN(t0) ? undefined : t0
}

function buildFilters(before?: number): RelayLogFilters {
  const f: RelayLogFilters = {
    ...filters,
    level: filters.level || undefined,
    from: ms(from.value),
    to: ms(to.value),
    user_id: userId.value || undefined,
    api_key_id: apiKeyId.value || undefined,
    account_id: accountId.value || undefined,
    limit: PAGE
  }
  if (before) f.before = before
  return f
}

async function fetchLogs(before?: number): Promise<RelayLogsResponse | null> {
  loading.value = true
  try {
    const f = buildFilters(before)
    return nodeId.value > 0
      ? await adminAPI.relay.nodeLogs(nodeId.value, f)
      : await adminAPI.relay.allNodeLogs(f)
  } catch (error) {
    appStore.showError(relayErrorMessage(error, t('common.unknownError')))
    return null
  } finally {
    loading.value = false
  }
}

async function search(): Promise<void> {
  const res = await fetchLogs()
  if (res) {
    result.value = res
    lastBatch.value = res.records.length
  }
}

async function loadMore(): Promise<void> {
  if (!result.value) return
  const last = [...result.value.records].reverse().find((r) => r.ts)
  if (!last?.ts) return
  const res = await fetchLogs(last.ts)
  if (res) {
    result.value = { nodes: res.nodes, records: [...result.value.records, ...res.records] }
    lastBatch.value = res.records.length
  }
}

function reset(): void {
  nodeId.value = 0
  userId.value = apiKeyId.value = accountId.value = null
  from.value = to.value = ''
  Object.assign(filters, { kind: 'app', level: '', keyword: '', request_id: '', platform: '', model: '' })
  result.value = null
  lastBatch.value = 0
}

function field(r: RelayMergedLogRecord, key: string): string {
  const v = r.record?.[key]
  return v === undefined || v === null ? '' : String(v)
}

/** Records without a message (moderation records) are shown as a compact key=value summary. */
function summary(r: RelayMergedLogRecord): string {
  return Object.entries(r.record ?? {})
    .filter(([k, v]) => k !== 'created_at' && typeof v !== 'object')
    .map(([k, v]) => `${k}=${v}`)
    .join(' ')
}
</script>
