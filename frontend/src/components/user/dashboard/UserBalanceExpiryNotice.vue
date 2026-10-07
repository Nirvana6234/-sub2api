<template>
  <div
    v-if="visible"
    :class="[
      'rounded-2xl border p-4',
      urgent
        ? 'border-amber-300 bg-amber-50 dark:border-amber-700/60 dark:bg-amber-900/20'
        : 'border-emerald-200 bg-emerald-50/60 dark:border-emerald-800/50 dark:bg-emerald-900/10'
    ]"
  >
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <p class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('balanceExpiry.title') }}
          <span v-if="urgent" class="ml-2 rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">
            {{ t('balanceExpiry.soon') }}
          </span>
        </p>

        <div class="mt-2 flex flex-wrap items-baseline gap-x-6 gap-y-1">
          <div>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('balanceExpiry.total') }}</span>
            <span class="ml-1.5 text-lg font-bold text-emerald-600 dark:text-emerald-400">${{ money(balance) }}</span>
          </div>
          <template v-if="view && view.lots.length > 0">
            <div>
              <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('balanceExpiry.expiring') }}</span>
              <span class="ml-1.5 text-sm font-semibold text-amber-600 dark:text-amber-400">${{ money(view.expiring_balance) }}</span>
            </div>
            <div>
              <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('balanceExpiry.permanent') }}</span>
              <span class="ml-1.5 text-sm font-semibold text-gray-700 dark:text-gray-200">${{ money(view.permanent_balance) }}</span>
            </div>
          </template>
        </div>

        <p v-if="first" class="mt-2 text-sm text-gray-700 dark:text-gray-200">
          {{ t('balanceExpiry.nextExpire') }}：
          <span class="font-semibold">{{ formatDateTime(first.expires_at) }}</span>
          <span :class="['ml-2 text-xs', urgent ? 'font-semibold text-amber-600 dark:text-amber-400' : 'text-gray-500 dark:text-gray-400']">
            ({{ daysLeftText(first.expires_at) }})
          </span>
        </p>
        <p v-else-if="view?.enabled" class="mt-2 text-xs text-gray-500 dark:text-gray-400">
          {{ t('balanceExpiry.noLots') }}
        </p>

        <p v-if="view?.enabled" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('balanceExpiry.policy', { days: view.days }) }}
        </p>
        <p v-if="lastExpired" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('balanceExpiry.lastExpired', { date: formatDate(lastExpired.settled_at || lastExpired.expires_at), amount: '$' + money(lastExpired.expired_amount) }) }}
        </p>
      </div>

      <button
        v-if="view && view.lots.length > 1"
        type="button"
        class="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400"
        @click="expanded = !expanded"
      >
        {{ t('balanceExpiry.lotsHeading') }} {{ expanded ? '▴' : '▾' }}
      </button>
    </div>

    <ul v-if="view && (expanded || view.lots.length === 1) && view.lots.length > 0" class="mt-3 space-y-1.5 border-t border-black/5 pt-3 dark:border-white/10">
      <li v-for="lot in view.lots" :key="lot.id" class="flex flex-wrap items-center justify-between gap-2 text-xs">
        <span class="text-gray-700 dark:text-gray-200">
          {{ t('balanceExpiry.lotLine', { amount: '$' + money(lot.remaining), date: formatDateTime(lot.expires_at) }) }}
        </span>
        <span class="text-gray-400 dark:text-gray-500">
          {{ t('balanceExpiry.credited', { date: formatDate(lot.credited_at) }) }} · {{ daysLeftText(lot.expires_at) }}
        </span>
      </li>
    </ul>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { paymentAPI } from '@/api/payment'
import type { BalanceExpiryView, BalanceLot } from '@/types/payment'

defineProps<{
  /** 用户当前总余额（与仪表盘余额卡片同一来源） */
  balance: number
}>()

const { t } = useI18n()
const view = ref<BalanceExpiryView | null>(null)
const expanded = ref(false)

// 到期前 7 天内醒目提醒
const URGENT_DAYS = 7

const first = computed<BalanceLot | null>(() => view.value?.lots[0] ?? null)
const lastExpired = computed<BalanceLot | null>(() => {
  const latest = view.value?.expired[0]
  if (!latest) return null
  // 只提醒最近 30 天内的清零，更早的不再打扰
  const at = new Date(latest.settled_at || latest.expires_at).getTime()
  return Date.now() - at <= 30 * 86400000 ? latest : null
})

// 开着有效期，或者还有限时余额/刚清零过，才显示；其余情况整块不出现。
const visible = computed(() => {
  const v = view.value
  if (!v) return false
  return v.enabled || v.lots.length > 0 || lastExpired.value !== null
})

const urgent = computed(() => {
  const lot = first.value
  return !!lot && msLeft(lot.expires_at) <= URGENT_DAYS * 86400000
})

function msLeft(iso: string): number {
  return new Date(iso).getTime() - Date.now()
}

function money(n: number): string {
  return (Number.isFinite(n) ? n : 0).toFixed(2)
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString()
}

function formatDateTime(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function daysLeftText(iso: string): string {
  const days = Math.ceil(msLeft(iso) / 86400000)
  return days <= 0 ? t('balanceExpiry.dueToday') : t('balanceExpiry.daysLeft', { days })
}

onMounted(async () => {
  try {
    view.value = (await paymentAPI.getBalanceExpiry()).data
  } catch (error) {
    // 到期提醒是附加信息，取不到就不显示，不影响首页其他内容
    console.warn('Failed to load balance expiry:', error)
  }
})
</script>
