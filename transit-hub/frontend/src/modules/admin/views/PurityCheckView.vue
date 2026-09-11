<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useDocumentVisibility, useIntervalFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import {
  AlertTriangle,
  CheckCircle2,
  FlaskConical,
  Loader2,
  RefreshCw,
  Search,
  ShieldAlert,
  Trash2,
  X,
  XCircle,
} from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import {
  cancelPurityJob,
  deletePurityJob,
  getPurityJob,
  listPurityJobs,
  listPurityTargets,
  listPurityTiers,
  submitPurityJobs,
} from '../api/purityCheck'
import {
  PURITY_CLAIMED_MODELS,
  type PurityJob,
  type PurityTarget,
  type PurityTier,
  type PurityTierInfo,
} from '../types/purityCheck'

const { t } = useI18n()

const targets = ref<PurityTarget[]>([])
const tiers = ref<PurityTierInfo[]>([])
const jobs = ref<PurityJob[]>([])
const jobsTotal = ref(0)
const queuedTotal = ref(0)
const detectorAvailable = ref(true)
const maxRetained = ref(0)
const deletingId = ref('')

const isLoadingTargets = ref(false)
const isLoadingJobs = ref(false)
const isLoadingMoreJobs = ref(false)
const isSubmitting = ref(false)
const errorKey = ref('')
const submitSummary = ref('')

const selectedIds = ref<string[]>([])
const targetKeyword = ref('')
const historySearch = ref('')
const historyStatus = ref('')
const selectedTier = ref<PurityTier>('low')
const claimedModel = ref<string>(PURITY_CLAIMED_MODELS[0])
// 中转常给模型起别名，实际写进 HTTP 请求的名字可能跟申报型号不同。留空即跟随申报。
const requestModel = ref('')

const confirmOpen = ref(false)
const reportJobId = ref('')
const reportPayload = ref<Record<string, unknown> | null>(null)
const isLoadingReport = ref(false)

const eligibleTargets = computed(() => targets.value.filter((target) => target.eligible))
const selectedCount = computed(() => selectedIds.value.length)

// 账号名普遍是「https://xxx-备注」这种长串，几十个堆在一列里靠肉眼找不现实。
// 按名称/地址/平台/类型模糊匹配，不区分大小写。
const filteredTargets = computed(() => {
  const keyword = targetKeyword.value.trim().toLowerCase()
  if (!keyword) return targets.value
  return targets.value.filter((target) =>
    [target.name, target.accountId, target.baseUrl, target.platform, target.type].some((field) =>
      (field || '').toLowerCase().includes(keyword),
    ),
  )
})

const filteredEligibleTargets = computed(() => filteredTargets.value.filter((target) => target.eligible))

// 「全选」只作用于当前筛选结果：搜完再点，用户要的是「选中我看到的这些」，
// 而不是把列表外那几十个也一起提交上去真金白银地跑一遍。
const allVisibleSelected = computed(
  () =>
    filteredEligibleTargets.value.length > 0 &&
    filteredEligibleTargets.value.every((target) => selectedIds.value.includes(target.accountId)),
)

const currentTierInfo = computed(() =>
  tiers.value.find((tier) => tier.tier === selectedTier.value) ?? null,
)

/**
 * 一次提交的总消耗。串行队列下这些请求会一个接一个真实打到上游，
 * 花的是我们自己的钱，所以确认框里要按「最坏情况」报数。
 */
const estimatedTotals = computed(() => {
  const info = currentTierInfo.value
  if (!info) return null
  return {
    logical: info.totalRequests * selectedCount.value,
    maxHttp: info.maxHttpRequests * selectedCount.value,
    inputTokens: info.approximateInputTokens * selectedCount.value,
  }
})

const runningJob = computed(() => jobs.value.find((job) => job.status === 'running') ?? null)
const hasMoreJobs = computed(() => jobs.value.length < jobsTotal.value)
const jobPageSize = 100

const readableMessage = (key: string): string => (key ? t(key) : '')

const formatNumber = (value: number): string => value.toLocaleString('zh-CN')

const reasonLabel = (reason: string): string =>
  reason ? t(`admin.purityCheck.targetReasons.${reason}`) : ''

/** 0.0550 → "0.055"，1.0000 → "1"。倍率是拿来对账的，别显示成一串没用的零。 */
const formatRate = (value: number): string => String(Number(value.toFixed(4)))

/**
 * 成本倍率标签。null 一律显示「未声明」而不是 1x——账号 rate_multiplier 列
 * 常年停在建表默认值，把「没人声明过」显示成「原价」会让人按错的数字算账。
 */
const rateLabel = (target: PurityTarget): string =>
  target.costRateMultiplier === null || target.costRateMultiplier === undefined
    ? t('admin.purityCheck.rateUndeclared')
    : `${formatRate(target.costRateMultiplier)}x`

const rateSourceLabel = (target: PurityTarget): string => {
  const source = target.costRateSource
  if (!source || source === 'none') return ''
  if (target.costRateMultiplier === null || target.costRateMultiplier === undefined) return ''
  return t(`admin.purityCheck.rateSources.${source}`)
}

// column 来源 = 只有 sub2api 的 rate_multiplier 列值，没有手工值也没有新鲜探测值，
// 可信度最低，标成琥珀色提醒别直接拿去对账。
const rateTone = (target: PurityTarget): string => {
  if (target.costRateMultiplier === null || target.costRateMultiplier === undefined) {
    return 'text-muted-foreground'
  }
  if (target.costRateSource === 'column') return 'text-amber-600 dark:text-amber-400'
  return 'text-foreground'
}

const toggleTarget = (target: PurityTarget) => {
  if (!target.eligible) return
  const index = selectedIds.value.indexOf(target.accountId)
  if (index >= 0) selectedIds.value.splice(index, 1)
  else selectedIds.value.push(target.accountId)
}

const toggleAll = () => {
  const visibleIds = filteredEligibleTargets.value.map((target) => target.accountId)
  if (visibleIds.length === 0) return
  if (allVisibleSelected.value) {
    // 只取消当前可见的，筛选前选中的其它账号保留。
    selectedIds.value = selectedIds.value.filter((id) => !visibleIds.includes(id))
    return
  }
  const merged = new Set(selectedIds.value)
  visibleIds.forEach((id) => merged.add(id))
  selectedIds.value = Array.from(merged)
}

const loadTargets = async () => {
  isLoadingTargets.value = true
  try {
    targets.value = await listPurityTargets()
    // 账号可能被删或改了平台，清掉已经选不中的。
    const alive = new Set(eligibleTargets.value.map((target) => target.accountId))
    selectedIds.value = selectedIds.value.filter((id) => alive.has(id))
  } catch (error) {
    errorKey.value = error instanceof Error ? error.message : 'admin.purityCheck.errors.request'
    // 失败原因显示在页面顶部；继续把确认框留在前景会遮住错误，看起来像点击无效。
    confirmOpen.value = false
  } finally {
    isLoadingTargets.value = false
  }
}

const loadTiers = async () => {
  try {
    tiers.value = await listPurityTiers()
  } catch {
    // 档位取不到通常意味着检测器没部署，jobs 接口的 detectorAvailable 会说明情况，
    // 这里不额外弹错误盖住页面。
    tiers.value = []
  }
}

const loadJobs = async (options: { silent?: boolean; append?: boolean } = {}) => {
  if (!options.silent) isLoadingJobs.value = true
  try {
    const response = await listPurityJobs({
      limit: jobPageSize,
      offset: options.append ? jobs.value.length : 0,
      search: historySearch.value,
      status: historyStatus.value,
    })
    jobs.value = options.append ? [...jobs.value, ...response.jobs] : response.jobs
    jobsTotal.value = response.total
    queuedTotal.value = response.queuedTotal
    detectorAvailable.value = response.detectorAvailable
    maxRetained.value = response.maxRetained
  } catch (error) {
    if (!options.silent) {
      errorKey.value = error instanceof Error ? error.message : 'admin.purityCheck.errors.request'
    }
  } finally {
    isLoadingJobs.value = false
  }
}

const searchHistory = async () => {
  errorKey.value = ''
  await loadJobs()
}

const loadMoreJobs = async () => {
  if (isLoadingMoreJobs.value || !hasMoreJobs.value) return
  isLoadingMoreJobs.value = true
  try {
    await loadJobs({ silent: true, append: true })
  } finally {
    isLoadingMoreJobs.value = false
  }
}

const refresh = async () => {
  errorKey.value = ''
  await Promise.all([loadTargets(), loadTiers(), loadJobs()])
}

const openConfirm = () => {
  errorKey.value = ''
  submitSummary.value = ''
  if (selectedCount.value === 0) return
  confirmOpen.value = true
}

const confirmSubmit = async () => {
  isSubmitting.value = true
  try {
    const result = await submitPurityJobs({
      accountIds: selectedIds.value,
      tier: selectedTier.value,
      claimedModel: claimedModel.value,
      requestModel: requestModel.value.trim() || claimedModel.value,
    })
    confirmOpen.value = false
    selectedIds.value = []
    if (result.skippedTargetCount > 0) {
      submitSummary.value = t('admin.purityCheck.submitSkippedSummary', {
        queued: result.jobs.length,
        skipped: result.skippedTargetCount,
      })
    }
    await loadJobs()
  } catch (error) {
    errorKey.value = error instanceof Error ? error.message : 'admin.purityCheck.errors.request'
    submitSummary.value = ''
  } finally {
    isSubmitting.value = false
  }
}

const cancelJob = async (job: PurityJob) => {
  try {
    await cancelPurityJob(job.id)
    await loadJobs()
  } catch (error) {
    errorKey.value = error instanceof Error ? error.message : 'admin.purityCheck.errors.request'
  }
}

const deleteJob = async (job: PurityJob) => {
  if (!window.confirm(t('admin.purityCheck.deleteConfirm', { name: job.accountName || job.accountId }))) {
    return
  }
  deletingId.value = job.id
  try {
    await deletePurityJob(job.id)
    if (reportJobId.value === job.id) closeReport()
    await loadJobs()
  } catch (error) {
    errorKey.value = error instanceof Error ? error.message : 'admin.purityCheck.errors.request'
  } finally {
    deletingId.value = ''
  }
}

/** 只有终态任务能删；排队中/运行中的要先取消，后端也会拒绝。 */
const isDeletable = (job: PurityJob): boolean =>
  job.status === 'succeeded' || job.status === 'failed' || job.status === 'cancelled'

const openReport = async (job: PurityJob) => {
  reportJobId.value = job.id
  reportPayload.value = null
  isLoadingReport.value = true
  try {
    const detail = await getPurityJob(job.id)
    reportPayload.value = detail.reportPayload ?? null
  } catch (error) {
    errorKey.value = error instanceof Error ? error.message : 'admin.purityCheck.errors.request'
    reportJobId.value = ''
  } finally {
    isLoadingReport.value = false
  }
}

const closeReport = () => {
  reportJobId.value = ''
  reportPayload.value = null
}

/** 报告里的「各模型匹配度」。检测器给的是 0-1 的相对接近程度，不是路由概率。 */
const reportMatches = computed(() => {
  const raw = reportPayload.value?.possible_models
  if (!Array.isArray(raw)) return []
  return raw.map((item) => {
    const entry = item as Record<string, unknown>
    return {
      label: String(entry.label_cn ?? entry.model ?? ''),
      match: typeof entry.match === 'number' ? entry.match : 0,
      threshold: typeof entry.threshold === 'number' ? entry.threshold : 0,
    }
  })
})

const reportField = (key: string): string => {
  const value = reportPayload.value?.[key]
  return typeof value === 'string' ? value : ''
}

const reportIsOfficial = computed(() => reportPayload.value?.official === true)
const reportMismatch = computed(() => reportPayload.value?.fingerprint_claim_mismatch === true)

const statusTone = (status: string): string => {
  switch (status) {
    case 'succeeded':
      return 'text-emerald-600 dark:text-emerald-400'
    case 'failed':
      return 'text-destructive'
    case 'running':
      return 'text-primary'
    case 'cancelled':
      return 'text-muted-foreground'
    default:
      return 'text-amber-600 dark:text-amber-400'
  }
}

// 有任务在跑或在排队时才轮询，页面切到后台就停——检测一次动辄几分钟到几小时，
// 让一个看不见的页面一直打接口没意义。
const visibility = useDocumentVisibility()
useIntervalFn(() => {
  if (visibility.value !== 'visible') return
  if (!jobs.value.some((job) => job.status === 'running' || job.status === 'queued')) return
  void loadJobs({ silent: true })
}, 4000)

onMounted(() => {
  void refresh()
})
</script>

<template>
  <div class="space-y-5">
    <header class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
      <div class="min-w-0">
        <h1 class="text-xl font-semibold text-foreground">{{ t('admin.purityCheck.title') }}</h1>
        <p class="mt-1 max-w-3xl text-sm leading-6 text-muted-foreground">
          {{ t('admin.purityCheck.subtitle') }}
        </p>
      </div>
      <Button variant="secondary" size="sm" :disabled="isLoadingJobs" @click="refresh">
        <Loader2 v-if="isLoadingJobs" class="h-4 w-4 animate-spin" />
        <RefreshCw v-else class="h-4 w-4" />
        {{ t('admin.purityCheck.refresh') }}
      </Button>
    </header>

    <!-- 检测器是旁路服务，没部署时整页功能不可用，要说清楚而不是让接口默默超时。 -->
    <p
      v-if="!detectorAvailable"
      class="flex items-start gap-2 rounded-lg bg-destructive/10 px-4 py-3 text-sm text-destructive"
    >
      <ShieldAlert class="mt-0.5 h-4 w-4 shrink-0" />
      <span>{{ t('admin.purityCheck.detectorUnavailable') }}</span>
    </p>

    <p v-if="errorKey" class="rounded-lg bg-destructive/10 px-4 py-3 text-sm text-destructive">
      {{ readableMessage(errorKey) }}
    </p>

    <p v-if="submitSummary" class="rounded-lg bg-emerald-500/10 px-4 py-3 text-sm text-emerald-700 dark:text-emerald-300">
      {{ submitSummary }}
    </p>

    <!-- 结论边界：检测器 README 明确写了「不通过不等于中转主动掺水」，原样传达。 -->
    <p class="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-300">
      <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" />
      <span>{{ t('admin.purityCheck.disclaimer') }}</span>
    </p>

    <div class="grid gap-5 xl:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
      <!-- 左：选目标 + 选档位 -->
      <section class="overflow-hidden rounded-lg border border-border/60 bg-card">
        <div class="flex items-center justify-between border-b border-border/50 px-4 py-3">
          <h2 class="text-sm font-medium text-foreground">{{ t('admin.purityCheck.targetsTitle') }}</h2>
          <button
            type="button"
            class="text-xs text-primary hover:underline disabled:opacity-50"
            :disabled="filteredEligibleTargets.length === 0"
            @click="toggleAll"
          >
            {{ allVisibleSelected ? t('admin.purityCheck.clearAll') : t('admin.purityCheck.selectAll') }}
          </button>
        </div>

        <div class="border-b border-border/50 px-4 py-2.5">
          <div class="relative">
            <Search class="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              v-model="targetKeyword"
              type="search"
              :placeholder="t('admin.purityCheck.targetsSearchPlaceholder')"
              class="w-full rounded-lg border border-border/60 bg-background py-1.5 pl-8 pr-8 text-sm text-foreground"
            />
            <button
              v-if="targetKeyword"
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              @click="targetKeyword = ''"
            >
              <X class="h-3.5 w-3.5" />
            </button>
          </div>
          <p v-if="targetKeyword" class="mt-1.5 text-xs text-muted-foreground">
            {{ t('admin.purityCheck.targetsSearchCount', {
              shown: filteredTargets.length,
              total: targets.length,
              selected: selectedCount,
            }) }}
          </p>
        </div>

        <div v-if="isLoadingTargets" class="space-y-2 p-4">
          <div v-for="index in 5" :key="index" class="h-12 animate-pulse rounded-lg bg-surface/70" />
        </div>
        <p v-else-if="targets.length === 0" class="px-4 py-6 text-sm text-muted-foreground">
          {{ t('admin.purityCheck.targetsEmpty') }}
        </p>
        <p v-else-if="filteredTargets.length === 0" class="px-4 py-6 text-sm text-muted-foreground">
          {{ t('admin.purityCheck.targetsSearchEmpty') }}
        </p>
        <ul v-else class="max-h-[24rem] divide-y divide-border/40 overflow-y-auto">
          <li
            v-for="target in filteredTargets"
            :key="target.accountId"
            class="px-4 py-2.5"
            :class="target.eligible ? 'cursor-pointer hover:bg-surface/60' : 'opacity-50'"
            @click="toggleTarget(target)"
          >
            <div class="flex items-start gap-2.5">
              <input
                type="checkbox"
                class="mt-1 h-4 w-4 shrink-0 accent-primary"
                :checked="selectedIds.includes(target.accountId)"
                :disabled="!target.eligible"
                @click.stop
                @change="toggleTarget(target)"
              />
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm text-foreground">{{ target.name || target.accountId }}</p>
                <p class="flex flex-wrap items-center gap-x-1.5 text-xs text-muted-foreground">
                  <span class="truncate">{{ target.platform || '-' }} · {{ target.type || '-' }}</span>
                  <span class="tabular-nums" :class="rateTone(target)">
                    · {{ rateLabel(target) }}
                  </span>
                  <span v-if="rateSourceLabel(target)">{{ rateSourceLabel(target) }}</span>
                </p>
                <p v-if="!target.eligible" class="mt-0.5 text-xs text-amber-600 dark:text-amber-400">
                  {{ reasonLabel(target.reason) }}
                </p>
              </div>
            </div>
          </li>
        </ul>

        <div class="space-y-3 border-t border-border/50 p-4">
          <div>
            <label class="text-xs font-medium text-muted-foreground">{{ t('admin.purityCheck.tierLabel') }}</label>
            <div class="mt-1.5 space-y-1.5">
              <label
                v-for="tier in tiers"
                :key="tier.tier"
                class="flex cursor-pointer items-start gap-2 rounded-lg border border-border/50 px-3 py-2 text-sm hover:bg-surface/60"
                :class="selectedTier === tier.tier ? 'border-primary bg-primary/5' : ''"
              >
                <input v-model="selectedTier" type="radio" :value="tier.tier" class="mt-1 h-3.5 w-3.5 accent-primary" />
                <span class="min-w-0">
                  <span class="text-foreground">{{ t(`admin.purityCheck.tiers.${tier.tier}`) }}</span>
                  <span class="mt-0.5 block text-xs text-muted-foreground">
                    {{ t('admin.purityCheck.tierCost', {
                      logical: formatNumber(tier.totalRequests),
                      http: formatNumber(tier.maxHttpRequests),
                      tokens: formatNumber(tier.approximateInputTokens),
                    }) }}
                  </span>
                </span>
              </label>
              <p v-if="tiers.length === 0" class="text-xs text-muted-foreground">
                {{ t('admin.purityCheck.tiersUnavailable') }}
              </p>
            </div>
          </div>

          <div>
            <label class="text-xs font-medium text-muted-foreground">{{ t('admin.purityCheck.claimedModelLabel') }}</label>
            <select
              v-model="claimedModel"
              class="mt-1.5 w-full rounded-lg border border-border/60 bg-background px-3 py-2 text-sm text-foreground"
            >
              <option v-for="model in PURITY_CLAIMED_MODELS" :key="model" :value="model">{{ model }}</option>
            </select>
          </div>

          <div>
            <label class="text-xs font-medium text-muted-foreground">{{ t('admin.purityCheck.requestModelLabel') }}</label>
            <input
              v-model="requestModel"
              type="text"
              :placeholder="claimedModel"
              class="mt-1.5 w-full rounded-lg border border-border/60 bg-background px-3 py-2 text-sm text-foreground"
            />
            <p class="mt-1 text-xs text-muted-foreground">{{ t('admin.purityCheck.requestModelHint') }}</p>
          </div>

          <Button
            class="w-full"
            size="sm"
            :disabled="selectedCount === 0 || !detectorAvailable || tiers.length === 0"
            @click="openConfirm"
          >
            <FlaskConical class="h-4 w-4" />
            {{ t('admin.purityCheck.submit', { count: selectedCount }) }}
          </Button>
        </div>
      </section>

      <!-- 右：任务与报告 -->
      <section class="overflow-hidden rounded-lg border border-border/60 bg-card">
        <div class="border-b border-border/50 px-4 py-3">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 class="text-sm font-medium text-foreground">{{ t('admin.purityCheck.jobsTitle') }}</h2>
            <p class="text-xs text-muted-foreground">
              {{ t('admin.purityCheck.queueSummary', { queued: queuedTotal }) }}
              <span v-if="maxRetained > 0"> · {{ t('admin.purityCheck.retentionHint', { max: maxRetained }) }}</span>
            </p>
          </div>
          <form class="mt-3 flex flex-col gap-2 sm:flex-row" @submit.prevent="searchHistory">
            <div class="relative min-w-0 flex-1">
              <Search class="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                v-model="historySearch"
                type="search"
                :placeholder="t('admin.purityCheck.historySearchPlaceholder')"
                class="w-full rounded-lg border border-border/60 bg-background py-1.5 pl-8 pr-3 text-sm text-foreground"
              />
            </div>
            <select
              v-model="historyStatus"
              class="h-8 rounded-lg border border-border/60 bg-background px-2 text-sm text-foreground"
              :aria-label="t('admin.purityCheck.historyStatusLabel')"
            >
              <option value="">{{ t('admin.purityCheck.historyAllStatuses') }}</option>
              <option value="queued">{{ t('admin.purityCheck.statuses.queued') }}</option>
              <option value="running">{{ t('admin.purityCheck.statuses.running') }}</option>
              <option value="succeeded">{{ t('admin.purityCheck.statuses.succeeded') }}</option>
              <option value="failed">{{ t('admin.purityCheck.statuses.failed') }}</option>
              <option value="cancelled">{{ t('admin.purityCheck.statuses.cancelled') }}</option>
            </select>
            <Button type="submit" variant="secondary" size="sm" :disabled="isLoadingJobs">
              <Loader2 v-if="isLoadingJobs" class="h-4 w-4 animate-spin" />
              <Search v-else class="h-4 w-4" />
              {{ t('admin.purityCheck.historySearch') }}
            </Button>
          </form>
          <p v-if="jobsTotal > 0" class="mt-2 text-xs text-muted-foreground">
            {{ t('admin.purityCheck.historyCount', { shown: jobs.length, total: jobsTotal }) }}
          </p>
        </div>

        <!-- 正在跑的那一个单独提出来展示进度：串行队列下它就是全局的「当前进度」。 -->
        <div v-if="runningJob" class="border-b border-border/50 bg-primary/5 px-4 py-3">
          <div class="flex items-center justify-between gap-2">
            <p class="min-w-0 truncate text-sm text-foreground">
              <Loader2 class="mr-1 inline h-3.5 w-3.5 animate-spin" />
              {{ runningJob.accountName || runningJob.accountId }}
            </p>
            <span class="shrink-0 text-xs tabular-nums text-muted-foreground">
              {{ runningJob.completedRequests }} / {{ runningJob.plannedRequests || '?' }}
            </span>
          </div>
          <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-border/50">
            <div
              class="h-full rounded-full bg-primary transition-all"
              :style="{ width: runningJob.plannedRequests > 0
                ? `${Math.min(100, (runningJob.completedRequests / runningJob.plannedRequests) * 100)}%`
                : '0%' }"
            />
          </div>
        </div>

        <p v-if="jobs.length === 0" class="px-4 py-8 text-center text-sm text-muted-foreground">
          {{ t('admin.purityCheck.jobsEmpty') }}
        </p>
        <ul v-else class="divide-y divide-border/40">
          <li v-for="job in jobs" :key="job.id" class="px-4 py-3">
            <div class="flex flex-wrap items-start justify-between gap-2">
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm text-foreground">{{ job.accountName || job.accountId }}</p>
                <p class="mt-0.5 text-xs text-muted-foreground">
                  {{ t(`admin.purityCheck.tiers.${job.tier}`) }} · {{ job.claimedModel }}
                  <span v-if="job.requestModel !== job.claimedModel"> → {{ job.requestModel }}</span>
                </p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <span class="text-xs font-medium" :class="statusTone(job.status)">
                  {{ t(`admin.purityCheck.statuses.${job.status}`) }}
                  <template v-if="job.status === 'queued' && job.queuePosition > 0">
                    ({{ t('admin.purityCheck.queuePosition', { ahead: job.queuePosition }) }})
                  </template>
                </span>
                <button
                  v-if="job.status === 'queued' || job.status === 'running'"
                  type="button"
                  class="text-xs text-muted-foreground hover:text-destructive"
                  @click="cancelJob(job)"
                >
                  {{ t('admin.purityCheck.cancel') }}
                </button>
                <button
                  v-if="job.report"
                  type="button"
                  class="text-xs text-primary hover:underline"
                  @click="openReport(job)"
                >
                  {{ t('admin.purityCheck.viewReport') }}
                </button>
                <button
                  v-if="isDeletable(job)"
                  type="button"
                  class="text-muted-foreground hover:text-destructive disabled:opacity-50"
                  :disabled="deletingId === job.id"
                  :title="t('admin.purityCheck.delete')"
                  @click="deleteJob(job)"
                >
                  <Loader2 v-if="deletingId === job.id" class="h-3.5 w-3.5 animate-spin" />
                  <Trash2 v-else class="h-3.5 w-3.5" />
                </button>
              </div>
            </div>

            <!-- 失败优先展示：探针一条都没打通时不能显示成一次检测结论。 -->
            <p v-if="job.status === 'failed'" class="mt-1.5 text-xs text-destructive">
              <XCircle class="mr-1 inline h-3.5 w-3.5" />
              {{ readableMessage(job.errorKey) }}
              <span v-if="job.errorDetail" class="text-muted-foreground">— {{ job.errorDetail }}</span>
            </p>
            <p
              v-else-if="job.report"
              class="mt-1.5 text-xs"
              :class="job.report.fingerprintClaimMismatch ? 'text-destructive' : 'text-muted-foreground'"
            >
              <AlertTriangle v-if="job.report.fingerprintClaimMismatch" class="mr-1 inline h-3.5 w-3.5" />
              <CheckCircle2 v-else class="mr-1 inline h-3.5 w-3.5" />
              {{ job.report.overallVerdict }}
              <span v-if="!job.report.official" class="ml-1 text-amber-600 dark:text-amber-400">
                {{ t('admin.purityCheck.nonOfficialBadge') }}
              </span>
            </p>

            <!-- 有效探针数：同样一句「证据不足」，0/19 和 19/19 含义完全不同。 -->
            <p
              v-if="job.report && job.report.totalRequests > 0"
              class="mt-1 text-xs"
              :class="job.report.successfulRequests < job.report.totalRequests
                ? 'text-amber-600 dark:text-amber-400'
                : 'text-muted-foreground'"
            >
              {{ t('admin.purityCheck.probeSuccess', {
                ok: job.report.successfulRequests,
                total: job.report.totalRequests,
              }) }}
              <span v-if="job.report.qualityNote" class="ml-1">· {{ job.report.qualityNote }}</span>
            </p>
          </li>
        </ul>
        <div v-if="hasMoreJobs" class="border-t border-border/50 px-4 py-3 text-center">
          <Button variant="secondary" size="sm" :disabled="isLoadingMoreJobs" @click="loadMoreJobs">
            <Loader2 v-if="isLoadingMoreJobs" class="h-4 w-4 animate-spin" />
            <span>{{ t('admin.purityCheck.historyLoadMore') }}</span>
          </Button>
        </div>
      </section>
    </div>

    <!-- 提交前确认：明确报出这次会真实花掉多少请求和 token。 -->
    <div v-if="confirmOpen" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <div class="w-full max-w-md rounded-lg border border-border/60 bg-card p-5 shadow-lg">
        <h3 class="text-base font-semibold text-foreground">{{ t('admin.purityCheck.confirmTitle') }}</h3>
        <p class="mt-2 text-sm text-muted-foreground">
          {{ t('admin.purityCheck.confirmBody', {
            count: selectedCount,
            tier: t(`admin.purityCheck.tiers.${selectedTier}`),
          }) }}
        </p>
        <dl v-if="estimatedTotals" class="mt-3 space-y-1.5 rounded-lg bg-surface/60 p-3 text-sm">
          <div class="flex justify-between">
            <dt class="text-muted-foreground">{{ t('admin.purityCheck.confirmLogical') }}</dt>
            <dd class="tabular-nums text-foreground">{{ formatNumber(estimatedTotals.logical) }}</dd>
          </div>
          <div class="flex justify-between">
            <dt class="text-muted-foreground">{{ t('admin.purityCheck.confirmMaxHttp') }}</dt>
            <dd class="tabular-nums text-foreground">{{ formatNumber(estimatedTotals.maxHttp) }}</dd>
          </div>
          <div class="flex justify-between">
            <dt class="text-muted-foreground">{{ t('admin.purityCheck.confirmTokens') }}</dt>
            <dd class="tabular-nums text-foreground">≈ {{ formatNumber(estimatedTotals.inputTokens) }}</dd>
          </div>
        </dl>
        <p class="mt-3 text-xs text-muted-foreground">{{ t('admin.purityCheck.confirmSerialNote') }}</p>
        <div class="mt-4 flex justify-end gap-2">
          <Button variant="secondary" size="sm" :disabled="isSubmitting" @click="confirmOpen = false">
            {{ t('admin.purityCheck.confirmCancel') }}
          </Button>
          <Button size="sm" :disabled="isSubmitting" @click="confirmSubmit">
            <Loader2 v-if="isSubmitting" class="h-4 w-4 animate-spin" />
            {{ t('admin.purityCheck.confirmOk') }}
          </Button>
        </div>
      </div>
    </div>

    <!-- 报告 -->
    <div v-if="reportJobId" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <div class="flex max-h-[85vh] w-full max-w-2xl flex-col rounded-lg border border-border/60 bg-card shadow-lg">
        <div class="flex items-center justify-between border-b border-border/50 px-5 py-3">
          <h3 class="text-base font-semibold text-foreground">{{ t('admin.purityCheck.reportTitle') }}</h3>
          <button type="button" class="text-muted-foreground hover:text-foreground" @click="closeReport">
            <X class="h-4 w-4" />
          </button>
        </div>

        <div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
          <div v-if="isLoadingReport" class="h-40 animate-pulse rounded-lg bg-surface/70" />
          <template v-else-if="reportPayload">
            <div
              class="rounded-lg px-4 py-3"
              :class="reportMismatch ? 'bg-destructive/10 text-destructive' : 'bg-surface/60 text-foreground'"
            >
              <p class="text-sm font-medium">{{ reportField('overall_verdict') || '-' }}</p>
              <p v-if="reportField('subtitle_cn')" class="mt-1 text-xs opacity-80">
                {{ reportField('subtitle_cn') }}
              </p>
            </div>

            <!-- 非官方档位的结论只是参考值，不能当强指向用。 -->
            <p
              v-if="!reportIsOfficial"
              class="rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-2.5 text-xs text-amber-700 dark:text-amber-300"
            >
              {{ t('admin.purityCheck.nonOfficialNote') }}
            </p>

            <dl class="grid gap-2 sm:grid-cols-2">
              <div class="rounded-lg border border-border/50 px-3 py-2">
                <dt class="text-xs text-muted-foreground">{{ t('admin.purityCheck.reportJuice') }}</dt>
                <dd class="mt-0.5 text-sm text-foreground">{{ reportField('juice_verdict_state') || '-' }}</dd>
              </div>
              <div class="rounded-lg border border-border/50 px-3 py-2">
                <dt class="text-xs text-muted-foreground">{{ t('admin.purityCheck.reportFingerprint') }}</dt>
                <dd class="mt-0.5 text-sm text-foreground">
                  {{ reportField('fingerprint_model') || t('admin.purityCheck.reportFingerprintUnclear') }}
                </dd>
              </div>
            </dl>

            <div v-if="reportMatches.length > 0">
              <p class="text-xs font-medium text-muted-foreground">{{ t('admin.purityCheck.reportMatches') }}</p>
              <ul class="mt-2 space-y-2">
                <li v-for="entry in reportMatches" :key="entry.label" class="text-sm">
                  <div class="flex justify-between">
                    <span class="text-foreground">{{ entry.label }}</span>
                    <span class="tabular-nums text-muted-foreground">
                      {{ (entry.match * 100).toFixed(1) }}% / {{ (entry.threshold * 100).toFixed(0) }}%
                    </span>
                  </div>
                  <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-border/50">
                    <div
                      class="h-full rounded-full"
                      :class="entry.match > entry.threshold ? 'bg-primary' : 'bg-muted-foreground/40'"
                      :style="{ width: `${Math.min(100, entry.match * 100)}%` }"
                    />
                  </div>
                </li>
              </ul>
              <p class="mt-2 text-xs text-muted-foreground">{{ t('admin.purityCheck.reportMatchesNote') }}</p>
            </div>

            <details class="rounded-lg border border-border/50">
              <summary class="cursor-pointer px-3 py-2 text-xs text-muted-foreground">
                {{ t('admin.purityCheck.reportRaw') }}
              </summary>
              <pre class="max-h-72 overflow-auto px-3 pb-3 text-xs text-muted-foreground">{{ JSON.stringify(reportPayload, null, 2) }}</pre>
            </details>
          </template>
          <p v-else class="text-sm text-muted-foreground">{{ t('admin.purityCheck.reportEmpty') }}</p>
        </div>

        <p class="border-t border-border/50 px-5 py-3 text-xs text-muted-foreground">
          {{ t('admin.purityCheck.disclaimer') }}
        </p>
      </div>
    </div>
  </div>
</template>
