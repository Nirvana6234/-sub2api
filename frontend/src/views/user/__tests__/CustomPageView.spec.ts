import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import CustomPageView from '../CustomPageView.vue'

const { appStore, apiBaseUrl } = vi.hoisted(() => ({
  appStore: {
    publicSettingsLoaded: true,
    cachedPublicSettings: { custom_menu_items: [{ id: 'docs', url: 'https://example.com/docs' }] },
  },
  apiBaseUrl: { value: '/api/v1' },
}))

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: 'docs' } }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }) }))
vi.mock('@/stores', () => ({ useAppStore: () => appStore }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: false, user: { id: 7 }, token: 'test-token' }) }))
vi.mock('@/stores/adminSettings', () => ({ useAdminSettingsStore: () => ({ customMenuItems: [] }) }))
vi.mock('@/api/client', () => ({ buildApiUrl: (path: string) => `${apiBaseUrl.value}${path}` }))

let notifyResize: () => void
const wrappers: ReturnType<typeof mount>[] = []

function mountPage() {
  const wrapper = mount(CustomPageView, {
    attachTo: document.body,
    global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true } },
  })
  wrappers.push(wrapper)
  return wrapper
}

function mountEmbed() {
  const wrapper = mountPage()
  const shell = wrapper.get('.custom-embed-shell').element
  const button = wrapper.get<HTMLAnchorElement>('.custom-open-fab').element
  const size = { width: 800, height: 600 }
  let capturedPointer: number | null = null
  Object.defineProperties(shell, {
    clientWidth: { get: () => size.width }, clientHeight: { get: () => size.height },
  })
  Object.defineProperties(button, {
    offsetWidth: { value: 100 }, offsetHeight: { value: 32 },
    offsetLeft: { get: () => Number.parseFloat(button.style.left || '688') },
    offsetTop: { get: () => Number.parseFloat(button.style.top || '12') },
    setPointerCapture: { value: vi.fn((id: number) => { capturedPointer = id }) },
    hasPointerCapture: { value: (id: number) => capturedPointer === id },
    releasePointerCapture: { value: vi.fn(() => { capturedPointer = null }) },
  })
  return { wrapper, button, size }
}

async function pointer(button: HTMLElement, type: string, x: number, y: number, extra = {}) {
  const event = new MouseEvent(type, { clientX: x, clientY: y, bubbles: true, cancelable: true, ...extra })
  Object.defineProperties(event, {
    pointerId: { value: 1 }, isPrimary: { value: true },
  })
  button.dispatchEvent(event)
  await nextTick()
}

function click(button: HTMLElement, detail = 1) {
  const event = new MouseEvent('click', { bubbles: true, cancelable: true, detail })
  button.dispatchEvent(event)
  return event
}

describe('custom page open button', () => {
  beforeEach(() => {
    apiBaseUrl.value = '/api/v1'
    appStore.cachedPublicSettings.custom_menu_items = [{ id: 'docs', url: 'https://example.com/docs' }]
    vi.stubGlobal('ResizeObserver', class {
      constructor(callback: () => void) { notifyResize = callback }
      observe() {}
      disconnect() {}
    })
  })

  afterEach(() => {
    wrappers.splice(0).forEach(wrapper => wrapper.unmount())
    vi.unstubAllGlobals()
  })

  it.each([undefined, false, true])('honors the per-menu hide button setting %s while keeping the iframe', (hidden) => {
    Object.assign(appStore.cachedPublicSettings.custom_menu_items[0], { hide_open_button: hidden })
    const wrapper = mountPage()
    expect(wrapper.find('.custom-open-fab').exists()).toBe(hidden !== true)
    expect(wrapper.get('iframe').attributes('src')).toContain('https://example.com/docs')
  })

  it('preserves the embedded URL, secure link attributes, and normal clicks with small pointer movements', async () => {
    const { wrapper, button } = mountEmbed()
    expect(button.href).toBe(wrapper.get('iframe').attributes('src'))
    expect(button.href).toContain('user_id=7')
    expect(button.href).toContain('token=test-token')
    expect(button.target).toBe('_blank')
    expect(button.rel).toBe('noopener noreferrer')
    await pointer(button, 'pointerdown', 700, 24)
    await pointer(button, 'pointermove', 702, 25)
    await pointer(button, 'pointerup', 702, 25)
    expect(button.style.left).toBe('')
    expect(click(button).defaultPrevented).toBe(false)
    expect(click(button, 0).defaultPrevented).toBe(false)
  })

  it('captures the pointer across iframe content and suppresses only the click following a drag', async () => {
    const { button } = mountEmbed()
    await pointer(button, 'pointerdown', 700, 24)
    expect(button.setPointerCapture).toHaveBeenCalledWith(1)
    await pointer(button, 'pointermove', 200, 124)
    expect(button.style.left).toBe('188px')
    expect(button.style.top).toBe('112px')
    await pointer(button, 'pointerup', 200, 124)
    expect(button.releasePointerCapture).toHaveBeenCalledWith(1)
    expect(click(button).defaultPrevented).toBe(true)
    expect(click(button).defaultPrevented).toBe(false)
    await pointer(button, 'pointerdown', 200, 124)
    await pointer(button, 'pointermove', 220, 124)
    await pointer(button, 'pointerup', 220, 124)
    expect(click(button, 0).defaultPrevented).toBe(false)
  })

  it('keeps the button inside each boundary and reachable when the container shrinks', async () => {
    const { button, size } = mountEmbed()
    await pointer(button, 'pointerdown', 700, 24)
    await pointer(button, 'pointermove', -1000, -1000)
    expect([button.style.left, button.style.top]).toEqual(['0px', '0px'])
    await pointer(button, 'pointermove', 2000, 2000)
    expect([button.style.left, button.style.top]).toEqual(['700px', '568px'])
    await pointer(button, 'pointerup', 2000, 2000)
    size.width = 300
    size.height = 200
    notifyResize()
    await nextTick()
    expect([button.style.left, button.style.top]).toEqual(['200px', '168px'])
  })

  it('stops moving on cancellation or lost pointer capture and permits the next normal click', async () => {
    const { button } = mountEmbed()
    for (const endEvent of ['pointercancel', 'lostpointercapture']) {
      await pointer(button, 'pointerdown', 700, 24)
      await pointer(button, 'pointermove', 500, 124)
      await pointer(button, endEvent, 500, 124)
      const position = button.style.cssText
      await pointer(button, 'pointermove', 400, 224)
      expect(button.style.cssText).toBe(position)
      await pointer(button, 'pointerdown', 500, 124)
      await pointer(button, 'pointerup', 500, 124)
      expect(click(button).defaultPrevented).toBe(false)
    }
  })

  it('leaves secondary mouse button gestures alone', async () => {
    const { button } = mountEmbed()
    await pointer(button, 'pointerdown', 700, 24, { button: 2 })
    await pointer(button, 'pointermove', 500, 124)
    expect(button.setPointerCapture).not.toHaveBeenCalled()
    expect(button.style.left).toBe('')
  })

  it('keeps Markdown pages separate from the embedded-page controls', async () => {
    appStore.cachedPublicSettings.custom_menu_items = [{ id: 'docs', url: 'md:guide' }]
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, text: async () => '# Guide' }))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.custom-open-fab').exists()).toBe(false)
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.get('.markdown-page-content h1').text()).toBe('Guide')
  })

  it('loads page images with the bearer header and revokes their blob URL', async () => {
    appStore.cachedPublicSettings.custom_menu_items = [{ id: 'docs', url: 'md:guide' }]
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        text: async () => [
          '![Guide logo](logo.png)',
          '<img data-page-image-url="https://attacker.example/secret.png" src="/api/v1/pages/guide/images-other/evil.png">',
        ].join('\n'),
      })
      .mockResolvedValueOnce({
        ok: true,
        blob: async () => new Blob(['png'], { type: 'image/png' }),
      })
    vi.stubGlobal('fetch', fetchMock)
    const createObjectURL = vi.fn(() => 'blob:page-image')
    const revokeObjectURL = vi.fn()
    Object.defineProperty(window.URL, 'createObjectURL', { configurable: true, value: createObjectURL })
    Object.defineProperty(window.URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL })

    const wrapper = mountPage()
    await flushPromises()
    await nextTick()
    await flushPromises()
    await nextTick()

    expect(fetchMock).toHaveBeenCalledTimes(2)
    const imageRequest = fetchMock.mock.calls[1]
    expect(imageRequest[0]).toBe('http://localhost:3000/api/v1/pages/guide/images/logo.png')
    expect(imageRequest[1]).toMatchObject({
      headers: { Authorization: 'Bearer test-token' },
      redirect: 'error',
    })
    expect(imageRequest[0]).not.toContain('token=')
    expect(wrapper.get('img').attributes('src')).toBe('blob:page-image')
    expect(wrapper.findAll('img')[1].attributes('src')).toBe('/api/v1/pages/guide/images-other/evil.png')
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes('attacker.example'))).toBe(false)

    wrapper.unmount()
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:page-image')
  })

  it('loads protected images from an externally configured API origin', async () => {
    apiBaseUrl.value = 'https://api.example.test/api/v1'
    appStore.cachedPublicSettings.custom_menu_items = [{ id: 'docs', url: 'md:guide' }]
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        text: async () => '![Guide logo](logo.png)',
      })
      .mockResolvedValueOnce({
        ok: true,
        blob: async () => new Blob(['png'], { type: 'image/png' }),
      })
    vi.stubGlobal('fetch', fetchMock)
    Object.defineProperty(window.URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob:external-page-image'),
    })
    Object.defineProperty(window.URL, 'revokeObjectURL', { configurable: true, value: vi.fn() })

    const wrapper = mountPage()
    await flushPromises()
    await nextTick()
    await flushPromises()
    await nextTick()

    expect(fetchMock.mock.calls[1]?.[0]).toBe(
      'https://api.example.test/api/v1/pages/guide/images/logo.png'
    )
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({
      headers: { Authorization: 'Bearer test-token' },
      redirect: 'error',
    })
    expect(wrapper.get('img').attributes('src')).toBe('blob:external-page-image')
  })

  it('cancels stale protected image requests when the page is unmounted', async () => {
    appStore.cachedPublicSettings.custom_menu_items = [{ id: 'docs', url: 'md:guide' }]
    let resolveImage: ((value: { ok: boolean; blob: () => Promise<Blob> }) => void) | undefined
    const imagePromise = new Promise<{ ok: boolean; blob: () => Promise<Blob> }>((resolve) => {
      resolveImage = resolve
    })
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        text: async () => '![Guide logo](logo.png)',
      })
      .mockReturnValueOnce(imagePromise)
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mountPage()
    await flushPromises()
    await nextTick()
    wrapper.unmount()

    resolveImage?.({
      ok: true,
      blob: async () => new Blob(['png'], { type: 'image/png' }),
    })
    await flushPromises()
    await nextTick()

    expect(fetchMock.mock.calls[1]?.[1]?.signal?.aborted).toBe(true)
  })
})
