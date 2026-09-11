package httpserver

import (
	"context"
	"testing"
)

type fakeBalanceAlertClaimer struct {
	claimed bool
	calls   int
}

func (f *fakeBalanceAlertClaimer) ClaimBalanceAlert(context.Context, string, string, string) (bool, error) {
	f.calls++
	if f.claimed {
		return false, nil
	}
	f.claimed = true
	return true, nil
}

func TestClaimBalanceAlertOnlyFirstCallClaims(t *testing.T) {
	fake := &fakeBalanceAlertClaimer{}
	ctx := context.Background()

	if !claimBalanceAlert(ctx, fake, "user-1", "workspace-1", "site-1", "site") {
		t.Fatal("首次余额预警应成功 claim")
	}
	if claimBalanceAlert(ctx, fake, "user-1", "workspace-1", "site-1", "site") {
		t.Fatal("同一账号的重复余额预警不应再次 claim")
	}
	if fake.calls != 2 {
		t.Fatalf("claim 调用次数 = %d，期望 2", fake.calls)
	}
}
