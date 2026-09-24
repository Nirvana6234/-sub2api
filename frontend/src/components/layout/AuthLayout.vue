<template>
  <div class="gongfei-auth relative flex min-h-screen items-center justify-center overflow-hidden p-4">
    <div class="gongfei-route gongfei-route-top" aria-hidden="true"></div>
    <div class="gongfei-route gongfei-route-bottom" aria-hidden="true"></div>
    <span class="gongfei-star" aria-hidden="true">✳</span>

    <!-- Content Container -->
    <div class="relative z-10 w-full max-w-md">
      <div class="gongfei-brand mb-7 text-center">
        <div class="gongfei-plane-wrap mx-auto mb-2">
          <img :src="siteLogo || '/gongfei-plane.svg'" :alt="`${siteName} 标志`" class="gongfei-plane" />
        </div>
        <h1 class="gongfei-title mb-1 text-3xl font-bold">{{ siteName }}</h1>
        <p class="gongfei-subtitle text-sm">{{ siteSubtitle }}</p>
      </div>

      <div class="gongfei-auth-card p-8">
        <slot />
      </div>

      <!-- Footer Links -->
      <div class="mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <!-- 还没想好注册？可以先回去继续免注册试用，或者回主页看看 -->
      <div v-if="showGuestActions" class="gongfei-guest-actions" data-testid="auth-guest-actions">
        <router-link v-if="trialEnabled" to="/trial" class="gongfei-guest-action is-trial" data-testid="auth-continue-trial">
          <Icon name="chat" size="sm" aria-hidden="true" />{{ t('guestTrial.continueTrial') }}<span class="gongfei-guest-badge">{{ t('guestTrial.noSignup') }}</span>
        </router-link>
        <router-link to="/home" class="gongfei-guest-action" data-testid="auth-back-home">
          <Icon name="home" size="sm" aria-hidden="true" />{{ t('guestTrial.backHome') }}
        </router-link>
      </div>

      <!-- Copyright -->
      <div class="mt-8 text-center text-xs text-gray-400 dark:text-dark-500">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import Icon from '@/components/icons/Icon.vue'
import { fetchGuestTrialState } from '@/api/trial'

// 登录 / 注册 / 找回密码页打开：提供「继续试用」「返回主页」两个出口；回调类中间页不显示。
const props = withDefaults(defineProps<{ showGuestActions?: boolean }>(), { showGuestActions: false })

const { t } = useI18n()
const trialEnabled = ref(false)

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || '共飞 AI')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || '一起共享，一起使用 AI')

const currentYear = computed(() => new Date().getFullYear())

onMounted(async () => {
  appStore.fetchPublicSettings()
  if (!props.showGuestActions) return
  try {
    trialEnabled.value = (await fetchGuestTrialState()).enabled === true
  } catch {
    trialEnabled.value = false
  }
})
</script>

<style scoped>
.gongfei-auth {
  background: #f6f7f3;
}

.gongfei-auth::before {
  position: absolute;
  inset: 0;
  content: '';
  background-image:
    radial-gradient(rgba(23, 43, 57, 0.08) 0.8px, transparent 0.8px),
    radial-gradient(ellipse 50% 40% at 85% 0%, rgba(22, 155, 208, 0.08), transparent 70%),
    radial-gradient(ellipse 45% 35% at 5% 100%, rgba(233, 219, 190, 0.35), transparent 70%);
  background-size: 18px 18px, 100% 100%, 100% 100%;
}

.gongfei-route {
  position: absolute;
  z-index: 0;
  border: 1.5px dashed #8cacb3;
  border-radius: 50%;
  opacity: 0.6;
}

.gongfei-star {
  position: absolute;
  top: 14%;
  left: calc(50% - 16rem);
  z-index: 0;
  color: #d28c62;
  font-size: 30px;
  transform: rotate(15deg);
}

.gongfei-route-top {
  top: -13rem;
  right: -4rem;
  width: 34rem;
  height: 22rem;
}

.gongfei-route-bottom {
  bottom: -13rem;
  left: -8rem;
  width: 30rem;
  height: 19rem;
}

.gongfei-brand,
.gongfei-auth-card {
  position: relative;
  z-index: 1;
}

.gongfei-plane-wrap {
  display: grid;
  width: 6rem;
  height: 5.5rem;
  place-items: center;
}

.gongfei-plane {
  width: 6.5rem;
  height: 6.5rem;
  object-fit: contain;
  filter: drop-shadow(0 9px 10px rgba(225, 66, 61, 0.16));
  animation: flight-hover 3.8s ease-in-out infinite;
}

.gongfei-title {
  color: #172b39;
  letter-spacing: -0.02em;
}

.gongfei-subtitle {
  color: #647077;
}

.gongfei-auth-card {
  border: 1px solid rgba(23, 43, 57, 0.08);
  border-radius: 20px;
  background: rgba(255, 255, 255, 0.96);
  box-shadow: 0 26px 56px -24px rgba(37, 59, 75, 0.35), 0 3px 9px rgba(38, 59, 74, 0.05);
}

:global(.dark) .gongfei-auth {
  background: #0f1720;
}

:global(.dark) .gongfei-auth::before {
  background-image:
    radial-gradient(rgba(148, 163, 184, 0.08) 0.8px, transparent 0.8px),
    radial-gradient(ellipse 50% 40% at 85% 0%, rgba(22, 155, 208, 0.1), transparent 70%);
  background-size: 18px 18px, 100% 100%;
}

:global(.dark) .gongfei-route {
  border-color: #476478;
}

:global(.dark) .gongfei-auth-card {
  border-color: #30414c;
  background: rgba(23, 35, 46, 0.96);
  box-shadow: 0 26px 56px -24px rgba(0, 0, 0, 0.5);
}

:global(.dark) .gongfei-title {
  color: #f4fbff;
}

:global(.dark) .gongfei-subtitle {
  color: #a3bed0;
}

.gongfei-guest-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 10px;
  margin-top: 18px;
}

.gongfei-guest-action {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-height: 40px;
  padding: 0 16px;
  border: 1px solid rgba(23, 43, 57, 0.12);
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.9);
  color: #172b39;
  font-size: 14px;
  font-weight: 550;
  transition: border-color 0.15s, background-color 0.15s, color 0.15s;
}

.gongfei-guest-action:hover {
  border-color: rgba(23, 43, 57, 0.24);
  background: #fff;
}

.gongfei-guest-action.is-trial {
  border-color: #c9d8f5;
  background: #eef3fb;
  color: #2864dc;
}

.gongfei-guest-action.is-trial:hover {
  background: #e2ebfa;
}

.gongfei-guest-badge {
  padding: 1px 7px;
  border-radius: 999px;
  background: #2864dc;
  color: #fff;
  font-size: 11px;
  font-weight: 600;
}

:global(.dark) .gongfei-guest-action {
  border-color: #30414c;
  background: rgba(23, 35, 46, 0.9);
  color: #e5ebef;
}

:global(.dark) .gongfei-guest-action.is-trial {
  border-color: #2c4468;
  background: #1c2c40;
  color: #91b6ff;
}

:global(.dark) .gongfei-guest-badge {
  background: #91b6ff;
  color: #0f1720;
}

@keyframes flight-hover {
  0%,
  100% {
    transform: translateY(0) rotate(-2deg);
  }
  50% {
    transform: translateY(-5px) rotate(2deg);
  }
}

@media (max-width: 640px) {
  .gongfei-route,
  .gongfei-star {
    display: none;
  }

  .gongfei-auth-card {
    padding: 1.5rem;
  }
}

@media (prefers-reduced-motion: reduce) {
  .gongfei-plane {
    animation: none;
  }
}
</style>
