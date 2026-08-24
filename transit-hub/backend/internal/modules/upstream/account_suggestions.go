package upstream

import (
	"net/url"
	"strings"
)

// AccountSuggestion 是"这个上游站点可能对应哪个 Sub2API 账号"的候选项，
// 供前端在绑定调价数据源时做预填。它只是建议——同一域名下常年挂着多个成本
// 迥异的账号（生产上 tntapi.com 就有 3 个，探测倍率分别是 0.16 / 0.079 / 无），
// 光靠域名无法判断该用哪个，最终必须由人确认。
type AccountSuggestion struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	BaseURL            string   `json:"baseUrl,omitempty"`
	CostRateMultiplier *float64 `json:"costRateMultiplier"`
	CostRateSource     string   `json:"costRateSource"`
}

// SuggestAccountsByBaseURL 返回域名与 siteBaseURL 相同的账号候选，保持入参顺序。
// siteBaseURL 无法解析出主机名时返回 nil，绝不退化成"返回全部账号"——
// 那会让前端把一个毫不相干的账号预填进去，比不给建议更危险。
func SuggestAccountsByBaseURL(siteBaseURL string, accounts []AdminGroupAccountInfo) []AccountSuggestion {
	target := normalizeUpstreamHost(siteBaseURL)
	if target == "" {
		return nil
	}
	suggestions := make([]AccountSuggestion, 0, len(accounts))
	for _, account := range accounts {
		if normalizeUpstreamHost(account.BaseURL) != target {
			continue
		}
		suggestions = append(suggestions, AccountSuggestion{
			ID:                 account.ID,
			Name:               account.Name,
			BaseURL:            account.BaseURL,
			CostRateMultiplier: account.CostRateMultiplier,
			CostRateSource:     account.CostRateSource,
		})
	}
	if len(suggestions) == 0 {
		return nil
	}
	return suggestions
}

// normalizeUpstreamHost 把上游地址归一化成可比较的主机名：
// 去协议、去端口、去路径（sub2api 账号的 base_url 常带 /v1）、去 www. 前缀、转小写。
// 这样 https://www.mcgrox.top 与 https://mcgrox.top/v1 会判为同一站点。
func normalizeUpstreamHost(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		// 允许直接传 "tntapi.com" 或 "tntapi.com/v1" 这类裸域名。
		value = "//" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return ""
	}
	host = strings.TrimSuffix(host, ".")
	return strings.TrimPrefix(host, "www.")
}
