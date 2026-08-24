<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'

type GuideImage = {
  src: string
  alt: string
}

type GuideStep = {
  number: number
  title: string
  intro: string
  actions: string[]
  images?: GuideImage[]
  note?: string
}

const appStore = useAppStore()
const authStore = useAuthStore()
const isDark = ref(document.documentElement.classList.contains('dark'))

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || '共飞 AI')
const siteLogo = computed(() => appStore.siteLogo || '/gongfei-plane.svg')
const dashboardPath = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))

// 下载地址由管理员在后台填写。后端 normalizeExternalHTTPURL 已挡掉伪协议，
// 这里再校验一次，避免绕过后端直接改库的值进到 href。
function safeExternalUrl(value: string | undefined): string {
  const url = (value || '').trim()
  return /^https?:\/\//i.test(url) ? url : ''
}
const directDownloadUrl = computed(() => safeExternalUrl(appStore.cachedPublicSettings?.client_download_direct_url))
const netdiskDownloadUrl = computed(() => safeExternalUrl(appStore.cachedPublicSettings?.client_download_netdisk_url))
const hasAnyDownloadUrl = computed(() => Boolean(directDownloadUrl.value || netdiskDownloadUrl.value))

const guideSteps: GuideStep[] = [
  {
    number: 1,
    title: '下载、解压并打开客户端',
    intro: '先把客户端压缩包下载到本地，再解压到一个单独的文件夹。',
    actions: [
      '下载客户端压缩包。',
      '右键压缩包，选择解压到一个文件夹。',
      '进入解压后的目录，双击客户端程序打开。'
    ],
    images: [{ src: '/client-guide/g1.png', alt: '双击打开共飞 AI 客户端' }]
  },
  {
    number: 2,
    title: '注册',
    intro: '如果你还没有账号，请先完成注册。',
    actions: [
      '打开客户端，进入登录页面。',
      '点击“还没有账号？注册”。',
      '填写注册邮箱和密码，点击“发送验证码”。',
      '打开你的注册邮箱，查收验证码邮件。',
      '把邮件里的验证码填回客户端，完成其他验证后点击“注册”提交。'
    ],
    images: [
      { src: '/client-guide/g2.png', alt: '登录页中的注册入口' },
      { src: '/client-guide/g4.png', alt: '填写邮箱和密码并发送验证码' },
      { src: '/client-guide/g5.png', alt: '输入邮箱验证码并完成注册' }
    ],
    note: '如果没有看到注册入口，说明当前服务器暂未开放注册。收不到验证码时，请先检查垃圾邮件、广告邮件文件夹，并确认邮箱地址填写正确。'
  },
  {
    number: 3,
    title: '登录',
    intro: '注册完成后，回到登录页使用刚注册的账号登录。',
    actions: [
      '输入邮箱和密码，点击“登录”。',
      '如果页面出现协议勾选或二次验证，按提示完成。',
      '登录成功后进入客户端主界面。'
    ],
    images: [
      { src: '/client-guide/g6.png', alt: '客户端登录表单' },
      { src: '/client-guide/g7.png', alt: '登录成功后的主界面' }
    ]
  },
  {
    number: 4,
    title: '安装 ChatGPT',
    intro: '如果电脑上还没有 ChatGPT，请先从客户端内安装。',
    actions: [
      '在主界面点击“安装 ChatGPT”。',
      '等待客户端下载 ChatGPT 安装包。',
      '下载完成后会自动弹出 ChatGPT 安装程序。',
      '在安装程序中点击“Install”。',
      '稍做等待，安装完成会自动拉起 ChatGPT 客户端。'
    ],
    images: [
      { src: '/client-guide/g8.png', alt: '主界面中的安装 ChatGPT 按钮' },
      { src: '/client-guide/g9.png', alt: 'ChatGPT 安装包下载完成' },
      { src: '/client-guide/g10.png', alt: 'ChatGPT 安装程序中的 Install 按钮' },
      { src: '/client-guide/g11.png', alt: 'ChatGPT 安装完成并自动启动' }
    ],
    note: '如果你已经安装过 ChatGPT，按钮通常会显示为“启动 ChatGPT”。'
  },
  {
    number: 5,
    title: '切换分组',
    intro: '分组可以理解为当前账号使用的线路或策略。',
    actions: [
      '在主界面找到“当前分组”或“切换分组”。',
      '点击下拉框，选择要使用的分组。',
      '等待切换完成，确认当前分组已经更新。'
    ],
    images: [
      { src: '/client-guide/g12.png', alt: '分组切换区域' },
      { src: '/client-guide/g13.png', alt: '分组下拉选项' }
    ],
    note: '切换后，倍率、余额折算和可用模型可能会变化。'
  },
  {
    number: 6,
    title: '充值',
    intro: '余额不足时，可以直接在客户端内充值。',
    actions: [
      '点击“去充值”。',
      '选择充值金额，或手动输入金额。',
      '选择支付方式并扫码完成支付。',
      '支付成功后等待余额刷新。'
    ],
    images: [
      { src: '/client-guide/g14.png', alt: '主界面中的去充值入口' },
      { src: '/client-guide/g15.png', alt: '充值金额选择页面' },
      { src: '/client-guide/g16.png', alt: '扫码支付页面' },
      { src: '/client-guide/g17.png', alt: '充值成功后的余额更新' }
    ],
    note: '充值页通常会显示当前余额、充值金额、手续费、实际支付金额和支付二维码。'
  },
  {
    number: 7,
    title: '在 ChatGPT 中切换模型和推理强度',
    intro: 'ChatGPT 启动后，可以在对话顶部切换模型。英文界面请找同一位置的“Model”入口。',
    actions: [
      '打开 ChatGPT。',
      '点击对话顶部的模型按钮（英文界面显示为“Model”）。',
      '选择需要的模型和推理强度。',
      '开始对话。'
    ],
    images: [
      { src: '/client-guide/g18.png', alt: 'ChatGPT 对话顶部的模型按钮' },
      { src: '/client-guide/g19.png', alt: 'ChatGPT 模型下拉列表' },
      { src: '/client-guide/g20.png', alt: '选择模型和推理强度后的对话界面' }
    ],
    note: '模型入口就是 ChatGPT 里的“模型”按钮；英文界面请找同一位置的“Model”入口。'
  },
  {
    number: 8,
    title: 'ChatGPT 推理强度说明',
    intro: '部分模型支持设置推理强度，也就是思考深度。挡位旁边的英文按钮名如下：',
    actions: [],
    note: '推理强度入口就是 ChatGPT 里的“推理强度”；英文界面请找 Instant、Medium、High、Extra High。'
  },
  {
    number: 9,
    title: '添加桌面和开始菜单快捷方式',
    intro: '客户端是免安装程序，可以按需创建快捷方式。',
    actions: [
      '在解压目录中双击“注册桌面和开始菜单快捷方式”。',
      '以后可以从桌面或开始菜单快速打开客户端。'
    ],
    images: [{ src: '/client-guide/g21.png', alt: '注册桌面和开始菜单快捷方式' }],
    note: '切记不要删除此目录。程序是免安装的，这就是你的程序目录，删除后将无法使用。'
  }
]

const modelRows = [
  { model: 'GPT-5.5', feature: '能力强，偏重复杂任务', price: '贵', scene: '编码、研究、深度分析' },
  { model: 'GPT-5.6 Luna', feature: '最快、最省', price: '便宜', scene: '追求速度和成本的轻量任务' },
  { model: 'GPT-5.6 Terra', feature: '能力、速度、成本更均衡', price: '中', scene: '大多数日常使用' },
  { model: 'GPT-5.6 Sol', feature: '用于更复杂的任务', price: '贵', scene: '编码、研究、深度分析' }
]

const reasoningRows = [
  { level: '低', english: 'Instant', description: '更快，适合简单问题' },
  { level: '中', english: 'Medium', description: '平衡速度和质量' },
  { level: '高', english: 'High', description: '更认真地分析问题' },
  { level: '极高', english: 'Extra High', description: '最强推理，适合复杂任务' }
]

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}
</script>

<template>
  <div class="client-download-page min-h-screen bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white">
    <header class="border-b border-gray-200/80 bg-white/90 px-4 py-4 backdrop-blur sm:px-6 dark:border-dark-800 dark:bg-dark-950/90">
      <nav class="mx-auto flex max-w-6xl items-center justify-between gap-4">
        <router-link to="/home" class="flex min-w-0 items-center gap-3">
          <img :src="siteLogo" :alt="`${siteName} logo`" class="h-9 w-9 shrink-0 object-contain" />
          <span class="truncate text-sm font-semibold sm:text-base">{{ siteName }}</span>
        </router-link>
        <div class="flex items-center gap-1.5 sm:gap-2">
          <LocaleSwitcher />
          <button
            type="button"
            class="flex h-9 w-9 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="isDark ? '切换到浅色模式' : '切换到深色模式'"
            @click="toggleTheme"
          >
            <Icon :name="isDark ? 'sun' : 'moon'" size="sm" />
          </button>
          <router-link
            :to="authStore.isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-9 items-center justify-center rounded-lg bg-gray-900 px-3 text-xs font-medium text-white transition-colors hover:bg-gray-800 sm:px-4 sm:text-sm dark:bg-white dark:text-gray-900 dark:hover:bg-gray-200"
          >
            {{ authStore.isAuthenticated ? '进入控制台' : '登录' }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="mx-auto max-w-6xl px-4 py-8 sm:px-6 sm:py-12">
      <section class="relative overflow-hidden rounded-3xl bg-gray-900 px-6 py-8 text-white shadow-xl sm:px-10 sm:py-12 dark:bg-dark-800">
        <div class="relative z-10 max-w-3xl">
          <p class="mb-3 text-xs font-semibold uppercase tracking-[0.18em] text-primary-300">Windows x64</p>
          <h1 class="text-3xl font-bold tracking-tight sm:text-5xl">共飞 ChatGPT 助手</h1>
          <p class="mt-4 max-w-2xl text-sm leading-7 text-gray-300 sm:text-base">
            下载客户端，完成注册、登录、ChatGPT 安装、分组切换和充值。下面的操作步骤适合第一次使用的用户。
          </p>
          <div v-if="directDownloadUrl" class="mt-7 border-t border-white/15 pt-6">
            <p class="text-xs font-semibold uppercase tracking-[0.16em] text-primary-300">推荐下载方式</p>
            <p class="mt-2 text-sm font-medium text-white">在线下载地址</p>
            <a
              :href="directDownloadUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="mt-2 block break-all text-base leading-7 text-primary-200 underline decoration-primary-300/60 underline-offset-4 hover:text-primary-100 sm:text-lg"
              title="右键此地址，选择将该链接另存为"
            >{{ directDownloadUrl }}</a>
            <p class="mt-3 text-sm leading-6 text-gray-300">
              右键该地址，选择“将该链接另存为”即可自动下载。下载后先解压，再双击打开客户端。
            </p>
          </div>
          <div class="mt-6 flex flex-wrap items-center gap-3">
            <a
              v-if="netdiskDownloadUrl"
              :href="netdiskDownloadUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="btn btn-secondary inline-flex items-center gap-2 px-5 py-3 text-sm"
            >
              <Icon name="download" size="sm" />
              网盘下载
              <Icon name="externalLink" size="sm" />
            </a>
            <p v-if="!hasAnyDownloadUrl" class="text-sm text-gray-300">
              管理员尚未配置下载地址，请稍后再试。
            </p>
          </div>
          <p class="mt-4 text-xs text-gray-400">
            文件：<span class="font-mono text-gray-200">codex-relay-client_v0.1_x64.zip</span>
          </p>
          <p class="mt-1 text-xs text-gray-400">客户端为免安装程序，下载后请先解压。</p>
        </div>
        <div class="pointer-events-none absolute -right-6 -top-10 hidden h-72 w-72 rotate-12 opacity-20 sm:block">
          <img src="/gongfei-plane.svg" alt="" class="h-full w-full object-contain" />
        </div>
      </section>

      <section class="mt-10 grid gap-6 lg:grid-cols-[220px_minmax(0,1fr)] lg:items-start">
        <aside class="hidden lg:sticky lg:top-6 lg:block">
          <div class="rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-800 dark:bg-dark-900">
            <p class="mb-3 text-xs font-semibold uppercase tracking-wider text-gray-400 dark:text-dark-500">操作目录</p>
            <nav class="space-y-1">
              <a
                v-for="step in guideSteps"
                :key="step.number"
                :href="`#guide-step-${step.number}`"
                class="block rounded-lg px-3 py-2 text-sm text-gray-600 transition-colors hover:bg-primary-50 hover:text-primary-700 dark:text-dark-300 dark:hover:bg-primary-900/20 dark:hover:text-primary-300"
              >
                {{ step.number }}. {{ step.title }}
              </a>
            </nav>
          </div>
        </aside>

        <div class="min-w-0 space-y-6">
          <article
            v-for="step in guideSteps"
            :id="`guide-step-${step.number}`"
            :key="step.number"
            class="scroll-mt-6 rounded-2xl border border-gray-200 bg-white p-5 shadow-sm sm:p-7 dark:border-dark-800 dark:bg-dark-900"
          >
            <div class="flex items-start gap-3">
              <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-primary-100 text-sm font-bold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ step.number }}</span>
              <div class="min-w-0">
                <h2 class="text-xl font-semibold text-gray-900 dark:text-white">{{ step.title }}</h2>
                <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">{{ step.intro }}</p>
              </div>
            </div>

            <ol v-if="step.actions.length" class="mt-5 space-y-2.5 text-sm leading-6 text-gray-700 dark:text-dark-200">
              <li v-for="(action, index) in step.actions" :key="action" class="flex gap-3">
                <span class="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-gray-100 text-xs font-semibold text-gray-500 dark:bg-dark-800 dark:text-dark-300">{{ index + 1 }}</span>
                <span>{{ action }}</span>
              </li>
            </ol>

            <div v-if="step.number === 7" class="mt-6 overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-700">
              <table class="w-full min-w-[620px] text-left text-sm">
                <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-300">
                  <tr><th class="px-4 py-3 font-medium">模型</th><th class="px-4 py-3 font-medium">特点</th><th class="px-4 py-3 font-medium">价格感受</th><th class="px-4 py-3 font-medium">适合场景</th></tr>
                </thead>
                <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                  <tr v-for="row in modelRows" :key="row.model">
                    <td class="whitespace-nowrap px-4 py-3 font-medium text-gray-900 dark:text-white">{{ row.model }}</td>
                    <td class="px-4 py-3 text-gray-600 dark:text-dark-300">{{ row.feature }}</td>
                    <td class="px-4 py-3"><span class="rounded-full bg-primary-50 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ row.price }}</span></td>
                    <td class="px-4 py-3 text-gray-600 dark:text-dark-300">{{ row.scene }}</td>
                  </tr>
                </tbody>
              </table>
            </div>

            <div v-if="step.number === 8" class="mt-6 overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-700">
              <table class="w-full min-w-[520px] text-left text-sm">
                <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-300">
                  <tr><th class="px-4 py-3 font-medium">挡位</th><th class="px-4 py-3 font-medium">英文按钮名</th><th class="px-4 py-3 font-medium">说明</th></tr>
                </thead>
                <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                  <tr v-for="row in reasoningRows" :key="row.english">
                    <td class="px-4 py-3 font-medium text-gray-900 dark:text-white">{{ row.level }}</td>
                    <td class="px-4 py-3 font-mono text-primary-700 dark:text-primary-300">{{ row.english }}</td>
                    <td class="px-4 py-3 text-gray-600 dark:text-dark-300">{{ row.description }}</td>
                  </tr>
                </tbody>
              </table>
            </div>

            <div v-if="step.images?.length" class="mt-6 grid gap-4 sm:grid-cols-2">
              <figure v-for="image in step.images" :key="image.src" class="overflow-hidden rounded-xl border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-800">
                <img :src="image.src" :alt="image.alt" loading="lazy" class="h-auto w-full object-contain" />
                <figcaption class="border-t border-gray-200 px-3 py-2 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400">{{ image.alt }}</figcaption>
              </figure>
            </div>

            <p v-if="step.note" class="mt-5 rounded-xl border-l-4 border-amber-400 bg-amber-50 px-4 py-3 text-sm leading-6 text-amber-900 dark:border-amber-500 dark:bg-amber-950/30 dark:text-amber-200">{{ step.note }}</p>
          </article>

          <section class="rounded-2xl border border-gray-200 bg-white p-5 sm:p-7 dark:border-dark-800 dark:bg-dark-900">
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">注意事项</h2>
            <ul class="mt-4 list-disc space-y-2 pl-5 text-sm leading-6 text-gray-600 dark:text-dark-300">
              <li>仅支持 Windows x64，请不要直接移动或删除版本目录内的依赖文件和快捷方式脚本。</li>
              <li>客户端和 ChatGPT 需要能够访问服务端，网络、代理或 DNS 异常可能导致登录、刷新或支付失败。</li>
              <li>客户端不是 ChatGPT 官方客户端，账号、软件许可和使用规则以 ChatGPT 官方规定为准。</li>
              <li>支付前请核对金额和收款页面信息，不要重复打开多个支付窗口。</li>
            </ul>
          </section>

          <section class="rounded-2xl border border-gray-200 bg-white p-5 sm:p-7 dark:border-dark-800 dark:bg-dark-900">
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">常见问题</h2>
            <div class="mt-4 grid gap-4 sm:grid-cols-2">
              <div>
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">启动后没有反应？</h3>
                <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">确认从完整的版本目录启动，并检查 Windows 安全软件是否拦截程序。必要时重新解压一份完整版本。</p>
              </div>
              <div>
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">余额没有及时更新？</h3>
                <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">客户端默认每 60 秒刷新一次，也可以重新进入主界面触发刷新。</p>
              </div>
              <div>
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">ChatGPT 没有切换到当前账号？</h3>
                <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">先退出正在运行的 ChatGPT，再回到客户端点击重启 ChatGPT 激活当前账号。</p>
              </div>
              <div>
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">充值二维码无法显示？</h3>
                <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">检查网络连接和系统时间；仍无法解决时，通过客户端“联系我们”反馈订单号和问题时间。</p>
              </div>
            </div>
          </section>
        </div>
      </section>
    </main>
  </div>
</template>
