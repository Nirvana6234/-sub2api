//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuildUsageBillingCommand_SubscriptionAppliesRateMultiplier locks in the fix
// that subscription-mode billing honours the group (and any user-specific) rate
// multiplier — i.e. cmd.SubscriptionCost tracks ActualCost (= TotalCost *
// RateMultiplier), not raw TotalCost.
func TestBuildUsageBillingCommand_SubscriptionAppliesRateMultiplier(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	subID := int64(42)

	tests := []struct {
		name           string
		totalCost      float64
		actualCost     float64
		isSubscription bool
		wantSub        float64
		wantBalance    float64
	}{
		{
			name:           "subscription with 2x multiplier consumes 2x quota",
			totalCost:      1.0,
			actualCost:     2.0,
			isSubscription: true,
			wantSub:        2.0,
			wantBalance:    0,
		},
		{
			name:           "subscription with 0.5x multiplier consumes 0.5x quota",
			totalCost:      1.0,
			actualCost:     0.5,
			isSubscription: true,
			wantSub:        0.5,
			wantBalance:    0,
		},
		{
			name:           "free subscription (multiplier 0) consumes no quota",
			totalCost:      1.0,
			actualCost:     0,
			isSubscription: true,
			wantSub:        0,
			wantBalance:    0,
		},
		{
			name:           "balance billing keeps using ActualCost (regression)",
			totalCost:      1.0,
			actualCost:     2.0,
			isSubscription: false,
			wantSub:        0,
			wantBalance:    2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &postUsageBillingParams{
				Cost:               &CostBreakdown{TotalCost: tt.totalCost, ActualCost: tt.actualCost},
				User:               &User{ID: 1},
				APIKey:             &APIKey{ID: 2, GroupID: &groupID},
				Account:            &Account{ID: 3},
				Subscription:       &UserSubscription{ID: subID},
				IsSubscriptionBill: tt.isSubscription,
			}

			cmd := buildUsageBillingCommand("req-1", nil, p, AccountShareRewardRate, AccountOwnUsageFeeRateDefaultPercent/100)
			if cmd == nil {
				t.Fatal("buildUsageBillingCommand returned nil")
			}
			if cmd.SubscriptionCost != tt.wantSub {
				t.Errorf("SubscriptionCost = %v, want %v", cmd.SubscriptionCost, tt.wantSub)
			}
			if cmd.BalanceCost != tt.wantBalance {
				t.Errorf("BalanceCost = %v, want %v", cmd.BalanceCost, tt.wantBalance)
			}
		})
	}
}

func TestBuildUsageBillingCommand_OwnContributedAccountChargesOnlyPlatformFee(t *testing.T) {
	groupID := int64(7)
	p := &postUsageBillingParams{
		Cost:   &CostBreakdown{TotalCost: 2, ActualCost: 2},
		User:   &User{ID: 42},
		APIKey: &APIKey{ID: 2, GroupID: &groupID},
		Account: &Account{ID: 3, Extra: map[string]any{
			AccountContributionSourceKey: AccountContributionSourceValue,
			AccountContributorUserIDKey:  float64(42),
		}},
	}

	cmd := buildUsageBillingCommand("req-own", nil, p, AccountShareRewardRate, 0.01)
	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.OwnAccountFeeCost != 0.02 {
		t.Errorf("OwnAccountFeeCost = %v, want 0.02", cmd.OwnAccountFeeCost)
	}
	if cmd.BalanceCost != 0.02 {
		t.Errorf("BalanceCost = %v, want 0.02", cmd.BalanceCost)
	}
	if cmd.SharedCost != 0 {
		t.Errorf("SharedCost = %v, want 0", cmd.SharedCost)
	}
}

func TestBuildUsageBillingCommand_RoomBudgetUsesRawTokenCost(t *testing.T) {
	roomRate := 2.0
	p := &postUsageBillingParams{
		Cost:   &CostBreakdown{TotalCost: 1.25, ActualCost: 2.5},
		User:   &User{ID: 42},
		APIKey: &APIKey{ID: 2},
		Account: &Account{
			ID:                                 3,
			ContributionRouteSource:            ContributionRouteSourceRoom,
			ContributionRoomID:                 15,
			ContributionRateMultiplierOverride: &roomRate,
			Extra: map[string]any{
				AccountContributionSourceKey: AccountContributionSourceValue,
				AccountContributorUserIDKey:  float64(77),
			},
		},
	}

	cmd := buildUsageBillingCommand("req-room", nil, p, AccountShareRewardRate, 0.01)
	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.SharedRoomID != 15 {
		t.Errorf("SharedRoomID = %v, want 15", cmd.SharedRoomID)
	}
	if cmd.SharedCost != 2.5 {
		t.Errorf("SharedCost = %v, want 2.5", cmd.SharedCost)
	}
	if cmd.SharedBudgetCost != 1.25 {
		t.Errorf("SharedBudgetCost = %v, want 1.25", cmd.SharedBudgetCost)
	}
	if cmd.BalanceCost != 2.5 {
		t.Errorf("BalanceCost = %v, want 2.5", cmd.BalanceCost)
	}
}

func TestBuildUsageBillingCommand_AccountQuotaUsesAccountStatsCost(t *testing.T) {
	accountStatsCost := 2.5
	p := &postUsageBillingParams{
		Cost:                  &CostBreakdown{TotalCost: 1, ActualCost: 1},
		Account:               &Account{ID: 3, Type: AccountTypeOAuth, Extra: map[string]any{"quota_daily_limit": 10.0}},
		AccountRateMultiplier: 1.2,
		AccountQuotaCost:      accountStatsCost * 1.2,
	}

	cmd := buildUsageBillingCommand("req-account-quota", &UsageLog{AccountStatsCost: &accountStatsCost}, p, AccountShareRewardRate, AccountOwnUsageFeeRateDefaultPercent/100)
	require.NotNil(t, cmd)
	require.InDelta(t, 3.0, cmd.AccountQuotaCost, 1e-12)
}
