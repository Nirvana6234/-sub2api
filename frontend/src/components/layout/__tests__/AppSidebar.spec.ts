import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })
})

describe('AppSidebar purchase link', () => {
  it('always opens the built-in purchase route', () => {
    expect(componentSource).toContain("path: '/purchase'")
    expect(componentSource).toContain('featureFlag: flagPurchase')
    expect(componentSource).not.toContain('externalUrl: purchaseSubscriptionUrl.value')
  })

  it('places purchase directly below the API keys entry', () => {
    const apiKeysIndex = componentSource.indexOf("{ path: '/keys', label: t('nav.apiKeys'), icon: KeyIcon }")
    const purchaseIndex = componentSource.indexOf("path: '/purchase'")
    const playgroundIndex = componentSource.indexOf("{ path: '/playground', label: t('nav.playground')")

    expect(apiKeysIndex).toBeGreaterThanOrEqual(0)
    expect(purchaseIndex).toBeGreaterThan(apiKeysIndex)
    expect(purchaseIndex).toBeLessThan(playgroundIndex)
  })
})

describe('AppSidebar ticket notification', () => {
  it('renders a shared unread indicator for personal and admin ticket links', () => {
    expect(componentSource).toContain('sidebar-notification-dot')
    expect(componentSource).toContain('ticketUnreadCountFor')
    expect(componentSource).toContain('ticketStore.adminUnreadCount')
    expect(componentSource).toContain('ticketStore.userUnreadCount')
  })
})

describe('AppSidebar header styles', () => {
  it('does not clip the version badge dropdown', () => {
    const sidebarHeaderBlockMatch = styleSource.match(/\.sidebar-header\s*\{[\s\S]*?\n {2}\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarHeaderBlockMatch?.[0]).not.toContain('@apply overflow-hidden;')
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
  })
})
