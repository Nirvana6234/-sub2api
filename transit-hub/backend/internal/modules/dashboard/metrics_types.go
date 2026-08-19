package dashboard

import "time"

// MetricsResponse 是 GET /api/dashboard/metrics 返回的实时指标数据。
// 营收和站点余额分 USD/CNY 双币种展示，成本统一为 CNY。
type MetricsResponse struct {
	TodayProfitUSD     Money      `json:"todayProfitUSD"`     // 今日营收（USD 原值）
	TodayProfitCNY     Money      `json:"todayProfitCNY"`     // 今日营收（CNY，已乘汇率）
	SiteBalanceUSD     Money      `json:"siteBalanceUSD"`     // 站点用户总余额（USD 原值）
	SiteBalanceCNY     Money      `json:"siteBalanceCNY"`     // 站点用户总余额（CNY，已乘汇率）
	TodayPurchaseCNY   Money      `json:"todayPurchaseCNY"`   // 今日成本（CNY）
	NetProfitCNY       Money      `json:"netProfitCNY"`       // 今日净利润（CNY）= todayProfitCNY - todayPurchaseCNY
	UpstreamBalanceCNY Money      `json:"upstreamBalanceCNY"` // 上游总余额（CNY）
	CostStatus         CostStatus `json:"costStatus"`         // 成本来源状态：只有 admin_accounted 可用于核算
	GroupCount         int        `json:"groupCount"`         // 管理员站点分组总数，省去前端单独请求
	// USDToCNYRate 是本次计算实际使用的汇率，随快照一起持久化。
	// 目的：历史行用「当天写入时的汇率」换算，而不是用「读取时的当前汇率」重算。
	// 否则改一次汇率会追溯改写整条历史曲线。
	USDToCNYRate float64 `json:"usdToCnyRate"`
}

// TrendResponse 是 GET /api/dashboard/trends 返回的历史趋势数据。
type TrendResponse struct {
	Points []TrendPoint `json:"points"`
}

// TrendPoint 代表一天的指标快照，用于趋势图渲染。
type TrendPoint struct {
	Date               string     `json:"date"` // 日期，格式 "2006-01-02"
	TodayProfitUSD     Money      `json:"todayProfitUSD"`
	TodayProfitCNY     Money      `json:"todayProfitCNY"`
	SiteBalanceUSD     Money      `json:"siteBalanceUSD"`
	SiteBalanceCNY     Money      `json:"siteBalanceCNY"`
	TodayPurchaseCNY   Money      `json:"todayPurchaseCNY"`
	NetProfitCNY       Money      `json:"netProfitCNY"`
	UpstreamBalanceCNY Money      `json:"upstreamBalanceCNY"`
	CostStatus         CostStatus `json:"costStatus"`
}

// DailySnapshot 是 dashboard_daily_stats 表的行结构。
// 每天至多一行（user_id + admin_account_id + date 唯一），
// 通过 LiveMetrics 调用时的 upsert 和午夜调度器持续更新。
type DailySnapshot struct {
	ID                 string
	UserID             string
	AdminAccountID     string
	Date               time.Time
	TodayProfitUSD     float64
	SiteBalanceUSD     float64
	TodayPurchaseCNY   float64
	UpstreamBalanceCNY float64
	CostStatus         CostStatus
	// USDToCNYRate 是写入这一行当天实际使用的汇率，随行持久化。
	// 读取侧（Trends）必须用这个值换算，不能用当前汇率重算，
	// 否则改一次汇率会追溯改写整条历史曲线。
	USDToCNYRate float64
	CreatedAt    time.Time
	FinalizedAt  *time.Time
	IsFinalized  bool
}

// EffectiveRate 返回该快照行可用于换算的汇率。
// 旧行（迁移前写入、未记录汇率）回退到默认值，避免乘成 0。
func (s DailySnapshot) EffectiveRate() float64 {
	if s.USDToCNYRate > 0 {
		return s.USDToCNYRate
	}
	return DefaultUSDToCNYRate
}

// AdminGroupsResponse 是 GET /api/dashboard/groups 返回的管理员站点分组数据。
type AdminGroupsResponse struct {
	Count  int              `json:"count"`
	Groups []AdminGroupItem `json:"groups"`
}

// AdminGroupItem 是管理员站点中单个分组的展示数据。
type AdminGroupItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	Multiplier string `json:"multiplier"`
}

// GroupUsageTodayResponse 是 GET /api/dashboard/group-usage-today 返回的分组今日用量明细。
type GroupUsageTodayResponse struct {
	Date   string                `json:"date"`
	Total  float64               `json:"total"`
	Groups []GroupUsageTodayItem `json:"groups"`
}

// GroupUsageTodayItem 是单个分组的今日使用额度。
type GroupUsageTodayItem struct {
	GroupName   string  `json:"groupName"`
	TodayAmount float64 `json:"todayAmount"`
}

// UpstreamKeyUsageTodayResponse 是 GET /api/dashboard/upstream-key-usage-today 返回的
// 「今日成本」下钻明细：当前工作区所有上游站点中，今天有消费的 key 列表。
type UpstreamKeyUsageTodayResponse struct {
	Date        string                      `json:"date"`
	Total       float64                     `json:"total"`
	Keys        []UpstreamKeyUsageTodayItem `json:"keys"`
	FailedSites int                         `json:"failedSites,omitempty"`
	TotalSites  int                         `json:"totalSites,omitempty"`
}

// UpstreamKeyUsageTodayItem 是单个 key 的今日消费明细。
// TodayAmount 已乘以所属站点的 rechargeRate，口径与仪表盘「今日成本」卡片一致；RawAmount 为上游平台原始金额。
type UpstreamKeyUsageTodayItem struct {
	SiteID       string  `json:"siteId"`
	SiteName     string  `json:"siteName"`
	Platform     string  `json:"platform"`
	KeyID        string  `json:"keyId"`
	KeyName      string  `json:"keyName"`
	GroupName    string  `json:"groupName"`
	TodayAmount  float64 `json:"todayAmount"`
	RawAmount    float64 `json:"rawAmount"`
	RechargeRate float64 `json:"rechargeRate"`
}

// UpstreamBalanceBreakdownResponse 是 GET /api/dashboard/upstream-balance-breakdown 返回的
// 「上游总余额」下钻明细：当前工作区所有上游站点的缓存余额列表。
type UpstreamBalanceBreakdownResponse struct {
	Total float64                        `json:"total"`
	Sites []UpstreamBalanceBreakdownItem `json:"sites"`
}

// UpstreamBalanceBreakdownItem 是单个上游站点的余额明细。
// Balance/RawBalance 为 null 表示该站点余额尚未同步或未配置 rechargeRate。
type UpstreamBalanceBreakdownItem struct {
	SiteID       string   `json:"siteId"`
	SiteName     string   `json:"siteName"`
	Platform     string   `json:"platform"`
	Balance      *float64 `json:"balance"`
	RawBalance   *float64 `json:"rawBalance"`
	RechargeRate float64  `json:"rechargeRate"`
	LastSyncedAt *int64   `json:"lastSyncedAt"`
	Status       string   `json:"status"`
}

// BalanceFilterConfig 是用户自定义的站点用户余额筛选条件，持久化在 dashboard_balance_filter 表中。
// 每个 (user_id, admin_account_id) 最多一行配置，控制 LiveMetrics 计算 siteBalance 时的过滤行为。
type BalanceFilterConfig struct {
	UserID          string    `json:"-"`
	AdminAccountID  string    `json:"-"`
	ExcludeAdmin    bool      `json:"excludeAdmin"`    // 是否排除 admin 角色用户（默认 true）
	ExcludeBalances []float64 `json:"excludeBalances"` // 需要排除的精确余额值列表
	// USDToCNYRate 是管理员站点营收/余额从 USD 折算到 CNY 的倍率。
	// 上游成本侧用的是每个站点自己的 rechargeRate，两者语义相同但作用对象不同：
	// 这里换算「我们卖出去的钱」，rechargeRate 换算「我们买进来的钱」。
	USDToCNYRate float64 `json:"usdToCnyRate"`
}

// DefaultUSDToCNYRate 是未配置时使用的兜底汇率。
const DefaultUSDToCNYRate = 7.0

// EffectiveUSDToCNYRate 返回可用于计算的汇率。
// 非正值（未配置、脏数据）一律回退到默认值，避免把营收乘成 0。
func (c BalanceFilterConfig) EffectiveUSDToCNYRate() float64 {
	if c.USDToCNYRate > 0 {
		return c.USDToCNYRate
	}
	return DefaultUSDToCNYRate
}
