import { apiClient } from '../client'
import type {
  ContributionRoom,
  ContributionRoomAccount,
  UpsertContributionRoomRequest,
} from '@/api/contributionRooms'

export interface AdminContributionRoomList {
  items: ContributionRoom[]
  total: number
  page: number
  page_size: number
}

export interface AdminContributionRoomListParams {
  page?: number
  page_size?: number
  keyword?: string
  status?: string
  visibility?: string
}

export async function list(params: AdminContributionRoomListParams = {}): Promise<AdminContributionRoomList> {
  const { data } = await apiClient.get<AdminContributionRoomList>('/admin/contribution-rooms', { params })
  return data
}

export async function get(id: number): Promise<ContributionRoom> {
  const { data } = await apiClient.get<ContributionRoom>(`/admin/contribution-rooms/${id}`)
  return data
}

export async function update(id: number, payload: UpsertContributionRoomRequest): Promise<ContributionRoom> {
  const { data } = await apiClient.put<ContributionRoom>(`/admin/contribution-rooms/${id}`, payload)
  return data
}

export async function updateAccount(roomId: number, accountId: number, enabled: boolean): Promise<ContributionRoomAccount> {
  const { data } = await apiClient.patch<ContributionRoomAccount>(`/admin/contribution-rooms/${roomId}/accounts/${accountId}`, { enabled })
  return data
}

export async function testAccount(roomId: number, accountId: number, modelId = ''): Promise<{ status?: string; error_message?: string; latency_ms?: number }> {
  const { data } = await apiClient.post<{ status?: string; error_message?: string; latency_ms?: number }>(
    `/admin/contribution-rooms/${roomId}/accounts/${accountId}/test`,
    modelId ? { model_id: modelId } : {},
  )
  return data
}

export default { list, get, update, updateAccount, testAccount }
