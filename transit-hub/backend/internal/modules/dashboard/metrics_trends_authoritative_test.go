package dashboard

import (
	"math"
	"testing"
	"time"
)

func TestAuthoritativeTrendPointsOmitsLegacyLocalCostSnapshots(t *testing.T) {
	date := time.Date(2026, time.August, 13, 0, 0, 0, 0, time.Local)
	legacyStatuses := []CostStatus{
		CostStatusComplete,
		CostStatusPartial,
		CostStatusInvalidRate,
		CostStatusCachedFallback,
		CostStatusMissing,
	}

	snapshots := make([]DailySnapshot, 0, len(legacyStatuses)+1)
	for _, status := range legacyStatuses {
		snapshots = append(snapshots, DailySnapshot{
			Date:             date,
			TodayProfitUSD:   999,
			TodayPurchaseCNY: 1,
			CostStatus:       status,
			USDToCNYRate:     1,
		})
	}
	snapshots = append(snapshots, DailySnapshot{
		Date:             date,
		TodayProfitUSD:   33.58,
		TodayPurchaseCNY: 19.85,
		CostStatus:       CostStatusAdminAccounted,
		USDToCNYRate:     1,
	})

	points := authoritativeTrendPoints(snapshots)
	if len(points) != 1 {
		t.Fatalf("authoritative trend point count = %d, want 1", len(points))
	}
	point := points[0]
	if point.CostStatus != CostStatusAdminAccounted {
		t.Fatalf("cost status = %q, want %q", point.CostStatus, CostStatusAdminAccounted)
	}
	if point.TodayProfitCNY.Amount != 33.58 {
		t.Fatalf("revenue = %v, want 33.58", point.TodayProfitCNY.Amount)
	}
	if point.TodayPurchaseCNY.Amount != 19.85 {
		t.Fatalf("cost = %v, want 19.85", point.TodayPurchaseCNY.Amount)
	}
	if math.Abs(point.NetProfitCNY.Amount-13.73) > 1e-9 {
		t.Fatalf("net profit = %v, want 13.73", point.NetProfitCNY.Amount)
	}
}
