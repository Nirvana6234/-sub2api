// 仪表盘指标数据来源。
//
// 从后端 /api/dashboard/metrics 获取实时指标，
// 从 /api/dashboard/trends 获取历史快照，两者组合后驱动统计卡片与趋势图。

import { ref } from 'vue'
import type {
  DashboardColorToken,
  DashboardMetricData,
  DashboardMetricKey,
  TrendPoint,
} from '../types/dashboard'
import {
  getDashboardMetrics,
  getDashboardTrends,
  type DashboardMetricsResponse,
  type DashboardTrendPoint,
  type DashboardTrendsResponse,
  type Money,
} from '../api/dashboardAdmin'

// USD 与 CNY 两套都构建：卡片区用 CNY 同币种对比（营收/成本/净利润），
// 资金区仍需 USD 原值展示站点余额。两者共用同一份趋势序列。
const METRIC_CONFIGS: { key: DashboardMetricKey; color: DashboardColorToken }[] = [
  { key: 'todayProfitUSD', color: 'primary' },
  { key: 'todayProfitCNY', color: 'primary' },
  { key: 'siteBalanceUSD', color: 'accent' },
  { key: 'siteBalanceCNY', color: 'accent' },
  { key: 'todayPurchaseCNY', color: 'warning' },
  { key: 'netProfitCNY', color: 'signal' },
  { key: 'upstreamBalanceCNY', color: 'primary' },
]

function dateLabel(dateStr: string): string {
  const d = new Date(dateStr)
  return `${d.getMonth() + 1}/${d.getDate()}`
}

function todayLabel(): string {
  const now = new Date()
  return `${now.getMonth() + 1}/${now.getDate()}`
}

function extractAmount(value: Money | number): number {
  return typeof value === 'number' ? value : value.amount
}

function buildMetricData(
  key: DashboardMetricKey,
  color: DashboardColorToken,
  live: DashboardMetricsResponse,
  trendPoints: DashboardTrendPoint[],
): DashboardMetricData {
  const current = extractAmount(live[key])
  const label = todayLabel()

  const monthPoints: TrendPoint[] = trendPoints.map((p) => ({
    label: dateLabel(p.date),
    value: extractAmount(p[key]),
  }))
  monthPoints.push({ label, value: current })

  const week = monthPoints.slice(-7)
  const month = monthPoints.slice(-30)

  return { key, color, current, series: { week, month } }
}

export function useDashboardMetrics() {
  const metrics = ref<DashboardMetricData[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  const fetchMetrics = async () => {
    loading.value = true
    error.value = null
    try {
      const [live, trends] = await Promise.all([
        getDashboardMetrics(),
        getDashboardTrends(30),
      ])

      metrics.value = METRIC_CONFIGS.map(({ key, color }) =>
        buildMetricData(key, color, live, trends.points),
      )
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'admin.dashboard.loadError'
    } finally {
      loading.value = false
    }
  }

  const applyRawData = (live: DashboardMetricsResponse, trends: DashboardTrendsResponse) => {
    metrics.value = METRIC_CONFIGS.map(({ key, color }) =>
      buildMetricData(key, color, live, trends.points),
    )
  }

  return { metrics, loading, error, fetchMetrics, applyRawData }
}
