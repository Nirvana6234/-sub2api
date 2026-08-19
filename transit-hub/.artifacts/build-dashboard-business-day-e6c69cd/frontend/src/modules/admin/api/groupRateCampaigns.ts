import type {
  CampaignDetail,
  CreateGroupRateCampaignRequest,
  CampaignPreviewResponse,
  GroupRateCampaignsQuery,
  PaginatedGroupRateCampaignsResponse,
} from '../types/groupRateCampaigns'
import {
  authUnauthorizedErrorKey,
  getAccessToken,
  handleAuthExpired,
  isUnauthorizedApiResponse,
} from '@/modules/auth/api/auth'

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL ?? '/api'

const endpoint = (path: string): string => `${apiBaseUrl.replace(/\/$/, '')}${path}`

const authHeaders = (): HeadersInit => {
  const token = getAccessToken()
  if (!token) return {}
  return { Authorization: `Bearer ${token}` }
}

type AdminErrorPayload = {
  message?: string
}

const requestJson = async <T>(path: string, options: RequestInit = {}): Promise<T> => {
  let response: Response
  try {
    response = await fetch(endpoint(path), {
      ...options,
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        ...authHeaders(),
        ...(options.headers ?? {}),
      },
    })
  } catch (error) {
    throw new Error('admin.groupRateCampaigns.errors.network')
  }

  const text = await response.text()
  const payload = text ? JSON.parse(text) as T & AdminErrorPayload : ({} as T & AdminErrorPayload)

  if (!response.ok) {
    if (isUnauthorizedApiResponse(response.status, payload)) {
      handleAuthExpired()
      throw new Error(authUnauthorizedErrorKey)
    }

    throw new Error(payload.message ?? 'admin.groupRateCampaigns.errors.request')
  }

  return payload
}

export const listGroupRateCampaigns = async (
  query: GroupRateCampaignsQuery,
): Promise<PaginatedGroupRateCampaignsResponse> => {
  const params = new URLSearchParams({
    page: query.page.toString(),
    pageSize: query.pageSize.toString(),
  })
  if (query.status) params.set('status', query.status)

  return requestJson<PaginatedGroupRateCampaignsResponse>(`/group-rate-campaigns?${params.toString()}`)
}

export const previewGroupRateCampaign = async (
  request: CreateGroupRateCampaignRequest,
): Promise<CampaignPreviewResponse> => (
  requestJson<CampaignPreviewResponse>('/group-rate-campaigns/preview', {
    method: 'POST',
    body: JSON.stringify(request),
  })
)

export const createGroupRateCampaign = async (
  request: CreateGroupRateCampaignRequest,
): Promise<CampaignDetail> => (
  requestJson<CampaignDetail>('/group-rate-campaigns', {
    method: 'POST',
    body: JSON.stringify(request),
  })
)

export const getGroupRateCampaign = async (id: string): Promise<CampaignDetail> => (
  requestJson<CampaignDetail>(`/group-rate-campaigns/${encodeURIComponent(id)}`)
)

export const startGroupRateCampaign = async (id: string): Promise<CampaignDetail> => (
  requestJson<CampaignDetail>(`/group-rate-campaigns/${encodeURIComponent(id)}/start`, {
    method: 'POST',
  })
)

export const endGroupRateCampaign = async (id: string): Promise<CampaignDetail> => (
  requestJson<CampaignDetail>(`/group-rate-campaigns/${encodeURIComponent(id)}/end`, {
    method: 'POST',
  })
)

export const cancelGroupRateCampaign = async (id: string): Promise<CampaignDetail> => (
  requestJson<CampaignDetail>(`/group-rate-campaigns/${encodeURIComponent(id)}/cancel`, {
    method: 'POST',
  })
)
