<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore } from '@/stores'
import Icon from '@/components/icons/Icon.vue'
import { useServiceAvailability } from '@/composables/useServiceAvailability'
import { fetchGuestTrialState } from '@/api/trial'

// variant="home"：首页用同一套视觉，只换成"AI 网关 + Codex + 网页版 AI"的文案；
// 下载页保持 client 原样。首页按后台开关决定是否提网页工作台和客户端。
const props = withDefaults(defineProps<{ downloadPage?: boolean; variant?: 'client' | 'home' }>(), { variant: 'client' })

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const { rechargeEnabled, webEnabled, clientEnabled, apiEndpoint } = useServiceAvailability()
const isHome = computed(() => props.variant === 'home')
// 免注册试用只对未登录访客展示；登录用户直接用完整的网页工作台。
const trialEnabled = ref(false)
const trialAvailable = computed(() => isHome.value && trialEnabled.value && !authStore.isAuthenticated)
onMounted(async () => {
  if (!isHome.value) return
  try {
    trialEnabled.value = (await fetchGuestTrialState()).enabled === true
  } catch {
    trialEnabled.value = false
  }
})
// 开了客户端走客户端三步（下载 → 登录 → 用），没开才讲手动写配置。
const codexStepKey = (step: number, suffix = '') => `homeIntro.codex.${clientEnabled.value ? 'clientStep' : 'step'}${step}${suffix}`
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || '共飞 AI')
const mapModels = ['GPT', 'Claude', 'Gemini']
const mapTools = ['Claude Code', 'Cursor', 'Cline', 'Cherry Studio']
const consoleTo = computed(() => (!authStore.isAuthenticated ? '/login' : authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))
// 需要登录的页面：未登录先去登录页，登录后回跳。
const authedPath = (path: string) => (authStore.isAuthenticated ? path : { path: '/login', query: { redirect: path } })
// 用法顺序按主推程度排：客户端是主推产品（装好就能用、还能省 Token），其次接入各类插件，最后网页版。
const ways = computed(() => {
  const list: { id: 'api' | 'web' | 'client'; to: ReturnType<typeof authedPath> }[] = []
  if (clientEnabled.value) list.push({ id: 'client', to: '/download' })
  list.push({ id: 'api', to: authedPath('/keys') })
  if (webEnabled.value) list.push({ id: 'web', to: trialAvailable.value ? '/trial' : authedPath('/playground') })
  return list
})
const macAvailable = computed(() =>
  /^https?:\/\//i.test((appStore.cachedPublicSettings?.client_download_direct_url_mac || '').trim()),
)
// 顺序按主要业务排：写代码 → 写作 → 设计图片 → 3D 模型，其余在后。第一个就是默认选中的场景。
const scenes = [
  { id: 'web', icon: 'globe', file: 'something-new.html' },
  { id: 'write', icon: 'edit', file: 'a-fresh-start.md' },
  { id: 'image', icon: 'sparkles', file: 'a-little-inspiration.png' },
  { id: 'model', icon: 'cube', file: 'my-first-object.blend' },
  { id: 'video', icon: 'play', file: 'weekend-film.mp4' },
  { id: 'data', icon: 'chartBar', file: 'a-clearer-picture.xlsx' },
] as const
const selectedScene = ref<(typeof scenes)[number]['id']>(scenes[0].id)
const activeScene = computed(() => scenes.find((scene) => scene.id === selectedScene.value)!)
</script>

<template>
  <div class="client-introduction">
    <section aria-labelledby="intro-title" class="studio-hero">
      <div class="hero-copy">
        <template v-if="isHome">
          <p class="eyebrow"><span class="brand-spark" aria-hidden="true">✳</span>{{ webEnabled ? t('homeIntro.eyebrow') : t('homeIntro.eyebrowGatewayOnly') }}</p>
          <h1 id="intro-title">{{ t('homeIntro.title') }}<span>{{ t('homeIntro.titleAccent') }}</span></h1>
          <p class="hero-description">{{ t('homeIntro.description') }}<template v-if="webEnabled">{{ t('homeIntro.descriptionWeb') }}</template></p>
          <div class="hero-actions">
            <router-link v-if="clientEnabled" to="/download" class="studio-button">
              {{ t('homeIntro.ctaClientPrimary') }}<Icon name="download" size="sm" aria-hidden="true" />
            </router-link>
            <!-- 「开始工作」和客户端按钮一样大：有客户端时用描边次级样式，没有客户端时它就是主按钮 -->
            <router-link :to="consoleTo" class="studio-button" :class="{ 'studio-button-outline': clientEnabled }" data-testid="home-start-work">
              {{ t('homeIntro.ctaStartWork') }}<Icon name="arrowRight" size="sm" aria-hidden="true" />
            </router-link>
            <!-- 未登录且开放了免注册试用：换成试用入口，让访客知道可以直接聊天 -->
            <router-link v-if="trialAvailable" to="/trial" class="studio-button studio-button-soft" data-testid="home-trial-cta">
              <Icon name="chat" size="xs" aria-hidden="true" />{{ t('homeIntro.ctaTrial') }}<span class="trial-badge">{{ t('homeIntro.ctaTrialBadge') }}</span>
            </router-link>
            <router-link v-else-if="webEnabled" :to="authedPath('/playground')" class="studio-link"><Icon name="chat" size="xs" aria-hidden="true" />{{ t('homeIntro.ctaWeb') }}</router-link>
          </div>
          <p class="beginner-note"><Icon :name="trialAvailable ? 'chat' : 'key'" size="sm" aria-hidden="true" />{{ trialAvailable ? t('homeIntro.trialHint') : t('homeIntro.beginnerNote') }}</p>
        </template>
        <template v-else>
        <p class="eyebrow"><span class="brand-spark" aria-hidden="true">✳</span>{{ t('clientIntroduction.eyebrow') }}</p>
        <h1 id="intro-title">{{ t('clientIntroduction.title') }}<span>{{ t('clientIntroduction.titleAccent') }}</span></h1>
        <p class="hero-description">{{ t('clientIntroduction.description') }}</p>
        <div class="hero-actions">
          <a v-if="downloadPage" href="#downloads" class="studio-button">
            {{ t('clientIntroduction.downloadHere') }}<Icon name="arrowDown" size="sm" aria-hidden="true" />
          </a>
          <router-link v-else to="/download" class="studio-button">
            {{ t('clientIntroduction.download') }}<Icon name="arrowRight" size="sm" aria-hidden="true" />
          </router-link>
          <a href="#requirements" class="studio-link">{{ t('clientIntroduction.checkRequirements') }}<Icon name="arrowDown" size="xs" aria-hidden="true" /></a>
        </div>
        <p class="beginner-note"><Icon name="chat" size="sm" aria-hidden="true" />{{ t('clientIntroduction.beginnerNote') }}</p>
        </template>
      </div>

      <div class="studio-canvas" aria-hidden="true">
        <div class="canvas-orbit"></div>
        <span class="canvas-caption">{{ t('clientIntroduction.canvasCaption') }}</span>
        <svg class="canvas-trail" viewBox="0 0 580 500" fill="none"><path d="M28 326C-2 212 161 125 293 162C425 199 422 50 514 34M410 415C485 443 555 378 548 288" stroke="currentColor" stroke-width="1.5" stroke-dasharray="5 7" /><path d="m502 32 13 1-3 13" stroke="currentColor" stroke-width="2" /></svg>
        <div class="work-window">
          <div class="window-bar"><span class="window-dots"><i></i><i></i><i></i></span><span>{{ activeScene.file }}</span><Icon :name="activeScene.icon" size="xs" /></div>
          <div class="work-preview" :class="`preview-${selectedScene}`">
            <template v-if="selectedScene === 'video'">
              <img src="/creative-studio/video-landscape.svg" alt="" width="800" height="500" />
              <span class="frame-label">A WEEKEND TO REMEMBER</span><span class="frame-time">00:12 / 00:30</span>
              <div class="timeline"><div class="time-ruler"><span>00:00</span><span>00:10</span><span>00:20</span><span>00:30</span></div><div class="clip-track"><i></i><i></i><i></i><i></i></div><div class="audio-track"></div><span class="playhead"></span></div>
            </template>
            <template v-else-if="selectedScene === 'model'">
              <img src="/creative-studio/model-sculpture.svg" alt="" width="480" height="480" />
              <span class="model-axis">Z<br />↗ X &nbsp; Y ↘</span><span class="art-caption">FORM / LIGHT / POSSIBILITY</span>
            </template>
            <template v-else-if="selectedScene === 'image'">
              <img src="/creative-studio/design-poster.svg" alt="" width="420" height="560" />
              <div class="color-swatches"><i></i><i></i><i></i><i></i></div>
            </template>
            <div v-else-if="selectedScene === 'data'" class="data-art">
              <span class="art-kicker">A CLEARER PICTURE</span><strong>{{ t('clientIntroduction.dataPreview') }}</strong>
              <div class="chart-art"><i style="--bar: 38%"></i><i style="--bar: 56%"></i><i style="--bar: 42%"></i><i style="--bar: 77%"></i><i style="--bar: 61%"></i><i style="--bar: 92%"></i></div>
              <div class="sheet-art"><span v-for="cell in 18" :key="cell"></span></div>
            </div>
            <div v-else-if="selectedScene === 'web'" class="web-art">
              <div class="mini-nav"><strong>little things.</strong><span>ABOUT &nbsp; WORK ↗</span></div>
              <span class="art-kicker">MADE WITH CURIOSITY</span><strong class="web-headline">A small idea.<br />A whole new world.</strong>
              <span class="mini-cta">LET’S MAKE IT ↗</span><div class="web-sun"></div>
            </div>
            <div v-else class="writing-art"><span class="art-kicker">A FRESH PAGE</span><strong>{{ t('clientIntroduction.writingPreview') }}</strong><p>{{ t('clientIntroduction.writingPreviewBody') }}</p><span class="writing-line"></span><span class="writing-line short"></span><Icon name="edit" size="xl" /></div>
          </div>
          <div class="window-footer"><span class="tiny-dot"></span>{{ t(`clientIntroduction.scenes.${selectedScene}.output`) }}<span>↗</span></div>
        </div>
        <div class="floating-piece model-piece">
          <span>{{ selectedScene === 'model' ? 'MOTION STUDY' : '3D EXPLORATION' }}<Icon name="arrowUp" size="xs" /></span>
          <img :src="selectedScene === 'model' ? '/creative-studio/video-landscape.svg' : '/creative-studio/model-sculpture.svg'" alt="" width="480" height="480" />
          <span class="piece-tag">{{ t(selectedScene === 'model' ? 'clientIntroduction.scenes.video.title' : 'clientIntroduction.scenes.model.title') }}</span>
        </div>
        <div class="floating-piece poster-piece"><img src="/creative-studio/design-poster.svg" alt="" width="420" height="560" /></div>
        <div class="idea-note"><Icon name="sparkles" size="sm" /><span>{{ isHome ? t('homeIntro.ideaNote') : t('clientIntroduction.ideaNote') }}</span><img src="/gongfei-plane.svg" alt="" width="40" height="40" /></div>
        <span class="canvas-star">✳</span>
      </div>
    </section>

    <section aria-labelledby="possibilities-title" class="possibilities">
      <div class="section-heading"><h2 id="possibilities-title">{{ t('clientIntroduction.possibilitiesTitle') }}</h2><p>{{ t('clientIntroduction.possibilitiesHint') }}</p></div>
      <div class="scene-options" role="group" :aria-label="t('clientIntroduction.possibilitiesTitle')">
        <button v-for="scene in scenes" :key="scene.id" type="button" :aria-pressed="selectedScene === scene.id" aria-controls="example-conversation" :class="{ selected: selectedScene === scene.id }" @click="selectedScene = scene.id">
          <span class="scene-icon" :class="`scene-icon-${scene.id}`"><Icon :name="scene.icon" size="lg" aria-hidden="true" /></span>
          <span>{{ t(`clientIntroduction.scenes.${scene.id}.title`) }}</span><Icon name="arrowRight" size="xs" class="scene-arrow" aria-hidden="true" />
        </button>
      </div>
      <div id="example-conversation" class="scene-brief" aria-live="polite" aria-atomic="true">
        <div class="prompt-example"><span class="quote-mark" aria-hidden="true">“</span><div><span class="small-label">{{ t('clientIntroduction.you') }}</span><p>{{ t(`clientIntroduction.scenes.${selectedScene}.prompt`) }}</p></div></div>
        <div class="scene-outcome"><p>{{ t(`clientIntroduction.scenes.${selectedScene}.description`) }}</p><span>{{ t(`clientIntroduction.scenes.${selectedScene}.tools`) }}</span></div>
      </div>
      <p class="purpose-note">{{ isHome ? (webEnabled ? t('homeIntro.purpose') : t('homeIntro.purposeNoWeb')) : t('clientIntroduction.purpose') }}</p>
    </section>

    <!-- 工作原理：同一套纸面 + 点阵画布语言，把"网关"讲给理科生看 -->
    <section v-if="isHome" id="how-it-connects" aria-labelledby="map-title" class="gateway-map">
      <div class="section-heading"><h2 id="map-title">{{ t('homeIntro.map.title') }}</h2><p>{{ t('homeIntro.map.hint') }}</p></div>
      <div class="map-board">
        <div class="map-column">
          <span class="map-label">{{ t('homeIntro.map.models') }}</span>
          <span v-for="(model, index) in mapModels" :key="model" class="map-ticket" :style="{ '--tilt': `${index % 2 ? 1.5 : -1.5}deg` }">{{ model }}</span>
          <span class="map-more">{{ t('homeIntro.map.more') }}</span>
        </div>
        <div class="map-wire" aria-hidden="true"><i></i><Icon name="arrowRight" size="xs" /></div>
        <div class="map-hub">
          <img src="/gongfei-plane.svg" alt="" width="56" height="56" />
          <strong>{{ t('homeIntro.map.gateway', { name: siteName }) }}</strong>
          <code>{{ apiEndpoint }}</code>
          <span>{{ t('homeIntro.map.protocols') }}</span>
        </div>
        <div class="map-wire" aria-hidden="true"><i></i><Icon name="arrowRight" size="xs" /></div>
        <div class="map-column map-targets">
          <span class="map-label">{{ t('homeIntro.map.targets') }}</span>
          <span class="map-target map-codex"><Icon name="terminal" size="sm" />Codex<em>{{ t('homeIntro.map.codexHint') }}</em></span>
          <span class="map-tools"><span v-for="tool in mapTools" :key="tool">{{ tool }}</span></span>
          <span v-if="webEnabled" class="map-target map-web"><Icon name="chat" size="sm" />{{ t('nav.playground') }}<em>{{ t('homeIntro.map.webHint') }}</em></span>
        </div>
      </div>
      <ul class="map-facts">
        <li v-for="fact in [1, 2, 3]" :key="fact"><span>0{{ fact }}</span>{{ t(`homeIntro.map.fact${fact}`) }}</li>
      </ul>
    </section>

    <section v-if="isHome" id="ways" aria-labelledby="ways-title" class="readiness">
      <div class="readiness-heading"><p class="eyebrow">{{ t('homeIntro.ways.eyebrow') }}</p><h2 id="ways-title">{{ t('homeIntro.ways.title') }}</h2><p>{{ t('homeIntro.ways.note') }}</p><img src="/gongfei-plane.svg" alt="" width="104" height="104" class="readiness-plane" /></div>
      <div class="readiness-body">
        <div class="readiness-list">
          <div v-for="(way, index) in ways" :key="way.id" :data-testid="`home-way-${way.id}`" :class="{ 'way-featured': way.id === 'client' }">
            <span class="requirement-number">0{{ index + 1 }}</span>
            <h3>{{ t(`homeIntro.ways.${way.id}.title`) }}<span v-if="way.id === 'client'" class="way-badge">{{ t('homeIntro.ways.client.badge') }}</span></h3>
            <p>{{ t(`homeIntro.ways.${way.id}.p1`) }}</p>
            <p>{{ t(`homeIntro.ways.${way.id}.p2`) }}</p>
            <div class="way-actions">
              <router-link :to="way.to" :class="way.id === 'client' ? 'studio-button way-button' : 'way-link'">{{ t(`homeIntro.ways.${way.id}.action`) }}<Icon :name="way.id === 'client' ? 'download' : 'arrowRight'" size="xs" aria-hidden="true" /></router-link>
              <router-link v-if="way.id === 'client'" :to="{ path: '/download', hash: '#guide-step-1' }" class="way-link">{{ t('homeIntro.ways.client.guide') }}<Icon name="arrowRight" size="xs" aria-hidden="true" /></router-link>
            </div>
          </div>
        </div>
      </div>
    </section>

    <section v-else id="requirements" aria-labelledby="requirements-title" class="readiness">
      <div class="readiness-heading"><p class="eyebrow">{{ t('clientIntroduction.requirementsEyebrow') }}</p><h2 id="requirements-title">{{ t('clientIntroduction.requirementsTitle') }}</h2><p>{{ t('clientIntroduction.readinessNote') }}</p><img src="/gongfei-plane.svg" alt="" width="104" height="104" class="readiness-plane" /></div>
      <div class="readiness-body">
        <div class="readiness-list">
          <div><span class="requirement-number">01</span><h3>{{ t('clientIntroduction.computerTitle') }}</h3><p>{{ t('clientIntroduction.windows') }}</p><p v-if="macAvailable">{{ t('clientIntroduction.mac') }}</p><p>{{ t('clientIntroduction.desktopOnly') }}</p></div>
          <div><span class="requirement-number">02</span><h3>{{ t('clientIntroduction.networkTitle') }}</h3><p>{{ t('clientIntroduction.network') }}</p><p>{{ t('clientIntroduction.hardware') }}</p></div>
          <div><span class="requirement-number">03</span><h3>{{ t('clientIntroduction.accountTitle') }}</h3><p>{{ t('clientIntroduction.account') }}</p></div>
          <div><span class="requirement-number">04</span><h3>{{ t('clientIntroduction.balanceTitle') }}</h3><p>{{ rechargeEnabled ? t('clientIntroduction.balance') : t('clientIntroduction.balanceNoRecharge') }}</p></div>
        </div>
        <p class="software-note"><Icon name="cube" size="md" aria-hidden="true" /><span>{{ t('clientIntroduction.creativeRequirements') }}</span></p>
        <details><summary>{{ t('clientIntroduction.checkComputer') }}</summary><p>{{ t('clientIntroduction.checkWindows') }}</p><p v-if="macAvailable">{{ t('clientIntroduction.checkMac') }}</p></details>
      </div>
    </section>

    <section v-if="isHome" id="codex" aria-labelledby="codex-title" class="start-ribbon">
      <div class="start-heading"><div><p class="eyebrow">{{ t('homeIntro.codex.eyebrow') }}</p><h2 id="codex-title">{{ clientEnabled ? t('homeIntro.codex.clientTitle') : t('homeIntro.codex.title') }}</h2></div><span aria-hidden="true">↗</span></div>
      <ol><li v-for="step in [1, 2, 3]" :key="step"><span class="step-index">{{ step }}</span><div><h3>{{ t(codexStepKey(step, 'Title')) }}</h3><p>{{ t(codexStepKey(step)) }}</p></div></li></ol>
      <div v-if="clientEnabled" class="ribbon-actions">
        <router-link to="/download" class="studio-button start-button">{{ t('homeIntro.codex.clientAction') }}<Icon name="download" size="sm" aria-hidden="true" /></router-link>
        <router-link :to="{ path: '/download', hash: '#guide-step-1' }" class="studio-link">{{ t('homeIntro.codex.guide') }}<Icon name="arrowRight" size="xs" aria-hidden="true" /></router-link>
      </div>
      <component :is="clientEnabled ? 'details' : 'div'" class="manual-config" data-testid="codex-manual-config">
        <summary v-if="clientEnabled">{{ t('homeIntro.codex.manualToggle') }}</summary>
      <div class="config-paper">
        <div class="window-bar"><span class="window-dots"><i></i><i></i><i></i></span><span>~/.codex/config.toml</span><Icon name="terminal" size="xs" /></div>
        <pre><code><span class="toml-comment"># {{ t('homeIntro.codex.comment') }}</span>
model_provider = <span class="toml-string">"OpenAI"</span>
model = <span class="toml-string">"gpt-5.5"</span>

[model_providers.OpenAI]
base_url = <span class="toml-string" data-testid="codex-base-url">"{{ apiEndpoint }}"</span>
wire_api = <span class="toml-string">"responses"</span></code></pre>
      </div>
      <div class="ribbon-actions">
        <router-link :to="authedPath('/keys')" :class="clientEnabled ? 'studio-link' : 'studio-button start-button'">{{ t('homeIntro.codex.action') }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link>
      </div>
      </component>
    </section>

    <section v-else id="quick-start" aria-labelledby="quick-start-title" class="start-ribbon">
      <div class="start-heading"><div><p class="eyebrow">{{ t('clientIntroduction.stepsEyebrow') }}</p><h2 id="quick-start-title">{{ t('clientIntroduction.stepsTitle') }}</h2></div><span aria-hidden="true">↗</span></div>
      <ol><li v-for="step in [1, 2, 3]" :key="step"><span class="step-index">{{ step }}</span><div><h3>{{ t(`clientIntroduction.step${step}Title`) }}</h3><p>{{ t(`clientIntroduction.step${step}`) }}</p></div></li></ol>
      <router-link v-if="!downloadPage" to="/download" class="studio-button start-button">{{ t('clientIntroduction.guideLink') }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link>
    </section>
  </div>
</template>

<style scoped>
.client-introduction { --ink: #172b39; --muted: #647077; --paper: #fff; --line: #dfe4e2; --blue: #2864dc; --soft: #eef3fb; color: var(--ink); }
.studio-hero { display: grid; grid-template-columns: .94fr 1.1fr; align-items: center; gap: 36px; padding: 22px 0 44px; }
.hero-copy { position: relative; z-index: 2; padding: 10px 0; }
.eyebrow { display: flex; align-items: center; gap: 9px; color: var(--blue); font-size: 13px; font-weight: 650; letter-spacing: .06em; }
.brand-spark { font-size: 25px; line-height: 1; }
h1 { margin-top: 24px; font-size: clamp(38px, 4.5vw, 64px); font-weight: 750; line-height: 1.25; letter-spacing: -.055em; }
h1 > span { display: block; color: var(--blue); white-space: pre-line; }
.hero-description { max-width: 480px; margin-top: 25px; color: var(--muted); font-size: 16px; line-height: 1.95; }
.hero-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 20px; margin-top: 30px; }
.studio-button { display: inline-flex; min-height: 50px; align-items: center; justify-content: center; gap: 20px; padding: 13px 22px; border-radius: 14px; background: var(--blue); color: white; font-size: 15px; font-weight: 600; box-shadow: 0 6px 16px #2864dc20; transition: background .2s, transform .2s; }
.studio-button:hover { background: #174bba; transform: translateY(-2px); }
.studio-button-outline { background: var(--paper); color: var(--blue); border: 1.5px solid var(--blue); box-shadow: 0 6px 16px -10px #2864dc55; }
.studio-button-outline:hover { background: var(--soft); color: var(--blue); }
.dark .studio-button-outline { background: transparent; color: var(--blue); }
/* 免注册试用按钮：和另外两个按钮一样大，浅蓝底区别于实心/描边两档 */
/* 首页三个按钮并排时收紧间距和内边距，保证一行放得下 */
.hero-actions:has(.studio-button-soft) { gap: 12px; }
.hero-actions:has(.studio-button-soft) .studio-button { padding: 13px 18px; gap: 12px; }
.studio-button-soft { gap: 10px; background: var(--soft); color: var(--blue); box-shadow: inset 0 0 0 1px #c9d8f5; }
.studio-button-soft:hover { background: #e2ebfa; color: var(--blue); }
.trial-badge { padding: 1px 8px; border-radius: 999px; background: var(--blue); color: #fff; font-size: 11px; font-weight: 600; }
.dark .studio-button-soft { background: var(--soft); color: var(--blue); box-shadow: inset 0 0 0 1px #2c4468; }
.dark .trial-badge { color: #0f1720; }.dark .studio-button-outline:hover { background: var(--soft); }
.studio-link { display: inline-flex; align-items: center; gap: 6px; min-height: 44px; color: var(--ink); font-size: 13px; }
.studio-link:hover { color: var(--blue); }
.beginner-note { display: flex; align-items: flex-start; gap: 7px; margin-top: 19px; color: var(--muted); font-size: 12px; line-height: 1.8; }
.beginner-note svg { flex-shrink: 0; margin-top: 3px; }
.studio-canvas { position: relative; min-width: 0; height: 490px; isolation: isolate; background-image: radial-gradient(#c4cecd88 .7px, transparent .7px); background-size: 18px 18px; border-radius: 42% 38% 36% 35%; }
.canvas-orbit { position: absolute; z-index: -1; inset: 10% 0 8% 6%; border: 1px solid #d9e1db; border-radius: 50%; transform: rotate(-18deg); background: radial-gradient(ellipse at 70% 35%, #e4ede6b0, #f3f3ea55 65%, transparent 70%); }
.canvas-caption { position: absolute; left: 8%; top: 8px; color: var(--muted); font-size: 10px; letter-spacing: .15em; }
.canvas-trail { position: absolute; inset: 0; width: 100%; height: 100%; color: #8cacb3; z-index: -1; }
.work-window { position: absolute; top: 85px; left: 5%; width: 82%; overflow: hidden; border: 1px solid #ffffff; border-radius: 17px; background: var(--paper); box-shadow: 0 26px 56px -24px #253b4b55, 0 3px 9px #263b4a0b; transform: rotate(-3deg); }
.window-bar { display: flex; height: 36px; align-items: center; justify-content: space-between; gap: 8px; padding: 0 12px; color: #849098; font: 9px ui-monospace, monospace; }
.window-dots { display: flex; gap: 4px; }.window-dots i { width: 5px; height: 5px; border-radius: 50%; background: #d7ddd9; }.window-dots i:first-child { background: #e5a597; }
.work-preview { height: 277px; position: relative; overflow: hidden; margin: 0 7px; border-radius: 7px; background: #edf1ec; }
.work-preview > img { width: 100%; height: 100%; object-fit: cover; }
.preview-video > img { height: 201px; object-position: center; }
.frame-label { position: absolute; top: 20px; left: 20px; color: #fff5e6; font: 8px ui-monospace, monospace; letter-spacing: .18em; }
.frame-time { position: absolute; top: 173px; right: 12px; color: white; font: 8px ui-monospace, monospace; }
.timeline { height: 76px; padding: 7px 12px; background: #f7f9fb; position: relative; }
.time-ruler { display: flex; justify-content: space-between; margin-bottom: 5px; color: #9ba6ad; font: 7px ui-monospace, monospace; }
.clip-track { display: flex; gap: 3px; height: 22px; }.clip-track i { flex: 1; background: #88b9b1 url('/creative-studio/video-landscape.svg') center / cover; border-radius: 3px; }.clip-track i:nth-child(2) { flex: 1.5; filter: hue-rotate(15deg); }.clip-track i:nth-child(3) { filter: hue-rotate(-12deg); }
.audio-track { height: 10px; margin-top: 5px; border-radius: 3px; background: repeating-linear-gradient(90deg, #a4b4f0 0 2px, #dfe5fa 2px 4px); }
.playhead { position: absolute; width: 1px; top: 3px; bottom: 4px; left: 42%; background: #d7664f; }.playhead:before { content: ''; position: absolute; top: 0; left: -3px; width: 7px; height: 6px; background: #d7664f; clip-path: polygon(0 0,100% 0,50% 100%); }
.window-footer { display: flex; align-items: center; gap: 6px; min-height: 30px; padding: 7px 14px; color: var(--muted); font-size: 9px; }.window-footer > span:last-child { margin-left: auto; }
.tiny-dot { width: 5px; height: 5px; border-radius: 50%; background: #5eaa83; }
.floating-piece { position: absolute; overflow: hidden; box-shadow: 0 14px 36px -18px #24344550; }
.model-piece { top: 17px; right: 0; width: 30%; background: #edf3fb; border: 5px solid var(--paper); border-radius: 13px; transform: rotate(7deg); }
.model-piece > span:first-child { display: flex; justify-content: space-between; align-items: center; padding: 7px; color: #596d83; font: 6px ui-monospace, monospace; letter-spacing: .04em; }
.model-piece img { width: 100%; height: auto; aspect-ratio: 1; object-fit: cover; }.piece-tag { position: absolute; bottom: 9px; left: 9px; padding: 3px 6px; background: #ffffffde; color: #294264; border-radius: 5px; font-size: 8px; }
.poster-piece { width: 22%; left: 0; bottom: 9px; border: 5px solid var(--paper); border-radius: 10px; transform: rotate(-10deg); }.poster-piece img { width: 100%; height: auto; }
.idea-note { position: absolute; display: flex; align-items: center; gap: 9px; right: 2%; bottom: 27px; max-width: 76%; padding: 12px 15px; background: #fffcf0; border: 1px solid #ebe5cf; border-radius: 12px 12px 12px 3px; color: #716749; box-shadow: 0 8px 24px #6a633910; font-size: 12px; transform: rotate(3deg); }.idea-note svg { color: #b3923e; flex-shrink: 0; }.idea-note img { width: 28px; height: 28px; }
.canvas-star { position: absolute; top: 17px; left: 41%; color: #d28c62; font-size: 34px; transform: rotate(15deg); }
.preview-model > img { object-fit: contain; }.model-axis { position: absolute; left: 12px; bottom: 12px; color: #54708b; font: 10px ui-monospace, monospace; }.art-caption { position: absolute; top: 12px; left: 12px; color: #647a92; font: 7px ui-monospace, monospace; letter-spacing: .12em; }
.preview-image { background: #e9dfc9; }.preview-image > img { object-fit: contain; padding: 10px; }.color-swatches { position: absolute; bottom: 20px; right: 14px; display: grid; gap: 5px; }.color-swatches i { width: 13px; height: 13px; background: #e97f48; border: 2px solid #fff; border-radius: 50%; }.color-swatches i:nth-child(2) { background: #f6d66c; }.color-swatches i:nth-child(3) { background: #203f43; }.color-swatches i:nth-child(4) { background: #fffcdf; }
.data-art { height: 100%; padding: 24px; background: #f6f9f4; color: #344c44; }.art-kicker { display: block; font-size: 7px; letter-spacing: .16em; opacity: .65; }.data-art > strong { display: block; margin-top: 7px; font-size: 21px; }.chart-art { display: flex; align-items: flex-end; gap: 13px; height: 107px; border-bottom: 1px solid #ced8cb; margin: 14px 0; padding: 0 10px; }.chart-art i { width: 15%; height: var(--bar); background: #a9c3aa; border-radius: 4px 4px 0 0; }.chart-art i:last-child { background: #315f57; }.sheet-art { display: grid; grid-template-columns: repeat(6,1fr); gap: 4px; }.sheet-art span { height: 8px; border-radius: 2px; background: #dfe6d9; }
.web-art { position: relative; height: 100%; padding: 21px; background: #eedac2; color: #354538; overflow: hidden; }.mini-nav { display: flex; justify-content: space-between; align-items: center; padding-bottom: 29px; }.mini-nav strong { font-size: 11px; }.mini-nav span { font-size: 6px; }.web-headline { position: relative; display: block; z-index: 1; margin-top: 13px; font: 32px/1.02 Georgia, serif; letter-spacing: -.04em; }.mini-cta { position: relative; z-index: 1; display: inline-block; margin-top: 20px; padding: 8px 10px; font-size: 7px; background: #344d3f; color: #fff; border-radius: 12px; }.web-sun { position: absolute; width: 144px; height: 144px; right: -24px; bottom: -32px; border: 18px solid #c76643; border-radius: 50%; box-shadow: 0 0 0 15px #dd9569, 0 0 0 28px #e8b186; }
.writing-art { position: relative; height: 100%; padding: 32px; background: #fbf8ef; color: #5e5749; }.writing-art strong { display: block; margin-top: 17px; font-size: 24px; }.writing-art p { max-width: 80%; margin-top: 13px; font-size: 12px; line-height: 1.9; }.writing-line { display: block; height: 5px; width: 70%; margin-top: 15px; background: #e8e3d5; }.writing-line.short { width: 45%; margin-top: 8px; }.writing-art > svg { position: absolute; bottom: 23px; right: 28px; color: #b3905b; transform: rotate(-12deg); }
.possibilities { margin-top: 10px; }
.section-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 15px; margin-bottom: 21px; }.section-heading h2 { font-size: 20px; font-weight: 650; letter-spacing: -.025em; }.section-heading > p { font-size: 12px; color: var(--muted); }
.scene-options { display: grid; grid-template-columns: repeat(6, 1fr); gap: 10px; }
.scene-options button { display: flex; align-items: center; justify-content: center; gap: 10px; min-height: 75px; border: 1px solid var(--line); border-radius: 17px; background: var(--paper); font-size: 14px; font-weight: 600; color: var(--ink); transition: background .2s, border-color .2s, transform .2s; }
.scene-options button:hover { border-color: #9cafc0; transform: translateY(-3px); }.scene-options button.selected { border-color: var(--blue); color: var(--blue); background: var(--soft); }.scene-icon { color: #86978c; }.scene-icon-model { color: #6587cc; }.scene-icon-image { color: #c58e62; }.scene-icon-data { color: #6b967d; }.scene-icon-web { color: #8891ae; }.scene-icon-write { color: #bb9471; }.scene-arrow { display: none; }.scene-options button.selected .scene-arrow { display: block; }
.scene-brief { display: grid; grid-template-columns: 1fr 1fr; align-items: center; gap: 40px; margin-top: 20px; min-height: 137px; padding: 24px 29px; border-radius: 17px; background: var(--soft); }
.prompt-example { display: flex; align-items: flex-start; gap: 16px; }.quote-mark { color: #9db9dc; font: 66px/1 Georgia, serif; }.small-label { color: var(--muted); font-size: 11px; }.prompt-example p { margin-top: 7px; font-size: 17px; font-weight: 550; line-height: 1.65; letter-spacing: -.015em; }.scene-outcome p { font-size: 14px; line-height: 1.8; }.scene-outcome > span { display: block; color: var(--muted); font-size: 12px; line-height: 1.8; margin-top: 7px; }.purpose-note { color: var(--muted); font-size: 12px; line-height: 1.8; margin-top: 16px; }
.readiness { display: grid; grid-template-columns: .72fr 1.8fr; gap: 64px; padding-top: 72px; margin-top: 70px; border-top: 1px solid var(--line); scroll-margin-top: 30px; }.readiness-heading h2 { margin-top: 18px; max-width: 230px; font-size: 34px; line-height: 1.5; font-weight: 650; letter-spacing: -.04em; }.readiness-heading > p:last-of-type { margin-top: 14px; font-size: 14px; line-height: 1.9; color: var(--muted); }.readiness-plane { width: 80px; height: 80px; margin-top: 32px; transform: rotate(-12deg); opacity: .75; }
.readiness-list { display: grid; grid-template-columns: 1fr 1fr; gap: 29px 32px; }.readiness-list > div { border-top: 1px solid var(--line); padding-top: 15px; position: relative; }.requirement-number { position: absolute; right: 0; top: 19px; font: 10px ui-monospace, monospace; color: #8c9d99; }.readiness-list h3 { font-size: 15px; font-weight: 650; margin-bottom: 11px; padding-right: 25px; }.readiness-list p { margin-top: 5px; color: var(--muted); font-size: 13px; line-height: 1.9; }.software-note { display: flex; align-items: flex-start; gap: 11px; margin-top: 25px; padding: 15px 18px; background: #eeeee4; border-radius: 12px; color: #686951; font-size: 12px; line-height: 1.9; }.software-note svg { flex-shrink: 0; margin-top: 3px; }
.readiness details { margin-top: 18px; font-size: 12px; line-height: 1.9; }.readiness summary { cursor: pointer; color: var(--blue); }.readiness details p { margin-top: 9px; color: var(--muted); }
.start-ribbon { position: relative; margin-top: 64px; padding: 35px 40px; border-radius: 24px; background: #e9efef; scroll-margin-top: 30px; }.start-heading { display: flex; justify-content: space-between; gap: 10px; }.start-heading .eyebrow { color: #5f7a7b; font-size: 11px; }.start-heading h2 { margin-top: 10px; font-size: 28px; font-weight: 650; letter-spacing: -.03em; }.start-heading > span { font-size: 58px; color: #9bb5b2; line-height: 1; }.start-ribbon ol { display: grid; grid-template-columns: repeat(3, 1fr); gap: 36px; margin-top: 28px; }.start-ribbon li { display: flex; gap: 13px; }.step-index { flex-shrink: 0; display: grid; place-items: center; width: 27px; height: 27px; border: 1px solid #b8cccb; border-radius: 50%; font-size: 12px; color: #617e7a; }.start-ribbon h3 { font-size: 15px; font-weight: 600; }.start-ribbon li p { margin-top: 9px; font-size: 12px; line-height: 1.9; color: var(--muted); }.start-button { margin-top: 26px; min-height: 44px; background: #294b49; font-size: 13px; box-shadow: none; }.start-button:hover { background: #193937; }
/* home 变体：沿用 readiness / start-ribbon 的版式，只补几处小样式 */
.gateway-map { margin-top: 70px; padding-top: 64px; border-top: 1px solid var(--line); }
.map-board { position: relative; display: grid; grid-template-columns: 1fr 70px 1.1fr 70px 1.35fr; align-items: center; gap: 8px; padding: 36px 40px; border-radius: 28px; background-color: var(--paper); background-image: radial-gradient(#c4cecd88 .7px, transparent .7px); background-size: 18px 18px; border: 1px solid var(--line); }
.map-column { display: flex; flex-direction: column; gap: 9px; }
.map-label { color: var(--muted); font-size: 10px; letter-spacing: .15em; }
.map-ticket { width: fit-content; min-width: 112px; padding: 10px 14px; border: 1px solid var(--line); border-radius: 12px; background: var(--paper); font-weight: 650; font-size: 14px; box-shadow: 0 10px 22px -16px #24344566; transform: rotate(var(--tilt)); }
.map-more { color: var(--muted); font-size: 12px; padding-left: 4px; }
.map-wire { position: relative; display: flex; align-items: center; justify-content: flex-end; color: #8cacb3; }
.map-wire i { position: absolute; left: 0; right: 10px; top: 50%; border-top: 1.5px dashed #8cacb3; }
.map-hub { display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 26px 18px; border-radius: 50% 46% 48% 44% / 40% 42% 40% 44%; background: radial-gradient(ellipse at 50% 35%, var(--soft), var(--paper) 72%); border: 1px solid #d9e1db; text-align: center; }
.map-hub img { transform: rotate(-10deg); }
.map-hub strong { font-size: 17px; letter-spacing: -.02em; }
.map-hub code { max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 3px 9px; border-radius: 7px; background: var(--paper); color: var(--blue); font: 11px ui-monospace, Consolas, monospace; }
.map-hub > span { color: var(--muted); font-size: 11px; }
.map-target { display: flex; align-items: center; gap: 8px; padding: 11px 14px; border-radius: 13px; font-weight: 650; font-size: 14px; }
.map-target em { margin-left: auto; font-style: normal; font-weight: 500; font-size: 11px; opacity: .75; }
.map-codex { background: var(--soft); color: var(--blue); border: 1px solid #c9d8f5; }
.map-web { background: #fffcf0; color: #716749; border: 1px solid #ebe5cf; transform: rotate(-1deg); }
.map-tools { display: flex; flex-wrap: wrap; gap: 6px; }
.map-tools span { padding: 5px 10px; border: 1px solid var(--line); border-radius: 999px; background: var(--paper); font-size: 12px; color: var(--ink); }
.map-facts { display: grid; grid-template-columns: repeat(3, 1fr); gap: 20px; margin-top: 22px; }
.map-facts li { display: flex; align-items: baseline; gap: 10px; padding-top: 12px; border-top: 1px solid var(--line); font-size: 14px; font-weight: 600; }
.map-facts li span { color: #8c9d99; font: 10px ui-monospace, monospace; }
.dark .map-board { background-image: radial-gradient(#66808e40 .7px, transparent .7px); }.dark .map-hub { border-color: #2b4350; }.dark .map-codex { border-color: #2c4468; }.dark .map-web { background: #343123; color: #ded4b2; border-color: #57523a; }
@media (max-width: 1100px) { .map-board { grid-template-columns: 1fr 40px 1fr 40px 1.2fr; padding: 28px 24px; } }
@media (max-width: 767px) {
  .gateway-map { margin-top: 43px; padding-top: 36px; }
  .map-board { grid-template-columns: 1fr; gap: 14px; padding: 24px 20px; }
  .map-column { flex-direction: row; flex-wrap: wrap; align-items: center; }
  .map-label { width: 100%; }
  .map-ticket { min-width: 0; }
  .map-wire { justify-content: center; height: 26px; transform: rotate(90deg); }
  .map-wire i { left: 10px; }
  .map-targets { flex-direction: column; align-items: stretch; }
  .map-facts { grid-template-columns: 1fr; gap: 10px; }
}
.readiness-heading h2 { white-space: pre-line; }
.way-featured { grid-column: 1 / -1; padding: 22px 24px 24px !important; border-top: 0 !important; border-radius: 18px; background: var(--soft); }
.way-featured .requirement-number { top: 26px; right: 24px; }
.way-featured h3 { font-size: 18px; }
.way-featured p { font-size: 14px; color: var(--ink); opacity: .8; }
.way-badge { display: inline-block; margin-left: 10px; padding: 2px 9px; border-radius: 999px; background: var(--blue); color: #fff; font-size: 11px; font-weight: 600; vertical-align: 2px; }
.way-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 20px; }
.way-button { min-height: 42px; margin-top: 16px; padding: 10px 18px; gap: 12px; font-size: 14px; }
.manual-config { margin-top: 8px; }
details.manual-config { margin-top: 22px; }
details.manual-config > summary { cursor: pointer; color: #5f7a7b; font-size: 13px; }
details.manual-config .config-paper { margin-top: 14px; }
.way-link { display: inline-flex; align-items: center; gap: 5px; margin-top: 12px; color: var(--blue); font-size: 13px; font-weight: 600; transition: gap .2s; }.way-link:hover { gap: 8px; }
.config-paper { margin-top: 28px; overflow: hidden; border: 1px solid #ffffff; border-radius: 15px; background: var(--paper); box-shadow: 0 18px 40px -26px #253b4b55; }
.config-paper .window-bar { border-bottom: 1px solid #eef1ee; }
.config-paper pre { overflow-x: auto; padding: 16px 20px 18px; color: var(--ink); font: 12.5px/1.85 ui-monospace, "Cascadia Code", Consolas, monospace; }
.toml-comment { color: #8c9d99; }.toml-string { color: #2f7a68; }
.ribbon-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 20px; margin-top: 26px; }.ribbon-actions .start-button { margin-top: 0; }
.dark .config-paper { border-color: #3a4c59; }.dark .config-paper .window-bar { border-color: #2a3a45; }.dark .toml-string { color: #7fd1bb; }
a:focus-visible, button:focus-visible, summary:focus-visible { outline: 3px solid var(--blue); outline-offset: 5px; }
.dark .client-introduction { --ink: #e5ebef; --muted: #a7b6c0; --paper: #17232e; --line: #30414c; --blue: #91b6ff; --soft: #1c2c40; }
.dark .studio-button { background: #376bdd; color: white; }.dark .canvas-orbit { border-color: #2b4350; background: radial-gradient(ellipse at 70% 35%, #173d4770, transparent 70%); }.dark .studio-canvas { background-image: radial-gradient(#66808e40 .7px, transparent .7px); }.dark .work-window { border-color: #3a4c59; }.dark .canvas-trail { color: #476478; }.dark .idea-note { background: #343123; color: #ded4b2; border-color: #57523a; }.dark .software-note { background: #2b3029; color: #c4c6ab; }.dark .start-ribbon { background: #192f34; }.dark .start-heading .eyebrow { color: #a2c2c3; }.dark .step-index { border-color: #4d6d70; color: #b4cfcd; }.dark .start-button { background: #335b59; }
@media (min-width: 1100px) { .studio-canvas { animation: canvas-arrive .75s ease-out both; }.hero-copy { animation: copy-arrive .55s ease-out both; } }
@keyframes canvas-arrive { from { opacity: 0; transform: translateY(18px) rotate(1deg); } to { opacity: 1; transform: translateY(0) rotate(0); } }
@keyframes copy-arrive { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: translateY(0); } }
@media (max-width: 1100px) { .studio-hero { gap: 15px; }.studio-canvas { height: 450px; }.work-window { top: 70px; width: 86%; }.work-preview { height: 249px; }.preview-video > img { height: 173px; }.frame-time { top: 145px; }.scene-options button { flex-direction: column; gap: 8px; min-height: 83px; font-size: 12px; }.scene-options button.selected .scene-arrow { display: none; }.readiness { gap: 35px; }.hero-actions { gap: 12px; }.studio-link { font-size: 12px; }.chart-art { height: 86px; } }
@media (max-width: 767px) { .canvas-orbit { inset: 16% 8% 12% 12%; }.studio-hero { grid-template-columns: 1fr; gap: 15px; padding: 2px 0 25px; }.hero-copy { padding: 0; }h1 { font-size: clamp(35px, 7vw, 52px); margin-top: 17px; }.hero-description { max-width: 560px; margin-top: 18px; font-size: 15px; line-height: 1.9; }.hero-actions { margin-top: 23px; }.beginner-note { margin-top: 14px; }.studio-canvas { width: min(100%, 530px); height: 440px; margin: 10px auto 0; }.work-window { top: 76px; }.model-piece { top: 7px; }.poster-piece { bottom: 0; }.idea-note { bottom: 19px; font-size: 11px; }.possibilities { margin-top: 0; }.section-heading { display: block; }.section-heading h2 { font-size: 21px; }.section-heading > p { margin-top: 7px; }.scene-options { grid-template-columns: repeat(3, 1fr); gap: 8px; }.scene-options button { min-height: 76px; }.scene-brief { grid-template-columns: 1fr; gap: 17px; padding: 21px; }.prompt-example { gap: 9px; }.prompt-example p { font-size: 16px; }.scene-outcome { padding-left: 31px; }.scene-outcome p { font-size: 13px; }.quote-mark { font-size: 50px; }.readiness { margin-top: 43px; padding-top: 36px; grid-template-columns: 1fr; gap: 26px; }.readiness-heading { position: relative; padding-right: 65px; }.readiness-heading h2 { max-width: none; font-size: 28px; margin-top: 11px; }.readiness-heading > p:last-of-type { font-size: 13px; }.readiness-plane { position: absolute; width: 58px; height: 58px; right: 0; top: 7px; margin: 0; }.readiness-list { gap: 25px 20px; }.start-ribbon { margin-top: 40px; padding: 27px 23px; }.start-heading h2 { font-size: 24px; }.start-ribbon ol { grid-template-columns: 1fr; gap: 23px; }.start-heading > span { font-size: 42px; } }
@media (max-width: 420px) { .eyebrow { font-size: 12px; }.studio-button { padding: 12px 17px; font-size: 14px; gap: 13px; }.hero-actions { gap: 7px 13px; }.studio-canvas { height: 375px; }.work-window { top: 63px; }.work-preview { height: 219px; }.preview-video > img { height: 153px; }.frame-time { top: 125px; }.timeline { height: 66px; }.window-bar { height: 29px; font-size: 7px; }.window-footer { font-size: 8px; min-height: 25px; }.model-piece { width: 31%; }.idea-note { right: 0; bottom: 5px; padding: 8px 10px; font-size: 10px; }.poster-piece { width: 22%; bottom: 0; }.canvas-star { font-size: 25px; }.canvas-caption { font-size: 8px; }.readiness-list { grid-template-columns: 1fr; gap: 22px; }.readiness-list p { font-size: 14px; }.web-headline { font-size: 24px; }.data-art { padding: 19px; }.data-art > strong { font-size: 17px; }.chart-art { height: 73px; margin: 10px 0; }.writing-art { padding: 24px; }.writing-art strong { font-size: 20px; margin-top: 12px; }.writing-art p { font-size: 10px; margin-top: 9px; }.writing-line { margin-top: 10px; }.frame-label { font-size: 6px; left: 12px; }.mini-nav { padding-bottom: 20px; } }
@media (prefers-reduced-motion: reduce) { *, *::before, *::after { animation: none !important; transition: none !important; }.studio-button:hover, .scene-options button:hover { transform: none; } }
</style>
