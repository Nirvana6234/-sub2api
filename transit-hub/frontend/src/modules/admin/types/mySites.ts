export interface MySiteGroupRef {
  siteId: string
  groupName: string
  /**
   * 绑定到本方 Sub2API 的账号 ID，用于按该账号的真实成本倍率核算毛利。
   * 未绑定时为 undefined —— 此时成本按未知处理，该数据源不参与毛利计算，
   * 绝不退回上游标称倍率（上游标称的是它的售价，不是我们的进货成本）。
   */
  sub2apiAccountId?: string | null
}

export type AutoPricingSource = 'primary_upstream' | 'lowest_upstream' | 'highest_upstream' | 'average_upstream'
export type AutoPricingStrategy = 'fixed' | 'percentage'
export type AutoPricingRunStatus = 'applied' | 'skipped' | 'threshold_exceeded' | 'failed'
export type AutoPricingRunTrigger = 'after_sync' | 'manual'

export interface AutoPricingRunResult {
  status?: AutoPricingRunStatus
  reason?: string
  trigger?: AutoPricingRunTrigger | string
  ranAt?: string
  oldReference?: number | null
  newReference?: number | null
  targetMultiplier?: number | null
  oldOwnMultiplier?: number | null
  newOwnMultiplier?: number | null
}

export interface MySiteMapping {
  ownGroup: string
  upstreamTargets: MySiteGroupRef[]
  enableAutoPricing?: boolean
  autoPricingSource?: AutoPricingSource
  primaryUpstreamSiteId?: string
  primaryUpstreamGroupName?: string
  autoPricingStrategy?: AutoPricingStrategy
  fixedIncrease?: number
  percentageIncrease?: number
  adjustThresholdPercent?: number
  minMultiplier?: number | null
  maxMultiplier?: number | null
  enableAutoPricingNotify?: boolean
  autoPricingNotifyBotIds?: string[]
  autoPricingNotifyTemplate?: string
  lastAutoPricingRun?: AutoPricingRunResult | null
}

export interface RunAutoPricingRequest {
  ownGroup: string
}

export interface RunAutoPricingResponse {
  result: AutoPricingRunResult
  mapping: MySiteMapping
}

export interface MySiteStatus {
  authenticated: boolean
  baseUrl?: string
  email?: string
  mappings?: MySiteMapping[]
}

export interface MySiteMappingOptionsResponse {
  ownGroups: MySiteMappingOwnGroupOption[]
  mappings: MySiteMapping[]
  upstreamTargetMultipliers?: MySiteUpstreamTargetMultiplier[]
  staleOwnGroups?: string[]
  staleTargets?: MySiteGroupRef[]
  connectionCapabilities?: ConnectionCapabilities
  costAccounts?: MySiteCostAccount[]
}

/** 成本倍率的出处。none 同时覆盖"未绑定账号"和"账号未声明成本"。 */
export type CostRateSource = 'manual' | 'probe' | 'column' | 'none'

/**
 * 可绑定的 Sub2API 账号及其成本倍率。
 * costRateMultiplier 由 Sub2API 按「手工值 > 新鲜探测值 > 列值」解析后给出；
 * null 表示无人声明过成本，前端必须把该数据源排除出毛利计算。
 */
export interface MySiteCostAccount {
  id: string
  name: string
  baseUrl?: string
  costRateMultiplier: number | null
  costRateSource: CostRateSource | string
}

export interface MySiteUpstreamTargetMultiplier extends MySiteGroupRef {
  multiplier: number | null
  stale: boolean
  source?: 'live' | 'cached' | string
}

export interface ConnectionCapabilities {
  mode: 'account' | 'channel' | string
  requiresGroupType: boolean
  requiresChannelType: boolean
  channelTypes?: NewAPIChannelType[]
  suggestedChannelTypeByGroup?: Record<string, number>
}

export interface MySiteUpstreamTargetOption extends MySiteGroupRef {
  siteName: string
  platform: string
  multiplier: number | null
  multiplierMode?: string
  stale: boolean
  source?: 'live' | 'cached' | 'sync' | string
}

export interface MySiteMappingOwnGroupOption {
  id: string
  siteName: string
  groupName: string
  multiplier: number
  platform: string
  status: string
  isExclusive: boolean
  subscriptionType: string
}

export interface RealConnectRequest {
  upstreamSiteId: string
  upstreamGroupId: string
  upstreamGroupName: string
  groupType: string
  channelType?: number
  ownGroupIds: string[]
  addToPricingMapping?: boolean
  operationId?: string
}

export interface NewAPIChannelType {
  id: number
  name: string
}

export const NEW_API_CHANNEL_TYPES: NewAPIChannelType[] = [
  { id: 1, name: 'OpenAI' },
  { id: 2, name: 'Midjourney' },
  { id: 3, name: 'Azure' },
  { id: 4, name: 'Ollama' },
  { id: 5, name: 'MidjourneyPlus' },
  { id: 6, name: 'OpenAIMax' },
  { id: 7, name: 'OhMyGPT' },
  { id: 8, name: 'Custom' },
  { id: 9, name: 'AILS' },
  { id: 10, name: 'AIProxy' },
  { id: 11, name: 'PaLM' },
  { id: 12, name: 'API2GPT' },
  { id: 13, name: 'AIGC2D' },
  { id: 14, name: 'Anthropic' },
  { id: 15, name: 'Baidu' },
  { id: 16, name: 'Zhipu' },
  { id: 17, name: 'Ali' },
  { id: 18, name: 'Xunfei' },
  { id: 19, name: '360' },
  { id: 20, name: 'OpenRouter' },
  { id: 21, name: 'AIProxyLibrary' },
  { id: 22, name: 'FastGPT' },
  { id: 23, name: 'Tencent' },
  { id: 24, name: 'Gemini' },
  { id: 25, name: 'Moonshot' },
  { id: 26, name: 'ZhipuV4' },
  { id: 27, name: 'Perplexity' },
  { id: 31, name: 'LingYiWanWu' },
  { id: 33, name: 'AWS' },
  { id: 34, name: 'Cohere' },
  { id: 35, name: 'MiniMax' },
  { id: 36, name: 'SunoAPI' },
  { id: 37, name: 'Dify' },
  { id: 38, name: 'Jina' },
  { id: 39, name: 'Cloudflare' },
  { id: 40, name: 'SiliconFlow' },
  { id: 41, name: 'VertexAI' },
  { id: 42, name: 'Mistral' },
  { id: 43, name: 'DeepSeek' },
  { id: 44, name: 'MokaAI' },
  { id: 45, name: 'VolcEngine' },
  { id: 46, name: 'BaiduV2' },
  { id: 47, name: 'Xinference' },
  { id: 48, name: 'xAI' },
  { id: 49, name: 'Coze' },
  { id: 50, name: 'Kling' },
  { id: 51, name: 'Jimeng' },
  { id: 52, name: 'Vidu' },
  { id: 53, name: 'Submodel' },
  { id: 54, name: 'DoubaoVideo' },
  { id: 55, name: 'Sora' },
  { id: 56, name: 'Replicate' },
  { id: 57, name: 'Codex' },
]

// Used only while a new frontend is temporarily connected to an older backend
// that does not yet return connectionCapabilities.
export const LEGACY_NEW_API_CHANNEL_SUGGESTIONS: Record<string, number> = {
  openai: 1,
  anthropic: 14,
  gemini: 24,
  deepseek: 43,
}

export interface RealConnection {
  id: string
  upstreamSiteId: string
  upstreamGroupId: string
  upstreamGroupName: string
  upstreamKeyId: string
  upstreamKey?: string
  adminAccountId: string
  adminAccountName: string
  ownGroupIds: string[]
  ownGroupNames?: string[]
  groupType: string
  provisioningMode?: 'legacy' | 'managed' | 'existing' | string
  status?: string
  upstreamPlatform?: string
  adminPlatform?: string
  pricingMappingEnabled?: boolean
  canDeleteRemote?: boolean
  createdAt: string
}

export interface RealBindRequest {
  upstreamSiteId: string
  upstreamGroupId: string
  upstreamGroupName: string
  upstreamKeyId: string
  upstreamKey?: string
  ownGroupIds: string[]
  groupType: string
  adminGroupId?: string
  adminResourceId?: string
  addToPricingMapping?: boolean
  operationId?: string
}

export interface UpstreamKeyItem {
  id: string
  key?: string
  keyPreview?: string
  name: string
  groupId: string
  groupName: string
  status: string
}

/** 上游 Key 连通性测试：一个阶段的结果。 */
export interface UpstreamKeyTestStage {
  ok: boolean
  /** 上一阶段就失败时后续阶段标成跳过，不要显示成「失败」——那会把人引去查错地方。 */
  skipped: boolean
  latencyMs: number
  errorKey: string
  detail: string
}

export interface UpstreamKeyTestRequest {
  upstreamSiteId: string
  upstreamGroupId: string
  upstreamGroupName: string
  /** 留空时后端自己挑：优先已对接连接用的那个 Key。 */
  upstreamKeyId?: string
  model?: string
}

export interface UpstreamKeyTestResponse {
  keyId: string
  keyName: string
  keyPreview: string
  /** 打 /v1/models：验证 key 有效并拿到模型池。 */
  models: UpstreamKeyTestStage
  /** 拿其中一个模型发一次 max_tokens=1 的真实请求。 */
  chat: UpstreamKeyTestStage
  modelCount: number
  modelSample: string[]
  testedModel: string
}

export interface UpstreamKeyModelsResponse {
  keyId: string
  keyName: string
  keyPreview: string
  models: string[]
}

export interface AdminResourceOption {
  id: string
  name: string
  type: string
  status: string
  platform: string
  groupIds: string[]
}

export interface RealConnectResponse {
  connection: RealConnection
}

export interface RealDisconnectRequest {
  connectionId: string
  mode: 'unlink' | 'delete-key'
  removePricingMapping?: boolean
}
