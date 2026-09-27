//go:build unit

package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type balanceEligibilityCacheStub struct {
	billingCacheWorkerStub

	balance                  float64
	cacheMissAfterInvalidate bool
	invalidated              atomic.Bool
	deductCalls              atomic.Int64
	invalidateCalls          atomic.Int64
}

func (s *balanceEligibilityCacheStub) GetUserBalance(context.Context, int64) (float64, error) {
	if s.cacheMissAfterInvalidate && s.invalidated.Load() {
		return 0, errors.New("cache miss")
	}
	return s.balance, nil
}

func (s *balanceEligibilityCacheStub) DeductUserBalance(context.Context, int64, float64) error {
	s.deductCalls.Add(1)
	return nil
}

func (s *balanceEligibilityCacheStub) InvalidateUserBalance(context.Context, int64) error {
	s.invalidateCalls.Add(1)
	s.invalidated.Store(true)
	return nil
}

func TestCheckBillingEligibility_RejectsBalanceBelowMinimumReserve(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.005}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.ErrorIs(t, err, ErrInsufficientBalance)
}

func TestCheckBillingEligibility_AllowsBalanceAtMinimumReserve(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.01}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.NoError(t, err)
}

func TestSyncBalanceCacheAfterDeduction_InvalidatesExhaustedBalance(t *testing.T) {
	cache := &balanceEligibilityCacheStub{
		balance:                  0.50,
		cacheMissAfterInvalidate: true,
	}
	userRepo := &balanceLoadUserRepoStub{balance: -0.25}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, userRepo, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	newBalance := -0.25
	syncBalanceCacheAfterDeduction(context.Background(), &postUsageBillingParams{
		Cost: &CostBreakdown{ActualCost: 0.75},
		User: &User{ID: 1},
	}, &billingDeps{billingCacheService: svc}, &UsageBillingApplyResult{
		NewBalance:         &newBalance,
		BalanceOverdrafted: true,
	})

	require.Equal(t, int64(1), cache.invalidateCalls.Load())
	require.Equal(t, int64(0), cache.deductCalls.Load())

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.ErrorIs(t, err, ErrInsufficientBalance)
	require.Equal(t, int64(1), userRepo.calls.Load())
}

func TestSyncBalanceCacheAfterDeduction_InvalidatesWhenBalanceFallsBelowReserve(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.50}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	newBalance := 0.005
	syncBalanceCacheAfterDeduction(context.Background(), &postUsageBillingParams{
		Cost: &CostBreakdown{ActualCost: 0.495},
		User: &User{ID: 1},
	}, &billingDeps{billingCacheService: svc}, &UsageBillingApplyResult{NewBalance: &newBalance})

	require.Equal(t, int64(1), cache.invalidateCalls.Load())
	require.Equal(t, int64(0), cache.deductCalls.Load())
}

func TestSyncBalanceCacheAfterDeduction_QueuesDeductWhenBalanceStillEligible(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 1}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	newBalance := 0.75
	syncBalanceCacheAfterDeduction(context.Background(), &postUsageBillingParams{
		Cost: &CostBreakdown{ActualCost: 0.25},
		User: &User{ID: 1},
	}, &billingDeps{billingCacheService: svc}, &UsageBillingApplyResult{NewBalance: &newBalance})

	require.Equal(t, int64(0), cache.invalidateCalls.Load())
	require.Eventually(t, func() bool {
		return cache.deductCalls.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)
}

type relayReservedStub map[int64]float64

func (s relayReservedStub) RelayReservedBalance(userID int64) float64 { return s[userID] }

// 主从分流运行时，余额预检看"余额 − 锁在从节点上的部分"；摘下后回到只看余额（设计 4.3）。
func TestCheckBillingEligibility_SubtractsBalanceLockedOnRelayNodes(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 5}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	ctx := context.Background()

	require.NoError(t, svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, ""))
	svc.SetRelayReservedBalanceReader(relayReservedStub{1: 4.995})
	require.ErrorIs(t, svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, ""), ErrInsufficientBalance,
		"0.005 spendable is below the minimum reserve")
	require.NoError(t, svc.CheckBillingEligibility(ctx, &User{ID: 2}, nil, nil, nil, ""), "other users are unaffected")
	svc.SetRelayReservedBalanceReader(relayReservedStub{1: 4.98})
	require.NoError(t, svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, ""), "0.02 spendable is above the reserve")
	svc.SetRelayReservedBalanceReader(nil)
	svc.SetRelayReservedBalanceReader(relayReservedStub{1: 5})
	require.ErrorIs(t, svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, ""), ErrInsufficientBalance)
	// 选号来自持有这笔锁定额的从节点：它手里的部分不算"被别处锁走"。
	require.NoError(t, svc.CheckBillingEligibility(WithRelayRequesterHeldBalance(ctx, 5), &User{ID: 1}, nil, nil, nil, ""),
		"all of the balance is locked on the requesting node itself")
	require.ErrorIs(t, svc.CheckBillingEligibility(WithRelayRequesterHeldBalance(ctx, 0.005), &User{ID: 1}, nil, nil, nil, ""), ErrInsufficientBalance,
		"only the requesting node's own share is added back")
	svc.SetRelayReservedBalanceReader(nil)
	require.NoError(t, svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, ""), "relay off: balance only")
}

func TestSpendableBalance(t *testing.T) {
	require.Equal(t, 10.0, SpendableBalance(10, 0))
	require.Equal(t, 10.0, SpendableBalance(10, -1), "a negative reserve is ignored")
	require.InDelta(t, 6.5, (&User{Balance: 10, RelayReservedBalance: 3.5}).SpendableBalance(), 1e-9)
	require.InDelta(t, -2.0, SpendableBalance(1, 3), 1e-9)
}

