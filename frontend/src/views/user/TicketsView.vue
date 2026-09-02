<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ticketsAPI } from '@/api'
import { useTicketStore } from '@/stores'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import type { Ticket, TicketStatus } from '@/types'

const { t } = useI18n()
const ticketStore = useTicketStore()

const tickets = ref<Ticket[]>([])
const selected = ref<Ticket | null>(null)
const loadingList = ref(false)
const loadingDetail = ref(false)
const submitting = ref(false)
const errorMessage = ref('')

const page = ref(1)
const pageSize = 20
const total = ref(0)
const statusFilter = ref<TicketStatus | ''>('')

const composerOpen = ref(false)
const newSubject = ref('')
const newContent = ref('')
const replyContent = ref('')

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const canReply = computed(() => selected.value !== null && selected.value.status !== 'closed')

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
    const res = await ticketsAPI.list(page.value, pageSize, {
      status: statusFilter.value || undefined
    })
    tickets.value = res.items || []
    total.value = res.total || 0
  } catch (error) {
    errorMessage.value = resolveError(error)
  } finally {
    loadingList.value = false
  }
}

async function openTicket(ticket: Ticket) {
  loadingDetail.value = true
  errorMessage.value = ''
  replyContent.value = ''
  try {
    selected.value = await ticketsAPI.getById(ticket.id)
    // 后端在返回详情时已把未读清零，这里同步列表里的角标，避免要刷新才消失。
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

async function submitTicket() {
  if (submitting.value) return
  submitting.value = true
  errorMessage.value = ''
  try {
    const created = await ticketsAPI.create({
      subject: newSubject.value,
      content: newContent.value
    })
    newSubject.value = ''
    newContent.value = ''
    composerOpen.value = false
    page.value = 1
    await loadList()
    await openTicket(created)
  } catch (error) {
    errorMessage.value = resolveError(error)
  } finally {
    submitting.value = false
  }
}

async function submitReply() {
  if (submitting.value || !selected.value) return
  submitting.value = true
  errorMessage.value = ''
  try {
    const message = await ticketsAPI.reply(selected.value.id, replyContent.value)
    selected.value.messages = [...(selected.value.messages || []), message]
    selected.value.status = 'open'
    replyContent.value = ''
    await loadList()
  } catch (error) {
    errorMessage.value = resolveError(error)
  } finally {
    submitting.value = false
  }
}

async function closeTicket() {
  if (!selected.value) return
  submitting.value = true
  errorMessage.value = ''
  try {
    await ticketsAPI.close(selected.value.id)
    selected.value.status = 'closed'
    await loadList()
  } catch (error) {
    errorMessage.value = resolveError(error)
  } finally {
    submitting.value = false
  }
}

function changePage(next: number) {
  if (next < 1 || next > totalPages.value) return
  page.value = next
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
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('tickets.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('tickets.description') }}</p>
        </div>
        <button type="button" class="btn btn-primary inline-flex items-center gap-2" @click="composerOpen = true">
          <Icon name="plus" size="sm" />
          {{ t('tickets.create') }}
        </button>
      </div>

      <p
        v-if="errorMessage"
        class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300"
      >
        {{ errorMessage }}
      </p>

      <!-- 新建工单 -->
      <div v-if="composerOpen" class="card p-5">
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('tickets.create') }}</h2>
        <form class="mt-4 space-y-4" @submit.prevent="submitTicket">
          <div>
            <label for="ticket-subject" class="input-label">{{ t('tickets.subject') }}</label>
            <input
              id="ticket-subject"
              v-model="newSubject"
              type="text"
              required
              maxlength="200"
              :placeholder="t('tickets.subjectPlaceholder')"
              class="input mt-1"
            />
          </div>
          <div>
            <label for="ticket-content" class="input-label">{{ t('tickets.content') }}</label>
            <textarea
              id="ticket-content"
              v-model="newContent"
              required
              rows="6"
              maxlength="10000"
              :placeholder="t('tickets.contentPlaceholder')"
              class="input mt-1"
            ></textarea>
          </div>
          <div class="flex items-center gap-3">
            <button type="submit" class="btn btn-primary" :disabled="submitting">
              {{ submitting ? t('tickets.submitting') : t('tickets.submit') }}
            </button>
            <button type="button" class="btn btn-secondary" :disabled="submitting" @click="composerOpen = false">
              {{ t('tickets.cancel') }}
            </button>
          </div>
        </form>
      </div>

      <div class="grid gap-4 lg:grid-cols-[minmax(0,360px)_minmax(0,1fr)]">
        <!-- 列表 -->
        <div class="card min-w-0 p-4">
          <div class="mb-3 flex items-center gap-2">
            <select v-model="statusFilter" class="input py-1.5 text-sm" @change="changePage(1)">
              <option value="">{{ t('tickets.filterAll') }}</option>
              <option value="open">{{ t('tickets.status.open') }}</option>
              <option value="answered">{{ t('tickets.status.answered') }}</option>
              <option value="closed">{{ t('tickets.status.closed') }}</option>
            </select>
          </div>

          <p v-if="loadingList" class="py-8 text-center text-sm text-gray-500 dark:text-dark-400">
            {{ t('tickets.loading') }}
          </p>
          <p v-else-if="tickets.length === 0" class="py-8 text-center text-sm text-gray-500 dark:text-dark-400">
            {{ t('tickets.empty') }}
          </p>
          <ul v-else class="space-y-2">
            <li v-for="ticket in tickets" :key="ticket.id">
              <button
                type="button"
                class="w-full rounded-lg border px-3 py-2.5 text-left transition-colors"
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
                <p class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400">
                  {{ ticket.last_message_preview }}
                </p>
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
                  {{ t('tickets.createdAt') }} {{ formatDateTime(selected.created_at) }}
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
                  @click="closeTicket"
                >
                  {{ t('tickets.close') }}
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
                :class="message.sender_role === 'user' ? 'justify-end' : 'justify-start'"
              >
                <div
                  class="max-w-[85%] rounded-2xl px-4 py-3"
                  :class="
                    message.sender_role === 'user'
                      ? 'bg-primary-600 text-white'
                      : 'bg-gray-100 text-gray-900 dark:bg-dark-800 dark:text-dark-100'
                  "
                >
                  <p class="text-xs font-medium opacity-80">
                    {{ message.sender_role === 'user' ? t('tickets.you') : t('tickets.support') }}
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
                :placeholder="t('tickets.replyPlaceholder')"
                class="input"
              ></textarea>
              <button type="submit" class="btn btn-primary mt-3" :disabled="submitting">
                {{ submitting ? t('tickets.submitting') : t('tickets.reply') }}
              </button>
            </form>
            <p v-else class="mt-5 border-t border-gray-200 pt-4 text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400">
              {{ t('tickets.closedHint') }}
            </p>
          </template>
        </div>
      </div>
    </div>
  </AppLayout>
</template>
