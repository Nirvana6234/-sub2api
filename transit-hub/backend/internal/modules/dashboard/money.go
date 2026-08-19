package dashboard

// Currency 表示货币类型。
type Currency string

const (
	CurrencyUSD Currency = "USD"
	CurrencyCNY Currency = "CNY"
)

// Money 是带币种标记的金额，用于 API 响应以避免混币种运算。
type Money struct {
	Amount   float64  `json:"amount"`
	Currency Currency `json:"currency"`
}

// FromFloat 构造一个 Money 实例。
func FromFloat(currency Currency, amount float64) Money {
	return Money{Amount: amount, Currency: currency}
}

// CostStatus 表示成本采集的可靠性状态。
type CostStatus string

const (
	// These legacy statuses remain defined for database compatibility. Trend
	// responses filter them out because they may contain locally rebuilt cost.
	CostStatusComplete       CostStatus = "complete"
	CostStatusPartial        CostStatus = "partial"
	CostStatusInvalidRate    CostStatus = "invalid_rate"
	CostStatusMissing        CostStatus = "missing"
	CostStatusCachedFallback CostStatus = "cached_fallback"
	// CostStatusAdminAccounted 表示成本取自 admin 站点自身的账号成本口径
	// （sub2api total_account_cost），与营收同源同日界，按每条 usage log 的账号
	// 倍率快照加权。这是最可信的一档：不依赖上游站点采集，也不依赖 rechargeRate。
	CostStatusAdminAccounted CostStatus = "admin_accounted"
)
