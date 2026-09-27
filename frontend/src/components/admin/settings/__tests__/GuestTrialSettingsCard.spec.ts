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

// 生产上的实际形态：试用密钥在 grok 分组，GPT / Claude 在各自分组的密钥里
const modelsByKey: Record<number, string[]> = {
  513: ['grok-4.7', 'grok-code'],
  29: ['gpt-5.5', 'gpt-5.4-mini'],
  2: ['claude-haiku-4-5', 'claude-sonnet-5'],
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

async function save(wrapper: ReturnType<typeof render>) {
  await wrapper.get('[data-testid="guest-trial-save"]').trigger('click')
  await flushPromises()
  return settingsApi.updateGuestTrialConfig.mock.calls.at(-1)?.[0]
}

describe('GuestTrialSettingsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    settingsApi.getGuestTrialConfig.mockResolvedValue({ ...baseConfig })
    settingsApi.updateGuestTrialConfig.mockImplementation(async (config) => config)
    keysApi.list.mockResolvedValue({
      items: [
        { id: 513, name: '试用', group_id: 46, group: { name: 'grok（兼容codex）' }, auto_group: false },
        { id: 29, name: 'GPT', group_id: 20, group: { name: 'plus-free' }, auto_group: false },
        { id: 2, name: 'claude', group_id: 14, group: { name: 'claude_正价' }, auto_group: false },
      ],
    })
    playgroundApi.fetchPlaygroundModels.mockImplementation(async (keyId: number) => (modelsByKey[keyId] || []).map((id) => ({ id })))
  })

  it('lets the trial offer models from several platforms, each bound to the key that serves it', async () => {
    const wrapper = render()
    await flushPromises()
    expect((wrapper.get('[data-testid="guest-trial-model-513-grok-4.7"]').element as HTMLInputElement).checked).toBe(true)

    await wrapper.get('[data-testid="guest-trial-key-29"]').setValue(true)
    await wrapper.get('[data-testid="guest-trial-key-2"]').setValue(true)
    await flushPromises()
    await wrapper.get('[data-testid="guest-trial-model-29-gpt-5.5"]').setValue(true)
    await wrapper.get('[data-testid="guest-trial-model-2-claude-haiku-4-5"]').setValue(true)

    expect(await save(wrapper)).toEqual(expect.objectContaining({
      api_key_id: 513,
      models: ['grok-4.7', 'gpt-5.5', 'claude-haiku-4-5'],
      model_keys: { 'grok-4.7': 513, 'gpt-5.5': 29, 'claude-haiku-4-5': 2 },
    }))
  })

  it('restores per-model keys and follows the default model when choosing the default key', async () => {
    settingsApi.getGuestTrialConfig.mockResolvedValue({ ...baseConfig, models: ['grok-4.7', 'gpt-5.5'], model_keys: { 'gpt-5.5': 29 } })
    const wrapper = render()
    await flushPromises()

    expect((wrapper.get('[data-testid="guest-trial-model-29-gpt-5.5"]').element as HTMLInputElement).checked).toBe(true)
    const makeDefault = wrapper.get('[data-testid="guest-trial-selected"]').findAll('button').find((button) => button.text() === '设为默认')
    await makeDefault!.trigger('click')

    expect(await save(wrapper)).toEqual(expect.objectContaining({
      api_key_id: 29,
      models: ['gpt-5.5', 'grok-4.7'],
      model_keys: { 'gpt-5.5': 29, 'grok-4.7': 513 },
    }))
  })

  it('drops the models of a key that is unticked and adds manual models to the chosen key', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="guest-trial-key-29"]').setValue(true)
    await flushPromises()
    await wrapper.get('[data-testid="guest-trial-model-29-gpt-5.5"]').setValue(true)

    await wrapper.get('[data-testid="guest-trial-custom-key"]').setValue('29')
    const custom = wrapper.get('[data-testid="guest-trial-custom-model"]')
    await custom.setValue(' gpt-5.6-sol ')
    await custom.trigger('keydown', { key: 'Enter' })

    await wrapper.get('[data-testid="guest-trial-key-513"]').setValue(false)
    expect(wrapper.find('[data-testid="guest-trial-key-models-513"]').exists()).toBe(false)

    expect(await save(wrapper)).toEqual(expect.objectContaining({
      api_key_id: 29,
      models: ['gpt-5.5', 'gpt-5.6-sol'],
      model_keys: { 'gpt-5.5': 29, 'gpt-5.6-sol': 29 },
    }))
  })
})
