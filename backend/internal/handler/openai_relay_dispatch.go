package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OpenAIRelayDispatcher 是主从分流从节点上的接缝（开发计划 WP9，设计 3.1）。处理函数里由主节点负责的步骤
// ——续链归属、安全审计、用户并发槽、计费资格、cyber 会话屏蔽、渠道映射、计价上下文、选号与准入——
// 在从节点上合成一次远程选号；用量写本地扣费队列，不调 RecordUsage；每次尝试结束发释放。
// 处理函数其余代码（请求校验与规范化、转发、失败换号的分类）两边是同一份。
// 处理函数的 relay 为 nil 时走单机逻辑。
type OpenAIRelayDispatcher interface {
	// Select 为这次请求的一次尝试远程选号。选中时已把选号 ID 放进 c.Request 的 ctx（上游错误决策、
	// 账号事件按它关联），并预扣了本地额度。
	Select(c *gin.Context, req OpenAIRelaySelectRequest) OpenAIRelaySelectResult
	// AttemptDone 这次尝试的转发已结束（对应单机放账号槽的时机）。释放在下一次选号前或请求结束时发出，
	// 带上这次转发产生的 response id。
	AttemptDone(c *gin.Context, attempt *OpenAIRelayAttempt)
	// SubmitUsage 把这次尝试的转发结果写入本地扣费队列（代替 RecordUsage）。
	SubmitUsage(c *gin.Context, attempt *OpenAIRelayAttempt, facts OpenAIUsageFacts, result *service.OpenAIForwardResult)
	// RequestDone 在处理函数返回时调用：最后一次尝试的释放带"请求结束"，主节点放掉用户并发槽。
	RequestDone(c *gin.Context)
	// HandOff 把请求原样交给主节点转发（主节点回"暂不支持"时，只在还没写出任何响应时调用）。
	HandOff(c *gin.Context)
}

// OpenAIRelaySelectRequest 是一次远程选号的输入：处理函数在单机上交给主节点那几步的原始事实。
type OpenAIRelaySelectRequest struct {
	Chat bool
	// Messages：OpenAI 分组的 /v1/messages 入口。
	Messages           bool
	APIKey             *service.APIKey
	Model              string
	Stream             bool
	SessionHash        string
	PreviousResponseID string
	ImageIntent        bool
	LegacyCompact      bool
	NativeCompactionV2 bool
	// Excluded 是本请求已失败、要排除的账号。
	Excluded map[int64]struct{}
	// Body 是算会话哈希用的请求体（cyber 会话屏蔽的查询键从它算）。
	Body []byte
}

// OpenAIRelaySelectResult 是一次远程选号的结果：Attempt 与 Rejection 二选一。
type OpenAIRelaySelectResult struct {
	Attempt   *OpenAIRelayAttempt
	Rejection *OpenAIRelayRejection
}

// OpenAIRelayAttempt 是选中的一次尝试。
type OpenAIRelayAttempt struct {
	Account *service.Account
	// SessionHash 是主节点实际使用的会话哈希（池模式账号可能改写），后续尝试沿用。
	SessionHash string
	// ChannelMapping 是主节点定下的渠道映射，ForwardModel 是映射后发给上游的模型。
	ChannelMapping service.ChannelMappingResult
	ForwardModel   string
	// MaxAccountSwitches 是主节点给的换号上限。
	MaxAccountSwitches int
	// State 归分发实现所有（选号 ID、凭证、预扣）。
	State any
}

// OpenAIRelayRejectionKind 是远程选号被拒时响应的写法。
type OpenAIRelayRejectionKind int

const (
	// OpenAIRelayRejectGateway：按网关错误写（流式已开始时写流内错误）；CyberBlockKey 非空时按 cyber 屏蔽的写法。
	OpenAIRelayRejectGateway OpenAIRelayRejectionKind = iota
	// OpenAIRelayRejectRaw：原样写出主节点按中间件写法生成的响应。
	OpenAIRelayRejectRaw
	// OpenAIRelayRejectFailoverExhausted：换号用完，按最近一次上游错误写。
	OpenAIRelayRejectFailoverExhausted
	// OpenAIRelayRejectUnsupported：交给主节点转发。
	OpenAIRelayRejectUnsupported
	// OpenAIRelayRejectUnavailable：主节点不可达。不重试、不换号（设计 3.1）：第一次尝试按 503 写，
	// 之后按最近一次上游错误写。
	OpenAIRelayRejectUnavailable
)

// OpenAIRelayRejection 是远程选号的拒绝。
type OpenAIRelayRejection struct {
	Kind          OpenAIRelayRejectionKind
	Gateway       OpenAIGatewayRejection
	CyberBlockKey string
	Raw           *middleware2.CapturedRejection
	// ContinuationUnsupported：这次选号跳过了不支持续链的账号，最后的错误是"续链不支持"。
	ContinuationUnsupported bool
}

// OpenAIUsageFacts 是入账输入里由转发节点得出的字段。单机直接放进 RecordUsage 的输入，从节点写进扣费记录，
// 两边用同一个函数收集（开发计划 2.2）。
type OpenAIUsageFacts struct {
	InboundEndpoint    string
	UpstreamEndpoint   string
	UserAgent          string
	IPAddress          string
	RequestPayloadHash string
	SessionID          string
	CyberBlocked       bool
	NativeCompactionV2 bool
}

// SetRelayDispatcher 让处理函数在主从分流的从节点上运行（WP9 装配时调用）。
func (h *OpenAIGatewayHandler) SetRelayDispatcher(d OpenAIRelayDispatcher) { h.relay = d }

// openAIHTTPContinuationUnsupportedError 是 HTTP 续链落到不支持的账号时的最后错误（选号跳过 OAuth 账号时记下）。
func openAIHTTPContinuationUnsupportedError() *service.UpstreamFailoverError {
	return &service.UpstreamFailoverError{
		StatusCode:       http.StatusBadRequest,
		Stage:            service.GatewayFailureStageInference,
		Scope:            service.GatewayFailureScopeRequest,
		Reason:           service.OpenAIHTTPContinuationUnsupportedReason,
		ClientStatusCode: http.StatusBadRequest,
		ClientMessage:    "previous_response_id requires an OpenAI API-key account for HTTP requests",
	}
}

// writeOpenAIRelayRejection 按远程选号拒绝的写法写出响应，与单机在同样情况下写的一致。
// format 同时表示入口：Chat 与 Responses 在换号用完、没有上游错误时写法不同；Messages（Anthropic）的换号用完、
// 主节点不可达按 Anthropic 格式写。
func (h *OpenAIGatewayHandler) writeOpenAIRelayRejection(c *gin.Context, r *OpenAIRelayRejection, apiKey *service.APIKey, model string, format cyberSessionBlockFormat, lastFailoverErr *service.UpstreamFailoverError, streamStarted bool, reqLog *zap.Logger) {
	switch r.Kind {
	case OpenAIRelayRejectRaw:
		middleware2.WriteCapturedRejection(c, r.Raw)
	case OpenAIRelayRejectFailoverExhausted:
		switch {
		case format == cyberBlockFormatAnthropic && lastFailoverErr != nil:
			h.handleAnthropicFailoverExhausted(c, lastFailoverErr, streamStarted)
		case format == cyberBlockFormatAnthropic:
			h.anthropicStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
		case r.ContinuationUnsupported:
			h.handleFailoverExhausted(c, openAIHTTPContinuationUnsupportedError(), streamStarted)
		case lastFailoverErr != nil:
			h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
		case format == cyberBlockFormatChat:
			h.handleStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
		default:
			h.handleFailoverExhaustedSimple(c, http.StatusBadGateway, streamStarted)
		}
	case OpenAIRelayRejectUnsupported:
		if streamStarted || c.Writer.Written() || service.StopOpenAICompactSSEKeepaliveCommitted(c) {
			reqLog.Error("openai.relay_handoff_after_response_started")
			if format == cyberBlockFormatAnthropic {
				h.anthropicStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
				return
			}
			h.handleStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
			return
		}
		h.relay.HandOff(c)
	case OpenAIRelayRejectUnavailable:
		reqLog.Warn("openai.relay_master_unavailable")
		switch {
		case format == cyberBlockFormatAnthropic && lastFailoverErr != nil:
			h.handleAnthropicFailoverExhausted(c, lastFailoverErr, streamStarted)
		case format == cyberBlockFormatAnthropic:
			h.anthropicStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", streamStarted)
		case lastFailoverErr != nil:
			h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
		default:
			h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", streamStarted)
		}
	default:
		if r.CyberBlockKey != "" {
			h.writeCyberSessionBlocked(c, apiKey, model, r.CyberBlockKey, format)
			return
		}
		h.writeOpenAIGatewayRejection(c, r.Gateway, streamStarted)
	}
}

// relayAttemptOutcome 把远程选中的尝试转成处理函数选号循环用的结果。
func (h *OpenAIGatewayHandler) relayAttemptOutcome(c *gin.Context, attempt *OpenAIRelayAttempt) OpenAISelectOutcome {
	return OpenAISelectOutcome{
		Kind: OpenAISelected, Account: attempt.Account, SessionHash: attempt.SessionHash, Ctx: c.Request.Context(),
		Release: func() { h.relay.AttemptDone(c, attempt) },
	}
}

// collectOpenAIUsageFacts 收集入账输入里由转发节点得出的字段（单机与从节点共用）。
func collectOpenAIUsageFacts(c *gin.Context, account *service.Account, res *service.OpenAIForwardResult, payloadHash func() string, nativeV2 bool) OpenAIUsageFacts {
	return OpenAIUsageFacts{
		UserAgent:          c.GetHeader("User-Agent"),
		IPAddress:          ip.GetClientIP(c),
		RequestPayloadHash: payloadHash(),
		InboundEndpoint:    GetInboundEndpoint(c),
		UpstreamEndpoint:   resolveOpenAIUpstreamEndpoint(c, account, res),
		SessionID:          service.ExtractClientSessionID(c),
		CyberBlocked:       service.GetOpsCyberPolicy(c) != nil,
		NativeCompactionV2: nativeV2,
	}
}
