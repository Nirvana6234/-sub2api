import { apiClient } from './client'

export type ContributionRoomStatus = 'active' | 'paused'
export type ContributionRoomVisibility = 'public' | 'private'

export interface ContributionRoomAccount {
  account_id: number
  name: string
  platform: string
  type: string
  status: string
  schedulable: boolean
  concurrency: number
  share_concurrency: number
  enabled: boolean
  share_budget_usd: number
  share_used_usd: number
  share_remaining_usd: number
  member_verified_at?: string
  verification_status: 'verified' | 'failed' | string
  verification_platform?: string
  verification_model_family?: string
  verification_source_kind?: string
  verification_tested_at?: string
  verification_test_model?: string
  needs_attention: boolean
}

export interface ContributionRoom {
  id: number
  name: string
  owner: {
    user_id: number
    username?: string
  }
  consumer_rate_multiplier: number
  status: ContributionRoomStatus | string
  visibility: ContributionRoomVisibility | string
  selectable: boolean
  accounts: ContributionRoomAccount[]
  created_at: string
  updated_at: string
}

export interface ContributionRoomPreference {
  api_key_id: number
  room_ids: number[]
  allow_pool_fallback: boolean
  fallback_group_id?: number | null
}

export interface ContributionRoomCatalog {
  items: ContributionRoom[]
  total: number
  page: number
  limit: number
  preference: ContributionRoomPreference
}

export interface UpsertContributionRoomRequest {
  name?: string
  consumer_rate_multiplier?: number
  status?: ContributionRoomStatus
  visibility?: ContributionRoomVisibility
}

export interface CreateContributionRoomAccountInput {
  account_id: number
  share_budget_usd: number
  share_concurrency: number
}

export interface CreateContributionRoomRequest {
  name: string
  consumer_rate_multiplier?: number
  accounts: CreateContributionRoomAccountInput[]
}

export async function getOwnContributionRoom(): Promise<ContributionRoom> {
  const { data } = await apiClient.get<ContributionRoom>('/account-contributions/room')
  return data
}

export async function createContributionRoom(payload: CreateContributionRoomRequest): Promise<ContributionRoom> {
  const { data } = await apiClient.post<ContributionRoom>('/account-contributions/room', payload)
  return data
}

export async function updateOwnContributionRoom(payload: UpsertContributionRoomRequest): Promise<ContributionRoom> {
  const { data } = await apiClient.put<ContributionRoom>('/account-contributions/room', payload)
  return data
}

export async function addContributionRoomAccount(accountId: number, shareBudgetUSD: number, shareConcurrency: number): Promise<ContributionRoomAccount> {
  const { data } = await apiClient.post<ContributionRoomAccount>('/account-contributions/room/accounts', {
    account_id: accountId,
    share_budget_usd: shareBudgetUSD,
    share_concurrency: shareConcurrency,
  })
  return data
}

export interface UpdateContributionRoomAccountRequest {
  enabled?: boolean
  share_budget_usd?: number
  share_concurrency?: number
}

export async function updateOwnContributionRoomAccount(accountId: number, payload: UpdateContributionRoomAccountRequest): Promise<ContributionRoomAccount> {
  const { data } = await apiClient.patch<ContributionRoomAccount>(`/account-contributions/room/accounts/${accountId}`, payload)
  return data
}

export async function removeContributionRoomAccount(accountId: number): Promise<void> {
  await apiClient.delete(`/account-contributions/room/accounts/${accountId}`)
}

export async function listContributionRooms(apiKeyId: number, params: { page?: number; limit?: number; keyword?: string } = {}): Promise<ContributionRoomCatalog> {
  const { data } = await apiClient.get<ContributionRoomCatalog>('/contribution-rooms', {
    params: { ...params, api_key_id: apiKeyId }
  })
  return data
}

export async function updateContributionRoomPreference(apiKeyId: number, roomIds: number[], allowPoolFallback: boolean, fallbackGroupId?: number | null): Promise<ContributionRoomPreference> {
  const { data } = await apiClient.put<ContributionRoomPreference>('/contribution-rooms/preference', {
    api_key_id: apiKeyId,
    room_ids: roomIds,
    allow_pool_fallback: allowPoolFallback,
    fallback_group_id: fallbackGroupId || null,
  })
  return data
}

export async function clearContributionRoomPreference(apiKeyId: number): Promise<ContributionRoomPreference> {
  const { data } = await apiClient.delete<ContributionRoomPreference>('/contribution-rooms/preference', {
    params: { api_key_id: apiKeyId }
  })
  return data
}

export default {
  getOwn: getOwnContributionRoom,
  create: createContributionRoom,
  updateOwn: updateOwnContributionRoom,
  addAccount: addContributionRoomAccount,
  updateOwnAccount: updateOwnContributionRoomAccount,
  removeAccount: removeContributionRoomAccount,
  list: listContributionRooms,
  updatePreference: updateContributionRoomPreference,
  clearPreference: clearContributionRoomPreference,
}
