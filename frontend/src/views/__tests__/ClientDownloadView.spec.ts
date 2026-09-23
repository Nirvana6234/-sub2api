import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import onboarding from '@/i18n/locales/zh/onboarding'

import ClientDownloadView from '../ClientDownloadView.vue'

const { appStore, authStore } = vi.hoisted(() => ({
  appStore: {
    cachedPublicSettings: {
      site_name: 'Test site',
      client_download_direct_url: 'https://example.com/Release/client_x64.zip',
      client_download_netdisk_url: 'https://pan.example.com/s/abc',
    },
    siteName: 'Fallback site',
    siteLogo: '',
  },
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore,
  useAuthStore: () => authStore,
}))

function mountDownloadPage() {
  return mount(ClientDownloadView, {
    global: {
      plugins: [createI18n({
        legacy: false,
        locale: 'zh',
        messages: { zh: onboarding },
        // The test alias uses the runtime-only build; these messages are plain text.
        messageCompiler: (message) => () => typeof message === 'string' ? message : '',
      })],
      stubs: {
        RouterLink: RouterLinkStub,
        LocaleSwitcher: { template: '<div data-testid="locale-switcher" />' },
        Icon: { template: '<span data-testid="icon" />' },
      },
    },
  })
}

describe('ClientDownloadView', () => {
  beforeEach(() => {
    authStore.isAuthenticated = false
    authStore.isAdmin = false
    document.documentElement.classList.remove('dark')
    localStorage.clear()
  })

  it('drops the recharge step and recharge FAQ when recharge is turned off', () => {
    const settings = appStore.cachedPublicSettings as Record<string, unknown>
    settings.payment_enabled = false
    settings.backup_payment_enabled = false
    try {
      const text = mountDownloadPage().text()
      expect(text).not.toContain('查看余额，不够时再充值')
      expect(text).not.toContain('充值二维码无法显示')
      // 后面的步骤顺延编号，不留空号。
      expect(text).toContain('6. 发出你的第一个问题')
    } finally {
      delete settings.payment_enabled
      delete settings.backup_payment_enabled
    }
  })

  it('keeps the recharge step when recharge is available', () => {
    const text = mountDownloadPage().text()
    expect(text).toContain('查看余额，不够时再充值')
    expect(text).toContain('7. 发出你的第一个问题')
  })

  it('renders the local client download, manual images, and English ChatGPT controls', () => {
    const wrapper = mountDownloadPage()

    const directLink = wrapper.get('a[href="/api/v1/download/client"]')
    expect(directLink.attributes('download')).toBeDefined()
    expect(directLink.text()).toContain('下载共飞客户端')
    expect(wrapper.find('a.btn-primary').exists()).toBe(true)
    // 两个客户端缺一不可，这句提示是页面的核心口径，掉了用户会只下一个。
    expect(wrapper.text()).toContain('下面两个客户端都要下载，缺一个用不了')
    // 文件名从下载直链解析，不再写死在组件里——否则换版本要重新部署整个后端
    // 才能让这行文字跟上，实测出现过「实际下 v0.1.2、页面写 v0.1」。
    expect(wrapper.text()).toContain('client_x64.zip')
    const codexLink = wrapper.get('a[href="https://codexapp.agentsmirror.com/latest/win-x64"]')
    expect(codexLink.attributes('target')).toBe('_blank')
    expect(codexLink.text()).toContain('下载 Codex 客户端')
    expect(wrapper.get('a[href="https://pan.example.com/s/abc"]')).toBeTruthy()
    expect(wrapper.text()).toContain('Install')
    expect(wrapper.text()).toContain('Model')
    expect(wrapper.text()).toContain('Extra High')
    expect(wrapper.text()).not.toContain('共飞 Chat（桌面版）')
    expect(wrapper.text()).not.toContain('下载共飞 Chat')

    const imageSources = wrapper.findAll('img').map((image) => image.attributes('src'))
    expect(imageSources).toContain('/client-guide/g1.png')
    expect(imageSources).toContain('/client-guide/g10.png')
    expect(imageSources).toContain('/client-guide/g21.png')
    expect(imageSources).toContain('/client-guide/g22.png')

  })

  it('drops fallback netdisk URLs that are not http(s)', () => {
    appStore.cachedPublicSettings.client_download_direct_url = 'javascript:alert(1)'
    appStore.cachedPublicSettings.client_download_netdisk_url = ''

    const wrapper = mountDownloadPage()

    const hrefs = wrapper.findAll('a').map((link) => link.attributes('href'))
    expect(hrefs.some((href) => href?.startsWith('javascript:'))).toBe(false)
    expect(wrapper.get('a[href="/api/v1/download/client"]')).toBeTruthy()

    appStore.cachedPublicSettings.client_download_direct_url = 'https://example.com/Release/client_x64.zip'
    appStore.cachedPublicSettings.client_download_netdisk_url = 'https://pan.example.com/s/abc'
  })

  it.each([
    { role: 'guest', authenticated: false, admin: false, destination: '/login' },
    { role: 'user', authenticated: true, admin: false, destination: '/dashboard' },
    { role: 'admin', authenticated: true, admin: true, destination: '/admin/dashboard' },
  ])('offers a console destination and a download shortcut for $role', ({ authenticated, admin, destination }) => {
    authStore.isAuthenticated = authenticated
    authStore.isAdmin = admin
    const wrapper = mountDownloadPage()
    const header = wrapper.get('header')
    const consoleLink = header.findAllComponents(RouterLinkStub).find((link) => link.text() === '控制台')

    expect(consoleLink?.props('to')).toBe(destination)
    expect(header.get('a[href="#downloads"]').text()).toBe('客户端下载')
    expect(wrapper.find('#downloads').exists()).toBe(true)
  })

  it('hides the entire macOS section when no mac direct URL is configured', () => {
    const wrapper = mountDownloadPage()

    expect(wrapper.text()).not.toContain('macOS')
    expect(wrapper.text()).not.toContain('Apple 芯片')
  })

  it('hides the tutorial video section when no video URL is configured', () => {
    const wrapper = mountDownloadPage()

    expect(wrapper.text()).not.toContain('去 B 站观看')
  })

  describe('with a tutorial video URL configured', () => {
    beforeEach(() => {
      appStore.cachedPublicSettings.client_tutorial_video_url = 'https://www.bilibili.com/video/BV1vWYJ6PEhc/'
    })

    afterEach(() => {
      delete appStore.cachedPublicSettings.client_tutorial_video_url
    })

    it('links out to the admin-configured video URL in a new tab instead of embedding it', () => {
      const wrapper = mountDownloadPage()

      const link = wrapper.get('a[href="https://www.bilibili.com/video/BV1vWYJ6PEhc/"]')
      expect(link.text()).toContain('去 B 站观看')
      expect(link.attributes('target')).toBe('_blank')
      expect(link.attributes('rel')).toBe('noopener')
      expect(wrapper.find('iframe').exists()).toBe(false)
    })

    it('drops the video link when the configured URL is not http(s)', () => {
      appStore.cachedPublicSettings.client_tutorial_video_url = 'javascript:alert(1)'

      const wrapper = mountDownloadPage()

      expect(wrapper.text()).not.toContain('去 B 站观看')
    })
  })

  describe('with a macOS direct URL configured', () => {
    beforeEach(() => {
      appStore.cachedPublicSettings.client_download_direct_url_mac =
        'https://download.example.com/downloads/codex-relay-client_v0.5_macos-arm64.tar.gz'
    })

    afterEach(() => {
      delete appStore.cachedPublicSettings.client_download_direct_url_mac
    })

    it('shows the mac install command derived from the same directory as the package', () => {
      const wrapper = mountDownloadPage()

      expect(wrapper.get('#windows-title').text()).toContain('Windows 64 位')
      expect(wrapper.get('#mac-title').text()).toContain('Apple 芯片')
      expect(wrapper.text()).toContain(
        'curl -fsSL https://download.example.com/downloads/install-mac.sh | bash'
      )
      expect(wrapper.text()).toContain('codex-relay-client_v0.5_macos-arm64.tar.gz')
    })

    it('tells mac users to skip the Windows-only unzip and shortcut steps', () => {
      const wrapper = mountDownloadPage()

      // 第 1 步的 Windows 解压说明和第 9 步的开始菜单快捷方式对 mac 用户没有意义，
      // 之前这两步会让 mac 用户误以为自己漏了什么操作。
      expect(wrapper.text()).toContain('请忽略上面的下载解压步骤')
      expect(wrapper.text()).toContain('不需要额外注册快捷方式')
    })

    it('adds the Intel-chip FAQ entry matching the real install script rejection message', () => {
      const wrapper = mountDownloadPage()

      expect(wrapper.text()).toContain('当前是 Intel 芯片，本版本只支持 Apple 芯片')
      expect(wrapper.get('#downloads').text()).toContain('Intel Mac 暂不支持')
      expect(wrapper.find('a[href$="mac-intel"]').exists()).toBe(false)
      expect(wrapper.get('a[href$="mac-arm64"]').text()).toContain('Codex')
    })

    it('copies the configured Mac install command', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      vi.stubGlobal('isSecureContext', true)
      const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
      Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
      try {
        const wrapper = mountDownloadPage()
        await wrapper.findAll('button').find((button) => button.text() === '复制命令')!.trigger('click')
        expect(writeText).toHaveBeenCalledWith('curl -fsSL https://download.example.com/downloads/install-mac.sh | bash')
        expect(wrapper.text()).toContain('已复制')
        wrapper.unmount()
      } finally {
        vi.unstubAllGlobals()
        if (originalClipboard) Object.defineProperty(navigator, 'clipboard', originalClipboard)
        else Reflect.deleteProperty(navigator, 'clipboard')
      }
    })
  })

  it('introduces the product and prerequisites before downloads, with working guide anchors', () => {
    const wrapper = mountDownloadPage()
    const text = wrapper.text()
    expect(text).toContain('共飞把 GPT 等 AI 能力接到你的电脑上')
    expect(text.indexOf('共飞把 GPT 等 AI 能力接到你的电脑上')).toBeLessThan(text.indexOf('选你的电脑，下载这两个软件'))
    expect(text.indexOf('开始前，准备好这 4 样')).toBeLessThan(text.indexOf('选你的电脑，下载这两个软件'))
    expect(wrapper.get('#downloads').text()).toContain('不用再找第三个软件')
    expect(wrapper.get('#guide-step-7').text()).toContain('保持共飞客户端运行')
    for (const link of wrapper.findAll('a[href^="#"]')) {
      expect(wrapper.find(link.attributes('href')!).exists()).toBe(true)
    }
  })
})
