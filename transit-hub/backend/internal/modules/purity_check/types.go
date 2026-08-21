// Package purity_check 把 GPT-5.6 混用检测器（旁路 Python 服务）接进 transithub：
// 解析上游账号凭据 → 排队 → 驱动检测器 → 存报告。
//
// 三条来自检测器本身的硬约束，决定了这个模块的形状：
//
//  1. 检测器同一时刻只允许一个会话（再 start 会返回 400），所以必须串行排队；
//  2. 「官方档位」由归一化配置的哈希与内置预设逐位比对决定，改任何一个参数
//     （连 workers 都算）都会掉成自定义档，结论从「强烈指向」降级为参考值——
//     所以配置必须从检测器 /api/bootstrap 原样透传，本模块绝不自己拼；
//  3. 检测会真实消耗上游 token，高档一次约 355 万输入 token。
package purity_check

import (
	"context"
	"time"

	"transithub/backend/internal/modules/upstream"
)

// Tier 是检测档位。取值必须与检测器 /api/bootstrap 返回的 single_presets 的
// key 一致，因为本模块是拿它当索引去取预设原文的。
type Tier string

const (
	TierLow    Tier = "low"
	TierMedium Tier = "medium"
	TierHigh   Tier = "high"
)

func (t Tier) valid() bool {
	switch t {
	case TierLow, TierMedium, TierHigh:
		return true
	}
	return false
}

// Status 是任务生命周期。queued/running 由 worker 推进，其余为终态。
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// 申报型号只能是这三个：检测器的指纹基线只为它们标定过，传别的会被拒。
const (
	ModelSol   = "gpt-5.6-sol"
	ModelTerra = "gpt-5.6-terra"
	ModelLuna  = "gpt-5.6-luna"
)

func validClaimedModel(model string) bool {
	switch model {
	case ModelSol, ModelTerra, ModelLuna:
		return true
	}
	return false
}

// Job 是一次检测任务。注意这里没有、也永远不会有 APIKey 字段。
type Job struct {
	ID             string `json:"id"`
	UserID         string `json:"-"`
	AdminAccountID string `json:"-"`

	AccountID       string `json:"accountId"`
	AccountName     string `json:"accountName"`
	AccountPlatform string `json:"accountPlatform"`
	BaseURL         string `json:"baseUrl"`

	Tier         Tier   `json:"tier"`
	ClaimedModel string `json:"claimedModel"`
	RequestModel string `json:"requestModel"`

	Status  Status `json:"status"`
	BatchID string `json:"batchId"`

	DetectorSessionID string `json:"-"`

	PlannedRequests   int `json:"plannedRequests"`
	CompletedRequests int `json:"completedRequests"`
	FailedRequests    int `json:"failedRequests"`

	ErrorKey    string `json:"errorKey"`
	ErrorDetail string `json:"errorDetail"`

	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`

	// QueuePosition 只在 status=queued 时有意义：本任务前面还排着几个。
	// 它是查询时现算的，不落库——落库的话每次出队都要全表重排。
	QueuePosition int `json:"queuePosition"`

	// Report 只在查询单个任务详情时填充，列表接口留空。
	Report *Report `json:"report,omitempty"`
}

// Report 是检测器报告。Payload 是原文，其余字段是抽出来给列表页用的摘要。
type Report struct {
	JobID string `json:"jobId"`
	// Payload 直接是检测器返回的 JSON 原文（json.RawMessage 透传，不做结构化解析）。
	// 检测器版本迭代会增减字段，结构化解析等于每次跟版本；原文透传由前端按需取。
	Payload []byte `json:"-"`

	OverallVerdict          string `json:"overallVerdict"`
	OutcomeCode             string `json:"outcomeCode"`
	JuiceVerdictState       string `json:"juiceVerdictState"`
	FingerprintModel        string `json:"fingerprintModel"`
	FingerprintVerdictState string `json:"fingerprintVerdictState"`
	FingerprintClaimMismatch bool  `json:"fingerprintClaimMismatch"`
	Official                bool   `json:"official"`

	// QualityNote 是检测器对结论质量的自评，例如「Juice 身份判定通过，但有效命中
	// 质量偏低」。有值就该显示——它是"结论没那么硬"的提示。
	QualityNote string `json:"qualityNote"`

	// SuccessfulRequests / TotalRequests 是本次真正打通了几条探针。
	// 必须显示：0/19 和 19/19 得出同一句「证据不足」，但含义天差地别。
	SuccessfulRequests int `json:"successfulRequests"`
	TotalRequests      int `json:"totalRequests"`

	// FailureHint 是第一条上游错误的脱敏描述（检测器已做过脱敏）。
	FailureHint string `json:"failureHint"`

	CreatedAt time.Time `json:"createdAt"`
}

// Target 是一个可检测的上游账号。BaseURL 在列表阶段可能为空——sub2api 的账号
// 列表接口不返回 base_url，要到导出凭据那一步才拿得到（见 upstream 模块注释）。
type Target struct {
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	Type      string `json:"type"`
	BaseURL   string `json:"baseUrl"`
	GroupIDs  []string `json:"groupIds"`

	// Eligible=false 时前端置灰，Reason 说明为什么不能测。
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
}

// TierInfo 是某个档位的成本预估，数据来自检测器 /api/detector/estimate，
// 不是本模块硬编码的——档位定义属于检测器，写死在这边迟早对不上。
type TierInfo struct {
	Tier Tier `json:"tier"`
	// TotalRequests 是逻辑任务数。实际 HTTP 请求可能是它的数倍：官方预设
	// retries=2，实测 19 条逻辑任务在全失败时发了 57 次 HTTP。
	TotalRequests int `json:"totalRequests"`
	// MaxHTTPRequests = TotalRequests * (retries + 1)，最坏情况下的请求数。
	MaxHTTPRequests           int    `json:"maxHttpRequests"`
	ApproximateInputTokens    int    `json:"approximateInputTokens"`
	Fixed32KRequests          int    `json:"fixed32kRequests"`
	EstimateDisclaimer        string `json:"estimateDisclaimer"`
}

// SubmitInput 是提交检测的请求体。AccountIDs 支持批量。
type SubmitInput struct {
	AccountIDs   []string `json:"accountIds"`
	Tier         Tier     `json:"tier"`
	ClaimedModel string   `json:"claimedModel"`
	// RequestModel 留空时回落到 ClaimedModel。
	RequestModel string `json:"requestModel"`
}

// ---- 依赖接口：与 connection_health 保持同一注入模式 ----

// AdminAccountResolver 解析当前用户所在的 workspace。
type AdminAccountResolver interface {
	RequireCurrentID(ctx context.Context, userID string) (string, error)
}

// SessionProvider 提供 admin 上游会话，由 *my_sites.Service 结构性满足。
type SessionProvider interface {
	RequireSession(ctx context.Context, userID string, adminAccountID string) (upstream.Session, error)
}

// AccountReader 是本模块对 upstream.PlatformService 的窄依赖。
// ResolveProbeCredential 返回的 Key 是明文，只允许在 worker 内存中传给检测器，
// 绝不落库、不写日志、不进任何响应体。
type AccountReader interface {
	ListAdminAllAccounts(session upstream.Session) ([]upstream.AdminGroupAccountInfo, error)
	ResolveProbeCredential(session upstream.Session, account upstream.AdminGroupAccountInfo) (upstream.ProbeCredential, error)
}

// ---- 错误 key ----

type requestError string

func (e requestError) Error() string { return string(e) }

const (
	ErrorRequest          = "admin.purityCheck.errors.request"
	ErrorUnknown          = "admin.purityCheck.errors.unknown"
	ErrorNotFound         = "admin.purityCheck.errors.notFound"
	ErrorNoCurrentAccount = "admin.purityCheck.errors.noCurrentAccount"
	ErrorDetectorUnavailable = "admin.purityCheck.errors.detectorUnavailable"
	ErrorDetectorBusy     = "admin.purityCheck.errors.detectorBusy"
	ErrorTargetNotFound   = "admin.purityCheck.errors.targetNotFound"
	ErrorTargetIneligible = "admin.purityCheck.errors.targetIneligible"
	ErrorInvalidTier      = "admin.purityCheck.errors.invalidTier"
	ErrorInvalidModel     = "admin.purityCheck.errors.invalidModel"
	ErrorAccountsFetch    = "admin.purityCheck.errors.accountsFetch"
	ErrorCredential       = "admin.purityCheck.errors.credential"
	ErrorNotCancellable   = "admin.purityCheck.errors.notCancellable"
	ErrorTooManyTargets   = "admin.purityCheck.errors.tooManyTargets"
	ErrorNotDeletable     = "admin.purityCheck.errors.notDeletable"
	// ErrorUpstreamUnreachable：探针一条都没打通，这次没有产出任何检测证据。
	ErrorUpstreamUnreachable = "admin.purityCheck.errors.upstreamUnreachable"
)

// 目标不合格的原因，前端据此显示置灰提示。
const (
	ReasonNotOpenAI    = "not_openai"
	ReasonNotAPIKey    = "not_api_key"
	ReasonNoBaseURL    = "no_base_url"
)

// maxTargetsPerSubmit 限制单次批量提交的账号数。串行队列下 12 个高档任务就是
// 4000 多万输入 token 和几个小时，给个上限免得手滑点全选。
const maxTargetsPerSubmit = 20
