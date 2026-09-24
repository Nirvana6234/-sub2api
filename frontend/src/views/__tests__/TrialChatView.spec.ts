import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'
import TrialChatView from '../TrialChatView.vue'

const { appStore, authStore, api } = vi.hoisted(() => {
  class GuestTrialError extends Error {
    constructor(message: string, readonly reason: string, readonly status: number) {
      super(message)
    }
  }
  return {
    appStore: { cachedPublicSettings: {} as Record<string, unknown>, publicSettingsLoaded: true, fetchPublicSettings: vi.fn(), siteName: '共飞 AI', siteLogo: '' },
    authStore: { isAuthenticated: false },
    api: {
      GuestTrialError,
      fetchGuestTrialState: vi.fn(),
      sendGuestTrialChat: vi.fn(),
      verifyGuestTrial: vi.fn(),
    },
  }
})

vi.mock('@/stores', () => ({ useAppStore: () => appStore, useAuthStore: () => authStore }))
vi.mock('@/features/playground/markdown', () => ({ renderPlaygroundMarkdown: (source: string) => `<p>${source}</p>` }))
vi.mock('@/api/trial', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/trial')>()
  return { ...actual, ...api }
})

const enabledState = {
  enabled: true,
  models: ['gpt-5.4-mini'],
  default_model: 'gpt-5.4-mini',
  daily_limit: 20,
  remaining: 20,
  max_input_chars: 6000,
  captcha_required: false,
}

function render() {
  return mount(TrialChatView, {
    global: {
      plugins: [createI18n({
        legacy: false,
        locale: 'zh',
        messages: { zh },
        messageCompiler: (message) => (ctx: { named: (key: string) => unknown }) =>
          typeof message === 'string' ? message.replace(/\{(\w+)\}/g, (_, key) => String(ctx.named(key))) : '',
      })],
      stubs: { RouterLink: RouterLinkStub, Icon: true, LoadingSpinner: true, CaptchaChallenge: true },
    },
  })
}

describe('TrialChatView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    authStore.isAuthenticated = false
    appStore.cachedPublicSettings = {}
  })

  it('shows a sign-up prompt when the trial is closed', async () => {
    api.fetchGuestTrialState.mockResolvedValue({ ...enabledState, enabled: false })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.find('[data-testid="trial-disabled"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="trial-chat"]').exists()).toBe(false)
  })

  it('tells visitors they can chat right away, text only', async () => {
    api.fetchGuestTrialState.mockResolvedValue(enabledState)
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('不用注册，直接开聊')
    expect(wrapper.text()).toContain('免注册')
    expect(wrapper.text()).toContain('试用版仅支持文字聊天')
    expect(wrapper.get('[data-testid="trial-remaining"]').text()).toContain('20 / 20')
  })

  it('streams a reply and updates the remaining count', async () => {
    api.fetchGuestTrialState.mockResolvedValue(enabledState)
    api.sendGuestTrialChat.mockImplementation(async (_model: string, _messages: unknown, options: { onDelta?: (text: string) => void }) => {
      options.onDelta?.('你好，')
      options.onDelta?.('有什么可以帮你？')
      return { content: '你好，有什么可以帮你？', remaining: 19 }
    })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="trial-input"]').setValue('你好')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const [model, messages] = api.sendGuestTrialChat.mock.calls[0]
    expect(model).toBe('gpt-5.4-mini')
    expect(messages).toEqual([{ role: 'user', content: '你好' }])
    expect(wrapper.text()).toContain('有什么可以帮你？')
    expect(wrapper.get('[data-testid="trial-remaining"]').text()).toContain('19 / 20')
  })

  it('switches to the sign-up prompt once the daily trial is used up', async () => {
    api.fetchGuestTrialState.mockResolvedValue(enabledState)
    api.sendGuestTrialChat.mockRejectedValue(new api.GuestTrialError('今天的试用次数已用完', 'GUEST_TRIAL_QUOTA_EXHAUSTED', 429))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="trial-input"]').setValue('再问一个')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[data-testid="trial-upsell"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="trial-input"]').exists()).toBe(false)
  })

  it('asks for the inline captcha before the first message', async () => {
    api.fetchGuestTrialState.mockResolvedValue({ ...enabledState, captcha_required: true })
    appStore.cachedPublicSettings = { turnstile_enabled: true, turnstile_site_key: 'site-key' }
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="trial-input"]').setValue('你好')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.sendGuestTrialChat).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="trial-error"]').text()).toContain('人机验证')
  })
})
