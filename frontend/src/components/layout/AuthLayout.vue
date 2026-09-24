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

      <!-- Copyright -->
      <div class="mt-8 text-center text-xs text-gray-400 dark:text-dark-500">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || '共飞 AI')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || '一起共享，一起使用 AI')

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
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
