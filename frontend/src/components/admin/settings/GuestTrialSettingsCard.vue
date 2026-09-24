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
        <label class="block">
          <span class="input-label">{{ t('admin.guestTrial.models') }}</span>
          <input v-model="modelsText" type="text" class="input w-full" placeholder="gpt-5.4-mini, claude-haiku-4-5" data-testid="guest-trial-models" />
          <span class="input-hint">{{ t('admin.guestTrial.modelsHint') }}</span>
        </label>
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
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { getGuestTrialConfig, updateGuestTrialConfig, type GuestTrialConfig } from '@/api/admin/settings'
import { keysAPI } from '@/api/keys'
import { useAppStore } from '@/stores'
import type { ApiKey } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const errorMessage = ref('')
const keys = ref<ApiKey[]>([])
const modelsText = ref('')
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

function apply(config: GuestTrialConfig) {
  Object.assign(form, config)
  modelsText.value = (config.models || []).join(', ')
}

const parseModels = (text: string) => text.split(/[,，\n]/).map((item) => item.trim()).filter(Boolean)

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
    const saved = await updateGuestTrialConfig({ ...form, models: parseModels(modelsText.value) })
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
</script>
