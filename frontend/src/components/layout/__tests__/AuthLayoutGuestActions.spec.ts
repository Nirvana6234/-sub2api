import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import AuthLayout from '../AuthLayout.vue'

const { appStore, trialState } = vi.hoisted(() => ({
  appStore: { siteName: '共飞 AI', siteLogo: '', cachedPublicSettings: {}, fetchPublicSettings: vi.fn() },
  trialState: { enabled: true },
}))

vi.mock('@/stores', () => ({ useAppStore: () => appStore }))
vi.mock('@/api/trial', () => ({ fetchGuestTrialState: vi.fn(async () => ({ ...trialState })) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

function render(showGuestActions: boolean) {
  return mount(AuthLayout, {
    props: { showGuestActions },
    global: { stubs: { RouterLink: RouterLinkStub, Icon: true } },
  })
}

const target = (wrapper: ReturnType<typeof render>, testid: string) =>
  wrapper.findAllComponents(RouterLinkStub).find((link) => link.attributes('data-testid') === testid)?.props('to')

describe('AuthLayout guest actions', () => {
  beforeEach(() => {
    trialState.enabled = true
  })

  it('offers "continue trial" and "back to home" on the login page', async () => {
    const wrapper = render(true)
    await flushPromises()
    expect(target(wrapper, 'auth-continue-trial')).toBe('/trial')
    expect(target(wrapper, 'auth-back-home')).toBe('/home')
  })

  it('hides "continue trial" when the admin has not opened the trial', async () => {
    trialState.enabled = false
    const wrapper = render(true)
    await flushPromises()
    expect(target(wrapper, 'auth-continue-trial')).toBeUndefined()
    expect(target(wrapper, 'auth-back-home')).toBe('/home')
  })

  it('stays out of the way on callback-style auth pages', async () => {
    const wrapper = render(false)
    await flushPromises()
    expect(wrapper.find('[data-testid="auth-guest-actions"]').exists()).toBe(false)
  })
})
