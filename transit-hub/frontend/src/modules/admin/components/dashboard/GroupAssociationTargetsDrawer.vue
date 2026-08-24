<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, Link2, Loader2, Search, Server, X } from 'lucide-vue-next'
import type { MySiteCostAccount, MySiteGroupRef, MySiteUpstreamTargetOption } from '../../types/mySites'

const props = defineProps<{
  open: boolean
  ownGroup: string
  options: MySiteUpstreamTargetOption[]
  selected: MySiteGroupRef[]
  saving?: boolean
  /** 可绑定的本方 Sub2API 账号，用于按真实成本倍率核算毛利 */
  costAccounts?: MySiteCostAccount[]
  /** siteId -> 站点 base_url，仅用于把同域名账号排在候选前面 */
  siteBaseUrls?: Record<string, string>
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'save', targets: MySiteGroupRef[]): void
}>()

const { t } = useI18n()
const search = ref('')
const selectedKeys = ref<string[]>([])
const prefix = 'admin.groupAssociations.targetsDrawer'
const targetKey = (target: MySiteGroupRef): string => `${target.siteId}\u0000${target.groupName}`

/** key -> 绑定的 Sub2API 账号 ID；空串表示明确不绑定 */
const bindings = ref<Record<string, string>>({})

watch(() => props.open, (open) => {
  if (!open) return
  search.value = ''
  selectedKeys.value = props.selected.map(targetKey)
  const next: Record<string, string> = {}
  for (const target of props.selected) {
    next[targetKey(target)] = (target.sub2apiAccountId ?? '').trim()
  }
  bindings.value = next
})

/**
 * 归一化上游地址为可比较的主机名：去协议、端口、路径（base_url 常带 /v1）、
 * www. 前缀。与后端 normalizeUpstreamHost 保持同一套规则。
 */
const normalizeHost = (raw: string | undefined | null): string => {
  const value = (raw ?? '').trim()
  if (!value) return ''
  try {
    const parsed = new URL(value.includes('://') ? value : `https://${value}`)
    return parsed.hostname.toLowerCase().replace(/\.$/, '').replace(/^www\./, '')
  } catch {
    return ''
  }
}

/**
 * 给某个上游数据源列出账号候选：同域名的排在前面并标记为推荐，其余仍可选。
 * 不做自动绑定——同一域名下常挂着多个成本迥异的账号（tntapi.com 有 3 个，
 * 倍率 0.16 / 0.079 / 无），域名不足以判定该用哪个，必须由人确认。
 */
const accountCandidates = (option: MySiteUpstreamTargetOption) => {
  const accounts = props.costAccounts ?? []
  const siteHost = normalizeHost(props.siteBaseUrls?.[option.siteId])
  const matched: MySiteCostAccount[] = []
  const others: MySiteCostAccount[] = []
  for (const account of accounts) {
    if (siteHost && normalizeHost(account.baseUrl) === siteHost) matched.push(account)
    else others.push(account)
  }
  return { matched, others }
}

const bindingFor = (option: MySiteUpstreamTargetOption): string => bindings.value[targetKey(option)] ?? ''

const setBinding = (option: MySiteUpstreamTargetOption, accountId: string) => {
  bindings.value = { ...bindings.value, [targetKey(option)]: accountId }
}

const accountLabel = (account: MySiteCostAccount): string => {
  const rate = account.costRateMultiplier
  const cost = rate == null || !Number.isFinite(rate)
    ? t(`${prefix}.costBinding.noCost`)
    : t(`${prefix}.costBinding.costValue`, { value: Number(rate.toFixed(4)).toString() })
  return `${account.name} · ${cost}`
}

const filteredOptions = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  if (!query) return props.options
  return props.options.filter(option => (
    option.siteName.toLocaleLowerCase().includes(query) ||
    option.groupName.toLocaleLowerCase().includes(query) ||
    option.platform.toLocaleLowerCase().includes(query)
  ))
})

const isSelected = (option: MySiteGroupRef): boolean => selectedKeys.value.includes(targetKey(option))

const toggle = (option: MySiteUpstreamTargetOption) => {
  const key = targetKey(option)
  const index = selectedKeys.value.indexOf(key)
  if (index >= 0) selectedKeys.value.splice(index, 1)
  else selectedKeys.value.push(key)
}

const submit = () => {
  const selected = new Set(selectedKeys.value)
  emit('save', props.options
    .filter(option => selected.has(targetKey(option)))
    .map(option => {
      const accountId = bindingFor(option)
      // 空串不写进去：未绑定就该是字段缺失，与"绑定了空账号"区分开。
      return accountId
        ? { siteId: option.siteId, groupName: option.groupName, sub2apiAccountId: accountId }
        : { siteId: option.siteId, groupName: option.groupName }
    }))
}

const formatMultiplier = (value: number | null): string => {
  if (value == null || !Number.isFinite(value)) return t(`${prefix}.unknownMultiplier`)
  return t(`${prefix}.multiplier`, { value: Number(value.toFixed(4)).toString() })
}

const formatOptionMultiplier = (option: MySiteUpstreamTargetOption): string => {
  if (option.multiplierMode === 'auto') return t(`${prefix}.autoMultiplier`)
  return formatMultiplier(option.multiplier)
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
      <div v-if="open" class="fixed inset-0 z-[160]">
        <div class="absolute inset-0 bg-background/70 backdrop-blur-sm" @click="saving ? undefined : emit('close')" />
        <aside
          role="dialog"
          aria-modal="true"
          :aria-label="t(`${prefix}.titleWithGroup`, { group: ownGroup })"
          class="absolute inset-y-0 right-0 flex w-full max-w-xl flex-col border-l border-border/60 bg-card shadow-2xl"
        >
          <header class="flex items-start justify-between gap-4 border-b border-border/60 px-5 py-4">
            <div class="min-w-0">
              <div class="flex items-center gap-2 text-foreground">
                <Link2 class="h-4 w-4 text-primary" />
                <h2 class="truncate text-sm font-semibold">{{ t(`${prefix}.titleWithGroup`, { group: ownGroup }) }}</h2>
              </div>
              <p class="mt-1 text-xs text-muted-foreground">{{ t(`${prefix}.selectedCount`, { count: selectedKeys.length }) }}</p>
            </div>
            <button
              type="button"
              class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-surface-elevated hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
              :aria-label="t(`${prefix}.close`)"
              :disabled="saving"
              @click="emit('close')"
            >
              <X class="h-4 w-4" />
            </button>
          </header>

          <div class="border-b border-border/50 px-5 py-4">
            <div class="relative">
              <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <input
                v-model="search"
                type="search"
                :placeholder="t(`${prefix}.searchPlaceholder`)"
                :aria-label="t(`${prefix}.searchLabel`)"
                class="h-10 w-full rounded-lg border border-border/60 bg-background pl-9 pr-3 text-sm text-foreground outline-none placeholder:text-muted-foreground focus-visible:border-primary focus-visible:ring-2 focus-visible:ring-primary/25"
              >
            </div>
          </div>

          <div class="min-h-0 flex-1 overflow-y-auto px-3 py-3">
            <div v-if="filteredOptions.length === 0" class="flex min-h-56 flex-col items-center justify-center px-6 text-center">
              <Server class="h-8 w-8 text-muted-foreground/50" />
              <p class="mt-3 text-sm font-medium text-foreground">{{ t(`${prefix}.emptyTitle`) }}</p>
              <p class="mt-1 text-xs text-muted-foreground">{{ t(`${prefix}.emptyDescription`) }}</p>
            </div>

            <label
              v-for="option in filteredOptions"
              v-else
              :key="targetKey(option)"
              class="mb-1 flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-3 transition-colors last:mb-0"
              :class="isSelected(option) ? 'border-primary/35 bg-primary/[0.06]' : 'border-transparent hover:border-border/60 hover:bg-surface/45'"
            >
              <input
                type="checkbox"
                class="sr-only"
                :checked="isSelected(option)"
                @change="toggle(option)"
              >
              <span
                class="flex h-5 w-5 shrink-0 items-center justify-center rounded border"
                :class="isSelected(option) ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-background'"
              >
                <Check v-if="isSelected(option)" class="h-3.5 w-3.5" />
              </span>
              <span class="min-w-0 flex-1">
                <span class="flex flex-wrap items-center gap-2">
                  <span class="truncate text-sm font-medium text-foreground">{{ option.groupName }}</span>
                  <span v-if="option.stale" class="rounded border border-warning/30 bg-warning/10 px-1.5 py-0.5 text-[10px] font-medium text-warning">
                    {{ t(`${prefix}.stale`) }}
                  </span>
                </span>
                <span class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                  <span>{{ option.siteName }}</span>
                  <span v-if="option.platform" class="rounded bg-surface-elevated px-1.5 py-0.5">{{ option.platform }}</span>
                </span>
                <!-- 成本账号绑定：只有选中的数据源才需要，未绑定则不参与毛利计算 -->
                <span v-if="isSelected(option)" class="mt-2 flex flex-wrap items-center gap-2">
                  <select
                    class="max-w-full rounded border border-border bg-background px-2 py-1 text-xs text-foreground"
                    :value="bindingFor(option)"
                    @click.stop
                    @change="setBinding(option, ($event.target as HTMLSelectElement).value)"
                  >
                    <option value="">{{ t(`${prefix}.costBinding.unbound`) }}</option>
                    <optgroup
                      v-if="accountCandidates(option).matched.length"
                      :label="t(`${prefix}.costBinding.sameHost`)"
                    >
                      <option v-for="account in accountCandidates(option).matched" :key="account.id" :value="account.id">
                        {{ accountLabel(account) }}
                      </option>
                    </optgroup>
                    <optgroup
                      v-if="accountCandidates(option).others.length"
                      :label="t(`${prefix}.costBinding.otherAccounts`)"
                    >
                      <option v-for="account in accountCandidates(option).others" :key="account.id" :value="account.id">
                        {{ accountLabel(account) }}
                      </option>
                    </optgroup>
                  </select>
                  <span v-if="!bindingFor(option)" class="text-[11px] text-warning">
                    {{ t(`${prefix}.costBinding.unboundHint`) }}
                  </span>
                </span>
              </span>
              <span class="shrink-0 text-xs font-medium tabular-nums text-foreground">{{ formatOptionMultiplier(option) }}</span>
            </label>
          </div>

          <footer class="flex items-center justify-between gap-3 border-t border-border/60 bg-card px-5 py-4">
            <span class="text-xs text-muted-foreground">{{ t(`${prefix}.selectedCount`, { count: selectedKeys.length }) }}</span>
            <div class="flex items-center gap-2">
              <button
                type="button"
                class="rounded-lg border border-border/60 px-4 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-surface-elevated hover:text-foreground disabled:opacity-50"
                :disabled="saving"
                @click="emit('close')"
              >
                {{ t(`${prefix}.cancel`) }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
                :disabled="saving"
                @click="submit"
              >
                <Loader2 v-if="saving" class="h-4 w-4 animate-spin" />
                <Check v-else class="h-4 w-4" />
                {{ saving ? t(`${prefix}.saving`) : t(`${prefix}.save`) }}
              </button>
            </div>
          </footer>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>
