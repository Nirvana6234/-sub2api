import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

const getBalanceExpiry = vi.fn()
vi.mock('@/api/payment', () => ({ paymentAPI: { getBalanceExpiry: (...a: unknown[]) => getBalanceExpiry(...a) } }))

import UserBalanceExpiryNotice from '../UserBalanceExpiryNotice.vue'
import type { BalanceExpiryView, BalanceLot } from '@/types/payment'

const DAY = 86400000

function lot(id: number, remaining: number, inDays: number): BalanceLot {
  return {
    id,
    amount: remaining,
    remaining,
    expired_amount: 0,
    status: 'active',
    credited_at: new Date(Date.now() - 3 * DAY).toISOString(),
    expires_at: new Date(Date.now() + inDays * DAY).toISOString(),
  }
}

function view(over: Partial<BalanceExpiryView> = {}): BalanceExpiryView {
  return {
    enabled: true,
    days: 30,
    permanent_balance: 0,
    expiring_balance: 0,
    lots: [],
    expired: [],
    ...over,
  }
}

async function mountWith(v: BalanceExpiryView | 'fail', balance = 12.5) {
  if (v === 'fail') getBalanceExpiry.mockImplementation(() => Promise.reject(new Error('boom')))
  else getBalanceExpiry.mockResolvedValue({ data: v })
  const wrapper = mount(UserBalanceExpiryNotice, { props: { balance } })
  await flushPromises()
  return wrapper
}

describe('UserBalanceExpiryNotice', () => {
  beforeEach(() => {
    getBalanceExpiry.mockReset()
  })

  it('shows nothing when the feature is off and there is nothing expiring', async () => {
    const wrapper = await mountWith(view({ enabled: false }))
    expect(wrapper.text()).toBe('')
  })

  it('shows nothing when the request fails', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const wrapper = await mountWith('fail')
    expect(wrapper.text()).toBe('')
    warn.mockRestore()
  })

  it('shows the balance, the earliest expiry and the policy', async () => {
    const wrapper = await mountWith(
      view({ permanent_balance: 2.5, expiring_balance: 10, lots: [lot(1, 10, 20)] }),
    )
    const text = wrapper.text()
    expect(text).toContain('$12.50')
    expect(text).toContain('balanceExpiry.nextExpire')
    expect(text).toContain('"days":20')
    expect(text).toContain('balanceExpiry.policy:{"days":30}')
    expect(wrapper.classes().join(' ')).not.toContain('amber')
  })

  it('turns urgent when a lot expires within a week', async () => {
    const wrapper = await mountWith(view({ expiring_balance: 5, lots: [lot(1, 5, 2)] }))
    expect(wrapper.text()).toContain('balanceExpiry.soon')
    expect(wrapper.classes().join(' ')).toContain('amber')
  })

  it('still reminds about a recent clear-out after the feature was switched off', async () => {
    const expired: BalanceLot = {
      ...lot(9, 0, -1),
      status: 'expired',
      expired_amount: 4.2,
      settled_at: new Date(Date.now() - DAY).toISOString(),
    }
    const wrapper = await mountWith(view({ enabled: false, expired: [expired] }))
    expect(wrapper.text()).toContain('balanceExpiry.lastExpired')
    expect(wrapper.text()).toContain('$4.20')
  })
})
