import { beforeEach, describe, expect, it, vi } from 'vitest'

const put = vi.fn()
const post = vi.fn()
const get = vi.fn()
const deleteRequest = vi.fn()

vi.mock('@/api/client', () => ({
  apiClient: {
    put,
    post,
    get,
    delete: deleteRequest
  }
}))

describe('contribution room preference api', () => {
  beforeEach(() => {
    put.mockReset()
    post.mockReset()
    get.mockReset()
    deleteRequest.mockReset()
    put.mockResolvedValue({ data: {} })
    post.mockResolvedValue({ data: {} })
    get.mockResolvedValue({ data: {} })
    deleteRequest.mockResolvedValue({ data: {} })
  })

  it('keeps the selected fallback group while fallback is disabled', async () => {
    const { updateContributionRoomPreference } = await import('@/api/contributionRooms')

    await updateContributionRoomPreference(31, [12], false, 7)

    expect(put).toHaveBeenCalledWith('/contribution-rooms/preference', {
      api_key_id: 31,
      room_ids: [12],
      allow_pool_fallback: false,
      fallback_group_id: 7
    })
  })

  it('creates a room together with its selected account allowances', async () => {
    const { createContributionRoom } = await import('@/api/contributionRooms')
    const payload = {
      name: 'GPT room',
      consumer_rate_multiplier: 1.2,
      accounts: [
        { account_id: 12, share_budget_usd: 5, share_concurrency: 2 },
        { account_id: 18, share_budget_usd: 8.5, share_concurrency: 4 }
      ]
    }

    await createContributionRoom(payload)

    expect(post).toHaveBeenCalledWith('/account-contributions/room', payload)
  })

  it('scopes room catalog and clear requests to one API key', async () => {
    const { listContributionRooms, clearContributionRoomPreference } = await import('@/api/contributionRooms')

    await listContributionRooms(31, { page: 2, limit: 24 })
    await clearContributionRoomPreference(31)

    expect(get).toHaveBeenCalledWith('/contribution-rooms', {
      params: { page: 2, limit: 24, api_key_id: 31 }
    })
    expect(deleteRequest).toHaveBeenCalledWith('/contribution-rooms/preference', {
      params: { api_key_id: 31 }
    })
  })
})
