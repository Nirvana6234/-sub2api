import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'
import GuestTrialSettingsCard from '../GuestTrialSettingsCard.vue'

const { appStore, settingsApi, keysApi, playgroundApi } = vi.hoisted(() => ({
  appStore: { showSuccess: vi.fn() },
  settingsApi: { getGuestTrialConfig: vi.fn(), updateGuestTrialConfig: vi.fn() },
  keysApi: { list: vi.fn() },
  playgroundApi: { fetchPlaygroundModels: vi.fn() },
}))

vi.mock('@/stores', () => ({ useAppStore: () => appStore }))
vi.mock('@/api/admin/settings', () => settingsApi)
vi.mock('@/api/keys', () => ({ keysAPI: keysApi }))
vi.mock('@/features/playground/api', () => playgroundApi)

const baseConfig = {
  enabled: true,
  api_key_id: 513,
  models: ['grok-4.7'],
  daily_per_visitor: 20,
  daily_global: 1000,
  max_input_chars: 6000,
  max_output_tokens: 1000,
  require_captcha: true,
}

function render() {
  return mount(GuestTrialSettingsCard, {
    global: {
      plugins: [createI18n({
        legacy: false,
        locale: 'zh',
        messages: { zh },
        messageCompiler: (message) => (ctx: { named: (key: string) => unknown }) =>
          typeof message === 'string' ? message.replace(/\{(\w+)\}/g, (_, key) => String(ctx.named(key))) : '',
      })],
      stubs: { RouterLink: RouterLinkStub, Toggle: true },
    },
  })
}

describe('GuestTrialSettingsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    settingsApi.getGuestTrialConfig.mockResolvedValue({ ...baseConfig })
    settingsApi.updateGuestTrialConfig.mockImplementation(async (config) => config)
    keysApi.list.mockResolvedValue({ items: [{ id: 513, name: '试用' }, { id: 600, name: '多模型' }] })
    playgroundApi.fetchPlaygroundModels.mockImplementation(async (keyId: number) =>
      keyId === 513
        ? [{ id: 'grok-4.7' }, { id: 'grok-4.7-fast' }, { id: 'grok-code' }]
        : [{ id: 'gpt-5.5' }, { id: 'claude-haiku-4-5' }],
    )
  })

  it('lists the selected key models as checkboxes and saves the ticked models in order', async () => {
    const wrapper = render()
    await flushPromises()

    expect(playgroundApi.fetchPlaygroundModels).toHaveBeenCalledWith(513, expect.any(AbortSignal))
    expect((wrapper.get('[data-testid="guest-trial-model-grok-4.7"]').element as HTMLInputElement).checked).toBe(true)

    await wrapper.get('[data-testid="guest-trial-model-grok-code"]').setValue(true)
    await wrapper.get('[data-testid="guest-trial-model-grok-4.7-fast"]').setValue(true)
    await wrapper.get('[data-testid="guest-trial-save"]').trigger('click')
    await flushPromises()

    expect(settingsApi.updateGuestTrialConfig).toHaveBeenCalledWith(
      expect.objectContaining({ models: ['grok-4.7', 'grok-code', 'grok-4.7-fast'] }),
    )
  })

  it('moves a model to the front when it is made the default', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="guest-trial-model-grok-code"]').setValue(true)

    const makeDefault = wrapper.get('[data-testid="guest-trial-selected"]').findAll('button').find((button) => button.text() === '设为默认')
    await makeDefault!.trigger('click')
    await wrapper.get('[data-testid="guest-trial-save"]').trigger('click')
    await flushPromises()

    expect(settingsApi.updateGuestTrialConfig).toHaveBeenCalledWith(expect.objectContaining({ models: ['grok-code', 'grok-4.7'] }))
  })

  it('reloads candidates when the key changes and keeps manually added models', async () => {
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-testid="guest-trial-key"]').setValue('600')
    await flushPromises()
    expect(playgroundApi.fetchPlaygroundModels).toHaveBeenLastCalledWith(600, expect.any(AbortSignal))
    expect(wrapper.find('[data-testid="guest-trial-model-gpt-5.5"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="guest-trial-model-grok-code"]').exists()).toBe(false)

    const custom = wrapper.get('[data-testid="guest-trial-custom-model"]')
    await custom.setValue(' my-custom-model ')
    await custom.trigger('keydown', { key: 'Enter' })
    await wrapper.get('[data-testid="guest-trial-save"]').trigger('click')
    await flushPromises()

    expect(settingsApi.updateGuestTrialConfig).toHaveBeenCalledWith(
      expect.objectContaining({ api_key_id: 600, models: ['grok-4.7', 'my-custom-model'] }),
    )
  })
})
