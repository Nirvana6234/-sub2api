<template>
  <BaseDialog :show="show" :title="t('accountContributions.usageModalTitle')" width="extra-wide" @close="emit('close')">
    <div class="space-y-4">
      <div class="flex flex-wrap items-end justify-between gap-3">
        <div class="min-w-0">
          <div class="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{{ account?.name }}</div>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.usageModalHint') }}</p>
        </div>
        <div class="w-40">
          <Select v-model="period" :options="periodOptions" data-testid="contribution-usage-period" @change="reload" />
        </div>
      </div>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div class="rounded-lg border border-gray-200 px-4 py-3 dark:border-dark-600">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.usageRequests') }}</div>
          <div class="mt-1 text-lg font-semibold text-gray-900 dark:text-white" data-testid="contribution-usage-requests">{{ (stats?.total_requests ?? 0).toLocaleString() }}</div>
        </div>
        <div class="rounded-lg border border-gray-200 px-4 py-3 dark:border-dark-600">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.usageTokens') }}</div>
          <div class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">{{ (stats?.total_tokens ?? 0).toLocaleString() }}</div>
        </div>
        <div class="rounded-lg border border-gray-200 px-4 py-3 dark:border-dark-600" :title="t('usage.accountSource.ownCostHint')">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.usageModelCost') }}</div>
          <div class="mt-1 text-lg font-semibold text-gray-400 dark:text-gray-500">${{ (stats?.total_actual_cost ?? 0).toFixed(4) }}</div>
        </div>
      </div>

      <UsageTable
        flat
        :data="logs"
        :loading="loading"
        :columns="columns"
        :show-account-billing="false"
        :show-upstream-endpoint="false"
      />

      <Pagination
        v-if="total > 0"
        :page="page"
        :total="total"
        :page-size="pageSize"
        @update:page="handlePageChange"
        @update:pageSize="handlePageSizeChange"
      />
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import UsageTable from '@/components/admin/usage/UsageTable.vue'
import type { Column } from '@/components/common/types'
import { usageAPI } from '@/api'
import type { UsageLog, UsageStatsResponse } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

type Period = 'today' | '7d' | '30d'

const props = defineProps<{
  show: boolean
  account: { id: number; name: string } | null
}>()
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const appStore = useAppStore()

const period = ref<Period>('7d')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const logs = ref<UsageLog[]>([])
const stats = ref<UsageStatsResponse | null>(null)
const loading = ref(false)
let requestSeq = 0

const periodOptions = computed<SelectOption[]>(() => [
  { value: 'today', label: t('accountContributions.usagePeriodToday') },
  { value: '7d', label: t('accountContributions.usagePeriod7d') },
  { value: '30d', label: t('accountContributions.usagePeriod30d') },
])

const columns = computed<Column[]>(() => [
  { key: 'model', label: t('usage.model'), sortable: false },
  { key: 'reasoning_effort', label: t('usage.reasoningEffort'), sortable: false },
  { key: 'tokens', label: t('usage.tokens'), sortable: false },
  { key: 'cost', label: t('usage.cost'), sortable: false },
  { key: 'latency', label: t('usage.latency'), sortable: false },
  { key: 'created_at', label: t('usage.time'), sortable: false },
])

const formatLocalDate = (date: Date): string =>
  `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`

function dateRange(value: Period): { start_date: string; end_date: string } {
  const end = new Date()
  const start = new Date(end)
  if (value === '7d') start.setDate(start.getDate() - 6)
  if (value === '30d') start.setDate(start.getDate() - 29)
  return { start_date: formatLocalDate(start), end_date: formatLocalDate(end) }
}

// 后端在带 account_id 时强制按「自有账号 + 当前用户」过滤，这里只需传账号和日期。
async function load() {
  const account = props.account
  if (!account) return
  const seq = ++requestSeq
  loading.value = true
  const params = { account_id: account.id, ...dateRange(period.value) }
  try {
    const [list, summary] = await Promise.all([
      usageAPI.query({ ...params, page: page.value, page_size: pageSize.value, sort_by: 'created_at', sort_order: 'desc' }),
      usageAPI.getStats(params),
    ])
    if (seq !== requestSeq) return
    logs.value = list.items
    total.value = list.total
    stats.value = summary
  } catch (error) {
    if (seq !== requestSeq) return
    appStore.showError(extractApiErrorMessage(error, t('usage.failedToLoad')))
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

function reload() {
  page.value = 1
  void load()
}

function handlePageChange(value: number) {
  page.value = value
  void load()
}

function handlePageSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  void load()
}

watch(
  () => [props.show, props.account?.id] as const,
  ([show]) => {
    if (!show) return
    period.value = '7d'
    logs.value = []
    stats.value = null
    total.value = 0
    reload()
  },
  { immediate: true },
)
</script>
