<template>
  <BaseDialog :show="show" :title="t('accountContributions.testModalTitle')" width="narrow" @close="handleClose">
    <div class="space-y-4">
      <div v-if="account" class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ account.name }}</div>

      <div class="space-y-1.5">
        <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
          {{ t('accountContributions.selectTestModel') }}
        </label>
        <Select
          v-model="selectedModelId"
          :options="models"
          :disabled="loadingModels || testing"
          value-key="id"
          label-key="display_name"
          :placeholder="loadingModels ? t('common.loading') + '...' : t('accountContributions.selectTestModel')"
        />
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.selectTestModelHint') }}</p>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button
          @click="handleClose"
          class="rounded-lg bg-gray-100 px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          @click="confirmTest"
          :disabled="testing || !selectedModelId"
          class="flex items-center gap-2 rounded-lg bg-primary-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-600 disabled:cursor-not-allowed disabled:bg-primary-400"
        >
          <Icon v-if="testing" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
          <Icon v-else name="play" size="sm" :stroke-width="2" />
          <span>{{ testing ? t('accountContributions.testing') : t('accountContributions.startTest') }}</span>
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { Icon } from '@/components/icons'
import accountContributionsAPI, { type ContributionModelOption } from '@/api/accountContributions'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const props = defineProps<{
  show: boolean
  account: { id: number; name: string } | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'tested'): void
}>()

const models = ref<ContributionModelOption[]>([])
const selectedModelId = ref('')
const loadingModels = ref(false)
const testing = ref(false)

watch(
  () => props.show,
  async (newVal) => {
    if (newVal && props.account) {
      selectedModelId.value = ''
      await loadModels()
    }
  }
)

async function loadModels() {
  if (!props.account) return
  loadingModels.value = true
  try {
    models.value = await accountContributionsAPI.getModels(props.account.id)
    if (models.value.length > 0) selectedModelId.value = models.value[0].id
  } catch (error) {
    models.value = []
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.loadModelsFailed')))
  } finally {
    loadingModels.value = false
  }
}

function handleClose() {
  if (testing.value) return
  emit('close')
}

async function confirmTest() {
  if (!props.account || !selectedModelId.value) return
  testing.value = true
  try {
    const result = await accountContributionsAPI.test(props.account.id, selectedModelId.value)
    if (result.status === 'success') appStore.showSuccess(t('accountContributions.testSuccess'))
    else appStore.showError(result.error_message || t('accountContributions.testFailed'))
    emit('tested')
    emit('close')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.testFailed')))
  } finally {
    testing.value = false
  }
}
</script>
