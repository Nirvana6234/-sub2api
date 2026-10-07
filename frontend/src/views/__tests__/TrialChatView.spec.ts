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
    localStorage.clear()
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

  describe('chat history kept in this browser', () => {
    const saved = (messages: { role: string; content: string }[], model = '') =>
      localStorage.setItem('guest_trial_history', JSON.stringify({ version: 1, model, messages }))

    it('shows the earlier chat again after a refresh', async () => {
      saved([{ role: 'user', content: '上次的问题' }, { role: 'assistant', content: '上次的回答' }])
      api.fetchGuestTrialState.mockResolvedValue(enabledState)
      const wrapper = render()
      await flushPromises()

      expect(wrapper.text()).toContain('上次的问题')
      expect(wrapper.text()).toContain('上次的回答')
      expect(wrapper.text()).toContain('聊天记录只保存在本机浏览器')
    })

    it('saves each finished turn locally and sends it as context next time', async () => {
      saved([{ role: 'user', content: '上次的问题' }, { role: 'assistant', content: '上次的回答' }])
      api.fetchGuestTrialState.mockResolvedValue(enabledState)
      api.sendGuestTrialChat.mockImplementation(async (_model: string, _messages: unknown, options: { onDelta?: (text: string) => void }) => {
        options.onDelta?.('新的回答')
        return { content: '新的回答', remaining: 19 }
      })
      const wrapper = render()
      await flushPromises()
      await wrapper.get('[data-testid="trial-input"]').setValue('新的问题')
      await wrapper.get('form').trigger('submit')
      await flushPromises()

      expect(api.sendGuestTrialChat.mock.calls[0][1]).toEqual([
        { role: 'user', content: '上次的问题' },
        { role: 'assistant', content: '上次的回答' },
        { role: 'user', content: '新的问题' },
      ])
      const stored = JSON.parse(localStorage.getItem('guest_trial_history') || '{}')
      expect(stored.model).toBe('gpt-5.4-mini')
      expect(stored.messages.map((m: { content: string }) => m.content)).toEqual(['上次的问题', '上次的回答', '新的问题', '新的回答'])
    })

    it('does not keep a question whose send failed', async () => {
      api.fetchGuestTrialState.mockResolvedValue(enabledState)
      api.sendGuestTrialChat.mockRejectedValue(new Error('网络错误'))
      const wrapper = render()
      await flushPromises()
      await wrapper.get('[data-testid="trial-input"]').setValue('发不出去的问题')
      await wrapper.get('form').trigger('submit')
      await flushPromises()

      expect(localStorage.getItem('guest_trial_history')).toBeNull()
    })

    it('comes back to the model used last time when it is still offered', async () => {
      saved([{ role: 'user', content: '问' }, { role: 'assistant', content: '答' }], 'deepseek-v4.1-flash')
      api.fetchGuestTrialState.mockResolvedValue({ ...enabledState, models: ['gpt-5.4-mini', 'deepseek-v4.1-flash'] })
      const wrapper = render()
      await flushPromises()

      expect((wrapper.get('[data-testid="trial-model"]').element as HTMLSelectElement).value).toBe('deepseek-v4.1-flash')
    })

    it('clears the saved chat after the visitor confirms', async () => {
      saved([{ role: 'user', content: '要清掉的问题' }, { role: 'assistant', content: '要清掉的回答' }])
      api.fetchGuestTrialState.mockResolvedValue(enabledState)
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
      const wrapper = render()
      await flushPromises()

      await wrapper.get('[data-testid="trial-clear-history"]').trigger('click')

      expect(confirm).toHaveBeenCalled()
      expect(wrapper.text()).not.toContain('要清掉的问题')
      expect(localStorage.getItem('guest_trial_history')).toBeNull()
      confirm.mockRestore()
    })

    it('ignores a damaged saved chat', async () => {
      localStorage.setItem('guest_trial_history', '{not json')
      api.fetchGuestTrialState.mockResolvedValue(enabledState)
      const wrapper = render()
      await flushPromises()

      expect(wrapper.find('[data-testid="trial-history-bar"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="trial-input"]').exists()).toBe(true)
    })
  })
})
