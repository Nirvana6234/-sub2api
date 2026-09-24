import { describe, expect, it } from 'vitest'
import { trimGuestTrialHistory, type GuestTrialMessage } from '../trial'

const msg = (role: GuestTrialMessage['role'], content: string): GuestTrialMessage => ({ role, content })

describe('trimGuestTrialHistory', () => {
  it('keeps the whole conversation when it fits', () => {
    const history = [msg('user', 'aa'), msg('assistant', 'bb'), msg('user', 'cc')]
    expect(trimGuestTrialHistory(history, 100)).toEqual(history)
  })

  it('drops the oldest turns first and always keeps the latest question', () => {
    const history = [msg('user', 'old question'), msg('assistant', 'old answer'), msg('user', 'new')]
    expect(trimGuestTrialHistory(history, 12)).toEqual([msg('user', 'new')])
  })

  it('never starts the context with an orphaned assistant reply', () => {
    const history = [msg('user', 'q1'), msg('assistant', 'a1'), msg('user', 'q2')]
    // 只放得下 a1 + q2 时，也要把孤立的 a1 去掉
    expect(trimGuestTrialHistory(history, 4)[0].role).toBe('user')
  })

  it('counts characters, not bytes', () => {
    const history = [msg('user', '你好'), msg('assistant', '你好呀'), msg('user', '再见')]
    expect(trimGuestTrialHistory(history, 7)).toHaveLength(3)
  })
})
