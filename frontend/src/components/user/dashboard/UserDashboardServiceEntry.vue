<template>
  <section aria-labelledby="service-entry-title" data-testid="dashboard-service-entry">
    <div class="mb-3">
      <h2 id="service-entry-title" class="text-base font-semibold text-gray-900 dark:text-white">
        {{ t('dashboard.serviceEntry.title') }}
      </h2>
      <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
        {{ cards.length > 1 ? t('dashboard.serviceEntry.subtitle') : t('dashboard.serviceEntry.subtitleSingle') }}
      </p>
    </div>

    <div class="grid grid-cols-1 gap-4" :class="gridClass">
      <article
        v-for="card in cards"
        :key="card.id"
        :data-testid="`service-card-${card.id}`"
        class="card relative flex flex-col overflow-hidden p-5 transition-shadow duration-200 hover:shadow-card-hover"
      >
        <span class="absolute inset-x-0 top-0 h-[3px]" :class="accent[card.id].bar" aria-hidden="true"></span>

        <div class="flex-1">
          <div class="flex items-start gap-3">
            <div class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl" :class="accent[card.id].icon">
              <Icon :name="card.icon" size="lg" />
            </div>
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
                <h3 class="text-[15px] font-semibold text-gray-900 dark:text-white">{{ t(`dashboard.serviceEntry.${card.id}.title`) }}</h3>
                <span
                  v-if="card.id === 'client'"
                  class="rounded-full bg-teal-50 px-2 py-0.5 text-[11px] font-semibold text-teal-700 dark:bg-teal-900/30 dark:text-teal-300"
                >{{ t('dashboard.serviceEntry.recommended') }}</span>
              </div>
              <p class="mt-0.5 text-xs leading-relaxed text-gray-500 dark:text-dark-400">{{ t(`dashboard.serviceEntry.${card.id}.description`) }}</p>
            </div>
          </div>

          <!-- 网页工作台：直达四个工作区 -->
          <div v-if="card.id === 'web'" class="mt-4 flex flex-wrap gap-1.5">
            <router-link
              v-for="entry in webEntries"
              :key="entry.key"
              :to="entry.to"
              class="inline-flex h-7 items-center gap-1.5 rounded-lg border border-gray-200 bg-gray-50 px-2.5 text-xs text-gray-700 transition-colors hover:border-primary-300 hover:text-primary-700 dark:border-dark-700 dark:bg-dark-800/60 dark:text-dark-200 dark:hover:border-primary-700 dark:hover:text-primary-300"
            >
              <Icon :name="entry.icon" size="xs" class="text-primary-500" />
              {{ t(`playground.${entry.key}`) }}
            </router-link>
          </div>

          <!-- API 接入：接口地址 + 复制 -->
          <div
            v-else-if="card.id === 'api'"
            class="mt-4 flex h-9 items-center gap-2 rounded-lg border border-dashed border-gray-300 bg-gray-50 pl-3 pr-1.5 dark:border-dark-600 dark:bg-dark-800/60"
          >
            <span class="flex-shrink-0 text-[11px] text-gray-400 dark:text-dark-500">{{ t('dashboard.serviceEntry.api.endpoint') }}</span>
            <code class="min-w-0 flex-1 truncate font-mono text-xs text-gray-800 dark:text-dark-100" data-testid="service-api-endpoint">{{ apiEndpoint }}</code>
            <button
              type="button"
              class="inline-flex h-6 flex-shrink-0 items-center gap-1 rounded-md border border-gray-200 bg-white px-2 text-[11px] text-gray-500 transition-colors hover:border-violet-300 hover:text-violet-600 dark:border-dark-600 dark:bg-dark-900 dark:text-dark-300"
              @click="copyToClipboard(apiEndpoint)"
            >
              <Icon :name="copied ? 'check' : 'copy'" size="xs" />
              {{ copied ? t('dashboard.serviceEntry.api.copied') : t('dashboard.serviceEntry.api.copy') }}
            </button>
          </div>

          <!-- 助手客户端：平台 -->
          <div v-else class="mt-4 flex flex-wrap gap-1.5">
            <router-link
              v-for="platform in clientPlatforms"
              :key="platform"
              :to="{ path: '/download', hash: '#downloads' }"
              class="inline-flex h-7 items-center gap-1.5 rounded-lg border border-gray-200 bg-gray-50 px-2.5 text-xs text-gray-700 transition-colors hover:border-teal-300 hover:text-teal-700 dark:border-dark-700 dark:bg-dark-800/60 dark:text-dark-200 dark:hover:border-teal-700 dark:hover:text-teal-300"
            >
              <Icon name="download" size="xs" class="text-teal-500" />
              {{ t(`dashboard.serviceEntry.client.${platform}`) }}
            </router-link>
          </div>

          <!-- 助手客户端：输入助手「同步会话」里的 6 位码，跳到手机控制界面（/paw/）接着配对 -->
          <form
            v-if="card.id === 'client'"
            class="mt-3 rounded-lg border border-dashed border-teal-200 bg-teal-50/40 px-3 py-2.5 dark:border-teal-800/60 dark:bg-teal-900/10"
            data-testid="service-client-remote"
            @submit.prevent="connectRemote"
          >
            <div class="flex items-start justify-between gap-2">
              <p class="min-w-0 text-xs font-medium text-gray-800 dark:text-dark-100">{{ t('dashboard.serviceEntry.client.remote.title') }}</p>
              <a
                :href="PAW_PATH"
                class="flex-shrink-0 whitespace-nowrap text-[11px] text-teal-700 hover:underline dark:text-teal-300"
                data-testid="service-remote-open"
              >{{ t('dashboard.serviceEntry.client.remote.open') }}</a>
            </div>
            <p class="mt-0.5 text-[11px] leading-relaxed text-gray-500 dark:text-dark-400">{{ t('dashboard.serviceEntry.client.remote.hint') }}</p>
            <div class="mt-2 flex items-center gap-2">
              <input
                v-model="pairCode"
                class="input h-8 min-w-0 max-w-[10rem] flex-1 px-2 font-mono text-sm tracking-[0.3em]"
                inputmode="numeric"
                autocomplete="one-time-code"
                maxlength="7"
                :placeholder="t('dashboard.serviceEntry.client.remote.placeholder')"
                :aria-label="t('dashboard.serviceEntry.client.remote.placeholder')"
                data-testid="service-remote-code"
              />
              <button
                type="submit"
                class="inline-flex h-8 flex-shrink-0 items-center whitespace-nowrap rounded-lg bg-teal-600 px-3 text-xs font-medium text-white shadow-sm transition-colors hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50"
                :disabled="!normalizedPairCode"
                data-testid="service-remote-connect"
              >{{ t('dashboard.serviceEntry.client.remote.connect') }}</button>
            </div>
          </form>
        </div>

        <div class="mt-4 flex flex-wrap items-center justify-between gap-2">
          <p class="min-w-0 text-xs text-gray-500 dark:text-dark-400">
            <template v-if="card.id === 'web'">{{ t('dashboard.serviceEntry.web.meta') }}</template>
            <template v-else-if="card.id === 'api'">
              {{ t('dashboard.serviceEntry.api.meta', { total: stats?.total_api_keys ?? 0, active: stats?.active_api_keys ?? 0 }) }}
            </template>
            <template v-else-if="clientVersion">{{ t('dashboard.serviceEntry.client.meta', { version: clientVersion }) }}</template>
          </p>
          <div class="ml-auto flex items-center gap-1.5">
            <a
              v-if="card.id === 'api' && docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="btn btn-secondary btn-sm"
            >{{ t('dashboard.serviceEntry.api.docs') }}</a>
            <router-link
              v-if="card.id === 'client'"
              :to="{ path: '/download', hash: '#guide-step-1' }"
              class="btn btn-secondary btn-sm"
            >{{ t('dashboard.serviceEntry.client.guide') }}</router-link>
            <router-link
              :to="card.to"
              class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium text-white shadow-sm transition-colors"
              :class="accent[card.id].button"
            >
              {{ t(`dashboard.serviceEntry.${card.id}.action`) }}
              <Icon :name="card.id === 'client' ? 'download' : 'arrowRight'" size="sm" />
            </router-link>
          </div>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'
import { useClipboard } from '@/composables/useClipboard'
import { useServiceAvailability } from '@/composables/useServiceAvailability'
import { sanitizeUrl } from '@/utils/url'
import type { UserDashboardStats as UserStatsType } from '@/api/usage'

type ServiceId = 'web' | 'api' | 'client'
type IconName = InstanceType<typeof Icon>['$props']['name']

defineProps<{ stats: UserStatsType | null }>()

const { t } = useI18n()
const appStore = useAppStore()
const { copied, copyToClipboard } = useClipboard()
const { webEnabled, clientEnabled, apiEndpoint } = useServiceAvailability()

// 识别色：网页 = 品牌天空蓝，API = 紫，客户端 = 青绿。只用于顶部细线、图标底色和主按钮。
const accent: Record<ServiceId, { bar: string; icon: string; button: string }> = {
  web: {
    bar: 'bg-primary-500',
    icon: 'bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300',
    button: 'bg-primary-500 hover:bg-primary-600',
  },
  api: {
    bar: 'bg-violet-500',
    icon: 'bg-violet-50 text-violet-600 dark:bg-violet-900/30 dark:text-violet-300',
    button: 'bg-violet-500 hover:bg-violet-600',
  },
  client: {
    bar: 'bg-teal-500',
    icon: 'bg-teal-50 text-teal-600 dark:bg-teal-900/30 dark:text-teal-300',
    button: 'bg-teal-600 hover:bg-teal-700',
  },
}

// 卡片按后台开关过滤，顺序按主推程度：客户端 → API 接入 → 网页工作台。
// 不提供客户端的部署版本只剩 API + 网页两张。
const cards = computed(() => {
  const list: { id: ServiceId; icon: IconName; to: string | { path: string; hash: string } }[] = []
  if (clientEnabled.value) list.push({ id: 'client', icon: 'download', to: { path: '/download', hash: '#downloads' } })
  list.push({ id: 'api', icon: 'terminal', to: '/keys' })
  if (webEnabled.value) list.push({ id: 'web', icon: 'chat', to: '/playground/unified' })
  return list
})

const gridClass = computed(() => (cards.value.length >= 3 ? 'lg:grid-cols-3' : cards.value.length === 2 ? 'md:grid-cols-2' : ''))

const webEntries: { key: string; icon: IconName; to: string }[] = [
  { key: 'chatWorkspace', icon: 'chat', to: '/playground/chat' },
  { key: 'imageWorkspace', icon: 'sparkles', to: '/playground/images' },
  { key: 'canvasWorkspace', icon: 'grid', to: '/playground/images?view=canvas' },
  { key: 'galleryWorkspace', icon: 'inbox', to: '/playground/gallery' },
]

const clientPlatforms = computed(() => {
  const mac = /^https?:\/\//i.test((appStore.cachedPublicSettings?.client_download_direct_url_mac || '').trim())
  return mac ? ['windows', 'mac'] : ['windows']
})

// 手机控制界面（Paw）挂在 /paw/。配对码交给它：它负责生成手机签名密钥并认领，
// 等电脑上确认后直接打开同步的会话。
const PAW_PATH = '/paw/'
const pairCode = ref('')
const normalizedPairCode = computed(() => {
  const code = pairCode.value.replace(/\s/g, '')
  return /^\d{6}$/.test(code) ? code : ''
})

function connectRemote() {
  const code = normalizedPairCode.value
  if (!code) return
  window.location.assign(`${PAW_PATH}?pair=${code}`)
}

const clientVersion = computed(() => (appStore.cachedPublicSettings?.client_latest_version || '').trim())
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
</script>
