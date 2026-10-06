import { describe, expect, it } from 'vitest'

import {
  ADDRESS_CHANGED_NOTICE_DAYS,
  distinctRelayEndpoints,
  keyBaseUrl,
  relayAddressChanged,
  usesSiteAddress
} from '../relayKeyAddress'

const SITE = 'https://api.example.com'
const DAY = 86_400_000

describe('keyBaseUrl', () => {
  it('uses the key\'s own node address, falling back to the site address', () => {
    expect(keyBaseUrl({ relay_base_url: 'https://r1.example.com' }, SITE)).toBe('https://r1.example.com')
    expect(keyBaseUrl({}, SITE)).toBe(SITE)
    expect(keyBaseUrl({ relay_base_url: '  ' }, SITE)).toBe(SITE)
    expect(keyBaseUrl(null, SITE)).toBe(SITE)
    expect(keyBaseUrl(undefined, '')).toBe('')
  })
})

describe('relayAddressChanged', () => {
  const now = Date.parse('2026-10-06T12:00:00Z')
  it('shows the notice only for a recent change', () => {
    expect(relayAddressChanged({ relay_address_changed_at: new Date(now - 2 * DAY).toISOString() }, now)).toBe(true)
    expect(
      relayAddressChanged({ relay_address_changed_at: new Date(now - (ADDRESS_CHANGED_NOTICE_DAYS + 1) * DAY).toISOString() }, now)
    ).toBe(false)
  })
  it('ignores missing, invalid and future timestamps', () => {
    expect(relayAddressChanged({}, now)).toBe(false)
    expect(relayAddressChanged({ relay_address_changed_at: 'not a date' }, now)).toBe(false)
    expect(relayAddressChanged({ relay_address_changed_at: new Date(now + DAY).toISOString() }, now)).toBe(false)
    expect(relayAddressChanged(null, now)).toBe(false)
  })
})

describe('endpoint grouping', () => {
  const keys = [
    { relay_base_url: 'https://r1.example.com' },
    { relay_base_url: 'https://r2.example.com' },
    { relay_base_url: 'https://r1.example.com' },
    { relay_base_url: SITE },
    {}
  ]
  it('lists each node address once, without the site address', () => {
    expect(distinctRelayEndpoints(keys, SITE)).toEqual(['https://r1.example.com', 'https://r2.example.com'])
  })
  it('knows whether any key still uses the site address', () => {
    expect(usesSiteAddress(keys, SITE)).toBe(true)
    expect(usesSiteAddress([{ relay_base_url: 'https://r1.example.com' }], SITE)).toBe(false)
    expect(usesSiteAddress([], SITE)).toBe(true)
  })
})
