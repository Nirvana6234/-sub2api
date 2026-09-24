import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mount, RouterLinkStub } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'
import ClientIntroduction from '../ClientIntroduction.vue'

const { appStore, authStore } = vi.hoisted(() => ({
  appStore: { cachedPublicSettings: {} as Record<string, unknown> },
  authStore: { isAuthenticated: false, isAdmin: false, user: null as null | { recharge_disabled?: boolean } },
}))
vi.mock('@/stores', () => ({ useAppStore: () => appStore, useAuthStore: () => authStore }))

const { trialState } = vi.hoisted(() => ({ trialState: { enabled: false } }))
vi.mock('@/api/trial', () => ({ fetchGuestTrialState: vi.fn(async () => ({ ...trialState })) }))

function render(settings: Record<string, unknown>) {
  appStore.cachedPublicSettings = { site_name: '共飞 AI', api_base_url: 'https://api.example.com', ...settings }
  return mount(ClientIntroduction, {
    props: { variant: 'home' },
    global: {
      plugins: [createI18n({
        legacy: false,
        locale: 'zh',
        messages: { zh },
        // The test alias uses the runtime-only build; interpolate {name} by hand.
        messageCompiler: (message) => (ctx: { named: (key: string) => unknown }) =>
          typeof message === 'string' ? message.replace(/\{(\w+)\}/g, (_, key) => String(ctx.named(key))) : '',
      })],
      stubs: { RouterLink: RouterLinkStub, Icon: true },
    },
  })
}

const wayIds = (wrapper: ReturnType<typeof render>) =>
  wrapper.findAll('[data-testid^="home-way-"]').map((way) => way.attributes('data-testid'))

describe('ClientIntroduction home variant', () => {
  beforeEach(() => {
    authStore.isAuthenticated = false
    trialState.enabled = false
  })

  it('tells the gateway story: many models inside Codex, plugins and the web workspace', () => {
    const text = render({ playground_enabled: true }).text()
    expect(text).toContain('都在 Codex 里用')
    expect(text).toContain('Claude Code、Cursor、Cline、Cherry Studio')
    expect(text).toContain('直接在网页上聊天、生图、改图')
  })

  it('leads with the client when it is offered, then plugins, then the web workspace', () => {
    const wrapper = render({ playground_enabled: true, client_download_enabled: true })
    expect(wayIds(wrapper)).toEqual(['home-way-client', 'home-way-api', 'home-way-web'])
    expect(wrapper.get('.hero-actions').findComponent(RouterLinkStub).props('to')).toBe('/download')
    // 客户端路线下，手动写配置收进折叠里
    expect(wrapper.get('[data-testid="codex-manual-config"]').element.tagName).toBe('DETAILS')
    expect(wrapper.text()).toContain('节省 Token')
  })

  it('offers a Start working button as large as the client button', () => {
    const withClient = render({ client_download_enabled: true }).get('[data-testid="home-start-work"]')
    expect(withClient.text()).toBe('开始工作')
    expect(withClient.classes()).toEqual(expect.arrayContaining(['studio-button', 'studio-button-outline']))
    // 没有客户端时，它就是唯一的主按钮
    const withoutClient = render({ client_download_enabled: false }).get('[data-testid="home-start-work"]')
    expect(withoutClient.classes()).toContain('studio-button')
    expect(withoutClient.classes()).not.toContain('studio-button-outline')
  })

  it('never mentions the client on the deployment without it', () => {
    const wrapper = render({ playground_enabled: true, client_download_enabled: false })
    expect(wayIds(wrapper)).toEqual(['home-way-api', 'home-way-web'])
    expect(wrapper.text()).not.toContain('客户端')
    const targets = wrapper.findAllComponents(RouterLinkStub).map((link) => JSON.stringify(link.props('to')))
    expect(targets.some((to) => to.includes('/download'))).toBe(false)
    // 没有客户端时直接展示手动配置
    expect(wrapper.get('[data-testid="codex-manual-config"]').element.tagName).toBe('DIV')
  })

  it('drops the web workspace when it is turned off', () => {
    const wrapper = render({ playground_enabled: false, client_download_enabled: false })
    expect(wayIds(wrapper)).toEqual(['home-way-api'])
    expect(wrapper.text()).not.toContain('网页工作台')
  })

  it('fills the Codex config with this site endpoint', () => {
    const wrapper = render({})
    expect(wrapper.get('[data-testid="codex-base-url"]').text()).toBe('"https://api.example.com/v1"')
  })

  it('does not mention recharging on the home page', () => {
    expect(render({ playground_enabled: true, client_download_enabled: true }).text()).not.toContain('充值')
  })

  it('offers the no-sign-up trial to guests when the admin opened it', async () => {
    trialState.enabled = true
    const wrapper = render({ playground_enabled: true, client_download_enabled: true })
    await flushPromises()
    const cta = wrapper.get('[data-testid="home-trial-cta"]')
    expect(cta.text()).toContain('免费试用')
    expect(cta.text()).toContain('免注册')
    const trialLink = wrapper.findAllComponents(RouterLinkStub).find((link) => link.text().includes('免费试用'))
    expect(trialLink?.props('to')).toBe('/trial')
    const webWay = wrapper.get('[data-testid="home-way-web"]').findComponent(RouterLinkStub)
    expect(webWay.props('to')).toBe('/trial')
  })

  it('keeps the full workspace entry for signed-in users even when the trial is open', async () => {
    trialState.enabled = true
    authStore.isAuthenticated = true
    const wrapper = render({ playground_enabled: true })
    await flushPromises()
    expect(wrapper.find('[data-testid="home-trial-cta"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('打开网页工作台')
  })

  it('falls back to the workspace link when the trial is closed', async () => {
    const wrapper = render({ playground_enabled: true })
    await flushPromises()
    expect(wrapper.find('[data-testid="home-trial-cta"]').exists()).toBe(false)
  })
})
