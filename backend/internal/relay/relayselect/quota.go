package relayselect

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// quotaNeed 是每项子额度至少要有的金额（微单位）。
//
// 本地只要求各项剩余大于 0 就放行（CheckBillingEligibility），超出的部分事后扣成负数；
// 主节点要求更多的话，单机能用的请求在主从模式下会被拒。所以这里只要求"有一点"，
// 从节点并发请求可能超支的部分由设计 4.5 的超支上限兜住。
const quotaNeed int64 = 1

// acquireQuota 按这次请求用到的各项子额度补充额度：节点手里某项不够 need 时才向额度服务申请
// （force 时总是申请，提前补充用）。返回给出的额度和这次请求用到的各项。
func (s *selector) acquireQuota(ctx context.Context, nodeID int64, req service.QuotaRequest, held []*relayv1.HeldQuota, need int64, force bool) ([]*relayv1.QuotaGrant, []*relayv1.QuotaScope, error) {
	headroom, err := s.deps.Billing.QuotaHeadroom(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	if len(headroom) == 0 {
		return nil, nil, nil // 简易模式、没有上限
	}
	heldBy := map[master.LeaseScope]int64{}
	for _, h := range held {
		heldBy[scopeFromProto(h.GetScope())] += h.GetUnused()
	}
	wants := make([]master.QuotaWant, 0, len(headroom))
	scopes := make([]*relayv1.QuotaScope, 0, len(headroom))
	short := force
	for _, h := range headroom {
		scope := master.LeaseScope{Dimension: h.Dimension, ScopeID: h.ScopeID, ScopeKey: h.ScopeKey}
		unused := heldBy[scope]
		if unused < need {
			short = true
		}
		wants = append(wants, master.QuotaWant{Scope: scope, Headroom: h.Remaining, ExpiresAt: h.ExpiresAt, NodeUnused: master.Micros(unused)})
		scopes = append(scopes, &relayv1.QuotaScope{Dimension: h.Dimension, ScopeId: h.ScopeID, ScopeKey: h.ScopeKey})
	}
	if !short {
		return nil, scopes, nil
	}
	if s.env.Quotas == nil {
		return nil, nil, status.Error(codes.Unavailable, "relay quota service is not available")
	}
	granted, err := s.env.Quotas.Acquire(ctx, master.AcquireRequest{UserID: req.User.ID, NodeID: nodeID, Wants: wants, Need: master.Micros(need)})
	if err != nil {
		return nil, nil, err
	}
	out := make([]*relayv1.QuotaGrant, 0, len(granted))
	for _, g := range granted {
		out = append(out, g.Proto(req.User.ID))
	}
	return out, scopes, nil
}

func scopeFromProto(s *relayv1.QuotaScope) master.LeaseScope {
	return master.LeaseScope{Dimension: s.GetDimension(), ScopeID: s.GetScopeId(), ScopeKey: s.GetScopeKey()}
}

// heldBalance 是节点报告的、它手里这个用户余额维度还没用掉的金额（微单位）。
func heldBalance(held []*relayv1.HeldQuota) int64 {
	var total int64
	for _, h := range held {
		if h.GetScope().GetDimension() == service.QuotaDimBalance {
			total += h.GetUnused()
		}
	}
	return total
}

// quotaError 把额度不够的那一项换成本地计费检查在同一项上返回的错误（文案、状态码一致）。
func quotaError(dimension string) error {
	switch dimension {
	case service.QuotaDimSubscriptionDaily:
		return service.ErrDailyLimitExceeded
	case service.QuotaDimSubscriptionWeekly:
		return service.ErrWeeklyLimitExceeded
	case service.QuotaDimSubscriptionMonthly:
		return service.ErrMonthlyLimitExceeded
	case service.QuotaDimPlatformDaily:
		return service.ErrUserPlatformDailyQuotaExhausted
	case service.QuotaDimPlatformWeekly:
		return service.ErrUserPlatformWeeklyQuotaExhausted
	case service.QuotaDimPlatformMonthly:
		return service.ErrUserPlatformMonthlyQuotaExhausted
	case service.QuotaDimAPIKey5h:
		return service.ErrAPIKeyRateLimit5hExceeded
	case service.QuotaDimAPIKey1d:
		return service.ErrAPIKeyRateLimit1dExceeded
	case service.QuotaDimAPIKey7d:
		return service.ErrAPIKeyRateLimit7dExceeded
	case service.QuotaDimAPIKeyTotal:
		return service.ErrAPIKeyQuotaExhausted
	default:
		return service.ErrInsufficientBalance
	}
}
