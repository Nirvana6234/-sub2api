<template>
  <AppLayout>
    <div class="admin-rooms-page mx-auto max-w-7xl p-4 sm:p-6">
      <section class="card admin-room-toolbar">
        <div class="admin-room-toolbar-heading">
          <div class="flex min-w-0 items-center gap-3">
            <span class="admin-room-mark"><Icon name="users" size="md" /></span>
            <div class="min-w-0">
              <h1>{{ t('admin.contributionRooms.title') }}</h1>
              <p>{{ t('admin.contributionRooms.roomsFound', { count: total }) }}</p>
            </div>
          </div>
          <button type="button" class="admin-room-icon-button" :title="t('common.refresh')" :disabled="loading" @click="loadRooms"><Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" /></button>
        </div>

        <form class="admin-room-filters" @submit.prevent="searchRooms">
          <div class="relative min-w-0 flex-1">
            <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
            <input v-model.trim="keywordInput" class="input h-9 min-w-0 pl-9 text-sm" :placeholder="t('admin.contributionRooms.searchPlaceholder')" />
          </div>
          <select v-model="statusFilter" class="input h-9 text-sm" @change="applyFilters">
            <option value="">{{ t('admin.contributionRooms.allStatuses') }}</option>
            <option value="active">{{ t('admin.contributionRooms.statusActive') }}</option>
            <option value="paused">{{ t('admin.contributionRooms.statusPaused') }}</option>
          </select>
          <select v-model="visibilityFilter" class="input h-9 text-sm" @change="applyFilters">
            <option value="">{{ t('admin.contributionRooms.allVisibility') }}</option>
            <option value="public">{{ t('admin.contributionRooms.visibilityPublic') }}</option>
            <option value="private">{{ t('admin.contributionRooms.visibilityPrivate') }}</option>
          </select>
          <button type="submit" class="admin-room-icon-button" :title="t('admin.contributionRooms.search')" :disabled="loading"><Icon name="search" size="sm" /></button>
          <button v-if="keyword" type="button" class="admin-room-icon-button" :title="t('admin.contributionRooms.clearSearch')" :disabled="loading" @click="clearSearch"><Icon name="x" size="sm" /></button>
        </form>
      </section>

      <section class="card admin-room-workspace overflow-hidden">
        <aside class="admin-room-list-panel">
          <div class="admin-room-list-heading">
            <div><h2>{{ t('admin.contributionRooms.roomList') }}</h2><p>{{ t('admin.contributionRooms.pageRooms', { count: rooms.length }) }}</p></div>
          </div>

          <div v-if="loading" class="admin-room-list-loading">
            <div v-for="index in 6" :key="index" class="admin-room-list-skeleton"><span /><span /></div>
          </div>
          <div v-else-if="rooms.length === 0" class="admin-room-list-empty">
            <Icon name="search" size="lg" />
            <span>{{ t('admin.contributionRooms.empty') }}</span>
          </div>
          <div v-else class="admin-room-list">
            <button
              v-for="room in rooms"
              :key="room.id"
              type="button"
              class="admin-room-list-item"
              :class="selectedRoomId === room.id ? 'admin-room-list-item--active' : ''"
              @click="selectRoom(room.id)"
            >
              <span class="flex min-w-0 items-start gap-2.5">
                <span class="room-status-dot" :class="room.status === 'active' ? 'room-status-dot--active' : 'room-status-dot--paused'" />
                <span class="min-w-0 flex-1 text-left">
                  <span class="flex min-w-0 items-center gap-2"><strong class="truncate">{{ room.name }}</strong><span class="room-rate">{{ formatMultiplier(room.consumer_rate_multiplier) }}</span></span>
                  <span class="mt-1 block truncate text-xs text-gray-500 dark:text-gray-400">{{ t('admin.contributionRooms.owner', { name: room.owner.username || `#${room.owner.user_id}` }) }}</span>
                  <span class="mt-2 flex items-center gap-3 text-xs text-gray-500 dark:text-gray-400"><span>{{ t('admin.contributionRooms.accountCount', { count: room.accounts.length }) }}</span><span v-if="roomAttentionCount(room)" class="text-amber-700 dark:text-amber-300">{{ t('admin.contributionRooms.attentionCount', { count: roomAttentionCount(room) }) }}</span></span>
                </span>
              </span>
              <Icon name="chevronRight" size="sm" class="shrink-0 text-gray-400" />
            </button>
          </div>

          <div v-if="total > 0" class="admin-room-pagination">
            <span>{{ t('admin.contributionRooms.pagination', { page, pages: totalPages }) }}</span>
            <div>
              <button type="button" :title="t('admin.contributionRooms.previousPage')" :disabled="page <= 1 || loading" @click="changePage(page - 1)"><Icon name="chevronLeft" size="sm" /></button>
              <button type="button" :title="t('admin.contributionRooms.nextPage')" :disabled="page >= totalPages || loading" @click="changePage(page + 1)"><Icon name="chevronRight" size="sm" /></button>
            </div>
          </div>
        </aside>

        <main class="admin-room-detail-panel">
          <template v-if="selectedRoom">
            <div class="admin-room-detail-header">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <h2>{{ selectedRoom.name }}</h2>
                  <span :class="selectedRoom.status === 'active' ? 'badge badge-success' : 'badge badge-warning'">{{ roomStatusLabel(selectedRoom.status) }}</span>
                  <span :class="selectedRoom.visibility === 'public' ? 'badge badge-primary' : 'badge badge-gray'">{{ roomVisibilityLabel(selectedRoom.visibility) }}</span>
                  <span class="room-rate room-rate--detail">{{ t('admin.contributionRooms.multiplier', { value: formatMultiplier(selectedRoom.consumer_rate_multiplier) }) }}</span>
                </div>
                <p>{{ t('admin.contributionRooms.owner', { name: selectedRoom.owner.username || `#${selectedRoom.owner.user_id}` }) }}</p>
              </div>
              <button class="btn btn-secondary btn-sm" :title="t('admin.contributionRooms.editRoom')" @click="toggleEdit(selectedRoom)"><Icon :name="editingRoomId === selectedRoom.id ? 'x' : 'edit'" size="sm" />{{ editingRoomId === selectedRoom.id ? t('common.cancel') : t('admin.contributionRooms.editRoom') }}</button>
            </div>

            <form v-if="editingRoomId === selectedRoom.id" class="admin-room-edit-form" @submit.prevent="saveRoom(selectedRoom)">
              <label><span>{{ t('admin.contributionRooms.roomName') }}</span><input v-model.trim="editForm.name" class="input h-9 text-sm" required maxlength="100" /></label>
              <label><span>{{ t('admin.contributionRooms.multiplierLabel') }}</span><input v-model.number="editForm.consumerRateMultiplier" type="number" min="0.01" max="100" step="0.01" class="input h-9 text-sm" required /></label>
              <label><span>{{ t('admin.contributionRooms.status') }}</span><select v-model="editForm.status" class="input h-9 text-sm"><option value="active">{{ t('admin.contributionRooms.statusActive') }}</option><option value="paused">{{ t('admin.contributionRooms.statusPaused') }}</option></select></label>
              <label><span>{{ t('admin.contributionRooms.visibility') }}</span><select v-model="editForm.visibility" class="input h-9 text-sm"><option value="public">{{ t('admin.contributionRooms.visibilityPublic') }}</option><option value="private">{{ t('admin.contributionRooms.visibilityPrivate') }}</option></select></label>
              <button type="submit" class="btn btn-primary btn-sm" :disabled="savingRoomId === selectedRoom.id">{{ savingRoomId === selectedRoom.id ? t('common.saving') : t('common.save') }}</button>
            </form>

            <div class="admin-room-metrics">
              <div><span>{{ t('admin.contributionRooms.account') }}</span><strong>{{ selectedRoom.accounts.length }}</strong></div>
              <div><span>{{ t('admin.contributionRooms.activeAccounts') }}</span><strong>{{ roomActiveAccountCount(selectedRoom) }}</strong></div>
              <div><span>{{ t('admin.contributionRooms.totalConcurrency') }}</span><strong>{{ roomConcurrency(selectedRoom) }}</strong></div>
              <div><span>{{ t('admin.contributionRooms.needsAttention') }}</span><strong :class="roomAttentionCount(selectedRoom) ? 'text-amber-700 dark:text-amber-300' : ''">{{ roomAttentionCount(selectedRoom) }}</strong></div>
            </div>

            <div class="admin-room-account-heading">
              <div><h3>{{ t('admin.contributionRooms.roomAccounts') }}</h3><p>{{ t('admin.contributionRooms.accountManagementHint') }}</p></div>
            </div>

            <div v-if="selectedRoom.accounts.length" class="admin-room-account-list">
              <article v-for="account in selectedRoom.accounts" :key="account.account_id" class="admin-room-account-row">
                <div class="min-w-0">
                  <div class="flex min-w-0 flex-wrap items-center gap-2"><strong class="truncate">{{ account.name }}</strong><span :class="account.needs_attention ? 'badge badge-danger' : 'badge badge-success'">{{ account.needs_attention ? t('admin.contributionRooms.needsAttention') : t('admin.contributionRooms.healthy') }}</span></div>
                  <p>#{{ account.account_id }} · {{ account.platform }} / {{ account.type }}</p>
                  <p>{{ t('admin.contributionRooms.budgetUsage', { used: money(account.share_used_usd), budget: money(account.share_budget_usd), remaining: money(account.share_remaining_usd) }) }}</p>
                </div>
                <div class="admin-room-account-status">
                  <span :class="account.enabled ? 'badge badge-success' : 'badge badge-gray'">{{ account.enabled ? t('admin.contributionRooms.enabled') : t('admin.contributionRooms.disabled') }}</span>
                  <small>{{ account.schedulable ? t('admin.contributionRooms.schedulable') : t('admin.contributionRooms.notSchedulable') }} · {{ t('admin.contributionRooms.shareConcurrency', { shared: account.share_concurrency, max: account.concurrency }) }}</small>
                </div>
                <div class="admin-room-account-status">
                  <span :class="account.verification_status === 'verified' ? 'badge badge-success' : 'badge badge-warning'">{{ verificationLabel(account.verification_status) }}</span>
                  <small v-if="account.verification_tested_at">{{ formatDate(account.verification_tested_at) }}</small>
                </div>
                <div class="flex justify-end gap-1.5">
                  <button class="admin-room-icon-button" :title="t('admin.contributionRooms.test')" :disabled="busyKey === keyFor(selectedRoom.id, account.account_id)" @click="testAccount(selectedRoom, account.account_id)"><Icon name="play" size="sm" /></button>
                  <button class="admin-room-icon-button" :title="account.enabled ? t('admin.contributionRooms.disable') : t('admin.contributionRooms.enable')" :disabled="busyKey === keyFor(selectedRoom.id, account.account_id)" @click="toggleAccount(selectedRoom, account.account_id, !account.enabled)"><Icon :name="account.enabled ? 'ban' : 'checkCircle'" size="sm" /></button>
                </div>
              </article>
            </div>
            <div v-else class="admin-room-detail-empty">{{ t('admin.contributionRooms.noAccounts') }}</div>
          </template>

          <div v-else class="admin-room-detail-empty">
            <Icon name="users" size="xl" />
            <strong>{{ t('admin.contributionRooms.selectRoom') }}</strong>
            <span>{{ t('admin.contributionRooms.selectRoomHint') }}</span>
          </div>
        </main>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import type { ContributionRoom } from '@/api/contributionRooms'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const rooms = ref<ContributionRoom[]>([])
const loading = ref(false)
const selectedRoomId = ref<number | null>(null)
const editingRoomId = ref<number | null>(null)
const savingRoomId = ref<number | null>(null)
const busyKey = ref<string | null>(null)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const keywordInput = ref('')
const keyword = ref('')
const statusFilter = ref('')
const visibilityFilter = ref('')
let requestSequence = 0
const editForm = reactive({ name: '', consumerRateMultiplier: 1, status: 'active' as 'active' | 'paused', visibility: 'public' as 'public' | 'private' })
const selectedRoom = computed(() => rooms.value.find((room) => room.id === selectedRoomId.value) || null)
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))

async function loadRooms() {
  const sequence = ++requestSequence
  loading.value = true
  try {
    const result = await adminAPI.contributionRooms.list({
      page: page.value,
      page_size: pageSize.value,
      keyword: keyword.value || undefined,
      status: statusFilter.value || undefined,
      visibility: visibilityFilter.value || undefined,
    })
    if (sequence !== requestSequence) return
    rooms.value = result.items
    total.value = result.total
    page.value = result.page
    pageSize.value = result.page_size
    if (!rooms.value.some((room) => room.id === selectedRoomId.value)) selectedRoomId.value = rooms.value[0]?.id || null
    if (editingRoomId.value !== selectedRoomId.value) editingRoomId.value = null
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.contributionRooms.loadFailed')))
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

function searchRooms() { keyword.value = keywordInput.value; page.value = 1; void loadRooms() }
function clearSearch() { keywordInput.value = ''; keyword.value = ''; page.value = 1; void loadRooms() }
function applyFilters() { page.value = 1; void loadRooms() }
function changePage(value: number) { page.value = value; void loadRooms() }
function selectRoom(roomId: number) { selectedRoomId.value = roomId; editingRoomId.value = null }

function toggleEdit(room: ContributionRoom) {
  if (editingRoomId.value === room.id) { editingRoomId.value = null; return }
  editingRoomId.value = room.id
  editForm.name = room.name
  editForm.consumerRateMultiplier = room.consumer_rate_multiplier
  editForm.status = room.status === 'paused' ? 'paused' : 'active'
  editForm.visibility = room.visibility === 'private' ? 'private' : 'public'
}

async function saveRoom(room: ContributionRoom) {
  savingRoomId.value = room.id
  try {
    await adminAPI.contributionRooms.update(room.id, { name: editForm.name, consumer_rate_multiplier: editForm.consumerRateMultiplier, status: editForm.status, visibility: editForm.visibility })
    editingRoomId.value = null
    appStore.showSuccess(t('admin.contributionRooms.saveSuccess'))
    await loadRooms()
  } catch (error) { appStore.showError(extractApiErrorMessage(error, t('admin.contributionRooms.saveFailed'))) } finally { savingRoomId.value = null }
}

async function toggleAccount(room: ContributionRoom, accountId: number, enabled: boolean) {
  const key = keyFor(room.id, accountId); busyKey.value = key
  try { await adminAPI.contributionRooms.updateAccount(room.id, accountId, enabled); appStore.showSuccess(t('admin.contributionRooms.accountUpdateSuccess')); await loadRooms() } catch (error) { appStore.showError(extractApiErrorMessage(error, t('admin.contributionRooms.accountUpdateFailed'))) } finally { busyKey.value = null }
}

async function testAccount(room: ContributionRoom, accountId: number) {
  const key = keyFor(room.id, accountId); busyKey.value = key
  try {
    const result = await adminAPI.contributionRooms.testAccount(room.id, accountId)
    if (result.status === 'success') appStore.showSuccess(t('admin.contributionRooms.testSuccess'))
    else appStore.showError(result.error_message || t('admin.contributionRooms.testFailed'))
    await loadRooms()
  } catch (error) { appStore.showError(extractApiErrorMessage(error, t('admin.contributionRooms.testFailed'))) } finally { busyKey.value = null }
}

function roomActiveAccountCount(room: ContributionRoom) { return room.accounts.filter((account) => account.enabled && account.schedulable && !account.needs_attention).length }
function roomConcurrency(room: ContributionRoom) { return room.accounts.filter((account) => account.enabled && !account.needs_attention).reduce((sum, account) => sum + Number(account.share_concurrency || 0), 0) }
function roomAttentionCount(room: ContributionRoom) { return room.accounts.filter((account) => account.needs_attention).length }
function keyFor(roomId: number, accountId: number) { return `${roomId}:${accountId}` }
function formatDate(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString() }
function formatMultiplier(value: number) { return `${Number(value || 1).toLocaleString(undefined, { maximumFractionDigits: 2 })}x` }
function money(value: unknown) { return Number(value || 0).toFixed(4) }
function roomStatusLabel(value: string) { return value === 'paused' ? t('admin.contributionRooms.statusPaused') : t('admin.contributionRooms.statusActive') }
function roomVisibilityLabel(value: string) { return value === 'private' ? t('admin.contributionRooms.visibilityPrivate') : t('admin.contributionRooms.visibilityPublic') }
function verificationLabel(value: string) { return value === 'verified' ? t('admin.contributionRooms.verified') : t('admin.contributionRooms.unverified') }

onMounted(() => { void loadRooms() })
</script>

<style scoped>
.admin-room-toolbar {
  display: flex;
  flex-direction: column;
  box-shadow: 0 10px 28px rgb(15 23 42 / 0.045);
}
.admin-room-toolbar-heading { display: flex; min-height: 4.5rem; align-items: center; justify-content: space-between; gap: 1rem; padding: 0.75rem 1rem; }
.admin-room-toolbar h1 { color: #0f172a; font-size: 1rem; font-weight: 750; }
.admin-room-toolbar p { margin-top: 0.125rem; color: #64748b; font-size: 0.75rem; }
.admin-room-mark { display: inline-flex; width: 2.25rem; height: 2.25rem; flex: 0 0 auto; align-items: center; justify-content: center; border: 1px solid rgb(13 148 136 / 0.18); border-radius: 0.5rem; background: #ecfdf5; color: #0f766e; }
.admin-room-filters { display: grid; width: 100%; grid-template-columns: minmax(12rem, 1fr) 10rem 10rem auto auto; gap: 0.5rem; padding: 0.75rem 1rem; border-top: 1px solid #e2e8f0; background: #f8fafc; }
.admin-room-icon-button { display: inline-flex; width: 2.25rem; height: 2.25rem; flex: 0 0 auto; align-items: center; justify-content: center; border: 1px solid #cbd5e1; border-radius: 0.375rem; background: white; color: #475569; transition: border-color 160ms ease, background-color 160ms ease, color 160ms ease; }
.admin-room-icon-button:hover:not(:disabled) { border-color: rgb(13 148 136 / 0.45); background: #f0fdfa; color: #0f766e; }
.admin-room-icon-button:disabled { cursor: wait; opacity: 0.5; }

.admin-room-workspace { display: grid; min-height: 37rem; grid-template-columns: minmax(18rem, 21rem) minmax(0, 1fr); margin-top: 1rem; box-shadow: 0 10px 28px rgb(15 23 42 / 0.045); }
.admin-room-list-panel { min-width: 0; border-right: 1px solid #e2e8f0; background: #f8fafc; }
.admin-room-list-heading { display: flex; min-height: 3.75rem; align-items: center; padding: 0.75rem 1rem; border-bottom: 1px solid #e2e8f0; }
.admin-room-list-heading h2 { color: #0f172a; font-size: 0.8125rem; font-weight: 700; }
.admin-room-list-heading p { margin-top: 0.125rem; color: #64748b; font-size: 0.6875rem; }
.admin-room-list { max-height: 31rem; overflow-y: auto; }
.admin-room-list-item { display: flex; width: 100%; min-height: 5.25rem; align-items: center; justify-content: space-between; gap: 0.5rem; padding: 0.75rem 1rem; border-bottom: 1px solid #e2e8f0; border-left: 3px solid transparent; color: #334155; transition: background-color 160ms ease, border-color 160ms ease; }
.admin-room-list-item:hover { background: white; }
.admin-room-list-item--active { border-left-color: #0f9b8e; background: #f0fdfa; }
.admin-room-list-item strong { color: #1e293b; font-size: 0.8125rem; }
.room-status-dot { width: 0.5rem; height: 0.5rem; flex: 0 0 auto; margin-top: 0.3125rem; border-radius: 999px; box-shadow: 0 0 0 3px rgb(148 163 184 / 0.12); }
.room-status-dot--active { background: #10b981; }
.room-status-dot--paused { background: #f59e0b; }
.room-rate { flex: 0 0 auto; padding: 0.125rem 0.375rem; border: 1px solid #bae6fd; border-radius: 0.25rem; background: #f0f9ff; color: #0369a1; font-size: 0.625rem; font-weight: 750; }
.room-rate--detail { font-size: 0.6875rem; }
.admin-room-list-empty,
.admin-room-list-loading { min-height: 18rem; }
.admin-room-list-empty { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.5rem; padding: 2rem; color: #94a3b8; font-size: 0.75rem; text-align: center; }
.admin-room-list-skeleton { display: flex; min-height: 5.25rem; flex-direction: column; justify-content: center; gap: 0.5rem; padding: 0 1rem; border-bottom: 1px solid #e2e8f0; }
.admin-room-list-skeleton span { width: 72%; height: 0.75rem; border-radius: 0.25rem; background: #e2e8f0; animation: admin-room-pulse 1.4s ease-in-out infinite; }
.admin-room-list-skeleton span:last-child { width: 48%; height: 0.625rem; }
.admin-room-pagination { display: flex; min-height: 3rem; align-items: center; justify-content: space-between; gap: 0.75rem; padding: 0.5rem 1rem; border-top: 1px solid #e2e8f0; color: #64748b; font-size: 0.6875rem; }
.admin-room-pagination > div { display: flex; gap: 0.25rem; }
.admin-room-pagination button { display: inline-flex; width: 1.875rem; height: 1.875rem; align-items: center; justify-content: center; border: 1px solid #cbd5e1; border-radius: 0.375rem; background: white; color: #475569; }
.admin-room-pagination button:hover:not(:disabled) { border-color: rgb(13 148 136 / 0.45); color: #0f766e; }
.admin-room-pagination button:disabled { cursor: not-allowed; opacity: 0.38; }

.admin-room-detail-panel { min-width: 0; background: white; }
.admin-room-detail-header { display: flex; min-height: 4.75rem; align-items: center; justify-content: space-between; gap: 1rem; padding: 1rem 1.25rem; border-bottom: 1px solid #e2e8f0; }
.admin-room-detail-header h2 { color: #0f172a; font-size: 1rem; font-weight: 750; }
.admin-room-detail-header p { margin-top: 0.25rem; color: #64748b; font-size: 0.75rem; }
.admin-room-edit-form { display: grid; grid-template-columns: minmax(10rem, 1.5fr) repeat(3, minmax(7rem, 1fr)) auto; align-items: end; gap: 0.75rem; padding: 1rem 1.25rem; border-bottom: 1px solid #e2e8f0; background: #f8fafc; }
.admin-room-edit-form label > span { display: block; margin-bottom: 0.375rem; color: #64748b; font-size: 0.6875rem; font-weight: 600; }
.admin-room-metrics { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); border-bottom: 1px solid #e2e8f0; }
.admin-room-metrics > div { min-width: 0; padding: 0.75rem 1rem; border-right: 1px solid #e2e8f0; }
.admin-room-metrics > div:last-child { border-right: 0; }
.admin-room-metrics span { display: block; overflow: hidden; color: #64748b; font-size: 0.6875rem; text-overflow: ellipsis; white-space: nowrap; }
.admin-room-metrics strong { display: block; margin-top: 0.25rem; color: #1e293b; font-size: 1rem; font-variant-numeric: tabular-nums; }
.admin-room-account-heading { padding: 0.875rem 1.25rem; border-bottom: 1px solid #e2e8f0; background: #f8fafc; }
.admin-room-account-heading h3 { color: #0f172a; font-size: 0.8125rem; font-weight: 700; }
.admin-room-account-heading p { margin-top: 0.125rem; color: #64748b; font-size: 0.6875rem; }
.admin-room-account-row { display: grid; min-height: 5rem; grid-template-columns: minmax(13rem, 1fr) 9rem 10rem auto; align-items: center; gap: 0.75rem; padding: 0.75rem 1.25rem; border-bottom: 1px solid #e2e8f0; }
.admin-room-account-row strong { color: #1e293b; font-size: 0.8125rem; }
.admin-room-account-row p { margin-top: 0.1875rem; color: #64748b; font-size: 0.6875rem; line-height: 1.35; }
.admin-room-account-status { display: flex; flex-direction: column; align-items: flex-start; gap: 0.375rem; }
.admin-room-account-status small { color: #64748b; font-size: 0.6875rem; line-height: 1.35; }
.admin-room-detail-empty { display: flex; min-height: 24rem; flex-direction: column; align-items: center; justify-content: center; gap: 0.5rem; padding: 2rem; color: #94a3b8; font-size: 0.75rem; text-align: center; }
.admin-room-detail-empty strong { color: #475569; font-size: 0.875rem; }

@keyframes admin-room-pulse { 0%, 100% { opacity: 0.55; } 50% { opacity: 1; } }

:global(.dark .admin-room-toolbar h1),
:global(.dark .admin-room-list-heading h2),
:global(.dark .admin-room-list-item strong),
:global(.dark .admin-room-detail-header h2),
:global(.dark .admin-room-account-heading h3),
:global(.dark .admin-room-account-row strong),
:global(.dark .admin-room-metrics strong){ color: #f8fafc; }
:global(.dark .admin-room-mark){ border-color: rgb(45 212 191 / 0.2); background: rgb(19 78 74 / 0.35); color: #5eead4; }
:global(.dark .admin-room-list-panel),
:global(.dark .admin-room-edit-form),
:global(.dark .admin-room-account-heading),
:global(.dark .admin-room-filters){ background: #111827; }
:global(.dark .admin-room-list-panel),
:global(.dark .admin-room-filters),
:global(.dark .admin-room-list-heading),
:global(.dark .admin-room-list-item),
:global(.dark .admin-room-pagination),
:global(.dark .admin-room-detail-header),
:global(.dark .admin-room-edit-form),
:global(.dark .admin-room-metrics),
:global(.dark .admin-room-metrics > div),
:global(.dark .admin-room-account-heading),
:global(.dark .admin-room-account-row){ border-color: #334155; }
:global(.dark .admin-room-detail-panel),
:global(.dark .admin-room-icon-button),
:global(.dark .admin-room-pagination button){ background: #172033; }
:global(.dark .admin-room-icon-button){ border-color: #475569; color: #cbd5e1; }
:global(.dark .admin-room-pagination button){ border-color: #475569; color: #cbd5e1; }
:global(.dark .admin-room-list-item:hover){ background: rgb(30 41 59 / 0.7); }
:global(.dark .admin-room-list-item--active){ background: rgb(19 78 74 / 0.28); }
:global(.dark .room-rate){ border-color: rgb(14 116 144 / 0.55); background: rgb(8 47 73 / 0.55); color: #7dd3fc; }
:global(.dark .admin-room-list-skeleton span){ background: #334155; }

@media (max-width: 1199px) {
  .admin-room-filters { width: 100%; grid-template-columns: repeat(2, minmax(0, 1fr)) repeat(3, 2.25rem); }
  .admin-room-filters > div { grid-column: 1 / 3; }
  .admin-room-filters select { grid-row: 2; width: 100%; }
  .admin-room-filters select:first-of-type { grid-column: 1; }
  .admin-room-filters select:last-of-type { grid-column: 2; }
  .admin-room-workspace { grid-template-columns: minmax(0, 1fr); }
  .admin-room-list-panel { border-right: 0; border-bottom: 1px solid #e2e8f0; }
  .admin-room-list { max-height: 20rem; }
  .admin-room-edit-form { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .admin-room-edit-form button { justify-self: end; }
}

@media (max-width: 767px) {
  .admin-rooms-page { padding: 1rem; }
  .admin-room-detail-header { align-items: flex-start; }
  .admin-room-edit-form { grid-template-columns: minmax(0, 1fr); }
  .admin-room-edit-form button { justify-self: stretch; }
  .admin-room-metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .admin-room-metrics > div:nth-child(2) { border-right: 0; }
  .admin-room-metrics > div:nth-child(-n+2) { border-bottom: 1px solid #e2e8f0; }
  .admin-room-account-row { grid-template-columns: minmax(0, 1fr) auto; }
  .admin-room-account-row > div:first-child { grid-column: 1 / -1; }
  .admin-room-account-row > div:last-child { align-self: end; }
}
</style>
