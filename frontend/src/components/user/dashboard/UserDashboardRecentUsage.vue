<template>
  <div class="card">
    <div class="flex items-center justify-between border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('dashboard.recentUsage') }}</h2>
      <span class="badge badge-gray">{{ t('dashboard.last7Days') }}</span>
    </div>
    <div class="p-6">
      <div v-if="loading" class="flex items-center justify-center py-12">
        <LoadingSpinner size="lg" />
      </div>
      <div v-else-if="data.length === 0" class="py-8">
        <EmptyState :title="t('dashboard.noUsageRecords')" :description="t('dashboard.startUsingApi')" />
      </div>
      <div v-else class="space-y-3">
        <div v-for="log in data" :key="log.id" class="flex items-center justify-between rounded-xl bg-gray-50 p-4 tabular-nums transition-colors hover:bg-gray-100 dark:bg-dark-800/50 dark:hover:bg-dark-800" data-testid="recent-usage-row">
          <div class="flex min-w-0 items-center gap-4">
            <!-- 图标按用途区分：对话 / 代码 / 生图，扫一眼就知道这条是干什么的 -->
            <div class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl" :class="kindStyle[describeModel(log.model).kind].tile" :data-kind="describeModel(log.model).kind">
              <Icon :name="kindStyle[describeModel(log.model).kind].icon" size="md" />
            </div>
            <div class="min-w-0">
              <p class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ log.model }}</p>
              <p class="text-xs text-gray-500 dark:text-dark-400">
                <span v-if="describeModel(log.model).vendor" class="mr-1.5 rounded bg-white px-1.5 py-px text-[11px] font-medium text-gray-600 ring-1 ring-gray-200 dark:bg-dark-900 dark:text-dark-300 dark:ring-dark-700">{{ describeModel(log.model).vendor }}</span>{{ formatDateTime(log.created_at) }}
              </p>
            </div>
          </div>
          <div class="text-right">
            <p class="text-sm font-semibold">
              <span class="text-green-600 dark:text-green-400" :title="t('dashboard.actual')">${{ formatCost(log.actual_cost) }}</span>
              <span class="font-normal text-gray-400 dark:text-gray-500" :title="t('dashboard.standard')"> / ${{ formatCost(log.total_cost) }}</span>
            </p>
            <p class="text-xs text-gray-500 dark:text-dark-400">{{ (log.input_tokens + log.output_tokens).toLocaleString() }} tokens</p>
          </div>
        </div>

        <router-link to="/usage" class="flex items-center justify-center gap-2 py-3 text-sm font-medium text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300">
          {{ t('dashboard.viewAllUsage') }}
          <Icon name="arrowRight" size="sm" />
        </router-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import { describeModel, type ModelKind } from '@/utils/modelKind'
import type { UsageLog } from '@/types'

defineProps<{
  data: UsageLog[]
  loading: boolean
}>()
const { t } = useI18n()
const formatCost = (c: number) => c.toFixed(4)

const kindStyle: Record<ModelKind, { icon: 'chat' | 'terminal' | 'sparkles'; tile: string }> = {
  chat: { icon: 'chat', tile: 'bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300' },
  code: { icon: 'terminal', tile: 'bg-violet-50 text-violet-600 dark:bg-violet-900/30 dark:text-violet-300' },
  image: { icon: 'sparkles', tile: 'bg-amber-50 text-amber-600 dark:bg-amber-900/30 dark:text-amber-300' },
}
</script>
