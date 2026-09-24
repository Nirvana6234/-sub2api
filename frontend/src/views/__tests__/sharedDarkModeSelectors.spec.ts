import { readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { describe, expect, it } from 'vitest'

const cases = [
  ['admin contribution rooms', '../admin/ContributionRoomsView.vue', '.dark .admin-room-detail-panel'],
  ['user shared rooms', '../user/SharedContributionRoomsView.vue', '.dark .shared-rooms-main'],
  ['account contributions', '../user/AccountContributionsView.vue', '.dark .contribution-form-zone'],
] as const

describe('shared feature dark-mode selectors', () => {
  it.each(cases)('keeps a complete dark ancestor for %s', (_, relativePath, expectedSelector) => {
    const filename = fileURLToPath(new URL(relativePath, import.meta.url))
    const source = readFileSync(filename, 'utf8')
    expect(source).not.toContain(':global(.dark) ')
    expect(source).toContain(`:global(${expectedSelector})`)
  })
})
