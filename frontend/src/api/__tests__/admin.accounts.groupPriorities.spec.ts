import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { post }
}))

import { updateGroupPriorities } from '@/api/admin/accounts'

describe('admin account group priority API', () => {
  beforeEach(() => {
    post.mockReset()
  })

  it('updates account_groups.priority through the dedicated endpoint', async () => {
    const updates = [{ account_id: 7, group_id: 34, priority: 12 }]
    const result = { updated: 1, requested: 1 }
    post.mockResolvedValueOnce({ data: result })

    await expect(updateGroupPriorities(updates)).resolves.toEqual(result)
    expect(post).toHaveBeenCalledWith('/admin/accounts/group-priorities', { updates })
  })
})
