<template>
  <AppLayout>
    <div class="shared-rooms-page mx-auto max-w-7xl p-4 sm:p-6">
      <header class="shared-rooms-header">
        <div class="flex min-w-0 items-center gap-3">
          <span class="shared-rooms-brand-mark"><Icon name="users" size="md" /></span>
          <div class="min-w-0">
            <h1>{{ t('sharedRooms.title') }}</h1>
            <p>{{ t('sharedRooms.description') }}</p>
          </div>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <span v-if="preferenceSaving" class="shared-save-state"><Icon name="sync" size="xs" class="animate-spin" />{{ t('sharedRooms.saving') }}</span>
          <span v-else-if="selectedAPIKey" class="selected-count">{{ t('sharedRooms.selectedRooms', { count: preference.room_ids.length }) }}</span>
          <button class="shared-rooms-icon-button" :title="t('common.refresh')" :disabled="loading || !selectedAPIKeyID" @click="loadRooms">
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
      </header>

      <section class="card shared-rooms-workspace overflow-hidden">
        <aside class="shared-rooms-sidebar">
          <div class="shared-config-section">
            <div class="shared-config-heading">
              <span class="shared-config-icon"><Icon name="key" size="sm" /></span>
              <div><h2>{{ t('sharedRooms.apiKeyStepTitle') }}</h2><p>{{ t('sharedRooms.apiKeyStepHint') }}</p></div>
            </div>
            <div v-if="activeAPIKeys.length" class="mt-3">
              <div v-if="activeAPIKeys.length > 5" class="relative mb-2">
                <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
                <input v-model.trim="apiKeyKeyword" class="input h-9 pl-9 text-sm" :placeholder="t('sharedRooms.apiKeySearchPlaceholder')" />
              </div>
              <div class="shared-api-key-list" role="list">
                <button
                  v-for="apiKey in filteredAPIKeys"
                  :key="apiKey.id"
                  type="button"
                  class="shared-api-key-item"
                  :class="selectedAPIKeyID === apiKey.id ? 'shared-api-key-item--active' : ''"
                  :disabled="keysLoading || preferenceSaving"
                  @click="selectAPIKey(apiKey.id)"
                >
                  <span class="shared-api-key-mark"><Icon name="key" size="xs" /></span>
                  <span class="min-w-0 flex-1 text-left">
                    <strong class="block truncate">{{ apiKey.name }}</strong>
                    <small class="block truncate">{{ apiKey.group?.name || t('sharedRooms.apiKeyNoGroup') }}</small>
                  </span>
                  <Icon v-if="selectedAPIKeyID === apiKey.id" name="checkCircle" size="sm" class="shrink-0" />
                </button>
              </div>
              <p v-if="filteredAPIKeys.length === 0" class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('sharedRooms.apiKeySearchEmpty') }}</p>
            </div>
            <div v-else-if="!keysLoading" class="mt-3 space-y-3 text-sm text-gray-500 dark:text-gray-400">
              <p>{{ t('sharedRooms.noAPIKeys') }}</p>
              <RouterLink to="/keys" class="btn btn-secondary btn-sm"><Icon name="plus" size="sm" />{{ t('sharedRooms.createAPIKey') }}</RouterLink>
            </div>
          </div>

          <template v-if="selectedAPIKeyID">
            <div class="shared-config-section">
              <div class="shared-config-heading">
                <span class="shared-config-icon shared-config-icon--fallback"><Icon name="shield" size="sm" /></span>
                <div><h2>{{ t('sharedRooms.fallbackStepTitle') }}</h2><p>{{ t('sharedRooms.fallbackStepHint') }}</p></div>
              </div>

              <label class="fallback-toggle-row mt-3" :class="preferenceSaving ? 'fallback-toggle-row--disabled' : ''">
                <span class="min-w-0"><span class="block text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('sharedRooms.allowPoolFallback') }}</span><span class="mt-0.5 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('sharedRooms.allowPoolFallbackHint') }}</span></span>
                <span class="shared-switch">
                  <input v-model="preference.allow_pool_fallback" type="checkbox" :disabled="preferenceSaving" />
                  <span class="shared-switch-track"><span class="shared-switch-thumb" /></span>
                </span>
              </label>

              <div class="mt-3">
                <label class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('sharedRooms.fallbackGroupLabel') }}</label>
                <select v-model.number="fallbackGroupSelection" class="input h-10 text-sm" :disabled="preferenceSaving || groupsLoading">
                  <option :value="0">{{ groupsLoading ? t('common.loading') : t('sharedRooms.fallbackGroupPlaceholder') }}</option>
                  <option v-for="group in fallbackGroups" :key="group.id" :value="group.id">{{ group.name }}</option>
                </select>
                <p class="mt-1.5 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('sharedRooms.fallbackGroupHint') }}</p>
              </div>
            </div>

            <div class="shared-selection-summary">
              <div class="flex items-center justify-between gap-3">
                <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('sharedRooms.currentSelection') }}</span>
                <span class="shared-auto-save" :class="preferenceDirty ? 'shared-auto-save--dirty' : ''"><Icon :name="preferenceDirty ? 'edit' : 'checkCircle'" size="xs" />{{ preferenceDirty ? t('sharedRooms.unsavedChanges') : t('sharedRooms.savedState') }}</span>
              </div>
              <strong>{{ preference.room_ids.length }}</strong>
              <span class="mt-0.5 block text-xs text-gray-500 dark:text-gray-400">{{ t('sharedRooms.roomsSelectedUnit') }}</span>
              <div class="mt-3 flex items-center justify-between gap-3 border-t border-gray-200 pt-3 text-xs dark:border-dark-600">
                <span class="text-gray-500 dark:text-gray-400">{{ t('sharedRooms.fallbackStatus') }}</span>
                <span class="truncate font-medium text-gray-700 dark:text-gray-200">{{ fallbackStatusLabel }}</span>
              </div>
              <button class="btn btn-primary mt-3 w-full" :disabled="preferenceSaving || !preferenceDirty" @click="saveRoomPreference"><Icon name="check" size="sm" />{{ preferenceSaving ? t('common.saving') : t('sharedRooms.saveSettings') }}</button>
              <button v-if="preference.room_ids.length" class="shared-clear-button mt-3" :disabled="preferenceSaving" @click="clearRoomPreference"><Icon name="trash" size="sm" />{{ t('sharedRooms.clearRooms') }}</button>
            </div>
          </template>

          <div v-else class="shared-api-prompt">
            <Icon name="arrowRight" size="sm" />
            <span>{{ t('sharedRooms.chooseAPIKeyHint') }}</span>
          </div>
        </aside>

        <main class="shared-rooms-main">
          <template v-if="selectedAPIKeyID">
            <div class="shared-rooms-toolbar">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <h2>{{ t('sharedRooms.roomsStepTitle') }}</h2>
                  <span v-if="selectedAPIKey" class="selected-api-key-chip"><Icon name="key" size="xs" />{{ selectedAPIKey.name }}</span>
                  <span class="room-total">{{ t('sharedRooms.roomsFound', { count: total }) }}</span>
                </div>
                <p>{{ t('sharedRooms.roomsStepHint') }}</p>
              </div>
              <div class="shared-toolbar-actions">
                <span v-if="preferenceSaving" class="shared-save-state"><Icon name="sync" size="xs" class="animate-spin" />{{ t('sharedRooms.saving') }}</span>
                <form class="shared-room-search" @submit.prevent="searchRooms">
                  <div class="relative min-w-0 flex-1">
                    <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
                    <input v-model.trim="keywordInput" class="input h-9 min-w-0 pl-9 text-sm" :placeholder="t('sharedRooms.searchPlaceholder')" />
                  </div>
                  <button type="submit" class="shared-search-button" :title="t('sharedRooms.search')" :disabled="loading"><Icon name="search" size="sm" /></button>
                  <button v-if="keyword" type="button" class="shared-search-button" :title="t('sharedRooms.clearSearch')" :disabled="loading" @click="clearSearch"><Icon name="x" size="sm" /></button>
                </form>
                <button class="shared-rooms-icon-button shared-desktop-refresh" :title="t('common.refresh')" :disabled="loading" @click="loadRooms"><Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" /></button>
              </div>
            </div>

            <div class="shared-room-list-head">
              <span>{{ t('sharedRooms.roomColumn') }}</span>
              <span>{{ t('sharedRooms.remainingColumn') }}</span>
              <span>{{ t('sharedRooms.capacityColumn') }}</span>
              <span>{{ t('sharedRooms.rateColumn') }}</span>
              <span class="text-right">{{ t('sharedRooms.actionColumn') }}</span>
            </div>

            <div v-if="loading" class="shared-room-loading" aria-live="polite">
              <div v-for="index in 6" :key="index" class="shared-room-skeleton"><span /><span /><span /><span /></div>
            </div>
            <div v-else-if="rooms.length === 0" class="shared-room-empty">
              <span class="shared-empty-icon"><Icon name="users" size="lg" /></span>
              <strong>{{ t('sharedRooms.empty') }}</strong>
              <button v-if="keyword" class="btn btn-secondary btn-sm" @click="clearSearch">{{ t('sharedRooms.clearSearch') }}</button>
            </div>
            <div v-else class="shared-room-list">
              <article
                v-for="room in rooms"
                :key="room.id"
                class="shared-room-row"
                :class="{
                  'shared-room-row--selected': roomSelected(room.id),
                  'shared-room-row--unavailable': !room.selectable,
                }"
              >
                <label class="shared-room-identity" :class="!room.selectable && !roomSelected(room.id) ? 'cursor-not-allowed' : 'cursor-pointer'">
                  <input
                    :checked="roomSelected(room.id)"
                    type="checkbox"
                    class="h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                    :disabled="preferenceSaving || (!room.selectable && !roomSelected(room.id))"
                    @change="toggleRoom(room.id)"
                  />
                  <span class="room-status-dot" :class="room.selectable ? 'room-status-dot--active' : 'room-status-dot--inactive'" :title="room.selectable ? t('sharedRooms.available') : t('sharedRooms.unavailable')" />
                  <span class="min-w-0">
                    <span class="flex min-w-0 items-center gap-2"><strong class="truncate">{{ room.name }}</strong><span v-if="!room.selectable" class="room-unavailable-label">{{ t('sharedRooms.unavailable') }}</span></span>
                    <span class="mt-0.5 block truncate text-xs text-gray-500 dark:text-gray-400">{{ t('sharedRooms.roomBy', { name: room.owner.username || `#${room.owner.user_id}` }) }}</span>
                  </span>
                </label>

                <div class="shared-room-metric">
                  <span>{{ t('sharedRooms.remainingColumn') }}</span>
                  <strong>{{ formatUSD(roomRemainingUSD(room)) }}</strong>
                </div>
                <div class="shared-room-metric">
                  <span>{{ t('sharedRooms.capacityColumn') }}</span>
                  <strong>{{ roomAvailableAccounts(room) }} / {{ roomConcurrency(room) }}</strong>
                </div>
                <div class="shared-room-rate">{{ formatMultiplier(room.consumer_rate_multiplier) }}x</div>
                <span class="shared-room-state" :class="roomSelected(room.id) ? 'shared-room-state--selected' : ''">
                  <Icon :name="roomSelected(room.id) ? 'checkCircle' : 'plus'" size="sm" />
                  {{ roomSelected(room.id) ? t('sharedRooms.selected') : t('sharedRooms.selectRoom') }}
                </span>
              </article>
            </div>

            <Pagination v-if="total > 0" :page="page" :total="total" :page-size="limit" @update:page="changePage" @update:pageSize="changePageSize" />
          </template>

          <div v-else class="shared-room-welcome">
            <span class="shared-welcome-icon"><Icon name="key" size="xl" /></span>
            <strong>{{ t('sharedRooms.chooseAPIKeyTitle') }}</strong>
            <p>{{ t('sharedRooms.chooseAPIKeyHint') }}</p>
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
import Pagination from '@/components/common/Pagination.vue'
import contributionRoomsAPI, { type ContributionRoom, type ContributionRoomPreference } from '@/api/contributionRooms'
import userGroupsAPI from '@/api/groups'
import keysAPI from '@/api/keys'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { ApiKey, Group } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()
const rooms = ref<ContributionRoom[]>([])
const apiKeys = ref<ApiKey[]>([])
const fallbackGroups = ref<Group[]>([])
const loading = ref(false)
const keysLoading = ref(false)
const groupsLoading = ref(false)
const preferenceSaving = ref(false)
const page = ref(1)
const limit = ref(24)
const total = ref(0)
const keywordInput = ref('')
const keyword = ref('')
const apiKeyKeyword = ref('')
const selectedAPIKeyID = ref(0)
const preference = reactive<ContributionRoomPreference>({ api_key_id: 0, room_ids: [], allow_pool_fallback: false, fallback_group_id: null })
const selectedAPIKeyStorageKey = 'gongfei:shared-rooms:selected-api-key'
const savedPreferenceSignature = ref('')
let roomsRequestSequence = 0
const activeAPIKeys = computed(() => apiKeys.value.filter((apiKey) => apiKey.status === 'active'))
const filteredAPIKeys = computed(() => {
  const query = apiKeyKeyword.value.trim().toLowerCase()
  if (!query) return activeAPIKeys.value
  return activeAPIKeys.value.filter((apiKey) => `${apiKey.name} ${apiKey.group?.name || ''}`.toLowerCase().includes(query))
})
const selectedAPIKey = computed(() => apiKeys.value.find((apiKey) => apiKey.id === selectedAPIKeyID.value) || null)
const preferenceDirty = computed(() => selectedAPIKeyID.value > 0 && preferenceSignature() !== savedPreferenceSignature.value)
const fallbackGroupSelection = computed({
  get: () => preference.fallback_group_id || 0,
  set: (value: number) => { preference.fallback_group_id = value > 0 ? value : null },
})
const selectedFallbackGroup = computed(() => fallbackGroups.value.find((group) => group.id === preference.fallback_group_id) || null)
const fallbackStatusLabel = computed(() => {
  if (!preference.allow_pool_fallback || !selectedFallbackGroup.value) return t('sharedRooms.fallbackOff')
  return selectedFallbackGroup.value.name
})

async function loadRooms() {
  const apiKeyID = selectedAPIKeyID.value
  if (!apiKeyID) return
  const requestSequence = ++roomsRequestSequence
  loading.value = true
  try {
    const result = await contributionRoomsAPI.list(apiKeyID, { page: page.value, limit: limit.value, keyword: keyword.value || undefined })
    if (requestSequence !== roomsRequestSequence || apiKeyID !== selectedAPIKeyID.value) return
    rooms.value = result.items
    total.value = result.total
    page.value = result.page
    limit.value = result.limit
    if (!preferenceDirty.value) applyPreference(result.preference)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('sharedRooms.loadFailed')))
  } finally {
    if (requestSequence === roomsRequestSequence) loading.value = false
  }
}

async function loadAPIKeys() {
  keysLoading.value = true
  try {
    const allKeys: ApiKey[] = []
    let nextPage = 1
    let pages = 1
    do {
      const result = await keysAPI.list(nextPage, 100, { sort_by: 'created_at', sort_order: 'desc' })
      allKeys.push(...result.items)
      pages = result.pages
      nextPage += 1
    } while (nextPage <= pages)
    apiKeys.value = allKeys
    restoreSelectedAPIKey()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('sharedRooms.apiKeysLoadFailed')))
  } finally {
    keysLoading.value = false
  }
}

function selectAPIKey(apiKeyID: number) {
  if (apiKeyID === selectedAPIKeyID.value) return
  if (preferenceDirty.value && !window.confirm(t('sharedRooms.unsavedSwitchConfirm'))) return
  selectedAPIKeyID.value = apiKeyID
  roomsRequestSequence += 1
  rooms.value = []
  total.value = 0
  page.value = 1
  keywordInput.value = ''
  keyword.value = ''
  resetPreference(apiKeyID)
  persistSelectedAPIKey(apiKeyID)
  if (apiKeyID) void loadRooms()
}

function restoreSelectedAPIKey() {
  const activeIDs = new Set(activeAPIKeys.value.map((apiKey) => apiKey.id))
  let restoredID = 0
  try {
    const storedID = Number(window.localStorage.getItem(selectedAPIKeyStorageKey) || 0)
    if (Number.isInteger(storedID) && activeIDs.has(storedID)) restoredID = storedID
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
  }
  if (!restoredID && activeIDs.has(selectedAPIKeyID.value)) restoredID = selectedAPIKeyID.value
  if (!restoredID) restoredID = activeAPIKeys.value[0]?.id || 0
  selectedAPIKeyID.value = restoredID
  resetPreference(restoredID)
  persistSelectedAPIKey(restoredID)
  if (restoredID) void loadRooms()
}

function persistSelectedAPIKey(apiKeyID: number) {
  try {
    if (apiKeyID > 0) window.localStorage.setItem(selectedAPIKeyStorageKey, String(apiKeyID))
    else window.localStorage.removeItem(selectedAPIKeyStorageKey)
  } catch {
    // The backend preference remains authoritative when storage is unavailable.
  }
}

async function loadFallbackGroups() {
  groupsLoading.value = true
  try {
    fallbackGroups.value = await userGroupsAPI.getAvailable()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('sharedRooms.groupsLoadFailed')))
  } finally {
    groupsLoading.value = false
  }
}

function searchRooms() {
  keyword.value = keywordInput.value
  page.value = 1
  void loadRooms()
}

function clearSearch() {
  keywordInput.value = ''
  keyword.value = ''
  page.value = 1
  void loadRooms()
}

function changePage(nextPage: number) {
  page.value = nextPage
  void loadRooms()
}

function changePageSize(nextLimit: number) {
  limit.value = nextLimit
  page.value = 1
  void loadRooms()
}

function toggleRoom(roomID: number) {
  const selected = new Set(preference.room_ids)
  if (selected.has(roomID)) selected.delete(roomID)
  else selected.add(roomID)
  preference.room_ids = Array.from(selected)
}

async function saveRoomPreference() {
  const apiKeyID = selectedAPIKeyID.value
  if (!apiKeyID) return
  const fallbackGroupID = selectedFallbackGroupID()
  if (preference.allow_pool_fallback && !fallbackGroupID) {
    appStore.showError(t('sharedRooms.fallbackGroupRequired'))
    return
  }
  preferenceSaving.value = true
  try {
    if (preference.room_ids.length === 0) {
      const next = await contributionRoomsAPI.clearPreference(apiKeyID)
      applyPreference(next)
      appStore.showSuccess(t('sharedRooms.selectionCleared'))
      return
    }
    const next = await contributionRoomsAPI.updatePreference(apiKeyID, preference.room_ids, preference.allow_pool_fallback, fallbackGroupID)
    applyPreference(next)
    appStore.showSuccess(t('sharedRooms.settingsSaved'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('sharedRooms.selectionSaveFailed')))
  } finally {
    preferenceSaving.value = false
  }
}

async function clearRoomPreference() {
  const apiKeyID = selectedAPIKeyID.value
  if (!apiKeyID) return
  preferenceSaving.value = true
  try {
    const next = await contributionRoomsAPI.clearPreference(apiKeyID)
    applyPreference(next)
    appStore.showSuccess(t('sharedRooms.selectionCleared'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('sharedRooms.selectionSaveFailed')))
  } finally {
    preferenceSaving.value = false
  }
}

function roomSelected(roomID: number) {
  return preference.room_ids.includes(roomID)
}

function roomConcurrency(room: ContributionRoom) {
  return room.accounts.filter((account) => account.enabled && !account.needs_attention).reduce((total, account) => total + Number(account.share_concurrency || 0), 0)
}

function roomAvailableAccounts(room: ContributionRoom) {
  return room.accounts.filter((account) => account.enabled && account.schedulable && !account.needs_attention).length
}

function roomRemainingUSD(room: ContributionRoom) {
  return room.accounts.reduce((total, account) => total + Math.max(0, Number(account.share_remaining_usd || 0)), 0)
}

function formatUSD(value: number) {
  return `$${value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

function formatMultiplier(value: number) {
  return Number(value || 1).toLocaleString(undefined, { maximumFractionDigits: 2 })
}

function selectedFallbackGroupID() {
  const value = Number(preference.fallback_group_id || 0)
  return Number.isFinite(value) && value > 0 ? value : null
}

function applyPreference(next: ContributionRoomPreference) {
  if (next.api_key_id !== selectedAPIKeyID.value) return
  Object.assign(preference, {
    api_key_id: next.api_key_id,
    room_ids: next.room_ids || [],
    allow_pool_fallback: Boolean(next.allow_pool_fallback && next.fallback_group_id),
    fallback_group_id: next.fallback_group_id || null,
  })
  savedPreferenceSignature.value = preferenceSignature()
}

function resetPreference(apiKeyID: number) {
  Object.assign(preference, {
    api_key_id: apiKeyID,
    room_ids: [],
    allow_pool_fallback: false,
    fallback_group_id: null,
  })
  savedPreferenceSignature.value = preferenceSignature()
}

function preferenceSignature() {
  return JSON.stringify({
    room_ids: [...preference.room_ids].sort((left, right) => left - right),
    allow_pool_fallback: Boolean(preference.allow_pool_fallback),
    fallback_group_id: preference.fallback_group_id || null,
  })
}

onMounted(() => {
  void loadAPIKeys()
  void loadFallbackGroups()
})
</script>

<style scoped>
.shared-rooms-header {
  display: none;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  margin-bottom: 1rem;
}

.shared-rooms-header h1 {
  color: #0f172a;
  font-size: 1.25rem;
  font-weight: 750;
  line-height: 1.45;
}

.shared-rooms-header p {
  margin-top: 0.125rem;
  color: #64748b;
  font-size: 0.8125rem;
  line-height: 1.45;
}

.shared-rooms-brand-mark {
  display: inline-flex;
  width: 2.5rem;
  height: 2.5rem;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(13 148 136 / 0.18);
  border-radius: 0.5rem;
  background: #ecfdf5;
  color: #0f766e;
}

.shared-rooms-workspace {
  display: grid;
  min-height: 36rem;
  grid-template-columns: minmax(17.5rem, 20rem) minmax(0, 1fr);
  box-shadow: 0 10px 28px rgb(15 23 42 / 0.045);
}

.shared-rooms-sidebar {
  border-right: 1px solid #e2e8f0;
  background: #f8fafc;
}

.shared-config-section {
  padding: 1.25rem;
  border-bottom: 1px solid #e2e8f0;
}

.shared-config-heading {
  display: flex;
  align-items: flex-start;
  gap: 0.625rem;
}

.shared-config-heading h2,
.shared-rooms-toolbar h2 {
  color: #0f172a;
  font-size: 0.875rem;
  font-weight: 700;
  line-height: 1.4;
}

.shared-config-heading p,
.shared-rooms-toolbar p {
  margin-top: 0.125rem;
  color: #64748b;
  font-size: 0.75rem;
  line-height: 1.45;
}

.shared-config-icon {
  display: inline-flex;
  width: 1.75rem;
  height: 1.75rem;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 0.375rem;
  background: #e0f2fe;
  color: #0369a1;
}

.shared-config-icon--fallback {
  background: #fef3c7;
  color: #b45309;
}

.shared-api-key-list {
  display: grid;
  max-height: 15rem;
  gap: 0.375rem;
  overflow-y: auto;
  padding-right: 0.125rem;
}

.shared-api-key-item {
  display: flex;
  min-height: 3.25rem;
  align-items: center;
  gap: 0.625rem;
  padding: 0.625rem 0.75rem;
  border: 1px solid #e2e8f0;
  border-radius: 0.375rem;
  background: white;
  color: #475569;
  transition: border-color 160ms ease, background-color 160ms ease, color 160ms ease;
}

.shared-api-key-item:hover:not(:disabled) { border-color: rgb(13 148 136 / 0.38); background: #f0fdfa; }
.shared-api-key-item:disabled { cursor: wait; opacity: 0.62; }
.shared-api-key-item strong { color: #1e293b; font-size: 0.8125rem; font-weight: 700; }
.shared-api-key-item small { margin-top: 0.125rem; color: #94a3b8; font-size: 0.6875rem; }
.shared-api-key-item--active { border-color: rgb(13 148 136 / 0.45); background: #ecfdf5; color: #0f766e; box-shadow: inset 3px 0 #0f9b8e; }

.shared-api-key-mark {
  display: inline-flex;
  width: 1.75rem;
  height: 1.75rem;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 0.375rem;
  background: #f1f5f9;
  color: #64748b;
}

.shared-api-key-item--active .shared-api-key-mark { background: white; color: #0f766e; }

.fallback-toggle-row {
  display: flex;
  cursor: pointer;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.75rem;
  border: 1px solid #e2e8f0;
  border-radius: 0.375rem;
  background: white;
}

.fallback-toggle-row--disabled { cursor: wait; opacity: 0.68; }

.shared-switch { flex: 0 0 auto; }
.shared-switch input { position: absolute; width: 1px; height: 1px; opacity: 0; }
.shared-switch-track {
  display: block;
  width: 2.25rem;
  height: 1.25rem;
  padding: 0.125rem;
  border-radius: 999px;
  background: #cbd5e1;
  transition: background-color 160ms ease;
}
.shared-switch-thumb {
  display: block;
  width: 1rem;
  height: 1rem;
  border-radius: 999px;
  background: white;
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.22);
  transition: transform 160ms ease;
}
.shared-switch input:checked + .shared-switch-track { background: #0f9b8e; }
.shared-switch input:checked + .shared-switch-track .shared-switch-thumb { transform: translateX(1rem); }
.shared-switch input:focus-visible + .shared-switch-track { outline: 2px solid rgb(20 184 166 / 0.35); outline-offset: 2px; }

.shared-selection-summary {
  margin: 1rem;
  padding: 0.875rem;
  border: 1px solid #dbe4ee;
  border-radius: 0.375rem;
  background: white;
}

.shared-selection-summary strong {
  display: block;
  margin-top: 0.5rem;
  color: #0f766e;
  font-size: 1.5rem;
  line-height: 1;
}

.shared-auto-save,
.shared-save-state {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  color: #0f766e;
  font-size: 0.6875rem;
  font-weight: 650;
  white-space: nowrap;
}

.shared-auto-save--dirty { color: #b45309; }

.shared-save-state {
  padding: 0.375rem 0.625rem;
  border-radius: 999px;
  background: #f0fdfa;
}

.shared-clear-button {
  display: inline-flex;
  width: 100%;
  height: 2rem;
  align-items: center;
  justify-content: center;
  gap: 0.375rem;
  border: 1px solid #e2e8f0;
  border-radius: 0.375rem;
  background: white;
  color: #64748b;
  font-size: 0.75rem;
  font-weight: 600;
}

.shared-clear-button:hover:not(:disabled) { border-color: #fecaca; background: #fff7f7; color: #b91c1c; }
.shared-clear-button:disabled { cursor: wait; opacity: 0.55; }

.shared-api-prompt {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin: 1rem;
  color: #64748b;
  font-size: 0.75rem;
  line-height: 1.45;
}

.shared-rooms-main { min-width: 0; background: white; }

.shared-rooms-toolbar {
  display: flex;
  min-height: 5.25rem;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid #e2e8f0;
}

.room-total {
  padding: 0.1875rem 0.4375rem;
  border-radius: 999px;
  background: #f1f5f9;
  color: #64748b;
  font-size: 0.6875rem;
  font-weight: 650;
}

.selected-api-key-chip {
  display: inline-flex;
  max-width: 13rem;
  height: 1.5rem;
  align-items: center;
  gap: 0.25rem;
  overflow: hidden;
  padding: 0 0.5rem;
  border: 1px solid rgb(13 148 136 / 0.2);
  border-radius: 0.25rem;
  background: #ecfdf5;
  color: #0f766e;
  font-size: 0.6875rem;
  font-weight: 700;
  white-space: nowrap;
}

.shared-room-search { display: flex; width: min(100%, 23rem); gap: 0.375rem; }
.shared-toolbar-actions { display: flex; min-width: 0; align-items: center; justify-content: flex-end; gap: 0.5rem; }
.shared-search-button {
  display: inline-flex;
  width: 2.25rem;
  height: 2.25rem;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border: 1px solid #cbd5e1;
  border-radius: 0.375rem;
  background: white;
  color: #475569;
}
.shared-search-button:hover:not(:disabled) { border-color: rgb(13 148 136 / 0.5); background: #f0fdfa; color: #0f766e; }
.shared-search-button:disabled { cursor: wait; opacity: 0.55; }

.shared-room-list-head,
.shared-room-row {
  display: grid;
  grid-template-columns: minmax(13rem, 1fr) 6.75rem 6.5rem 4.5rem 6.5rem;
  align-items: center;
  column-gap: 0.75rem;
}

.shared-room-list-head {
  min-height: 2rem;
  padding: 0 1.25rem;
  border-bottom: 1px solid #e2e8f0;
  background: #f8fafc;
  color: #64748b;
  font-size: 0.6875rem;
  font-weight: 650;
}

.shared-room-row {
  min-height: 3.75rem;
  padding: 0.625rem 1.25rem;
  border-bottom: 1px solid #e2e8f0;
  border-left: 3px solid transparent;
  transition: background-color 160ms ease, border-color 160ms ease, opacity 160ms ease;
}

.shared-room-row:hover { background: rgb(248 250 252 / 0.8); }
.shared-room-row--selected { border-left-color: #0f9b8e; background: rgb(240 253 250 / 0.72); }
.shared-room-row--unavailable:not(.shared-room-row--selected) { background: #f8fafc; opacity: 0.62; }

.shared-room-identity { display: flex; min-width: 0; align-items: center; gap: 0.625rem; }
.shared-room-identity strong { color: #1e293b; font-size: 0.8125rem; font-weight: 700; }

.room-status-dot {
  width: 0.5rem;
  height: 0.5rem;
  flex: 0 0 auto;
  border-radius: 999px;
  box-shadow: 0 0 0 3px rgb(148 163 184 / 0.12);
}
.room-status-dot--active { background: #10b981; }
.room-status-dot--inactive { background: #94a3b8; }

.room-unavailable-label {
  flex: 0 0 auto;
  padding: 0.125rem 0.375rem;
  border-radius: 0.25rem;
  background: #e2e8f0;
  color: #64748b;
  font-size: 0.625rem;
  font-weight: 650;
}

.shared-room-metric span { display: none; }
.shared-room-metric strong { color: #334155; font-size: 0.75rem; font-weight: 650; }

.shared-room-rate {
  display: inline-flex;
  width: fit-content;
  min-width: 2.75rem;
  height: 1.5rem;
  align-items: center;
  justify-content: center;
  padding: 0 0.4375rem;
  border: 1px solid #bae6fd;
  border-radius: 0.25rem;
  background: #f0f9ff;
  color: #0369a1;
  font-size: 0.6875rem;
  font-weight: 750;
}

.selected-count {
  border-radius: 999px;
  background: rgb(15 155 142 / 0.1);
  color: #0f766e;
  padding: 0.38rem 0.65rem;
  font-size: 0.75rem;
  font-weight: 700;
  white-space: nowrap;
}

.shared-rooms-icon-button {
  display: inline-flex;
  height: 2.4rem;
  width: 2.4rem;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(203 213 225 / 0.9);
  border-radius: 0.5rem;
  background: white;
  color: #475569;
  transition: border-color 160ms ease, color 160ms ease, background-color 160ms ease;
}

.shared-rooms-icon-button:hover:not(:disabled) { border-color: rgb(13 148 136 / 0.45); background: rgb(240 253 250); color: #0f766e; }
.shared-rooms-icon-button:disabled { cursor: not-allowed; opacity: 0.5; }

.shared-pool-control { max-width: 46rem; }

.shared-room-row {
  border-left: 3px solid transparent;
  transition: background-color 160ms ease, border-color 160ms ease;
}

.shared-room-row:hover { background: rgb(248 250 252 / 0.78); }
.shared-room-row--selected { border-left-color: #0f9b8e; background: rgb(240 253 250 / 0.72); }

.shared-room-state {
  display: inline-flex;
  justify-self: end;
  align-items: center;
  gap: 0.35rem;
  color: #64748b;
  font-size: 0.75rem;
  white-space: nowrap;
}

.shared-room-state--selected { color: #0f766e; font-weight: 700; }

.shared-room-loading { padding: 0.25rem 1.25rem; }
.shared-room-skeleton {
  display: grid;
  min-height: 3.75rem;
  grid-template-columns: minmax(12rem, 1fr) 6rem 5rem 4rem;
  align-items: center;
  gap: 1rem;
  border-bottom: 1px solid #edf2f7;
}
.shared-room-skeleton span { height: 0.75rem; border-radius: 0.25rem; background: #e2e8f0; animation: room-pulse 1.4s ease-in-out infinite; }
.shared-room-skeleton span:first-child { width: min(15rem, 80%); }

.shared-room-empty,
.shared-room-welcome {
  display: flex;
  min-height: 25rem;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.625rem;
  padding: 2rem;
  color: #64748b;
  text-align: center;
}
.shared-room-empty strong,
.shared-room-welcome strong { color: #334155; font-size: 0.875rem; }
.shared-room-welcome p { max-width: 24rem; font-size: 0.75rem; line-height: 1.5; }
.shared-empty-icon,
.shared-welcome-icon {
  display: inline-flex;
  width: 3.25rem;
  height: 3.25rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.5rem;
  background: #ecfdf5;
  color: #0f9b8e;
}

@keyframes room-pulse { 0%, 100% { opacity: 0.55; } 50% { opacity: 1; } }

:global(.dark .shared-rooms-header h1),
:global(.dark .shared-config-heading h2),
:global(.dark .shared-rooms-toolbar h2){ color: #f8fafc; }
:global(.dark .shared-rooms-brand-mark){ border-color: rgb(45 212 191 / 0.2); background: rgb(19 78 74 / 0.35); color: #5eead4; }
:global(.dark .shared-rooms-sidebar),
:global(.dark .shared-room-list-head){ border-color: #334155; background: #111827; }
:global(.dark .shared-config-section){ border-color: #334155; }
:global(.dark .fallback-toggle-row),
:global(.dark .shared-selection-summary),
:global(.dark .shared-rooms-main),
:global(.dark .shared-search-button),
:global(.dark .shared-clear-button),
:global(.dark .shared-api-key-item){ border-color: #334155; background: #172033; }
:global(.dark .shared-api-key-item strong){ color: #e2e8f0; }
:global(.dark .shared-api-key-item:hover:not(:disabled)),
:global(.dark .shared-api-key-item--active){ border-color: rgb(45 212 191 / 0.35); background: rgb(19 78 74 / 0.35); color: #5eead4; }
:global(.dark .shared-api-key-mark){ background: #243047; color: #94a3b8; }
:global(.dark .shared-api-key-item--active .shared-api-key-mark){ background: #172033; color: #5eead4; }
:global(.dark .selected-api-key-chip){ border-color: rgb(45 212 191 / 0.25); background: rgb(19 78 74 / 0.35); color: #99f6e4; }
:global(.dark .shared-room-list-head),
:global(.dark .shared-rooms-toolbar),
:global(.dark .shared-room-row){ border-color: #334155; }
:global(.dark .shared-room-identity strong),
:global(.dark .shared-room-metric strong),
:global(.dark .shared-room-empty strong),
:global(.dark .shared-room-welcome strong){ color: #e2e8f0; }
:global(.dark .shared-room-row:hover){ background: rgb(30 41 59 / 0.58); }
:global(.dark .shared-room-row--selected){ border-left-color: #2dd4bf; background: rgb(19 78 74 / 0.24); }
:global(.dark .shared-room-row--unavailable:not(.shared-room-row--selected) ){ background: rgb(15 23 42 / 0.7); }
:global(.dark .shared-room-rate){ border-color: rgb(14 116 144 / 0.55); background: rgb(8 47 73 / 0.55); color: #7dd3fc; }
:global(.dark .shared-room-skeleton span){ background: #334155; }
:global(.dark .shared-rooms-icon-button){ border-color: rgb(71 85 105 / 0.8); background: rgb(30 41 59); color: #cbd5e1; }
:global(.dark .shared-rooms-icon-button:hover:not(:disabled) ){ background: rgb(19 78 74 / 0.4); color: #5eead4; }
:global(.dark .selected-count){ background: rgb(19 78 74 / 0.45); color: #99f6e4; }

@media (max-width: 1199px) {
  .shared-rooms-workspace { grid-template-columns: minmax(0, 1fr); }
  .shared-rooms-sidebar { border-right: 0; border-bottom: 1px solid #e2e8f0; }
  .shared-selection-summary { margin-top: 1rem; }
  .shared-api-key-list { grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr)); max-height: 12rem; }
  .shared-room-welcome { min-height: 16rem; }
  :global(.dark .shared-rooms-sidebar){ border-bottom-color: #334155; }
}

@media (max-width: 767px) {
  .shared-rooms-toolbar { align-items: stretch; flex-direction: column; }
  .shared-toolbar-actions { width: 100%; }
  .shared-room-search { width: 100%; }
  .shared-room-list-head { display: none; }
  .shared-room-row {
    min-height: 5.75rem;
    grid-template-columns: minmax(0, 1fr) auto auto;
    grid-template-areas: 'identity identity state' 'remaining capacity rate';
    row-gap: 0.75rem;
  }
  .shared-room-identity { grid-area: identity; }
  .shared-room-metric { display: flex; flex-direction: column; gap: 0.125rem; }
  .shared-room-metric:nth-of-type(1) { grid-area: remaining; }
  .shared-room-metric:nth-of-type(2) { grid-area: capacity; }
  .shared-room-metric span { display: block; color: #94a3b8; font-size: 0.625rem; }
  .shared-room-rate { grid-area: rate; align-self: end; }
  .shared-room-state { grid-area: state; }
}

@media (max-width: 640px) {
  .shared-rooms-page { padding: 1rem; }
  .shared-rooms-header { display: flex; align-items: flex-start; }
  .shared-rooms-header p { display: none; }
  .selected-count { display: none; }
  .shared-desktop-refresh { display: none; }
  .shared-config-section,
  .shared-rooms-toolbar { padding: 1rem; }
  .shared-room-row { padding: 0.75rem 1rem; }
}
</style>
