import type { ProxyProtocol } from '@/types'

const directProtocols = new Set<ProxyProtocol>(['http', 'https', 'socks5', 'socks5h'])
const nodeProtocols = new Set(['vless', 'vmess', 'trojan', 'ss', 'ssr', 'hysteria', 'hysteria2', 'tuic'])

export type ProxyAddressIssue =
  | 'empty'
  | 'missing_scheme'
  | 'node_link'
  | 'unsupported_scheme'
  | 'invalid_url'
  | 'missing_host'
  | 'unsupported_components'

export interface ParsedProxyAddress {
  protocol: ProxyProtocol
  host: string
  port: number
  username: string
  password: string
}

export type ProxyAddressParseResult =
  | { ok: true; value: ParsedProxyAddress }
  | { ok: false; issue: ProxyAddressIssue; scheme?: string }

function decoded(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

export function parseProxyAddress(input: string): ProxyAddressParseResult {
  const raw = input.trim()
  if (!raw) return { ok: false, issue: 'empty' }

  const schemeMatch = raw.match(/^([a-z][a-z0-9+.-]*):\/\//i)
  if (!schemeMatch) return { ok: false, issue: 'missing_scheme' }
  const scheme = schemeMatch[1]?.toLowerCase() || ''
  if (nodeProtocols.has(scheme)) return { ok: false, issue: 'node_link', scheme }
  if (!directProtocols.has(scheme as ProxyProtocol)) return { ok: false, issue: 'unsupported_scheme', scheme }

  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch {
    return { ok: false, issue: 'invalid_url', scheme }
  }
  if (!parsed.hostname) return { ok: false, issue: 'missing_host', scheme }
  if ((parsed.pathname && parsed.pathname !== '/') || parsed.search || parsed.hash) {
    return { ok: false, issue: 'unsupported_components', scheme }
  }

  const protocol = scheme as ProxyProtocol
  const defaultPort = protocol === 'http' ? 80 : protocol === 'https' ? 443 : 1080
  const port = parsed.port ? Number(parsed.port) : defaultPort
  if (!Number.isInteger(port) || port < 1 || port > 65535) return { ok: false, issue: 'invalid_url', scheme }

  return {
    ok: true,
    value: {
      protocol,
      host: parsed.hostname,
      port,
      username: decoded(parsed.username),
      password: decoded(parsed.password),
    },
  }
}
