import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'

import HomeView from '../HomeView.vue'

const { appStore, authStore } = vi.hoisted(() => ({
  appStore: {
    cachedPublicSettings: {} as Record<string, unknown>,
    siteName: 'Fallback site',
    siteLogo: '',
    docUrl: '',
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
  },
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
    user: null as { email?: string } | null,
    checkAuth: vi.fn(),
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore,
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

function mountHome(settings: Record<string, unknown> = {}) {
  appStore.cachedPublicSettings = {
    site_name: 'Test site',
    site_subtitle: 'Test subtitle',
    ...settings,
  }

  return mount(HomeView, {
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        LocaleSwitcher: { template: '<div data-testid="locale-switcher" />' },
        ClientIntroduction: { props: ['variant'], template: '<section data-testid="home-introduction" :data-variant="variant" />' },
        Icon: { template: '<span data-testid="icon" />' },
      },
    },
  })
}

describe('HomeView compact mode', () => {
  beforeEach(() => {
    authStore.isAuthenticated = false
    authStore.isAdmin = false
    authStore.user = null
    authStore.checkAuth.mockClear()
    appStore.fetchPublicSettings.mockClear()
    localStorage.clear()
    vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: false } as MediaQueryList)
  })

  it('renders custom HTML ahead of compact mode', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      home_content: '<section id="custom-home">Custom home</section>',
    })

    expect(wrapper.get('#custom-home').text()).toBe('Custom home')
    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="home-introduction"]').exists()).toBe(false)
  })

  it('renders custom URL content ahead of compact mode', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      home_content: ' https://example.com/home ',
    })

    expect(wrapper.get('iframe').attributes('src')).toBe('https://example.com/home')
    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="home-introduction"]').exists()).toBe(false)
  })

  it('treats whitespace-only custom content as empty and selects compact mode', () => {
    const wrapper = mountHome({ compact_home_enabled: true, home_content: ' \n\t ' })

    expect(wrapper.get('[data-testid="compact-home"]').text()).toContain('Test site')
  })

  it.each([undefined, false])('selects the default home when compact mode is %s', (enabled) => {
    const settings = enabled === undefined ? {} : { compact_home_enabled: enabled }
    const wrapper = mountHome(settings)

    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
    expect(wrapper.get('main').find('[data-testid="home-introduction"]').exists()).toBe(true)
  })

  it('includes the introduction in compact mode', () => {
    const wrapper = mountHome({ compact_home_enabled: true })

    expect(wrapper.get('main').get('[data-testid="home-introduction"]').attributes('data-variant')).toBe('home')
    expect(wrapper.find('details').exists()).toBe(false)
  })

  it('keeps provider information closed until an experienced user opens it', () => {
    const wrapper = mountHome()
    const details = wrapper.get('details')

    expect(details.get('summary').text()).toBe('clientIntroduction.advanced')
    expect(details.attributes('open')).toBeUndefined()
    expect(details.text()).toContain('home.providers.title')
  })

  describe.each([false, true])('client navigation with compact mode %s', (compact) => {
    it.each([
      { role: 'guest', authenticated: false, admin: false, destination: '/login' },
      { role: 'user', authenticated: true, admin: false, destination: '/dashboard' },
      { role: 'admin', authenticated: true, admin: true, destination: '/admin/dashboard' },
    ])('offers console and download links for $role', ({ authenticated, admin, destination }) => {
      authStore.isAuthenticated = authenticated
      authStore.isAdmin = admin
      const wrapper = mountHome({
        compact_home_enabled: compact,
        model_plaza_enabled: true,
        model_plaza_require_auth: false,
      })
      const links = wrapper.get('header').findAllComponents(RouterLinkStub)

      expect(links.map((link) => [link.text(), link.props('to')])).toEqual([
        ['clientIntroduction.console', destination],
        ['clientIntroduction.clientDownload', '/download'],
      ])
      expect(authStore.checkAuth).toHaveBeenCalledOnce()
      expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
    })
  })

  describe.each([false, true])('deployment without the client (compact mode %s)', (compact) => {
    it('replaces the download button with the web workspace', () => {
      const wrapper = mountHome({ compact_home_enabled: compact, client_download_enabled: false, playground_enabled: true })
      const links = wrapper.get('header').findAllComponents(RouterLinkStub)

      expect(links.map((link) => [link.text(), link.props('to')])).toEqual([
        ['clientIntroduction.console', '/login'],
        ['homeIntro.ctaWeb', { path: '/login', query: { redirect: '/playground' } }],
      ])
      expect(wrapper.html()).not.toContain('/download')
    })

    it('only keeps the console link when the web workspace is also off', () => {
      const wrapper = mountHome({ compact_home_enabled: compact, client_download_enabled: false, playground_enabled: false })
      const links = wrapper.get('header').findAllComponents(RouterLinkStub)

      expect(links.map((link) => link.text())).toEqual(['clientIntroduction.console'])
    })
  })
})
