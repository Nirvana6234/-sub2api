<template>
  <div class="space-y-5" data-test="keys-panel">
    <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.relay.keys.hint') }}</p>

    <section
      v-for="purpose in purposes"
      :key="purpose"
      class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700"
      :data-test="`keys-${purpose}`"
    >
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 class="font-medium text-gray-900 dark:text-white">{{ t(`admin.relay.keys.purpose.${purpose}`) }}</h3>
          <p class="text-xs text-gray-500">{{ t(`admin.relay.keys.purposeHint.${purpose}`) }}</p>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" :data-test="`stage-${purpose}`" @click="stage(purpose)">
          {{ t('admin.relay.keys.stage') }}
        </button>
      </div>

      <p v-if="(keys[purpose] ?? []).length === 0" class="text-xs text-gray-500">{{ t('admin.relay.keys.none') }}</p>
      <table v-else class="min-w-full text-xs">
        <thead class="text-left text-gray-500">
          <tr>
            <th class="py-1 pr-3">{{ t('admin.relay.keys.version') }}</th>
            <th class="py-1 pr-3">{{ t('common.status') }}</th>
            <th class="py-1 pr-3">{{ t('admin.relay.keys.fingerprint') }}</th>
            <th class="py-1 pr-3">{{ t('admin.relay.keys.createdAt') }}</th>
            <th class="py-1">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="k in keys[purpose]" :key="k.version" class="align-top">
            <td class="py-1 pr-3">v{{ k.version }}</td>
            <td class="py-1 pr-3">
              <span class="badge" :class="k.signing ? 'badge-success' : k.staged ? 'badge-primary' : 'badge-gray'">
                {{ k.signing ? t('admin.relay.keys.signing') : k.staged ? t('admin.relay.keys.staged') : t('admin.relay.keys.verifyOnly') }}
              </span>
              <div v-if="k.staged && (k.pending_node_ids ?? []).length" class="mt-1 text-amber-600">
                {{ t('admin.relay.keys.pending', { nodes: (k.pending_node_ids ?? []).join(', ') }) }}
              </div>
            </td>
            <td class="max-w-xs break-all py-1 pr-3 font-mono">{{ k.fingerprint }}</td>
            <td class="py-1 pr-3">{{ formatTime(k.created_at) }}</td>
            <td class="py-1">
              <div class="flex gap-1">
                <button
                  v-if="k.staged"
                  type="button"
                  class="btn btn-primary btn-sm"
                  :disabled="busy || (k.pending_node_ids ?? []).length > 0"
                  :data-test="`activate-${purpose}-${k.version}`"
                  @click="activate(purpose, k.version)"
                >
                  {{ t('admin.relay.keys.activate') }}
                </button>
                <button
                  v-if="!k.signing"
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="busy"
                  :data-test="`retire-${purpose}-${k.version}`"
                  @click="askRetire(purpose, k)"
                >
                  {{ k.staged ? t('admin.relay.keys.abandon') : t('admin.relay.keys.retire') }}
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <ConfirmDialog
      :show="retiring !== null"
      :title="t('admin.relay.keys.retireTitle')"
      :message="t('admin.relay.keys.retireMessage', { version: retiring?.key.version })"
      danger
      @confirm="doRetire"
      @cancel="retiring = null"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { RelayKeyInfo, RelayKeyPurpose } from '@/api/admin/relay'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { StepUpController } from '@/composables/useStepUp'
import { formatTime, useRelayAction } from './useRelayAction'

const props = defineProps<{ stepUp: StepUpController }>()

const { t } = useI18n()
const action = useRelayAction(props.stepUp)
const purposes: RelayKeyPurpose[] = ['root_ca', 'ticket', 'voucher']
const keys = reactive<Record<string, RelayKeyInfo[]>>({})
const busy = ref(false)
const retiring = ref<{ purpose: RelayKeyPurpose; key: RelayKeyInfo } | null>(null)

async function load(): Promise<void> {
  for (const p of purposes) {
    const list = await action.load(() => adminAPI.relay.listKeys(p))
    if (list) keys[p] = list
  }
}

async function guarded(fn: () => Promise<unknown>, message: string): Promise<void> {
  busy.value = true
  try {
    if (await action.runOk(fn, message)) await load()
  } finally {
    busy.value = false
  }
}

const stage = (p: RelayKeyPurpose) => guarded(() => adminAPI.relay.stageKey(p), t('admin.relay.keys.staged_done'))
const activate = (p: RelayKeyPurpose, v: number) =>
  guarded(() => adminAPI.relay.activateKey(p, v), t('admin.relay.keys.activated_done'))

function askRetire(purpose: RelayKeyPurpose, key: RelayKeyInfo): void {
  retiring.value = { purpose, key }
}

async function doRetire(): Promise<void> {
  const r = retiring.value
  retiring.value = null
  if (!r) return
  await guarded(() => adminAPI.relay.retireKey(r.purpose, r.key.version), t('admin.relay.keys.retired_done'))
}

onMounted(load)
defineExpose({ load })
</script>
