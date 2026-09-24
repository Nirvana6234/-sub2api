<template>
  <div class="trial-page min-h-screen">
    <header class="trial-header">
      <nav class="mx-auto flex max-w-5xl items-center justify-between gap-3 px-4 py-4 sm:px-6">
        <router-link to="/home" class="flex min-w-0 items-center gap-3">
          <img :src="siteLogo" :alt="siteName" class="h-9 w-9 shrink-0 rounded-lg object-contain" />
          <span class="truncate text-base font-semibold">{{ siteName }}</span>
        </router-link>
        <div class="flex items-center gap-2">
          <router-link v-if="!isAuthenticated" to="/login" class="trial-nav-link">{{ t('guestTrial.login') }}</router-link>
          <router-link :to="isAuthenticated ? '/playground' : '/register'" class="trial-nav-cta">
            {{ isAuthenticated ? t('guestTrial.openWorkspace') : t('guestTrial.register') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="mx-auto w-full max-w-3xl px-4 pb-16 pt-8 sm:px-6 sm:pt-12">
      <section class="text-center">
        <p class="trial-eyebrow"><span aria-hidden="true">✳</span>{{ t('guestTrial.eyebrow') }}<span class="trial-pill">{{ t('guestTrial.noSignup') }}</span></p>
        <h1 class="trial-title">{{ t('guestTrial.title') }}</h1>
        <p class="trial-subtitle">{{ t('guestTrial.subtitle') }}</p>
      </section>

      <div v-if="loading" class="mt-10 flex justify-center"><LoadingSpinner /></div>

      <!-- 未开放 -->
      <section v-else-if="!state?.enabled" class="trial-card mt-10 p-8 text-center" data-testid="trial-disabled">
        <h2 class="text-lg font-semibold">{{ t('guestTrial.disabledTitle') }}</h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ t('guestTrial.disabledHint') }}</p>
        <div class="mt-6 flex justify-center gap-3">
          <router-link to="/register" class="trial-send">{{ t('guestTrial.register') }}</router-link>
          <router-link to="/login" class="trial-ghost">{{ t('guestTrial.login') }}</router-link>
        </div>
      </section>

      <section v-else class="trial-card mt-8 flex flex-col" data-testid="trial-chat">
        <!-- 顶部：模型 + 剩余次数 -->
        <div class="trial-toolbar">
          <label v-if="state.models.length > 1" class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
            {{ t('guestTrial.model') }}
            <select v-model="model" class="trial-select" :disabled="sending" data-testid="trial-model">
              <option v-for="option in state.models" :key="option" :value="option">{{ option }}</option>
            </select>
          </label>
          <span v-else class="text-xs text-gray-500 dark:text-dark-400">{{ t('guestTrial.model') }} · <b class="font-medium text-gray-700 dark:text-dark-200">{{ model }}</b></span>
          <span class="trial-remaining" :class="{ 'is-low': remaining <= 3 }" data-testid="trial-remaining">
            {{ t('guestTrial.remaining', { remaining, total: state.daily_limit }) }}
          </span>
        </div>

        <!-- 对话区 -->
        <div ref="scrollRef" class="trial-messages" aria-live="polite">
          <div v-if="messages.length === 0" class="trial-empty">
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('guestTrial.emptyHint') }}</p>
            <div class="mt-4 flex flex-wrap justify-center gap-2">
              <button v-for="index in [1, 2, 3]" :key="index" type="button" class="trial-suggestion" @click="useSuggestion(t(`guestTrial.suggestion${index}`))">
                {{ t(`guestTrial.suggestion${index}`) }}
              </button>
            </div>
          </div>
          <template v-else>
            <div v-for="(message, index) in messages" :key="index" class="trial-message" :class="`is-${message.role}`">
              <div v-if="message.role === 'user'" class="trial-bubble">{{ message.content }}</div>
              <div v-else class="trial-answer">
                <div v-if="message.content" class="trial-markdown" v-html="renderMarkdown(message.content)"></div>
                <span v-else class="trial-typing"><i></i><i></i><i></i></span>
              </div>
            </div>
          </template>
        </div>

        <p v-if="errorMessage" class="trial-error" role="alert" data-testid="trial-error">{{ errorMessage }}</p>

        <!-- 次数用完：引导注册 -->
        <div v-if="exhausted" class="trial-upsell" data-testid="trial-upsell">
          <div>
            <p class="font-semibold">{{ t('guestTrial.exhaustedTitle') }}</p>
            <p class="mt-1 text-sm text-gray-600 dark:text-dark-300">{{ t('guestTrial.exhaustedHint') }}</p>
          </div>
          <router-link to="/register" class="trial-send shrink-0">{{ t('guestTrial.register') }}</router-link>
        </div>

        <!-- 输入区 -->
        <form v-else class="trial-composer" @submit.prevent="send()">
          <CaptchaChallenge
            v-if="captchaNeeded"
            ref="captchaRef"
            class="mb-3"
            :turnstile-enabled="captchaSettings.turnstileEnabled"
            :turnstile-site-key="captchaSettings.turnstileSiteKey"
            :tencent-enabled="captchaSettings.tencentEnabled"
            :tencent-app-id="captchaSettings.tencentAppId"
            :tencent-region="captchaSettings.tencentRegion"
            :aliyun-enabled="captchaSettings.aliyunEnabled"
            :aliyun-scene-id="captchaSettings.aliyunSceneId"
            :aliyun-prefix="captchaSettings.aliyunPrefix"
            :aliyun-region="captchaSettings.aliyunRegion"
            @verify="onWidgetVerify"
          />
          <textarea
            v-model="draft"
            class="trial-input"
            rows="3"
            :placeholder="t('guestTrial.placeholder')"
            :maxlength="state.max_input_chars"
            :disabled="sending"
            data-testid="trial-input"
            @keydown.enter.exact.prevent="send()"
          ></textarea>
          <div class="mt-2 flex items-center justify-between gap-3">
            <span class="text-[11px] text-gray-400 dark:text-dark-500">{{ t('guestTrial.textOnly') }}</span>
            <button v-if="sending" type="button" class="trial-ghost" @click="stop">{{ t('guestTrial.stop') }}</button>
            <button v-else type="submit" class="trial-send" :disabled="!draft.trim()" data-testid="trial-send">
              {{ t('guestTrial.send') }}<Icon name="arrowRight" size="sm" aria-hidden="true" />
            </button>
          </div>
        </form>
      </section>

      <!-- 注册后能做什么 -->
      <section v-if="state?.enabled" class="trial-unlock">
        <p class="text-sm font-semibold">{{ t('guestTrial.unlockTitle') }}</p>
        <ul>
          <li v-for="index in [1, 2, 3, 4]" :key="index"><Icon name="check" size="xs" aria-hidden="true" />{{ t(`guestTrial.unlock${index}`) }}</li>
        </ul>
        <router-link :to="isAuthenticated ? '/playground' : '/register'" class="trial-unlock-link">
          {{ isAuthenticated ? t('guestTrial.openWorkspace') : t('guestTrial.registerFree') }}<Icon name="arrowRight" size="xs" aria-hidden="true" />
        </router-link>
      </section>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import CaptchaChallenge from '@/components/CaptchaChallenge.vue'
import { useAppStore, useAuthStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import { renderPlaygroundMarkdown } from '@/features/playground/markdown'
import {
  GuestTrialError,
  fetchGuestTrialState,
  sendGuestTrialChat,
  trimGuestTrialHistory,
  verifyGuestTrial,
  type GuestTrialMessage,
  type GuestTrialState,
} from '@/api/trial'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || '共飞 AI')
const siteLogo = computed(
  () => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }) || '/gongfei-plane.svg',
)
const isAuthenticated = computed(() => authStore.isAuthenticated)

const loading = ref(true)
const state = ref<GuestTrialState | null>(null)
const model = ref('')
const remaining = ref(0)
const messages = ref<GuestTrialMessage[]>([])
const draft = ref('')
const sending = ref(false)
const errorMessage = ref('')
const exhausted = ref(false)
const captchaNeeded = ref(false)
const scrollRef = ref<HTMLElement | null>(null)
const captchaRef = ref<InstanceType<typeof CaptchaChallenge> | null>(null)
let controller: AbortController | null = null

const captchaSettings = computed(() => {
  const settings = appStore.cachedPublicSettings
  return {
    turnstileEnabled: settings?.turnstile_enabled === true,
    turnstileSiteKey: settings?.turnstile_site_key || '',
    tencentEnabled: settings?.tencent_captcha_enabled === true,
    tencentAppId: settings?.tencent_captcha_app_id || '',
    tencentRegion: settings?.tencent_captcha_region || 'cn',
    aliyunEnabled: settings?.aliyun_captcha_enabled === true,
    aliyunSceneId: settings?.aliyun_captcha_scene_id || '',
    aliyunPrefix: settings?.aliyun_captcha_prefix || '',
    aliyunRegion: settings?.aliyun_captcha_region || 'cn',
  }
})
// Turnstile 是页面内组件，验证结果通过事件回传；腾讯 / 阿里云是发送时弹窗。
const usesInlineWidget = computed(() => captchaSettings.value.turnstileEnabled && Boolean(captchaSettings.value.turnstileSiteKey))
const pendingWidgetToken = ref('')

const renderMarkdown = (source: string) => renderPlaygroundMarkdown(source)

async function loadState() {
  loading.value = true
  try {
    const next = await fetchGuestTrialState()
    state.value = next
    model.value = next.default_model || next.models[0] || ''
    remaining.value = next.remaining
    exhausted.value = next.enabled && next.remaining <= 0
    captchaNeeded.value = next.captcha_required
  } catch {
    state.value = { enabled: false, models: [], default_model: '', daily_limit: 0, remaining: 0, max_input_chars: 0, captcha_required: false }
  } finally {
    loading.value = false
  }
}

function useSuggestion(text: string) {
  draft.value = text
}

function onWidgetVerify(token: string) {
  pendingWidgetToken.value = token
}

/** 需要人机验证时先完成验证；返回 false 表示用户取消或验证失败。 */
async function ensureVerified(): Promise<boolean> {
  if (!captchaNeeded.value) return true
  try {
    if (usesInlineWidget.value) {
      if (!pendingWidgetToken.value) {
        errorMessage.value = t('guestTrial.captchaFirst')
        return false
      }
      await verifyGuestTrial({ turnstile_token: pendingWidgetToken.value })
    } else {
      const proof = await captchaRef.value?.verifyAction()
      if (!proof) return false
      await verifyGuestTrial(proof.randstr ? { tencent_ticket: proof.token, tencent_randstr: proof.randstr } : { turnstile_token: proof.token })
    }
    captchaNeeded.value = false
    return true
  } catch {
    errorMessage.value = t('guestTrial.captchaFailed')
    pendingWidgetToken.value = ''
    captchaRef.value?.reset()
    return false
  }
}

async function scrollToBottom() {
  await nextTick()
  if (scrollRef.value) scrollRef.value.scrollTop = scrollRef.value.scrollHeight
}

async function send() {
  const text = draft.value.trim()
  if (!text || sending.value || !state.value?.enabled || exhausted.value) return
  errorMessage.value = ''
  if (!(await ensureVerified())) return

  const history = trimGuestTrialHistory([...messages.value, { role: 'user', content: text }], state.value.max_input_chars)
  messages.value.push({ role: 'user', content: text })
  const answer: GuestTrialMessage = { role: 'assistant', content: '' }
  messages.value.push(answer)
  const answerIndex = messages.value.length - 1
  draft.value = ''
  sending.value = true
  controller = new AbortController()
  await scrollToBottom()

  try {
    const result = await sendGuestTrialChat(model.value, history, {
      signal: controller.signal,
      onDelta: (delta) => {
        messages.value[answerIndex].content += delta
        void scrollToBottom()
      },
    })
    if (result.remaining !== null && Number.isFinite(result.remaining)) remaining.value = result.remaining
    else remaining.value = Math.max(remaining.value - 1, 0)
    if (!messages.value[answerIndex].content) messages.value[answerIndex].content = t('guestTrial.emptyAnswer')
  } catch (error) {
    if (controller?.signal.aborted) {
      if (!messages.value[answerIndex].content) messages.value.splice(answerIndex, 1)
    } else {
      messages.value.splice(answerIndex, 1)
      handleSendError(error, text)
    }
  } finally {
    sending.value = false
    controller = null
    exhausted.value = remaining.value <= 0
  }
}

function handleSendError(error: unknown, text: string) {
  const reason = error instanceof GuestTrialError ? error.reason : ''
  // 失败的这条不算数：把问题放回输入框方便重发
  messages.value.pop()
  draft.value = text
  if (reason === 'GUEST_TRIAL_QUOTA_EXHAUSTED' || reason === 'GUEST_TRIAL_BUSY') {
    remaining.value = 0
    exhausted.value = true
    draft.value = ''
    return
  }
  if (reason === 'GUEST_TRIAL_CAPTCHA_REQUIRED') {
    captchaNeeded.value = true
    pendingWidgetToken.value = ''
  }
  if (reason === 'GUEST_TRIAL_DISABLED') {
    void loadState()
  }
  errorMessage.value = error instanceof Error && error.message ? error.message : t('guestTrial.sendFailed')
}

function stop() {
  controller?.abort()
}

onMounted(() => {
  if (!appStore.publicSettingsLoaded) void appStore.fetchPublicSettings()
  void loadState()
})

onBeforeUnmount(() => controller?.abort())
</script>

<style scoped>
.trial-page {
  --ink: #172b39;
  --muted: #647077;
  --blue: #2864dc;
  --soft: #eef3fb;
  color: var(--ink);
  background-color: #f6f7f3;
  background-image: radial-gradient(rgba(23, 43, 57, 0.07) 0.8px, transparent 0.8px);
  background-size: 18px 18px;
}
.trial-header { border-bottom: 1px solid rgba(23, 43, 57, 0.07); background: rgba(255, 255, 255, 0.85); backdrop-filter: blur(12px); }
.trial-nav-link { display: inline-flex; min-height: 40px; align-items: center; padding: 0 14px; border-radius: 12px; font-size: 14px; color: var(--muted); }
.trial-nav-link:hover { background: rgba(23, 43, 57, 0.05); color: var(--ink); }
.trial-nav-cta { display: inline-flex; min-height: 40px; align-items: center; padding: 0 16px; border-radius: 12px; background: var(--blue); color: #fff; font-size: 14px; font-weight: 600; }
.trial-nav-cta:hover { background: #174bba; }
.trial-eyebrow { display: inline-flex; align-items: center; gap: 8px; color: var(--blue); font-size: 13px; font-weight: 650; letter-spacing: 0.04em; }
.trial-pill { padding: 2px 9px; border-radius: 999px; background: var(--blue); color: #fff; font-size: 11px; letter-spacing: 0; }
.trial-title { margin-top: 14px; font-size: clamp(28px, 4vw, 40px); font-weight: 750; letter-spacing: -0.04em; }
.trial-subtitle { max-width: 34rem; margin: 10px auto 0; color: var(--muted); font-size: 15px; line-height: 1.8; }
.trial-card { border: 1px solid rgba(23, 43, 57, 0.08); border-radius: 22px; background: rgba(255, 255, 255, 0.96); box-shadow: 0 26px 56px -28px rgba(37, 59, 75, 0.35), 0 3px 9px rgba(38, 59, 74, 0.05); overflow: hidden; }
.trial-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 12px 18px; border-bottom: 1px solid rgba(23, 43, 57, 0.06); }
.trial-select { border: 1px solid rgba(23, 43, 57, 0.12); border-radius: 8px; background: #fff; padding: 3px 8px; font-size: 12px; color: var(--ink); }
.trial-remaining { padding: 3px 10px; border-radius: 999px; background: var(--soft); color: var(--blue); font-size: 12px; font-weight: 600; font-variant-numeric: tabular-nums; }
.trial-remaining.is-low { background: #fff4e0; color: #b45309; }
.trial-messages { min-height: 18rem; max-height: 55vh; overflow-y: auto; padding: 18px; }
.trial-empty { display: flex; min-height: 15rem; flex-direction: column; align-items: center; justify-content: center; text-align: center; }
.trial-suggestion { padding: 8px 14px; border: 1px solid rgba(23, 43, 57, 0.1); border-radius: 999px; background: #fff; font-size: 13px; color: var(--ink); transition: border-color 0.15s, color 0.15s; }
.trial-suggestion:hover { border-color: var(--blue); color: var(--blue); }
.trial-message { display: flex; margin-bottom: 14px; }
.trial-message.is-user { justify-content: flex-end; }
.trial-bubble { max-width: 80%; padding: 10px 14px; border-radius: 16px 16px 4px 16px; background: var(--blue); color: #fff; font-size: 14px; line-height: 1.7; white-space: pre-wrap; word-break: break-word; }
.trial-answer { max-width: 92%; font-size: 14px; line-height: 1.8; }
.trial-markdown :deep(p) { margin: 0 0 0.6em; }
.trial-markdown :deep(pre) { overflow-x: auto; padding: 10px 12px; border-radius: 10px; background: #f3f4f0; font-size: 12.5px; }
.trial-markdown :deep(code) { font-family: ui-monospace, Consolas, monospace; }
.trial-markdown :deep(ul), .trial-markdown :deep(ol) { padding-left: 1.3em; margin: 0 0 0.6em; list-style: revert; }
.trial-typing { display: inline-flex; gap: 4px; padding: 8px 0; }
.trial-typing i { width: 6px; height: 6px; border-radius: 50%; background: #9aa7b0; animation: trial-dot 1.2s infinite ease-in-out; }
.trial-typing i:nth-child(2) { animation-delay: 0.15s; }
.trial-typing i:nth-child(3) { animation-delay: 0.3s; }
@keyframes trial-dot { 0%, 80%, 100% { opacity: 0.3; transform: translateY(0); } 40% { opacity: 1; transform: translateY(-3px); } }
.trial-error { margin: 0 18px 10px; padding: 8px 12px; border-radius: 10px; background: #fef2f2; color: #b91c1c; font-size: 13px; }
.trial-composer { padding: 14px 18px 16px; border-top: 1px solid rgba(23, 43, 57, 0.06); background: #fbfbf9; }
.trial-input { width: 100%; resize: none; border: 1px solid rgba(23, 43, 57, 0.12); border-radius: 14px; background: #fff; padding: 10px 14px; font-size: 14px; line-height: 1.7; color: var(--ink); outline: none; transition: border-color 0.15s, box-shadow 0.15s; }
.trial-input:focus { border-color: var(--blue); box-shadow: 0 0 0 4px rgba(40, 100, 220, 0.12); }
.trial-send { display: inline-flex; align-items: center; gap: 8px; min-height: 38px; padding: 0 16px; border-radius: 12px; background: var(--blue); color: #fff; font-size: 14px; font-weight: 600; box-shadow: 0 6px 16px -8px rgba(40, 100, 220, 0.6); }
.trial-send:hover:not(:disabled) { background: #174bba; }
.trial-send:disabled { opacity: 0.45; cursor: not-allowed; }
.trial-ghost { display: inline-flex; align-items: center; min-height: 38px; padding: 0 16px; border: 1px solid rgba(23, 43, 57, 0.14); border-radius: 12px; font-size: 14px; color: var(--ink); background: #fff; }
.trial-upsell { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin: 0 18px 18px; padding: 16px 18px; border-radius: 16px; background: var(--soft); }
.trial-unlock { margin-top: 20px; padding: 18px 22px; border: 1px dashed rgba(23, 43, 57, 0.16); border-radius: 18px; background: rgba(255, 255, 255, 0.6); }
.trial-unlock ul { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 6px 16px; margin-top: 10px; font-size: 13px; color: var(--muted); }
.trial-unlock li { display: flex; align-items: center; gap: 6px; }
.trial-unlock li svg { color: var(--blue); flex-shrink: 0; }
.trial-unlock-link { display: inline-flex; align-items: center; gap: 5px; margin-top: 12px; color: var(--blue); font-size: 13px; font-weight: 600; }
:global(.dark) .trial-page { --ink: #e5ebef; --muted: #a7b6c0; --blue: #91b6ff; --soft: #1c2c40; background-color: #0f1720; background-image: radial-gradient(rgba(148, 163, 184, 0.07) 0.8px, transparent 0.8px); }
:global(.dark) .trial-header { background: rgba(15, 23, 32, 0.85); border-color: #1f2d38; }
:global(.dark) .trial-card, :global(.dark) .trial-input, :global(.dark) .trial-select, :global(.dark) .trial-suggestion, :global(.dark) .trial-ghost { background: #17232e; border-color: #30414c; color: var(--ink); }
:global(.dark) .trial-composer { background: #131d27; border-color: #26343f; }
:global(.dark) .trial-toolbar { border-color: #26343f; }
:global(.dark) .trial-bubble, :global(.dark) .trial-send, :global(.dark) .trial-nav-cta { background: #376bdd; }
:global(.dark) .trial-markdown :deep(pre) { background: #0f1720; }
:global(.dark) .trial-unlock { background: rgba(23, 35, 46, 0.6); border-color: #30414c; }
@media (max-width: 640px) {
  .trial-unlock ul { grid-template-columns: 1fr; }
  .trial-upsell { flex-direction: column; align-items: stretch; text-align: center; }
  .trial-messages { max-height: 50vh; }
}
</style>
