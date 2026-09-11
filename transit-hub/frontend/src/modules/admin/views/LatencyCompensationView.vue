<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  AlertCircle,
  ChevronDown,
  ChevronRight,
  HandCoins,
  History,
  Loader2,
  Pencil,
  Play,
  Plus,
  Search,
  Trash2,
  Undo2,
  X
} from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  listLatencySubsidyTasks,
  createLatencySubsidyTask,
  updateLatencySubsidyTask,
  deleteLatencySubsidyTask,
  previewLatencySubsidyTask,
  applyLatencySubsidyTask,
  listLatencySubsidyTasksHistory,
  revokeLatencyCompensationPayout
} from '../api/mySites'
import type {
  LatencyCompensationSummary,
  LatencyCompensationPayoutView,
  LatencySubsidyTaskView,
  LatencySubsidyTaskInput,
  LatencySubsidyRecurrenceType
} from '../types/mySites'

// 星期几选项：value 跟后端/JS Date.getDay() 对齐 (0=周日..6=周六)，
// 但从周一开始展示，符合中文用户的阅读习惯。
const WEEKDAY_OPTIONS: { value: number; labelKey: string }[] = [
  { value: 1, labelKey: 'admin.latencyCompensation.weekdayMon' },
  { value: 2, labelKey: 'admin.latencyCompensation.weekdayTue' },
  { value: 3, labelKey: 'admin.latencyCompensation.weekdayWed' },
  { value: 4, labelKey: 'admin.latencyCompensation.weekdayThu' },
  { value: 5, labelKey: 'admin.latencyCompensation.weekdayFri' },
  { value: 6, labelKey: 'admin.latencyCompensation.weekdaySat' },
  { value: 0, labelKey: 'admin.latencyCompensation.weekdaySun' }
]

const { t } = useI18n()

function formatCost(value: number): string {
  return (value ?? 0).toFixed(4)
}

function formatDateTime(value: string): string {
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString()
}

const WEEKDAY_SHORT_KEYS = [
  'admin.latencyCompensation.weekdaySunShort',
  'admin.latencyCompensation.weekdayMonShort',
  'admin.latencyCompensation.weekdayTueShort',
  'admin.latencyCompensation.weekdayWedShort',
  'admin.latencyCompensation.weekdayThuShort',
  'admin.latencyCompensation.weekdayFriShort',
  'admin.latencyCompensation.weekdaySatShort'
]

function formatAutoBadge(task: LatencySubsidyTaskView): string {
  if (task.recurrenceType === 'weekday') {
    return t('admin.latencyCompensation.autoBadgeWeekday', { time: task.windowEnd })
  }
  if (task.recurrenceType === 'weekly') {
    const days = [...task.recurrenceDaysOfWeek].sort().map((d) => t(WEEKDAY_SHORT_KEYS[d])).join('')
    return t('admin.latencyCompensation.autoBadgeWeekly', { days: days || '?', time: task.windowEnd })
  }
  return t('admin.latencyCompensation.autoBadge', { time: task.windowEnd })
}

function formatValidityBadge(task: LatencySubsidyTaskView): string {
  if (task.validFrom && task.validUntil) {
    return `${task.validFrom} ~ ${task.validUntil}`
  }
  if (task.validFrom) {
    return t('admin.latencyCompensation.validityFromOnly', { date: task.validFrom })
  }
  return t('admin.latencyCompensation.validityUntilOnly', { date: task.validUntil })
}

// datetime-local carries no timezone info; the browser interprets (and this
// page sends) it in local time, then converts to RFC3339 UTC at request time.
function toLocalInputValue(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// ---------------------------------------------------------------------------
// 任务列表
// ---------------------------------------------------------------------------
const tasks = ref<LatencySubsidyTaskView[]>([])
const loadingTasks = ref(false)
const tasksErrorKey = ref<string | null>(null)

async function loadTasks() {
  loadingTasks.value = true
  tasksErrorKey.value = null
  try {
    tasks.value = await listLatencySubsidyTasks()
  } catch (err) {
    tasksErrorKey.value = err instanceof Error ? err.message : 'admin.latencyCompensation.tasksLoadFailed'
  } finally {
    loadingTasks.value = false
  }
}

// ---------------------------------------------------------------------------
// 新建/编辑任务表单
// ---------------------------------------------------------------------------
const showTaskForm = ref(false)
const editingTaskId = ref<string | null>(null)
const formName = ref('')
// 阈值改成按秒填写，存库/传后端时再换算成毫秒——毫秒对管理员没有意义，
// 30 秒远比 30000 毫秒直观。
const formThresholdSecondsInput = ref('30')
const formProfitRatioPercentInput = ref('100')
const formAutoEnabled = ref(false)
// 补贴时间段：每天只对这段时间内的请求算补贴，不是从当天 00:00 到执行时刻。
// 窗口终点同时是自动执行的触发时刻——早于它窗口的数据还没走完。
const formWindowStartInput = ref('18:00')
const formWindowEndInput = ref('22:00')
const formRecurrenceType = ref<LatencySubsidyRecurrenceType>('daily')
const formRecurrenceDaysOfWeek = ref<number[]>([])
const formValidityEnabled = ref(false)
const formValidFrom = ref('')
const formValidUntil = ref('')
const savingTask = ref(false)
const formErrorKey = ref<string | null>(null)

function toggleFormWeekday(value: number) {
  const idx = formRecurrenceDaysOfWeek.value.indexOf(value)
  if (idx === -1) {
    formRecurrenceDaysOfWeek.value = [...formRecurrenceDaysOfWeek.value, value].sort()
  } else {
    formRecurrenceDaysOfWeek.value = formRecurrenceDaysOfWeek.value.filter((d) => d !== value)
  }
}

function openCreateForm() {
  editingTaskId.value = null
  formName.value = ''
  formThresholdSecondsInput.value = '30'
  formProfitRatioPercentInput.value = '100'
  formAutoEnabled.value = false
  formWindowStartInput.value = '18:00'
  formWindowEndInput.value = '22:00'
  formRecurrenceType.value = 'daily'
  formRecurrenceDaysOfWeek.value = []
  formValidityEnabled.value = false
  formValidFrom.value = ''
  formValidUntil.value = ''
  formErrorKey.value = null
  showTaskForm.value = true
}

function openEditForm(task: LatencySubsidyTaskView) {
  editingTaskId.value = task.id
  formName.value = task.name
  formThresholdSecondsInput.value = String(Math.round(task.thresholdMs / 1000))
  formProfitRatioPercentInput.value = String(Math.round(task.profitRatio * 100))
  formAutoEnabled.value = task.autoEnabled
  formWindowStartInput.value = task.windowStart || '00:00'
  formWindowEndInput.value = task.windowEnd || '23:59'
  formRecurrenceType.value = task.recurrenceType || 'daily'
  formRecurrenceDaysOfWeek.value = [...(task.recurrenceDaysOfWeek || [])]
  formValidityEnabled.value = Boolean(task.validFrom || task.validUntil)
  formValidFrom.value = task.validFrom || ''
  formValidUntil.value = task.validUntil || ''
  formErrorKey.value = null
  showTaskForm.value = true
}

function closeTaskForm() {
  showTaskForm.value = false
}

async function submitTaskForm() {
  formErrorKey.value = null
  const name = formName.value.trim()
  const thresholdSeconds = Number(formThresholdSecondsInput.value)
  const ratioPercent = Number(formProfitRatioPercentInput.value)
  if (!name) {
    formErrorKey.value = 'admin.latencyCompensation.invalidName'
    return
  }
  if (!thresholdSeconds || thresholdSeconds <= 0) {
    formErrorKey.value = 'admin.latencyCompensation.invalidThreshold'
    return
  }
  if (ratioPercent < 0 || ratioPercent > 100) {
    formErrorKey.value = 'admin.latencyCompensation.invalidRatio'
    return
  }
  if (!formWindowStartInput.value || !formWindowEndInput.value) {
    formErrorKey.value = 'admin.latencyCompensation.invalidTime'
    return
  }
  if (formWindowEndInput.value <= formWindowStartInput.value) {
    formErrorKey.value = 'admin.latencyCompensation.invalidWindow'
    return
  }
  if (formRecurrenceType.value === 'weekly' && formRecurrenceDaysOfWeek.value.length === 0) {
    formErrorKey.value = 'admin.latencyCompensation.invalidWeekdays'
    return
  }
  if (formValidityEnabled.value && formValidFrom.value && formValidUntil.value && formValidUntil.value < formValidFrom.value) {
    formErrorKey.value = 'admin.latencyCompensation.invalidValidity'
    return
  }
  const input: LatencySubsidyTaskInput = {
    name,
    thresholdMs: Math.round(thresholdSeconds * 1000),
    profitRatio: ratioPercent / 100,
    autoEnabled: formAutoEnabled.value,
    windowStart: formWindowStartInput.value,
    windowEnd: formWindowEndInput.value,
    recurrenceType: formRecurrenceType.value,
    recurrenceDaysOfWeek: formRecurrenceType.value === 'weekly' ? formRecurrenceDaysOfWeek.value : [],
    validFrom: formValidityEnabled.value ? formValidFrom.value : '',
    validUntil: formValidityEnabled.value ? formValidUntil.value : ''
  }
  savingTask.value = true
  try {
    if (editingTaskId.value) {
      await updateLatencySubsidyTask(editingTaskId.value, input)
    } else {
      await createLatencySubsidyTask(input)
    }
    showTaskForm.value = false
    await loadTasks()
  } catch (err) {
    formErrorKey.value = err instanceof Error ? err.message : 'admin.latencyCompensation.autoSaveFailed'
  } finally {
    savingTask.value = false
  }
}

const deletingTaskId = ref<string | null>(null)
const confirmingDeleteId = ref<string | null>(null)

async function removeTask(task: LatencySubsidyTaskView) {
  if (deletingTaskId.value) return
  deletingTaskId.value = task.id
  confirmingDeleteId.value = null
  try {
    await deleteLatencySubsidyTask(task.id)
    if (runningTask.value?.id === task.id) {
      runningTask.value = null
    }
    await loadTasks()
  } catch (err) {
    tasksErrorKey.value = err instanceof Error ? err.message : 'admin.latencyCompensation.tasksLoadFailed'
  } finally {
    deletingTaskId.value = null
  }
}

// ---------------------------------------------------------------------------
// 运行面板：对某一个任务预览 + 发放
// ---------------------------------------------------------------------------
const runningTask = ref<LatencySubsidyTaskView | null>(null)
const runFromInput = ref('')
const runToInput = ref('')
const previewing = ref(false)
const applying = ref(false)
const showApplyConfirm = ref(false)
const runErrorKey = ref<string | null>(null)
const runSuccessMessage = ref<string | null>(null)
const runSummary = ref<LatencyCompensationSummary | null>(null)

function openRunPanel(task: LatencySubsidyTaskView) {
  runningTask.value = task
  // 默认套用任务自己的每天补贴窗口，映射到今天——跟自动执行用的是同一段
  // 时间，手动运行时预览到的结果才能跟自动执行对得上。仍可以在下面改成
  // 任意历史区间，用于补一次性的事故窗口。
  const today = new Date()
  const [startHour, startMinute] = (task.windowStart || '00:00').split(':').map(Number)
  const [endHour, endMinute] = (task.windowEnd || '23:59').split(':').map(Number)
  const windowStart = new Date(today.getFullYear(), today.getMonth(), today.getDate(), startHour, startMinute)
  const windowEnd = new Date(today.getFullYear(), today.getMonth(), today.getDate(), endHour, endMinute)
  runFromInput.value = toLocalInputValue(windowStart)
  runToInput.value = toLocalInputValue(windowEnd)
  runErrorKey.value = null
  runSuccessMessage.value = null
  runSummary.value = null
  showApplyConfirm.value = false
}

function closeRunPanel() {
  runningTask.value = null
}

const applyConfirmMessage = computed(() =>
  t('admin.latencyCompensation.applyConfirmMessage', {
    amount: runSummary.value ? formatCost(runSummary.value.totalCompensation) : '0',
    count: runSummary.value?.users.length ?? 0
  })
)

function runWindowParams(): { from: string; to: string } | null {
  runErrorKey.value = null
  const from = new Date(runFromInput.value)
  const to = new Date(runToInput.value)
  if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime()) || to <= from) {
    runErrorKey.value = 'admin.latencyCompensation.invalidRange'
    return null
  }
  return { from: from.toISOString(), to: to.toISOString() }
}

async function runPreview() {
  if (!runningTask.value) return
  const params = runWindowParams()
  if (!params) return
  runSuccessMessage.value = null
  previewing.value = true
  try {
    runSummary.value = await previewLatencySubsidyTask(runningTask.value.id, params.from, params.to)
  } catch (err) {
    runErrorKey.value = err instanceof Error ? err.message : 'admin.latencyCompensation.previewFailed'
  } finally {
    previewing.value = false
  }
}

async function runApply() {
  if (!runningTask.value) return
  showApplyConfirm.value = false
  const params = runWindowParams()
  if (!params) return
  applying.value = true
  try {
    runSummary.value = await applyLatencySubsidyTask(runningTask.value.id, params.from, params.to)
    runSuccessMessage.value = t('admin.latencyCompensation.applySuccess', {
      amount: formatCost(runSummary.value.totalCompensation),
      count: runSummary.value.users.length
    })
    await loadHistory()
  } catch (err) {
    runErrorKey.value = err instanceof Error ? err.message : 'admin.latencyCompensation.applyFailed'
  } finally {
    applying.value = false
  }
}

// ---------------------------------------------------------------------------
// 发放历史
// ---------------------------------------------------------------------------
const history = ref<LatencyCompensationPayoutView[]>([])
const loadingHistory = ref(false)
const expandedPayoutId = ref<string | null>(null)

function toggleExpanded(id: string) {
  expandedPayoutId.value = expandedPayoutId.value === id ? null : id
}

async function loadHistory() {
  loadingHistory.value = true
  try {
    history.value = await listLatencySubsidyTasksHistory()
  } catch {
    // 历史记录加载失败不影响任务管理/运行这两个主功能，静默即可。
  } finally {
    loadingHistory.value = false
  }
}

// 撤回：针对某一整批历史记录（这一批里的所有用户），不是针对单个用户。
const confirmingRevokeId = ref<string | null>(null)
const revokingPayoutId = ref<string | null>(null)
const revokeErrorKey = ref<string | null>(null)
const revokeSuccessMessage = ref<string | null>(null)

async function revokePayout(payout: LatencyCompensationPayoutView) {
  if (revokingPayoutId.value) return
  revokingPayoutId.value = payout.id
  confirmingRevokeId.value = null
  revokeErrorKey.value = null
  revokeSuccessMessage.value = null
  try {
    const result = await revokeLatencyCompensationPayout(payout.id)
    if (result.skippedUserIds.length > 0) {
      revokeSuccessMessage.value = t('admin.latencyCompensation.revokePartialSuccess', {
        revoked: result.revokedUserIds.length,
        skipped: result.skippedUserIds.length
      })
    } else {
      revokeSuccessMessage.value = t('admin.latencyCompensation.revokeSuccess', {
        count: result.revokedUserIds.length
      })
    }
    await loadHistory()
  } catch (err) {
    revokeErrorKey.value = err instanceof Error ? err.message : 'admin.latencyCompensation.revokeFailed'
  } finally {
    revokingPayoutId.value = null
  }
}

onMounted(() => {
  loadTasks()
  loadHistory()
})
</script>

<template>
  <div class="mx-auto max-w-5xl space-y-6 p-6">
    <div class="flex items-center gap-3">
      <div class="flex h-10 w-10 items-center justify-center rounded-full bg-accent/10 text-accent">
        <HandCoins class="h-5 w-5" />
      </div>
      <div>
        <h1 class="text-xl font-semibold text-foreground">{{ t('admin.latencyCompensation.title') }}</h1>
        <p class="text-sm text-muted-foreground">{{ t('admin.latencyCompensation.subtitle') }}</p>
      </div>
    </div>

    <!-- 任务列表 -->
    <div class="space-y-4 rounded-2xl border border-border/60 bg-card p-5">
      <div class="flex items-center justify-between gap-4">
        <h2 class="text-sm font-semibold text-foreground">{{ t('admin.latencyCompensation.tasksTitle') }}</h2>
        <Button size="sm" :disabled="showTaskForm" @click="openCreateForm">
          <Plus class="mr-2 h-4 w-4" />
          {{ t('admin.latencyCompensation.newTask') }}
        </Button>
      </div>

      <div v-if="tasksErrorKey" class="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
        <AlertCircle class="h-4 w-4 shrink-0" />
        {{ t(tasksErrorKey) }}
      </div>

      <!-- 新建/编辑表单 -->
      <div v-if="showTaskForm" class="space-y-4 rounded-xl border border-border/60 bg-background/60 p-4">
        <div class="flex items-center justify-between">
          <h3 class="text-sm font-semibold text-foreground">
            {{ editingTaskId ? t('admin.latencyCompensation.editTask') : t('admin.latencyCompensation.newTask') }}
          </h3>
          <button type="button" class="text-muted-foreground hover:text-foreground" @click="closeTaskForm">
            <X class="h-4 w-4" />
          </button>
        </div>

        <div v-if="formErrorKey" class="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          <AlertCircle class="h-4 w-4 shrink-0" />
          {{ t(formErrorKey) }}
        </div>

        <div>
          <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.taskName') }}</label>
          <Input v-model="formName" type="text" :placeholder="t('admin.latencyCompensation.taskNamePlaceholder')" class="mt-1.5" />
        </div>

        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.thresholdSeconds') }}</label>
            <Input v-model="formThresholdSecondsInput" type="number" min="1" step="1" class="mt-1.5" />
            <p class="mt-1.5 text-xs text-muted-foreground">{{ t('admin.latencyCompensation.thresholdHint') }}</p>
          </div>
          <div>
            <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.profitRatio') }}</label>
            <div class="mt-1.5 flex items-center gap-2">
              <Input v-model="formProfitRatioPercentInput" type="number" min="0" max="100" step="5" />
              <span class="text-sm text-muted-foreground">%</span>
            </div>
            <p class="mt-1.5 text-xs text-muted-foreground">{{ t('admin.latencyCompensation.profitRatioHint') }}</p>
          </div>
        </div>

        <div class="grid gap-4 border-t border-border/60 pt-4 sm:grid-cols-2">
          <div>
            <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.windowStart') }}</label>
            <Input v-model="formWindowStartInput" type="time" class="mt-1.5" />
          </div>
          <div>
            <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.windowEnd') }}</label>
            <Input v-model="formWindowEndInput" type="time" class="mt-1.5" />
          </div>
        </div>
        <p class="text-xs text-muted-foreground">{{ t('admin.latencyCompensation.windowHint') }}</p>

        <div class="flex items-center justify-between border-t border-border/60 pt-4">
          <div>
            <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.autoEnabled') }}</label>
            <p class="mt-0.5 text-xs text-muted-foreground">{{ t('admin.latencyCompensation.autoEnabledHint', { time: formWindowEndInput || '--:--' }) }}</p>
          </div>
          <label class="relative inline-flex shrink-0 cursor-pointer items-center">
            <input type="checkbox" v-model="formAutoEnabled" class="peer sr-only" :aria-label="t('admin.latencyCompensation.autoEnabled')">
            <div class="peer h-6 w-11 rounded-full bg-surface-elevated after:absolute after:left-[2px] after:top-[2px] after:h-5 after:w-5 after:rounded-full after:border after:border-border after:bg-white after:transition-transform after:content-[''] peer-checked:bg-primary peer-checked:after:translate-x-full peer-checked:after:border-white peer-focus-visible:ring-2 peer-focus-visible:ring-primary peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background"></div>
          </label>
        </div>

        <!-- 重复方式：只在开了自动执行时才有意义 -->
        <div v-if="formAutoEnabled" class="space-y-3 border-t border-border/60 pt-4">
          <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.recurrenceType') }}</label>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="opt in (['daily', 'weekly', 'weekday'] as const)"
              :key="opt"
              type="button"
              class="rounded-full border px-3 py-1.5 text-xs font-medium transition-colors"
              :class="formRecurrenceType === opt
                ? 'border-primary bg-primary/10 text-primary'
                : 'border-border/60 text-muted-foreground hover:text-foreground'"
              @click="formRecurrenceType = opt"
            >
              {{ t(`admin.latencyCompensation.recurrence${opt.charAt(0).toUpperCase()}${opt.slice(1)}`) }}
            </button>
          </div>

          <div v-if="formRecurrenceType === 'weekly'" class="flex flex-wrap gap-2">
            <button
              v-for="opt in WEEKDAY_OPTIONS"
              :key="opt.value"
              type="button"
              class="rounded-full border px-3 py-1.5 text-xs font-medium transition-colors"
              :class="formRecurrenceDaysOfWeek.includes(opt.value)
                ? 'border-primary bg-primary/10 text-primary'
                : 'border-border/60 text-muted-foreground hover:text-foreground'"
              @click="toggleFormWeekday(opt.value)"
            >
              {{ t(opt.labelKey) }}
            </button>
          </div>
        </div>

        <!-- 有效期：跟重复方式正交，限定这个自动执行规则本身在哪段日期内生效 -->
        <div v-if="formAutoEnabled" class="space-y-3 border-t border-border/60 pt-4">
          <div class="flex items-center justify-between">
            <div>
              <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.validityEnabled') }}</label>
              <p class="mt-0.5 text-xs text-muted-foreground">{{ t('admin.latencyCompensation.validityHint') }}</p>
            </div>
            <label class="relative inline-flex shrink-0 cursor-pointer items-center">
              <input type="checkbox" v-model="formValidityEnabled" class="peer sr-only" :aria-label="t('admin.latencyCompensation.validityEnabled')">
              <div class="peer h-6 w-11 rounded-full bg-surface-elevated after:absolute after:left-[2px] after:top-[2px] after:h-5 after:w-5 after:rounded-full after:border after:border-border after:bg-white after:transition-transform after:content-[''] peer-checked:bg-primary peer-checked:after:translate-x-full peer-checked:after:border-white peer-focus-visible:ring-2 peer-focus-visible:ring-primary peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background"></div>
            </label>
          </div>
          <div v-if="formValidityEnabled" class="grid gap-4 sm:grid-cols-2">
            <div>
              <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.validFrom') }}</label>
              <Input v-model="formValidFrom" type="date" class="mt-1.5" />
            </div>
            <div>
              <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.validUntil') }}</label>
              <Input v-model="formValidUntil" type="date" class="mt-1.5" />
            </div>
          </div>
        </div>

        <div class="flex justify-end gap-2 border-t border-border/60 pt-4">
          <Button variant="secondary" size="sm" :disabled="savingTask" @click="closeTaskForm">
            {{ t('admin.latencyCompensation.cancel') }}
          </Button>
          <Button size="sm" :disabled="savingTask" @click="submitTaskForm">
            <Loader2 v-if="savingTask" class="mr-2 h-4 w-4 animate-spin" />
            {{ t('admin.latencyCompensation.save') }}
          </Button>
        </div>
      </div>

      <div v-if="loadingTasks" class="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 class="h-4 w-4 animate-spin" />
        {{ t('admin.latencyCompensation.tasksLoading') }}
      </div>
      <p v-else-if="tasks.length === 0 && !showTaskForm" class="text-sm text-muted-foreground">
        {{ t('admin.latencyCompensation.tasksEmpty') }}
      </p>
      <div v-else class="divide-y divide-border/40">
        <div v-for="task in tasks" :key="task.id" class="flex flex-wrap items-center justify-between gap-3 py-3">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="truncate text-sm font-medium text-foreground">{{ task.name }}</span>
              <span
                v-if="task.autoEnabled"
                class="shrink-0 rounded-full bg-emerald-500/10 px-2 py-0.5 text-xs font-medium text-emerald-600 dark:text-emerald-400"
              >
                {{ formatAutoBadge(task) }}
              </span>
              <span v-else class="shrink-0 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
                {{ t('admin.latencyCompensation.manualBadge') }}
              </span>
              <span
                v-if="task.autoEnabled && (task.validFrom || task.validUntil)"
                class="shrink-0 rounded-full bg-amber-500/10 px-2 py-0.5 text-xs font-medium text-amber-600 dark:text-amber-400"
              >
                {{ formatValidityBadge(task) }}
              </span>
            </div>
            <p class="mt-0.5 text-xs text-muted-foreground">
              {{ t('admin.latencyCompensation.taskSummary', {
                windowStart: task.windowStart,
                windowEnd: task.windowEnd,
                seconds: Math.round(task.thresholdMs / 1000),
                ratio: Math.round(task.profitRatio * 100)
              }) }}
            </p>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <Button size="sm" variant="secondary" @click="openRunPanel(task)">
              <Play class="mr-1.5 h-3.5 w-3.5" />
              {{ t('admin.latencyCompensation.run') }}
            </Button>
            <Button size="sm" variant="ghost" @click="openEditForm(task)">
              <Pencil class="h-3.5 w-3.5" />
            </Button>
            <Button
              v-if="confirmingDeleteId !== task.id"
              size="sm"
              variant="ghost"
              class="text-destructive hover:text-destructive"
              @click="confirmingDeleteId = task.id"
            >
              <Trash2 class="h-3.5 w-3.5" />
            </Button>
            <template v-else>
              <span class="text-xs text-muted-foreground">{{ t('admin.latencyCompensation.confirmDelete') }}</span>
              <Button size="sm" variant="destructive" :disabled="deletingTaskId === task.id" @click="removeTask(task)">
                <Loader2 v-if="deletingTaskId === task.id" class="h-3.5 w-3.5 animate-spin" />
                <span v-else>{{ t('admin.latencyCompensation.confirm') }}</span>
              </Button>
              <Button size="sm" variant="secondary" @click="confirmingDeleteId = null">
                {{ t('admin.latencyCompensation.cancel') }}
              </Button>
            </template>
          </div>
        </div>
      </div>
    </div>

    <!-- 运行面板 -->
    <div v-if="runningTask" class="space-y-4 rounded-2xl border border-border/60 bg-card p-5">
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-sm font-semibold text-foreground">
            {{ t('admin.latencyCompensation.runTitle', { name: runningTask.name }) }}
          </h2>
          <p class="mt-0.5 text-xs text-muted-foreground">{{ t('admin.latencyCompensation.runHint') }}</p>
        </div>
        <button type="button" class="text-muted-foreground hover:text-foreground" @click="closeRunPanel">
          <X class="h-4 w-4" />
        </button>
      </div>

      <div v-if="runErrorKey" class="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
        <AlertCircle class="h-4 w-4 shrink-0" />
        {{ t(runErrorKey) }}
      </div>
      <div v-if="runSuccessMessage" class="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-4 py-3 text-sm text-emerald-600 dark:text-emerald-400">
        {{ runSuccessMessage }}
      </div>

      <div class="grid gap-4 sm:grid-cols-2">
        <div>
          <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.from') }}</label>
          <Input v-model="runFromInput" type="datetime-local" class="mt-1.5" />
        </div>
        <div>
          <label class="text-sm font-medium text-foreground">{{ t('admin.latencyCompensation.to') }}</label>
          <Input v-model="runToInput" type="datetime-local" class="mt-1.5" />
        </div>
      </div>

      <div class="flex justify-end border-t border-border/60 pt-4">
        <Button :disabled="previewing" @click="runPreview">
          <Loader2 v-if="previewing" class="mr-2 h-4 w-4 animate-spin" />
          <Search v-else class="mr-2 h-4 w-4" />
          {{ t('admin.latencyCompensation.preview') }}
        </Button>
      </div>

      <div v-if="runSummary" class="space-y-4 border-t border-border/60 pt-4">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <div class="grid grid-cols-2 gap-x-8 gap-y-1 text-sm sm:grid-cols-4">
            <div>
              <p class="text-muted-foreground">{{ t('admin.latencyCompensation.slowRequests') }}</p>
              <p class="font-mono text-base font-semibold text-foreground">{{ runSummary.totalRequests }}</p>
            </div>
            <div>
              <p class="text-muted-foreground">{{ t('admin.latencyCompensation.actualCost') }}</p>
              <p class="font-mono text-base font-semibold text-emerald-600 dark:text-emerald-400">${{ formatCost(runSummary.totalActualCost) }}</p>
            </div>
            <div>
              <p class="text-muted-foreground">{{ t('admin.latencyCompensation.accountCost') }}</p>
              <p class="font-mono text-base font-semibold text-amber-600 dark:text-amber-400">${{ formatCost(runSummary.totalAccountCost) }}</p>
            </div>
            <div>
              <p class="text-muted-foreground">{{ t('admin.latencyCompensation.totalCompensation') }}</p>
              <p class="font-mono text-lg font-bold text-accent">${{ formatCost(runSummary.totalCompensation) }}</p>
            </div>
          </div>
          <Button variant="destructive" :disabled="applying || runSummary.users.length === 0" @click="showApplyConfirm = true">
            <Loader2 v-if="applying" class="mr-2 h-4 w-4 animate-spin" />
            <HandCoins v-else class="mr-2 h-4 w-4" />
            {{ t('admin.latencyCompensation.apply') }}
          </Button>
        </div>

        <div v-if="showApplyConfirm" class="rounded-lg border border-destructive/30 bg-destructive/10 p-4">
          <p class="mb-1 text-sm font-semibold text-destructive">{{ t('admin.latencyCompensation.applyConfirmTitle') }}</p>
          <p class="mb-3 text-sm text-foreground">{{ applyConfirmMessage }}</p>
          <div class="flex gap-2">
            <Button variant="destructive" size="sm" @click="runApply">{{ t('admin.latencyCompensation.confirm') }}</Button>
            <Button variant="secondary" size="sm" @click="showApplyConfirm = false">{{ t('admin.latencyCompensation.cancel') }}</Button>
          </div>
        </div>

        <p v-if="runSummary.users.length === 0" class="text-sm text-muted-foreground">
          {{ t('admin.latencyCompensation.noneQualified') }}
        </p>
        <div v-else class="max-h-96 overflow-auto">
          <table class="w-full text-sm">
            <thead class="sticky top-0 bg-card">
              <tr class="border-b border-border/60 text-left text-xs text-muted-foreground">
                <th class="py-2 pr-3">{{ t('admin.latencyCompensation.user') }}</th>
                <th class="py-2 pr-3 text-right">{{ t('admin.latencyCompensation.requests') }}</th>
                <th class="py-2 pr-3 text-right">{{ t('admin.latencyCompensation.actualCost') }}</th>
                <th class="py-2 pr-3 text-right">{{ t('admin.latencyCompensation.accountCost') }}</th>
                <th class="py-2 text-right">{{ t('admin.latencyCompensation.compensation') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in runSummary.users" :key="u.userId" class="border-b border-border/40">
                <td class="py-1.5 pr-3 text-foreground">{{ u.email || `#${u.userId}` }}</td>
                <td class="py-1.5 pr-3 text-right text-muted-foreground">{{ u.requests }}</td>
                <td class="py-1.5 pr-3 text-right font-mono text-emerald-600 dark:text-emerald-400">${{ formatCost(u.actualCost) }}</td>
                <td class="py-1.5 pr-3 text-right font-mono text-amber-600 dark:text-amber-400">${{ formatCost(u.accountCost) }}</td>
                <td class="py-1.5 text-right font-mono font-semibold text-accent">${{ formatCost(u.compensation) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- 发放历史 -->
    <div class="space-y-4 rounded-2xl border border-border/60 bg-card p-5">
      <div class="flex items-center gap-2">
        <History class="h-4 w-4 text-muted-foreground" />
        <h2 class="text-sm font-semibold text-foreground">{{ t('admin.latencyCompensation.historyTitle') }}</h2>
      </div>

      <div v-if="revokeErrorKey" class="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
        <AlertCircle class="h-4 w-4 shrink-0" />
        {{ t(revokeErrorKey) }}
      </div>
      <div v-if="revokeSuccessMessage" class="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-4 py-3 text-sm text-emerald-600 dark:text-emerald-400">
        {{ revokeSuccessMessage }}
      </div>

      <div v-if="loadingHistory" class="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 class="h-4 w-4 animate-spin" />
        {{ t('admin.latencyCompensation.historyLoading') }}
      </div>
      <p v-else-if="history.length === 0" class="text-sm text-muted-foreground">
        {{ t('admin.latencyCompensation.historyEmpty') }}
      </p>
      <div v-else class="divide-y divide-border/40">
        <div v-for="payout in history" :key="payout.id" class="py-3">
          <div class="flex w-full items-center justify-between gap-4">
            <button
              type="button"
              class="flex min-w-0 items-center gap-2 text-left text-sm"
              @click="toggleExpanded(payout.id)"
            >
              <ChevronDown v-if="expandedPayoutId === payout.id" class="h-4 w-4 shrink-0 text-muted-foreground" />
              <ChevronRight v-else class="h-4 w-4 shrink-0 text-muted-foreground" />
              <span class="text-foreground">{{ formatDateTime(payout.createdAt) }}</span>
              <span v-if="payout.taskName" class="shrink-0 rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">{{ payout.taskName }}</span>
              <span v-if="payout.revokedAt" class="shrink-0 rounded-full bg-destructive/10 px-2 py-0.5 text-xs font-medium text-destructive">
                {{ t('admin.latencyCompensation.revokedBadge') }}
              </span>
              <span class="truncate text-muted-foreground">
                {{ formatDateTime(payout.fromTime) }} ~ {{ formatDateTime(payout.toTime) }}
              </span>
            </button>
            <div class="flex shrink-0 items-center gap-3 text-sm">
              <span class="text-muted-foreground">{{ t('admin.latencyCompensation.historyUsersCount', { count: payout.usersCompensated }) }}</span>
              <span class="font-mono font-semibold text-accent">${{ formatCost(payout.amountUsd) }}</span>
              <template v-if="!payout.revokedAt">
                <Button
                  v-if="confirmingRevokeId !== payout.id"
                  size="sm"
                  variant="ghost"
                  class="text-destructive hover:text-destructive"
                  @click="confirmingRevokeId = payout.id"
                >
                  <Undo2 class="mr-1.5 h-3.5 w-3.5" />
                  {{ t('admin.latencyCompensation.revoke') }}
                </Button>
                <template v-else>
                  <span class="text-xs text-muted-foreground">{{ t('admin.latencyCompensation.confirmRevoke') }}</span>
                  <Button size="sm" variant="destructive" :disabled="revokingPayoutId === payout.id" @click="revokePayout(payout)">
                    <Loader2 v-if="revokingPayoutId === payout.id" class="h-3.5 w-3.5 animate-spin" />
                    <span v-else>{{ t('admin.latencyCompensation.confirm') }}</span>
                  </Button>
                  <Button size="sm" variant="secondary" @click="confirmingRevokeId = null">
                    {{ t('admin.latencyCompensation.cancel') }}
                  </Button>
                </template>
              </template>
            </div>
          </div>
          <div v-if="expandedPayoutId === payout.id" class="mt-3 max-h-64 overflow-auto rounded-lg border border-border/40">
            <table class="w-full text-sm">
              <thead class="sticky top-0 bg-card">
                <tr class="border-b border-border/60 text-left text-xs text-muted-foreground">
                  <th class="py-2 pl-3 pr-3">{{ t('admin.latencyCompensation.user') }}</th>
                  <th class="py-2 pr-3 text-right">{{ t('admin.latencyCompensation.requests') }}</th>
                  <th class="py-2 pr-3 text-right">{{ t('admin.latencyCompensation.compensation') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="u in payout.users" :key="u.userId" class="border-b border-border/40 last:border-b-0">
                  <td class="py-1.5 pl-3 pr-3 text-foreground">{{ u.email || `#${u.userId}` }}</td>
                  <td class="py-1.5 pr-3 text-right text-muted-foreground">{{ u.requests }}</td>
                  <td class="py-1.5 pr-3 text-right font-mono font-semibold text-accent">${{ formatCost(u.compensation) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
