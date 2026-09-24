import { describe, expect, it } from 'vitest'
import { parseProxyAddress } from '../proxyAddress'

describe('parseProxyAddress', () => {
  it('parses a credentialed SOCKS5 address', () => {
    expect(parseProxyAddress('socks5://alice:p%40ss@proxy.example.com:1080')).toEqual({
      ok: true,
      value: {
        protocol: 'socks5',
        host: 'proxy.example.com',
        port: 1080,
        username: 'alice',
        password: 'p@ss',
      },
    })
  })

  it('uses standard default ports', () => {
    expect(parseProxyAddress('https://proxy.example.com')).toMatchObject({
      ok: true,
      value: { protocol: 'https', port: 443 },
    })
  })

  it('identifies node links that require a protocol core', () => {
    expect(parseProxyAddress('vless://uuid@example.com:443?security=reality')).toEqual({
      ok: false,
      issue: 'node_link',
      scheme: 'vless',
    })
  })

  it('rejects proxy URLs with paths or query parameters', () => {
    expect(parseProxyAddress('http://proxy.example.com:8080/path')).toMatchObject({
      ok: false,
      issue: 'unsupported_components',
    })
  })
})
