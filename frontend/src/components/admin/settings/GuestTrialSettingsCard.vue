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
        <label class="block">
          <span class="input-label">{{ t('admin.guestTrial.apiKey') }}</span>
          <select v-model.number="form.api_key_id" class="input w-full" data-testid="guest-trial-key">
            <option :value="0">{{ t('admin.guestTrial.apiKeyPlaceholder') }}</option>
            <option v-for="key in keys" :key="key.id" :value="key.id">{{ key.name || `#${key.id}` }} (#{{ key.id }})</option>
          </select>
          <span class="input-hint">{{ t('admin.guestTrial.apiKeyHint') }}</span>
        </label>
        <div class="block lg:col-span-2" data-testid="guest-trial-models">
          <div class="flex flex-wrap items-baseline justify-between gap-2">
            <span class="input-label">{{ t('admin.guestTrial.models') }}</span>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.guestTrial.modelsSelected', { count: selectedModels.length }) }}</span>
          </div>

          <!-- 已选：顺序即访客下拉顺序，第一个是默认模型 -->
          <div class="mt-1.5 flex min-h-[2.5rem] flex-wrap gap-2 rounded-lg border border-gray-200 p-2 dark:border-dark-600" data-testid="guest-trial-selected">
            <span v-if="selectedModels.length === 0" class="self-center px-1 text-xs text-gray-400">{{ t('admin.guestTrial.modelsNoneSelected') }}</span>
            <span
              v-for="(model, index) in selectedModels"
              :key="model"
              class="inline-flex items-center gap-1.5 rounded-md border py-1 pl-2.5 pr-1 text-xs"
              :class="isAvailable(model)
                ? 'border-primary-200 bg-primary-50 text-primary-800 dark:border-primary-800 dark:bg-primary-900/30 dark:text-primary-200'
                : 'border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-900/30 dark:text-amber-200'"
              :title="isAvailable(model) ? undefined : t('admin.guestTrial.modelsNotInKey')"
            >
              <span class="font-mono">{{ model }}</span>
              <span v-if="index === 0" class="rounded bg-primary-600 px-1.5 py-px text-[10px] font-semibold text-white">{{ t('admin.guestTrial.modelsDefault') }}</span>
              <button v-else type="button" class="rounded px-1 text-[11px] opacity-70 hover:bg-black/5 hover:opacity-100 dark:hover:bg-white/10" @click="makeDefault(model)">
                {{ t('admin.guestTrial.modelsMakeDefault') }}
              </button>
              <button type="button" class="rounded p-0.5 opacity-60 hover:bg-black/5 hover:opacity-100 dark:hover:bg-white/10" :aria-label="t('admin.guestTrial.modelsRemove', { model })" @click="removeModel(model)">
                <Icon name="x" size="xs" />
              </button>
            </span>
          </div>

          <!-- 候选：所选密钥当前能调用的模型 -->
          <div class="mt-2 rounded-lg border border-gray-200 dark:border-dark-600">
            <div class="flex flex-wrap items-center gap-2 border-b border-gray-100 p-2 dark:border-dark-700">
              <input v-model="modelFilter" type="search" class="input h-8 min-w-0 flex-1 py-1 text-xs" :placeholder="t('admin.guestTrial.modelsSearch')" :disabled="availableModels.length === 0" />
              <button type="button" class="btn btn-secondary btn-sm" :disabled="filteredAvailable.length === 0" @click="selectAllVisible">{{ t('admin.guestTrial.modelsSelectAll') }}</button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="selectedModels.length === 0" @click="selectedModels = []">{{ t('admin.guestTrial.modelsClear') }}</button>
            </div>
            <div class="max-h-56 overflow-y-auto p-2">
              <p v-if="!form.api_key_id" class="p-2 text-xs text-gray-400">{{ t('admin.guestTrial.modelsPickKeyFirst') }}</p>
              <p v-else-if="modelsLoading" class="p-2 text-xs text-gray-400">{{ t('admin.guestTrial.modelsLoading') }}</p>
              <p v-else-if="modelsError" class="p-2 text-xs text-amber-600 dark:text-amber-400">{{ t('admin.guestTrial.modelsLoadFailed') }}</p>
              <p v-else-if="availableModels.length === 0" class="p-2 text-xs text-gray-400">{{ t('admin.guestTrial.modelsEmpty') }}</p>
              <p v-else-if="filteredAvailable.length === 0" class="p-2 text-xs text-gray-400">{{ t('admin.guestTrial.modelsNoMatch') }}</p>
              <div v-else class="grid gap-1 sm:grid-cols-2 xl:grid-cols-3">
                <label v-for="model in filteredAvailable" :key="model" class="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-gray-50 dark:hover:bg-dark-700">
                  <input type="checkbox" class="rounded border-gray-300" :checked="isSelected(model)" :data-testid="`guest-trial-model-${model}`" @change="toggleModel(model)" />
                  <span class="truncate font-mono text-gray-700 dark:text-gray-200" :title="model">{{ model }}</span>
                </label>
              </div>
            </div>
            <div class="flex gap-2 border-t border-gray-100 p-2 dark:border-dark-700">
              <input
                v-model="customModel"
                type="text"
                class="input h-8 min-w-0 flex-1 py-1 font-mono text-xs"
                :placeholder="t('admin.guestTrial.modelsCustomPlaceholder')"
                data-testid="guest-trial-custom-model"
                @keydown.enter.prevent="addCustomModel"
              />
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
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
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

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const errorMessage = ref('')
const keys = ref<ApiKey[]>([])
const selectedModels = ref<string[]>([])
const availableModels = ref<string[]>([])
const modelsLoading = ref(false)
const modelsError = ref(false)
const modelFilter = ref('')
const customModel = ref('')
let modelsController: AbortController | null = null
const form = reactive<GuestTrialConfig>({
  enabled: false,
  api_key_id: 0,
  models: [],
  daily_per_visitor: 20,
  daily_global: 1000,
  max_input_chars: 6000,
  max_output_tokens: 1024,
  require_captcha: true,
})

const sameModel = (a: string, b: string) => a.toLowerCase() === b.toLowerCase()
const isSelected = (model: string) => selectedModels.value.some((item) => sameModel(item, model))
// 候选列表还没读到或读取失败时不标黄，避免误报
const isAvailable = (model: string) =>
  modelsLoading.value || modelsError.value || availableModels.value.length === 0 || availableModels.value.some((item) => sameModel(item, model))

const filteredAvailable = computed(() => {
  const keyword = modelFilter.value.trim().toLowerCase()
  return keyword ? availableModels.value.filter((model) => model.toLowerCase().includes(keyword)) : availableModels.value
})

function addModel(model: string) {
  const name = model.trim()
  if (!name || isSelected(name) || selectedModels.value.length >= MAX_TRIAL_MODELS) return
  selectedModels.value = [...selectedModels.value, name]
}

function removeModel(model: string) {
  selectedModels.value = selectedModels.value.filter((item) => !sameModel(item, model))
}

function toggleModel(model: string) {
  if (isSelected(model)) removeModel(model)
  else addModel(model)
}

function makeDefault(model: string) {
  selectedModels.value = [model, ...selectedModels.value.filter((item) => !sameModel(item, model))]
}

function selectAllVisible() {
  filteredAvailable.value.forEach(addModel)
}

function addCustomModel() {
  addModel(customModel.value)
  customModel.value = ''
}

// 候选模型取自所选密钥的 /playground/models：和访客实际请求走的是同一把密钥、同一个分组
async function loadAvailableModels(keyId: number) {
  modelsController?.abort()
  availableModels.value = []
  modelsError.value = false
  if (!keyId) {
    modelsLoading.value = false
    return
  }
  const controller = new AbortController()
  modelsController = controller
  modelsLoading.value = true
  try {
    const models = await fetchPlaygroundModels(keyId, controller.signal)
    availableModels.value = [...new Set(models.map((model) => model.id).filter(Boolean))].sort((a, b) => a.localeCompare(b))
  } catch {
    if (!controller.signal.aborted) modelsError.value = true
  } finally {
    if (modelsController === controller) {
      modelsLoading.value = false
      modelsController = null
    }
  }
}

watch(() => form.api_key_id, (keyId) => void loadAvailableModels(keyId))

function apply(config: GuestTrialConfig) {
  Object.assign(form, config)
  selectedModels.value = [...(config.models || [])]
}

async function load() {
  loading.value = true
  try {
    const [config, keyPage] = await Promise.all([
      getGuestTrialConfig(),
      // 试用流量记在管理员自己的一把密钥上，这里只列当前管理员的密钥
      keysAPI.list(1, 100).catch(() => ({ items: [] as ApiKey[] })),
    ])
    apply(config)
    keys.value = keyPage.items || []
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
    const saved = await updateGuestTrialConfig({ ...form, models: [...selectedModels.value] })
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

onBeforeUnmount(() => modelsController?.abort())
</script>
