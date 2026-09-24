<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import ClientIntroduction from '@/components/client/ClientIntroduction.vue'
import { useServiceAvailability } from '@/composables/useServiceAvailability'

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
  // 这几步的操作因平台而异（Mac 版走终端命令自动安装，没有解压/快捷方式这些
  // 概念）。只在 macOS 区块出现时才显示，避免在 mac 未发布时提前剧透。
  macNote?: string
}

const appStore = useAppStore()
const authStore = useAuthStore()
const { t } = useI18n()
const isDark = ref(document.documentElement.classList.contains('dark'))

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || '共飞 AI')
const siteLogo = computed(() => appStore.siteLogo || '/gongfei-plane.svg')
const dashboardPath = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))
// 后台「联系方式」设置项，已经是全站通用的公开配置（AppHeader 也在用），
// 不用为这个页面单独加设置项。管理员没填时这块直接不显示 QQ。
const contactInfo = computed(() => appStore.contactInfo)

// 备用网盘地址由管理员填写。后端 normalizeExternalHTTPURL 已挡掉伪协议，
// 这里再校验一次，避免绕过后端直接改库的值进到 href。
function safeExternalUrl(value: string | undefined): string {
  const url = (value || '').trim()
  return /^https?:\/\//i.test(url) ? url : ''
}
const clientDownloadUrl = '/api/v1/download/client'
// 从下载直链里解析文件名，而不是写死。
//
// 写死过一次，代价是：换客户端版本只需要改一个数据库设置项就能生效，但页面上
// 这行文字却要跟着重新构建前端、重新部署后端（dist 是 embed 进二进制的）才能更新。
// 结果就是实际下载 v0.1.2、页面却还写着 v0.1，用户以为下错了。
const clientFileName = computed(() => {
  const url = appStore.cachedPublicSettings?.client_download_direct_url || ''
  const name = url.split('?')[0].split('#')[0].split('/').pop() || ''
  return /\.zip$/i.test(name) ? name : ''
})
const codexDownloadUrl = 'https://codexapp.agentsmirror.com/latest/win-x64'

// 跳转链接而不是内嵌播放：直接用管理员填的原始视频页地址，不用再解析 BV 号
// 拼播放器 iframe 地址。
const tutorialVideoUrl = computed(() => safeExternalUrl(appStore.cachedPublicSettings?.client_tutorial_video_url))
const netdiskDownloadUrl = computed(() => safeExternalUrl(appStore.cachedPublicSettings?.client_download_netdisk_url))

// macOS 安装包直链，由管理员填写。为空表示 mac 版尚未发布，整个 macOS 区块不出现——
// 而不是显示一个点了没反应的按钮。
const macDownloadUrl = computed(() =>
  safeExternalUrl(appStore.cachedPublicSettings?.client_download_direct_url_mac)
)

// 安装脚本约定与安装包同目录发布：出包流水线本来就把 tar.gz 和 install-mac.sh
// 放进同一份产物里。约定破坏时 curl 会当场报 404 —— 是用户看得见的失败，
// 而不是装上一个坏掉的应用。
const macInstallScriptUrl = computed(() => {
  const url = macDownloadUrl.value
  if (!url) return ''
  const path = url.split('?')[0].split('#')[0]
  return `${path.slice(0, path.lastIndexOf('/') + 1)}install-mac.sh`
})

const macInstallCommand = computed(() =>
  macInstallScriptUrl.value ? `curl -fsSL ${macInstallScriptUrl.value} | bash` : ''
)

// 与 Windows 的 clientFileName 对称：把解析出的包名显示出来。地址指错东西
// （填成 .dmg、填成带版本号的旧名、或是一个报错页）在页面上本来毫无迹象，
// 而安装脚本要到用户的终端里才会失败。
const macFileName = computed(() => {
  const name = macDownloadUrl.value.split('?')[0].split('#')[0].split('/').pop() || ''
  return /\.tar\.gz$/i.test(name) ? name : ''
})

// 共飞 Mac 版仅发布了 Apple 芯片版本，因此只提供配套的 Codex 下载。
const codexMacDownloadUrl = 'https://codexapp.agentsmirror.com/latest/mac-arm64'

const macCommandCopied = ref(false)

// 刻意不用 @/composables/useClipboard。那个 composable 在自己的模块里取
// useAppStore（从 '@/stores/app'）和 i18n，而本页的测试只把 '@/stores' 换成了
// 假对象 —— 引进来会在 mount 阶段就要求一个真实的 Pinia，打坏一个与本次改动
// 无关的测试。这里只需要复制一行文本。
async function copyMacInstallCommand() {
  const command = macInstallCommand.value
  if (!command) return

  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(command)
    } else {
      // Clipboard API 只在安全上下文可用，HTTP 部署下不存在。
      const textarea = document.createElement('textarea')
      textarea.value = command
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
    }
    macCommandCopied.value = true
    setTimeout(() => {
      macCommandCopied.value = false
    }, 2000)
  } catch {
    // 复制失败不打断：命令就完整显示在旁边，用户可以手动选中。
    macCommandCopied.value = false
  }
}

const guideSteps: GuideStep[] = [
  {
    number: 1,
    title: '下载、解压并打开客户端',
    intro: '在上面的下载区选择与你电脑对应的版本。Windows 用户先下载共飞客户端和 Codex 客户端，再把压缩包分别解压到文件夹。',
    actions: [
      '点击“下载共飞客户端”，保存压缩包。',
      '右键压缩包，选择解压到一个文件夹。',
      '进入解压后的目录，双击客户端程序打开。',
      '再点击“下载 Codex 客户端”，同样解压后使用。'
    ],
    images: [{ src: '/client-guide/g1.png', alt: '双击打开共飞 AI 客户端' }],
    note: '两个客户端缺一不可：共飞客户端管账号、分组和余额，Codex 客户端才是实际对话的程序。',
    macNote: '请忽略上面的下载解压步骤，改用上方 macOS 下载区的安装命令。它会自动下载、安装到「应用程序」并启动，不需要解压。然后下载配套的 Apple 芯片版 Codex。'
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
    title: '安装并启动对话软件',
    intro: '对话软件叫 Codex，下面截图和共飞里的按钮仍可能写着“ChatGPT”。它们指的是这里的同一个对话软件，不需要再下载第三个。',
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
    note: '已经安装好 Codex 时，直接点击“启动 ChatGPT”或对应的启动按钮即可。对话时请保持共飞客户端运行。'
  },
  {
    number: 5,
    title: '查看当前分组（可以先用默认的）',
    intro: '“分组”就是连接 AI 的线路，不同线路的价格和可用模型可能不同。第一次使用可以先保留默认选择，需要时再切换。',
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
    title: '查看余额，不够时再充值',
    intro: '先确认账号有可用余额或额度。使用 AI 会产生费用；余额不足时，可以直接在客户端内充值。',
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
    title: '发出你的第一个问题',
    intro: '打开 Codex，在对话输入框里用中文写下你想做的事，点击发送。比如：“帮我写一份请假条”。下面的模型选项按需要再调整。',
    actions: [
      '保持共飞客户端运行，打开 Codex 的对话窗口。',
      '在输入框里写下问题，点击发送，等待回答。',
      '回答不够清楚，就继续说“再简单一点”或补充你的要求。',
      '需要更换模型时，点击输入框附近的模型名称（英文界面可能显示为“Model”），再选择模型和推理强度。'
    ],
    images: [
      { src: '/client-guide/g18.png', alt: 'Codex 输入框附近的模型按钮' },
      { src: '/client-guide/g19.png', alt: 'Codex 模型下拉列表' },
      { src: '/client-guide/g20.png', alt: '在 Codex 输入框里提问并查看回答' }
    ],
    note: '“模型”可以理解为不同的 AI 帮手；“推理强度”就是让它思考得多一些还是少一些。第一次可以先用默认设置。'
  },
  {
    number: 8,
    title: '想让 AI 多想一会儿？（选看）',
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
    note: '切记不要删除此目录。程序是免安装的，这就是你的程序目录，删除后将无法使用。',
    macNote: '此步骤仅适用于 Windows 版。Mac 版安装后已经在「应用程序」里，可以直接拖到 Dock 上，不需要额外注册快捷方式。'
  },
  {
    number: 10,
    title: '查看用量与节省统计',
    intro: '客户端首页会展示当前账号累计处理的 Token 数量，以及通过共飞线路节省的 Token 数量和比例。',
    actions: [
      '打开共飞客户端并进入首页。',
      '在首页底部查看“累计处理”和“节省”统计。',
      '用节省比例了解当前线路相对直接调用的使用效果。'
    ],
    images: [{ src: '/client-guide/g22.png', alt: '客户端首页的用量与节省统计' }],
    note: '统计数据会随客户端使用更新，具体数值以客户端当前显示为准。'
  }
]

// 关闭充值的部署版本：去掉"查看余额，不够时再充值"这一步，后面的步骤顺延编号。
const { rechargeEnabled } = useServiceAvailability()
const RECHARGE_STEP_NUMBER = 6
const visibleGuideSteps = computed(() =>
  (rechargeEnabled.value ? guideSteps : guideSteps.filter((step) => step.number !== RECHARGE_STEP_NUMBER)).map(
    (step, index) => ({ ...step, number: index + 1 }),
  ),
)

const modelRows = [
  { model: 'GPT-5.5', feature: '能力强，偏重复杂任务', price: '贵', scene: '编码、研究、深度分析' },
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
  <div class="client-download-page min-h-screen bg-[#f7f8f5] text-gray-900 dark:bg-dark-950 dark:text-white">
    <header class="border-b border-gray-200/80 bg-white/90 px-4 py-4 backdrop-blur sm:px-6 dark:border-dark-800 dark:bg-dark-950/90">
      <nav class="mx-auto grid max-w-7xl grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-3 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
        <router-link to="/home" class="flex min-w-0 items-center gap-3">
          <img :src="siteLogo" :alt="`${siteName} logo`" class="h-9 w-9 shrink-0 object-contain" />
          <span class="truncate text-base font-semibold">{{ siteName }}</span>
        </router-link>
        <div class="flex items-center gap-1">
          <LocaleSwitcher />
          <button
            type="button"
            class="flex h-10 w-10 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="isDark ? '切换到浅色模式' : '切换到深色模式'"
            @click="toggleTheme"
          >
            <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
          </button>
        </div>
        <div class="col-span-2 flex items-center gap-2 sm:col-span-1">
          <router-link
            :to="authStore.isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-11 flex-1 items-center justify-center whitespace-nowrap rounded-xl px-4 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 sm:flex-none dark:text-dark-200 dark:hover:bg-dark-800 dark:hover:text-white"
          >
            {{ t('clientIntroduction.console') }}
          </router-link>
          <a
            href="#downloads"
            class="inline-flex min-h-11 flex-1 items-center justify-center gap-2 whitespace-nowrap rounded-xl bg-[#2864dc] px-5 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-[#174bba] sm:flex-none dark:bg-[#376bdd] dark:hover:bg-[#285bc5]"
          >
            <Icon name="download" size="sm" aria-hidden="true" />
            {{ t('clientIntroduction.clientDownload') }}
          </a>
        </div>
      </nav>
    </header>

    <main class="mx-auto max-w-7xl px-4 py-8 sm:px-6 sm:py-12">
      <ClientIntroduction download-page />

      <section id="downloads" aria-labelledby="downloads-title" class="mt-12 scroll-mt-6 sm:mt-16">
        <p class="text-sm font-semibold text-primary-700 dark:text-primary-300">准备好了，就从这里下载</p>
        <h2 id="downloads-title" class="mt-2 text-2xl font-bold">选你的电脑，下载这两个软件</h2>
        <p class="mt-3 text-base leading-7 text-gray-600 dark:text-dark-300">
          下面两个客户端都要下载，缺一个用不了。<strong class="font-semibold text-gray-900 dark:text-white">共飞负责账号、连接和费用，Codex 负责提问和回答。</strong>
        </p>
        <p class="mt-2 text-sm leading-7 text-gray-600 dark:text-dark-300">
          教程截图中的“安装 ChatGPT”“启动 ChatGPT”，指的就是这里的 Codex 对话软件，不用再找第三个软件。
        </p>
        <div class="mt-6 grid items-start gap-5" :class="macDownloadUrl ? 'lg:grid-cols-2' : ''">
          <section aria-labelledby="windows-title" class="min-w-0 rounded-3xl border border-gray-200 bg-white p-5 sm:p-7 dark:border-dark-800 dark:bg-dark-900">
            <p class="text-sm font-medium text-primary-700 dark:text-primary-300">Windows 电脑</p>
            <h3 id="windows-title" class="mt-2 text-xl font-bold">Windows 64 位（x64）</h3>
            <p class="mt-2 text-sm leading-7 text-gray-600 dark:text-dark-300">建议使用更新后的 Windows 10 或 Windows 11。32 位、ARM 电脑不适用这个下载包。</p>
            <div class="mt-5 space-y-5">
              <div>
                <p class="mb-2 text-sm font-semibold">① 共飞：登录账号、看余额、连接服务</p>
                <a :href="clientDownloadUrl" download class="btn btn-primary flex min-h-12 w-full items-center justify-center gap-2 px-4 py-3 text-base">
                  <Icon name="download" size="sm" />
                  下载共飞客户端
                </a>
                <p v-if="clientFileName" class="mt-2 break-all text-xs leading-5 text-gray-500 dark:text-dark-400">文件：{{ clientFileName }}</p>
              </div>
              <div>
                <p class="mb-2 text-sm font-semibold">② Codex：输入问题、查看 AI 回答</p>
                <a :href="codexDownloadUrl" target="_blank" rel="noopener noreferrer" class="btn btn-secondary flex min-h-12 w-full items-center justify-center gap-2 px-4 py-3 text-base">
                  <Icon name="download" size="sm" />
                  下载 Codex 客户端
                  <Icon name="externalLink" size="sm" />
                </a>
              </div>
            </div>
            <p class="mt-5 border-t border-gray-100 pt-4 text-sm leading-7 text-gray-600 dark:border-dark-800 dark:text-dark-300">下载后先解压，再打开文件夹里的程序。不要直接在压缩包里双击运行，也不要删除解压后的文件夹。</p>
          </section>

          <section v-if="macDownloadUrl" aria-labelledby="mac-title" class="min-w-0 rounded-3xl border border-gray-200 bg-white p-5 sm:p-7 dark:border-dark-800 dark:bg-dark-900">
            <p class="text-sm font-medium text-primary-700 dark:text-primary-300">苹果电脑 · macOS</p>
            <h3 id="mac-title" class="mt-2 text-xl font-bold">Apple 芯片（M 系列）</h3>
            <p class="mt-2 text-sm leading-7 text-gray-600 dark:text-dark-300">先点左上角苹果菜单 →「关于本机」，确认芯片名称以 Apple M 开头。<strong class="text-gray-900 dark:text-white">Intel Mac 暂不支持。</strong></p>
            <div class="mt-5">
              <p class="text-sm font-semibold">① 安装共飞：把下面的命令粘贴到「终端」</p>
              <ol class="mt-2 list-decimal space-y-1 pl-5 text-sm leading-7 text-gray-600 dark:text-dark-300">
                <li>同时按 Command（⌘）和空格键，搜索“终端”并打开。</li>
                <li>点击“复制命令”，回到终端按 Command（⌘）+ V 粘贴，再按回车。</li>
                <li>等安装完成，共飞会自动打开。以后更新也用这条命令。</li>
              </ol>
              <code class="mt-3 block whitespace-pre-wrap break-all rounded-xl bg-gray-900 p-4 font-mono text-xs leading-6 text-gray-100">{{ macInstallCommand }}</code>
              <button type="button" class="btn btn-secondary mt-3 inline-flex min-h-11 items-center gap-2 px-4 py-2.5 text-sm" @click="copyMacInstallCommand">
                <Icon :name="macCommandCopied ? 'check' : 'clipboard'" size="sm" />
                <span aria-live="polite">{{ macCommandCopied ? '已复制' : '复制命令' }}</span>
              </button>
              <p v-if="macFileName" class="mt-2 break-all text-xs leading-5 text-gray-500 dark:text-dark-400">安装包：{{ macFileName }}</p>
              <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">共飞 Mac 版尚未经过苹果公证，请使用上面的安装命令。浏览器直接下载的文件可能被系统拦截。</p>
            </div>
            <div class="mt-5 border-t border-gray-100 pt-4 dark:border-dark-800">
              <p class="mb-2 text-sm font-semibold">② 下载 Codex：用来提问和看回答</p>
              <a :href="codexMacDownloadUrl" target="_blank" rel="noopener noreferrer" class="btn btn-primary flex min-h-12 w-full items-center justify-center gap-2 px-4 py-3 text-base">
                <Icon name="download" size="sm" />
                下载 Codex（Apple 芯片）
                <Icon name="externalLink" size="sm" />
              </a>
            </div>
          </section>
        </div>
        <div class="mt-5 flex flex-wrap items-center gap-x-6 gap-y-3 text-sm">
          <a href="#guide-step-1" class="inline-flex min-h-11 items-center gap-2 font-semibold text-primary-700 hover:underline dark:text-primary-300">
            已经下载好了？继续看图文操作
            <Icon name="arrowDown" size="sm" />
          </a>
          <a v-if="netdiskDownloadUrl" :href="netdiskDownloadUrl" target="_blank" rel="noopener noreferrer" class="inline-flex min-h-11 items-center gap-2 text-gray-600 underline underline-offset-4 dark:text-dark-300">
            共飞客户端备用网盘下载
            <Icon name="externalLink" size="sm" />
          </a>
        </div>
      </section>

      <section
        v-if="tutorialVideoUrl"
        class="mt-10 flex flex-wrap items-center justify-between gap-4 rounded-3xl border border-gray-200 bg-white p-6 shadow-sm sm:p-8 dark:border-dark-800 dark:bg-dark-900"
      >
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.16em] text-primary-600 dark:text-primary-300">视频教程</p>
          <h2 class="mt-2 text-xl font-bold tracking-tight sm:text-2xl">跟着视频一步步操作</h2>
          <p class="mt-3 max-w-2xl text-sm leading-6 text-gray-600 dark:text-gray-300">
            不想看文字教程？点右边按钮去 B 站看这段视频，{{ rechargeEnabled ? '从下载到充值完整走一遍' : '从下载到使用完整走一遍' }}。下面还有图文步骤可以对照。
          </p>
        </div>
        <a
          :href="tutorialVideoUrl"
          target="_blank"
          rel="noopener"
          class="btn btn-primary inline-flex shrink-0 items-center justify-center gap-2 px-5 py-3 text-sm"
        >
          <Icon name="chat" size="sm" />
          去 B 站观看
        </a>
      </section>

      <section class="mt-10 grid gap-6 lg:grid-cols-[220px_minmax(0,1fr)] lg:items-start">
        <aside class="hidden lg:sticky lg:top-6 lg:block">
          <div class="rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-800 dark:bg-dark-900">
            <p class="mb-3 text-xs font-semibold uppercase tracking-wider text-gray-400 dark:text-dark-500">操作目录</p>
            <nav class="space-y-1">
              <a
                v-for="step in visibleGuideSteps"
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
            v-for="step in visibleGuideSteps"
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
            <p v-if="macDownloadUrl && step.macNote" class="mt-3 rounded-xl border-l-4 border-primary-400 bg-primary-50 px-4 py-3 text-sm leading-6 text-primary-900 dark:border-primary-500 dark:bg-primary-950/30 dark:text-primary-200">Mac 用户：{{ step.macNote }}</p>
          </article>

          <section class="rounded-2xl border border-gray-200 bg-white p-5 sm:p-7 dark:border-dark-800 dark:bg-dark-900">
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">注意事项</h2>
            <ul class="mt-4 list-disc space-y-2 pl-5 text-sm leading-6 text-gray-600 dark:text-dark-300">
              <li v-if="!macDownloadUrl">仅支持 Windows x64，请不要直接移动或删除版本目录内的依赖文件和快捷方式脚本。</li>
              <li v-else>Windows 版请不要直接移动或删除版本目录内的依赖文件和快捷方式脚本；macOS 版仅支持 Apple 芯片（M 系列），Intel Mac 暂未发布。</li>
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
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">ChatGPT 一直安装不上？</h3>
                <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">
                  常见原因是系统版本过旧，早期 Windows 10 版本无法安装 ChatGPT。任选其一升级系统后重新安装即可：
                  <a
                    href="https://go.microsoft.com/fwlink/?linkid=2171764"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="font-medium text-primary-600 hover:underline dark:text-primary-400"
                  >升级到 Windows 11</a>，或
                  <a
                    href="https://go.microsoft.com/fwlink/?LinkID=799445"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="font-medium text-primary-600 hover:underline dark:text-primary-400"
                  >下载 Windows 10 升级助手</a>。
                </p>
              </div>
              <div>
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">余额没有及时更新？</h3>
                <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">客户端默认每 60 秒刷新一次，也可以重新进入主界面触发刷新。</p>
              </div>
              <div>
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">ChatGPT 没有切换到当前账号？</h3>
                <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">先退出正在运行的 ChatGPT，再回到客户端点击重启 ChatGPT 激活当前账号。</p>
              </div>
              <div v-if="rechargeEnabled">
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">充值二维码无法显示？</h3>
                <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">检查网络连接和系统时间；仍无法解决时，通过客户端“联系我们”反馈订单号和问题时间。</p>
              </div>
              <div v-if="macDownloadUrl">
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">Mac 安装命令提示"当前是 Intel 芯片，本版本只支持 Apple 芯片"？</h3>
                <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">目前只发布了 Apple 芯片（M 系列）版本。可在左上角苹果菜单 →「关于本机」确认芯片型号；Intel Mac 暂不支持，请联系客服了解进展。</p>
              </div>
            </div>
          </section>

          <section class="rounded-2xl border border-primary-200 bg-primary-50/60 p-5 sm:p-7 dark:border-primary-900 dark:bg-primary-950/20">
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">还是没解决？</h2>
            <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">上面的教程没能解决你的问题，直接找人工帮你看。</p>
            <div class="mt-4 flex flex-wrap items-center gap-3">
              <router-link
                to="/tickets"
                class="btn btn-primary inline-flex items-center justify-center gap-2 px-5 py-2.5 text-sm"
              >
                <Icon name="bell" size="sm" />
                提交工单
              </router-link>
              <div
                v-if="contactInfo"
                class="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-4 py-2.5 text-sm text-gray-700 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-200"
              >
                <Icon name="chat" size="sm" />
                客服{{ contactInfo }}
              </div>
            </div>
          </section>
        </div>
      </section>
    </main>
  </div>
</template>
