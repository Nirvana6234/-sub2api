<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Settings2, X, Save, Loader2, CheckCircle2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { addUpstreamRecharge, listUpstreamRecharges, updateSiteSettings } from '../../api/upstream'
import type { UpstreamRechargeEntry, UpstreamSite, UpstreamSiteResponse } from '../../types/upstream'

const props = defineProps<{
  open: boolean
  site: UpstreamSite | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'saved', siteId: string, settings: { balanceThreshold: number | null; manualAccountingEnabled: boolean; manualGroupMultipliers?: Record<string, number> }): void
  (event: 'recharged', siteId: string, site: UpstreamSiteResponse): void
}>()

const { t } = useI18n()

const useCustomThreshold = ref(false)
const balanceThreshold = ref('')
const isSaving = ref(false)
const showSuccess = ref(false)
const errorMsg = ref<string | null>(null)
const rechargeAmount = ref('')
const rechargeNote = ref('')
const rechargeEntries = ref<UpstreamRechargeEntry[]>([])
const isRechargeSaving = ref(false)
const rechargeLoadError = ref(false)
const manualGroupMultipliers = ref<Array<{ name: string; multiplier: string }>>([])

watch(() => props.open, (isOpen) => {
  if (!isOpen || !props.site) return
  const s = props.site.settings
  if (s.balanceThreshold != null) {
    useCustomThreshold.value = true
    balanceThreshold.value = String(s.balanceThreshold)
  } else {
    useCustomThreshold.value = false
    balanceThreshold.value = ''
  }
  errorMsg.value = null
  showSuccess.value = false
  rechargeAmount.value = ''
  rechargeNote.value = ''
  rechargeEntries.value = []
  rechargeLoadError.value = false
  manualGroupMultipliers.value = Object.entries(s.manualGroupMultipliers ?? {}).map(([name, multiplier]) => ({ name, multiplier: String(multiplier) }))
  void loadRecharges(props.site.id)
})

const loadRecharges = async (siteId: string) => {
  try {
    rechargeEntries.value = await listUpstreamRecharges(siteId)
  } catch {
    rechargeLoadError.value = true
  }
}

const save = async () => {
  if (isSaving.value || !props.site) return
  isSaving.value = true
  errorMsg.value = null
  try {
    const settings = {
      balanceThreshold: useCustomThreshold.value ? (Number.parseFloat(balanceThreshold.value) || null) : null,
      manualAccountingEnabled: props.site.settings.manualAccountingEnabled,
      manualGroupMultipliers: Object.fromEntries(manualGroupMultipliers.value
        .map(({ name, multiplier }) => [name.trim(), Number.parseFloat(multiplier)] as const)
        .filter(([name, multiplier]) => name && Number.isFinite(multiplier) && multiplier >= 0)),
    }
    await updateSiteSettings(props.site.id, settings)
    emit('saved', props.site.id, settings)
    showSuccess.value = true
    setTimeout(() => { showSuccess.value = false }, 2000)
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : 'admin.upstream.errors.unknown'
  } finally {
    isSaving.value = false
  }
}

const addManualGroupMultiplier = () => {
  manualGroupMultipliers.value.push({ name: '', multiplier: '' })
}

const removeManualGroupMultiplier = (index: number) => {
  manualGroupMultipliers.value.splice(index, 1)
}

const addRecharge = async () => {
  if (isRechargeSaving.value || !props.site) return
  const amount = Number.parseFloat(rechargeAmount.value)
  if (!Number.isFinite(amount) || amount <= 0) {
    errorMsg.value = 'admin.upstream.siteSettings.rechargeInvalid'
    return
  }
  isRechargeSaving.value = true
  errorMsg.value = null
  try {
    const site = await addUpstreamRecharge(props.site.id, amount, rechargeNote.value.trim())
    rechargeAmount.value = ''
    rechargeNote.value = ''
    emit('recharged', props.site.id, site)
    await loadRecharges(props.site.id)
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : 'admin.upstream.errors.unknown'
  } finally {
    isRechargeSaving.value = false
  }
}
</script>

<template>
  <Teleport defer to="body">
    <div v-if="open && site" class="fixed inset-0 z-[100] flex items-center justify-center p-4">
      <div class="absolute inset-0 bg-background/80 backdrop-blur-sm" @click="emit('close')"></div>

      <div
        role="dialog"
        aria-modal="true"
        @click.stop
        class="relative w-full max-w-md overflow-hidden rounded-2xl border border-border/60 bg-card text-card-foreground shadow-2xl animate-in fade-in zoom-in-95 duration-200"
      >
        <div class="absolute left-0 right-0 top-0 h-1 bg-gradient-to-r from-primary via-accent to-primary" />

        <div class="flex items-center justify-between px-6 pt-6 pb-4 border-b border-border/40">
          <div class="flex items-center gap-3">
            <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10 text-primary">
              <Settings2 class="h-5 w-5" />
            </div>
            <div>
              <h2 class="text-base font-semibold text-foreground">{{ t('admin.upstream.siteSettings.title') }}</h2>
              <p class="text-xs text-muted-foreground">{{ site.name }}</p>
            </div>
          </div>
          <button
            type="button"
            class="rounded-md p-1 text-muted-foreground transition-colors hover:bg-surface-elevated hover:text-foreground"
            @click="emit('close')"
          >
            <X class="h-5 w-5" />
          </button>
        </div>

        <div class="px-6 py-5 space-y-5">
          <!-- Balance Threshold Override -->
          <div class="space-y-3">
            <div class="flex items-center justify-between">
              <label class="text-sm font-medium text-foreground">{{ t('admin.upstream.siteSettings.balanceThreshold') }}</label>
              <label class="relative inline-flex items-center cursor-pointer">
                <input type="checkbox" v-model="useCustomThreshold" class="sr-only peer">
                <div class="w-9 h-5 bg-surface-elevated rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-border after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-primary"></div>
              </label>
            </div>
            <p class="text-xs text-muted-foreground">{{ t('admin.upstream.siteSettings.balanceThresholdHelp') }}</p>
            <div v-if="useCustomThreshold" class="animate-in slide-in-from-top-2 fade-in duration-200">
              <Input
                type="number"
                v-model="balanceThreshold"
                min="0"
                step="0.01"
                :placeholder="t('admin.upstream.siteSettings.balanceThresholdPlaceholder')"
                class="max-w-[200px]"
              />
            </div>
          </div>

          <div class="space-y-3 border-t border-border/40 pt-5">
            <div>
              <h3 class="text-sm font-semibold text-foreground">{{ t('admin.upstream.siteSettings.manualAccounting') }}</h3>
              <p class="mt-1 text-xs leading-5 text-muted-foreground">{{ t('admin.upstream.siteSettings.manualAccountingHelp') }}</p>
            </div>
            <div class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-2">
              <Input v-model="rechargeAmount" type="number" min="0.000001" step="0.01" :placeholder="t('admin.upstream.siteSettings.rechargeAmountPlaceholder')" :disabled="isRechargeSaving" />
              <Input v-model="rechargeNote" :placeholder="t('admin.upstream.siteSettings.rechargeNotePlaceholder')" :disabled="isRechargeSaving" />
            </div>
            <Button type="button" class="w-full" :disabled="isRechargeSaving" @click="addRecharge">
              <Loader2 v-if="isRechargeSaving" class="mr-2 h-4 w-4 animate-spin" />
              <Save v-else class="mr-2 h-4 w-4" />
              {{ t('admin.upstream.siteSettings.addRecharge') }}
            </Button>
            <p v-if="rechargeLoadError" class="text-xs text-warning">{{ t('admin.upstream.siteSettings.rechargeLoadError') }}</p>
            <div v-else-if="rechargeEntries.length" class="max-h-32 space-y-1 overflow-y-auto rounded-lg border border-border/40 bg-surface/30 p-2">
              <div v-for="entry in rechargeEntries" :key="entry.id" class="flex items-center justify-between gap-3 text-xs">
                <span class="min-w-0 truncate text-muted-foreground">{{ entry.note || t('admin.upstream.siteSettings.rechargeNoNote') }}</span>
                <span class="shrink-0 font-medium tabular-nums text-foreground">+{{ entry.amount.toFixed(2) }}</span>
              </div>
            </div>
            <p v-else class="text-xs text-muted-foreground">{{ t('admin.upstream.siteSettings.rechargeEmpty') }}</p>

            <div class="space-y-2 border-t border-border/40 pt-4">
              <div class="flex items-center justify-between gap-3">
                <div>
                  <h4 class="text-sm font-medium text-foreground">{{ t('admin.upstream.siteSettings.manualGroupMultipliers') }}</h4>
                  <p class="mt-1 text-xs leading-5 text-muted-foreground">{{ t('admin.upstream.siteSettings.manualGroupMultipliersHelp') }}</p>
                </div>
                <Button type="button" size="sm" variant="secondary" @click="addManualGroupMultiplier">{{ t('admin.upstream.siteSettings.addManualGroupMultiplier') }}</Button>
              </div>
              <div v-for="(entry, index) in manualGroupMultipliers" :key="index" class="grid grid-cols-[minmax(0,1fr)_8rem_auto] gap-2">
                <Input v-model="entry.name" :placeholder="t('admin.upstream.siteSettings.manualGroupNamePlaceholder')" />
                <Input v-model="entry.multiplier" type="number" min="0" step="0.0001" inputmode="decimal" :placeholder="t('admin.upstream.siteSettings.manualMultiplierPlaceholder')" />
                <Button type="button" size="sm" variant="ghost" :aria-label="t('admin.upstream.siteSettings.removeManualGroupMultiplier')" @click="removeManualGroupMultiplier(index)">
                  <X class="h-4 w-4" />
                </Button>
              </div>
            </div>
          </div>

          <p v-if="errorMsg" class="text-sm text-destructive rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2">
            {{ t(errorMsg) }}
          </p>
        </div>

        <div class="px-6 pb-6 flex justify-end gap-3">
          <Button variant="ghost" @click="emit('close')">
            {{ t('admin.upstream.siteSettings.cancel') }}
          </Button>
          <Button :disabled="isSaving" @click="save">
            <Loader2 v-if="isSaving" class="h-4 w-4 animate-spin mr-2" />
            <CheckCircle2 v-else-if="showSuccess" class="h-4 w-4 mr-2 text-green-400" />
            <Save v-else class="h-4 w-4 mr-2" />
            {{ showSuccess ? t('admin.upstream.siteSettings.saveSuccess') : (isSaving ? t('admin.upstream.siteSettings.saving') : t('admin.upstream.siteSettings.save')) }}
          </Button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
