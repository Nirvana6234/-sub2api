<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-6">
      <div>
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.latencyCompensation.title') }}</h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.latencyCompensation.description') }}</p>
      </div>

      <div class="card space-y-4 p-5">
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.latencyCompensation.from') }}</label>
            <input v-model="fromInput" type="datetime-local" class="input mt-1.5" />
          </div>
          <div>
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.latencyCompensation.to') }}</label>
            <input v-model="toInput" type="datetime-local" class="input mt-1.5" />
          </div>
        </div>

        <div class="grid gap-4 border-t border-gray-100 pt-4 sm:grid-cols-2 dark:border-dark-700">
          <div>
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.latencyCompensation.thresholdMs') }}
              <span class="text-xs font-normal text-gray-400">{{ t('admin.latencyCompensation.thresholdHint') }}</span>
            </label>
            <input v-model.number="thresholdMs" type="number" min="1" step="1000" class="input mt-1.5" />
          </div>
          <div>
            <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.latencyCompensation.profitRatio') }}
              <span class="text-xs font-normal text-gray-400">{{ t('admin.latencyCompensation.profitRatioHint') }}</span>
            </label>
            <div class="mt-1.5 flex items-center gap-2">
              <input v-model.number="profitRatioPercent" type="number" min="0" max="100" step="5" class="input" />
              <span class="text-sm text-gray-500 dark:text-gray-400">%</span>
            </div>
          </div>
        </div>

        <div class="flex flex-wrap items-center justify-end gap-2 border-t border-gray-100 pt-4 dark:border-dark-700">
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="savingSettings || !settingsDirty"
            @click="saveThresholdAndRatio"
          >
            {{ savingSettings ? t('common.loading') : t('admin.latencyCompensation.saveSettings') }}
          </button>
          <button type="button" class="btn btn-primary" :disabled="previewing" @click="runPreview">
            <Icon name="search" size="sm" class="mr-1" />
            {{ previewing ? t('common.loading') : t('admin.latencyCompensation.preview') }}
          </button>
        </div>
      </div>

      <div v-if="summary" class="card space-y-4 p-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="grid grid-cols-2 gap-x-8 gap-y-1 text-sm sm:grid-cols-4">
            <div>
              <p class="text-gray-500 dark:text-gray-400">{{ t('admin.latencyCompensation.slowRequests') }}</p>
              <p class="font-mono text-base font-semibold text-gray-900 dark:text-white">{{ summary.total_requests }}</p>
            </div>
            <div>
              <p class="text-gray-500 dark:text-gray-400">{{ t('admin.latencyCompensation.actualCost') }}</p>
              <p class="font-mono text-base font-semibold text-green-600 dark:text-green-400">${{ formatCost(summary.total_actual_cost) }}</p>
            </div>
            <div>
              <p class="text-gray-500 dark:text-gray-400">{{ t('admin.latencyCompensation.accountCost') }}</p>
              <p class="font-mono text-base font-semibold text-orange-500 dark:text-orange-400">${{ formatCost(summary.total_account_cost) }}</p>
            </div>
            <div>
              <p class="text-gray-500 dark:text-gray-400">{{ t('admin.latencyCompensation.totalCompensation') }}</p>
              <p class="font-mono text-lg font-bold text-primary-600 dark:text-primary-400">${{ formatCost(summary.total_compensation) }}</p>
            </div>
          </div>
          <button
            type="button"
            class="btn btn-primary shrink-0"
            :disabled="applying || summary.users.length === 0"
            @click="showApplyConfirm = true"
          >
            <Icon name="check" size="sm" class="mr-1" />
            {{ applying ? t('common.loading') : t('admin.latencyCompensation.apply') }}
          </button>
        </div>

        <p v-if="summary.users.length === 0" class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.latencyCompensation.noneQualified') }}
        </p>
        <div v-else class="max-h-96 overflow-auto">
          <table class="w-full text-sm">
            <thead class="sticky top-0 bg-white dark:bg-dark-900">
              <tr class="border-b border-gray-100 text-left text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">
                <th class="py-2 pr-3">{{ t('admin.latencyCompensation.user') }}</th>
                <th class="py-2 pr-3 text-right">{{ t('admin.latencyCompensation.requests') }}</th>
                <th class="py-2 pr-3 text-right">{{ t('admin.latencyCompensation.actualCost') }}</th>
                <th class="py-2 pr-3 text-right">{{ t('admin.latencyCompensation.accountCost') }}</th>
                <th class="py-2 text-right">{{ t('admin.latencyCompensation.compensation') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="u in summary.users"
                :key="u.user_id"
                class="border-b border-gray-50 dark:border-dark-800"
              >
                <td class="py-1.5 pr-3 text-gray-900 dark:text-white">{{ u.email || `#${u.user_id}` }}</td>
                <td class="py-1.5 pr-3 text-right text-gray-600 dark:text-gray-400">{{ u.requests }}</td>
                <td class="py-1.5 pr-3 text-right font-mono text-green-600 dark:text-green-400">${{ formatCost(u.actual_cost) }}</td>
                <td class="py-1.5 pr-3 text-right font-mono text-orange-500 dark:text-orange-400">${{ formatCost(u.account_cost) }}</td>
                <td class="py-1.5 text-right font-mono font-semibold text-primary-600 dark:text-primary-400">${{ formatCost(u.compensation) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <ConfirmDialog
      :show="showApplyConfirm"
      :title="t('admin.latencyCompensation.applyConfirmTitle')"
      :message="applyConfirmMessage"
      :confirm-text="t('admin.latencyCompensation.apply')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="runApply"
      @cancel="showApplyConfirm = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { LatencyCompensationSummary } from '@/api/admin/usage'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()

function formatCost(value: number): string {
  return (value ?? 0).toFixed(4)
}

// datetime-local has no timezone info; it's interpreted (and sent) in the
// browser's local time, matching how the admin reads timestamps everywhere
// else in this dashboard.
function toLocalInputValue(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const now = new Date()
const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate())
const fromInput = ref(toLocalInputValue(startOfToday))
const toInput = ref(toLocalInputValue(now))

const thresholdMs = ref(30000)
const savedThresholdMs = ref(30000)
// The backend stores a 0~1 fraction; the UI reads better as a percentage.
const profitRatioPercent = ref(100)
const savedProfitRatioPercent = ref(100)
const savingSettings = ref(false)

const previewing = ref(false)
const applying = ref(false)
const showApplyConfirm = ref(false)
const summary = ref<LatencyCompensationSummary | null>(null)

const settingsDirty = computed(
  () => thresholdMs.value !== savedThresholdMs.value || profitRatioPercent.value !== savedProfitRatioPercent.value
)

const applyConfirmMessage = computed(() =>
  t('admin.latencyCompensation.applyConfirmMessage', {
    amount: summary.value ? formatCost(summary.value.total_compensation) : '0',
    count: summary.value?.users.length ?? 0
  })
)

onMounted(async () => {
  try {
    const settings = await adminAPI.settings.getSettings()
    const ms = settings.latency_compensation_threshold_ms
    if (typeof ms === 'number' && ms > 0) {
      thresholdMs.value = ms
      savedThresholdMs.value = ms
    }
    const ratio = settings.latency_compensation_profit_ratio
    if (typeof ratio === 'number' && ratio >= 0 && ratio <= 1) {
      profitRatioPercent.value = Math.round(ratio * 100)
      savedProfitRatioPercent.value = profitRatioPercent.value
    }
  } catch {
    // Keep the built-in defaults (30s / 100%) if settings can't be loaded;
    // the page still works, it just starts from the fallback values.
  }
})

async function saveThresholdAndRatio() {
  if (!thresholdMs.value || thresholdMs.value <= 0) {
    appStore.showError(t('admin.latencyCompensation.invalidThreshold'))
    return
  }
  if (profitRatioPercent.value < 0 || profitRatioPercent.value > 100) {
    appStore.showError(t('admin.latencyCompensation.invalidRatio'))
    return
  }
  savingSettings.value = true
  try {
    await adminAPI.settings.updateSettings({
      latency_compensation_threshold_ms: thresholdMs.value,
      latency_compensation_profit_ratio: profitRatioPercent.value / 100
    })
    savedThresholdMs.value = thresholdMs.value
    savedProfitRatioPercent.value = profitRatioPercent.value
    appStore.showSuccess(t('common.saved'))
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.latencyCompensation.saveFailed'))
  } finally {
    savingSettings.value = false
  }
}

function windowParams(): { from: string; to: string; threshold_ms: number; profit_ratio: number } | null {
  const from = new Date(fromInput.value)
  const to = new Date(toInput.value)
  if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime()) || to <= from) {
    appStore.showError(t('admin.latencyCompensation.invalidRange'))
    return null
  }
  if (!thresholdMs.value || thresholdMs.value <= 0) {
    appStore.showError(t('admin.latencyCompensation.invalidThreshold'))
    return null
  }
  if (profitRatioPercent.value < 0 || profitRatioPercent.value > 100) {
    appStore.showError(t('admin.latencyCompensation.invalidRatio'))
    return null
  }
  return {
    from: from.toISOString(),
    to: to.toISOString(),
    threshold_ms: thresholdMs.value,
    profit_ratio: profitRatioPercent.value / 100
  }
}

async function runPreview() {
  const params = windowParams()
  if (!params) return
  previewing.value = true
  try {
    summary.value = await adminAPI.usage.previewLatencyCompensation(params)
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.latencyCompensation.previewFailed'))
  } finally {
    previewing.value = false
  }
}

async function runApply() {
  showApplyConfirm.value = false
  const params = windowParams()
  if (!params) return
  applying.value = true
  try {
    summary.value = await adminAPI.usage.applyLatencyCompensation(params)
    appStore.showSuccess(
      t('admin.latencyCompensation.applySuccess', {
        amount: formatCost(summary.value.total_compensation),
        count: summary.value.users.length
      })
    )
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.latencyCompensation.applyFailed'))
  } finally {
    applying.value = false
  }
}
</script>
