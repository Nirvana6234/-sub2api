package dashboard

import (
	"testing"
	"time"
)

func TestDashboardDateUsesShanghaiBoundary(t *testing.T) {
	// 17:00 UTC is already the next calendar day in Asia/Shanghai. This is
	// the boundary that previously caused yesterday to disappear from trends.
	now := time.Date(2026, time.August, 4, 17, 0, 0, 0, time.UTC)
	if got, want := dashboardDate(now), "2026-08-05"; got != want {
		t.Fatalf("dashboardDate(%s) = %s, want %s", now, got, want)
	}
}
