<template>
  <div class="space-y-6" data-test="assignment-panel">
    <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
      <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.relay.assign.keysTitle') }}</h3>
      <p class="text-xs text-gray-500">{{ t('admin.relay.assign.keysHint') }}</p>
      <table class="min-w-full text-sm">
        <thead class="text-left text-xs text-gray-500">
          <tr>
            <th class="py-1 pr-4">{{ t('admin.relay.logs.node') }}</th>
            <th class="py-1 pr-4">{{ t('admin.relay.assign.keysTotal') }}</th>
            <th class="py-1 pr-4">{{ t('admin.relay.assign.keysActive') }}</th>
            <th class="py-1">{{ t('admin.relay.assign.users') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.id" :data-test="`assign-row-${row.id}`">
            <td class="py-1 pr-4">{{ row.label }}</td>
            <td class="py-1 pr-4">{{ row.keys }}</td>
            <td class="py-1 pr-4">{{ row.active }}</td>
            <td class="py-1">{{ row.users }}</td>
          </tr>
        </tbody>
      </table>
      <p v-if="(keySummary?.unassigned ?? 0) > 0" class="text-sm text-amber-600" data-test="unassigned-notice">
        {{ t('admin.relay.assign.unassigned', { count: keySummary?.unassigned }) }}
      </p>
      <div class="flex flex-wrap gap-2">
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="busy || !(keySummary?.unassigned)"
          data-test="assign-unassigned"
          @click="assignUnassigned"
        >
          {{ t('admin.relay.assign.assignUnassigned') }}
        </button>
      </div>

      <div class="grid gap-3 border-t border-gray-100 pt-3 dark:border-dark-700 sm:grid-cols-3">
        <div class="sm:col-span-2">
          <label class="input-label">{{ t('admin.relay.assign.moveKeyIds') }}</label>
          <input v-model="keyIdsText" class="input" data-test="key-ids" placeholder="12, 15, 40" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.assign.moveTarget') }}</label>
          <Select v-model="keyTarget" :options="targetOptions" />
        </div>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || parsedKeyIds.length === 0" data-test="move-keys" @click="moveKeys">
        {{ t('admin.relay.assign.moveKeys', { count: parsedKeyIds.length }) }}
      </button>
    </section>

    <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
      <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.relay.assign.usersTitle') }}</h3>
      <p class="text-xs text-gray-500">{{ t('admin.relay.assign.usersHint') }}</p>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" data-test="rebalance" @click="rebalance">
        {{ t('admin.relay.assign.rebalance') }}
      </button>

      <div class="grid gap-3 border-t border-gray-100 pt-3 dark:border-dark-700 sm:grid-cols-3">
        <div>
          <label class="input-label">{{ t('admin.relay.logs.userId') }}</label>
          <input v-model.number="userId" type="number" min="1" class="input" data-test="user-id" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.assign.moveTarget') }}</label>
          <Select v-model="userTarget" :options="userTargetOptions" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.assign.pinUntil') }}</label>
          <input v-model="pinUntil" type="datetime-local" class="input" data-test="pin-until" />
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.relay.assign.pinUntilHint') }}</p>
        </div>
      </div>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !userId" data-test="move-user" @click="moveUser">
          {{ t('admin.relay.assign.moveUser') }}
        </button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !userId || !pinUntil" data-test="pin-user" @click="pinUser">
          {{ t('admin.relay.assign.pinUser') }}
        </button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !userId" data-test="unpin-user" @click="unpinUser">
          {{ t('admin.relay.assign.unpinUser') }}
        </button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !userId" data-test="reclaim-user" @click="reclaimUser">
          {{ t('admin.relay.assign.reclaimUser') }}
        </button>
      </div>
      <p class="text-xs text-gray-500">{{ t('admin.relay.assign.reclaimHint') }}</p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { RelayKeyAssignmentSummary, RelayNode } from '@/api/admin/relay'
import Select from '@/components/common/Select.vue'
import type { StepUpController } from '@/composables/useStepUp'
import { useAppStore } from '@/stores'
import { useRelayAction } from './useRelayAction'

const props = defineProps<{
  nodes: RelayNode[]
  keySummary: RelayKeyAssignmentSummary | null
  userSummary: Record<string, number>
  masterRatio: number
  stepUp: StepUpController
}>()
const emit = defineEmits<{ (e: 'refresh'): void }>()

const { t } = useI18n()
const appStore = useAppStore()
const action = useRelayAction(props.stepUp)
const busy = ref(false)
const keyIdsText = ref('')
const keyTarget = ref<number>(0)
const userId = ref<number | null>(null)
const userTarget = ref<number>(0)
const pinUntil = ref('')

const rows = computed(() => {
  const out = [
    {
      id: 0,
      label: t('admin.relay.nodes.master'),
      keys: props.keySummary?.master ?? 0,
      active: '-' as string | number,
      users: props.userSummary['0'] ?? 0
    }
  ]
  for (const n of props.nodes) {
    if (n.status === 'pending' || n.status === 'rejected') continue
    const k = props.keySummary?.nodes[String(n.id)]
    out.push({
      id: n.id,
      label: n.name || n.hostname || `#${n.id}`,
      keys: k?.total ?? 0,
      active: k?.active ?? 0,
      users: props.userSummary[String(n.id)] ?? 0
    })
  }
  return out
})

const targetOptions = computed(() => {
  const opts: { value: number; label: string }[] = []
  if (props.masterRatio > 0) opts.push({ value: 0, label: t('admin.relay.nodes.master') })
  for (const n of props.nodes) {
    if (n.status === 'active') opts.push({ value: n.id, label: n.name || n.hostname || `#${n.id}` })
  }
  return opts
})
const userTargetOptions = targetOptions

const parsedKeyIds = computed(() =>
  Array.from(
    new Set(
      keyIdsText.value
        .split(/[\s,，;；]+/)
        .map((s) => Number.parseInt(s, 10))
        .filter((n) => Number.isFinite(n) && n > 0)
    )
  )
)

async function guarded<T>(fn: () => Promise<T>): Promise<T | undefined> {
  busy.value = true
  try {
    return await action.run(fn)
  } finally {
    busy.value = false
  }
}

async function assignUnassigned(): Promise<void> {
  const res = await guarded(() => adminAPI.relay.assignUnassignedKeys())
  if (res) {
    appStore.showSuccess(t('admin.relay.assign.assignedDone', { assigned: res.assigned, left: res.left }))
    emit('refresh')
  }
}

async function moveKeys(): Promise<void> {
  const ids = parsedKeyIds.value
  const res = await guarded(() => adminAPI.relay.moveKeys(ids, keyTarget.value))
  if (res) {
    appStore.showSuccess(t('admin.relay.assign.movedDone', { count: res.moved }))
    keyIdsText.value = ''
    emit('refresh')
  }
}

async function rebalance(): Promise<void> {
  const res = await guarded(() => adminAPI.relay.rebalanceUsers())
  if (res) {
    appStore.showSuccess(t('admin.relay.assign.rebalanceDone', { count: res.users }))
    emit('refresh')
  }
}

async function moveUser(): Promise<void> {
  const id = userId.value
  if (!id) return
  const res = await guarded(() => adminAPI.relay.moveUser(id, userTarget.value))
  if (res) {
    appStore.showSuccess(t('admin.relay.assign.userMoved'))
    emit('refresh')
  }
}

async function pinUser(): Promise<void> {
  const id = userId.value
  const until = new Date(pinUntil.value)
  if (!id || Number.isNaN(until.getTime())) return
  const res = await guarded(() => adminAPI.relay.pinUser(id, userTarget.value, until.toISOString()))
  if (res) {
    appStore.showSuccess(t('admin.relay.assign.userPinned'))
    emit('refresh')
  }
}

async function unpinUser(): Promise<void> {
  const id = userId.value
  if (!id) return
  const ok = await guarded(async () => {
    await adminAPI.relay.unpinUser(id)
    return true
  })
  if (ok) {
    appStore.showSuccess(t('admin.relay.assign.userUnpinned'))
    emit('refresh')
  }
}

async function reclaimUser(): Promise<void> {
  const id = userId.value
  if (!id) return
  const res = await guarded(() => adminAPI.relay.reclaimUserQuota(id))
  if (res) {
    appStore.showSuccess(
      t('admin.relay.confirm.reclaim.done', {
        sent: res.recall_sent,
        voided: res.voided,
        amount: res.voided_amount_display
      })
    )
  }
}
</script>
