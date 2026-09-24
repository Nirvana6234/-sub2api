import { apiClient } from '../client'
import type { AccountUsageInfo, AccountUsageStatsResponse } from '@/types'

export type ContributionId = number | string
export type ContributionAction = 'pause' | 'resume'

export interface ContributionGroup {
  id?: ContributionId
  name?: string
}

export interface AdminContribution {
  id: ContributionId
  name: string
  platform: string
  type: string
  status: string
  governance_state: string
  governance_reason?: string
  schedulable: boolean
  concurrency: number | null
  groups: Array<ContributionGroup | string>
  submitted_at: string | null
  contributor: {
    id: ContributionId
    email: string
    username: string | null
  }
  share: {
    mode: string
    total_budget: number | null
    daily_budget: number | null
    used_total: number | null
    used_today: number | null
    expires_at: string | null
  }
  health: {
    error_message: string | null
    last_used_at: string | null
  }
}

export interface ContributionListFilters {
  search?: string
  platform?: string
  status?: string
  share_mode?: string
}

export interface ContributionListResponse {
  items: AdminContribution[]
  total: number
  page: number
  page_size: number
  summary: Record<string, unknown>
}

export interface ContributionTestResult {
  status?: string
  success?: boolean
  message?: string
}

export interface UpdateManagedContributionRequest {
  name?: string
  api_key?: string
  base_url?: string
  concurrency?: number
  priority?: number
  load_factor?: number
  group_ids?: number[]
  status?: 'active' | 'disabled'
}

export interface ContributionUsageSummary {
  upstream: AccountUsageInfo
  stats: AccountUsageStatsResponse
  days: number
}

export async function list(
  page = 1,
  pageSize = 20,
  filters?: ContributionListFilters,
  options?: { signal?: AbortSignal }
): Promise<ContributionListResponse> {
  const { data } = await apiClient.get<ContributionListResponse>('/admin/contributions', {
    params: {
      page,
      page_size: pageSize,
      ...filters
    },
    signal: options?.signal
  })
  return data
}

export async function update(
  id: ContributionId,
  payload: { action: ContributionAction; reason?: string }
): Promise<AdminContribution> {
  const { data } = await apiClient.patch<AdminContribution>(`/admin/contributions/${id}`, payload)
  return data
}

export async function updateManaged(id: ContributionId, payload: UpdateManagedContributionRequest): Promise<AdminContribution> {
  const { data } = await apiClient.put<AdminContribution>(`/admin/contributions/${id}`, payload)
  return data
}

export async function test(id: ContributionId): Promise<ContributionTestResult> {
  const { data } = await apiClient.post<ContributionTestResult>(`/admin/contributions/${id}/test`)
  return data
}

export async function deleteContribution(id: ContributionId): Promise<void> {
	await apiClient.delete(`/admin/contributions/${id}`)
}

export async function getUsageSummary(id: ContributionId, force = false): Promise<ContributionUsageSummary> {
  const { data } = await apiClient.get<ContributionUsageSummary>(`/admin/contributions/${id}/usage-summary`, {
    params: force ? { force: 'true' } : undefined
  })
  return data
}

export default {
	list,
	updateManaged,
	update,
	test,
	delete: deleteContribution,
	getUsageSummary
}
