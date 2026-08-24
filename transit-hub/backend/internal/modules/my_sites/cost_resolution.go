package my_sites

import (
	"strings"

	"transithub/backend/internal/modules/upstream"
)

// realConnectionAccountIndex indexes active real connections for legacy
// mappings whose explicit cost-account field is empty.
func realConnectionAccountIndex(connections []RealConnection) map[string]string {
	index := make(map[string]string)
	for _, connection := range connections {
		status := strings.TrimSpace(strings.ToLower(connection.Status))
		if status != "" && status != ConnectionStatusActive {
			continue
		}
		accountID := strings.TrimSpace(connection.AdminAccountID)
		siteID := strings.TrimSpace(connection.UpstreamSiteID)
		groupName := strings.TrimSpace(connection.UpstreamGroupName)
		if accountID == "" || siteID == "" || groupName == "" {
			continue
		}
		index[UpstreamGroupKey(siteID, groupName)] = accountID
	}
	return index
}

func fallbackRealConnectionAccount(index map[string]string, siteID, groupName string) *string {
	if accountID := strings.TrimSpace(index[UpstreamGroupKey(siteID, groupName)]); accountID != "" {
		return &accountID
	}
	return nil
}

// 调价数据源的成本来源标记。与 Sub2API 的 cost_rate_source 对外取值一一对应，
// 多出来的 none 同时覆盖"未绑定账号"和"账号未声明成本"两种情况——对毛利计算
// 而言两者是同一件事：这条数据源的成本未知。
const (
	CostSourceManual = "manual"
	CostSourceProbe  = "probe"
	CostSourceColumn = "column"
	CostSourceNone   = "none"
)

// normalizeSub2APIAccountID 归一化绑定的账号 ID：去空白，空串一律收敛成 nil，
// 让"没填"和"填了空字符串"在存储层是同一种状态。
func normalizeSub2APIAccountID(raw *string) *string {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// resolveTargetCostMultiplier 给出一个调价数据源的成本倍率及其来源。
//
// 只认显式绑定的 Sub2API 账号，且倍率一律采用 Sub2API 已解析好的
// CostRateMultiplier（内部按 手工值 > 新鲜探测值 > 列值 取值）。这里刻意不重新
// 解析一遍 extra：优先级判断只应存在一处，两边各写一套迟早分叉。
//
// 拿不到成本时返回 (nil, CostSourceNone)，调用方必须把该数据源排除在毛利计算之外。
// 【绝不回退】上游标称倍率或 1.0：上游标称的是它的售价倍率，不是我们的进货成本，
// 生产上 mcgrox.top 标称 0.8 而实际手工成本 0.04，拿标称值顶替会把毛利算成 -1130%。
func resolveTargetCostMultiplier(
	target UpstreamGroupRef,
	accounts []upstream.AdminGroupAccountInfo,
) (*float64, string) {
	accountID := normalizeSub2APIAccountID(target.Sub2APIAccountID)
	if accountID == nil {
		return nil, CostSourceNone
	}
	for _, account := range accounts {
		if strings.TrimSpace(account.ID) != *accountID {
			continue
		}
		if account.CostRateMultiplier == nil {
			// 账号存在但无人声明成本（含探测失败只剩建表默认值的情况）。
			return nil, CostSourceNone
		}
		switch strings.ToLower(strings.TrimSpace(account.CostRateSource)) {
		case CostSourceManual, CostSourceProbe, CostSourceColumn:
			value := *account.CostRateMultiplier
			return &value, strings.ToLower(strings.TrimSpace(account.CostRateSource))
		default:
			// 来源说不清就不采信这个数字，理由同上：宁可少算，也不虚报。
			return nil, CostSourceNone
		}
	}
	// 绑定的账号已不在当前账号集合里（被删、改分组或本次没拉到）。
	return nil, CostSourceNone
}
