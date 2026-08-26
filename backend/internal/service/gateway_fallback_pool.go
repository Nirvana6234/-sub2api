package service

import (
	"context"
	"log/slog"
)

// gatewayFallbackPoolSourcing marks a request whose account candidates are
// being sourced from a configured fallback pool. The request's billing group
// and profit gate remain attached to the original request context.
type gatewayFallbackPoolSourcingCtxKey struct{}
type gatewayFallbackGroupStateCtxKey struct{}

const gatewayFallbackGroupMaxHops = 3

type gatewayFallbackGroupState struct {
	visited map[int64]struct{}
	hops    int
}

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

func gatewayFallbackGroupStateFromContext(ctx context.Context) gatewayFallbackGroupState {
	if ctx == nil {
		return gatewayFallbackGroupState{}
	}
	state, _ := ctx.Value(gatewayFallbackGroupStateCtxKey{}).(gatewayFallbackGroupState)
	return state
}

func withGatewayFallbackGroupState(ctx context.Context, state gatewayFallbackGroupState) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, gatewayFallbackGroupStateCtxKey{}, state)
}

func cloneGatewayFallbackVisited(in map[int64]struct{}) map[int64]struct{} {
	out := make(map[int64]struct{}, len(in)+1)
	for id := range in {
		out[id] = struct{}{}
	}
	return out
}

// nextGatewayFallbackGroup resolves the next Anthropic fallback-pool target.
// It deliberately does not follow the legacy ClaudeCodeOnly fallback unless
// that target is explicitly marked as a fallback pool.
func (s *GatewayService) nextGatewayFallbackGroup(ctx context.Context, currentGroupID *int64) (context.Context, *int64) {
	if s == nil || currentGroupID == nil || *currentGroupID <= 0 {
		return ctx, nil
	}

	state := gatewayFallbackGroupStateFromContext(ctx)
	visited := cloneGatewayFallbackVisited(state.visited)
	currentID := *currentGroupID
	if _, seen := visited[currentID]; seen {
		slog.Warn("gateway_fallback_group_cycle_detected", "group_id", currentID)
		return ctx, nil
	}
	visited[currentID] = struct{}{}
	if state.hops >= gatewayFallbackGroupMaxHops {
		slog.Warn("gateway_fallback_group_max_hops_reached", "group_id", currentID, "max_hops", gatewayFallbackGroupMaxHops)
		return ctx, nil
	}

	currentGroup := s.groupFromContext(ctx, currentID)
	if currentGroup == nil && s.groupRepo != nil {
		currentGroup, _ = s.groupRepo.GetByIDLite(ctx, currentID)
	}
	if currentGroup == nil || currentGroup.Status != StatusActive ||
		currentGroup.Platform != PlatformAnthropic ||
		currentGroup.FallbackGroupID == nil || *currentGroup.FallbackGroupID <= 0 {
		return ctx, nil
	}

	fallbackID := *currentGroup.FallbackGroupID
	if _, seen := visited[fallbackID]; seen {
		slog.Warn("gateway_fallback_group_cycle_detected", "group_id", currentID, "fallback_group_id", fallbackID)
		return ctx, nil
	}
	fallbackGroup, err := s.resolveGroupByID(ctx, fallbackID)
	if err != nil || fallbackGroup == nil || fallbackGroup.Status != StatusActive {
		return ctx, nil
	}
	if fallbackGroup.Platform != PlatformAnthropic || !fallbackGroup.IsFallbackPool {
		slog.Warn("gateway_fallback_group_invalid_target",
			"group_id", currentID,
			"fallback_group_id", fallbackID,
			"fallback_platform", fallbackGroup.Platform,
			"is_fallback_pool", fallbackGroup.IsFallbackPool)
		return ctx, nil
	}

	nextState := gatewayFallbackGroupState{
		visited: visited,
		hops:    state.hops + 1,
	}
	nextCtx := withGatewayFallbackGroupState(withGatewayFallbackPoolSourcing(ctx), nextState)
	return nextCtx, &fallbackID
}
