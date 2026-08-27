package service

import (
	"context"
)

// GatewayService 这一侧的分组兜底池：Anthropic 与 Gemini。
//
// 遍历算法、防环、跳数上限和准入加严都在 fallback_pool.go 的共享内核里，这里只负责
// 三件事：本侧的 ctx key、分组怎么读、以及平台白名单。
//
// ctx key 不与 OpenAI 侧共用是刻意的，原因见 fallback_pool.go 顶部说明。

type gatewayFallbackPoolSourcingCtxKey struct{}
type gatewayFallbackGroupStateCtxKey struct{}

func withGatewayFallbackPoolSourcing(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, gatewayFallbackPoolSourcingCtxKey{}, true)
}

func isGatewayFallbackPoolSourcing(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(gatewayFallbackPoolSourcingCtxKey{}).(bool)
	return value
}

func gatewayFallbackGroupStateFromContext(ctx context.Context) fallbackGroupState {
	if ctx == nil {
		return fallbackGroupState{}
	}
	state, _ := ctx.Value(gatewayFallbackGroupStateCtxKey{}).(fallbackGroupState)
	return state
}

func gatewayFallbackPoolUsageTraceFromContext(ctx context.Context) (fallbackPoolUsageTrace, bool) {
	return fallbackPoolUsageTraceFromState(gatewayFallbackGroupStateFromContext(ctx))
}

func withGatewayFallbackGroupState(ctx context.Context, state fallbackGroupState) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, gatewayFallbackGroupStateCtxKey{}, state)
}

// gatewayPlatformSupportsFallbackPool 限定本 service 负责兜底的平台。
//
// 比全局白名单 platformSupportsFallbackPool 更窄：OpenAI 与 Grok 由
// OpenAIGatewayService 自己那条链路兜底，这里不能重复接管，否则同一次请求会被两套
// 兜底逻辑各推进一次，visited 也各算各的。
func gatewayPlatformSupportsFallbackPool(platform string) bool {
	switch platform {
	case PlatformAnthropic, PlatformGemini:
		return true
	default:
		return false
	}
}

// gatewayFallbackPoolRejectReason 报告某账号在兜底取号时是否应被拒绝。
// 规则与 OpenAI 侧一致，见 fallbackPoolRejectReasonWhenSourcing。
func gatewayFallbackPoolRejectReason(ctx context.Context, account *Account) string {
	return fallbackPoolRejectReasonWhenSourcing(ctx, account, isGatewayFallbackPoolSourcing(ctx))
}

// nextGatewayFallbackGroup 解析下一个兜底目标。
//
// 它刻意不跟随旧的 ClaudeCodeOnly 降级：那条链路复用了同一个 fallback_group_id
// 字段，但只在 group.ClaudeCodeOnly 为真时才走（见 resolveGatewayGroup）。这里要求
// 目标显式标记为 is_fallback_pool，两条链路因此互不干扰。
func (s *GatewayService) nextGatewayFallbackGroup(ctx context.Context, currentGroupID *int64) (context.Context, *int64) {
	if s == nil || currentGroupID == nil || *currentGroupID <= 0 {
		return ctx, nil
	}

	fallbackID, nextState, ok := nextFallbackGroupID(
		ctx,
		*currentGroupID,
		gatewayFallbackGroupStateFromContext(ctx),
		fallbackTraversal{
			logNS:        "gateway",
			resolveGroup: s.resolveFallbackGroup,
			currentGroupOK: func(group *Group) bool {
				return group.Status == StatusActive &&
					gatewayPlatformSupportsFallbackPool(group.Platform)
			},
			fallbackGroupOK: func(group *Group) (bool, string) {
				if group.Status != StatusActive {
					return false, ""
				}
				if !gatewayPlatformSupportsFallbackPool(group.Platform) {
					return false, "platform_not_supported"
				}
				if !group.IsFallbackPool {
					return false, "target_not_fallback_pool"
				}
				return true, ""
			},
		},
	)
	if !ok {
		return ctx, nil
	}

	nextCtx := withGatewayFallbackGroupState(withGatewayFallbackPoolSourcing(ctx), nextState)
	if trace, ok := fallbackPoolUsageTraceFromState(nextState); ok {
		notifyFallbackPoolSelection(ctx, s.cfg, trace)
	}
	return nextCtx, &fallbackID
}

// resolveFallbackGroup 优先用请求上下文里已有的分组，避免兜底链路上重复查库。
func (s *GatewayService) resolveFallbackGroup(ctx context.Context, groupID int64) *Group {
	if s == nil || groupID <= 0 {
		return nil
	}
	if group := s.groupFromContext(ctx, groupID); group != nil {
		return group
	}
	if s.groupRepo == nil {
		return nil
	}
	group, err := s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil
	}
	return group
}
