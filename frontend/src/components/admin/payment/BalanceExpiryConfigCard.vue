<template>
  <div class="card p-4">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div class="min-w-0 flex-1">
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('balanceExpiry.admin.title') }}</h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('balanceExpiry.admin.description') }}</p>
        <p v-if="loaded && !enabled" class="mt-1 text-xs text-gray-400 dark:text-gray-500">{{ t('balanceExpiry.admin.offHint') }}</p>
      </div>

      <div class="flex flex-wrap items-center gap-4">
        <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
          <button
            type="button"
            role="switch"
            :aria-checked="enabled"
            :disabled="!loaded || saving"
            :class="[
              'relative inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors duration-200 disabled:opacity-50',
              enabled ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600'
            ]"
            @click="enabled = !enabled"
          >
            <span
              :class="[
                'pointer-events-none inline-block h-4 w-4 rounded-full bg-white shadow-sm transition-transform duration-200',
                enabled ? 'translate-x-4' : 'translate-x-0'
              ]"
            />
          </button>
          {{ t('balanceExpiry.admin.enabled') }}
        </label>

        <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
          {{ t('balanceExpiry.admin.days') }}
          <input
            v-model.number="days"
            type="number"
            min="1"
            max="3650"
            step="1"
            :disabled="!loaded || saving"
            class="input w-24"
          />
        </label>

        <button type="button" class="btn btn-primary" :disabled="!loaded || saving || !daysValid" @click="save">
          {{ t('balanceExpiry.admin.save') }}
        </button>
      </div>
    </div>
    <p class="mt-2 text-xs text-gray-400 dark:text-gray-500">{{ t('balanceExpiry.admin.daysHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const enabled = ref(false)
const days = ref(30)
const loaded = ref(false)
const saving = ref(false)

const daysValid = computed(() => Number.isInteger(days.value) && days.value >= 1 && days.value <= 3650)

async function load() {
  try {
    const { data: cfg } = await adminPaymentAPI.getBalanceExpiryConfig()
    enabled.value = cfg.enabled
    days.value = cfg.days
    loaded.value = true
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'payment.errors', t('balanceExpiry.admin.saveFailed')))
  }
}

async function save() {
  if (!daysValid.value) return
  saving.value = true
  try {
    const { data: cfg } = await adminPaymentAPI.updateBalanceExpiryConfig({ enabled: enabled.value, days: days.value })
    enabled.value = cfg.enabled
    days.value = cfg.days
    appStore.showSuccess(t('balanceExpiry.admin.saved'))
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'payment.errors', t('balanceExpiry.admin.saveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
