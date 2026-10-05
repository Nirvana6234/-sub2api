package handler

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OpenAIRelayDispatcher 是主从分流从节点上的接缝（开发计划 WP9，设计 3.1）。处理函数里由主节点负责的步骤
// ——续链归属、用户并发槽、计费资格、cyber 会话屏蔽、渠道映射、计价上下文、选号与准入——
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
	// RecordCyberPolicy 上游判定 cyber 策略后调用（设计 3.4）：会话屏蔽标记交给主节点（授权），最多等 500ms
	// （单机同步写屏蔽标记的上限）；usage 非 nil 时（转发返回错误）把用量行写进本地扣费队列。风控记录由处理函数
	// 用本机的审核服务写（CyberPolicyRecorder）。
	RecordCyberPolicy(c *gin.Context, attempt *OpenAIRelayAttempt, hit CyberPolicyHit, usage *OpenAIRelayCyberUsage)
	// RequestDone 在处理函数返回时调用：最后一次尝试的释放带"请求结束"，主节点放掉用户并发槽。
	RequestDone(c *gin.Context)
	// HandOff 把请求原样交给主节点转发（主节点回"暂不支持"时，只在还没写出任何响应时调用）。
	HandOff(c *gin.Context)

	// ---- Responses WebSocket（开发计划 WP10-3）：连接选号用 Select（WS 为 true），每一轮在主节点准入 ----

	// WSIngressLeaseCache 返回经主节点申请的每 Key 连接数租约存储（带 Key 原文）。
	WSIngressLeaseCache(c *gin.Context, apiKey *service.APIKey) service.OpenAIWSIngressLeaseCache
	// BeginTurn 一轮开始（本地 BeforeTurn 里的复核、定价、占槽）：返回这一轮的尝试（用量、cyber 按它上报），
	// 或者关闭连接的错误（service.OpenAIWSClientCloseError）。
	BeginTurn(c *gin.Context, conn *OpenAIRelayAttempt, turn int, model string) (*OpenAIRelayAttempt, error)
	// EndTurn 一轮结束（本地 AfterTurn 里的放槽）：放掉这一轮的槽，带上这一轮产生的 response id。
	EndTurn(c *gin.Context, conn, turn *OpenAIRelayAttempt)
	// TurnMapping 一轮的渠道映射（这一轮换了模型时）。
	TurnMapping(c *gin.Context, conn *OpenAIRelayAttempt, model string) (service.ChannelMappingResult, error)

	// SwitchAutoGroup 自动分组 Key 换到下一个候选分组（本地 tryOpenAIAutoGroupFailover 里选组那一步，经主节点）。
	// failedGroupIDs 已含当前分组。没有可换的返回 false；出错按没换处理（不重发：换组会改主节点的选组状态）。
	SwitchAutoGroup(c *gin.Context, apiKey *service.APIKey, model string, failedGroupIDs map[int64]struct{}) (OpenAIRelayAutoGroupSwitch, bool)

	// ---- Anthropic Messages（GatewayHandler.Messages，选号用 Select，Anthropic 为 true）----

	// SubmitAnthropicUsage 把这次尝试的转发结果写入本地扣费队列（代替 GatewayService.RecordUsage）。
	SubmitAnthropicUsage(c *gin.Context, attempt *OpenAIRelayAttempt, facts OpenAIUsageFacts, result *service.ForwardResult, forceCacheBilling bool)
	// SwitchFallbackGroup Antigravity 回 prompt 过长时换到分组配置的兜底分组（经主节点解析、做计费资格复查）：
	// 返回兜底分组的 Key；没有可换的 ok 为 false；计费复查不过时返回的拒绝非空（照它写响应）。之后的选号带兜底分组。
	SwitchFallbackGroup(c *gin.Context, apiKey *service.APIKey) (OpenAIRelayFallbackSwitch, bool)
	// ForwardSucceeded 这次尝试转发成功（本地这时刷新粘性会话绑定；主节点在释放时按同一条件刷新）。
	ForwardSucceeded(c *gin.Context, attempt *OpenAIRelayAttempt)
}

// OpenAIRelayFallbackSwitch 是主节点定下的兜底分组切换。
type OpenAIRelayFallbackSwitch struct {
	APIKey           *service.APIKey
	BillingRejection *OpenAIGatewayRejection
}

// OpenAIRelayAutoGroupSwitch 是主节点定下的换组：换到的分组的 Key 快照和订阅。
type OpenAIRelayAutoGroupSwitch struct {
	APIKey       *service.APIKey
	Subscription *service.UserSubscription
	// BillingRejection 非 nil：换到的分组计费资格复查不过（主节点顺带查的；本地在"换号用完后换组"时复查）。
	BillingRejection *OpenAIGatewayRejection
}

// OpenAIRelaySelectRequest 是一次远程选号的输入：处理函数在单机上交给主节点那几步的原始事实。
type OpenAIRelaySelectRequest struct {
	Chat bool
	// Messages：OpenAI 分组的 /v1/messages 入口。
	Messages bool
	// WS：Responses WebSocket 的连接选号（PreviousResponseCanMove 只在这里用）。
	WS                      bool
	PreviousResponseCanMove bool
	APIKey                  *service.APIKey
	Model                   string
	Stream                  bool
	SessionHash             string
	PreviousResponseID      string
	ImageIntent             bool
	LegacyCompact           bool
	NativeCompactionV2      bool
	// Excluded 是本请求已失败、要排除的账号。
	Excluded map[int64]struct{}
	// Body 是算会话哈希用的请求体（cyber 会话屏蔽的查询键从它算）。
	Body []byte

	// Anthropic：Anthropic 分组的 /v1/messages（GatewayHandler.Messages）。SessionHash 是会话键；
	// MetadataUserID 是请求体里的 metadata.user_id；InterceptType 是按请求体算好的预热拦截类型。
	Anthropic      bool
	MetadataUserID string
	InterceptType  InterceptType
	// CountTokens：Anthropic 的 /v1/messages/count_tokens（不占槽、不计费、没有凭证）。
	CountTokens bool

	// Gemini：Gemini 原生入口（GeminiV1BetaModels）。SessionHash 是 CLI / 通用会话哈希（不带 "gemini:" 前缀），
	// GeminiDigestChain 是请求体的内容摘要链；会话键、内容摘要会话匹配都在主节点。
	Gemini            bool
	GeminiDigestChain string
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
	// StickyBoundAccountID：Anthropic Messages 请求开始时粘性会话绑定的账号（0 没有）。
	StickyBoundAccountID int64
	// SingleAccountRetry：分组里只有一个 Antigravity 账号（请求开始时主节点查好）。
	SingleAccountRetry bool
	// ChannelMapping 是主节点定下的渠道映射，ForwardModel 是映射后发给上游的模型。
	ChannelMapping service.ChannelMappingResult
	ForwardModel   string
	// MaxAccountSwitches 是主节点给的换号上限。
	MaxAccountSwitches int
	// StickyPreviousHit：WebSocket 连接选号时 previous_response_id 命中了粘连账号。
	StickyPreviousHit bool
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
	// OpenAIRelayRejectWSClose：关闭 WebSocket 连接（WSCloseStatus、WSCloseReason；CyberBlockKey 非空时先写 cyber 错误帧）。
	OpenAIRelayRejectWSClose
	// OpenAIRelayRejectIntercepted：Anthropic Messages 选到的账号开了预热拦截，写模拟响应（InterceptType）。
	OpenAIRelayRejectIntercepted
	// OpenAIRelayRejectProfitVetoed：Anthropic Messages 利润终检否决（VetoedAccountID），照本地记一次否决再选。
	OpenAIRelayRejectProfitVetoed
)

// OpenAIRelayRejection 是远程选号的拒绝。
type OpenAIRelayRejection struct {
	Kind          OpenAIRelayRejectionKind
	Gateway       OpenAIGatewayRejection
	CyberBlockKey string
	Raw           *middleware2.CapturedRejection
	WSCloseStatus int
	WSCloseReason string
	// ContinuationUnsupported：这次选号跳过了不支持续链的账号，最后的错误是"续链不支持"。
	ContinuationUnsupported bool
	InterceptType           InterceptType
	VetoedAccountID         int64
	// AutoGroupFailover：自动分组 Key 的这个失败本地会先换到下一个候选分组再试（选不出账号、换号用完）。
	// 处理函数照本地的换组分支走（relayAutoGroupFailoverOutcome），没换成再按这个拒绝写。
	AutoGroupFailover bool
}

// relayAutoGroupFailoverError 是主节点回的、本地会先换组再试的选号失败：按"没有可用账号"走本地的换组分支，
// 没换成时按主节点的拒绝写（writeRelayAutoGroupFailoverRejection）。
type relayAutoGroupFailoverError struct {
	rejection *OpenAIRelayRejection
}

func (e *relayAutoGroupFailoverError) Error() string { return service.ErrNoAvailableAccounts.Error() }
func (e *relayAutoGroupFailoverError) Unwrap() error { return service.ErrNoAvailableAccounts }

// relayAutoGroupFailoverOutcome 把主节点标了 AutoGroupFailover 的拒绝转成本地选号失败的结果（会话哈希照旧，与本地一样）。
func relayAutoGroupFailoverOutcome(c *gin.Context, r *OpenAIRelayRejection, sessionHash string) OpenAISelectOutcome {
	return OpenAISelectOutcome{Kind: OpenAISelectFailed, Ctx: c.Request.Context(), SessionHash: sessionHash, Err: &relayAutoGroupFailoverError{rejection: r}}
}

// writeRelayAutoGroupFailoverRejection：换组分支没换成时，err 是主节点的拒绝就按它写（与没有自动分组时一样）。
func (h *OpenAIGatewayHandler) writeRelayAutoGroupFailoverRejection(c *gin.Context, err error, apiKey *service.APIKey, model string, format cyberSessionBlockFormat, lastFailoverErr *service.UpstreamFailoverError, streamStarted bool, reqLog *zap.Logger) bool {
	var relayErr *relayAutoGroupFailoverError
	if !errors.As(err, &relayErr) {
		return false
	}
	h.writeOpenAIRelayRejection(c, relayErr.rejection, apiKey, model, format, lastFailoverErr, streamStarted, reqLog)
	return true
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

// OpenAIRelayCyberUsage 是 cyber 命中且转发返回错误时要记的用量（单机的 RecordCyberPolicyUsageLog）。
type OpenAIRelayCyberUsage struct {
	Result *service.OpenAIForwardResult
	Facts  OpenAIUsageFacts
}

// SetRelayDispatcher 让处理函数在主从分流的从节点上运行（WP9 装配时调用）。
func (h *OpenAIGatewayHandler) SetRelayDispatcher(d OpenAIRelayDispatcher) { h.relay = d }

// SetSecurityAuditCoordinator 装上安全审计协调器（从节点装配用：审计在从节点本地判定，设计 3.4）。
func (h *OpenAIGatewayHandler) SetSecurityAuditCoordinator(c *securityaudit.Coordinator) {
	h.securityAuditCoordinator = c
}

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

// openAIRelayWSUnavailableReason 是 WebSocket 连接上主节点不可达、这条连接不能再继续时的关闭原因。
const openAIRelayWSUnavailableReason = "Service temporarily unavailable"

// writeOpenAIRelayWSRejection 按远程连接选号的拒绝关闭 WebSocket（连接已经接受，不能再交给主节点），
// 与单机在同样情况下的关闭码和原因一致。
func (h *OpenAIGatewayHandler) writeOpenAIRelayWSRejection(c *gin.Context, conn *coderws.Conn, r *OpenAIRelayRejection, lastFailoverErr *service.UpstreamFailoverError, reqLog *zap.Logger) {
	switch r.Kind {
	case OpenAIRelayRejectWSClose:
		if r.CyberBlockKey != "" {
			writeCyberSessionBlockedWSError(c.Request.Context(), conn)
		}
		closeOpenAIClientWS(conn, coderws.StatusCode(r.WSCloseStatus), r.WSCloseReason)
	case OpenAIRelayRejectFailoverExhausted:
		if lastFailoverErr != nil {
			closeOpenAIWSFailoverExhausted(c, conn, lastFailoverErr)
			return
		}
		closeOpenAIClientWS(conn, coderws.StatusTryAgainLater, "no available account")
	case OpenAIRelayRejectUnavailable:
		reqLog.Warn("openai.websocket_relay_master_unavailable")
		if lastFailoverErr != nil {
			closeOpenAIWSFailoverExhausted(c, conn, lastFailoverErr)
			return
		}
		closeOpenAIClientWS(conn, coderws.StatusTryAgainLater, openAIRelayWSUnavailableReason)
	default:
		// 暂不支持 / 中间件拒绝：升级之前的准入会交给主节点，到这里是配置刚变过，请客户端重连。
		reqLog.Warn("openai.websocket_relay_rejected_after_upgrade", zap.Int("kind", int(r.Kind)))
		closeOpenAIClientWS(conn, coderws.StatusTryAgainLater, "relay session expired, please reconnect")
	}
}

// relayWSTurnMapping 是从节点上一轮的渠道映射：与连接开始时的模型相同时用连接选号回复里的，否则按模型缓存、
// 问主节点（本地 MapRequestModel 里的 ResolveChannelMappingAndRestrict）。
func (h *OpenAIGatewayHandler) relayWSTurnMapping(c *gin.Context, conn *OpenAIRelayAttempt, cache *sync.Map, reqModel, model string) (service.ChannelMappingResult, error) {
	if conn == nil {
		return service.ChannelMappingResult{}, service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, openAIRelayWSUnavailableReason, nil)
	}
	if strings.TrimSpace(model) == strings.TrimSpace(reqModel) {
		return conn.ChannelMapping, nil
	}
	if v, ok := cache.Load(model); ok {
		if mapping, isMapping := v.(service.ChannelMappingResult); isMapping {
			return mapping, nil
		}
	}
	mapping, err := h.relay.TurnMapping(c, conn, model)
	if err != nil {
		return service.ChannelMappingResult{}, service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, openAIRelayWSUnavailableReason, err)
	}
	cache.Store(model, mapping)
	return mapping, nil
}
