import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'

import UserDashboardServiceEntry from '../UserDashboardServiceEntry.vue'

const { appStore, authStore } = vi.hoisted(() => ({
  appStore: {
    cachedPublicSettings: {} as Record<string, unknown>,
    docUrl: '',
    showSuccess: vi.fn(),
    showError: vi.fn(),
  },
  authStore: { user: null as { recharge_disabled?: boolean } | null },
}))

vi.mock('@/stores', () => ({ useAppStore: () => appStore, useAuthStore: () => authStore }))
vi.mock('@/stores/app', () => ({ useAppStore: () => appStore }))
vi.mock('@/i18n', () => ({ i18n: { global: { t: (key: string) => key } } }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const stats = { total_api_keys: 3, active_api_keys: 2 } as never

function mountEntry(settings: Record<string, unknown>) {
  appStore.cachedPublicSettings = settings
  return mount(UserDashboardServiceEntry, {
    props: { stats },
    global: { stubs: { RouterLink: RouterLinkStub, Icon: true } },
  })
}

const cardIds = (wrapper: ReturnType<typeof mountEntry>) =>
  wrapper.findAll('[data-testid^="service-card-"]').map((card) => card.attributes('data-testid'))

describe('UserDashboardServiceEntry', () => {
  beforeEach(() => {
    authStore.user = null
  })

  it('puts the client first on the full deployment, then API, then web', () => {
    const wrapper = mountEntry({ playground_enabled: true, client_download_enabled: true })
    expect(cardIds(wrapper)).toEqual(['service-card-client', 'service-card-api', 'service-card-web'])
  })

  it('never mentions the client on the deployment without it', () => {
    const wrapper = mountEntry({ playground_enabled: true, client_download_enabled: false })
    expect(cardIds(wrapper)).toEqual(['service-card-api', 'service-card-web'])
    expect(wrapper.html()).not.toContain('/download')
    expect(wrapper.text()).not.toContain('client')
  })

  it('keeps the API card even when the web workspace is off', () => {
    const wrapper = mountEntry({ playground_enabled: false, client_download_enabled: false })
    expect(cardIds(wrapper)).toEqual(['service-card-api'])
    expect(wrapper.text()).toContain('dashboard.serviceEntry.subtitleSingle')
  })

  it('shows the configured API endpoint with a /v1 suffix', () => {
    const wrapper = mountEntry({ api_base_url: 'https://api.example.com/' })
    expect(wrapper.get('[data-testid="service-api-endpoint"]').text()).toBe('https://api.example.com/v1')
  })

  it('does not mention recharging anywhere', () => {
    const wrapper = mountEntry({ playground_enabled: true, client_download_enabled: true })
    expect(wrapper.html()).not.toMatch(/recharge|purchase|充值/)
  })
})
