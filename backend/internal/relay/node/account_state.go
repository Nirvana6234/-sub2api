package node

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// RemoteAccountState 是从节点上非 OpenAI 网关服务（Anthropic、Gemini、Antigravity 等）的账号状态判定
// （service.AccountStateDecider）：限流、流超时、错误策略同步问主节点（上游错误决策），会话窗口作为账号事件发出。
// 主节点用同一个限流服务判定，库里和调度器内存里的账号状态只在主节点一份。
type RemoteAccountState struct {
	decider  *RemoteUpstreamErrorDecider
	reporter *RemoteAccountReporter
}

var _ service.AccountStateDecider = (*RemoteAccountState)(nil)

// NewRemoteAccountState 用同一份上游错误决策和账号事件发送组装。
func NewRemoteAccountState(decider *RemoteUpstreamErrorDecider, reporter *RemoteAccountReporter) *RemoteAccountState {
	return &RemoteAccountState{decider: decider, reporter: reporter}
}

// HandleUpstreamError 见 service.AccountStateDecider（主节点执行 RateLimitService.HandleUpstreamError）。
func (r *RemoteAccountState) HandleUpstreamError(ctx context.Context, account *service.Account, statusCode int, headers http.Header, body []byte, requestedModel ...string) bool {
	return r.decider.HandleRateLimitError(ctx, account, statusCode, headers, body, requestedModel...)
}

// HandleStreamTimeout 见 service.AccountStateDecider。
func (r *RemoteAccountState) HandleStreamTimeout(ctx context.Context, account *service.Account, model string) bool {
	return r.decider.HandleStreamTimeout(ctx, account, model)
}

// CheckErrorPolicy 见 service.AccountStateDecider。
func (r *RemoteAccountState) CheckErrorPolicy(ctx context.Context, account *service.Account, statusCode int, body []byte, requestedModel ...string) service.ErrorPolicyResult {
	model := ""
	if len(requestedModel) > 0 {
		model = requestedModel[0]
	}
	return r.decider.CheckErrorPolicy(ctx, account, statusCode, body, model)
}

// UpdateSessionWindow 见 service.AccountStateDecider（账号事件，不等回复）。
func (r *RemoteAccountState) UpdateSessionWindow(ctx context.Context, account *service.Account, headers http.Header) {
	r.reporter.UpdateSessionWindow(ctx, account, headers)
}
