<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="hasHomeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
  </div>

  <!-- Compact Home Page -->
  <div
    v-else-if="compactHomeEnabled"
    data-testid="compact-home"
    class="flex min-h-screen flex-col bg-[#f7f8f5] text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <header class="border-b border-gray-200/80 bg-white/90 px-4 py-4 sm:px-6 dark:border-dark-800 dark:bg-dark-950/90">
      <nav class="mx-auto grid max-w-7xl grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-3 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
        <div class="flex min-w-0 items-center gap-3">
          <img :src="siteLogo || '/gongfei-plane.svg'" :alt="siteName" class="h-9 w-9 shrink-0 rounded-lg object-contain" />
          <span class="min-w-0 truncate text-base font-semibold">{{ siteName }}</span>
        </div>
        <div class="flex items-center gap-1">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="flex h-10 w-10 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <button
            type="button"
            class="flex h-10 w-10 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
          </button>
        </div>
        <div class="col-span-2 flex items-center gap-2 sm:col-span-1">
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-11 flex-1 items-center justify-center whitespace-nowrap rounded-xl px-4 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 sm:flex-none dark:text-dark-200 dark:hover:bg-dark-800 dark:hover:text-white"
          >
            {{ t('clientIntroduction.console') }}
          </router-link>
          <!-- 不提供客户端的部署版本：首页完全不出现客户端，主按钮换成网页工作台 -->
          <router-link
            v-if="clientEnabled"
            to="/download"
            class="inline-flex min-h-11 flex-1 items-center justify-center gap-2 whitespace-nowrap rounded-xl bg-[#2864dc] px-5 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-[#174bba] sm:flex-none dark:bg-[#376bdd] dark:hover:bg-[#285bc5]"
          >
            <Icon name="download" size="sm" aria-hidden="true" />
            {{ t('clientIntroduction.clientDownload') }}
          </router-link>
          <router-link
            v-else-if="webEnabled"
            :to="isAuthenticated ? '/playground' : { path: '/login', query: { redirect: '/playground' } }"
            class="inline-flex min-h-11 flex-1 items-center justify-center gap-2 whitespace-nowrap rounded-xl bg-[#2864dc] px-5 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-[#174bba] sm:flex-none dark:bg-[#376bdd] dark:hover:bg-[#285bc5]"
          >
            <Icon name="chat" size="sm" aria-hidden="true" />
            {{ t('homeIntro.ctaWeb') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="mx-auto w-full max-w-7xl flex-1 px-4 py-10 sm:px-6 sm:py-14">
      <ClientIntroduction variant="home" />
    </main>

    <footer class="min-w-0 border-t border-gray-200 px-4 py-5 text-center text-sm text-gray-500 [overflow-wrap:anywhere] sm:px-6 dark:border-dark-800 dark:text-dark-400">
      &copy; {{ currentYear }} {{ siteName }}
    </footer>
  </div>

  <!-- Default Home Page -->
  <div
    v-else
    class="flex min-h-screen flex-col bg-[#f7f8f5] text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <!-- Header -->
    <header class="border-b border-gray-200/80 bg-white/90 px-4 py-4 sm:px-6 dark:border-dark-800 dark:bg-dark-950/90">
      <nav class="mx-auto grid max-w-7xl grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-3 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
        <div class="flex min-w-0 items-center gap-3">
          <img :src="siteLogo || '/gongfei-plane.svg'" :alt="siteName" class="h-9 w-9 shrink-0 rounded-lg object-contain" />
          <span class="min-w-0 truncate text-base font-semibold">{{ siteName }}</span>
        </div>
        <div class="flex items-center gap-1">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="flex h-10 w-10 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <button
            type="button"
            class="flex h-10 w-10 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
          </button>
        </div>
        <div class="col-span-2 flex items-center gap-2 sm:col-span-1">
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-11 flex-1 items-center justify-center whitespace-nowrap rounded-xl px-4 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 sm:flex-none dark:text-dark-200 dark:hover:bg-dark-800 dark:hover:text-white"
          >
            {{ t('clientIntroduction.console') }}
          </router-link>
          <!-- 不提供客户端的部署版本：首页完全不出现客户端，主按钮换成网页工作台 -->
          <router-link
            v-if="clientEnabled"
            to="/download"
            class="inline-flex min-h-11 flex-1 items-center justify-center gap-2 whitespace-nowrap rounded-xl bg-[#2864dc] px-5 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-[#174bba] sm:flex-none dark:bg-[#376bdd] dark:hover:bg-[#285bc5]"
          >
            <Icon name="download" size="sm" aria-hidden="true" />
            {{ t('clientIntroduction.clientDownload') }}
          </router-link>
          <router-link
            v-else-if="webEnabled"
            :to="isAuthenticated ? '/playground' : { path: '/login', query: { redirect: '/playground' } }"
            class="inline-flex min-h-11 flex-1 items-center justify-center gap-2 whitespace-nowrap rounded-xl bg-[#2864dc] px-5 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-[#174bba] sm:flex-none dark:bg-[#376bdd] dark:hover:bg-[#285bc5]"
          >
            <Icon name="chat" size="sm" aria-hidden="true" />
            {{ t('homeIntro.ctaWeb') }}
          </router-link>
        </div>
      </nav>
    </header>

    <!-- Main Content -->
    <main class="mx-auto w-full max-w-7xl flex-1 px-4 py-10 sm:px-6 sm:py-14">
      <ClientIntroduction variant="home" />

      <details class="mt-12 border-t border-gray-200 pt-6 dark:border-dark-800">
        <summary class="cursor-pointer text-sm font-medium text-gray-500 hover:text-gray-900 dark:text-dark-400 dark:hover:text-white">
          {{ t('clientIntroduction.advanced') }}
        </summary>
        <div class="pt-8">
          <!-- Supported Providers -->
          <div class="mb-8 text-center">
            <h2 class="mb-3 text-2xl font-bold text-gray-900 dark:text-white">
              {{ t('home.providers.title') }}
            </h2>
            <p class="text-sm text-gray-600 dark:text-dark-400">
              {{ t('home.providers.description') }}
            </p>
          </div>

          <div class="mb-4 flex flex-wrap items-center justify-center gap-4">
            <!-- Claude - Supported -->
            <div
              class="flex flex-col gap-2 rounded-xl border border-primary-200 bg-white/60 px-5 py-3 ring-1 ring-primary-500/20 backdrop-blur-sm dark:border-primary-800 dark:bg-dark-800/60"
            >
              <div class="flex items-center gap-2">
                <div
                  class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-orange-400 to-orange-500"
                >
                  <span class="text-xs font-bold text-white">C</span>
                </div>
                <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('home.providers.claude') }}</span>
              </div>
              <div class="flex flex-wrap items-center gap-1.5">
                <span
                  class="rounded bg-green-100 px-1.5 py-0.5 text-[10px] font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400"
                  >{{ t('home.providers.supported') }}</span
                >
                <span
                  class="rounded bg-primary-100 px-1.5 py-0.5 text-[10px] font-medium text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"
                  >{{ t('home.providers.priceFrom', { rate: providerPriceRates.claude }) }}</span
                >
              </div>
            </div>
            <!-- GPT - Supported -->
            <div
              class="flex flex-col gap-2 rounded-xl border border-primary-200 bg-white/60 px-5 py-3 ring-1 ring-primary-500/20 backdrop-blur-sm dark:border-primary-800 dark:bg-dark-800/60"
            >
              <div class="flex items-center gap-2">
                <div
                  class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-green-500 to-green-600"
                >
                  <span class="text-xs font-bold text-white">G</span>
                </div>
                <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('home.providers.gpt') }}</span>
              </div>
              <div class="flex flex-wrap items-center gap-1.5">
                <span
                  class="rounded bg-green-100 px-1.5 py-0.5 text-[10px] font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400"
                  >{{ t('home.providers.supported') }}</span
                >
                <span
                  class="rounded bg-primary-100 px-1.5 py-0.5 text-[10px] font-medium text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"
                  >{{ t('home.providers.priceFrom', { rate: providerPriceRates.gpt }) }}</span
                >
              </div>
            </div>
            <!-- Gemini - Supported -->
            <div
              class="flex flex-col gap-2 rounded-xl border border-primary-200 bg-white/60 px-5 py-3 ring-1 ring-primary-500/20 backdrop-blur-sm dark:border-primary-800 dark:bg-dark-800/60"
            >
              <div class="flex items-center gap-2">
                <div
                  class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-blue-500 to-blue-600"
                >
                  <span class="text-xs font-bold text-white">G</span>
                </div>
                <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('home.providers.gemini') }}</span>
              </div>
              <div class="flex flex-wrap items-center gap-1.5">
                <span
                  class="rounded bg-green-100 px-1.5 py-0.5 text-[10px] font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400"
                  >{{ t('home.providers.supported') }}</span
                >
                <span
                  class="rounded bg-primary-100 px-1.5 py-0.5 text-[10px] font-medium text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"
                  >{{ t('home.providers.priceFrom', { rate: providerPriceRates.gemini }) }}</span
                >
              </div>
            </div>
            <!-- More - Coming Soon -->
            <div
              class="flex items-center gap-2 rounded-xl border border-gray-200/50 bg-white/40 px-5 py-3 opacity-60 backdrop-blur-sm dark:border-dark-700/50 dark:bg-dark-800/40"
            >
              <div
                class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-gray-500 to-gray-600"
              >
                <span class="text-xs font-bold text-white">+</span>
              </div>
              <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('home.providers.more') }}</span>
              <span
                class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-500 dark:bg-dark-700 dark:text-dark-400"
                >{{ t('home.providers.soon') }}</span
              >
            </div>
          </div>
        </div>
      </details>
    </main>

    <!-- Footer -->
    <footer class="relative z-10 border-t border-gray-200/50 px-6 py-8 dark:border-dark-800/50">
      <div
        class="mx-auto flex max-w-7xl flex-col items-center justify-center gap-4 text-center sm:flex-row sm:text-left"
      >
        <p class="text-sm text-gray-500 dark:text-dark-400">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
        <div class="flex items-center gap-4">
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
          >
            {{ t('home.docs') }}
          </a>
          <a
            :href="githubUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
          >
            GitHub
          </a>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import ClientIntroduction from '@/components/client/ClientIntroduction.vue'
import { useServiceAvailability } from '@/composables/useServiceAvailability'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'

const { t } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()
const { webEnabled, clientEnabled } = useServiceAvailability()

// Site settings - directly from appStore (already initialized from injected config)
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)

// Check if homeContent is a URL (for iframe display)
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

// Theme
const isDark = ref(document.documentElement.classList.contains('dark'))

// GitHub URL
const githubUrl = 'https://github.com/Wei-Shaw/sub2api'

// 2026-09-22 从生产库 groups 表核对的分组倍率（排除测试/专属兜底分组），按平台取当前
// 最具代表性的最低倍率。分组调价后需要手动同步这几个数字，不接公开接口。
const providerPriceRates = {
  claude: '0.03',
  gpt: '0.1',
  gemini: '0.1'
}

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')

// Current year for footer
const currentYear = computed(() => new Date().getFullYear())

// Toggle theme
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

// Initialize theme
function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()

  // Check auth state
  authStore.checkAuth()

  // Ensure public settings are loaded (will use cache if already loaded from injected config)
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>
