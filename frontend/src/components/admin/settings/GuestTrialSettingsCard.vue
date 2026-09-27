<template>
  <div class="card" data-testid="guest-trial-settings">
    <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.guestTrial.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.guestTrial.description') }}</p>
      </div>
      <router-link to="/trial" target="_blank" class="btn btn-secondary btn-sm">{{ t('admin.guestTrial.openPage') }}</router-link>
    </div>

    <div v-if="loading" class="flex justify-center p-8"><LoadingSpinner /></div>
    <div v-else class="space-y-5 p-6">
      <div class="flex items-center justify-between">
        <div>
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.guestTrial.enabled') }}</label>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.guestTrial.enabledHint') }}</p>
        </div>
        <Toggle v-model="form.enabled" />
      </div>

      <div class="grid gap-5 border-t border-gray-100 pt-5 dark:border-dark-700 lg:grid-cols-2">
        <!-- 承担流量的密钥：可多选，每把密钥对应一个分组 / 平台 -->
        <div class="block lg:col-span-2" data-testid="guest-trial-keys">
          <span class="input-label">{{ t('admin.guestTrial.apiKey') }}</span>
          <div class="mt-1.5 flex flex-wrap gap-2">
            <span v-if="keys.length === 0" class="text-xs text-gray-400">{{ t('admin.guestTrial.apiKeyNone') }}</span>
            <label
              v-for="key in keys"
              :key="key.id"
              class="inline-flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-1.5 text-xs transition-colors"
              :class="trialKeyIds.includes(key.id)
                ? 'border-primary-400 bg-primary-50 text-primary-800 dark:border-primary-600 dark:bg-primary-900/30 dark:text-primary-200'
                : 'border-gray-200 text-gray-700 hover:border-gray-300 dark:border-dark-600 dark:text-gray-300'"
            >
              <input type="checkbox" class="rounded border-gray-300" :checked="trialKeyIds.includes(key.id)" :data-testid="`guest-trial-key-${key.id}`" @change="toggleKey(key.id)" />
              <span class="font-medium">{{ keyName(key.id) }}</span>
              <span class="text-gray-400">{{ keyScope(key) }}</span>
            </label>
          </div>
          <span class="input-hint">{{ t('admin.guestTrial.apiKeyHint') }}</span>
        </div>

        <div class="block lg:col-span-2" data-testid="guest-trial-models">
          <div class="flex flex-wrap items-baseline justify-between gap-2">
            <span class="input-label">{{ t('admin.guestTrial.models') }}</span>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.guestTrial.modelsSelected', { count: selected.length }) }}</span>
          </div>

          <!-- 已选：顺序即访客下拉顺序，第一个是默认模型 -->
          <div class="mt-1.5 flex min-h-[2.5rem] flex-wrap gap-2 rounded-lg border border-gray-200 p-2 dark:border-dark-600" data-testid="guest-trial-selected">
            <span v-if="selected.length === 0" class="self-center px-1 text-xs text-gray-400">{{ t('admin.guestTrial.modelsNoneSelected') }}</span>
            <span
              v-for="(item, index) in selected"
              :key="item.model"
              class="inline-flex items-center gap-1.5 rounded-md border py-1 pl-2.5 pr-1 text-xs"
              :class="isAvailable(item)
                ? 'border-primary-200 bg-primary-50 text-primary-800 dark:border-primary-800 dark:bg-primary-900/30 dark:text-primary-200'
                : 'border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-900/30 dark:text-amber-200'"
              :title="isAvailable(item) ? undefined : t('admin.guestTrial.modelsNotInKey')"
            >
              <span class="font-mono">{{ item.model }}</span>
              <span class="opacity-60">· {{ keyName(item.keyId) }}</span>
              <span v-if="index === 0" class="rounded bg-primary-600 px-1.5 py-px text-[10px] font-semibold text-white">{{ t('admin.guestTrial.modelsDefault') }}</span>
              <button v-else type="button" class="rounded px-1 text-[11px] opacity-70 hover:bg-black/5 hover:opacity-100 dark:hover:bg-white/10" @click="makeDefault(item.model)">
                {{ t('admin.guestTrial.modelsMakeDefault') }}
              </button>
              <button type="button" class="rounded p-0.5 opacity-60 hover:bg-black/5 hover:opacity-100 dark:hover:bg-white/10" :aria-label="t('admin.guestTrial.modelsRemove', { model: item.model })" @click="removeModel(item.model)">
                <Icon name="x" size="xs" />
              </button>
            </span>
          </div>

          <!-- 候选：按密钥分组列出各自能调用的模型 -->
          <div class="mt-2 rounded-lg border border-gray-200 dark:border-dark-600">
            <div class="flex flex-wrap items-center gap-2 border-b border-gray-100 p-2 dark:border-dark-700">
              <input v-model="modelFilter" type="search" class="input h-8 min-w-0 flex-1 py-1 text-xs" :placeholder="t('admin.guestTrial.modelsSearch')" :disabled="trialKeyIds.length === 0" />
              <button type="button" class="btn btn-secondary btn-sm" :disabled="selected.length === 0" @click="selected = []">{{ t('admin.guestTrial.modelsClear') }}</button>
            </div>
            <div class="max-h-80 overflow-y-auto p-2">
              <p v-if="trialKeyIds.length === 0" class="p-2 text-xs text-gray-400">{{ t('admin.guestTrial.modelsPickKeyFirst') }}</p>
              <section v-for="keyId in trialKeyIds" :key="keyId" class="mb-2 last:mb-0" :data-testid="`guest-trial-key-models-${keyId}`">
                <div class="flex items-center justify-between gap-2 px-2 py-1">
                  <span class="text-xs font-semibold text-gray-700 dark:text-gray-200">{{ keyName(keyId) }} <span class="font-normal text-gray-400">{{ keyScope(keyById(keyId)) }}</span></span>
                  <button type="button" class="text-[11px] text-primary-600 hover:underline disabled:opacity-40 dark:text-primary-400" :disabled="filteredModels(keyId).length === 0" @click="selectAllOfKey(keyId)">
                    {{ t('admin.guestTrial.modelsSelectAll') }}
                  </button>
                </div>
                <p v-if="modelLists[keyId]?.loading" class="px-2 py-1 text-xs text-gray-400">{{ t('admin.guestTrial.modelsLoading') }}</p>
                <p v-else-if="modelLists[keyId]?.error" class="px-2 py-1 text-xs text-amber-600 dark:text-amber-400">{{ t('admin.guestTrial.modelsLoadFailed') }}</p>
                <p v-else-if="!modelLists[keyId]?.models.length" class="px-2 py-1 text-xs text-gray-400">{{ t('admin.guestTrial.modelsEmpty') }}</p>
                <p v-else-if="filteredModels(keyId).length === 0" class="px-2 py-1 text-xs text-gray-400">{{ t('admin.guestTrial.modelsNoMatch') }}</p>
                <div v-else class="grid gap-1 sm:grid-cols-2 xl:grid-cols-3">
                  <label v-for="model in filteredModels(keyId)" :key="model" class="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-gray-50 dark:hover:bg-dark-700">
                    <input type="checkbox" class="rounded border-gray-300" :checked="boundKey(model) === keyId" :data-testid="`guest-trial-model-${keyId}-${model}`" @change="toggleModel(model, keyId)" />
                    <span class="truncate font-mono text-gray-700 dark:text-gray-200" :title="model">{{ model }}</span>
                    <span v-if="boundKey(model) && boundKey(model) !== keyId" class="shrink-0 text-[10px] text-gray-400">{{ t('admin.guestTrial.modelsServedBy', { key: keyName(boundKey(model)) }) }}</span>
                  </label>
                </div>
              </section>
            </div>
            <div v-if="trialKeyIds.length > 0" class="flex flex-wrap gap-2 border-t border-gray-100 p-2 dark:border-dark-700">
              <input
                v-model="customModel"
                type="text"
                class="input h-8 min-w-0 flex-1 py-1 font-mono text-xs"
                :placeholder="t('admin.guestTrial.modelsCustomPlaceholder')"
                data-testid="guest-trial-custom-model"
                @keydown.enter.prevent="addCustomModel"
              />
              <select v-if="trialKeyIds.length > 1" v-model.number="customKeyId" class="input h-8 w-auto py-1 text-xs" data-testid="guest-trial-custom-key">
                <option v-for="keyId in trialKeyIds" :key="keyId" :value="keyId">{{ keyName(keyId) }}</option>
              </select>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!customModel.trim()" @click="addCustomModel">
                <Icon name="plus" size="xs" />{{ t('admin.guestTrial.modelsAdd') }}
              </button>
            </div>
          </div>
          <span class="input-hint">{{ t('admin.guestTrial.modelsHint') }}</span>
        </div>

        <label class="block">
          <span class="input-label">{{ t('admin.guestTrial.dailyPerVisitor') }}</span>
          <input v-model.number="form.daily_per_visitor" type="number" min="1" class="input w-full" />
          <span class="input-hint">{{ t('admin.guestTrial.dailyPerVisitorHint', { multiplier: 3 }) }}</span>
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.guestTrial.dailyGlobal') }}</span>
          <input v-model.number="form.daily_global" type="number" min="1" class="input w-full" />
          <span class="input-hint">{{ t('admin.guestTrial.dailyGlobalHint') }}</span>
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.guestTrial.maxInputChars') }}</span>
          <input v-model.number="form.max_input_chars" type="number" min="1" class="input w-full" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.guestTrial.maxOutputTokens') }}</span>
          <input v-model.number="form.max_output_tokens" type="number" min="1" class="input w-full" />
        </label>
      </div>

      <div class="flex items-center justify-between border-t border-gray-100 pt-5 dark:border-dark-700">
        <div>
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.guestTrial.requireCaptcha') }}</label>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.guestTrial.requireCaptchaHint') }}</p>
        </div>
        <Toggle v-model="form.require_captcha" />
      </div>

      <div class="flex items-center justify-end gap-3">
        <span v-if="errorMessage" class="text-sm text-red-500" role="alert">{{ errorMessage }}</span>
        <button type="button" class="btn btn-primary" :disabled="saving" data-testid="guest-trial-save" @click="save">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { getGuestTrialConfig, updateGuestTrialConfig, type GuestTrialConfig } from '@/api/admin/settings'
import { keysAPI } from '@/api/keys'
import { fetchPlaygroundModels } from '@/features/playground/api'
import { useAppStore } from '@/stores'
import type { ApiKey } from '@/types'

// 与后端 guestTrialMaxModels 一致，超出部分保存时会被截掉
const MAX_TRIAL_MODELS = 20

interface TrialModel {
  model: string
  keyId: number
}

interface KeyModelList {
  loading: boolean
  error: boolean
  models: string[]
}

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const errorMessage = ref('')
const keys = ref<ApiKey[]>([])
// 一把密钥只对应一个分组；跨平台试用 = 多把密钥，每个模型绑定到能承担它的那把
const trialKeyIds = ref<number[]>([])
const selected = ref<TrialModel[]>([])
const modelLists = reactive<Record<number, KeyModelList>>({})
const modelFilter = ref('')
const customModel = ref('')
const customKeyId = ref(0)
const controllers = new Map<number, AbortController>()
const form = reactive<GuestTrialConfig>({
  enabled: false,
  api_key_id: 0,
  models: [],
  model_keys: {},
  daily_per_visitor: 20,
  daily_global: 1000,
  max_input_chars: 6000,
  max_output_tokens: 1024,
  require_captcha: true,
})

const sameModel = (a: string, b: string) => a.toLowerCase() === b.toLowerCase()
const keyById = (keyId: number) => keys.value.find((key) => key.id === keyId)
const keyName = (keyId: number) => keyById(keyId)?.name || `#${keyId}`

function keyScope(key: ApiKey | undefined): string {
  if (!key) return ''
  if (key.auto_group) return t('admin.guestTrial.apiKeyAutoGroup')
  return key.group?.name || (key.group_id ? `#${key.group_id}` : '')
}

const boundKey = (model: string) => selected.value.find((item) => sameModel(item.model, model))?.keyId || 0

// 自动分组密钥按模型挑分组，列表只反映当前解析到的分组，不能据此判定不可用；
// 列表没读到或读取失败时也不标黄，避免误报
function isAvailable(item: TrialModel): boolean {
  const list = modelLists[item.keyId]
  if (keyById(item.keyId)?.auto_group || !list || list.loading || list.error || list.models.length === 0) return true
  return list.models.some((model) => sameModel(model, item.model))
}

function filteredModels(keyId: number): string[] {
  const models = modelLists[keyId]?.models || []
  const keyword = modelFilter.value.trim().toLowerCase()
  return keyword ? models.filter((model) => model.toLowerCase().includes(keyword)) : models
}

function setModel(model: string, keyId: number) {
  const name = model.trim()
  if (!name || !keyId) return
  const index = selected.value.findIndex((item) => sameModel(item.model, name))
  if (index >= 0) {
    // 已选的模型改由另一把密钥承担，保持原位置
    selected.value = selected.value.map((item, i) => (i === index ? { ...item, keyId } : item))
    return
  }
  if (selected.value.length >= MAX_TRIAL_MODELS) return
  selected.value = [...selected.value, { model: name, keyId }]
}

function removeModel(model: string) {
  selected.value = selected.value.filter((item) => !sameModel(item.model, model))
}

function toggleModel(model: string, keyId: number) {
  if (boundKey(model) === keyId) removeModel(model)
  else setModel(model, keyId)
}

function makeDefault(model: string) {
  const item = selected.value.find((entry) => sameModel(entry.model, model))
  if (item) selected.value = [item, ...selected.value.filter((entry) => entry !== item)]
}

function selectAllOfKey(keyId: number) {
  // 已由其他密钥承担的模型不抢过来
  filteredModels(keyId).filter((model) => !boundKey(model)).forEach((model) => setModel(model, keyId))
}

function addCustomModel() {
  setModel(customModel.value, trialKeyIds.value.includes(customKeyId.value) ? customKeyId.value : trialKeyIds.value[0])
  customModel.value = ''
}

// 候选模型取自该密钥的 /playground/models：和访客实际请求走的是同一把密钥、同一个分组
async function loadKeyModels(keyId: number) {
  controllers.get(keyId)?.abort()
  const controller = new AbortController()
  controllers.set(keyId, controller)
  modelLists[keyId] = { loading: true, error: false, models: [] }
  try {
    const models = await fetchPlaygroundModels(keyId, controller.signal)
    modelLists[keyId] = { loading: false, error: false, models: [...new Set(models.map((model) => model.id).filter(Boolean))].sort((a, b) => a.localeCompare(b)) }
  } catch {
    if (!controller.signal.aborted) modelLists[keyId] = { loading: false, error: true, models: [] }
  } finally {
    if (controllers.get(keyId) === controller) controllers.delete(keyId)
  }
}

function toggleKey(keyId: number) {
  if (trialKeyIds.value.includes(keyId)) {
    trialKeyIds.value = trialKeyIds.value.filter((id) => id !== keyId)
    // 去掉密钥后，由它承担的模型也一并移除（否则没有密钥可用）
    selected.value = selected.value.filter((item) => item.keyId !== keyId)
    controllers.get(keyId)?.abort()
    delete modelLists[keyId]
  } else {
    trialKeyIds.value = [...trialKeyIds.value, keyId]
    void loadKeyModels(keyId)
  }
  if (!trialKeyIds.value.includes(customKeyId.value)) customKeyId.value = trialKeyIds.value[0] || 0
}

function apply(config: GuestTrialConfig) {
  Object.assign(form, config)
  const mapping = Object.entries(config.model_keys || {})
  const keyFor = (model: string) => mapping.find(([name]) => sameModel(name, model))?.[1] || config.api_key_id
  selected.value = (config.models || []).map((model) => ({ model, keyId: keyFor(model) })).filter((item) => item.keyId > 0)
  const keyIds = [config.api_key_id, ...selected.value.map((item) => item.keyId)].filter((id) => id > 0)
  trialKeyIds.value = [...new Set(keyIds)]
  customKeyId.value = trialKeyIds.value[0] || 0
  trialKeyIds.value.filter((keyId) => !modelLists[keyId]).forEach((keyId) => void loadKeyModels(keyId))
}

async function load() {
  loading.value = true
  try {
    const [config, keyPage] = await Promise.all([
      getGuestTrialConfig(),
      // 试用流量记在管理员自己的密钥上，这里只列当前管理员的密钥
      keysAPI.list(1, 100).catch(() => ({ items: [] as ApiKey[] })),
    ])
    keys.value = keyPage.items || []
    apply(config)
  } catch {
    errorMessage.value = t('admin.guestTrial.loadFailed')
  } finally {
    loading.value = false
  }
}

async function save() {
  errorMessage.value = ''
  saving.value = true
  try {
    const saved = await updateGuestTrialConfig({
      ...form,
      // 默认密钥跟随默认模型；每个模型的实际密钥写进 model_keys
      api_key_id: selected.value[0]?.keyId || trialKeyIds.value[0] || 0,
      models: selected.value.map((item) => item.model),
      model_keys: Object.fromEntries(selected.value.map((item) => [item.model, item.keyId])),
    })
    apply(saved)
    appStore.showSuccess(t('admin.guestTrial.saved'))
  } catch (error) {
    errorMessage.value = (error as { message?: string })?.message || t('admin.guestTrial.saveFailed')
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void load()
})

onBeforeUnmount(() => controllers.forEach((controller) => controller.abort()))
</script>
