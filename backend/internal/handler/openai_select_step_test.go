//go:build unit

package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// scriptedChooser 按顺序给出账号，跳过已排除的；给完返回 ErrNoAvailableAccounts。
type scriptedChooser struct {
	accounts []*service.Account
	calls    int
	releases int
}

func (s *scriptedChooser) choose(_ context.Context, _ *int64, _ string, _ string, _ string, excluded map[int64]struct{},
	_ service.OpenAIUpstreamTransport, _ service.OpenAIEndpointCapability, _ bool, _ bool, _ bool, _ ...string,
) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error) {
	s.calls++
	for _, a := range s.accounts {
		if _, skip := excluded[a.ID]; skip {
			continue
		}
		return &service.AccountSelectionResult{
			Account:     a,
			Acquired:    true,
			ReleaseFunc: func() { s.releases++ },
		}, service.OpenAIAccountScheduleDecision{}, nil
	}
	return nil, service.OpenAIAccountScheduleDecision{}, service.ErrNoAvailableAccounts
}

func stepAccount(id int64, typ string) *service.Account {
	a := profitSlotTestAccount(id, 0.3)
	a.Type = typ
	return a
}

func newStepAdmitter(cache service.ConcurrencyCache, chooser *scriptedChooser) OpenAIAccountAdmitter {
	a := newAdmitter(cache)
	a.choose = chooser.choose
	return a
}

// 主从分流主节点与本地 Responses 共用的"选号 + 准入"一步：各分支的结果。
func TestOpenAISelectAndAdmit(t *testing.T) {
	groupID := int64(50)
	gw := &service.OpenAIGatewayService{}
	log := zap.NewNop()
	base := func() OpenAISelectRequest {
		return OpenAISelectRequest{GroupID: &groupID, ForwardModel: "gpt-5", RequestPlatform: service.PlatformOpenAI, Excluded: map[int64]struct{}{}}
	}

	t.Run("previous_response_id skips OAuth accounts and picks an API-key account", func(t *testing.T) {
		chooser := &scriptedChooser{accounts: []*service.Account{stepAccount(1, service.AccountTypeOAuth), stepAccount(2, service.AccountTypeAPIKey)}}
		req := base()
		req.PreviousResponseID = "resp_abc"
		var state OpenAISelectState
		out := newStepAdmitter(&scriptedConcurrencyCache{}, chooser).SelectAndAdmit(profitSlotTestContext(t, gw, groupID, false), req, &state, log)
		require.Equal(t, OpenAISelected, out.Kind)
		require.Equal(t, int64(2), out.Account.ID)
		require.Contains(t, req.Excluded, int64(1))
		require.Equal(t, 1, chooser.releases, "the skipped account's slot is given back")
		require.NotNil(t, state.LastFailoverErr)
		require.Equal(t, service.OpenAIHTTPContinuationUnsupportedReason, state.LastFailoverErr.Reason)
	})

	t.Run("only OAuth accounts: selection fails with the continuation error recorded", func(t *testing.T) {
		chooser := &scriptedChooser{accounts: []*service.Account{stepAccount(1, service.AccountTypeOAuth)}}
		req := base()
		req.PreviousResponseID = "resp_abc"
		var state OpenAISelectState
		out := newStepAdmitter(&scriptedConcurrencyCache{}, chooser).SelectAndAdmit(profitSlotTestContext(t, gw, groupID, false), req, &state, log)
		require.Equal(t, OpenAISelectFailed, out.Kind)
		require.ErrorIs(t, out.Err, service.ErrNoAvailableAccounts)
		require.NotNil(t, state.LastFailoverErr, "the caller answers with the continuation 400, not a generic error")
	})

	t.Run("without previous_response_id OAuth accounts are used", func(t *testing.T) {
		chooser := &scriptedChooser{accounts: []*service.Account{stepAccount(1, service.AccountTypeOAuth)}}
		var state OpenAISelectState
		out := newStepAdmitter(&scriptedConcurrencyCache{}, chooser).SelectAndAdmit(profitSlotTestContext(t, gw, groupID, false), base(), &state, log)
		require.Equal(t, OpenAISelected, out.Kind)
		require.Equal(t, int64(1), out.Account.ID)
		require.Nil(t, state.LastFailoverErr)
	})

	t.Run("profit vetoes reselect until the budget is spent", func(t *testing.T) {
		accounts := make([]*service.Account, 0, maxProfitVetoAttempts+2)
		for i := int64(1); i <= maxProfitVetoAttempts+2; i++ {
			accounts = append(accounts, profitSlotTestAccount(i, 0.8))
		}
		chooser := &scriptedChooser{accounts: accounts}
		req := base()
		var state OpenAISelectState
		out := newStepAdmitter(&scriptedConcurrencyCache{}, chooser).SelectAndAdmit(profitSlotTestContext(t, gw, groupID, false), req, &state, log)
		require.Equal(t, OpenAISelectVetoExhausted, out.Kind)
		require.Equal(t, maxProfitVetoAttempts, state.ProfitVetoCount)
		require.Len(t, req.Excluded, maxProfitVetoAttempts)
		require.Equal(t, maxProfitVetoAttempts, chooser.releases, "every vetoed slot is released")
	})

	t.Run("a vetoed account is replaced by a cheaper one", func(t *testing.T) {
		chooser := &scriptedChooser{accounts: []*service.Account{profitSlotTestAccount(1, 0.8), profitSlotTestAccount(2, 0.3)}}
		var state OpenAISelectState
		out := newStepAdmitter(&scriptedConcurrencyCache{}, chooser).SelectAndAdmit(profitSlotTestContext(t, gw, groupID, false), base(), &state, log)
		require.Equal(t, OpenAISelected, out.Kind)
		require.Equal(t, int64(2), out.Account.ID)
		require.Equal(t, 1, state.ProfitVetoCount)
	})

	t.Run("no account and cancelled requests", func(t *testing.T) {
		var state OpenAISelectState
		out := newStepAdmitter(&scriptedConcurrencyCache{}, &scriptedChooser{}).SelectAndAdmit(profitSlotTestContext(t, gw, groupID, false), base(), &state, log)
		require.Equal(t, OpenAISelectFailed, out.Kind)
		nothing := newStepAdmitter(&scriptedConcurrencyCache{}, &scriptedChooser{})
		nothing.choose = func(context.Context, *int64, string, string, string, map[int64]struct{},
			service.OpenAIUpstreamTransport, service.OpenAIEndpointCapability, bool, bool, bool, ...string,
		) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error) {
			return nil, service.OpenAIAccountScheduleDecision{}, nil
		}
		out = nothing.SelectAndAdmit(profitSlotTestContext(t, gw, groupID, false), base(), &state, log)
		require.Equal(t, OpenAISelectNone, out.Kind)

		ctx, cancel := context.WithCancel(profitSlotTestContext(t, gw, groupID, false))
		cancel()
		chooser := &scriptedChooser{accounts: []*service.Account{stepAccount(1, service.AccountTypeAPIKey)}}
		out = newStepAdmitter(&scriptedConcurrencyCache{}, chooser).SelectAndAdmit(ctx, base(), &state, log)
		require.Equal(t, OpenAISelectAborted, out.Kind)
		require.Zero(t, chooser.calls)
	})

	t.Run("pool-mode accounts get a one-off session hash", func(t *testing.T) {
		pool := stepAccount(1, service.AccountTypeAPIKey)
		pool.Credentials = map[string]any{"pool_mode": true}
		var state OpenAISelectState
		out := newStepAdmitter(&scriptedConcurrencyCache{}, &scriptedChooser{accounts: []*service.Account{pool}}).SelectAndAdmit(profitSlotTestContext(t, gw, groupID, false), base(), &state, log)
		require.Equal(t, OpenAISelected, out.Kind)
		require.True(t, strings.HasPrefix(out.SessionHash, "openai-pool-retry-"), out.SessionHash)
	})

	t.Run("admission failures are reported with the local response", func(t *testing.T) {
		chooser := &scriptedChooser{accounts: []*service.Account{stepAccount(1, service.AccountTypeAPIKey)}}
		waiting := func(ctx context.Context, groupID *int64, _ string, _ string, _ string, _ map[int64]struct{},
			_ service.OpenAIUpstreamTransport, _ service.OpenAIEndpointCapability, _ bool, _ bool, _ bool, _ ...string,
		) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error) {
			a := chooser.accounts[0]
			return &service.AccountSelectionResult{Account: a, WaitPlan: &service.AccountWaitPlan{AccountID: a.ID, MaxConcurrency: 1, Timeout: time.Second, MaxWaiting: 1}}, service.OpenAIAccountScheduleDecision{}, nil
		}
		a := newAdmitter(&scriptedConcurrencyCache{failFirst: 100, queueFull: true})
		a.choose = waiting
		var state OpenAISelectState
		out := a.SelectAndAdmit(profitSlotTestContext(t, gw, groupID, false), base(), &state, log)
		require.Equal(t, OpenAISelectQueueFull, out.Kind)
		status, _, code, _ := OpenAIAdmissionFailureResponse(out)
		require.Equal(t, 429, status)
		require.Equal(t, gatewayQueueFullCode, code)

		noPlan := func(context.Context, *int64, string, string, string, map[int64]struct{},
			service.OpenAIUpstreamTransport, service.OpenAIEndpointCapability, bool, bool, bool, ...string,
		) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error) {
			return &service.AccountSelectionResult{Account: chooser.accounts[0]}, service.OpenAIAccountScheduleDecision{}, nil
		}
		a.choose = noPlan
		out = a.SelectAndAdmit(profitSlotTestContext(t, gw, groupID, false), base(), &state, log)
		require.Equal(t, OpenAISelectNoWaitPlan, out.Kind)
		status, _, _, message := OpenAIAdmissionFailureResponse(out)
		require.Equal(t, 503, status)
		require.Equal(t, "No available accounts", message)
	})
}
