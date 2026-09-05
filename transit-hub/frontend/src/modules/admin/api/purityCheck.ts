import type {
  PurityJobDetail,
  PurityJobListResponse,
  PurityTarget,
  PurityTierInfo,
  PuritySubmitInput,
  PuritySubmitResult,
  PurityJob,
} from '../types/purityCheck'
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

type ApiErrorPayload = {
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
  } catch {
    throw new Error('admin.purityCheck.errors.network')
  }

  const text = await response.text()
  const payload = text ? (JSON.parse(text) as T & ApiErrorPayload) : ({} as T & ApiErrorPayload)

  if (!response.ok) {
    if (isUnauthorizedApiResponse(response.status, payload)) {
      handleAuthExpired()
      throw new Error(authUnauthorizedErrorKey)
    }
    throw new Error(payload.message ?? 'admin.purityCheck.errors.request')
  }

  return payload
}

export const listPurityTargets = async (): Promise<PurityTarget[]> =>
  requestJson<PurityTarget[]>('/purity-check/targets')

export const listPurityTiers = async (): Promise<PurityTierInfo[]> =>
  requestJson<PurityTierInfo[]>('/purity-check/tiers')

export type PurityJobListQuery = {
  limit?: number
  offset?: number
  search?: string
  status?: string
}

export const listPurityJobs = async (query: PurityJobListQuery = {}): Promise<PurityJobListResponse> => {
  const params = new URLSearchParams()
  params.set('limit', String(query.limit ?? 100))
  if (query.offset) params.set('offset', String(query.offset))
  if (query.search?.trim()) params.set('search', query.search.trim())
  if (query.status) params.set('status', query.status)
  return requestJson<PurityJobListResponse>(`/purity-check/jobs?${params.toString()}`)
}

export const getPurityJob = async (id: string): Promise<PurityJobDetail> =>
  requestJson<PurityJobDetail>(`/purity-check/jobs/${encodeURIComponent(id)}`)

export const submitPurityJobs = async (input: PuritySubmitInput): Promise<PuritySubmitResult> => {
  const response = await requestJson<PuritySubmitResult>('/purity-check/jobs', {
    method: 'POST',
    body: JSON.stringify(input),
  })
  return { jobs: response.jobs ?? [], skippedTargetCount: response.skippedTargetCount ?? 0 }
}

export const cancelPurityJob = async (id: string): Promise<void> => {
  await requestJson<{ ok: boolean }>(`/purity-check/jobs/${encodeURIComponent(id)}/cancel`, {
    method: 'POST',
  })
}

/** 删除一条已结束的任务及其报告。排队中/运行中的要先取消，后端会拒绝。 */
export const deletePurityJob = async (id: string): Promise<void> => {
  await requestJson<{ ok: boolean }>(`/purity-check/jobs/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
}
