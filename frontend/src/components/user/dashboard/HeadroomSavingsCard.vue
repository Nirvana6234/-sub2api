<template>
  <div class="card relative overflow-hidden border border-teal-200 p-4 dark:border-teal-800/50">
    <div v-if="loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/50 backdrop-blur-sm dark:bg-dark-800/50">
      <LoadingSpinner size="md" />
    </div>

    <!-- Always-on summary (today/cumulative, independent of the range picker below) -->
    <div class="flex items-center gap-3">
      <div class="rounded-lg bg-teal-100 p-2 dark:bg-teal-900/30">
        <Icon name="sync" size="md" class="text-teal-600 dark:text-teal-400" :stroke-width="2" />
      </div>
      <div>
        <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('dashboard.headroomSavings') }}</p>
        <p class="text-xl font-bold text-teal-600 dark:text-teal-400">
          {{ formatTokens(stats?.today_headroom_tokens_saved || 0) }} tokens
          <span class="text-sm font-normal text-gray-400 dark:text-gray-500">({{ formatPercent(todayHeadroomPercent) }})</span>
        </p>
        <p class="text-xs">
          <span class="text-gray-500 dark:text-gray-400">{{ t('common.total') }}: </span>
          <span class="text-teal-600 dark:text-teal-400">
            {{ formatTokens(stats?.headroom_tokens_saved || 0) }} tokens ({{ formatPercent(totalHeadroomPercent) }})
          </span>
          <span class="text-green-600 dark:text-green-400" :title="t('dashboard.actual')"> ≈ ${{ formatCost(stats?.headroom_savings_actual_usd || 0) }}</span>
          <span class="text-gray-400 dark:text-gray-500" :title="t('dashboard.standard')"> / ${{ formatCost(stats?.headroom_savings_usd || 0) }}</span>
        </p>
      </div>
    </div>

    <!-- Range + granularity controls, shared by the model distribution and trend panels below -->
    <div class="mt-4 flex flex-wrap items-center gap-4 border-t border-gray-100 pt-4 dark:border-dark-700">
      <div class="flex items-center gap-2">
        <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('dashboard.timeRange') }}:</span>
        <DateRangePicker :start-date="startDate" :end-date="endDate" @update:startDate="startDate = $event" @update:endDate="endDate = $event" @change="loadRangeData" />
      </div>
      <button @click="loadRangeData" :disabled="loading" class="btn btn-secondary">{{ t('common.refresh') }}</button>
      <div class="ml-auto flex items-center gap-2">
        <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('dashboard.granularity') }}:</span>
        <div class="w-28">
          <Select :model-value="granularity" :options="[{ value: 'day', label: t('dashboard.day') }, { value: 'hour', label: t('dashboard.hour') }]" @update:model-value="granularity = $event as 'day' | 'hour'; loadRangeData()" />
        </div>
      </div>
    </div>

    <div class="mt-4 grid grid-cols-1 gap-6 border-t border-gray-100 pt-4 dark:border-dark-700 lg:grid-cols-2">
      <!-- Model distribution (date-range scoped) -->
      <div>
        <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">{{ t('dashboard.headroomModelDistribution') }}</h3>
        <div v-if="models.length > 0" class="flex flex-col items-center gap-4 sm:flex-row sm:gap-6">
          <div class="h-48 w-48 shrink-0">
            <Doughnut :data="modelChartData" :options="doughnutOptions" />
          </div>
          <div class="max-h-48 w-full min-w-0 flex-1 overflow-auto">
            <table class="w-full text-xs">
              <thead>
                <tr class="text-gray-500 dark:text-gray-400">
                  <th class="pb-2 text-left">{{ t('dashboard.model') }}</th>
                  <th class="pb-2 text-right">{{ t('dashboard.requests') }}</th>
                  <th class="pb-2 text-right">{{ t('dashboard.headroomSavingsTokensColumn') }}</th>
                  <th class="pb-2 text-right">{{ t('dashboard.actual') }}</th>
                  <th class="pb-2 text-right">{{ t('dashboard.standard') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="model in models" :key="model.model" class="border-t border-gray-100 dark:border-dark-700">
                  <td class="max-w-[100px] truncate py-1.5 font-medium text-gray-900 dark:text-white" :title="model.model">{{ model.model }}</td>
                  <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">{{ formatNumber(model.requests) }}</td>
                  <td class="py-1.5 text-right text-teal-600 dark:text-teal-400">{{ formatTokens(model.tokens_saved) }}</td>
                  <td class="py-1.5 text-right text-green-600 dark:text-green-400">${{ formatCost(model.savings_actual_usd) }}</td>
                  <td class="py-1.5 text-right text-gray-400 dark:text-gray-500">${{ formatCost(model.savings_usd) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
        <div v-else class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400">
          {{ t('dashboard.noDataAvailable') }}
        </div>
      </div>

      <!-- Savings trend -->
      <div>
        <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">{{ t('dashboard.headroomSavingsTrend') }}</h3>
        <div v-if="trend.length > 0" class="h-48">
          <Line :data="trendChartData" :options="lineOptions" />
        </div>
        <div v-else class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400">
          {{ t('dashboard.noDataAvailable') }}
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Doughnut, Line } from 'vue-chartjs'
import {
  Chart as ChartJS,
  ArcElement,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import usageAPI, { type UserDashboardStats as UserStatsType, type HeadroomModelStat, type HeadroomTrendPoint } from '@/api/usage'
import { formatCostFixed as formatCost, formatNumberLocaleString as formatNumber, formatTokensK as formatTokens, formatDateLocalInput } from '@/utils/format'

ChartJS.register(ArcElement, CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend, Filler)

const props = defineProps<{ stats: UserStatsType }>()
const { t } = useI18n()

// 节省比例 = saved / (saved + actualTokens)，actualTokens 后端只统计被压缩命中的
// 请求（headroom_tokens_saved > 0），不是用户全部历史流量——用全部流量做分母会被
// 开压缩之前的历史用量稀释成没有意义的小数字（2026-09-09 生产反馈过这个问题），
// 只看命中过压缩的请求范围才是"压缩本身效果"的稳定指标。也不用 headroom 自己上报
// 的"压缩前原始 token 数"头——那个头对 OpenAI/Gemini 流量在响应头阶段只有粗估值，
// 经常是 0，会算出离谱的天文数字百分比（同样是 2026-09-09 生产遇到的问题）。
function headroomPercent(saved: number, actualTokens: number): number {
  const before = saved + actualTokens
  if (before <= 0) return 0
  return (saved / before) * 100
}
const todayHeadroomPercent = computed(() =>
  headroomPercent(props.stats?.today_headroom_tokens_saved || 0, props.stats?.today_headroom_actual_tokens || 0)
)
const totalHeadroomPercent = computed(() =>
  headroomPercent(props.stats?.headroom_tokens_saved || 0, props.stats?.headroom_actual_tokens || 0)
)
const formatPercent = (p: number) => `${p.toFixed(1)}%`

// Range + granularity controls default to the same last-7-days window as the
// existing usage charts, loaded independently so this section can be inspected
// without disturbing the rest of the dashboard's date range.
const startDate = ref(formatDateLocalInput(new Date(Date.now() - 6 * 86400000)))
const endDate = ref(formatDateLocalInput(new Date()))
const granularity = ref<'day' | 'hour'>('day')
const loading = ref(false)
const models = ref<HeadroomModelStat[]>([])
const trend = ref<HeadroomTrendPoint[]>([])

async function loadRangeData() {
  loading.value = true
  try {
    const [modelsRes, trendRes] = await Promise.all([
      usageAPI.getDashboardHeadroomModels({ start_date: startDate.value, end_date: endDate.value }),
      usageAPI.getDashboardHeadroomTrend({ start_date: startDate.value, end_date: endDate.value, granularity: granularity.value })
    ])
    models.value = modelsRes.models || []
    trend.value = trendRes.trend || []
  } catch (error) {
    console.error('Failed to load headroom range data:', error)
  } finally {
    loading.value = false
  }
}

onMounted(() => { loadRangeData() })

const modelChartData = computed(() => ({
  labels: models.value.map((m) => m.model),
  datasets: [{
    data: models.value.map((m) => m.tokens_saved),
    backgroundColor: ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899', '#06b6d4', '#84cc16']
  }]
}))

const doughnutOptions = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: { display: false },
    tooltip: {
      callbacks: {
        label: (context: any) => `${context.label}: ${formatTokens(context.parsed)} tokens`
      }
    }
  }
}

const isDarkMode = computed(() => document.documentElement.classList.contains('dark'))
const chartColors = computed(() => ({
  text: isDarkMode.value ? '#e5e7eb' : '#374151',
  grid: isDarkMode.value ? '#374151' : '#e5e7eb',
  saved: '#14b8a6'
}))

const trendChartData = computed(() => ({
  labels: trend.value.map((d) => d.date),
  datasets: [{
    label: t('dashboard.headroomSavingsTokensColumn'),
    data: trend.value.map((d) => d.tokens_saved),
    borderColor: chartColors.value.saved,
    backgroundColor: `${chartColors.value.saved}20`,
    fill: true,
    tension: 0.3
  }]
}))

const lineOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { intersect: false, mode: 'index' as const },
  plugins: {
    legend: { display: false },
    tooltip: {
      callbacks: {
        label: (context: any) => `${formatTokens(context.raw)} tokens`,
        footer: (tooltipItems: any) => {
          const dataIndex = tooltipItems[0]?.dataIndex
          const point = dataIndex !== undefined ? trend.value[dataIndex] : undefined
          if (!point) return ''
          return `${t('dashboard.actual')}: $${formatCost(point.savings_actual_usd)} / ${t('dashboard.standard')}: $${formatCost(point.savings_usd)}`
        }
      }
    }
  },
  scales: {
    x: { grid: { color: chartColors.value.grid }, ticks: { color: chartColors.value.text, font: { size: 10 } } },
    y: {
      grid: { color: chartColors.value.grid },
      ticks: { color: chartColors.value.text, font: { size: 10 }, callback: (value: string | number) => formatTokens(Number(value)) }
    }
  }
}))
</script>
