import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'

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

    const imageSources = wrapper.findAll('img').map((image) => image.attributes('src'))
    expect(imageSources).toContain('/client-guide/g1.png')
    expect(imageSources).toContain('/client-guide/g10.png')
    expect(imageSources).toContain('/client-guide/g21.png')

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

  it('links unauthenticated visitors to login', () => {
    const wrapper = mountDownloadPage()
    const links = wrapper.findAllComponents(RouterLinkStub)

    expect(links.some((link) => link.props('to') === '/login')).toBe(true)
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
        'https://download.example.com/downloads/codex-relay-client_v0.2_macos-arm64.tar.gz'
    })

    afterEach(() => {
      delete appStore.cachedPublicSettings.client_download_direct_url_mac
    })

    it('shows the mac install command derived from the same directory as the package', () => {
      const wrapper = mountDownloadPage()

      // 平台徽章要跟着变，否则页面顶部一直写"Windows x64"，mac 用户会以为找错了页面。
      expect(wrapper.text()).toContain('Windows x64 · macOS')
      expect(wrapper.text()).toContain(
        'curl -fsSL https://download.example.com/downloads/install-mac.sh | bash'
      )
      expect(wrapper.text()).toContain('codex-relay-client_v0.2_macos-arm64.tar.gz')
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
    })
  })
})
