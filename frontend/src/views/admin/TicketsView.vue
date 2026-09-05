<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useTicketStore } from '@/stores'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import type { AdminTicket, TicketStatus } from '@/types'

const { t } = useI18n()
const ticketStore = useTicketStore()

const tickets = ref<AdminTicket[]>([])
const selected = ref<AdminTicket | null>(null)
const loadingList = ref(false)
const loadingDetail = ref(false)
const submitting = ref(false)
const batchOperating = ref(false)
const errorMessage = ref('')

const page = ref(1)
const pageSize = 20
const total = ref(0)
const statusFilter = ref<TicketStatus | ''>('')
const searchTerm = ref('')
const unreadOnly = ref(false)
const selectedIds = ref<Set<number>>(new Set())

const replyContent = ref('')

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const canReply = computed(() => selected.value !== null && selected.value.status !== 'closed')
const selectedCount = computed(() => selectedIds.value.size)
const selectedVisibleCount = computed(() => tickets.value.filter((ticket) => selectedIds.value.has(ticket.id)).length)
const allVisibleSelected = computed(() => tickets.value.length > 0 && selectedVisibleCount.value === tickets.value.length)
const someVisibleSelected = computed(() => selectedVisibleCount.value > 0 && !allVisibleSelected.value)

const statusLabels: Record<TicketStatus, string> = {
  open: 'tickets.status.open',
  answered: 'tickets.status.answered',
  closed: 'tickets.status.closed'
}

function statusClass(status: TicketStatus): string {
  switch (status) {
    case 'open':
      return 'bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300'
    case 'answered':
      return 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-dark-300'
  }
}

async function loadList() {
  loadingList.value = true
  errorMessage.value = ''
  try {
    const res = await adminAPI.tickets.list(page.value, pageSize, {
      status: statusFilter.value || undefined,
      search: searchTerm.value.trim() || undefined,
      unread_only: unreadOnly.value || undefined
    })
    tickets.value = res.items || []
    total.value = res.total || 0
    selectedIds.value = new Set(tickets.value.filter((ticket) => selectedIds.value.has(ticket.id)).map((ticket) => ticket.id))
  } catch (error) {
    errorMessage.value = resolveError(error)
  } finally {
    loadingList.value = false
  }
}

async function openTicket(ticket: AdminTicket) {
  loadingDetail.value = true
  errorMessage.value = ''
  replyContent.value = ''
  try {
    selected.value = await adminAPI.tickets.getById(ticket.id)
    // 详情接口已清零管理员侧未读，这里同步列表角标。
    const row = tickets.value.find((item) => item.id === ticket.id)
    if (row) {
      row.unread_count = 0
    }
    void ticketStore.refreshUnreadCounts()
  } catch (error) {
    errorMessage.value = resolveError(error)
  } finally {
    loadingDetail.value = false
  }
}

async function submitReply() {
  if (submitting.value || !selected.value) return
  submitting.value = true
  errorMessage.value = ''
  try {
    const message = await adminAPI.tickets.reply(selected.value.id, replyContent.value)
    selected.value.messages = [...(selected.value.messages || []), message]
    selected.value.status = 'answered'
    replyContent.value = ''
    await loadList()
  } catch (error) {
    errorMessage.value = resolveError(error)
  } finally {
    submitting.value = false
  }
}

async function changeStatus(status: TicketStatus) {
  if (!selected.value) return
  submitting.value = true
  errorMessage.value = ''
  try {
    await adminAPI.tickets.updateStatus(selected.value.id, status)
    selected.value.status = status
    await loadList()
  } catch (error) {
    errorMessage.value = resolveError(error)
  } finally {
    submitting.value = false
  }
}

function onSelectionChange(ticketID: number, event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  const next = new Set(selectedIds.value)
  if (checked) {
    next.add(ticketID)
  } else {
    next.delete(ticketID)
  }
  selectedIds.value = next
}

function onSelectAllChange(event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  const next = new Set(selectedIds.value)
  for (const ticket of tickets.value) {
    if (checked) {
      next.add(ticket.id)
    } else {
      next.delete(ticket.id)
    }
  }
  selectedIds.value = next
}

function clearSelection() {
  selectedIds.value = new Set()
}

async function setSelectedReadStatus(read: boolean) {
  const ids = [...selectedIds.value]
  if (ids.length === 0 || batchOperating.value) return

  batchOperating.value = true
  errorMessage.value = ''
  try {
    await adminAPI.tickets.batchReadStatus(ids, read)
    const selectedSet = new Set(ids)
    for (const ticket of tickets.value) {
      if (selectedSet.has(ticket.id)) {
        ticket.unread_count = read ? 0 : 1
      }
    }
    if (selected.value && selectedSet.has(selected.value.id)) {
      selected.value = { ...selected.value, unread_count: read ? 0 : 1 }
    }
    clearSelection()
    void ticketStore.refreshUnreadCounts()
  } catch (error) {
    errorMessage.value = resolveError(error)
  } finally {
    batchOperating.value = false
  }
}

async function deleteSelected() {
  const ids = [...selectedIds.value]
  if (ids.length === 0 || batchOperating.value) return

  if (!window.confirm(t('tickets.deleteConfirm', { count: ids.length }))) return

  batchOperating.value = true
  errorMessage.value = ''
  try {
    await adminAPI.tickets.batchDelete(ids)
    const deletedSet = new Set(ids)
    if (selected.value && deletedSet.has(selected.value.id)) {
      selected.value = null
      replyContent.value = ''
    }
    clearSelection()
    if (page.value > 1 && tickets.value.every((ticket) => deletedSet.has(ticket.id))) {
      page.value -= 1
    }
    await loadList()
    void ticketStore.refreshUnreadCounts()
  } catch (error) {
    errorMessage.value = resolveError(error)
  } finally {
    batchOperating.value = false
  }
}

function changePage(next: number) {
  if (next < 1 || next > totalPages.value) return
  clearSelection()
  page.value = next
  loadList()
}

function applyFilters() {
  clearSelection()
  page.value = 1
  loadList()
}

function resolveError(error: unknown): string {
  const err = error as { response?: { data?: { message?: string } }; message?: string }
  return err?.response?.data?.message || err?.message || t('tickets.genericError')
}

onMounted(loadList)
</script>

<template>
  <AppLayout>
    <div class="space-y-4">
      <div>
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('tickets.adminTitle') }}</h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('tickets.adminDescription') }}</p>
      </div>

      <p
        v-if="errorMessage"
        class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300"
      >
        {{ errorMessage }}
      </p>

      <div class="grid gap-4 lg:grid-cols-[minmax(0,400px)_minmax(0,1fr)]">
        <!-- 列表 -->
        <div class="card min-w-0 p-4">
          <div class="mb-3 space-y-2">
            <input
              v-model="searchTerm"
              type="search"
              :placeholder="t('tickets.searchPlaceholder')"
              class="input py-1.5 text-sm"
              @keyup.enter="applyFilters"
            />
            <div class="flex flex-wrap items-center gap-2">
              <select v-model="statusFilter" class="input py-1.5 text-sm" @change="applyFilters">
                <option value="">{{ t('tickets.filterAll') }}</option>
                <option value="open">{{ t('tickets.status.open') }}</option>
                <option value="answered">{{ t('tickets.status.answered') }}</option>
                <option value="closed">{{ t('tickets.status.closed') }}</option>
              </select>
              <label class="flex items-center gap-1.5 text-sm text-gray-600 dark:text-dark-300">
                <input v-model="unreadOnly" type="checkbox" class="rounded" @change="applyFilters" />
                {{ t('tickets.unreadOnly') }}
              </label>
            </div>
          </div>

          <p v-if="loadingList" class="py-8 text-center text-sm text-gray-500 dark:text-dark-400">
            {{ t('tickets.loading') }}
          </p>
          <p v-else-if="tickets.length === 0" class="py-8 text-center text-sm text-gray-500 dark:text-dark-400">
            {{ t('tickets.empty') }}
          </p>
          <template v-else>
            <div class="mb-3 flex flex-wrap items-center justify-between gap-2 border-y border-gray-200 py-2 dark:border-dark-700">
              <label class="flex items-center gap-2 text-sm text-gray-600 dark:text-dark-300">
                <input
                  type="checkbox"
                  class="rounded"
                  :checked="allVisibleSelected"
                  :indeterminate="someVisibleSelected"
                  @change="onSelectAllChange"
                />
                {{ t('tickets.selectAll') }}
              </label>
              <div v-if="selectedCount > 0" class="flex flex-wrap items-center gap-1.5">
                <span class="mr-1 text-xs text-gray-500 dark:text-dark-400">
                  {{ t('tickets.selectedCount', { count: selectedCount }) }}
                </span>
                <button
                  type="button"
                  class="btn btn-secondary inline-flex items-center gap-1.5 px-2.5 py-1 text-xs"
                  :disabled="batchOperating"
                  :title="t('tickets.markRead')"
                  @click="setSelectedReadStatus(true)"
                >
                  <Icon name="checkCircle" size="sm" />
                  <span>{{ t('tickets.markRead') }}</span>
                </button>
                <button
                  type="button"
                  class="btn btn-secondary inline-flex items-center gap-1.5 px-2.5 py-1 text-xs"
                  :disabled="batchOperating"
                  :title="t('tickets.markUnread')"
                  @click="setSelectedReadStatus(false)"
                >
                  <Icon name="mail" size="sm" />
                  <span>{{ t('tickets.markUnread') }}</span>
                </button>
                <button
                  type="button"
                  class="btn btn-danger inline-flex items-center gap-1.5 px-2.5 py-1 text-xs"
                  :disabled="batchOperating"
                  :title="t('tickets.deleteSelected')"
                  @click="deleteSelected"
                >
                  <Icon name="trash" size="sm" />
                  <span>{{ t('tickets.deleteSelected') }}</span>
                </button>
              </div>
            </div>

            <p v-if="batchOperating" class="mb-2 text-xs text-gray-500 dark:text-dark-400">
              {{ t('tickets.actionInProgress') }}
            </p>
            <ul class="space-y-2">
              <li v-for="ticket in tickets" :key="ticket.id" class="flex items-start gap-2">
                <label class="flex shrink-0 cursor-pointer items-center px-1 pt-3" @click.stop>
                  <input
                    type="checkbox"
                    class="rounded"
                    :checked="selectedIds.has(ticket.id)"
                    @change="onSelectionChange(ticket.id, $event)"
                  />
                </label>
                <button
                  type="button"
                  class="min-w-0 flex-1 rounded-lg border px-3 py-2.5 text-left transition-colors"
                  :class="
                    selected?.id === ticket.id
                      ? 'border-primary-400 bg-primary-50 dark:border-primary-600 dark:bg-primary-900/20'
                      : 'border-gray-200 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-800'
                  "
                  @click="openTicket(ticket)"
                >
                  <div class="flex items-start justify-between gap-2">
                    <span class="min-w-0 flex-1 truncate text-sm font-medium text-gray-900 dark:text-white">
                      {{ ticket.subject }}
                    </span>
                    <span
                      v-if="ticket.unread_count > 0"
                      class="shrink-0 rounded-full bg-red-500 px-1.5 text-xs font-semibold text-white"
                    >
                      {{ ticket.unread_count }}
                    </span>
                  </div>
                  <p class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-400">{{ ticket.user_email }}</p>
                  <div class="mt-2 flex items-center justify-between gap-2">
                    <span class="rounded-full px-2 py-0.5 text-xs font-medium" :class="statusClass(ticket.status)">
                      {{ t(statusLabels[ticket.status]) }}
                    </span>
                    <span class="text-xs text-gray-400 dark:text-dark-500">
                      {{ formatDateTime(ticket.last_message_at) }}
                    </span>
                  </div>
                </button>
              </li>
            </ul>
          </template>

          <div v-if="totalPages > 1" class="mt-4 flex items-center justify-between text-sm">
            <button type="button" class="btn btn-secondary px-3 py-1" :disabled="page <= 1" @click="changePage(page - 1)">
              {{ t('tickets.prev') }}
            </button>
            <span class="text-gray-500 dark:text-dark-400">{{ page }} / {{ totalPages }}</span>
            <button
              type="button"
              class="btn btn-secondary px-3 py-1"
              :disabled="page >= totalPages"
              @click="changePage(page + 1)"
            >
              {{ t('tickets.next') }}
            </button>
          </div>
        </div>

        <!-- 会话 -->
        <div class="card min-w-0 p-5">
          <p v-if="!selected" class="py-16 text-center text-sm text-gray-500 dark:text-dark-400">
            {{ t('tickets.selectHint') }}
          </p>
          <template v-else>
            <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700">
              <div class="min-w-0">
                <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ selected.subject }}</h2>
                <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                  {{ selected.user_email }} · {{ t('tickets.createdAt') }} {{ formatDateTime(selected.created_at) }}
                </p>
              </div>
              <div class="flex items-center gap-2">
                <span class="rounded-full px-2 py-0.5 text-xs font-medium" :class="statusClass(selected.status)">
                  {{ t(statusLabels[selected.status]) }}
                </span>
                <button
                  v-if="selected.status !== 'closed'"
                  type="button"
                  class="btn btn-secondary px-3 py-1 text-xs"
                  :disabled="submitting"
                  @click="changeStatus('closed')"
                >
                  {{ t('tickets.close') }}
                </button>
                <button
                  v-else
                  type="button"
                  class="btn btn-secondary px-3 py-1 text-xs"
                  :disabled="submitting"
                  @click="changeStatus('open')"
                >
                  {{ t('tickets.reopen') }}
                </button>
              </div>
            </div>

            <p v-if="loadingDetail" class="py-8 text-center text-sm text-gray-500 dark:text-dark-400">
              {{ t('tickets.loading') }}
            </p>
            <div v-else class="mt-4 space-y-4">
              <div
                v-for="message in selected.messages || []"
                :key="message.id"
                class="flex"
                :class="message.sender_role === 'admin' ? 'justify-end' : 'justify-start'"
              >
                <div
                  class="max-w-[85%] rounded-2xl px-4 py-3"
                  :class="
                    message.sender_role === 'admin'
                      ? 'bg-primary-600 text-white'
                      : 'bg-gray-100 text-gray-900 dark:bg-dark-800 dark:text-dark-100'
                  "
                >
                  <p class="text-xs font-medium opacity-80">
                    {{ message.sender_role === 'admin' ? t('tickets.support') : t('tickets.customer') }}
                  </p>
                  <p class="mt-1 whitespace-pre-wrap break-words text-sm leading-6">{{ message.content }}</p>
                  <p class="mt-1.5 text-xs opacity-70">{{ formatDateTime(message.created_at) }}</p>
                </div>
              </div>
            </div>

            <form v-if="canReply" class="mt-5 border-t border-gray-200 pt-4 dark:border-dark-700" @submit.prevent="submitReply">
              <textarea
                v-model="replyContent"
                required
                rows="4"
                maxlength="10000"
                :placeholder="t('tickets.adminReplyPlaceholder')"
                class="input"
              ></textarea>
              <button type="submit" class="btn btn-primary mt-3" :disabled="submitting">
                {{ submitting ? t('tickets.submitting') : t('tickets.reply') }}
              </button>
            </form>
            <p v-else class="mt-5 border-t border-gray-200 pt-4 text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400">
              {{ t('tickets.adminClosedHint') }}
            </p>
          </template>
        </div>
      </div>
    </div>
  </AppLayout>
</template>
