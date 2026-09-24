<template>
  <section class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="codex-profile-statistics">
    <div class="flex items-start justify-between gap-4">
      <div>
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.accounts.codexProfile.title') }}
        </h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.codexProfile.hint') }}
        </p>
      </div>
      <button
        type="button"
        class="btn btn-secondary btn-sm"
        :disabled="loading"
        data-testid="codex-profile-refresh"
        @click="load(true)"
      >
        <Icon name="refresh" size="xs" class="mr-1.5" :class="{ 'animate-spin': loading }" />
        {{ t('admin.accounts.codexProfile.refresh') }}
      </button>
    </div>

    <div v-if="loading && !stats" class="flex h-20 items-center justify-center text-gray-400">
      <Icon name="refresh" size="sm" class="animate-spin" />
    </div>

    <p v-else-if="error" class="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-800/50 dark:bg-amber-900/20 dark:text-amber-200">
      {{ error }}
    </p>

    <template v-else-if="stats">
      <div class="flex items-center gap-3">
        <img
          v-if="stats.avatar_url"
          :src="stats.avatar_url"
          :alt="displayName"
          class="h-10 w-10 rounded-full border border-gray-200 object-cover dark:border-dark-600"
          referrerpolicy="no-referrer"
        />
        <div v-else class="flex h-10 w-10 items-center justify-center rounded-full bg-gray-100 text-sm font-medium text-gray-500 dark:bg-dark-700 dark:text-gray-300">
          {{ (displayName || '?').charAt(0).toUpperCase() }}
        </div>
        <div class="min-w-0">
          <p class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ displayName || '-' }}</p>
          <p v-if="stats.username" class="truncate text-xs text-gray-500 dark:text-gray-400">@{{ stats.username }}</p>
        </div>
      </div>

      <p v-if="stats.has_stats_error" class="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-800/50 dark:bg-amber-900/20 dark:text-amber-200">
        {{ t('admin.accounts.codexProfile.statsError') }}
      </p>

      <div class="grid grid-cols-[minmax(4rem,auto)_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
        <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexProfile.lifetimeTokens') }}</span>
        <span class="break-words text-gray-900 dark:text-white">{{ formatCount(stats.lifetime_tokens) }}</span>
        <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexProfile.peakDailyTokens') }}</span>
        <span class="break-words text-gray-900 dark:text-white">{{ formatCount(stats.peak_daily_tokens) }}</span>
        <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexProfile.longestTurn') }}</span>
        <span class="break-words text-gray-900 dark:text-white">{{ formatDuration(stats.longest_turn_seconds) }}</span>
        <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexProfile.streak') }}</span>
        <span class="break-words text-gray-900 dark:text-white">
          {{ t('admin.accounts.codexProfile.streakValue', { current: stats.current_streak_days ?? 0, longest: stats.longest_streak_days ?? 0 }) }}
        </span>
        <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexProfile.reasoningEffort') }}</span>
        <span class="break-words text-gray-900 dark:text-white">
          {{ stats.reasoning_effort ? `${stats.reasoning_effort} (${formatPercent(stats.reasoning_effort_percent)})` : '-' }}
        </span>
        <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexProfile.fastMode') }}</span>
        <span class="break-words text-gray-900 dark:text-white">{{ formatPercent(stats.fast_mode_percent) }}</span>
        <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexProfile.skillsUsed') }}</span>
        <span class="break-words text-gray-900 dark:text-white">
          {{ t('admin.accounts.codexProfile.skillsUsedValue', { unique: stats.unique_skills_used ?? 0, total: stats.total_skills_used ?? 0 }) }}
        </span>
        <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexProfile.totalThreads') }}</span>
        <span class="break-words text-gray-900 dark:text-white">{{ formatCount(stats.total_threads) }}</span>
        <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexProfile.fetchedAt') }}</span>
        <span class="break-words text-gray-900 dark:text-white">{{ formatDate(stats.fetched_at) }}</span>
      </div>

      <div v-if="stats.top_invocations?.length" class="border-t border-gray-100 pt-3 dark:border-dark-700">
        <p class="mb-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexProfile.topInvocations') }}</p>
        <ul class="space-y-1 text-xs text-gray-900 dark:text-white">
          <li v-for="(inv, idx) in stats.top_invocations" :key="idx" class="flex justify-between gap-2">
            <span class="truncate">{{ inv.skill_name || inv.plugin_name || inv.type }}</span>
            <span class="flex-shrink-0 text-gray-500 dark:text-gray-400">{{ inv.usage_count ?? '-' }}</span>
          </li>
        </ul>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account } from '@/types'
import type { CodexProfileStatistics } from '@/api/admin/accounts'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ account: Account }>()
const { t } = useI18n()

const stats = ref<CodexProfileStatistics | null>(null)
const loading = ref(false)
const error = ref('')

const displayName = computed(() => stats.value?.display_name || stats.value?.username || '')

const formatCount = (value?: number) => typeof value === 'number' && Number.isFinite(value) ? value.toLocaleString() : '-'
const formatPercent = (value?: number) => typeof value === 'number' && Number.isFinite(value) ? `${(value * 100).toFixed(1)}%` : '-'
const formatDuration = (seconds?: number) => {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds)) return '-'
  const minutes = Math.floor(seconds / 60)
  const remaining = Math.round(seconds % 60)
  return minutes > 0 ? `${minutes}m ${remaining}s` : `${remaining}s`
}
const formatDate = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

const load = async (refresh = false) => {
  loading.value = true
  error.value = ''
  try {
    stats.value = await adminAPI.accounts.getProfileStatistics(props.account.id, refresh)
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('admin.accounts.codexProfile.loadFailed'))
  } finally {
    loading.value = false
  }
}

onMounted(() => void load())
</script>
