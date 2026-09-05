package upstream

import (
	"context"
	"strings"
	"time"
)

type Platform string

const (
	PlatformAuto    Platform = "auto"
	PlatformNewAPI  Platform = "newapi"
	PlatformSub2API Platform = "sub2api"
)

// AmountCurrency 是平台管理端金额（营收、站点余额、账号成本）的原始币种。
type AmountCurrency string

const (
	AmountCurrencyUSD AmountCurrency = "USD"
	AmountCurrencyCNY AmountCurrency = "CNY"
)

// ReportingCurrency 返回该平台返回的金额本身是什么币种。
//
// sub2api 全链路人民币：支付宝充值 10 元对应余额 +10，usage_logs 的 actual_cost
// 与 total_account_cost 都是同一套人民币单位，不存在美元原值。new-api 则只有
// quota，fetchNewAPIAdminUsageStats 会按 quotaPerUnit 折成美元后返回。
//
// 分支必须与 FetchAdminUsageAccounting / FetchAdminSiteBalanceFiltered 的平台
// 分支一一对应：那两处的 default 走的是 sub2api 实现，所以这里的 default 也必须
// 是 CNY，否则未知平台会拿 sub2api 的人民币数字再乘一次汇率。
func (p Platform) ReportingCurrency() AmountCurrency {
	switch p {
	case PlatformNewAPI:
		return AmountCurrencyUSD
	default:
		return AmountCurrencyCNY
	}
}

type Status string

const (
	StatusConnecting Status = "connecting"
	StatusSyncing    Status = "syncing"
	StatusConnected  Status = "connected"
	StatusError      Status = "error"
)

const (
	ErrorNotFound                 = "admin.upstream.errors.notFound"
	ErrorInvalidURL               = "admin.upstream.errors.invalidUrl"
	ErrorAuth                     = "admin.upstream.errors.auth"
	ErrorAdminRequired            = "admin.upstream.errors.adminRequired"
	ErrorInteractiveLoginRequired = "admin.upstream.errors.interactiveLoginRequired"
	ErrorNetwork                  = "admin.upstream.errors.network"
	ErrorRequest                  = "admin.upstream.errors.request"
	ErrorInvalidResponse          = "admin.upstream.errors.invalidResponse"
	ErrorUnknown                  = "admin.upstream.errors.unknown"
	// ErrorSub2APIBulkUpdateUnsupported 表示当前 Sub2API 站点没有字段级批量更新能力。
	// 调度优先级/状态更新遇到该能力缺失时必须要求升级，绝不回退到整对象回写。
	ErrorSub2APIBulkUpdateUnsupported = "admin.upstream.errors.sub2APIBulkUpdateUnsupported"
)

// SSE 同步流事件类型。
const (
	SyncEventSyncing  = "syncing"
	SyncEventDone     = "done"
	SyncEventError    = "error"
	SyncEventComplete = "complete"
)

// SyncEvent 是 SSE 同步流中每个 data: 行的 JSON 载荷。
// 前端通过 event 字段判断当前阶段并更新对应站点卡片的进度。
type SyncEvent struct {
	Event    string    `json:"event"`
	SiteID   string    `json:"siteId"`
	Attempt  int       `json:"attempt,omitempty"`
	MaxRetry int       `json:"maxRetry,omitempty"`
	Site     *Response `json:"site,omitempty"`
	ErrorKey string    `json:"errorKey,omitempty"`
}

// SyncEventCallback 是 SSE 事件的推送回调，由 handler 注入，
// 负责将事件序列化并写入 ResponseWriter。
type SyncEventCallback func(SyncEvent)

type AuthMode string

const (
	AuthModePassword AuthMode = "password"
	AuthModeToken    AuthMode = "token"
	AuthModeUserKey  AuthMode = "user_key"
)

type MetricValue struct {
	Value   *float64 `json:"value"`
	Display string   `json:"display"`
}

type GroupInfo struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Platform          *string  `json:"platform"`
	Multiplier        *float64 `json:"multiplier"`
	MultiplierDisplay string   `json:"multiplierDisplay"`
	MultiplierMode    string   `json:"multiplierMode,omitempty"`
	// 以下字段为 sub2api 专属倍率合并规则新增的向后兼容字段：/groups/available 默认倍率
	// 与 /groups/rates 专属倍率覆盖后，Multiplier 始终表示最终生效倍率；这些字段仅供前端
	// 展示"默认倍率 -> 专属倍率"提示，不参与业务计算。旧数据缺少这些字段时 omitempty 生效，
	// 前端按无专属倍率处理。
	DefaultMultiplier          *float64 `json:"defaultMultiplier,omitempty"`
	DefaultMultiplierDisplay   string   `json:"defaultMultiplierDisplay,omitempty"`
	DedicatedMultiplier        *float64 `json:"dedicatedMultiplier,omitempty"`
	DedicatedMultiplierDisplay string   `json:"dedicatedMultiplierDisplay,omitempty"`
	HasDedicatedMultiplier     bool     `json:"hasDedicatedMultiplier"`
}

// SnapshotGroup and SnapshotWriter keep upstream decoupled from the group_rates
// module while still allowing successful metric refreshes to publish multiplier
// history. Any implementation can be injected by the HTTP server assembly layer.
type SnapshotGroup struct {
	ID         string
	Name       string
	Platform   *string
	Multiplier *float64
}

type SnapshotWriter interface {
	SaveSiteSnapshot(ctx context.Context, userID string, adminAccountID string, siteID string, siteName string, sitePlatform Platform, groups []SnapshotGroup) error
}

type Metrics struct {
	Balance         MetricValue `json:"balance"`
	TodayConsume    MetricValue `json:"todayConsume"`
	HistoryRecharge MetricValue `json:"historyRecharge"`
	Group           GroupInfo   `json:"group"`
	Groups          []GroupInfo `json:"groups"`
}

// MultiplierEvent 是一次上游分组倍率变化的权威记录。
// mapped 表示变化发生时该上游分组是否已被我方映射对接；日报只汇总未映射事件。
type MultiplierEvent struct {
	ID                 string
	UserID             string
	AdminAccountID     string
	SiteID             string
	SiteName           string
	GroupID            string
	GroupName          string
	PreviousMultiplier float64
	CurrentMultiplier  float64
	Mapped             bool
	Notified           bool
	ObservedAt         time.Time
}

type CreateRequest struct {
	Name         string   `json:"name"`
	SiteURL      string   `json:"siteUrl"`
	Platform     Platform `json:"platform"`
	AuthMode     AuthMode `json:"authMode"`
	Account      string   `json:"account"`
	Password     string   `json:"password"`
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken"`
	TokenType    string   `json:"tokenType"`
	UserID       string   `json:"userId"`
	Remark       string   `json:"remark"`
	RechargeRate float64  `json:"rechargeRate"`
}

type UpdateRequest struct {
	Name         string   `json:"name"`
	SiteURL      string   `json:"siteUrl"`
	Platform     Platform `json:"platform"`
	AuthMode     AuthMode `json:"authMode"`
	Account      string   `json:"account"`
	Password     string   `json:"password"`
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken"`
	TokenType    string   `json:"tokenType"`
	UserID       string   `json:"userId"`
	Remark       string   `json:"remark"`
	RechargeRate float64  `json:"rechargeRate"`
}

// SiteSettings 站点级预警覆盖配置。nil 表示使用全局默认值。
type SiteSettings struct {
	BalanceThreshold        *float64 `json:"balanceThreshold"`
	ManualAccountingEnabled bool     `json:"manualAccountingEnabled"`
	// ManualGroupMultipliers is TransitHub-owned pricing for manual ledger
	// fallback. It intentionally does not depend on the upstream group API.
	ManualGroupMultipliers map[string]float64 `json:"manualGroupMultipliers"`
}

// RechargeEntry is an immutable manual top-up record. Historical balances are
// entered as the first record, so the total remains auditable after upgrades.
type RechargeEntry struct {
	ID        string  `json:"id"`
	Amount    float64 `json:"amount"`
	Note      string  `json:"note"`
	CreatedAt int64   `json:"createdAt"`
}

type CreateRechargeRequest struct {
	Amount float64 `json:"amount"`
	Note   string  `json:"note"`
}

type ManualAccountingSummary struct {
	RechargeTotal float64 `json:"rechargeTotal"`
	ConsumedTotal float64 `json:"consumedTotal"`
}

// BalanceSnapshot 是某个上游站点在某一天的余额快照，用于计算真实成本。
type BalanceSnapshot struct {
	SiteID       string  `json:"siteId"`
	SnapshotDate string  `json:"snapshotDate"` // YYYY-MM-DD
	BalanceUSD   float64 `json:"balanceUsd"`
	BalanceCNY   float64 `json:"balanceCny"`
	RechargeRate float64 `json:"rechargeRate"`
	CreatedAt    int64   `json:"createdAt"`
}

type Site struct {
	ID                string       `json:"id"`
	UserID            string       `json:"-"`
	AdminAccountID    string       `json:"-"`
	Name              string       `json:"name"`
	BaseURL           string       `json:"baseUrl"`
	Platform          Platform     `json:"platform"`
	RequestedPlatform Platform     `json:"requestedPlatform"`
	Account           string       `json:"account"`
	Remark            string       `json:"remark"`
	RechargeRate      float64      `json:"rechargeRate"`
	Status            Status       `json:"status"`
	ErrorKey          *string      `json:"errorKey"`
	Metrics           Metrics      `json:"metrics"`
	Settings          SiteSettings `json:"settings"`
	LastSyncedAt      *int64       `json:"lastSyncedAt"`
	Session           *Session     `json:"-"`
}

type Response struct {
	ID                string       `json:"id"`
	UserID            string       `json:"-"`
	AdminAccountID    string       `json:"-"`
	Name              string       `json:"name"`
	BaseURL           string       `json:"baseUrl"`
	Platform          Platform     `json:"platform"`
	RequestedPlatform Platform     `json:"requestedPlatform"`
	Account           string       `json:"account"`
	Remark            string       `json:"remark"`
	RechargeRate      float64      `json:"rechargeRate"`
	Status            Status       `json:"status"`
	ErrorKey          *string      `json:"errorKey"`
	Metrics           Metrics      `json:"metrics"`
	Settings          SiteSettings `json:"settings"`
	LastSyncedAt      *int64       `json:"lastSyncedAt"`
}

type Session struct {
	Platform    Platform
	BaseURL     string
	Cookie      string
	UserID      string
	AccessToken string
	// AdminAPIKey 是 Sub2API 管理路由使用的 Admin API Key，通过 x-api-key 发送。
	// 它与用户 JWT/AccessToken 分开保存，避免被误发到普通用户路由。
	AdminAPIKey  string `json:",omitempty"`
	RefreshToken string
	TokenType    string
	// ExpiresAt 是 access token 过期的毫秒时间戳，来自登录/刷新响应的 expires_in。
	// 临期时由 refreshIfNeeded 用 refresh token 自动换新（refresh token 本身无过期时间）。
	ExpiresAt *int64
	// QuotaPerUnit 是 new-api 的 quota 换算单位（来自 /api/status 的 quota_per_unit 字段）。
	// sub2api 不使用此字段。为 0 时回退到默认值 500000。
	QuotaPerUnit float64
}

// IsAuthenticated 按平台判断会话是否有效（已持有登录凭证）。
// sub2api 支持用户 AccessToken 或 Admin API Key；new-api 需要 UserID，
// 并支持 Cookie 会话或“个人设置 -> 系统访问令牌”生成的 Access Token。
func (s Session) IsAuthenticated() bool {
	switch s.Platform {
	case PlatformNewAPI:
		return strings.TrimSpace(s.UserID) != "" &&
			(strings.TrimSpace(s.Cookie) != "" || strings.TrimSpace(s.AccessToken) != "")
	case PlatformSub2API:
		return strings.TrimSpace(s.AccessToken) != "" || strings.TrimSpace(s.AdminAPIKey) != ""
	default:
		return strings.TrimSpace(s.AccessToken) != "" || strings.TrimSpace(s.AdminAPIKey) != "" ||
			(strings.TrimSpace(s.Cookie) != "" && strings.TrimSpace(s.UserID) != "")
	}
}

// Sub2APIKeyItem 表示从上游 Sub2API 站点获取的单个 API Key 信息。
// 用于手动绑定时展示 key 列表供用户选择。
type Sub2APIKeyItem struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	GroupID   string `json:"groupId"`
	GroupName string `json:"groupName"`
	Status    string `json:"status"`
}

type LoginResult struct {
	Platform Platform
	Session  Session
	Metrics  Metrics
}

// GroupDailyStat 是上游站点按分组统计的当日金额。
//
// TodayActualCost 是上游站点对用户的扣费，**不是**我们的采购成本，
// 只用于手工记账兜底。要拿采购成本请走 FetchSub2APIAccountCostRange，
// 那条路查的是本方 Sub2API 上明确绑定的账号成本。
type GroupDailyStat struct {
	GroupName       string  `json:"groupName"`
	TodayActualCost float64 `json:"todayActualCost"`
}

// GroupAccounting 是一个自有分组在某段时间内的营收与采购成本。
//
// 两个数字来自同一次 /api/v1/admin/dashboard/groups 调用：actual_cost 是对我方
// 用户的扣费（营收），account_cost 是付给上游账号的采购成本。**同币种**，
// 对 sub2api 平台都是人民币，相减即毛利，绝不能再乘汇率
// （生产上出过 ¥409.57 被乘 7 变成 ¥2866.96 的事故）。
type GroupAccounting struct {
	GroupName string
	// RevenueAmount 是对用户的扣费合计（actual_cost）。
	RevenueAmount float64
	// CostAmount 是采购成本合计（account_cost）。
	CostAmount float64
	// CostKnown 区分「成本是 0」和「这一版上游不返回 account_cost」。
	// 为 false 时不能把毛利算成等于营收——那会让分组看起来全是纯利。
	CostKnown bool
}

// GrossMargin 返回毛利率（0-1）。营收为 0 或成本口径缺失时返回 false。
func (g GroupAccounting) GrossMargin() (float64, bool) {
	if !g.CostKnown || g.RevenueAmount <= 0 {
		return 0, false
	}
	return (g.RevenueAmount - g.CostAmount) / g.RevenueAmount, true
}

type AdminSiteBalance struct {
	Balance float64 `json:"balance"`
}

// BalanceFilter 控制统计站点用户余额时的过滤条件。
// 由仪表盘模块创建，传递给 PlatformService 在分页遍历用户时应用。
type BalanceFilter struct {
	ExcludeAdmin    bool      // 是否排除 admin 角色用户
	ExcludeBalances []float64 // 需要排除的精确余额值（如 0、0.1、1 等）
}

// Sub2APIAdminUser 是 GET /api/v1/admin/users/:id 返回的用户详情中，工单模块"Sub2API 用户
// 资料"弹窗需要展示的只读字段。字段在远端响应中不存在或类型不匹配时保持零值/nil，
// 由调用方（tickets.Service）按需降级展示，不在这里伪造数据。
type Sub2APIAdminUser struct {
	ID            string
	Email         string
	Username      string
	Role          string
	Status        string
	Balance       *float64
	FrozenBalance *float64
	Concurrency   *int
	RPMLimit      *int
	CreatedAt     *time.Time
	LastUsedAt    *time.Time
}

// Sub2APIAdminUsersQuery 是 Sub2API admin 用户分页列表的安全查询对象。
// 调用方只能通过这些显式字段影响远端查询，PlatformService 会继续做白名单和分页夹紧。
type Sub2APIAdminUsersQuery struct {
	Page      int
	PageSize  int
	Status    string
	Role      string
	Search    string
	SortBy    string
	SortOrder string
	Timezone  string
}

type Sub2APIAdminUsersPage struct {
	Items    []Sub2APIAdminUser
	Total    int
	Page     int
	PageSize int
	Pages    int
	// TotalKnown/PagesKnown let batch jobs distinguish real upstream pagination
	// metadata from local fallbacks, so all-mode jobs never silently truncate an
	// unknown-length user stream.
	TotalKnown bool
	PagesKnown bool
}

// Sub2APIUserBreakdownQuery is the explicit contract for the Sub2API admin
// leaderboard source endpoint. The upstream end_date is exclusive.
type Sub2APIUserBreakdownQuery struct {
	StartDate string
	EndDate   string
	SortBy    string
	Limit     int
	Timezone  string
}

// Sub2APIUserBreakdownItem is one user row from
// /api/v1/admin/dashboard/user-breakdown. Optional token/cost fields stay at
// zero when older Sub2API deployments omit them.
type Sub2APIUserBreakdownItem struct {
	UserID       string
	Email        string
	Requests     int
	InputTokens  int64
	OutputTokens int64
	CacheTokens  int64
	TotalTokens  int64
	Cost         float64
	ActualCost   float64
}

type Sub2APIUserBreakdown struct {
	Users     []Sub2APIUserBreakdownItem
	StartDate string
	EndDate   string
}

// Sub2APIBalanceHistoryItem 是 Sub2API 用户余额/充值历史中的单条记录。
type Sub2APIBalanceHistoryItem struct {
	ID        string
	Type      string
	Amount    *float64
	Note      string
	CreatedAt *time.Time
}

// Sub2APIUserBalanceHistory 是 GET /api/v1/admin/users/:id/balance-history 的解析结果。
type Sub2APIUserBalanceHistory struct {
	Items          []Sub2APIBalanceHistoryItem
	Total          int
	TotalRecharged *float64
}

// KeyUsageTodayStat 是平台层返回的单个 key 今日消费统计（上游平台原始金额，未乘以站点 rechargeRate）。
type KeyUsageTodayStat struct {
	KeyID       string
	KeyName     string
	GroupName   string
	TodayAmount float64
}

// KeyUsageTodayItem 是仪表盘「今日成本」下钻明细中单个 key 的聚合结果（已按站点 rechargeRate 换算）。
type KeyUsageTodayItem struct {
	SiteID       string
	SiteName     string
	Platform     Platform
	KeyID        string
	KeyName      string
	GroupName    string
	TodayAmount  float64
	RawAmount    float64
	RechargeRate float64
}

// KeyUsageCollectionError 表示跨多个上游站点采集 Key 用量时有站点失败。
// Items 仍由调用方通过正常返回值获得；FailedSites < TotalSites 时属于部分成功，
// 调用方可以展示已成功数据并明确标注缺失范围，而不必把失败站点静默当成零消费。
type KeyUsageCollectionError struct {
	FailedSites int
	TotalSites  int
	Cause       error
}

func (e *KeyUsageCollectionError) Error() string {
	if e == nil || e.Cause == nil {
		return ErrorRequest
	}
	return e.Cause.Error()
}

func (e *KeyUsageCollectionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// BalanceBreakdownItem 是仪表盘「上游总余额」下钻明细中单个站点的余额展示数据。
// Balance/RawBalance 为 nil 表示该站点余额未知（未配置 rechargeRate 或尚未同步成功）。
type BalanceBreakdownItem struct {
	SiteID       string
	SiteName     string
	Platform     Platform
	Balance      *float64
	RawBalance   *float64
	RechargeRate float64
	LastSyncedAt *int64
	Status       Status
}

// AdminUsageAccounting 是 admin 站点同一次用量查询返回的营收与上游成本。
//
// 两者同源是刻意的：来自同一段 usage_logs、同一时间范围、同一时区，因此可以
// 直接相减得到毛利，不存在口径或日界错位。
type AdminUsageAccounting struct {
	// RevenueUSD 是用户侧实际扣费合计（sub2api 的 total_actual_cost）。
	RevenueUSD float64
	// AccountCostUSD 是上游成本合计（sub2api 的 total_account_cost），按每条
	// usage log 的账号倍率快照加权，天然支持当天中途调整倍率。
	AccountCostUSD float64
	// HasAccountCost 区分"上游成本为 0"与"这个平台/版本不提供该口径"。
	// 为 false 时调用方必须回退到上游站点采集，绝不能把成本当成 0。
	HasAccountCost bool
}

type Sub2APIResourceUsage struct {
	CPUUsagePercent    *float64
	MemoryUsagePercent *float64
}

type FallbackPoolUsageEvent struct {
	RequestID       string
	AccountID       string
	AccountName     string
	Model           string
	SourceGroupID   string
	SourceGroupName string
	TargetGroupID   string
	TargetGroupName string
	CreatedAt       time.Time
	ActualCost      float64
}

// UpstreamErrorEvent is a redacted request-error row from the Sub2API ops API.
// Request bodies and credentials are deliberately excluded from this boundary.
type UpstreamErrorEvent struct {
	GroupID    string
	GroupName  string
	StatusCode int
	Message    string
	Model      string
	CreatedAt  time.Time
}
