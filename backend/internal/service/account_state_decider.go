package service

import (
	"context"
	"net/http"
	"sync/atomic"
)

// AccountStateDecider 是转发路径上由限流服务做的账号状态判定（Anthropic、Gemini、Antigravity 等网关服务共用；
// OpenAI 网关有自己的 OpenAIUpstreamErrorDecider）。这些状态有的在库里、有的在调度器的内存里，必须在同一个
// 地方判定和记录。
//
// 单机与主节点用本机的 *RateLimitService（就是原来的代码）；主从分流的从节点装经主节点判定的实现
// （上游错误决策与账号事件，设计 3.1 第 10 步）：转发代码不变、调用顺序不变。
type AccountStateDecider interface {
	// HandleUpstreamError 处理一次上游错误响应，返回账号是否要停止调度（RateLimitService.HandleUpstreamError）。
	HandleUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte, requestedModel ...string) bool
	// HandleStreamTimeout 处理流超时（RateLimitService.HandleStreamTimeout）。
	HandleStreamTimeout(ctx context.Context, account *Account, model string) bool
	// CheckErrorPolicy 按账号的错误策略判定（RateLimitService.CheckErrorPolicy）。
	CheckErrorPolicy(ctx context.Context, account *Account, statusCode int, responseBody []byte, requestedModel ...string) ErrorPolicyResult
	// UpdateSessionWindow 按上游响应头更新账号的 5 小时会话窗口（RateLimitService.UpdateSessionWindow）。
	UpdateSessionWindow(ctx context.Context, account *Account, headers http.Header)
}

var _ AccountStateDecider = (*RateLimitService)(nil)

type accountStateHolder struct{ d AccountStateDecider }

// accountStateSlot 嵌进网关服务：从节点装远程实现；没装时用本机的限流服务。
type accountStateSlot struct {
	accountStateOverride atomic.Pointer[accountStateHolder]
}

// SetAccountStateDecider 换掉账号状态判定（从节点装配用）；nil 恢复本机实现。
func (s *accountStateSlot) SetAccountStateDecider(d AccountStateDecider) {
	if d == nil {
		s.accountStateOverride.Store(nil)
		return
	}
	s.accountStateOverride.Store(&accountStateHolder{d: d})
}

// resolveAccountState 返回生效的判定：装了远程实现用它，否则用本机限流服务；都没有时为 nil
// （与原来"限流服务为 nil 时跳过"一致）。
func (s *accountStateSlot) resolveAccountState(local *RateLimitService) AccountStateDecider {
	if h := s.accountStateOverride.Load(); h != nil {
		return h.d
	}
	if local != nil {
		return local
	}
	return nil
}

// accountStateErrorHandler 把判定包成共用 Anthropic 直通函数要的限流判定（没有判定时为 nil，跳过）。
func accountStateErrorHandler(d AccountStateDecider) func(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte, requestedModel ...string) bool {
	if d == nil {
		return nil
	}
	return d.HandleUpstreamError
}

func (s *GatewayService) accountState() AccountStateDecider {
	return s.resolveAccountState(s.rateLimitService)
}

func (s *GeminiMessagesCompatService) accountState() AccountStateDecider {
	return s.resolveAccountState(s.rateLimitService)
}

func (s *AntigravityGatewayService) accountState() AccountStateDecider {
	return s.resolveAccountState(s.rateLimitService)
}
