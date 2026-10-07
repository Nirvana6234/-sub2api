/**
 * Admin relay (master / relay-node) API endpoints.
 * Every mutating call is behind step-up verification on the backend; wrap calls with useStepUp().run().
 */

import { apiClient } from '../client'

export type RelayNodeStatus = 'pending' | 'active' | 'draining' | 'disabled' | 'rejected'
export type RelayRuntimeState = 'not_master' | 'not_configured' | 'off' | 'running' | 'failed'
export type RelayDomainState = 'unknown' | 'direct' | 'node' | 'master' | 'other' | 'failed'
export type RelayKeyPurpose = 'root_ca' | 'ticket' | 'voucher'

export interface RelayRuntimeStatus {
  state: RelayRuntimeState
  reason?: string
  listen_addr?: string
  epoch?: string
  root_fingerprints?: string[]
  since: string
}

export interface RelayStatus {
  enabled: boolean
  runtime: RelayRuntimeStatus
}

export interface RelayNode {
  id: number
  name: string
  hostname: string
  region: string
  public_domain: string
  status: RelayNodeStatus
  identity_fingerprint: string
  registered_ip: string
  program_version: string
  system_info?: Record<string, string>
  bandwidth_limit_mbps: number
  allow_multi_ip: boolean
  activated_at?: string
  activated_by?: number
  last_seen_at?: string
  last_seen_ip: string
  created_at: string
  updated_at: string
}

export interface RelayGeneralConfig {
  master_ratio_percent?: number
  api_key_node_rule?: 'assigned' | 'any'
  heartbeat_interval_seconds?: number
  offline_after_seconds?: number
  drain_max_wait_minutes?: number
  load_threshold_percent?: number
  assignment_refresh_seconds?: number
  master_max_concurrent?: number
  probe_enabled?: boolean
  probe_interval_seconds?: number
  probe_port?: number
  master_max_bandwidth_mbps?: number
}

export interface RelayHeartbeat {
  program_version?: string
  config_version?: string
  started_at_unix_ms?: number
  sent_at_unix_ms?: number
  rx_bytes_per_sec?: number
  tx_bytes_per_sec?: number
  client_connections?: number
  inflight_requests?: number
  active_users?: number
  active_keys?: number
  reserved_total_micros?: number
  billing_backlog?: number
  billing_oldest_at_unix_ms?: number
  log_backlog?: number
  log_dropped?: number
  requests_1m?: number
  errors_1m?: number
  cpu_percent?: number
  memory_bytes?: number
  disk_free_bytes?: number
}

export interface RelayExternalHealth {
  probe_checked: boolean
  probe_ok: boolean
  probe_failures: number
  probe_error?: string
  probe_at?: string
  unreachable: boolean
  cert_not_after?: string
  dns: RelayDomainState
  dns_resolved?: string[]
  dns_checked_at?: string
  degraded: boolean
}

export interface RelayNodeHealth {
  node_id: number
  online: boolean
  last_heartbeat_at?: string
  heartbeat?: RelayHeartbeat
  load_percent: number
  clock_skew_ms: number
  external: RelayExternalHealth
  voucher_shortfall: number
}

export interface RelayDomainCheck {
  state: RelayDomainState
  resolved?: string[]
  expected?: string
}

export interface RelayActivateRequest {
  fingerprint: string
  name?: string
  public_domain: string
  bandwidth_limit_mbps?: number
  region?: string
  ignore_dns_mismatch?: boolean
}

export interface RelayNodeDomainUpdateRequest {
  public_domain: string
  ignore_dns_mismatch?: boolean
}

export interface RelayReclaimResult {
  recall_sent: number
  voided: number
  voided_amount: number
  voided_amount_display: string
}

export interface RelayKeyInfo {
  purpose: RelayKeyPurpose
  version: number
  fingerprint: string
  staged: boolean
  signing: boolean
  created_at: string
  activated_at?: string
  pending_node_ids?: number[]
}

export interface RelayKeyAssignmentSummary {
  unassigned: number
  master: number
  nodes: Record<string, { total: number; active: number }>
}

export interface RelayNotificationEvent {
  kind: string
  title: string
  severity: string
  default_enabled: boolean
  enabled: boolean
  feishu: boolean
  email: boolean
}

export interface RelayNotificationConfig {
  feishu_enabled: boolean
  webhook_configured: boolean
  webhook_host?: string
  secret_configured: boolean
  email_enabled: boolean
  merge_window_seconds: number
  events: RelayNotificationEvent[]
}

export interface RelayNotificationUpdate {
  feishu_enabled?: boolean
  /** Empty string clears; omitted leaves untouched. */
  webhook_url?: string
  secret?: string
  email_enabled?: boolean
  merge_window_seconds?: number
  events?: Record<string, { enabled?: boolean; feishu?: boolean; email?: boolean }>
}

export type RelayLogKind = 'app' | 'error' | 'moderation'

export interface RelayLogFilters {
  kind?: RelayLogKind
  from?: number
  to?: number
  before?: number
  level?: string
  component?: string
  request_id?: string
  user_id?: number
  api_key_id?: number
  account_id?: number
  platform?: string
  model?: string
  keyword?: string
  result?: string
  limit?: number
  page?: number
  tail?: boolean
  node_ids?: number[]
}

export interface RelayNodeLogResult {
  node_id: number
  node_name: string
  status: string
  error?: string
  truncated?: boolean
  scan_limited?: boolean
  skipped?: number
  records: Record<string, unknown>[]
}

/** One merged line: the node it came from, its timestamp (ms, 0 when unknown) and the node's own record. */
export interface RelayMergedLogRecord {
  node_id: number
  node_name: string
  ts?: number
  record: Record<string, unknown>
}

export interface RelayLogsResponse {
  nodes: RelayNodeLogResult[]
  records: RelayMergedLogRecord[]
}

export interface RelayUserNodeResult {
  user_id: number
  node_id: number
}

function logParams(f: RelayLogFilters): Record<string, string | number> {
  const out: Record<string, string | number> = {}
  for (const [k, v] of Object.entries(f)) {
    if (v === undefined || v === null || v === '' || k === 'node_ids') continue
    if (k === 'tail') {
      if (v) out.tail = '1'
      continue
    }
    out[k] = v as string | number
  }
  if (f.node_ids?.length) out.node_ids = f.node_ids.join(',')
  return out
}

// ---- status / switch / general config ----

export async function getStatus(): Promise<RelayStatus> {
  const { data } = await apiClient.get<RelayStatus>('/admin/relay/status')
  return data
}

export async function setEnabled(enabled: boolean): Promise<RelayStatus> {
  const { data } = await apiClient.put<RelayStatus>('/admin/relay/enabled', { enabled })
  return data
}

export async function getGeneralConfig(): Promise<RelayGeneralConfig> {
  const { data } = await apiClient.get<RelayGeneralConfig>('/admin/relay/general-config')
  return data
}

export async function updateGeneralConfig(cfg: RelayGeneralConfig): Promise<RelayGeneralConfig> {
  const { data } = await apiClient.put<RelayGeneralConfig>('/admin/relay/general-config', cfg)
  return data
}

// ---- nodes ----

export async function listNodes(): Promise<RelayNode[]> {
  const { data } = await apiClient.get<RelayNode[]>('/admin/relay/nodes')
  return Array.isArray(data) ? data : []
}

export async function nodeHealths(): Promise<RelayNodeHealth[]> {
  const { data } = await apiClient.get<RelayNodeHealth[]>('/admin/relay/nodes/health')
  return Array.isArray(data) ? data : []
}

export async function checkNodeDomain(id: number, domain: string): Promise<RelayDomainCheck> {
  const { data } = await apiClient.get<RelayDomainCheck>(`/admin/relay/nodes/${id}/domain-check`, {
    params: { domain }
  })
  return data
}

export async function activateNode(id: number, req: RelayActivateRequest): Promise<void> {
  await apiClient.post(`/admin/relay/nodes/${id}/activate`, req)
}

export async function updateNodeDomain(id: number, req: RelayNodeDomainUpdateRequest): Promise<void> {
  await apiClient.put(`/admin/relay/nodes/${id}/domain`, req)
}

export async function rejectNode(id: number): Promise<void> {
  await apiClient.post(`/admin/relay/nodes/${id}/reject`)
}

export async function rejectAllPending(): Promise<{ rejected: number }> {
  const { data } = await apiClient.post<{ rejected: number }>('/admin/relay/nodes/reject-pending')
  return data
}

export async function drainNode(id: number): Promise<void> {
  await apiClient.post(`/admin/relay/nodes/${id}/drain`)
}

export async function undrainNode(id: number): Promise<void> {
  await apiClient.post(`/admin/relay/nodes/${id}/undrain`)
}

export async function disableNode(id: number): Promise<void> {
  await apiClient.post(`/admin/relay/nodes/${id}/disable`)
}

export async function enableNode(id: number): Promise<void> {
  await apiClient.post(`/admin/relay/nodes/${id}/enable`)
}

export async function revokeNode(id: number, reason: string): Promise<void> {
  await apiClient.post(`/admin/relay/nodes/${id}/revoke`, { reason })
}

export async function setNodeAllowMultiIP(id: number, allow: boolean): Promise<void> {
  await apiClient.put(`/admin/relay/nodes/${id}/allow-multi-ip`, { allow })
}

export async function reclaimNodeQuota(id: number): Promise<RelayReclaimResult> {
  const { data } = await apiClient.post<RelayReclaimResult>(`/admin/relay/nodes/${id}/reclaim-quota`)
  return data
}

export async function reclaimUserQuota(userId: number): Promise<RelayReclaimResult> {
  const { data } = await apiClient.post<RelayReclaimResult>(`/admin/relay/users/${userId}/reclaim-quota`)
  return data
}

export async function replaceNode(
  oldId: number,
  newNodeId: number,
  fingerprint: string
): Promise<{ moved_keys: number }> {
  const { data } = await apiClient.post<{ moved_keys: number }>(`/admin/relay/nodes/${oldId}/replace`, {
    new_node_id: newNodeId,
    fingerprint
  })
  return data
}

// ---- API Key assignment ----

export async function keyAssignmentSummary(): Promise<RelayKeyAssignmentSummary> {
  const { data } = await apiClient.get<RelayKeyAssignmentSummary>('/admin/relay/api-keys/assignment')
  return data
}

export async function assignUnassignedKeys(): Promise<{ assigned: number; left: number }> {
  const { data } = await apiClient.post<{ assigned: number; left: number }>(
    '/admin/relay/api-keys/assign-unassigned'
  )
  return data
}

export async function moveKeys(keyIds: number[], nodeId: number): Promise<{ moved: number }> {
  const { data } = await apiClient.post<{ moved: number }>('/admin/relay/api-keys/move', {
    key_ids: keyIds,
    node_id: nodeId
  })
  return data
}

/** toNodeId omitted: each key is re-picked by the assignment rules (never the same node). */
export async function moveNodeKeys(
  id: number,
  toNodeId?: number
): Promise<{ moved: number; left: number }> {
  const { data } = await apiClient.post<{ moved: number; left: number }>(
    `/admin/relay/nodes/${id}/move-keys`,
    toNodeId === undefined ? {} : { to_node_id: toNodeId }
  )
  return data
}

// ---- user assignment (novice client) ----

export async function userAssignmentSummary(): Promise<Record<string, number>> {
  const { data } = await apiClient.get<{ nodes: Record<string, number> }>('/admin/relay/users/assignment')
  return data?.nodes ?? {}
}

export interface RelayUserState {
  user_id: number
  assignment?: { node_id: number; reason?: string; pinned_until?: string; assigned_at: string }
  leases: Array<{
    node_id: number
    node_name?: string
    dimension: string
    granted: string
    expires_at: string
    node_online: boolean
  }>
}

export async function userRelayState(userId: number): Promise<RelayUserState> {
  const { data } = await apiClient.get<RelayUserState>(`/admin/relay/users/${userId}`)
  return data
}

export async function rebalanceUsers(): Promise<{ users: number }> {
  const { data } = await apiClient.post<{ users: number }>('/admin/relay/users/rebalance')
  return data
}

export async function moveUser(userId: number, nodeId: number): Promise<RelayUserNodeResult> {
  const { data } = await apiClient.post<RelayUserNodeResult>(`/admin/relay/users/${userId}/move`, {
    node_id: nodeId
  })
  return data
}

export async function pinUser(userId: number, nodeId: number, until: string): Promise<RelayUserNodeResult> {
  const { data } = await apiClient.post<RelayUserNodeResult>(`/admin/relay/users/${userId}/pin`, {
    node_id: nodeId,
    until
  })
  return data
}

export async function unpinUser(userId: number): Promise<void> {
  await apiClient.post(`/admin/relay/users/${userId}/unpin`)
}

// ---- notifications ----

export async function getNotifications(): Promise<RelayNotificationConfig> {
  const { data } = await apiClient.get<RelayNotificationConfig>('/admin/relay/notifications')
  return data
}

export async function updateNotifications(
  upd: RelayNotificationUpdate
): Promise<RelayNotificationConfig> {
  const { data } = await apiClient.put<RelayNotificationConfig>('/admin/relay/notifications', upd)
  return data
}

export async function testNotification(): Promise<{ sent: boolean }> {
  const { data } = await apiClient.post<{ sent: boolean }>('/admin/relay/notifications/test')
  return data
}

// ---- logs (queried live from the nodes) ----

export async function nodeLogs(id: number, filters: RelayLogFilters = {}): Promise<RelayLogsResponse> {
  const { data } = await apiClient.get<RelayLogsResponse>(`/admin/relay/nodes/${id}/logs`, {
    params: logParams(filters)
  })
  return data
}

export async function allNodeLogs(filters: RelayLogFilters = {}): Promise<RelayLogsResponse> {
  const { data } = await apiClient.get<RelayLogsResponse>('/admin/relay/logs', { params: logParams(filters) })
  return data
}

// ---- key rotation ----

export async function listKeys(purpose: RelayKeyPurpose): Promise<RelayKeyInfo[]> {
  const { data } = await apiClient.get<RelayKeyInfo[]>(`/admin/relay/keys/${purpose}`)
  return Array.isArray(data) ? data : []
}

export async function stageKey(purpose: RelayKeyPurpose): Promise<RelayKeyInfo> {
  const { data } = await apiClient.post<RelayKeyInfo>(`/admin/relay/keys/${purpose}/stage`)
  return data
}

export async function activateKey(purpose: RelayKeyPurpose, version: number): Promise<void> {
  await apiClient.post(`/admin/relay/keys/${purpose}/${version}/activate`)
}

export async function retireKey(purpose: RelayKeyPurpose, version: number): Promise<void> {
  await apiClient.post(`/admin/relay/keys/${purpose}/${version}/retire`)
}

export const relayAPI = {
  getStatus,
  setEnabled,
  getGeneralConfig,
  updateGeneralConfig,
  listNodes,
  nodeHealths,
  checkNodeDomain,
  activateNode,
  updateNodeDomain,
  rejectNode,
  rejectAllPending,
  drainNode,
  undrainNode,
  disableNode,
  enableNode,
  revokeNode,
  setNodeAllowMultiIP,
  reclaimNodeQuota,
  reclaimUserQuota,
  replaceNode,
  keyAssignmentSummary,
  assignUnassignedKeys,
  moveKeys,
  moveNodeKeys,
  userAssignmentSummary,
  userRelayState,
  rebalanceUsers,
  moveUser,
  pinUser,
  unpinUser,
  getNotifications,
  updateNotifications,
  testNotification,
  nodeLogs,
  allNodeLogs,
  listKeys,
  stageKey,
  activateKey,
  retireKey
}

export default relayAPI
