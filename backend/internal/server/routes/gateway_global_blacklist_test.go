package routes

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// 全局黑名单必须挂在每一条网关路由上，且两段顺序不能颠倒：
//
//	blacklistIP      在 apiKeyAuth 之前 —— 被封 IP 连"拿无效 Key 试探网关"都不该做到；
//	                 挂到认证之后等于先白送它一次枚举机会。
//	blacklistAccount 在 apiKeyAuth 之后 —— 它要从上下文取认证后的 apiKey 才拿得到
//	                 userID，提前挂载读到的是空值，账号黑名单直接失效。
//
// 这两个中间件曾经定义了却没有被任何路由挂载：管理端能增删查、数据也确实写进了
// settings.global_blacklist_entries（生产上存着 2 条 enabled 的），但请求从不经过
// 拦截，表现为"配了黑名单却照样能用"。漏挂不会有任何报错，只能靠这个测试兜住。
func TestGatewayRoutesGlobalBlacklistMountedOnEveryGatewayRoute(t *testing.T) {
	routeSource, err := os.ReadFile("gateway.go")
	require.NoError(t, err)
	source := string(routeSource)

	require.Contains(t, source, "blacklistIP := middleware.GlobalBlacklistIP(settingService, cfg)",
		"IP 段中间件必须被构造，否则下面的挂载断言只是在比对字符串")
	require.Contains(t, source, "blacklistAccount := middleware.GlobalBlacklistAccount(settingService, cfg)",
		"账号段中间件必须被构造")

	// 一条 Use 调用写完的链路，直接断言完整顺序。
	inlineChains := []struct {
		name  string
		chain string
	}{
		{
			name:  "rootRoute helper",
			chain: `r.Handle(method, path, limit, clientRequestID, opsErrorLogger, endpointNorm, blacklistIP, gin.HandlerFunc(apiKeyAuth), blacklistAccount, autoGroupModelRouting`,
		},
		{
			name:  "codexDirect",
			chain: `codexDirect.Use(bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, blacklistIP, gin.HandlerFunc(apiKeyAuth), blacklistAccount, autoGroupModelRouting`,
		},
		{
			name:  "antigravity models alias",
			chain: `r.GET("/antigravity/models", blacklistIP, gin.HandlerFunc(apiKeyAuth), blacklistAccount, requireGroupAnthropic`,
		},
	}
	for _, c := range inlineChains {
		require.Contains(t, source, c.chain,
			"%s 必须按 blacklistIP → apiKeyAuth → blacklistAccount 的顺序挂载", c.name)
	}

	// 逐行 Use 的分组链路：用正则确认三者在源码里的先后次序。
	groupChains := []struct {
		group string
		auth  string
	}{
		{group: "gateway", auth: "gin.HandlerFunc(apiKeyAuth)"},
		{group: "gemini", auth: "middleware.APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg)"},
		{group: "antigravityV1", auth: "gin.HandlerFunc(apiKeyAuth)"},
		{group: "antigravityV1Beta", auth: "middleware.APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg)"},
	}
	for _, c := range groupChains {
		re := regexp.MustCompile(
			regexp.QuoteMeta(c.group+".Use(blacklistIP)") +
				`[\s\S]{0,200}?` + regexp.QuoteMeta(c.group+".Use("+c.auth+")") +
				`[\s\S]{0,200}?` + regexp.QuoteMeta(c.group+".Use(blacklistAccount)"))
		require.Regexp(t, re, source,
			"%s 链必须是 blacklistIP → auth → blacklistAccount", c.group)
	}

	// 两段数量必须相等：漏挂其中一段会让另一半静默失效。
	ipCount := len(regexp.MustCompile(`\bblacklistIP\b`).FindAllString(source, -1))
	acctCount := len(regexp.MustCompile(`\bblacklistAccount\b`).FindAllString(source, -1))
	require.Equal(t, ipCount, acctCount,
		"IP 段与账号段挂载数必须一致，否则有路由只拦了一半")
	require.GreaterOrEqual(t, ipCount, 8, "构造 1 处 + 至少 7 条网关路由")
}
