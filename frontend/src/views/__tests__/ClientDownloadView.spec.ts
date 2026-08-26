import { beforeEach, describe, expect, it, vi } from 'vitest'
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
})
