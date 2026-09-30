package nodegw

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// AnthropicDeps 是从节点 Anthropic Messages 处理函数额外用到的部件。
type AnthropicDeps struct {
	// AccountState 是转发路径上的账号状态判定（node.RemoteAccountState：限流、流超时、错误策略同步问主节点）。
	AccountState service.AccountStateDecider
	// TempUnschedulable 把转发路径上直接写账号仓储的临时不可调度交给主节点照写（账号事件）。
	TempUnschedulable func(accountID int64, until time.Time, reason string)
}

// relayAccountRepo 是从节点上 GatewayService 的账号仓储：转发路径只写临时不可调度（同账号重试用尽的 400/502、
// 持久的传输错误），作为账号事件交给主节点照写。其余方法没有实现（从节点不连库），转发路径不该调到。
type relayAccountRepo struct {
	service.AccountRepository
	tempUnschedulable func(accountID int64, until time.Time, reason string)
}

func (r relayAccountRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	if r.tempUnschedulable != nil {
		r.tempUnschedulable(id, until, reason)
	}
	return nil
}

// NewAnthropicHandler 组装从节点上的 Messages 处理函数（Anthropic 分组）：与单机同一个处理函数和转发服务，
// 换上远程选号与记账（Dispatcher）、远程账号状态判定、只写临时不可调度的账号仓储。仓储、调度、并发、计费一律
// 不给（这些在主节点）。
func NewAnthropicHandler(d GatewayDeps, a AnthropicDeps) *handler.GatewayHandler {
	gw := service.NewGatewayService(relayAccountRepo{tempUnschedulable: a.TempUnschedulable}, nil, nil, nil, nil, nil, nil,
		NoopGatewayCache{}, d.Config, nil, nil, nil, nil, nil, nil, d.HTTPUpstream, nil, nil, nil, nil,
		service.NewDigestSessionStore(), d.Settings, nil, nil, nil, nil, nil, nil)
	gw.SetAccountStateDecider(a.AccountState)
	var moderation *service.ContentModerationService
	if d.Moderation != nil {
		moderation = d.Moderation.Service
	}
	h := handler.NewGatewayHandler(gw, nil, nil, nil, nil, nil, nil, nil, nil, nil, d.ErrorPassthrough, moderation, nil, d.Config, d.Settings)
	h.SetRelayDispatcher(d.Dispatcher)
	if d.Moderation != nil {
		// 安全审计在从节点本地判定（设计 3.4），与单机同一个协调器。
		h.SetSecurityAuditCoordinator(d.Moderation.Coordinator)
	}
	return h
}
