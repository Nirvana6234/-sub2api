<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.relay.nodes.hint') }}</p>
      <button
        v-if="pendingCount > 0"
        type="button"
        class="btn btn-secondary btn-sm"
        data-test="reject-all-pending"
        @click="confirmRejectAll"
      >
        {{ t('admin.relay.nodes.rejectAllPending', { count: pendingCount }) }}
      </button>
    </div>

    <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
      <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
        <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400">
          <tr>
            <th class="px-3 py-2">{{ t('admin.relay.nodes.colNode') }}</th>
            <th class="px-3 py-2">{{ t('common.status') }}</th>
            <th class="px-3 py-2">{{ t('admin.relay.nodes.colHealth') }}</th>
            <th class="px-3 py-2">{{ t('admin.relay.nodes.colLoad') }}</th>
            <th class="px-3 py-2">{{ t('admin.relay.nodes.colAssigned') }}</th>
            <th class="px-3 py-2">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <!-- master row -->
          <tr data-test="master-row" class="bg-primary-50/40 dark:bg-primary-900/10">
            <td class="px-3 py-2">
              <div class="font-medium text-gray-900 dark:text-white">{{ t('admin.relay.nodes.master') }}</div>
              <div class="text-xs text-gray-500">{{ t('admin.relay.nodes.masterHint') }}</div>
            </td>
            <td class="px-3 py-2">
              <span class="badge badge-success">{{ t('admin.relay.status.master') }}</span>
            </td>
            <td class="px-3 py-2 text-xs text-gray-500">-</td>
            <td class="px-3 py-2 text-xs">
              {{ t('admin.relay.nodes.masterRatio', { percent: masterRatio }) }}
            </td>
            <td class="px-3 py-2 text-xs">
              {{ t('admin.relay.nodes.assignedCounts', { keys: keyCount(0), users: userCount(0) }) }}
            </td>
            <td class="px-3 py-2"></td>
          </tr>

          <tr v-if="nodes.length === 0">
            <td colspan="6" class="px-3 py-8 text-center text-gray-500">{{ t('admin.relay.nodes.empty') }}</td>
          </tr>

          <tr v-for="n in nodes" :key="n.id" :data-test="`node-${n.id}`">
            <td class="px-3 py-2 align-top">
              <div class="font-medium text-gray-900 dark:text-white">{{ n.name || n.hostname || `#${n.id}` }}</div>
              <div v-if="n.public_domain" class="text-xs text-gray-500">
                <span class="text-gray-400">{{ t('admin.relay.nodes.domain') }}:</span> {{ n.public_domain }}
              </div>
              <div class="text-xs text-gray-500">
                <span class="text-gray-400">{{ t('admin.relay.nodes.registeredIp') }}:</span> {{ n.registered_ip || '-' }}<span v-if="n.region"> · {{ n.region }}</span>
              </div>
              <div v-if="n.program_version" class="text-xs text-gray-400">{{ t('admin.relay.nodes.version') }} {{ n.program_version }}</div>
            </td>
            <td class="px-3 py-2 align-top">
              <span class="badge" :class="statusClass(n.status)">{{ t(`admin.relay.status.${n.status}`) }}</span>
            </td>
            <td class="px-3 py-2 align-top">
              <template v-if="inService(n)">
                <div class="flex flex-wrap gap-1">
                  <span class="badge" :class="healthOf(n.id)?.online ? 'badge-success' : 'badge-danger'">
                    {{ healthOf(n.id)?.online ? t('admin.relay.health.online') : t('admin.relay.health.offline') }}
                  </span>
                  <span v-if="healthOf(n.id)?.external.unreachable" class="badge badge-danger">
                    {{ t('admin.relay.health.unreachable') }}
                  </span>
                  <span v-if="healthOf(n.id)?.external.degraded" class="badge badge-warning">
                    {{ t('admin.relay.health.degraded') }}
                  </span>
                  <span v-if="healthOf(n.id) && !healthOf(n.id)?.external.probe_checked" class="badge badge-gray">
                    {{ t('admin.relay.health.notProbed') }}
                  </span>
                  <span v-if="skewTooBig(n.id)" class="badge badge-warning">
                    {{ t('admin.relay.health.clockSkew', { seconds: skewSeconds(n.id) }) }}
                  </span>
                  <span v-if="(healthOf(n.id)?.voucher_shortfall ?? 0) > 0" class="badge badge-danger">
                    {{ t('admin.relay.health.shortfall', { count: healthOf(n.id)?.voucher_shortfall }) }}
                  </span>
                </div>
                <div class="mt-1 text-xs text-gray-500">
                  {{ t('admin.relay.health.dnsNow') }}:
                  <span :class="dnsClass(healthOf(n.id)?.external.dns)">
                    {{ dnsText(n.id) }}
                  </span>
                </div>
                <div v-if="certDays(n.id) !== null" class="text-xs" :class="certDays(n.id)! < 7 ? 'text-red-600' : 'text-gray-500'">
                  {{ t('admin.relay.health.certDays', { days: certDays(n.id) }) }}
                </div>
                <div v-if="healthOf(n.id)?.external.probe_error" class="text-xs text-red-600">
                  {{ healthOf(n.id)?.external.probe_error }}
                </div>
              </template>
              <span v-else class="text-xs text-gray-400">-</span>
            </td>
            <td class="px-3 py-2 align-top text-xs">
              <template v-if="inService(n) && healthOf(n.id)?.heartbeat">
                <div>{{ t('admin.relay.health.load', { percent: (healthOf(n.id)?.load_percent ?? 0).toFixed(0) }) }}</div>
                <div>
                  {{ t('admin.relay.health.requests', { requests: hb(n.id).requests_1m ?? 0, errors: hb(n.id).errors_1m ?? 0 }) }}
                </div>
                <div>{{ formatMbps(hb(n.id).rx_bytes_per_sec) }} ↓ / {{ formatMbps(hb(n.id).tx_bytes_per_sec) }} ↑</div>
                <div>
                  {{ t('admin.relay.health.inflight', { inflight: hb(n.id).inflight_requests ?? 0, conns: hb(n.id).client_connections ?? 0 }) }}
                </div>
              </template>
              <span v-else class="text-gray-400">-</span>
            </td>
            <td class="px-3 py-2 align-top text-xs">
              {{ t('admin.relay.nodes.assignedCounts', { keys: keyCount(n.id), users: userCount(n.id) }) }}
              <div v-if="inService(n) && (hb(n.id).reserved_total_micros ?? 0) > 0" class="text-gray-500">
                {{ t('admin.relay.nodes.reserved', { amount: ((hb(n.id).reserved_total_micros ?? 0) / 1e8).toFixed(2) }) }}
              </div>
            </td>
            <td class="px-3 py-2 align-top">
              <div class="flex flex-wrap gap-1">
                <button v-if="n.status === 'pending'" type="button" class="btn btn-primary btn-sm" data-test="activate" @click="openActivate(n)">
                  {{ t('admin.relay.actions.activate') }}
                </button>
                <button v-if="n.status === 'pending'" type="button" class="btn btn-secondary btn-sm" data-test="reject" @click="confirmSimple('reject', n)">
                  {{ t('admin.relay.actions.reject') }}
                </button>
                <button v-if="n.status === 'active'" type="button" class="btn btn-secondary btn-sm" data-test="drain" @click="confirmSimple('drain', n)">
                  {{ t('admin.relay.actions.drain') }}
                </button>
                <button v-if="n.status === 'draining'" type="button" class="btn btn-secondary btn-sm" data-test="undrain" @click="confirmSimple('undrain', n)">
                  {{ t('admin.relay.actions.undrain') }}
                </button>
                <button v-if="n.status === 'active' || n.status === 'draining'" type="button" class="btn btn-secondary btn-sm" data-test="disable" @click="confirmSimple('disable', n)">
                  {{ t('admin.relay.actions.disable') }}
                </button>
                <button v-if="n.status === 'disabled'" type="button" class="btn btn-secondary btn-sm" data-test="enable" @click="confirmSimple('enable', n)">
                  {{ t('admin.relay.actions.enable') }}
                </button>
                <button v-if="n.status === 'active' || n.status === 'draining'" type="button" class="btn btn-secondary btn-sm" data-test="move-keys" @click="openMoveKeys(n)">
                  {{ t('admin.relay.actions.moveKeys') }}
                </button>
                <button v-if="n.status === 'active' || n.status === 'draining' || n.status === 'disabled'" type="button" class="btn btn-secondary btn-sm" data-test="replace" @click="openReplace(n)">
                  {{ t('admin.relay.actions.replace') }}
                </button>
                <button v-if="n.status === 'active' || n.status === 'draining' || n.status === 'disabled'" type="button" class="btn btn-secondary btn-sm" data-test="edit-domain" @click="openEditDomain(n)">
                  {{ t('admin.relay.actions.editDomain') }}
                </button>
                <button v-if="n.status === 'active' || n.status === 'draining'" type="button" class="btn btn-secondary btn-sm" data-test="reclaim" @click="confirmSimple('reclaim', n)">
                  {{ t('admin.relay.actions.reclaim') }}
                </button>
                <button v-if="n.status === 'active' || n.status === 'draining'" type="button" class="btn btn-danger btn-sm" data-test="revoke" @click="openRevoke(n)">
                  {{ t('admin.relay.actions.revoke') }}
                </button>
                <button v-if="inService(n)" type="button" class="btn btn-secondary btn-sm" data-test="details" @click="openDetails(n)">
                  {{ t('admin.relay.actions.details') }}
                </button>
              </div>
              <label v-if="inService(n)" class="mt-2 flex items-center gap-2 text-xs text-gray-500">
                <Toggle :model-value="n.allow_multi_ip" @update:model-value="(v: boolean) => setMultiIP(n, v)" />
                {{ t('admin.relay.nodes.allowMultiIp') }}
              </label>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- activate -->
    <BaseDialog :show="dialog.kind === 'activate'" :title="t('admin.relay.activate.title')" width="normal" @close="closeDialog">
      <div v-if="dialog.kind === 'activate'" class="space-y-3" data-test="activate-form">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.relay.activate.fingerprintHint') }}</p>
        <div>
          <label class="input-label">{{ t('admin.relay.activate.registeredFingerprint') }}</label>
          <code class="block break-all rounded bg-gray-100 p-2 text-xs dark:bg-dark-700">{{ dialog.node.identity_fingerprint }}</code>
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.activate.typedFingerprint') }}</label>
          <input v-model="form.fingerprint" class="input font-mono text-xs" data-test="fingerprint" autocomplete="off" />
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.relay.activate.typedFingerprintHint') }}</p>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="input-label">{{ t('common.name') }}</label>
            <input v-model="form.name" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.relay.activate.region') }}</label>
            <input v-model="form.region" class="input" />
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.activate.domain') }}</label>
          <div class="flex gap-2">
            <input v-model="form.domain" class="input flex-1" data-test="domain" :placeholder="t('admin.relay.activate.endpointPlaceholder')" @input="domainCheck = null" />
            <button type="button" class="btn btn-secondary" :disabled="!form.domain.trim() || checking" data-test="check-domain" @click="checkDomain">
              {{ t('admin.relay.activate.checkDomain') }}
            </button>
          </div>
          <p v-if="domainCheck" class="mt-1 text-xs" :class="domainCheck.state === 'node' || domainCheck.state === 'direct' ? 'text-green-600' : 'text-amber-600'" data-test="domain-check-result">
            {{ domainCheckText }}
          </p>
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.relay.activate.endpointHint') }}</p>
          <label v-if="domainCheck && domainCheck.state !== 'node' && domainCheck.state !== 'direct'" class="mt-2 flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
            <input v-model="form.ignoreDns" type="checkbox" data-test="ignore-dns" />
            {{ t('admin.relay.activate.ignoreDns') }}
          </label>
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.activate.bandwidth') }}</label>
          <input v-model.number="form.bandwidth" type="number" min="0" class="input" />
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.relay.activate.bandwidthHint') }}</p>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="closeDialog">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="busy || !canActivate" data-test="activate-submit" @click="submitActivate">
            {{ t('admin.relay.actions.activate') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- edit domain -->
    <BaseDialog :show="dialog.kind === 'editDomain'" :title="t('admin.relay.editDomain.title')" @close="closeDialog">
      <div v-if="dialog.kind === 'editDomain'" class="space-y-3" data-test="edit-domain-form">
        <p class="text-sm text-gray-600 dark:text-gray-300">
          {{ t('admin.relay.editDomain.hint', { name: nodeLabel(dialog.node), count: keyCount(dialog.node.id) }) }}
        </p>
        <div>
          <label class="input-label">{{ t('admin.relay.activate.domain') }}</label>
          <div class="flex gap-2">
            <input v-model="form.domain" class="input flex-1" data-test="edit-domain-input" :placeholder="t('admin.relay.activate.endpointPlaceholder')" @input="domainCheck = null" />
            <button type="button" class="btn btn-secondary" :disabled="!form.domain.trim() || checking" data-test="edit-domain-check" @click="checkDomain">
              {{ t('admin.relay.activate.checkDomain') }}
            </button>
          </div>
          <p v-if="domainCheck" class="mt-1 text-xs" :class="domainCheck.state === 'node' || domainCheck.state === 'direct' ? 'text-green-600' : 'text-amber-600'" data-test="edit-domain-check-result">
            {{ domainCheckText }}
          </p>
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.relay.activate.endpointHint') }}</p>
          <label v-if="domainCheck && domainCheck.state !== 'node' && domainCheck.state !== 'direct'" class="mt-2 flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
            <input v-model="form.ignoreDns" type="checkbox" data-test="edit-domain-ignore-dns" />
            {{ t('admin.relay.editDomain.ignoreDns') }}
          </label>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="closeDialog">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="busy || !canEditDomain" data-test="edit-domain-submit" @click="submitEditDomain">
            {{ t('admin.relay.editDomain.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- revoke -->
    <BaseDialog :show="dialog.kind === 'revoke'" :title="t('admin.relay.revoke.title')" @close="closeDialog">
      <div class="space-y-3">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.relay.revoke.hint') }}</p>
        <label class="input-label">{{ t('admin.relay.revoke.reason') }}</label>
        <input v-model="form.reason" class="input" maxlength="500" data-test="revoke-reason" />
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="closeDialog">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-danger" :disabled="busy || !form.reason.trim()" data-test="revoke-submit" @click="submitRevoke">
            {{ t('admin.relay.actions.revoke') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- after a revoke: secrets the node had in memory must be replaced -->
    <BaseDialog :show="dialog.kind === 'revoked'" :title="t('admin.relay.revoke.followupTitle')" @close="closeDialog">
      <div v-if="dialog.kind === 'revoked'" class="space-y-2 text-sm text-gray-700 dark:text-gray-300" data-test="revoke-followup">
        <p>{{ t('admin.relay.revoke.followupIntro', { name: nodeLabel(dialog.node) }) }}</p>
        <ul class="list-disc pl-5">
          <li>{{ t('admin.relay.revoke.followupModeration') }}</li>
          <li>{{ t('admin.relay.revoke.followupPromptAudit') }}</li>
          <li>{{ t('admin.relay.revoke.followupWebSearch') }}</li>
          <li>{{ t('admin.relay.revoke.followupKeys') }}</li>
        </ul>
      </div>
      <template #footer>
        <div class="flex justify-end">
          <button type="button" class="btn btn-primary" data-test="revoke-followup-close" @click="closeDialog">{{ t('common.close') }}</button>
        </div>
      </template>
    </BaseDialog>

    <!-- replace -->
    <BaseDialog :show="dialog.kind === 'replace'" :title="t('admin.relay.replace.title')" @close="closeDialog">
      <div v-if="dialog.kind === 'replace'" class="space-y-3">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.relay.replace.hint', { name: nodeLabel(dialog.node) }) }}</p>
        <div>
          <label class="input-label">{{ t('admin.relay.replace.newNode') }}</label>
          <Select v-model="form.newNodeId" :options="pendingOptions" :placeholder="t('admin.relay.replace.newNodePlaceholder')" />
          <p v-if="pendingOptions.length === 0" class="mt-1 text-xs text-amber-600">{{ t('admin.relay.replace.noPending') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.relay.activate.typedFingerprint') }}</label>
          <input v-model="form.fingerprint" class="input font-mono text-xs" data-test="replace-fingerprint" autocomplete="off" />
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="closeDialog">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="busy || !form.newNodeId || !form.fingerprint.trim()" data-test="replace-submit" @click="submitReplace">
            {{ t('admin.relay.actions.replace') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- move keys -->
    <BaseDialog :show="dialog.kind === 'moveKeys'" :title="t('admin.relay.moveKeys.title')" @close="closeDialog">
      <div v-if="dialog.kind === 'moveKeys'" class="space-y-3">
        <p class="text-sm text-gray-600 dark:text-gray-300">
          {{ t('admin.relay.moveKeys.hint', { name: nodeLabel(dialog.node), count: keyCount(dialog.node.id) }) }}
        </p>
        <label class="input-label">{{ t('admin.relay.moveKeys.target') }}</label>
        <Select v-model="form.targetNode" :options="moveTargets(dialog.node.id)" />
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="closeDialog">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="busy" data-test="move-keys-submit" @click="submitMoveKeys">
            {{ t('admin.relay.actions.moveKeys') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- simple confirm -->
    <ConfirmDialog
      :show="dialog.kind === 'confirm'"
      :title="confirmTitle"
      :message="confirmMessage"
      :danger="dialog.kind === 'confirm' && (dialog.action === 'disable' || dialog.action === 'rejectAll')"
      @confirm="submitConfirm"
      @cancel="closeDialog"
    />

    <!-- details -->
    <BaseDialog :show="dialog.kind === 'details'" :title="t('admin.relay.details.title')" width="wide" @close="closeDialog">
      <div v-if="dialog.kind === 'details'" class="space-y-4" data-test="details-body">
        <dl class="grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-3">
          <template v-for="row in detailRows(dialog.node)" :key="row[0]">
            <dt class="text-gray-500">{{ row[0] }}</dt>
            <dd class="col-span-1 break-all text-gray-900 dark:text-gray-100 sm:col-span-2">{{ row[1] }}</dd>
          </template>
        </dl>
        <div>
          <h4 class="mb-1 text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.relay.details.recentLogs') }}</h4>
          <p v-if="detailLogs.length === 0" class="text-xs text-gray-500">{{ detailLogsMessage }}</p>
          <pre v-else class="max-h-72 overflow-auto rounded bg-gray-100 p-2 text-xs dark:bg-dark-800">{{ detailLogs.join('\n') }}</pre>
        </div>
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type {
  RelayDomainCheck,
  RelayDomainState,
  RelayHeartbeat,
  RelayKeyAssignmentSummary,
  RelayNode,
  RelayNodeHealth,
  RelayNodeStatus
} from '@/api/admin/relay'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores'
import type { StepUpController } from '@/composables/useStepUp'
import { daysUntil, formatBytes, formatMbps, formatTime, useRelayAction } from './useRelayAction'

const props = defineProps<{
  nodes: RelayNode[]
  healths: RelayNodeHealth[]
  masterRatio: number
  keySummary: RelayKeyAssignmentSummary | null
  userSummary: Record<string, number>
  stepUp: StepUpController
}>()
const emit = defineEmits<{ (e: 'refresh'): void }>()

const { t } = useI18n()
const appStore = useAppStore()
const action = useRelayAction(props.stepUp)

type SimpleAction = 'reject' | 'rejectAll' | 'drain' | 'undrain' | 'disable' | 'enable' | 'reclaim'
type Dialog =
  | { kind: 'none' }
  | { kind: 'activate'; node: RelayNode }
  | { kind: 'editDomain'; node: RelayNode }
  | { kind: 'revoke'; node: RelayNode }
  | { kind: 'replace'; node: RelayNode }
  | { kind: 'moveKeys'; node: RelayNode }
  | { kind: 'details'; node: RelayNode }
  | { kind: 'revoked'; node: RelayNode }
  | { kind: 'confirm'; action: SimpleAction; node?: RelayNode }

const dialog = ref<Dialog>({ kind: 'none' })
const busy = ref(false)
const checking = ref(false)
const domainCheck = ref<RelayDomainCheck | null>(null)
const detailLogs = ref<string[]>([])
const detailLogsMessage = ref('')

const form = reactive({
  fingerprint: '',
  name: '',
  region: '',
  domain: '',
  bandwidth: 0,
  ignoreDns: false,
  reason: '',
  newNodeId: null as number | null,
  targetNode: -1 as number
})

const pendingCount = computed(() => props.nodes.filter((n) => n.status === 'pending').length)

const pendingOptions = computed(() =>
  props.nodes
    .filter((n) => n.status === 'pending')
    .map((n) => ({ value: n.id, label: `${nodeLabel(n)} (${n.registered_ip || '-'})` }))
)

function nodeLabel(n: RelayNode): string {
  return n.name || n.hostname || `#${n.id}`
}

function inService(n: RelayNode): boolean {
  return n.status === 'active' || n.status === 'draining'
}

function statusClass(s: RelayNodeStatus): string {
  switch (s) {
    case 'active':
      return 'badge-success'
    case 'draining':
      return 'badge-warning'
    case 'pending':
      return 'badge-primary'
    case 'disabled':
      return 'badge-gray'
    default:
      return 'badge-danger'
  }
}

function healthOf(id: number): RelayNodeHealth | undefined {
  return props.healths.find((h) => h.node_id === id)
}

function hb(id: number): RelayHeartbeat {
  return healthOf(id)?.heartbeat ?? {}
}

function keyCount(id: number): number {
  if (!props.keySummary) return 0
  return id === 0 ? props.keySummary.master : (props.keySummary.nodes[String(id)]?.total ?? 0)
}

function userCount(id: number): number {
  return props.userSummary[String(id)] ?? 0
}

const SKEW_LIMIT_MS = 30_000
function skewTooBig(id: number): boolean {
  return Math.abs(healthOf(id)?.clock_skew_ms ?? 0) > SKEW_LIMIT_MS
}
function skewSeconds(id: number): number {
  return Math.round((healthOf(id)?.clock_skew_ms ?? 0) / 1000)
}

function certDays(id: number): number | null {
  return daysUntil(healthOf(id)?.external.cert_not_after)
}

function dnsClass(state: RelayDomainState | undefined): string {
	if (state === 'node' || state === 'direct') return 'text-green-600'
  if (state === 'other' || state === 'failed') return 'text-red-600'
  return 'text-gray-500'
}

function dnsText(id: number): string {
  const ext = healthOf(id)?.external
  if (!ext) return '-'
  const label = t(`admin.relay.dns.${ext.dns}`)
  return ext.dns_resolved?.length ? `${label} (${ext.dns_resolved.join(', ')})` : label
}

function resetForm(): void {
  Object.assign(form, {
    fingerprint: '',
    name: '',
    region: '',
    domain: '',
    bandwidth: 0,
    ignoreDns: false,
    reason: '',
    newNodeId: null,
    targetNode: -1
  })
  domainCheck.value = null
}

function closeDialog(): void {
  dialog.value = { kind: 'none' }
}

// ---- activate ----

function openActivate(n: RelayNode): void {
  resetForm()
  form.name = n.name
  form.region = n.region
  form.domain = n.public_domain
  form.bandwidth = n.bandwidth_limit_mbps
  dialog.value = { kind: 'activate', node: n }
}

function openEditDomain(n: RelayNode): void {
  resetForm()
  form.domain = n.public_domain
  dialog.value = { kind: 'editDomain', node: n }
}

const canActivate = computed(() => {
  if (!form.fingerprint.trim() || !form.domain.trim()) return false
  // A domain that does not resolve to this node needs an explicit "go ahead anyway".
  if (domainCheck.value && domainCheck.value.state !== 'node' && domainCheck.value.state !== 'direct' && !form.ignoreDns) return false
  return true
})

const canEditDomain = computed(() => {
  if (!form.domain.trim()) return false
  if (domainCheck.value && domainCheck.value.state !== 'node' && domainCheck.value.state !== 'direct' && !form.ignoreDns) return false
  return true
})

const domainCheckText = computed(() => {
  const c = domainCheck.value
  if (!c) return ''
  const resolved = c.resolved?.length ? c.resolved.join(', ') : '-'
  return t(`admin.relay.activate.domainResult.${c.state}`, { resolved, expected: c.expected || '-' })
})

async function checkDomain(): Promise<void> {
  if (dialog.value.kind !== 'activate' && dialog.value.kind !== 'editDomain') return
  checking.value = true
  try {
    const id = dialog.value.node.id
    domainCheck.value =
      (await action.load(() => adminAPI.relay.checkNodeDomain(id, form.domain.trim()))) ?? null
  } finally {
    checking.value = false
  }
}

async function submitEditDomain(): Promise<void> {
  if (dialog.value.kind !== 'editDomain') return
  const id = dialog.value.node.id
  busy.value = true
  try {
    const ok = await action.runOk(
      () => adminAPI.relay.updateNodeDomain(id, {
        public_domain: form.domain.trim(),
        ignore_dns_mismatch: form.ignoreDns
      }),
      t('admin.relay.editDomain.done')
    )
    if (ok) {
      closeDialog()
      emit('refresh')
    }
  } finally {
    busy.value = false
  }
}

async function submitActivate(): Promise<void> {
  if (dialog.value.kind !== 'activate') return
  const id = dialog.value.node.id
  busy.value = true
  try {
    const ok = await action.runOk(
      () =>
        adminAPI.relay.activateNode(id, {
          fingerprint: form.fingerprint.trim(),
          name: form.name.trim(),
          public_domain: form.domain.trim(),
          bandwidth_limit_mbps: Math.max(0, Math.floor(form.bandwidth || 0)),
          region: form.region.trim(),
          ignore_dns_mismatch: form.ignoreDns
        }),
      t('admin.relay.activate.done')
    )
    if (ok) {
      closeDialog()
      emit('refresh')
    }
  } finally {
    busy.value = false
  }
}

// ---- revoke / replace / move keys ----

function openRevoke(n: RelayNode): void {
  resetForm()
  dialog.value = { kind: 'revoke', node: n }
}

async function submitRevoke(): Promise<void> {
  if (dialog.value.kind !== 'revoke') return
  const id = dialog.value.node.id
  busy.value = true
  try {
    const node = dialog.value.node
    const ok = await action.runOk(() => adminAPI.relay.revokeNode(id, form.reason.trim()), t('admin.relay.revoke.done'))
    if (ok) {
      // The node held the moderation key, prompt-audit credentials and web-search key in memory: say what to rotate.
      dialog.value = { kind: 'revoked', node }
      emit('refresh')
    }
  } finally {
    busy.value = false
  }
}

function openReplace(n: RelayNode): void {
  resetForm()
  dialog.value = { kind: 'replace', node: n }
}

async function submitReplace(): Promise<void> {
  if (dialog.value.kind !== 'replace' || !form.newNodeId) return
  const id = dialog.value.node.id
  const newId = form.newNodeId
  busy.value = true
  try {
    const res = await action.run(() => adminAPI.relay.replaceNode(id, newId, form.fingerprint.trim()))
    if (res) {
      appStore.showSuccess(t('admin.relay.replace.done', { count: res.moved_keys }))
      closeDialog()
      emit('refresh')
    }
  } finally {
    busy.value = false
  }
}

function moveTargets(fromId: number) {
  const opts: { value: number; label: string }[] = [{ value: -1, label: t('admin.relay.moveKeys.auto') }]
  if (props.masterRatio > 0) opts.push({ value: 0, label: t('admin.relay.nodes.master') })
  for (const n of props.nodes) {
    if (n.id !== fromId && n.status === 'active') opts.push({ value: n.id, label: nodeLabel(n) })
  }
  return opts
}

function openMoveKeys(n: RelayNode): void {
  resetForm()
  dialog.value = { kind: 'moveKeys', node: n }
}

async function submitMoveKeys(): Promise<void> {
  if (dialog.value.kind !== 'moveKeys') return
  const id = dialog.value.node.id
  const target = form.targetNode
  busy.value = true
  try {
    const res = await action.run(() => adminAPI.relay.moveNodeKeys(id, target < 0 ? undefined : target))
    if (res) {
      appStore.showSuccess(t('admin.relay.moveKeys.done', { moved: res.moved, left: res.left }))
      closeDialog()
      emit('refresh')
    }
  } finally {
    busy.value = false
  }
}

// ---- simple confirmations ----

function confirmSimple(a: SimpleAction, n: RelayNode): void {
  dialog.value = { kind: 'confirm', action: a, node: n }
}

function confirmRejectAll(): void {
  dialog.value = { kind: 'confirm', action: 'rejectAll' }
}

const confirmTitle = computed(() =>
  dialog.value.kind === 'confirm' ? t(`admin.relay.confirm.${dialog.value.action}.title`) : ''
)
const confirmMessage = computed(() => {
  const d = dialog.value
  if (d.kind !== 'confirm') return ''
  return t(`admin.relay.confirm.${d.action}.message`, { name: d.node ? nodeLabel(d.node) : '', count: pendingCount.value })
})

async function submitConfirm(): Promise<void> {
  const d = dialog.value
  if (d.kind !== 'confirm') return
  closeDialog()
  const id = d.node?.id ?? 0
  // Calls that return nothing resolve to true so a successful run is distinguishable from a failed one.
  const done = async (fn: () => Promise<unknown>): Promise<true> => {
    await fn()
    return true
  }
  const calls: Record<SimpleAction, () => Promise<unknown>> = {
    reject: () => done(() => adminAPI.relay.rejectNode(id)),
    rejectAll: () => adminAPI.relay.rejectAllPending(),
    drain: () => done(() => adminAPI.relay.drainNode(id)),
    undrain: () => done(() => adminAPI.relay.undrainNode(id)),
    disable: () => done(() => adminAPI.relay.disableNode(id)),
    enable: () => done(() => adminAPI.relay.enableNode(id)),
    reclaim: () => adminAPI.relay.reclaimNodeQuota(id)
  }
  const res = await action.run(calls[d.action])
  if (res !== undefined) {
    if (d.action === 'reclaim') {
      const r = res as { recall_sent: number; voided: number; voided_amount_display: string }
      appStore.showSuccess(
        t('admin.relay.confirm.reclaim.done', { sent: r.recall_sent, voided: r.voided, amount: r.voided_amount_display })
      )
    } else {
      appStore.showSuccess(t(`admin.relay.confirm.${d.action}.done`))
    }
    emit('refresh')
  }
}

async function setMultiIP(n: RelayNode, allow: boolean): Promise<void> {
  await action.runOk(() => adminAPI.relay.setNodeAllowMultiIP(n.id, allow))
  // Refresh either way so the toggle snaps back when the change was refused.
  emit('refresh')
}

// ---- details ----

function detailRows(n: RelayNode): [string, string][] {
  const h = healthOf(n.id)
  const b = hb(n.id)
  return [
    [t('admin.relay.details.id'), String(n.id)],
    [t('admin.relay.details.fingerprint'), n.identity_fingerprint],
    [t('admin.relay.details.hostname'), n.hostname || '-'],
    [t('admin.relay.details.lastSeen'), `${formatTime(n.last_seen_at)} (${n.last_seen_ip || '-'})`],
    [t('admin.relay.details.activatedAt'), formatTime(n.activated_at)],
    [t('admin.relay.details.bandwidth'), n.bandwidth_limit_mbps ? `${n.bandwidth_limit_mbps} Mbps` : t('admin.relay.details.unlimited')],
    [t('admin.relay.details.cpu'), `${(b.cpu_percent ?? 0).toFixed(0)}%`],
    [t('admin.relay.details.memory'), formatBytes(b.memory_bytes)],
    [t('admin.relay.details.disk'), formatBytes(b.disk_free_bytes)],
    [t('admin.relay.details.activeUsers'), `${b.active_users ?? 0} / ${b.active_keys ?? 0}`],
    [t('admin.relay.details.billingBacklog'), String(b.billing_backlog ?? 0)],
    [t('admin.relay.details.logBacklog'), `${b.log_backlog ?? 0} / ${b.log_dropped ?? 0}`],
    [t('admin.relay.details.configVersion'), b.config_version || '-'],
    [t('admin.relay.details.probeAt'), formatTime(h?.external.probe_at)],
    ...Object.entries(n.system_info ?? {}).map(([k, v]) => [k, v] as [string, string])
  ]
}

async function openDetails(n: RelayNode): Promise<void> {
  detailLogs.value = []
  detailLogsMessage.value = t('common.loading')
  dialog.value = { kind: 'details', node: n }
  const res = await action.load(() => adminAPI.relay.nodeLogs(n.id, { tail: true }))
  if (!res) {
    detailLogsMessage.value = t('admin.relay.details.logsFailed')
    return
  }
  const result = res.nodes?.[0]
  if (result && result.status !== 'ok') {
    detailLogsMessage.value = result.error || t(`admin.relay.logs.nodeStatus.${result.status}`)
    return
  }
  const lines = (res.records ?? []).map((r) => {
    const rec = r.record as { level?: string; component?: string; message?: string }
    const when = r.ts ? new Date(r.ts).toLocaleString() : ''
    return `${when} ${rec.level ?? ''} ${rec.component ?? ''} ${rec.message ?? JSON.stringify(rec)}`.trim()
  })
  detailLogs.value = lines
  detailLogsMessage.value = t('admin.relay.details.noLogs')
}
</script>
