//go:build unit

package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newAnthropicAdmitter(cache service.ConcurrencyCache, choose anthropicAccountChooser) AnthropicAccountAdmitter {
	return AnthropicAccountAdmitter{Gateway: &service.GatewayService{}, Concurrency: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, 0), choose: choose}
}

func acquiredChooser(a *service.Account, releases *int) anthropicAccountChooser {
	return func(context.Context, *int64, string, string, map[int64]struct{}, string, int64) (*service.AccountSelectionResult, error) {
		return &service.AccountSelectionResult{Account: a, Acquired: true, ReleaseFunc: func() { *releases++ }}, nil
	}
}

func waitingChooser(a *service.Account) anthropicAccountChooser {
	return func(context.Context, *int64, string, string, map[int64]struct{}, string, int64) (*service.AccountSelectionResult, error) {
		return &service.AccountSelectionResult{Account: a, WaitPlan: &service.AccountWaitPlan{AccountID: a.ID, MaxConcurrency: 1, Timeout: time.Second, MaxWaiting: 1}}, nil
	}
}

// 本地 Messages 与主从分流主节点共用的"选号 + 准入"一轮：各分支的结果。
func TestAnthropicSelectAndAdmit(t *testing.T) {
	groupID := int64(50)
	ctx := profitSlotTestContext(t, &service.OpenAIGatewayService{}, groupID, false)
	log := zap.NewNop()
	base := func() AnthropicSelectRequest {
		return AnthropicSelectRequest{GroupID: &groupID, Model: "claude-sonnet-4-5", Excluded: map[int64]struct{}{}}
	}

	t.Run("selected with the scheduler's slot", func(t *testing.T) {
		releases := 0
		var chosen int64
		req := base()
		req.OnAccountChosen = func(s *service.AccountSelectionResult) { chosen = s.Account.ID }
		out := newAnthropicAdmitter(&scriptedConcurrencyCache{}, acquiredChooser(profitSlotTestAccount(1, 0.3), &releases)).SelectAndAdmit(ctx, req, log)
		require.Equal(t, AnthropicSelected, out.Kind)
		require.Equal(t, int64(1), out.Account.ID)
		require.Equal(t, int64(1), chosen)
		require.NotNil(t, out.Release)
		out.Release()
		require.Equal(t, 1, releases)
	})

	t.Run("profit veto gives the slot back and leaves the retry to the caller", func(t *testing.T) {
		releases := 0
		out := newAnthropicAdmitter(&scriptedConcurrencyCache{}, acquiredChooser(profitSlotTestAccount(1, 0.8), &releases)).SelectAndAdmit(ctx, base(), log)
		require.Equal(t, AnthropicSelectProfitVetoed, out.Kind)
		require.Equal(t, 1, releases)
		require.NotEmpty(t, out.VetoReason)
	})

	t.Run("warmup interception releases the slot before any admission", func(t *testing.T) {
		releases := 0
		a := profitSlotTestAccount(1, 0.3)
		a.Credentials = map[string]any{"intercept_warmup_requests": true}
		req := base()
		req.Intercept = func() InterceptType { return InterceptTypeWarmup }
		out := newAnthropicAdmitter(&scriptedConcurrencyCache{}, acquiredChooser(a, &releases)).SelectAndAdmit(ctx, req, log)
		require.Equal(t, AnthropicSelectIntercepted, out.Kind)
		require.Equal(t, InterceptTypeWarmup, out.Intercept)
		require.Equal(t, 1, releases)

		req.Intercept = func() InterceptType { return InterceptTypeNone }
		out = newAnthropicAdmitter(&scriptedConcurrencyCache{}, acquiredChooser(a, &releases)).SelectAndAdmit(ctx, req, log)
		require.Equal(t, AnthropicSelected, out.Kind, "requests that are not intercepted go on")
	})

	t.Run("scheduler errors, missing wait plans and full queues", func(t *testing.T) {
		failing := func(context.Context, *int64, string, string, map[int64]struct{}, string, int64) (*service.AccountSelectionResult, error) {
			return nil, service.ErrNoAvailableAccounts
		}
		out := newAnthropicAdmitter(&scriptedConcurrencyCache{}, failing).SelectAndAdmit(ctx, base(), log)
		require.Equal(t, AnthropicSelectFailed, out.Kind)
		require.ErrorIs(t, out.Err, service.ErrNoAvailableAccounts)

		noPlan := func(context.Context, *int64, string, string, map[int64]struct{}, string, int64) (*service.AccountSelectionResult, error) {
			return &service.AccountSelectionResult{Account: profitSlotTestAccount(1, 0.3)}, nil
		}
		out = newAnthropicAdmitter(&scriptedConcurrencyCache{}, noPlan).SelectAndAdmit(ctx, base(), log)
		require.Equal(t, AnthropicSelectNoWaitPlan, out.Kind)

		out = newAnthropicAdmitter(&scriptedConcurrencyCache{failFirst: 100, queueFull: true}, waitingChooser(profitSlotTestAccount(1, 0.3))).SelectAndAdmit(ctx, base(), log)
		require.Equal(t, AnthropicSelectQueueFull, out.Kind)
	})

	t.Run("waiting for a slot", func(t *testing.T) {
		out := newAnthropicAdmitter(&scriptedConcurrencyCache{}, waitingChooser(profitSlotTestAccount(1, 0.3))).SelectAndAdmit(ctx, base(), log)
		require.Equal(t, AnthropicSelected, out.Kind, "the slot is taken once it frees up")
		require.NotNil(t, out.Release)

		cannotWait := errors.New("streaming not supported")
		req := base()
		req.CannotWait = cannotWait
		out = newAnthropicAdmitter(&scriptedConcurrencyCache{failFirst: 100}, waitingChooser(profitSlotTestAccount(1, 0.3))).SelectAndAdmit(ctx, req, log)
		require.Equal(t, AnthropicSelectSlotError, out.Kind)
		require.ErrorIs(t, out.Err, cannotWait, "a response that cannot be kept alive does not queue")
	})
}
