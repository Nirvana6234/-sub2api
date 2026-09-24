import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { useTicketStore } from '@/stores/tickets'

const mockUserUnreadCount = vi.fn()
const mockAdminUnreadCount = vi.fn()

vi.mock('@/api/tickets', () => ({
  default: {
    unreadCount: (...args: unknown[]) => mockUserUnreadCount(...args)
  }
}))

vi.mock('@/api/admin/tickets', () => ({
  default: {
    unreadCount: (...args: unknown[]) => mockAdminUnreadCount(...args)
  }
}))

describe('useTicketStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  function authenticate(role: 'admin' | 'user') {
    const authStore = useAuthStore()
    authStore.$patch({
      token: 'test-token',
      user: { id: 1, role } as any
    })
  }

  it('普通用户只读取自己的工单未读数', async () => {
    authenticate('user')
    mockUserUnreadCount.mockResolvedValue(3)

    const store = useTicketStore()
    await store.refreshUnreadCounts()

    expect(store.userUnreadCount).toBe(3)
    expect(store.adminUnreadCount).toBe(0)
    expect(mockUserUnreadCount).toHaveBeenCalledTimes(1)
    expect(mockAdminUnreadCount).not.toHaveBeenCalled()
  })

  it('管理员同时读取个人工单和管理端新工单未读数', async () => {
    authenticate('admin')
    mockUserUnreadCount.mockResolvedValue(2)
    mockAdminUnreadCount.mockResolvedValue(5)

    const store = useTicketStore()
    await store.refreshUnreadCounts()

    expect(store.userUnreadCount).toBe(2)
    expect(store.adminUnreadCount).toBe(5)
    expect(mockUserUnreadCount).toHaveBeenCalledTimes(1)
    expect(mockAdminUnreadCount).toHaveBeenCalledTimes(1)
  })

  it('轮询不会创建重复定时器，清理后会停止并清空数量', async () => {
    authenticate('admin')
    mockUserUnreadCount.mockResolvedValue(1)
    mockAdminUnreadCount.mockResolvedValue(1)

    const store = useTicketStore()
    store.startPolling()
    store.startPolling()

    vi.advanceTimersByTime(30_000)
    await Promise.resolve()
    await Promise.resolve()
    expect(mockAdminUnreadCount).toHaveBeenCalledTimes(1)

    store.clear()
    vi.advanceTimersByTime(60_000)
    expect(mockAdminUnreadCount).toHaveBeenCalledTimes(1)
    expect(store.adminUnreadCount).toBe(0)
  })
})
