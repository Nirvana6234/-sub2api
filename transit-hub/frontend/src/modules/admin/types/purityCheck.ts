// GPT-5.6 纯度检测的前端类型，与 backend/internal/modules/purity_check 的 JSON 一一对应。

export type PurityTier = 'low' | 'medium' | 'high'

export type PurityJobStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'

/** 申报型号只能是这三个：检测器的指纹基线只为它们标定过。 */
export const PURITY_CLAIMED_MODELS = ['gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna'] as const

export type PurityClaimedModel = (typeof PURITY_CLAIMED_MODELS)[number]

/** 目标不合格的原因，与后端 ReasonXxx 常量对应。 */
export type PurityTargetReason = '' | 'not_openai' | 'not_api_key' | 'no_base_url'

export type PurityTarget = {
  accountId: string
  name: string
  platform: string
  type: string
  baseUrl: string
  groupIds: string[] | null
  /**
   * Sub2API 已按「手工值 > 新鲜探测值 > 列值」解析好的上游成本倍率。
   * null = 没人声明过成本，要显示成「未声明」而不是 1x——那一列常年停在默认
   * 1.0000，把「不知道」显示成「原价」会差一到两个数量级。
   */
  costRateMultiplier: number | null
  costRateSource: PurityCostRateSource
  /** false 时前端置灰，reason 说明原因。 */
  eligible: boolean
  reason: PurityTargetReason
}

/** 成本倍率的出处，与后端 CostRateSource 对应。 */
export type PurityCostRateSource = '' | 'manual' | 'probe' | 'column' | 'none'

export type PurityTierInfo = {
  tier: PurityTier
  /** 逻辑任务数，也就是档位表上写的 19/49/158。 */
  totalRequests: number
  /**
   * 最坏情况下的真实 HTTP 请求数 = totalRequests × (retries+1)。
   * 官方预设 retries=2，所以低档最多会打 57 次而不是 19 次——这是要在
   * 确认弹窗里如实告诉用户的数字。
   */
  maxHttpRequests: number
  approximateInputTokens: number
  fixed32kRequests: number
  estimateDisclaimer: string
}

export type PurityReportSummary = {
  jobId: string
  overallVerdict: string
  outcomeCode: string
  juiceVerdictState: string
  fingerprintModel: string
  fingerprintVerdictState: string
  /** 指纹强指向的型号与申报型号不一致——列表页要标红的信号。 */
  fingerprintClaimMismatch: boolean
  /** false 表示这次跑的不是官方档位，结论只能当参考值。 */
  official: boolean
  /** 检测器对结论质量的自评，例如「有效命中质量偏低」。有值就该显示。 */
  qualityNote: string
  /**
   * 本次真正打通了几条探针。必须显示：0/19 和 19/19 会得出同一句
   * 「证据不足」，但前者是上游根本没通，后者才是真的测过了没抓到。
   */
  successfulRequests: number
  totalRequests: number
  /** 第一条上游错误的脱敏描述。 */
  failureHint: string
  createdAt: string
}

export type PurityJob = {
  id: string
  accountId: string
  accountName: string
  accountPlatform: string
  baseUrl: string
  tier: PurityTier
  claimedModel: string
  requestModel: string
  status: PurityJobStatus
  batchId: string
  plannedRequests: number
  completedRequests: number
  failedRequests: number
  errorKey: string
  errorDetail: string
  createdAt: string
  startedAt: string | null
  finishedAt: string | null
  updatedAt: string
  /** 排队中时表示前面还排着几个（全局，跨 workspace）。 */
  queuePosition: number
  report?: PurityReportSummary
}

export type PurityJobListResponse = {
  jobs: PurityJob[]
  /** 全局排队总数：检测器只有一条队列，只看自己的会低估等待时间。 */
  queuedTotal: number
  detectorAvailable: boolean
  /** 每个 workspace 保留的历史条数上限，超出的最旧记录会被自动删除。 */
  maxRetained: number
}

export type PuritySubmitInput = {
  accountIds: string[]
  tier: PurityTier
  claimedModel: string
  requestModel: string
}

export type PurityJobDetail = {
  job: PurityJob
  /** 检测器报告原文，字段随检测器版本变化，按需读取。 */
  reportPayload?: Record<string, unknown>
}
