<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex flex-wrap justify-end gap-3">
          <div class="flex flex-wrap gap-x-4 gap-y-1 text-sm text-gray-600 dark:text-dark-300">
            <span v-for="item in summaryItems" :key="item.label">
              <span class="text-gray-500 dark:text-dark-400">{{ item.label }}</span>
              <span class="ml-1 font-semibold text-gray-900 dark:text-gray-100">{{ item.value }}</span>
            </span>
          </div>
          <button type="button" class="btn btn-primary" @click="openManagedCreateDialog">
            <Icon name="plus" size="sm" />
            {{ t('admin.contributions.addManaged') }}
          </button>
        </div>
      </template>

      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <div class="min-w-52 flex-1 sm:max-w-md">
            <SearchInput
              v-model="filters.search"
              :placeholder="t('admin.contributions.searchPlaceholder')"
              @search="applyFilters"
            />
          </div>
          <div class="w-full sm:w-40">
            <Select v-model="filters.platform" :options="platformOptions" @change="applyFilters" />
          </div>
          <div class="w-full sm:w-36">
            <Select v-model="filters.status" :options="statusOptions" @change="applyFilters" />
          </div>
          <div class="w-full sm:w-40">
            <Select v-model="filters.shareMode" :options="shareModeOptions" @change="applyFilters" />
          </div>
          <button
            type="button"
            class="btn btn-secondary px-3"
            :disabled="loading"
            :title="t('common.refresh')"
            @click="loadContributions"
          >
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="items" :loading="loading" :actions-count="5">
          <template #cell-account="{ row }">
            <div class="max-w-56">
              <p class="truncate font-medium text-gray-900 dark:text-gray-100" :title="row.name">
                {{ row.name || `#${row.id}` }}
              </p>
              <p class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400">
                {{ t('admin.contributions.platformAndType', { platform: row.platform, type: row.type }) }}
              </p>
            </div>
          </template>

          <template #cell-contributor="{ row }">
            <div class="max-w-48">
              <p class="truncate font-medium text-gray-800 dark:text-gray-200">
                {{ row.contributor.username || row.contributor.email }}
              </p>
              <p v-if="row.contributor.username" class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400">
                {{ row.contributor.email }}
              </p>
            </div>
          </template>

          <template #cell-scheduling="{ row }">
            <div class="flex flex-col items-start gap-1.5">
              <span :class="['badge', statusClass(row.status)]">{{ statusLabel(row.status) }}</span>
              <span :class="['badge', governanceClass(row.governance_state)]">
                {{ governanceLabel(row.governance_state) }}
              </span>
              <span class="text-xs text-gray-500 dark:text-dark-400">
                {{ row.schedulable ? t('admin.contributions.schedulable') : t('admin.contributions.notSchedulable') }}
              </span>
              <span v-if="row.concurrency !== null" class="text-xs text-gray-500 dark:text-dark-400">
                {{ t('admin.contributions.concurrency', { count: row.concurrency }) }}
              </span>
            </div>
          </template>

          <template #cell-sharing="{ row }">
            <div class="min-w-36 space-y-1 text-xs text-gray-600 dark:text-dark-300">
              <span :class="['badge', shareClass(row.share.mode)]">{{ shareModeLabel(row.share.mode) }}</span>
              <p>{{ budgetSummary(row.share.used_total, row.share.total_budget, 'totalBudget') }}</p>
              <p>{{ budgetSummary(row.share.used_today, row.share.daily_budget, 'dailyBudget') }}</p>
            </div>
          </template>

          <template #header-usage="{ column }">
            <div class="flex items-center">
              <span>{{ column.label }}</span>
              <HelpTooltip :content="t('admin.contributions.usageWindowsHint')" width-class="w-72" />
            </div>
          </template>

          <template #cell-usage="{ row }">
            <div class="min-w-[188px] space-y-1">
              <div v-if="isUsageLoading(row)" class="space-y-1.5 py-1">
                <div v-for="window in 2" :key="window" class="flex items-center gap-1">
                  <span class="h-4 w-8 animate-pulse rounded bg-gray-200 dark:bg-dark-600" />
                  <span class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-dark-600" />
                  <span class="h-4 w-8 animate-pulse rounded bg-gray-200 dark:bg-dark-600" />
                </div>
              </div>
              <template v-else-if="usageSummaries[String(row.id)]">
                <template v-if="isLocalUsage(row)">
                  <div class="space-y-0.5 text-xs text-gray-600 dark:text-dark-300">
                    <span class="inline-flex rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-medium text-slate-600 dark:bg-dark-700 dark:text-dark-300">
                      {{ t('admin.contributions.localUsage') }}
                    </span>
                    <p>{{ t('admin.contributions.last30Days') }} {{ formatTokens(usageSummaries[String(row.id)]?.stats?.summary?.total_tokens) }}</p>
                    <p>{{ t('admin.contributions.usageRequestsCost', { requests: formatNumber(usageSummaries[String(row.id)]?.stats?.summary?.total_requests), cost: money(usageSummaries[String(row.id)]?.stats?.summary?.total_cost) }) }}</p>
                  </div>
                  <button
                    type="button"
                    class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
                    :disabled="isUsageLoading(row)"
                    @click="loadUsageSummary(row, true)"
                  >
                    <Icon name="refresh" size="xs" />
                    {{ t('admin.contributions.usageQuery') }}
                  </button>
                </template>
                <template v-else>
                  <UsageProgressBar
                    v-if="usageSummaries[String(row.id)]?.upstream?.five_hour"
                    label="5h"
                    :utilization="usageSummaries[String(row.id)]?.upstream?.five_hour?.utilization ?? 0"
                    :resets-at="usageSummaries[String(row.id)]?.upstream?.five_hour?.resets_at"
                    :window-stats="usageSummaries[String(row.id)]?.upstream?.five_hour?.window_stats"
                    :show-now-when-idle="true"
                    color="indigo"
                  />
                  <UsageProgressBar
                    v-if="usageSummaries[String(row.id)]?.upstream?.seven_day"
                    label="7d"
                    :utilization="usageSummaries[String(row.id)]?.upstream?.seven_day?.utilization ?? 0"
                    :resets-at="usageSummaries[String(row.id)]?.upstream?.seven_day?.resets_at"
                    :window-stats="usageSummaries[String(row.id)]?.upstream?.seven_day?.window_stats"
                    :show-now-when-idle="true"
                    color="emerald"
                  />
                  <p
                    v-if="!usageSummaries[String(row.id)]?.upstream?.five_hour && !usageSummaries[String(row.id)]?.upstream?.seven_day"
                    class="text-xs text-gray-400 dark:text-dark-500"
                  >
                    {{ t('admin.contributions.windowUnavailable') }}
                  </p>
                  <button
                    type="button"
                    class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
                    :disabled="isUsageLoading(row)"
                    @click="loadUsageSummary(row, true)"
                  >
                    <Icon name="refresh" size="xs" />
                    {{ t('admin.contributions.usageQuery') }}
                  </button>
                </template>
              </template>
              <div v-else class="space-y-1">
                <p v-if="usageErrors[String(row.id)]" class="truncate text-xs text-red-600 dark:text-red-400" :title="usageErrors[String(row.id)]">
                  {{ usageErrors[String(row.id)] }}
                </p>
                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 transition-colors hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
                  @click="loadUsageSummary(row)"
                >
                  <Icon name="refresh" size="xs" />
                  {{ t('admin.contributions.usageQuery') }}
                </button>
              </div>
            </div>
          </template>

          <template #cell-health="{ row }">
            <div class="max-w-52">
              <span :class="['badge', healthClass(row.health.error_message)]">
                {{ healthLabel(row.health.error_message) }}
              </span>
              <p v-if="row.health.error_message" class="mt-1 truncate text-xs text-red-600 dark:text-red-400" :title="row.health.error_message">
                {{ row.health.error_message }}
              </p>
              <p v-else-if="row.health.last_used_at" class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                {{ t('admin.contributions.lastUsed', { date: formatDate(row.health.last_used_at) }) }}
              </p>
            </div>
          </template>

          <template #cell-submitted="{ row }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{ formatDate(row.submitted_at) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                type="button"
                class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-800 dark:hover:bg-dark-700 dark:hover:text-gray-100"
                :aria-label="t('admin.contributions.editManaged')"
                :title="t('admin.contributions.editManaged')"
                @click="openManagedEditDialog(row)"
              >
                <Icon name="edit" size="sm" />
              </button>
              <HelpTooltip :content="t('admin.contributions.detailsHint')" width-class="w-52">
                <template #trigger>
                  <button
                    type="button"
                    class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-800 dark:hover:bg-dark-700 dark:hover:text-gray-100"
                    :aria-label="t('admin.contributions.details')"
                    :title="t('admin.contributions.details')"
                    @click="openContributionDetails(row)"
                  >
                    <Icon name="eye" size="sm" />
                  </button>
                </template>
              </HelpTooltip>
              <HelpTooltip :content="t('admin.contributions.testHint')" width-class="w-56">
                <template #trigger>
                  <button
                    type="button"
                    class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-blue-50 hover:text-blue-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-blue-900/20 dark:hover:text-blue-400"
                    :disabled="busyId === String(row.id)"
                    :aria-label="busyId === String(row.id) ? t('admin.contributions.testing') : t('admin.contributions.test')"
                    :title="busyId === String(row.id) ? t('admin.contributions.testing') : t('admin.contributions.test')"
                    @click="testContribution(row)"
                  >
                    <Icon name="play" size="sm" :class="busyId === String(row.id) && busyAction === 'test' ? 'animate-spin' : ''" />
                  </button>
                </template>
              </HelpTooltip>
              <HelpTooltip :content="isPaused(row) ? t('admin.contributions.resumeHint') : t('admin.contributions.pauseHint')" width-class="w-60">
                <template #trigger>
                  <button
                    type="button"
                    :class="[
                      'rounded-lg p-2 transition-colors disabled:cursor-not-allowed disabled:opacity-50',
                      isPaused(row) ? 'text-emerald-600 hover:bg-emerald-50 dark:text-emerald-400 dark:hover:bg-emerald-900/20' : 'text-amber-600 hover:bg-amber-50 dark:text-amber-400 dark:hover:bg-amber-900/20'
                    ]"
                    :disabled="busyId === String(row.id)"
                    :aria-label="isPaused(row) ? t('admin.contributions.resume') : t('admin.contributions.pause')"
                    :title="isPaused(row) ? t('admin.contributions.resume') : t('admin.contributions.pause')"
                    @click="changeStatus(row)"
                  >
                    <Icon :name="isPaused(row) ? 'play' : 'ban'" size="sm" :class="busyId === String(row.id) && busyAction === 'status' ? 'animate-spin' : ''" />
                  </button>
                </template>
              </HelpTooltip>
              <HelpTooltip :content="t('admin.contributions.deleteHint')" width-class="w-52">
                <template #trigger>
                  <button
                    type="button"
                    class="rounded-lg p-2 text-red-600 transition-colors hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-red-400 dark:hover:bg-red-900/20"
                    :disabled="busyId === String(row.id)"
                    :aria-label="t('admin.contributions.delete')"
                    :title="t('admin.contributions.delete')"
                    @click="deleteTarget = row"
                  >
                    <Icon name="trash" size="sm" />
                  </button>
                </template>
              </HelpTooltip>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.contributions.emptyTitle')"
              :description="t('admin.contributions.emptyDescription')"
            />
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.pageSize"
          @update:page="changePage"
          @update:page-size="changePageSize"
        />
      </template>
    </TablePageLayout>

    <CreateAccountModal
      :show="managedCreateOpen"
      :proxies="managedProxies"
      :groups="managedGroups"
      :contributor-user-id="managedForm.contributorUserId || null"
      :contributor-users="managedUsers"
      @update:contributor-user-id="managedForm.contributorUserId = $event"
      @search-contributor-users="searchManagedUsers"
      @close="closeManagedCreateDialog"
      @created="handleManagedCreated"
    />

    <BaseDialog
      :show="managedDialogMode === 'edit'"
      :title="t('admin.contributions.editManaged')"
      width="wide"
      @close="closeManagedDialog"
    >
      <form class="grid gap-4 sm:grid-cols-2" @submit.prevent="saveManagedContribution">
        <label v-if="managedDialogMode === 'create'" class="block">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.contributions.ownerUserId') }}</span>
          <input v-model.number="managedForm.contributorUserId" type="number" min="1" required class="input w-full" :placeholder="t('admin.contributions.ownerUserIdPlaceholder')" />
        </label>
        <div v-else class="block">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.contributions.owner') }}</span>
          <p class="input flex items-center bg-gray-50 text-sm text-gray-700 dark:bg-dark-800 dark:text-gray-200">{{ managedEditing?.contributor.username || managedEditing?.contributor.email || `#${managedEditing?.contributor.id}` }}</p>
        </div>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.contributions.managedName') }}</span>
          <input v-model.trim="managedForm.name" required maxlength="100" class="input w-full" />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.contributions.platform') }}</span>
          <select v-model="managedForm.platform" class="input w-full" :disabled="managedDialogMode === 'edit'">
            <option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="gemini">Gemini</option><option value="grok">Grok</option>
          </select>
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ managedDialogMode === 'create' ? t('admin.contributions.apiKey') : t('admin.contributions.newApiKey') }}</span>
          <input v-model.trim="managedForm.apiKey" type="password" autocomplete="off" class="input w-full" :required="managedDialogMode === 'create'" :placeholder="managedDialogMode === 'edit' ? t('admin.contributions.connectionOptional') : ''" />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.contributions.baseUrl') }}</span>
          <input v-model.trim="managedForm.baseUrl" class="input w-full" :placeholder="managedDialogMode === 'edit' ? t('admin.contributions.connectionOptional') : 'https://api.example.com'" />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.contributions.concurrency', { count: '' }) }}</span>
          <input v-model.number="managedForm.concurrency" type="number" min="1" max="1000" class="input w-full" />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.contributions.priority') }}</span>
          <input v-model.number="managedForm.priority" type="number" min="0" max="100000" class="input w-full" />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.contributions.loadFactor') }}</span>
          <input v-model.number="managedForm.loadFactor" type="number" min="0" max="10000" class="input w-full" />
        </label>
        <label v-if="managedDialogMode === 'edit'" class="block">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.contributions.status') }}</span>
          <select v-model="managedForm.status" class="input w-full"><option value="active">{{ t('admin.contributions.statusActive') }}</option><option value="disabled">{{ t('admin.contributions.statusDisabled') }}</option></select>
        </label>
        <label class="block sm:col-span-2">
          <span class="mb-1 block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.contributions.groups') }}</span>
          <select v-model="managedForm.groupIds" multiple size="4" class="input w-full">
            <option v-for="group in managedGroupsForPlatform" :key="group.id" :value="group.id" :disabled="group.require_oauth_only">{{ group.name }}</option>
          </select>
          <span class="mt-1 block text-xs text-gray-500 dark:text-dark-400">{{ t('admin.contributions.managedGroupsHint') }}</span>
        </label>
        <div class="sm:col-span-2 flex justify-end gap-3 pt-2">
          <button type="button" class="btn btn-secondary" :disabled="managedSaving" @click="closeManagedDialog">{{ t('common.cancel') }}</button>
          <button type="submit" class="btn btn-primary" :disabled="managedSaving">{{ managedSaving ? t('common.saving') : t('common.save') }}</button>
        </div>
      </form>
    </BaseDialog>

    <BaseDialog
      :show="selectedContribution !== null"
      :title="t('admin.contributions.detailTitle')"
      width="wide"
      @close="selectedContribution = null"
    >
      <div v-if="selectedContribution" class="space-y-6">
        <section class="detail-section">
          <h2>{{ t('admin.contributions.accountDetails') }}</h2>
          <dl class="detail-grid">
            <DetailRow :label="t('admin.contributions.accountId')" :value="String(selectedContribution.id)" />
            <DetailRow :label="t('admin.contributions.platform')" :value="selectedContribution.platform" />
            <DetailRow :label="t('admin.contributions.type')" :value="selectedContribution.type" />
            <DetailRow :label="t('admin.contributions.status')" :value="statusLabel(selectedContribution.status)" />
            <DetailRow :label="t('admin.contributions.governanceState')" :value="governanceLabel(selectedContribution.governance_state)" />
            <DetailRow :label="t('admin.contributions.pauseReason')" :value="selectedContribution.governance_reason || '-'" />
            <DetailRow :label="t('admin.contributions.schedulable')" :value="selectedContribution.schedulable ? t('common.yes') : t('common.no')" />
            <DetailRow :label="t('admin.contributions.concurrency', { count: '' })" :value="formatNumber(selectedContribution.concurrency)" />
            <DetailRow :label="t('admin.contributions.groups')" :value="groupsText(selectedContribution.groups)" />
            <DetailRow :label="t('admin.contributions.submittedAt')" :value="formatDate(selectedContribution.submitted_at)" />
          </dl>
        </section>

        <section class="detail-section">
          <h2>{{ t('admin.contributions.contributorDetails') }}</h2>
          <dl class="detail-grid">
            <DetailRow :label="t('admin.contributions.email')" :value="selectedContribution.contributor.email" />
            <DetailRow :label="t('admin.contributions.username')" :value="selectedContribution.contributor.username || '-'" />
          </dl>
        </section>

        <section class="detail-section">
          <h2>{{ t('admin.contributions.sharingDetails') }}</h2>
          <dl class="detail-grid">
            <DetailRow :label="t('admin.contributions.status')" :value="shareModeLabel(selectedContribution.share.mode)" />
            <DetailRow :label="t('admin.contributions.totalUsage')" :value="formatNumber(selectedContribution.share.used_total)" />
            <DetailRow :label="t('admin.contributions.todayUsage')" :value="formatNumber(selectedContribution.share.used_today)" />
            <DetailRow :label="t('admin.contributions.totalBudgetLabel')" :value="formatNumber(selectedContribution.share.total_budget)" />
            <DetailRow :label="t('admin.contributions.dailyBudgetLabel')" :value="formatNumber(selectedContribution.share.daily_budget)" />
            <DetailRow :label="t('admin.contributions.expiresAtLabel')" :value="formatDate(selectedContribution.share.expires_at)" />
          </dl>
        </section>

        <section class="detail-section">
          <h2>{{ t('admin.contributions.healthDetails') }}</h2>
          <dl class="detail-grid">
            <DetailRow :label="t('admin.contributions.lastUsedAt')" :value="formatDate(selectedContribution.health.last_used_at)" />
            <DetailRow :label="t('admin.contributions.healthStatus')" :value="healthLabel(selectedContribution.health.error_message)" />
            <DetailRow :label="t('admin.contributions.errorMessage')" :value="selectedContribution.health.error_message || t('admin.contributions.noError')" />
          </dl>
        </section>
      </div>
    </BaseDialog>

    <BaseDialog
      :show="pauseTarget !== null"
      :title="t('admin.contributions.pauseTitle')"
      width="narrow"
      @close="closePauseDialog"
    >
      <div class="space-y-4">
        <p class="text-sm text-gray-600 dark:text-dark-300">
          {{ t('admin.contributions.pauseDescription', { name: pauseTarget?.name || `#${pauseTarget?.id || ''}` }) }}
        </p>
        <div>
          <label for="contribution-pause-reason" class="mb-1.5 block text-sm font-medium text-gray-800 dark:text-gray-200">
            {{ t('admin.contributions.pauseReason') }}
          </label>
          <textarea
            id="contribution-pause-reason"
            v-model="pauseReason"
            rows="3"
            maxlength="300"
            class="input w-full resize-y"
            :placeholder="t('admin.contributions.pauseReasonPlaceholder')"
          />
        </div>
      </div>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" :disabled="busyAction === 'status'" @click="closePauseDialog">
            {{ t('common.cancel') }}
          </button>
          <button type="button" class="btn btn-warning" :disabled="!pauseReason.trim() || busyAction === 'status'" @click="confirmPause">
            {{ busyAction === 'status' ? t('admin.contributions.pausing') : t('admin.contributions.pause') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="deleteTarget !== null"
      :title="t('admin.contributions.deleteTitle')"
      width="narrow"
      @close="closeDeleteDialog"
    >
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">
        {{ t('admin.contributions.deleteDescription', { name: deleteTarget?.name || `#${deleteTarget?.id || ''}` }) }}
      </p>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" :disabled="busyAction === 'delete'" @click="closeDeleteDialog">
            {{ t('common.cancel') }}
          </button>
          <button type="button" class="btn btn-danger" :disabled="busyAction === 'delete'" @click="confirmDelete">
            {{ busyAction === 'delete' ? t('common.loading') : t('admin.contributions.delete') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AdminContribution, ContributionGroup, ContributionUsageSummary } from '@/api/admin'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useAppStore } from '@/stores/app'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Pagination from '@/components/common/Pagination.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import UsageProgressBar from '@/components/account/UsageProgressBar.vue'
import type { AdminGroup, AdminUser, Proxy } from '@/types'
import { CreateAccountModal } from '@/components/account'

const { t } = useI18n()
const appStore = useAppStore()

const DetailRow = defineComponent({
  props: {
    label: { type: String, required: true },
    value: { type: String, required: true }
  },
  setup(props) {
    return () => h('div', [
      h('dt', { class: 'text-xs font-medium text-gray-500 dark:text-dark-400' }, props.label),
      h('dd', { class: 'mt-1 break-words text-sm text-gray-900 dark:text-gray-100' }, props.value || '-')
    ])
  }
})

const items = ref<AdminContribution[]>([])
const loading = ref(false)
const selectedContribution = ref<AdminContribution | null>(null)
const usageLoadingIds = reactive(new Set<string>())
const usageSummaries = reactive<Record<string, ContributionUsageSummary | undefined>>({})
const usageErrors = reactive<Record<string, string | undefined>>({})
const pauseTarget = ref<AdminContribution | null>(null)
const pauseReason = ref('')
const deleteTarget = ref<AdminContribution | null>(null)
const managedDialogMode = ref<'create' | 'edit' | null>(null)
const managedCreateOpen = ref(false)
const managedEditing = ref<AdminContribution | null>(null)
const managedSaving = ref(false)
const managedGroups = ref<AdminGroup[]>([])
const managedProxies = ref<Proxy[]>([])
const managedUsers = ref<AdminUser[]>([])
const managedForm = reactive({ contributorUserId: 0, name: '', platform: 'openai' as 'openai' | 'anthropic' | 'gemini' | 'grok', apiKey: '', baseUrl: '', concurrency: 3, priority: 0, loadFactor: 0, groupIds: [] as number[], status: 'active' as 'active' | 'disabled' })
const busyId = ref<string | null>(null)
const busyAction = ref<'test' | 'status' | 'delete' | null>(null)
const summary = ref<Record<string, unknown>>({})
const filters = reactive({ search: '', platform: '', status: '', shareMode: '' })
const pagination = reactive({ page: 1, pageSize: getPersistedPageSize(), total: 0 })

const platformOptions = computed(() => {
  const standardPlatforms = ['openai', 'anthropic', 'gemini', 'grok', 'claude', 'codex']
  const platforms = new Set([...standardPlatforms, ...items.value.map((item) => item.platform).filter(Boolean)])
  return [
    { value: '', label: t('admin.contributions.allPlatforms') },
    ...Array.from(platforms).sort().map((platform) => ({ value: platform, label: platform }))
  ]
})

const statusOptions = computed(() => [
  { value: '', label: t('admin.contributions.allStatuses') },
  { value: 'active', label: t('admin.contributions.statusActive') },
  { value: 'paused', label: t('admin.contributions.statusPaused') },
  { value: 'disabled', label: t('admin.contributions.statusDisabled') },
  { value: 'error', label: t('admin.contributions.statusError') }
])

const shareModeOptions = computed(() => [
  { value: '', label: t('admin.contributions.allShareModes') },
  { value: 'private', label: t('admin.contributions.sharePrivate') },
  { value: 'pool', label: t('admin.contributions.sharePool') }
])

const managedGroupsForPlatform = computed(() => managedGroups.value.filter((group) => group.platform === managedForm.platform))

const columns = computed<Column[]>(() => [
  { key: 'account', label: t('admin.contributions.columns.account') },
  { key: 'contributor', label: t('admin.contributions.columns.contributor') },
  { key: 'scheduling', label: t('admin.contributions.columns.scheduling') },
  { key: 'sharing', label: t('admin.contributions.columns.sharing') },
  { key: 'usage', label: t('admin.contributions.columns.usage') },
  { key: 'health', label: t('admin.contributions.columns.health') },
  { key: 'submitted', label: t('admin.contributions.columns.submitted') },
  { key: 'actions', label: t('admin.contributions.columns.actions') }
])

const summaryItems = computed(() => {
  const valueFor = (keys: string[], fallback?: number) => {
    for (const key of keys) {
      const value = summary.value[key]
      if (typeof value === 'number') return value
    }
    return fallback
  }
  return [
    { label: t('admin.contributions.total'), value: valueFor(['total'], pagination.total) },
    { label: t('admin.contributions.shared'), value: valueFor(['shared']) },
    { label: t('admin.contributions.attention'), value: valueFor(['attention']) },
    { label: t('admin.contributions.paused'), value: valueFor(['paused', 'paused_count']) },
  ].filter((item): item is { label: string; value: number } => typeof item.value === 'number')
})

let currentController: AbortController | null = null
let usageAutoLoadGeneration = 0
const usageAutoLoadConcurrency = 4

async function loadContributions() {
  currentController?.abort()
  const usageGeneration = ++usageAutoLoadGeneration
  const controller = new AbortController()
  currentController = controller
  loading.value = true
  try {
    const response = await adminAPI.contributions.list(
      pagination.page,
      pagination.pageSize,
      {
        search: filters.search || undefined,
        platform: filters.platform || undefined,
        status: filters.status || undefined,
        share_mode: filters.shareMode || undefined
      },
      { signal: controller.signal }
    )
    if (controller.signal.aborted || currentController !== controller) return
    items.value = response.items
    pagination.total = response.total
    pagination.page = response.page
    pagination.pageSize = response.page_size
    summary.value = response.summary || {}
    void autoLoadUsageSummaries(response.items, usageGeneration)
  } catch (error) {
    if (!controller.signal.aborted) appStore.showError(errorMessage(error, t('admin.contributions.loadFailed')))
  } finally {
    if (currentController === controller) {
      loading.value = false
      currentController = null
    }
  }
}

function applyFilters() {
  pagination.page = 1
  void loadContributions()
}

function changePage(page: number) {
  pagination.page = page
  void loadContributions()
}

function changePageSize(pageSize: number) {
  pagination.pageSize = pageSize
  pagination.page = 1
  void loadContributions()
}

function resetManagedForm() {
  managedForm.contributorUserId = 0
  managedForm.name = ''
  managedForm.platform = 'openai'
  managedForm.apiKey = ''
  managedForm.baseUrl = ''
  managedForm.concurrency = 3
  managedForm.priority = 0
  managedForm.loadFactor = 0
  managedForm.groupIds = []
  managedForm.status = 'active'
}

async function openManagedCreateDialog() {
  resetManagedForm()
  managedEditing.value = null
  try {
    const [groups, proxies, users] = await Promise.all([
      adminAPI.groups.getAll(),
      adminAPI.proxies.getAll(),
      adminAPI.users.list(1, 100, { status: 'active', sort_by: 'email', sort_order: 'asc' })
    ])
    managedGroups.value = groups
    managedProxies.value = proxies
    managedUsers.value = users.items
    managedCreateOpen.value = true
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.contributions.groupsLoadFailed')))
  }
}

let managedUserSearchTimer: ReturnType<typeof setTimeout> | null = null

function searchManagedUsers(value: string) {
  if (managedUserSearchTimer) clearTimeout(managedUserSearchTimer)
  managedUserSearchTimer = setTimeout(async () => {
    try {
      const users = await adminAPI.users.list(1, 100, {
        status: 'active',
        search: value.trim() || undefined,
        sort_by: 'email',
        sort_order: 'asc'
      })
      managedUsers.value = users.items
    } catch (error) {
      appStore.showError(errorMessage(error, t('admin.contributions.groupsLoadFailed')))
    }
  }, 180)
}

function closeManagedCreateDialog() {
  managedCreateOpen.value = false
  resetManagedForm()
}

async function handleManagedCreated() {
  managedCreateOpen.value = false
  resetManagedForm()
  await loadContributions()
}

async function openManagedEditDialog(contribution: AdminContribution) {
  resetManagedForm()
  managedEditing.value = contribution
  managedForm.name = contribution.name
  managedForm.platform = contribution.platform as typeof managedForm.platform
  managedForm.concurrency = contribution.concurrency || 3
  managedForm.groupIds = contribution.groups.map((group) => typeof group === 'string' ? 0 : Number(group.id)).filter((id) => id > 0)
  managedForm.status = contribution.status === 'disabled' ? 'disabled' : 'active'
  managedDialogMode.value = 'edit'
  try {
    managedGroups.value = await adminAPI.groups.getAll()
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.contributions.groupsLoadFailed')))
  }
}

function closeManagedDialog() {
  if (managedSaving.value) return
  managedDialogMode.value = null
  managedEditing.value = null
  resetManagedForm()
}

async function saveManagedContribution() {
  if (!managedDialogMode.value) return
  managedSaving.value = true
  try {
    if (managedEditing.value) {
      const payload: {
        name: string
        concurrency: number
        priority: number
        load_factor: number
        group_ids: number[]
        status: 'active' | 'disabled'
        api_key?: string
        base_url?: string
      } = {
        name: managedForm.name,
        concurrency: managedForm.concurrency,
        priority: managedForm.priority,
        load_factor: managedForm.loadFactor,
        group_ids: managedForm.groupIds,
        status: managedForm.status,
      }
      if (managedForm.apiKey) payload.api_key = managedForm.apiKey
      if (managedForm.baseUrl) payload.base_url = managedForm.baseUrl
      await adminAPI.contributions.updateManaged(managedEditing.value.id, payload)
      appStore.showSuccess(t('admin.contributions.managedUpdateSuccess'))
    }
    managedDialogMode.value = null
    managedEditing.value = null
    resetManagedForm()
    await loadContributions()
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.contributions.managedSaveFailed')))
  } finally {
    managedSaving.value = false
  }
}

async function changeStatus(contribution: AdminContribution) {
  if (!isPaused(contribution)) {
    pauseTarget.value = contribution
    pauseReason.value = ''
    return
  }
  await runAction(contribution, 'status', async () => {
    await adminAPI.contributions.update(contribution.id, { action: 'resume' })
    appStore.showSuccess(t('admin.contributions.resumeSuccess'))
  }, t('admin.contributions.actionFailed'))
}

async function confirmPause() {
  const contribution = pauseTarget.value
  const reason = pauseReason.value.trim()
  if (!contribution || !reason) return
  await runAction(contribution, 'status', async () => {
    await adminAPI.contributions.update(contribution.id, { action: 'pause', reason })
    closePauseDialog()
    appStore.showSuccess(t('admin.contributions.pauseSuccess'))
  }, t('admin.contributions.actionFailed'))
}

function closePauseDialog() {
  if (busyAction.value === 'status') return
  pauseTarget.value = null
  pauseReason.value = ''
}

function closeDeleteDialog() {
  if (busyAction.value === 'delete') return
  deleteTarget.value = null
}

async function confirmDelete() {
  const contribution = deleteTarget.value
  if (!contribution) return
  await runAction(contribution, 'delete', async () => {
    await adminAPI.contributions.delete(contribution.id)
    deleteTarget.value = null
    if (selectedContribution.value?.id === contribution.id) selectedContribution.value = null
    appStore.showSuccess(t('admin.contributions.deleteSuccess'))
  }, t('admin.contributions.deleteFailed'))
}

async function testContribution(contribution: AdminContribution) {
  await runAction(contribution, 'test', async () => {
    await adminAPI.contributions.test(contribution.id)
    appStore.showSuccess(t('admin.contributions.testSuccess'))
  }, t('admin.contributions.testRequestFailed'))
}

function openContributionDetails(contribution: AdminContribution) {
  selectedContribution.value = contribution
}

async function loadUsageSummary(contribution: AdminContribution, force = false) {
  const id = String(contribution.id)
  if (usageLoadingIds.has(id)) return
  usageLoadingIds.add(id)
  delete usageErrors[id]
  try {
    usageSummaries[id] = await adminAPI.contributions.getUsageSummary(contribution.id, force)
  } catch (error) {
    usageErrors[id] = errorMessage(error, t('admin.contributions.usageLoadFailed'))
  } finally {
    usageLoadingIds.delete(id)
  }
}

async function autoLoadUsageSummaries(contributions: AdminContribution[], generation: number) {
  if (!contributions.length || generation !== usageAutoLoadGeneration) return
  let nextIndex = 0
  const workerCount = Math.min(usageAutoLoadConcurrency, contributions.length)

  const worker = async () => {
    while (generation === usageAutoLoadGeneration) {
      const index = nextIndex++
      if (index >= contributions.length) return
      await loadUsageSummary(contributions[index])
    }
  }

  await Promise.all(Array.from({ length: workerCount }, () => worker()))
}

function isUsageLoading(contribution: AdminContribution) {
  return usageLoadingIds.has(String(contribution.id))
}

async function runAction(
  contribution: AdminContribution,
  action: 'test' | 'status' | 'delete',
  request: () => Promise<unknown>,
  failedMessage: string
) {
  busyId.value = String(contribution.id)
  busyAction.value = action
  try {
    await request()
    await loadContributions()
  } catch (error) {
    appStore.showError(errorMessage(error, failedMessage))
  } finally {
    busyId.value = null
    busyAction.value = null
  }
}

function isPaused(contribution: AdminContribution) {
  return String(contribution.governance_state).toLowerCase() === 'paused'
}

function statusLabel(status: string | null | undefined) {
  const normalized = String(status || '').toLowerCase()
  const labels: Record<string, string> = {
    active: t('admin.contributions.statusActive'),
    paused: t('admin.contributions.statusPaused'),
    disabled: t('admin.contributions.statusDisabled'),
    inactive: t('admin.contributions.statusPaused'),
    error: t('admin.contributions.statusError')
  }
  return labels[normalized] || status || '-'
}

function statusClass(status: string) {
  const normalized = status.toLowerCase()
  if (normalized === 'active') return 'badge-success'
  if (normalized === 'paused' || normalized === 'inactive') return 'badge-warning'
  if (normalized === 'error' || normalized === 'disabled') return 'badge-danger'
  return 'badge-gray'
}

function governanceLabel(state: string | null | undefined) {
  return String(state).toLowerCase() === 'paused'
    ? t('admin.contributions.governancePaused')
    : t('admin.contributions.governanceActive')
}

function governanceClass(state: string | null | undefined) {
  return String(state).toLowerCase() === 'paused' ? 'badge-warning' : 'badge-success'
}

function shareModeLabel(mode: string | null | undefined) {
  if (String(mode).toLowerCase() === 'pool') return t('admin.contributions.sharePool')
  if (String(mode).toLowerCase() === 'private') return t('admin.contributions.sharePrivate')
  return mode || '-'
}

function shareClass(mode: string | null | undefined) {
  return String(mode).toLowerCase() === 'pool' ? 'badge-primary' : 'badge-gray'
}

function healthLabel(errorMessage: string | null | undefined) {
  return errorMessage ? t('admin.contributions.healthNeedsAttention') : t('admin.contributions.healthNormal')
}

function healthClass(errorMessage: string | null | undefined) {
  return errorMessage ? 'badge-danger' : 'badge-success'
}

function budgetSummary(used: number | null, budget: number | null, key: 'totalBudget' | 'dailyBudget') {
  if (budget === null || budget === undefined) return t('admin.contributions.noBudget')
  return t(`admin.contributions.${key}`, { used: formatNumber(used), budget: formatNumber(budget) })
}

function formatNumber(value: number | null | undefined) {
  if (value === null || value === undefined || !Number.isFinite(value)) return '-'
  return value.toLocaleString(undefined, { maximumFractionDigits: 4 })
}

function formatTokens(value: number | null | undefined) {
  const tokens = Number(value || 0)
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(2)}M`
  if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(1)}K`
  return formatNumber(tokens)
}

function money(value: number | null | undefined) {
  return Number(value || 0).toFixed(4)
}

function isLocalUsage(contribution: AdminContribution) {
  return String(contribution.type).toLowerCase() === 'apikey'
}

function formatDate(value: string | null | undefined) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function groupsText(groups: Array<ContributionGroup | string>) {
  if (!groups?.length) return t('admin.contributions.noGroups')
  return groups.map(groupName).join(', ')
}

function groupName(group: ContributionGroup | string) {
  return typeof group === 'string' ? group : group.name || String(group.id || '-')
}

function errorMessage(error: unknown, fallback: string) {
  const response = (error as { response?: { data?: { detail?: unknown } } })?.response
  const detail = response?.data?.detail
  if (typeof detail === 'string' && detail) return detail
  const message = (error as { message?: unknown })?.message
  return typeof message === 'string' && message ? message : fallback
}

onMounted(() => {
  void loadContributions()
})

onBeforeUnmount(() => {
  currentController?.abort()
  if (managedUserSearchTimer) clearTimeout(managedUserSearchTimer)
  usageAutoLoadGeneration++
})
</script>

<style scoped>
.detail-section h2 {
  @apply text-sm font-semibold text-gray-900 dark:text-gray-100;
}

.detail-grid {
  @apply mt-3 grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2;
}
</style>
