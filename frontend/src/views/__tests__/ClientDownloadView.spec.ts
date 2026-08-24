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

  it('renders the client download, manual images, and English ChatGPT controls', () => {
    const wrapper = mountDownloadPage()

    const directLink = wrapper.get('a[href="https://example.com/Release/client_x64.zip"]')
    expect(directLink.attributes('target')).toBe('_blank')
    expect(directLink.attributes('download')).toBeUndefined()
    expect(directLink.text()).toContain('https://example.com/Release/client_x64.zip')
    expect(wrapper.find('a.btn-primary').exists()).toBe(false)
    expect(wrapper.text()).toContain('推荐下载方式')
    expect(wrapper.text()).toContain('右键该地址，选择“将该链接另存为”即可自动下载')
    expect(wrapper.text()).toContain('右键该地址，选择“将该链接另存为”即可自动下载')
    expect(wrapper.get('a[href="https://pan.example.com/s/abc"]')).toBeTruthy()
    expect(wrapper.text()).toContain('Install')
    expect(wrapper.text()).toContain('Model')
    expect(wrapper.text()).toContain('Extra High')

    const imageSources = wrapper.findAll('img').map((image) => image.attributes('src'))
    expect(imageSources).toContain('/client-guide/g1.png')
    expect(imageSources).toContain('/client-guide/g10.png')
    expect(imageSources).toContain('/client-guide/g21.png')

  })

  it('drops download URLs that are not http(s)', () => {
    appStore.cachedPublicSettings.client_download_direct_url = 'javascript:alert(1)'
    appStore.cachedPublicSettings.client_download_netdisk_url = ''

    const wrapper = mountDownloadPage()

    const hrefs = wrapper.findAll('a').map((link) => link.attributes('href'))
    expect(hrefs.some((href) => href?.startsWith('javascript:'))).toBe(false)
    expect(wrapper.text()).toContain('管理员尚未配置下载地址')

    appStore.cachedPublicSettings.client_download_direct_url = 'https://example.com/Release/client_x64.zip'
    appStore.cachedPublicSettings.client_download_netdisk_url = 'https://pan.example.com/s/abc'
  })

  it('links unauthenticated visitors to login', () => {
    const wrapper = mountDownloadPage()
    const links = wrapper.findAllComponents(RouterLinkStub)

    expect(links.some((link) => link.props('to') === '/login')).toBe(true)
  })
})
