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

// 主从分流主节点与本地 Responses WebSocket 共用的"选号 + 不排队抢槽 + 利润终检"一步：各分支的结果。
func TestOpenAISelectAndAdmitWS(t *testing.T) {
	groupID := int64(50)
	gw := &service.OpenAIGatewayService{}
	log := zap.NewNop()
	base := func() OpenAIWSSelectRequest {
		return OpenAIWSSelectRequest{
			GroupID: &groupID, ForwardModel: "gpt-5", RequestPlatform: service.PlatformOpenAI,
			RequiredTransport: service.OpenAIUpstreamTransportResponsesWebsocketV2Ingress, Excluded: map[int64]struct{}{},
		}
	}
	waitPlan := func(accounts ...*service.Account) openAIAccountChooser {
		return func(_ context.Context, _ *int64, _ string, _ string, _ string, excluded map[int64]struct{},
			_ service.OpenAIUpstreamTransport, _ service.OpenAIEndpointCapability, _ bool, _ bool, _ bool, _ ...string,
		) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error) {
			for _, a := range accounts {
				if _, skip := excluded[a.ID]; skip {
					continue
				}
				return &service.AccountSelectionResult{Account: a, WaitPlan: &service.AccountWaitPlan{AccountID: a.ID, MaxConcurrency: 7, Timeout: time.Second}},
					service.OpenAIAccountScheduleDecision{StickyPreviousHit: true}, nil
			}
			return nil, service.OpenAIAccountScheduleDecision{}, service.ErrNoAvailableAccounts
		}
	}

	t.Run("an account the scheduler already acquired", func(t *testing.T) {
		chooser := &scriptedChooser{accounts: []*service.Account{stepAccount(1, service.AccountTypeOAuth)}}
		veto := 0
		out := newStepAdmitter(&scriptedConcurrencyCache{}, chooser).SelectAndAdmitWS(profitSlotTestContext(t, gw, groupID, false), base(), &veto, log)
		require.Equal(t, OpenAIWSSelected, out.Kind)
		require.Equal(t, int64(1), out.Account.ID)
		require.Equal(t, 2, out.MaxConcurrency, "without a wait plan the account's own concurrency")
		out.Release()
		require.Equal(t, 1, chooser.releases)
	})

	t.Run("a wait plan is tried once without queueing", func(t *testing.T) {
		cache := &scriptedConcurrencyCache{}
		a := newAdmitter(cache)
		a.choose = waitPlan(stepAccount(1, service.AccountTypeAPIKey))
		veto := 0
		out := a.SelectAndAdmitWS(profitSlotTestContext(t, gw, groupID, false), base(), &veto, log)
		require.Equal(t, OpenAIWSSelected, out.Kind)
		require.Equal(t, 7, out.MaxConcurrency, "the wait plan's limit is used for later turns")
		require.True(t, out.StickyPreviousHit)
		require.Equal(t, int64(1), cache.attempts.Load())

		busy := newAdmitter(&scriptedConcurrencyCache{failFirst: 100})
		busy.choose = waitPlan(stepAccount(1, service.AccountTypeAPIKey))
		out = busy.SelectAndAdmitWS(profitSlotTestContext(t, gw, groupID, false), base(), &veto, log)
		require.Equal(t, OpenAIWSSelectBusy, out.Kind)

		broken := newAdmitter(&scriptedConcurrencyCache{acquireErr: errors.New("redis down")})
		broken.choose = waitPlan(stepAccount(1, service.AccountTypeAPIKey))
		out = broken.SelectAndAdmitWS(profitSlotTestContext(t, gw, groupID, false), base(), &veto, log)
		require.Equal(t, OpenAIWSSelectSlotError, out.Kind)
		require.Error(t, out.Err)

		noPlan := newAdmitter(&scriptedConcurrencyCache{})
		noPlan.choose = func(context.Context, *int64, string, string, string, map[int64]struct{},
			service.OpenAIUpstreamTransport, service.OpenAIEndpointCapability, bool, bool, bool, ...string,
		) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error) {
			return &service.AccountSelectionResult{Account: stepAccount(1, service.AccountTypeAPIKey)}, service.OpenAIAccountScheduleDecision{}, nil
		}
		out = noPlan.SelectAndAdmitWS(profitSlotTestContext(t, gw, groupID, false), base(), &veto, log)
		require.Equal(t, OpenAIWSSelectBusy, out.Kind)
	})

	t.Run("profit vetoes reselect until the budget is spent, on both slot paths", func(t *testing.T) {
		accounts := make([]*service.Account, 0, maxProfitVetoAttempts+2)
		for i := int64(1); i <= maxProfitVetoAttempts+2; i++ {
			accounts = append(accounts, profitSlotTestAccount(i, 0.8))
		}
		chooser := &scriptedChooser{accounts: accounts}
		req := base()
		veto := 0
		out := newStepAdmitter(&scriptedConcurrencyCache{}, chooser).SelectAndAdmitWS(profitSlotTestContext(t, gw, groupID, false), req, &veto, log)
		require.Equal(t, OpenAIWSSelectVetoExhausted, out.Kind)
		require.Equal(t, maxProfitVetoAttempts, veto)
		require.Len(t, req.Excluded, maxProfitVetoAttempts)
		require.Equal(t, maxProfitVetoAttempts, chooser.releases, "every vetoed slot is released")

		cache := &scriptedConcurrencyCache{}
		a := newAdmitter(cache)
		a.choose = waitPlan(profitSlotTestAccount(1, 0.8), profitSlotTestAccount(2, 0.3))
		veto = 0
		out = a.SelectAndAdmitWS(profitSlotTestContext(t, gw, groupID, false), base(), &veto, log)
		require.Equal(t, OpenAIWSSelected, out.Kind)
		require.Equal(t, int64(2), out.Account.ID)
		require.Equal(t, 1, veto)
		require.Equal(t, int64(1), cache.releases.Load(), "the vetoed fast-acquired slot is released")
	})

	t.Run("no account and cancelled connections", func(t *testing.T) {
		veto := 0
		out := newStepAdmitter(&scriptedConcurrencyCache{}, &scriptedChooser{}).SelectAndAdmitWS(profitSlotTestContext(t, gw, groupID, false), base(), &veto, log)
		require.Equal(t, OpenAIWSSelectFailed, out.Kind)
		require.ErrorIs(t, out.Err, service.ErrNoAvailableAccounts)

		ctx, cancel := context.WithCancel(profitSlotTestContext(t, gw, groupID, false))
		cancel()
		chooser := &scriptedChooser{accounts: []*service.Account{stepAccount(1, service.AccountTypeAPIKey)}}
		out = newStepAdmitter(&scriptedConcurrencyCache{}, chooser).SelectAndAdmitWS(ctx, base(), &veto, log)
		require.Equal(t, OpenAIWSSelectAborted, out.Kind)
		require.Zero(t, chooser.calls)
	})
}
