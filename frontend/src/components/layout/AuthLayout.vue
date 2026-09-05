<template>
  <div class="gongfei-auth relative flex min-h-screen items-center justify-center overflow-hidden p-4">
    <div class="gongfei-route gongfei-route-top" aria-hidden="true"></div>
    <div class="gongfei-route gongfei-route-bottom" aria-hidden="true"></div>

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
  background: #f4fbff;
}

.gongfei-auth::before {
  position: absolute;
  inset: 0;
  content: '';
  background-image: linear-gradient(#dbeef7 1px, transparent 1px), linear-gradient(90deg, #dbeef7 1px, transparent 1px);
  background-size: 40px 40px;
  opacity: 0.45;
}

.gongfei-route {
  position: absolute;
  z-index: 0;
  border: 2px dashed #9cdbef;
  border-radius: 50%;
  opacity: 0.75;
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
  color: #173b5c;
  letter-spacing: 0;
}

.gongfei-subtitle {
  color: #5d7387;
}

.gongfei-auth-card {
  border: 1px solid #cfe6f1;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 18px 45px rgba(39, 102, 137, 0.14), 0 2px 0 rgba(255, 255, 255, 0.9) inset;
}

:global(.dark) .gongfei-auth {
  background: #0d1d2b;
}

:global(.dark) .gongfei-auth::before {
  background-image: linear-gradient(#26475c 1px, transparent 1px), linear-gradient(90deg, #26475c 1px, transparent 1px);
  opacity: 0.32;
}

:global(.dark) .gongfei-auth-card {
  border-color: #29475a;
  background: rgba(17, 35, 50, 0.96);
  box-shadow: 0 18px 45px rgba(0, 0, 0, 0.25);
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
  .gongfei-route {
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
