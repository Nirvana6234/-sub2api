package service

import (
	"testing"
	"time"
)

func TestSummarizeLatencyCompensationRowsGroupsByUserAndFloorsAtZero(t *testing.T) {
	from := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)

	rows := []LatencyCompensationRow{
		{ID: 1, UserID: 24, Email: "a@example.com", ActualCost: 10, AccountCost: 6},
		{ID: 2, UserID: 24, Email: "a@example.com", ActualCost: 2, AccountCost: 1},
		// A row whose account cost exceeds what was billed must not turn into
		// a negative compensation that offsets this user's other rows —
		// 2026-09-08's gpt-6-astra requests showed this actually happens
		// (list-price cost above the discounted price charged to the user).
		{ID: 3, UserID: 98, Email: "b@example.com", ActualCost: 5, AccountCost: 8},
	}

	summary := SummarizeLatencyCompensationRows(rows, from, to, 30000, 1)

	if summary.TotalRequests != 3 {
		t.Fatalf("total requests = %d, want 3", summary.TotalRequests)
	}
	if got, want := summary.TotalCompensation, 5.0; got != want {
		t.Fatalf("total compensation = %v, want %v", got, want)
	}
	if len(summary.Users) != 2 {
		t.Fatalf("users = %d, want 2", len(summary.Users))
	}

	byUser := make(map[int64]LatencyCompensationUserSummary, len(summary.Users))
	for _, u := range summary.Users {
		byUser[u.UserID] = u
	}

	u24 := byUser[24]
	if u24.Requests != 2 {
		t.Fatalf("user 24 requests = %d, want 2", u24.Requests)
	}
	if u24.ActualCost != 12 || u24.AccountCost != 7 || u24.Compensation != 5 {
		t.Fatalf("user 24 = %+v, want actual=12 account=7 compensation=5", u24)
	}

	u98 := byUser[98]
	if u98.Compensation != 0 {
		t.Fatalf("user 98 compensation = %v, want 0 (account_cost > actual_cost must floor, not go negative)", u98.Compensation)
	}
}

func TestSummarizeLatencyCompensationRowsAppliesProfitRatio(t *testing.T) {
	from := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	rows := []LatencyCompensationRow{
		{ID: 1, UserID: 24, ActualCost: 10, AccountCost: 6},
	}

	// An admin choosing to keep half the margin (ratio 0.5) must halve the
	// payout, not just apply the ratio to the total for display.
	summary := SummarizeLatencyCompensationRows(rows, from, to, 30000, 0.5)
	if summary.TotalCompensation != 2 {
		t.Fatalf("total compensation at ratio 0.5 = %v, want 2 (half of the 4 margin)", summary.TotalCompensation)
	}

	zero := SummarizeLatencyCompensationRows(rows, from, to, 30000, 0)
	if zero.TotalCompensation != 0 {
		t.Fatalf("total compensation at ratio 0 = %v, want 0", zero.TotalCompensation)
	}
}

// Admins scan this list to see who was worst affected — it must read like a
// leaderboard (highest compensation first), not the order rows happened to
// arrive in from the DB (first slow request wins otherwise, which has
// nothing to do with impact).
func TestSummarizeLatencyCompensationRowsSortsDescendingByCompensation(t *testing.T) {
	from := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	rows := []LatencyCompensationRow{
		{ID: 1, UserID: 1, ActualCost: 1, AccountCost: 0.5}, // compensation 0.5, arrives first
		{ID: 2, UserID: 2, ActualCost: 10, AccountCost: 1},  // compensation 9, arrives second
		{ID: 3, UserID: 3, ActualCost: 3, AccountCost: 1},   // compensation 2, arrives third
	}

	summary := SummarizeLatencyCompensationRows(rows, from, to, 30000, 1)

	if len(summary.Users) != 3 {
		t.Fatalf("users = %d, want 3", len(summary.Users))
	}
	gotOrder := []int64{summary.Users[0].UserID, summary.Users[1].UserID, summary.Users[2].UserID}
	wantOrder := []int64{2, 3, 1}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("user order = %v, want %v (descending by compensation)", gotOrder, wantOrder)
		}
	}
}

func TestSummarizeLatencyCompensationRowsEmpty(t *testing.T) {
	from := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)

	summary := SummarizeLatencyCompensationRows(nil, from, to, 30000, 1)

	if summary.TotalRequests != 0 || summary.TotalCompensation != 0 {
		t.Fatalf("empty rows should produce a zero summary, got %+v", summary)
	}
	if len(summary.Users) != 0 {
		t.Fatalf("empty rows should produce no users, got %d", len(summary.Users))
	}
}
