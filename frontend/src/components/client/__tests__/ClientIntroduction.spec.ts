import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh/onboarding'
import en from '@/i18n/locales/en/onboarding'
import ClientIntroduction from '../ClientIntroduction.vue'

const { appStore } = vi.hoisted(() => ({
  appStore: { cachedPublicSettings: {} as Record<string, string> },
}))
vi.mock('@/stores', () => ({ useAppStore: () => appStore, useAuthStore: () => ({ user: null }) }))

function render(downloadPage = false, locale = 'zh') {
  return mount(ClientIntroduction, {
    props: { downloadPage },
    global: {
      plugins: [createI18n({
        legacy: false,
        locale,
        messages: { zh, en },
        // The test alias uses the runtime-only build; these messages are plain text.
        messageCompiler: (message) => () => typeof message === 'string' ? message : '',
      })],
      stubs: { RouterLink: RouterLinkStub, Icon: true },
    },
  })
}

describe('ClientIntroduction', () => {
  beforeEach(() => { appStore.cachedPublicSettings = {} })

  it('does not mention recharging when recharge is turned off', () => {
    appStore.cachedPublicSettings = { payment_enabled: false, backup_payment_enabled: false } as unknown as Record<string, string>
    const text = render().text()
    expect(text).toContain(zh.clientIntroduction.balanceNoRecharge)
    expect(text).not.toContain('充值')
  })

  it('offers a download route from home and an in-page download anchor on the guide', () => {
    expect(render().findComponent(RouterLinkStub).props('to')).toBe('/download')
    expect(render(true).get('a[href="#downloads"]').text()).toContain('选我的电脑')
  })

  it('switches creative scenarios and explains the tools needed for the selected task', async () => {
    const wrapper = render()
    const buttons = wrapper.findAll('button')
    // 主要业务在前：写代码 → 写作 → 设计图片 → 3D 模型
    expect(buttons.slice(0, 4).map((button) => button.text())).toEqual(['写代码 / 网页', '写作 / 学习', '设计图片', '做 3D 模型'])
    expect(buttons[0].attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('.web-art').exists()).toBe(true)
    await buttons[3].trigger('click')
    expect(buttons[3].attributes('aria-pressed')).toBe('true')
    expect(buttons[0].attributes('aria-pressed')).toBe('false')
    expect(wrapper.get('#example-conversation').text()).toContain('需要安装 Blender')
    expect(wrapper.find('.preview-model').exists()).toBe(true)
    expect(wrapper.get('#example-conversation').text()).not.toContain('旅行视频')
    await buttons[2].trigger('click')
    expect(wrapper.get('#example-conversation').text()).toContain('需要可用的图片模型或工具')
    expect(wrapper.find('.preview-image').exists()).toBe(true)
  })

  it('explains requirements and paid usage before the first-use steps', () => {
    const wrapper = render()
    const checklist = wrapper.get('#requirements').text()
    for (const text of ['Windows 64 位', '能正常上网', '邮箱和共飞账号', '使用 AI 会消耗余额或额度', '手机、平板不能安装']) {
      expect(checklist).toContain(text)
    }
    expect(wrapper.get('#quick-start').text()).toContain('使用时保持共飞客户端运行')
    expect(checklist).toContain('复杂视频、模型和渲染，对电脑性能也有要求')
  })

  it.each(['', 'javascript:alert(1)'])('does not advertise Mac availability for an unpublished or unsafe URL: %s', (url) => {
    appStore.cachedPublicSettings.client_download_direct_url_mac = url
    expect(render().text()).not.toContain('Apple 芯片')
  })

  it('shows Apple silicon support and Intel exclusion only when Mac is available', () => {
    appStore.cachedPublicSettings.client_download_direct_url_mac = 'https://example.com/mac.tar.gz'
    expect(render().get('#requirements').text()).toContain('目前支持 Apple 芯片（M 系列），暂不支持 Intel Mac')
  })

  it('also renders the introduction and examples in English', async () => {
    const wrapper = render(false, 'en')
    expect(wrapper.get('h1').text()).toContain('A creation in your hands')
    await wrapper.findAll('button')[3].trigger('click')
    expect(wrapper.get('#example-conversation').text()).toContain('Requires software such as Blender')
    expect(wrapper.text()).not.toContain('clientIntroduction.')
  })
})
