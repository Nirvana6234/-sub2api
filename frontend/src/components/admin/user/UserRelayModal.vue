<template>
  <BaseDialog :show="show" :title="t('admin.users.relay.title')" width="wide" @close="$emit('close')">
    <div v-if="user" class="space-y-4" data-test="user-relay-modal">
      <p class="text-sm text-gray-600 dark:text-gray-400">{{ t('admin.users.relay.subtitle', { email: user.email }) }}</p>
      <div v-if="loading" class="py-8 text-center text-gray-500">{{ t('common.loading') }}</div>
      <template v-else-if="state">
        <section>
          <h4 class="mb-1 text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.users.relay.assignment') }}</h4>
          <p v-if="!state.assignment" class="text-sm text-gray-500" data-test="no-assignment">{{ t('admin.users.relay.noAssignment') }}</p>
          <p v-else class="text-sm text-gray-700 dark:text-gray-300" data-test="assignment">
            {{ state.assignment.node_id === 0 ? t('admin.users.relay.onMaster') : t('admin.users.relay.onNode', { node: nodeLabel(state.assignment.node_id) }) }}
            <span v-if="state.assignment.pinned_until" class="ml-2 text-xs text-amber-600">
              {{ t('admin.users.relay.pinnedUntil', { time: formatTime(state.assignment.pinned_until) }) }}
            </span>
          </p>
        </section>

        <section>
          <h4 class="mb-1 text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.users.relay.leases') }}</h4>
          <p v-if="state.leases.length === 0" class="text-sm text-gray-500" data-test="no-leases">{{ t('admin.users.relay.noLeases') }}</p>
          <table v-else class="min-w-full text-sm">
            <thead class="text-left text-xs text-gray-500">
              <tr>
                <th class="py-1 pr-3">{{ t('admin.users.relay.node') }}</th>
                <th class="py-1 pr-3">{{ t('admin.users.relay.dimension') }}</th>
                <th class="py-1 pr-3">{{ t('admin.users.relay.locked') }}</th>
                <th class="py-1 pr-3">{{ t('admin.users.relay.expiresAt') }}</th>
                <th class="py-1">{{ t('common.status') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(l, i) in state.leases" :key="i" :data-test="`lease-${i}`">
                <td class="py-1 pr-3">{{ l.node_name || `#${l.node_id}` }}</td>
                <td class="py-1 pr-3">{{ l.dimension }}</td>
                <td class="py-1 pr-3 font-mono">{{ l.granted }}</td>
                <td class="py-1 pr-3">{{ formatTime(l.expires_at) }}</td>
                <td class="py-1">
                  <span class="badge" :class="l.node_online ? 'badge-success' : 'badge-danger'">
                    {{ l.node_online ? t('admin.relay.health.online') : t('admin.relay.health.offline') }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
          <p v-if="hasOffline" class="mt-2 text-xs text-amber-600" data-test="offline-hint">{{ t('admin.users.relay.offlineHint') }}</p>
        </section>
      </template>
      <TotpStepUpDialog :controller="stepUp" />
    </div>
    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="$emit('close')">{{ t('common.close') }}</button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="busy || !state || state.leases.length === 0"
          data-test="reclaim"
          @click="reclaim"
        >
          {{ t('admin.users.relay.reclaim') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { RelayUserState } from '@/api/admin/relay'
import type { AdminUser } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores'
import { formatTime, useRelayAction } from '@/components/admin/relay/useRelayAction'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
defineEmits<{ (e: 'close'): void }>()

const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()
const action = useRelayAction(stepUp)
const loading = ref(false)
const busy = ref(false)
const state = ref<RelayUserState | null>(null)

const hasOffline = computed(() => (state.value?.leases ?? []).some((l) => !l.node_online))

function nodeLabel(id: number): string {
  const l = state.value?.leases.find((x) => x.node_id === id)
  return l?.node_name || `#${id}`
}

async function load(): Promise<void> {
  if (!props.user) return
  const id = props.user.id
  loading.value = true
  try {
    state.value = (await action.load(() => adminAPI.relay.userRelayState(id))) ?? null
  } finally {
    loading.value = false
  }
}

async function reclaim(): Promise<void> {
  if (!props.user) return
  const id = props.user.id
  busy.value = true
  try {
    const res = await action.run(() => adminAPI.relay.reclaimUserQuota(id))
    if (res) {
      appStore.showSuccess(
        t('admin.relay.confirm.reclaim.done', { sent: res.recall_sent, voided: res.voided, amount: res.voided_amount_display })
      )
      await load()
    }
  } finally {
    busy.value = false
  }
}

watch(
  () => [props.show, props.user?.id] as const,
  ([shown]) => {
    if (shown) void load()
    else state.value = null
  },
  { immediate: true }
)
</script>
