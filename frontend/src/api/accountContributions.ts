import { apiClient } from './client'
import type { Account, AccountPlatform, AccountUsageInfo, AccountUsageStatsResponse, Group, ProxyProtocol } from '@/types'

export type ContributionMode = 'oauth' | 'api_key'

export interface SubmitAccountContributionRequest {
  mode: ContributionMode
  name?: string
  content?: string
  contents?: string[]
  platform?: AccountPlatform
  api_key?: string
  base_url?: string
  concurrency?: number
  priority?: number
  load_factor?: number
	group_ids?: number[]
	pool_group_id?: number
  test_model_id?: string
  proxy_id?: number
}

export interface AccountContributionResultItem {
  index: number
  name?: string
  account_id?: number
  status: 'created' | 'skipped' | 'failed'
  message?: string
  latency_ms?: number
}

export interface AccountContributionResult {
  total: number
  created: number
  failed: number
  skipped: number
  limit: number
  used: number
  remaining: number
  items: AccountContributionResultItem[]
  warnings?: Array<{ index: number; name?: string; message: string }>
}

export interface ContributionAuthURLResult {
  auth_url: string
  session_id: string
}

export type OpenAIContributionAuthURLResult = ContributionAuthURLResult

export interface OpenAIContributionAuthRequest {
  name?: string
  concurrency?: number
  priority?: number
  load_factor?: number
	group_ids?: number[]
	pool_group_id?: number
  test_model_id?: string
  proxy_id?: number
  session_id?: string
  code?: string
  state?: string
  redirect_uri?: string
  refresh_token?: string
  access_token?: string
}

export interface AnthropicContributionAuthRequest {
  name?: string
  concurrency?: number
  priority?: number
  load_factor?: number
	group_ids?: number[]
	pool_group_id?: number
  test_model_id?: string
  proxy_id?: number
  session_id?: string
  code?: string
}

export interface AccountContributionList {
  items: Account[]
  total: number
  page: number
  limit: number
  wallet: { balance: number; earned_total: number; spent_total: number }
  income_rates?: {
    share_reward_rate_percent: number
    own_income_rate_percent: number
  }
}

export interface ContributionAccountUsageSummary {
  upstream: AccountUsageInfo
  stats: AccountUsageStatsResponse
  days: number
}

export interface UpdateAccountContributionRequest {
  name?: string
  api_key?: string
  base_url?: string
  concurrency?: number
  priority?: number
  load_factor?: number
	group_ids?: number[]
	pool_group_id?: number
  proxy_id?: number
  status?: 'active' | 'disabled'
}

export interface ContributionProxy {
  id: number
  name: string
  protocol: ProxyProtocol
  host: string
  port: number
  username?: string
  has_password: boolean
  status: 'active' | 'inactive'
  created_at: string
  updated_at: string
}

export interface SaveContributionProxyRequest {
  name: string
  protocol: ProxyProtocol
  host: string
  port: number
  username?: string
  password?: string
  status?: 'active' | 'inactive'
}

export interface ContributionProxyTestResult {
  success: boolean
  message: string
  latency_ms?: number
  ip_address?: string
  city?: string
  region?: string
  country?: string
  country_code?: string
}

export async function listAccountContributions(page = 1, limit = 100): Promise<AccountContributionList> {
  const { data } = await apiClient.get<AccountContributionList>('/account-contributions', { params: { page, limit } })
  return data
}

// Contributors can bind their own accounts to active groups. The response uses
// the regular group shape and deliberately excludes administrator-only routing
// settings and model mappings.
export async function listContributionGroups(): Promise<Group[]> {
  const { data } = await apiClient.get<Group[]>('/account-contributions/groups')
  return data
}

export async function listContributionPoolGroups(): Promise<Group[]> {
	const { data } = await apiClient.get<Group[]>('/account-contributions/pool-groups')
	return data
}

export async function submitAccountContribution(payload: SubmitAccountContributionRequest): Promise<AccountContributionResult> {
  const { data } = await apiClient.post<AccountContributionResult>('/account-contributions', payload)
  return data
}

export async function generateOpenAIContributionAuthURL(payload: Pick<OpenAIContributionAuthRequest, 'proxy_id' | 'redirect_uri'>): Promise<ContributionAuthURLResult> {
  const { data } = await apiClient.post<OpenAIContributionAuthURLResult>('/account-contributions/openai/generate-auth-url', payload)
  return data
}

export async function createOpenAIContributionFromCode(payload: OpenAIContributionAuthRequest): Promise<AccountContributionResult> {
  const { data } = await apiClient.post<AccountContributionResult>('/account-contributions/openai/create-from-code', payload)
  return data
}

export async function createOpenAIContributionFromRefreshToken(payload: OpenAIContributionAuthRequest, mobile = false): Promise<AccountContributionResult> {
  const endpoint = mobile ? 'create-from-mobile-refresh-token' : 'create-from-refresh-token'
  const { data } = await apiClient.post<AccountContributionResult>(`/account-contributions/openai/${endpoint}`, payload)
  return data
}

export async function createOpenAIContributionFromCodexPAT(payload: OpenAIContributionAuthRequest): Promise<AccountContributionResult> {
  const { data } = await apiClient.post<AccountContributionResult>('/account-contributions/openai/create-from-codex-pat', payload)
  return data
}

export async function generateAnthropicContributionAuthURL(payload: Pick<AnthropicContributionAuthRequest, 'proxy_id'>): Promise<ContributionAuthURLResult> {
  const { data } = await apiClient.post<ContributionAuthURLResult>('/account-contributions/anthropic/generate-auth-url', payload)
  return data
}

export async function createAnthropicContributionFromCode(payload: AnthropicContributionAuthRequest): Promise<AccountContributionResult> {
  const { data } = await apiClient.post<AccountContributionResult>('/account-contributions/anthropic/create-from-code', payload)
  return data
}

export async function updateAccountContribution(id: number, payload: UpdateAccountContributionRequest): Promise<Account> {
  const { data } = await apiClient.put<Account>(`/account-contributions/${id}`, payload)
  return data
}

export async function getContributionAccountUsageSummary(id: number, force = false): Promise<ContributionAccountUsageSummary> {
  const { data } = await apiClient.get<ContributionAccountUsageSummary>(`/account-contributions/${id}/usage-summary`, {
    params: force ? { force: 'true' } : undefined,
  })
  return data
}

export async function deleteAccountContribution(id: number): Promise<void> {
  await apiClient.delete(`/account-contributions/${id}`)
}

export async function testAccountContribution(id: number, modelId = ''): Promise<{ status: string; error_message?: string; latency_ms?: number }> {
  const { data } = await apiClient.post<{ status: string; error_message?: string; latency_ms?: number }>(
    `/account-contributions/${id}/test`,
    { model_id: modelId },
  )
  return data
}

export async function listContributionProxies(): Promise<ContributionProxy[]> {
  const { data } = await apiClient.get<ContributionProxy[]>('/account-contributions/proxies')
  return data
}

export async function createContributionProxy(payload: SaveContributionProxyRequest): Promise<ContributionProxy> {
  const { data } = await apiClient.post<ContributionProxy>('/account-contributions/proxies', payload)
  return data
}

export async function updateContributionProxy(id: number, payload: Partial<SaveContributionProxyRequest>): Promise<ContributionProxy> {
  const { data } = await apiClient.put<ContributionProxy>(`/account-contributions/proxies/${id}`, payload)
  return data
}

export async function deleteContributionProxy(id: number): Promise<void> {
  await apiClient.delete(`/account-contributions/proxies/${id}`)
}

export async function testContributionProxy(id: number): Promise<ContributionProxyTestResult> {
  const { data } = await apiClient.post<ContributionProxyTestResult>(`/account-contributions/proxies/${id}/test`)
  return data
}

export default {
  list: listAccountContributions,
	listGroups: listContributionGroups,
	listPoolGroups: listContributionPoolGroups,
  submit: submitAccountContribution,
  generateOpenAIAuthURL: generateOpenAIContributionAuthURL,
  createOpenAIFromCode: createOpenAIContributionFromCode,
  createOpenAIFromRefreshToken: createOpenAIContributionFromRefreshToken,
  createOpenAIFromCodexPAT: createOpenAIContributionFromCodexPAT,
  generateAnthropicAuthURL: generateAnthropicContributionAuthURL,
  createAnthropicFromCode: createAnthropicContributionFromCode,
  update: updateAccountContribution,
  getUsageSummary: getContributionAccountUsageSummary,
  delete: deleteAccountContribution,
  test: testAccountContribution,
  listProxies: listContributionProxies,
  createProxy: createContributionProxy,
  updateProxy: updateContributionProxy,
  deleteProxy: deleteContributionProxy,
  testProxy: testContributionProxy,
}
