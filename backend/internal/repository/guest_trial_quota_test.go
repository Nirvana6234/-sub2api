package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newGuestTrialQuotaForTest(t *testing.T) (service.GuestTrialQuota, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewGuestTrialQuota(client), server
}

func TestGuestTrialQuotaConsumeEnforcesEachLayer(t *testing.T) {
	quota, _ := newGuestTrialQuotaForTest(t)
	ctx := context.Background()
	limits := service.GuestTrialQuotaLimits{PerVisitor: 2, PerIP: 3, Global: 4}
	deviceA := service.GuestTrialQuotaKeys{Day: "20260923", Device: "device-a", IP: "1.1.1.1"}
	deviceB := service.GuestTrialQuotaKeys{Day: "20260923", Device: "device-b", IP: "1.1.1.1"}
	deviceC := service.GuestTrialQuotaKeys{Day: "20260923", Device: "device-c", IP: "2.2.2.2"}

	for i := 1; i <= 2; i++ {
		reason, used, err := quota.Consume(ctx, deviceA, limits)
		if err != nil || reason != service.GuestTrialQuotaAllowed || used != i {
			t.Fatalf("consume #%d: reason=%v used=%d err=%v", i, reason, used, err)
		}
	}
	if reason, _, _ := quota.Consume(ctx, deviceA, limits); reason != service.GuestTrialQuotaVisitorFull {
		t.Fatalf("visitor limit not enforced: %v", reason)
	}
	// 同一 IP 的另一个设备：IP 上限 3，已用 2，只能再放 1 次
	if reason, _, _ := quota.Consume(ctx, deviceB, limits); reason != service.GuestTrialQuotaAllowed {
		t.Fatalf("second device on same IP should get one more: %v", reason)
	}
	if reason, _, _ := quota.Consume(ctx, deviceB, limits); reason != service.GuestTrialQuotaIPFull {
		t.Fatalf("ip limit not enforced: %v", reason)
	}
	// 全站上限 4，已用 3
	if reason, _, _ := quota.Consume(ctx, deviceC, limits); reason != service.GuestTrialQuotaAllowed {
		t.Fatalf("new IP should still pass once: %v", reason)
	}
	if reason, _, _ := quota.Consume(ctx, deviceC, limits); reason != service.GuestTrialQuotaGlobalFull {
		t.Fatalf("global limit not enforced: %v", reason)
	}
	if used, err := quota.VisitorUsed(ctx, deviceA); err != nil || used != 2 {
		t.Fatalf("visitor used = %d, %v", used, err)
	}
}

func TestGuestTrialQuotaRejectedRequestsDoNotIncrement(t *testing.T) {
	quota, _ := newGuestTrialQuotaForTest(t)
	ctx := context.Background()
	keys := service.GuestTrialQuotaKeys{Day: "20260923", Device: "device-a", IP: "1.1.1.1"}
	limits := service.GuestTrialQuotaLimits{PerVisitor: 1, PerIP: 3, Global: 10}
	_, _, _ = quota.Consume(ctx, keys, limits)
	for i := 0; i < 3; i++ {
		_, _, _ = quota.Consume(ctx, keys, limits)
	}
	other := service.GuestTrialQuotaKeys{Day: "20260923", Device: "device-b", IP: "1.1.1.1"}
	// 被拒绝的 3 次不应该计入 IP 计数，否则同 IP 的其他设备会被连带拒绝
	if reason, _, _ := quota.Consume(ctx, other, service.GuestTrialQuotaLimits{PerVisitor: 5, PerIP: 2, Global: 10}); reason != service.GuestTrialQuotaAllowed {
		t.Fatalf("rejected attempts leaked into the IP counter: %v", reason)
	}
}

func TestGuestTrialQuotaNewDayStartsFresh(t *testing.T) {
	quota, _ := newGuestTrialQuotaForTest(t)
	ctx := context.Background()
	limits := service.GuestTrialQuotaLimits{PerVisitor: 1, PerIP: 1, Global: 1}
	today := service.GuestTrialQuotaKeys{Day: "20260923", Device: "device-a", IP: "1.1.1.1"}
	tomorrow := service.GuestTrialQuotaKeys{Day: "20260924", Device: "device-a", IP: "1.1.1.1"}
	_, _, _ = quota.Consume(ctx, today, limits)
	if reason, _, _ := quota.Consume(ctx, tomorrow, limits); reason != service.GuestTrialQuotaAllowed {
		t.Fatalf("a new trial day must reset all counters: %v", reason)
	}
}

func TestGuestTrialQuotaVerifiedMarkExpires(t *testing.T) {
	quota, server := newGuestTrialQuotaForTest(t)
	ctx := context.Background()
	if ok, err := quota.IsVerified(ctx, "device|ip"); err != nil || ok {
		t.Fatalf("unexpected verified state before marking: %v %v", ok, err)
	}
	if err := quota.MarkVerified(ctx, "device|ip", time.Hour); err != nil {
		t.Fatal(err)
	}
	if ok, _ := quota.IsVerified(ctx, "device|ip"); !ok {
		t.Fatal("verified mark missing")
	}
	server.FastForward(2 * time.Hour)
	if ok, _ := quota.IsVerified(ctx, "device|ip"); ok {
		t.Fatal("verified mark should expire")
	}
}
