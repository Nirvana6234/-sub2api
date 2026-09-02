import { describe, expect, it } from 'vitest'
import { isManagedClientApiKey, isWebsiteHiddenApiKey } from '@/api/keys'

describe('website API key visibility', () => {
  it('hides keys issued by the Cofly desktop client', () => {
    expect(isManagedClientApiKey({ name: '共飞直连客户端-PC-abc' })).toBe(true)
    expect(isManagedClientApiKey({ name: '  共飞直连客户端-PC-abc  ' })).toBe(true)
    expect(isManagedClientApiKey({ name: '用户手动创建的 Key' })).toBe(false)
  })

  it('hides the two system keys created for the playground', () => {
    expect(isWebsiteHiddenApiKey({ name: 'Playground Chat' })).toBe(true)
    expect(isWebsiteHiddenApiKey({ name: ' Playground Images ' })).toBe(true)
    expect(isWebsiteHiddenApiKey({ name: '用户手动创建的 Key' })).toBe(false)
  })
})
