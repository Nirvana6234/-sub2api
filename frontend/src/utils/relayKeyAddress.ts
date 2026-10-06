import type { ApiKey } from '@/types'

/**
 * Master / relay-node split: an API key is pinned to one node and must be used against that node's address,
 * not the site-wide api_base_url. Keys the backend has not assigned (or master-assigned keys with no
 * configured api_base_url) carry no relay_base_url and keep using the site address.
 */

/** How long a key keeps its "address changed" notice. */
export const ADDRESS_CHANGED_NOTICE_DAYS = 14

type KeyAddressFields = Pick<ApiKey, 'relay_base_url' | 'relay_address_changed_at'>

/** The address to put into client configs for this key. */
export function keyBaseUrl(key: Partial<KeyAddressFields> | null | undefined, siteBaseUrl: string): string {
  const own = key?.relay_base_url?.trim()
  return own ? own : siteBaseUrl
}

/** True when the key's node (and so its address) changed recently enough that the user should update their clients. */
export function relayAddressChanged(
  key: Partial<KeyAddressFields> | null | undefined,
  now: number = Date.now()
): boolean {
  const raw = key?.relay_address_changed_at
  if (!raw) return false
  const at = new Date(raw).getTime()
  if (Number.isNaN(at)) return false
  const age = now - at
  return age >= 0 && age <= ADDRESS_CHANGED_NOTICE_DAYS * 86_400_000
}

/** Distinct node addresses used by a set of keys, in first-seen order, excluding the site address itself. */
export function distinctRelayEndpoints(
  keys: ReadonlyArray<Partial<KeyAddressFields>>,
  siteBaseUrl: string
): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const k of keys) {
    const url = k.relay_base_url?.trim()
    if (!url || url === siteBaseUrl || seen.has(url)) continue
    seen.add(url)
    out.push(url)
  }
  return out
}

/** Whether any key (or an empty list) still uses the site-wide address. */
export function usesSiteAddress(keys: ReadonlyArray<Partial<KeyAddressFields>>, siteBaseUrl: string): boolean {
  if (keys.length === 0) return true
  return keys.some((k) => {
    const url = k.relay_base_url?.trim()
    return !url || url === siteBaseUrl
  })
}
