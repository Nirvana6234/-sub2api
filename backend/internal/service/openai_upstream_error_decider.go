package service

import (
	"context"
	"net/http"
	"time"
)

// OpenAIUpstreamErrorDecider 判定上游错误并更新账号状态：限流、临时不可调度、停用、冷却、
// OAuth 429 的同账号重试窗口。这些状态有的在库里，有的在进程内存里（调度器读），所以必须在
// 同一个地方判定和记录。
//
// 单机与主节点用本机实现（下面的 localOpenAIUpstreamErrorDecider，就是原来的代码）；主从分流的
// 从节点换成调用主节点的实现（"上游错误决策"，设计 3.1 第 10 步）：转发代码不变，调用的顺序和
// 单机一样，由主节点执行同一段判定，所以判定结果一致。
type OpenAIUpstreamErrorDecider interface {
	// HandleUpstreamError 处理一次上游错误响应，返回账号是否要停止调度（原 handleOpenAIAccountUpstreamError）。
	HandleUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte, canonicalModel ...string) bool
	// OAuth429SameAccountRetry 报告 OAuth 账号的瞬时 429 能否在同一账号上重试，以及重试窗口的截止时间
	// （账号没被运行时熔断、重试窗口还开着）。调用方已先做过与状态无关的判断。
	OAuth429SameAccountRetry(ctx context.Context, account *Account) (bool, time.Time)
	// HandleStreamTimeout 处理流超时（可能把账号标记为临时不可调度或错误）。
	HandleStreamTimeout(ctx context.Context, account *Account, model string) bool
	// CheckErrorPolicy 按账号的错误策略判定（可能把账号标记为临时不可调度）。
	CheckErrorPolicy(ctx context.Context, account *Account, statusCode int, body []byte, model string) ErrorPolicyResult
	// HandleRateLimitError 只走限流服务自己的判定（原 rateLimitService.HandleUpstreamError，
	// 比 HandleUpstreamError 少 OpenAI 的运行时熔断等步骤；共用的 Anthropic 直通函数这样调）。
	HandleRateLimitError(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte, requestedModel ...string) bool
}

type openAIUpstreamErrorDeciderHolder struct{ d OpenAIUpstreamErrorDecider }

// SetUpstreamErrorDecider 换掉上游错误的判定（从节点装配用）；nil 恢复本机实现。
func (s *OpenAIGatewayService) SetUpstreamErrorDecider(d OpenAIUpstreamErrorDecider) {
	if d == nil {
		s.upstreamErrorDecider.Store(nil)
		return
	}
	s.upstreamErrorDecider.Store(&openAIUpstreamErrorDeciderHolder{d: d})
}

// LocalUpstreamErrorDecider 返回本机实现（主节点处理从节点的上游错误决策时用）。
func (s *OpenAIGatewayService) LocalUpstreamErrorDecider() OpenAIUpstreamErrorDecider {
	return localOpenAIUpstreamErrorDecider{s: s}
}

func (s *OpenAIGatewayService) errorDecider() OpenAIUpstreamErrorDecider {
	if s != nil {
		if h := s.upstreamErrorDecider.Load(); h != nil {
			return h.d
		}
	}
	return localOpenAIUpstreamErrorDecider{s: s}
}

// openAIRateLimitErrorHandler 是 OpenAI 转发文件调共用 Anthropic 直通函数时用的限流判定（经判定接口）；
// 没有地方判定时为 nil（与原来限流服务为 nil 时一样跳过）。
func (s *OpenAIGatewayService) openAIRateLimitErrorHandler() func(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte, requestedModel ...string) bool {
	if !s.hasErrorDecider() {
		return nil
	}
	return s.errorDecider().HandleRateLimitError
}

// hasErrorDecider：有地方可以判定上游错误（本机有限流服务，或装了远端实现）。
func (s *OpenAIGatewayService) hasErrorDecider() bool {
	return s != nil && (s.rateLimitService != nil || s.upstreamErrorDecider.Load() != nil)
}

type localOpenAIUpstreamErrorDecider struct{ s *OpenAIGatewayService }

func (d localOpenAIUpstreamErrorDecider) HandleUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte, canonicalModel ...string) bool {
	return d.s.handleOpenAIAccountUpstreamErrorLocal(ctx, account, statusCode, headers, body, canonicalModel...)
}

func (d localOpenAIUpstreamErrorDecider) OAuth429SameAccountRetry(_ context.Context, account *Account) (bool, time.Time) {
	s := d.s
	// markOpenAIOAuth429RateLimited parks the account once the window expires.
	// Do not accidentally create a fresh window after that transition.
	if s.isOpenAIAccountRuntimeBlocked(account) {
		return false, time.Time{}
	}
	if !s.openAIOAuth429RetryWindowActive(account) {
		return false, time.Time{}
	}
	return true, s.openAIOAuth429RetryDeadline(account)
}

func (d localOpenAIUpstreamErrorDecider) HandleStreamTimeout(ctx context.Context, account *Account, model string) bool {
	if d.s == nil || d.s.rateLimitService == nil {
		return false
	}
	return d.s.rateLimitService.HandleStreamTimeout(ctx, account, model)
}

func (d localOpenAIUpstreamErrorDecider) HandleRateLimitError(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte, requestedModel ...string) bool {
	if d.s == nil || d.s.rateLimitService == nil {
		return false
	}
	return d.s.rateLimitService.HandleUpstreamError(ctx, account, statusCode, headers, body, requestedModel...)
}

func (d localOpenAIUpstreamErrorDecider) CheckErrorPolicy(ctx context.Context, account *Account, statusCode int, body []byte, model string) ErrorPolicyResult {
	if d.s == nil || d.s.rateLimitService == nil {
		return ErrorPolicyNone
	}
	return d.s.rateLimitService.CheckErrorPolicy(ctx, account, statusCode, body, model)
}

// OpenAIUpstreamErrorFlags 是判定会从请求 ctx 读的标记。主从分流时从节点把它们随上游错误决策发给主节点，
// 主节点装回 ctx 再判定。
type OpenAIUpstreamErrorFlags struct {
	ImagesSelfBuilt bool
	ImagesEndpoint  bool
}

// OpenAIUpstreamErrorFlagsFromContext 读出 ctx 上的标记。
func OpenAIUpstreamErrorFlagsFromContext(ctx context.Context) OpenAIUpstreamErrorFlags {
	return OpenAIUpstreamErrorFlags{ImagesSelfBuilt: isOpenAIImagesSelfBuiltRequest(ctx), ImagesEndpoint: OpenAIImagesEndpointFromContext(ctx)}
}

// WithOpenAIUpstreamErrorFlags 把标记装回 ctx。
func WithOpenAIUpstreamErrorFlags(ctx context.Context, f OpenAIUpstreamErrorFlags) context.Context {
	if f.ImagesSelfBuilt {
		ctx = withOpenAIImagesSelfBuiltRequest(ctx)
	}
	if f.ImagesEndpoint {
		ctx = WithOpenAIImagesEndpoint(ctx)
	}
	return ctx
}
