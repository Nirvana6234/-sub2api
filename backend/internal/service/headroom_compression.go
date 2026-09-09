package service

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// HeadroomBaseURLHeader 是 headroom 用来动态指定真实上游地址的请求头名。
// gateway_forward.go / openai_gateway_forward.go 也用它判断某次出站请求是否
// 走了压缩代理，以便在连接失败时触发熔断。
const HeadroomBaseURLHeader = "x-headroom-base-url"

// headroomTokensSavedHeader 是 headroom 在实际压缩过某次请求后，于上游响应里
// 回传本次节省 token 数的响应头。未压缩（包括未经过 headroom）时该头不存在。
//
// headroom 同批还提供了 x-headroom-tokens-before/-after，理论上可以用来算出更
// 精确的节省比例（saved/before）。但生产实测（2026-09-09）对 OpenAI/Gemini 流量，
// headroom 自己在响应头阶段用的是压缩前的粗估值，经常是 0 或极小的数（真正准确的
// "effective_original_tokens" 要等流式响应完全解析完才能算出来，那时响应头早已
// 发出），导致 before 列基本全是 0，saved/before 算出离谱的天文数字百分比。
// 已改为在 usage_log_repo_dashboard.go 里用 usage_logs 自己已经可靠记录的
// input_tokens + cache_creation_tokens + cache_read_tokens 反推 before，
// 不再依赖这两个头。
const headroomTokensSavedHeader = "x-headroom-tokens-saved"

// parseHeadroomTokensSavedHeader 从上游响应头解析本次节省的 token 数；头缺失、
// 空值或非法数字一律按 0 处理（未压缩），不影响正常计费流程。
func parseHeadroomTokensSavedHeader(h http.Header) int {
	if h == nil {
		return 0
	}
	raw := strings.TrimSpace(h.Get(headroomTokensSavedHeader))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// headroomCircuitOpenUntilNano 是一个简单的进程内熔断器：headroom 转发出现连接
// 级错误（拨号失败/超时，说明代理本身不可用）时打开，在冷却时间内后续请求直接
// 跳过压缩走直连，不会因为 headroom 抖动而拖累整个网关的可用性。0 表示未打开。
var headroomCircuitOpenUntilNano atomic.Int64

const headroomCircuitCooldown = 60 * time.Second

// markHeadroomTransportFailure 由发送出站请求的调用方在确认某次 headroom 转发
// 出现连接级错误后调用。
func markHeadroomTransportFailure() {
	headroomCircuitOpenUntilNano.Store(time.Now().Add(headroomCircuitCooldown).UnixNano())
}

func headroomCircuitOpen() bool {
	openUntil := headroomCircuitOpenUntilNano.Load()
	return openUntil != 0 && time.Now().UnixNano() < openUntil
}

// resolveHeadroomCompressionTarget 判断能否把 targetURL 换成经 headroom 压缩代理
// 转发的版本。headroom 按 x-headroom-base-url 请求头动态路由：请求路径与 query
// 原样保留，只把 host 换成该头给出的地址，因此只需把 targetURL 的 origin 部分
// 换成 headroom 地址，并把原 origin 放进返回头值里。
//
// ok=false 时调用方必须保持 targetURL 不变、直连原上游：
//   - 用户未开启压缩开关，或 headroom 地址未配置；
//   - targetURL 的 host 解析为私有/回环地址（自建中转）——headroom 自身的 SSRF
//     防护会拒绝该覆盖并静默回退到它配置的默认上游（真实 OpenAI/Anthropic），
//     而不是报错；用错地址比不压缩更糟，宁可跳过。
//
// 调用方还需自行排除协议层面会被 headroom 内部逻辑抢先接管路由的账号类型
// （例如 OpenAI 的 ChatGPT OAuth/Codex 内部协议），本函数不感知账号类型。
func resolveHeadroomCompressionTarget(ctx context.Context, settingService *SettingService, apiKey *APIKey, targetURL string) (compressedURL string, realOrigin string, ok bool) {
	if settingService == nil || apiKey == nil || apiKey.User == nil || !apiKey.User.HeadroomCompressionEnabled {
		return "", "", false
	}
	if headroomCircuitOpen() {
		return "", "", false
	}
	headroomBaseURL, err := settingService.GetHeadroomBaseURL(ctx)
	if err != nil || headroomBaseURL == "" {
		return "", "", false
	}
	parsed, err := url.Parse(targetURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", false
	}
	// 复用 channel_monitor_ssrf.go 里现成的私网/回环判断；解析失败时保守跳过压缩
	// （宁可不压缩，也不要在无法确认安全性时把地址交给 headroom 覆盖）。
	unsafe, err := isPrivateOrLoopbackHost(ctx, parsed.Hostname())
	if err != nil || unsafe {
		return "", "", false
	}
	origin := parsed.Scheme + "://" + parsed.Host
	suffix := strings.TrimPrefix(targetURL, origin)
	compressedURL = strings.TrimRight(headroomBaseURL, "/") + suffix
	return compressedURL, origin, true
}
