<template>
  <div class="space-y-5" data-test="config-panel">
    <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.relay.config.hint') }}</p>

    <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
      <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.relay.config.assignment') }}</h3>
      <div class="grid gap-4 sm:grid-cols-2">
        <div>
          <label class="input-label">{{ t('admin.relay.config.masterRatio') }}</label>
          <input v-model.number="form.master_ratio_percent" type="number" min="0" max="100" class="input" data-test="master-ratio" />
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.relay.config.masterRatioHint') }}</p>
          <p v-if="ratioZero" class="mt-1 text-xs text-amber-600" data-test="ratio-zero-warning">
            {{ t('admin.relay.config.masterRatioZeroWarning', { keys: masterKeys, users: masterUsers }) }}
          </p>
          <p v-else-if="ratioChanged" class="mt-1 text-xs text-gray-500">
            {{ t('admin.relay.config.masterRatioImpact', { keys: masterKeys, users: masterUsers }) }}
          </p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.config.keyRule') }}</label>
          <Select v-model="form.api_key_node_rule" :options="ruleOptions" />
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.relay.config.keyRuleHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.config.loadThreshold') }}</label>
          <input v-model.number="form.load_threshold_percent" type="number" min="1" max="100" class="input" />
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.relay.config.loadThresholdHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.config.assignmentRefresh') }}</label>
          <input v-model.number="form.assignment_refresh_seconds" type="number" min="15" class="input" />
        </div>
      </div>
    </section>

    <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
      <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.relay.config.liveness') }}</h3>
      <div class="grid gap-4 sm:grid-cols-3">
        <div>
          <label class="input-label">{{ t('admin.relay.config.heartbeat') }}</label>
          <input v-model.number="form.heartbeat_interval_seconds" type="number" min="1" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.config.offlineAfter') }}</label>
          <input v-model.number="form.offline_after_seconds" type="number" min="3" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.config.drainMax') }}</label>
          <input v-model.number="form.drain_max_wait_minutes" type="number" min="1" class="input" />
        </div>
      </div>
    </section>

    <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
      <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.relay.config.masterCap') }}</h3>
      <p class="text-xs text-gray-500">{{ t('admin.relay.config.masterCapHint') }}</p>
      <div class="grid gap-4 sm:grid-cols-2">
        <div>
          <label class="input-label">{{ t('admin.relay.config.masterMaxConcurrent') }}</label>
          <input v-model.number="form.master_max_concurrent" type="number" min="0" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.config.masterMaxBandwidth') }}</label>
          <input v-model.number="form.master_max_bandwidth_mbps" type="number" min="0" class="input" />
        </div>
      </div>
    </section>

    <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
      <div class="flex items-center justify-between">
        <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.relay.config.probe') }}</h3>
        <Toggle v-model="probeEnabled" />
      </div>
      <p class="text-xs text-gray-500">{{ t('admin.relay.config.probeHint') }}</p>
      <div class="grid gap-4 sm:grid-cols-2">
        <div>
          <label class="input-label">{{ t('admin.relay.config.probeInterval') }}</label>
          <input v-model.number="form.probe_interval_seconds" type="number" min="10" class="input" :disabled="!probeEnabled" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.config.probePort') }}</label>
          <input v-model.number="form.probe_port" type="number" min="1" max="65535" class="input" :disabled="!probeEnabled" />
        </div>
      </div>
    </section>

    <div class="flex justify-end">
      <button type="button" class="btn btn-primary" :disabled="saving" data-test="save-config" @click="save">
        {{ t('common.save') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { RelayGeneralConfig, RelayKeyAssignmentSummary } from '@/api/admin/relay'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import type { StepUpController } from '@/composables/useStepUp'
import { useRelayAction } from './useRelayAction'

const props = defineProps<{
  config: RelayGeneralConfig | null
  keySummary: RelayKeyAssignmentSummary | null
  userSummary: Record<string, number>
  stepUp: StepUpController
}>()
const emit = defineEmits<{ (e: 'saved', cfg: RelayGeneralConfig): void }>()

const { t } = useI18n()
const action = useRelayAction(props.stepUp)
const saving = ref(false)
const probeEnabled = ref(true)
const form = reactive<RelayGeneralConfig>({})

watch(
  () => props.config,
  (c) => {
    Object.assign(form, c ?? {})
    probeEnabled.value = c?.probe_enabled !== false
  },
  { immediate: true }
)

const ruleOptions = computed(() => [
  { value: 'assigned', label: t('admin.relay.config.ruleAssigned') },
  { value: 'any', label: t('admin.relay.config.ruleAny') }
])

const masterKeys = computed(() => props.keySummary?.master ?? 0)
const masterUsers = computed(() => props.userSummary['0'] ?? 0)
const ratioZero = computed(() => form.master_ratio_percent === 0 && (props.config?.master_ratio_percent ?? 10) !== 0)
const ratioChanged = computed(
  () => form.master_ratio_percent !== undefined && form.master_ratio_percent !== props.config?.master_ratio_percent
)

async function save(): Promise<void> {
  saving.value = true
  try {
    const body: RelayGeneralConfig = { ...form, probe_enabled: probeEnabled.value }
    const saved = await action.run(() => adminAPI.relay.updateGeneralConfig(body), t('common.saved'))
    if (saved) emit('saved', saved)
  } finally {
    saving.value = false
  }
}
</script>
