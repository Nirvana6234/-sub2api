<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Loader2, Trash2, X } from 'lucide-vue-next'

export type ManualUpstreamKeyMultiplierTarget = {
  targetId: string
  accountName: string
  multiplier?: number
  isManual: boolean
}

const props = defineProps<{
  open: boolean
  target: ManualUpstreamKeyMultiplierTarget | null
  saving: boolean
  error: string
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'save', multiplier: number): void
  (event: 'clear'): void
}>()

const { t } = useI18n()
const prefix = 'admin.connectionHealth.manualMultiplierDialog'
const input = ref('')

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) input.value = props.target?.multiplier == null ? '' : String(props.target.multiplier)
  },
)

const inputText = computed(() => String(input.value ?? '').trim())
const multiplier = computed(() => {
  const value = Number(inputText.value)
  return inputText.value !== '' && Number.isFinite(value) && value >= 0 && value <= 1_000_000 ? value : null
})
const canSave = computed(() => !props.saving && multiplier.value != null)

const save = () => {
  if (multiplier.value == null) return
  emit('save', multiplier.value)
}
</script>

<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="opacity-0"
      enter-to-class="opacity-100"
      leave-active-class="transition duration-150 ease-in"
      leave-from-class="opacity-100"
      leave-to-class="opacity-0"
    >
      <div v-if="open" class="fixed inset-0 z-[150] flex items-center justify-center p-4">
        <div class="absolute inset-0 bg-background/60 backdrop-blur-sm" />
        <div role="dialog" aria-modal="true" :aria-label="t(`${prefix}.title`)" class="relative w-full max-w-md overflow-hidden rounded-lg border border-border/60 bg-card shadow-2xl" @pointerdown.stop @click.stop>
          <div class="flex items-center justify-between gap-3 border-b border-border/60 px-5 py-4">
            <div class="min-w-0">
              <h3 class="text-sm font-semibold text-foreground">{{ t(`${prefix}.title`) }}</h3>
              <p class="mt-0.5 truncate text-xs text-muted-foreground">{{ target?.accountName }}</p>
            </div>
            <button type="button" class="shrink-0 rounded-md p-1 text-muted-foreground transition-colors hover:bg-surface-elevated hover:text-foreground disabled:opacity-50" :disabled="saving" :aria-label="t(`${prefix}.close`)" @click="emit('close')">
              <X class="h-4 w-4" />
            </button>
          </div>

          <div class="space-y-3 px-5 py-4">
            <label class="block text-sm font-medium text-foreground" for="manual-upstream-key-multiplier">{{ t(`${prefix}.multiplier`) }}</label>
            <input
              id="manual-upstream-key-multiplier"
              :value="input"
              type="number"
              min="0"
              max="1000000"
              step="0.0001"
              inputmode="decimal"
              class="h-10 w-full rounded-lg border bg-background px-3 text-sm text-foreground outline-none focus-visible:ring-2"
              :class="inputText && multiplier == null ? 'border-destructive focus-visible:ring-destructive/20' : 'border-border/60 focus-visible:border-primary focus-visible:ring-primary/20'"
              :disabled="saving"
              @input="input = ($event.target as HTMLInputElement).value"
            >
            <p v-if="inputText && multiplier == null" class="text-xs text-destructive">{{ t(`${prefix}.invalid`) }}</p>
            <p v-if="error" class="text-xs text-destructive">{{ error }}</p>
          </div>

          <div class="flex flex-col-reverse gap-2 border-t border-border/60 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
            <button
              v-if="target?.isManual"
              type="button"
              class="inline-flex items-center gap-1.5 rounded-lg px-2 py-1.5 text-sm text-destructive transition-colors hover:bg-destructive/10 disabled:opacity-50"
              :disabled="saving"
              @click="emit('clear')"
            >
              <Trash2 class="h-4 w-4" />{{ t(`${prefix}.clear`) }}
            </button>
            <span v-else />
            <div class="flex items-center gap-2">
              <button type="button" class="rounded-lg px-3 py-1.5 text-sm text-muted-foreground hover:bg-surface-line disabled:opacity-50" :disabled="saving" @click="emit('close')">
                {{ t(`${prefix}.cancel`) }}
              </button>
              <button type="button" class="inline-flex min-w-20 items-center justify-center gap-1.5 rounded-lg bg-primary px-4 py-1.5 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50" :disabled="!canSave" @click="save">
                <Loader2 v-if="saving" class="h-4 w-4 animate-spin" />{{ t(`${prefix}.${saving ? 'saving' : 'save'}`) }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
