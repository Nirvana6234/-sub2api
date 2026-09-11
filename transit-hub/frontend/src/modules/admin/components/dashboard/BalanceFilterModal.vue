<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { AlertCircle, Filter, Loader2, Plus, Trash2, X } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { getBalanceFilter, saveBalanceFilter, type BalanceFilterConfig } from '../../api/dashboardAdmin'

// 与后端 dashboard.DefaultUSDToCNYRate 保持一致，仅用于输入框 placeholder 提示。
const DEFAULT_USD_TO_CNY_RATE = 7.0

const props = defineProps<{
  open: boolean
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'saved'): void
}>()

const { t } = useI18n()

const loading = ref(false)
const saving = ref(false)
const errorKey = ref<string | null>(null)

const excludeAdmin = ref(true)
const excludeBalances = ref<number[]>([])
const newBalanceInput = ref('')
// 汇率用字符串 ref：空串表示「不改动」，与后端 preserve-on-omit 语义对应。
// 用 number ref 无法区分「用户清空」和「用户填 0」，前者必须保留库里原值。
const usdToCnyRate = ref('')

const loadConfig = async () => {
  loading.value = true
  errorKey.value = null
  try {
    const config = await getBalanceFilter()
    excludeAdmin.value = config.excludeAdmin
    excludeBalances.value = Array.isArray(config.excludeBalances) ? [...config.excludeBalances] : []
    usdToCnyRate.value = typeof config.usdToCnyRate === 'number' && config.usdToCnyRate > 0
      ? String(config.usdToCnyRate)
      : ''
  } catch (err) {
    errorKey.value = err instanceof Error ? err.message : 'admin.dashboard.balanceFilter.loadError'
  } finally {
    loading.value = false
  }
}

const addBalance = () => {
  const raw = String(newBalanceInput.value).trim()
  if (!raw) return
  const num = parseFloat(raw)
  if (isNaN(num)) return
  if (!excludeBalances.value.includes(num)) {
    excludeBalances.value.push(num)
  }
  newBalanceInput.value = ''
}

const removeBalance = (index: number) => {
  excludeBalances.value.splice(index, 1)
}

const handleSave = async () => {
  saving.value = true
  errorKey.value = null
  try {
    const config: BalanceFilterConfig = {
      excludeAdmin: excludeAdmin.value,
      excludeBalances: excludeBalances.value,
    }
    // 只在填了有效正数时才带上汇率。留空/填 0/填非法值一律不传，
    // 由后端保留库里已配置的值，避免改余额过滤条件时把汇率打回默认值。
    const parsedRate = parseFloat(String(usdToCnyRate.value).trim())
    if (!isNaN(parsedRate) && parsedRate > 0) {
      config.usdToCnyRate = parsedRate
    }
    await saveBalanceFilter(config)
    emit('saved')
    emit('close')
  } catch (err) {
    errorKey.value = err instanceof Error ? err.message : 'admin.dashboard.balanceFilter.saveError'
  } finally {
    saving.value = false
  }
}

watch(() => props.open, (isOpen) => {
  if (isOpen) {
    void loadConfig()
  }
})

onMounted(() => {
  if (props.open) {
    void loadConfig()
  }
})
</script>

<template>
  <Teleport defer to="body">
    <div v-if="open" class="fixed inset-0 z-[100] flex items-center justify-center p-4">
      <div class="absolute inset-0 bg-background/80 backdrop-blur-sm" @click="emit('close')"></div>

      <div
        role="dialog"
        aria-modal="true"
        class="relative w-full max-w-lg overflow-hidden rounded-[2rem] border border-border/60 bg-card text-card-foreground shadow-2xl shadow-primary/10 animate-in fade-in zoom-in-95 duration-200"
      >
        <div class="absolute left-0 right-0 top-0 h-1 bg-gradient-to-r from-accent via-primary to-accent" />

        <!-- Header -->
        <div class="flex items-start justify-between gap-4 px-6 pt-6">
          <div class="flex items-center gap-3">
            <div class="flex h-11 w-11 items-center justify-center rounded-full bg-accent/10 text-accent">
              <Filter class="h-5 w-5" />
            </div>
            <div>
              <h2 class="text-lg font-semibold text-foreground">{{ t('admin.dashboard.balanceFilter.title') }}</h2>
              <p class="text-sm text-muted-foreground">{{ t('admin.dashboard.balanceFilter.subtitle') }}</p>
            </div>
          </div>
          <button
            type="button"
            class="rounded-md p-1 text-muted-foreground transition-colors hover:bg-surface-elevated hover:text-foreground"
            :title="t('admin.dashboard.balanceFilter.close')"
            @click="emit('close')"
          >
            <X class="h-5 w-5" />
          </button>
        </div>

        <div class="space-y-5 px-6 py-6">
          <!-- Loading -->
          <div v-if="loading" class="flex items-center justify-center py-8">
            <Loader2 class="h-6 w-6 animate-spin text-primary/60" />
          </div>

          <template v-else>
            <!-- Exclude admin toggle -->
            <div class="flex items-center justify-between gap-4 rounded-xl border border-border/60 p-4">
              <div>
                <p class="text-sm font-medium text-foreground">{{ t('admin.dashboard.balanceFilter.excludeAdmin') }}</p>
                <p class="mt-0.5 text-xs text-muted-foreground">{{ t('admin.dashboard.balanceFilter.excludeAdminHelp') }}</p>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="excludeAdmin"
                class="relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/50"
                :class="excludeAdmin ? 'bg-primary' : 'bg-muted'"
                @click="excludeAdmin = !excludeAdmin"
              >
                <span
                  class="pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow-lg ring-0 transition duration-200"
                  :class="excludeAdmin ? 'translate-x-5' : 'translate-x-0'"
                />
              </button>
            </div>

            <!-- USD → CNY rate -->
            <div class="space-y-3 rounded-xl border border-border/60 p-4">
              <div>
                <p class="text-sm font-medium text-foreground">{{ t('admin.dashboard.balanceFilter.usdToCnyRate') }}</p>
                <p class="mt-0.5 text-xs leading-5 text-muted-foreground">{{ t('admin.dashboard.balanceFilter.usdToCnyRateHelp') }}</p>
              </div>
              <Input
                v-model="usdToCnyRate"
                type="number"
                inputmode="decimal"
                min="0"
                step="0.01"
                :placeholder="String(DEFAULT_USD_TO_CNY_RATE)"
                :aria-label="t('admin.dashboard.balanceFilter.usdToCnyRate')"
                class="font-mono"
              />
            </div>

            <!-- Exclude balance values -->
            <div class="space-y-3">
              <div>
                <p class="text-sm font-medium text-foreground">{{ t('admin.dashboard.balanceFilter.excludeBalances') }}</p>
                <p class="mt-0.5 text-xs text-muted-foreground">{{ t('admin.dashboard.balanceFilter.excludeBalancesHelp') }}</p>
              </div>

              <!-- Existing values -->
              <div v-if="excludeBalances.length > 0" class="flex flex-wrap gap-2">
                <span
                  v-for="(val, idx) in excludeBalances"
                  :key="idx"
                  class="inline-flex items-center gap-1 rounded-lg border border-border/60 bg-surface/60 px-2.5 py-1 text-sm text-foreground"
                >
                  <span class="font-mono">= {{ val }}</span>
                  <button
                    type="button"
                    class="rounded p-0.5 text-muted-foreground transition-colors hover:text-red-500"
                    @click="removeBalance(idx)"
                  >
                    <Trash2 class="h-3.5 w-3.5" />
                  </button>
                </span>
              </div>

              <!-- Add new value -->
              <div class="flex items-center gap-2">
                <Input
                  v-model="newBalanceInput"
                  type="number"
                  step="any"
                  :placeholder="t('admin.dashboard.balanceFilter.addPlaceholder')"
                  class="flex-1"
                  @keydown.enter.prevent="addBalance"
                />
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  class="shrink-0"
                  :disabled="!String(newBalanceInput).trim()"
                  @click="addBalance"
                >
                  <Plus class="h-4 w-4" />
                  {{ t('admin.dashboard.balanceFilter.add') }}
                </Button>
              </div>
            </div>

            <!-- Error -->
            <div v-if="errorKey" class="flex items-start gap-2 rounded-xl border border-warning/20 bg-warning/10 p-3 text-sm text-warning">
              <AlertCircle class="mt-0.5 h-4 w-4 shrink-0" />
              <span>{{ t(errorKey) }}</span>
            </div>

            <!-- Actions -->
            <div class="flex items-center justify-end gap-3 pt-1">
              <Button type="button" variant="secondary" @click="emit('close')">
                {{ t('admin.dashboard.balanceFilter.cancel') }}
              </Button>
              <Button type="button" :disabled="saving" @click="handleSave">
                <Loader2 v-if="saving" class="h-4 w-4 animate-spin" />
                {{ saving ? t('admin.dashboard.balanceFilter.saving') : t('admin.dashboard.balanceFilter.save') }}
              </Button>
            </div>
          </template>
        </div>
      </div>
    </div>
  </Teleport>
</template>
