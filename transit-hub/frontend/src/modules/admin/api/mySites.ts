import type {
  RunAutoPricingRequest,
  RunAutoPricingResponse,
  MySiteMapping,
  MySiteMappingOptionsResponse,
  MySiteStatus,
  RealBindRequest,
  RealConnectRequest,
  RealConnectResponse,
  RealConnection,
  RealDisconnectRequest,
  UpstreamKeyItem,
  UpstreamKeyTestRequest,
  UpstreamKeyTestResponse,
  UpstreamKeyModelsResponse,
  AdminResourceOption,
  LatencyCompensationSummary,
  LatencyCompensationPayoutView,
  LatencySubsidyTaskView,
  LatencySubsidyTaskInput,
  RevokeLatencyCompensationPayoutResult,
} from '../types/mySites'
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

type MySiteMappingRequest = Omit<MySiteMapping, 'lastAutoPricingRun'>

// Auto-pricing runs are recorded by the server. The API accepts configuration
// fields only, so never round-trip this read-only status in a save request.
const toMappingRequest = ({ lastAutoPricingRun: _lastAutoPricingRun, ...mapping }: MySiteMapping): MySiteMappingRequest => mapping

const normalizeMappings = (value: unknown): MySiteMapping[] => {
  if (!Array.isArray(value)) return []
  return value.flatMap((entry) => {
    if (entry == null || typeof entry !== 'object') return []
    const mapping = entry as MySiteMapping
    if (typeof mapping.ownGroup !== 'string' || !mapping.ownGroup.trim()) return []
    const upstreamTargets = Array.isArray(mapping.upstreamTargets)
      ? mapping.upstreamTargets.filter(target => (
          target != null &&
          typeof target.siteId === 'string' &&
          typeof target.groupName === 'string'
        ))
      : []
    return [{ ...mapping, upstreamTargets }]
  })
}

const normalizeStatus = (status: MySiteStatus): MySiteStatus => ({
  ...status,
  ...(Object.prototype.hasOwnProperty.call(status, 'mappings')
    ? { mappings: normalizeMappings(status.mappings) }
    : {}),
})

const normalizeMappingOptions = (response: MySiteMappingOptionsResponse): MySiteMappingOptionsResponse => ({
  ...response,
  ownGroups: Array.isArray(response.ownGroups) ? response.ownGroups : [],
  mappings: normalizeMappings(response.mappings),
  upstreamTargetMultipliers: Array.isArray(response.upstreamTargetMultipliers) ? response.upstreamTargetMultipliers : [],
  staleOwnGroups: Array.isArray(response.staleOwnGroups) ? response.staleOwnGroups : [],
  staleTargets: Array.isArray(response.staleTargets) ? response.staleTargets : [],
})

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
    throw new Error('admin.mySites.errors.network')
  }

  const text = await response.text()
  let payload = {} as T & AdminErrorPayload
  if (text) {
    try {
      payload = JSON.parse(text) as T & AdminErrorPayload
    } catch {
      payload = {} as T & AdminErrorPayload
    }
  }

  if (!response.ok) {
    if (isUnauthorizedApiResponse(response.status, payload)) {
      handleAuthExpired()
      throw new Error(authUnauthorizedErrorKey)
    }

    throw new Error(payload.message ?? 'admin.mySites.errors.request')
  }

  return payload
}

export const getMySiteMappingOptions = async (): Promise<MySiteMappingOptionsResponse> => (
  normalizeMappingOptions(await requestJson<MySiteMappingOptionsResponse>('/my-sites/mapping-options'))
)

export const saveMySiteMappings = async (mappings: MySiteMapping[]): Promise<MySiteStatus> => (
  normalizeStatus(await requestJson<MySiteStatus>('/my-sites/mappings', {
    method: 'PUT',
    body: JSON.stringify({ mappings: mappings.map(toMappingRequest) }),
  }))
)

export const realConnect = async (req: RealConnectRequest): Promise<RealConnectResponse> => (
  requestJson<RealConnectResponse>('/my-sites/real-connect', {
    method: 'POST',
    body: JSON.stringify(req),
  })
)

export const listRealConnections = async (): Promise<RealConnection[]> =>
  requestJson<RealConnection[]>('/my-sites/real-connections')

export const listUpstreamKeys = async (siteId: string, groupId: string, groupName: string): Promise<UpstreamKeyItem[]> => {
  const params = new URLSearchParams({ siteId, groupId, groupName })
  const items = await requestJson<UpstreamKeyItem[]>(`/my-sites/upstream-keys?${params.toString()}`)
  return Array.isArray(items)
    ? items.map(item => ({
        ...item,
        // Older backends returned the full key. Keep it only as an internal
        // compatibility fallback; the UI renders the non-secret preview.
        keyPreview: item.keyPreview || (item.key ? `${item.key.slice(0, 6)}...${item.key.slice(-4)}` : ''),
      }))
    : []
}

export const listAdminResources = async (groupId: string): Promise<AdminResourceOption[]> => {
  const items = await requestJson<AdminResourceOption[]>(`/my-sites/admin-resources?groupId=${encodeURIComponent(groupId)}`)
  return Array.isArray(items) ? items : []
}

/**
 * 测一个上游 Key 到底能不能用：先列模型，再发一次 max_tokens=1 的真实请求。
 *
 * 用 POST 是因为它会真实打到上游并产生（极小的）计费。明文 key 不经过这里——
 * 只传 keyId，后端自己去上游解析。
 */
export const testUpstreamKey = async (req: UpstreamKeyTestRequest): Promise<UpstreamKeyTestResponse> => (
  requestJson<UpstreamKeyTestResponse>('/my-sites/upstream-keys/test', {
    method: 'POST',
    body: JSON.stringify(req),
  })
)

export const listUpstreamKeyModels = async (req: UpstreamKeyTestRequest): Promise<UpstreamKeyModelsResponse> => (
  requestJson<UpstreamKeyModelsResponse>('/my-sites/upstream-keys/models', {
    method: 'POST',
    body: JSON.stringify(req),
  })
)

export const realBind = async (req: RealBindRequest): Promise<RealConnectResponse> => (
  requestJson<RealConnectResponse>('/my-sites/real-bind', {
    method: 'POST',
    body: JSON.stringify(req),
  })
)

export const realDisconnect = async (req: RealDisconnectRequest): Promise<void> => {
  await requestJson<{ ok: boolean }>('/my-sites/real-disconnect', {
    method: 'POST',
    body: JSON.stringify(req),
  })
}

export const runAutoPricing = async (req: RunAutoPricingRequest): Promise<RunAutoPricingResponse> => {
  const response = await requestJson<RunAutoPricingResponse>('/my-sites/auto-pricing/run', {
    method: 'POST',
    body: JSON.stringify(req),
  })
  return {
    ...response,
    mapping: normalizeMappings([response.mapping])[0] ?? response.mapping,
  }
}

// New backends update one mapping atomically. A generic method-not-supported
// response falls back to the legacy full-array PUT so rolling deployments remain usable.
export const saveMySiteMapping = async (mapping: MySiteMapping, currentMappings: MySiteMapping[]): Promise<MySiteStatus> => {
  try {
    return normalizeStatus(await requestJson<MySiteStatus>('/my-sites/mappings', {
      method: 'PATCH',
      body: JSON.stringify({ mapping: toMappingRequest(mapping) }),
    }))
  } catch (error) {
    if (!(error instanceof Error) || error.message !== 'admin.mySites.errors.request') throw error
    const nextMappings = currentMappings.some(item => item.ownGroup === mapping.ownGroup)
      ? currentMappings.map(item => item.ownGroup === mapping.ownGroup ? mapping : item)
      : [...currentMappings, mapping]
    return saveMySiteMappings(nextMappings)
  }
}

/**
 * 延迟补贴任务：一套阈值/退款比例配置，可选每天自动执行。列表、增删改，
 * 以及对某个任务发起一次运行（预览/实际发放）。
 */
export const listLatencySubsidyTasks = async (): Promise<LatencySubsidyTaskView[]> => {
  const items = await requestJson<LatencySubsidyTaskView[]>('/my-sites/latency-subsidy-tasks')
  return Array.isArray(items) ? items : []
}

export const createLatencySubsidyTask = async (input: LatencySubsidyTaskInput): Promise<LatencySubsidyTaskView> => (
  requestJson<LatencySubsidyTaskView>('/my-sites/latency-subsidy-tasks', {
    method: 'POST',
    body: JSON.stringify(input),
  })
)

export const updateLatencySubsidyTask = async (id: string, input: LatencySubsidyTaskInput): Promise<LatencySubsidyTaskView> => (
  requestJson<LatencySubsidyTaskView>(`/my-sites/latency-subsidy-tasks/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(input),
  })
)

export const deleteLatencySubsidyTask = async (id: string): Promise<void> => {
  await requestJson<{ ok: boolean }>(`/my-sites/latency-subsidy-tasks/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
}

/**
 * 预览：跑一遍这个任务在 [from, to) 这段时间会补贴多少，不动任何用户余额。
 * 阈值/比例用任务里存的，不接受调用方覆盖——避免预览和实际发放用的是两套配置。
 */
export const previewLatencySubsidyTask = async (id: string, from: string, to: string): Promise<LatencyCompensationSummary> => {
  const query = new URLSearchParams({ from, to })
  return requestJson<LatencyCompensationSummary>(
    `/my-sites/latency-subsidy-tasks/${encodeURIComponent(id)}/preview?${query.toString()}`
  )
}

/**
 * 真正发放：连接的 Sub2API 站点会把每个符合条件用户的差价直接加到余额上。
 * 不可撤销——调用前必须先预览并让操作者确认过。
 */
export const applyLatencySubsidyTask = async (id: string, from: string, to: string): Promise<LatencyCompensationSummary> => (
  requestJson<LatencyCompensationSummary>(`/my-sites/latency-subsidy-tasks/${encodeURIComponent(id)}/apply`, {
    method: 'POST',
    body: JSON.stringify({ from, to }),
  })
)

/**
 * 补贴发放历史：按时间倒序列出过往的每一批发放，带触发它的任务名和完整
 * 按用户明细（谁、多少钱、什么时候）。用来回答"这笔退过没有、什么时候退的"。
 */
export const listLatencySubsidyTasksHistory = async (limit = 50): Promise<LatencyCompensationPayoutView[]> => {
  const items = await requestJson<LatencyCompensationPayoutView[]>(
    `/my-sites/latency-subsidy-tasks-history?limit=${limit}`
  )
  return Array.isArray(items) ? items : []
}

/**
 * 撤回一整批已发放的补贴：Sub2API 那边会把这批里每个用户的钱扣回去，且不会
 * 在 Sub2API 自己的记录里留痕（不是给单个用户撤销，是整批一起撤）。
 * 可能部分失败——某个用户余额已经不够扣回时会跳过他，不代表整批撤回失败。
 */
export const revokeLatencyCompensationPayout = async (id: string): Promise<RevokeLatencyCompensationPayoutResult> => (
  requestJson<RevokeLatencyCompensationPayoutResult>(
    `/my-sites/latency-subsidy-tasks-history/${encodeURIComponent(id)}/revoke`,
    { method: 'POST' }
  )
)

export const removeMySiteMapping = async (ownGroup: string, currentMappings: MySiteMapping[]): Promise<MySiteStatus> => {
  try {
    return normalizeStatus(await requestJson<MySiteStatus>(`/my-sites/mappings/${encodeURIComponent(ownGroup)}`, { method: 'DELETE' }))
  } catch (error) {
    if (!(error instanceof Error) || error.message !== 'admin.mySites.errors.request') throw error
    return saveMySiteMappings(currentMappings.filter(item => item.ownGroup !== ownGroup))
  }
}
