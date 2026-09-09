package service

import (
	"context"
	"sort"
	"time"
)

// LatencyCompensationUserSummary is one user's share of a latency
// compensation preview or payout: how much they were actually charged for
// slow requests in the window versus what those requests cost the platform,
// and the difference (never negative — a request that cost more than it was
// billed for isn't clawed back, it just contributes nothing to Compensation).
type LatencyCompensationUserSummary struct {
	UserID       int64   `json:"user_id"`
	Email        string  `json:"email"`
	Requests     int     `json:"requests"`
	ActualCost   float64 `json:"actual_cost"`
	AccountCost  float64 `json:"account_cost"`
	Compensation float64 `json:"compensation"`
}

// LatencyCompensationSummary is the full preview/payout result for a time
// window and threshold: per-user breakdown plus totals, in the same shape so
// the frontend can render a preview and a "just paid" receipt with one
// component.
type LatencyCompensationSummary struct {
	From              time.Time                        `json:"from"`
	To                time.Time                        `json:"to"`
	ThresholdMs       int                              `json:"threshold_ms"`
	ProfitRatio       float64                          `json:"profit_ratio"`
	Users             []LatencyCompensationUserSummary `json:"users"`
	TotalRequests     int                              `json:"total_requests"`
	TotalActualCost   float64                          `json:"total_actual_cost"`
	TotalAccountCost  float64                          `json:"total_account_cost"`
	TotalCompensation float64                          `json:"total_compensation"`
}

// SummarizeLatencyCompensationRows groups pending rows by user and computes
// each user's compensation as (actual_cost - account_cost) * profitRatio,
// floored at 0. profitRatio is how much of the margin on a slow request gets
// refunded — 1 means "we make no profit on this request", an admin-set value
// below 1 keeps the platform a cut. Shared by preview (read-only) and apply
// (which additionally pays out and marks the same rows compensated) so the
// two can never disagree about who owes what.
func SummarizeLatencyCompensationRows(rows []LatencyCompensationRow, from, to time.Time, thresholdMs int, profitRatio float64) *LatencyCompensationSummary {
	byUser := make(map[int64]*LatencyCompensationUserSummary)
	order := make([]int64, 0)
	summary := &LatencyCompensationSummary{From: from, To: to, ThresholdMs: thresholdMs, ProfitRatio: profitRatio}

	for _, row := range rows {
		u, ok := byUser[row.UserID]
		if !ok {
			u = &LatencyCompensationUserSummary{UserID: row.UserID, Email: row.Email}
			byUser[row.UserID] = u
			order = append(order, row.UserID)
		}
		u.Requests++
		u.ActualCost += row.ActualCost
		u.AccountCost += row.AccountCost

		summary.TotalRequests++
		summary.TotalActualCost += row.ActualCost
		summary.TotalAccountCost += row.AccountCost
	}

	summary.Users = make([]LatencyCompensationUserSummary, 0, len(order))
	for _, userID := range order {
		u := byUser[userID]
		margin := u.ActualCost - u.AccountCost
		if margin < 0 {
			margin = 0
		}
		u.Compensation = margin * profitRatio
		summary.TotalCompensation += u.Compensation
		summary.Users = append(summary.Users, *u)
	}
	// Highest compensation first — this is the list an admin scans to see who
	// was worst affected, not a log of who happened to show up first.
	sort.SliceStable(summary.Users, func(i, j int) bool {
		return summary.Users[i].Compensation > summary.Users[j].Compensation
	})
	return summary
}

// PreviewLatencyCompensation reports what a payout over [from, to) at
// thresholdMs would look like, without touching any balance or marking any
// row. Safe to call repeatedly (e.g. while an admin adjusts the date range).
func (s *UsageService) PreviewLatencyCompensation(ctx context.Context, from, to time.Time, thresholdMs int, profitRatio float64) (*LatencyCompensationSummary, error) {
	rows, err := s.usageRepo.FetchPendingLatencyCompensationRows(ctx, from, to, thresholdMs)
	if err != nil {
		return nil, err
	}
	return SummarizeLatencyCompensationRows(rows, from, to, thresholdMs, profitRatio), nil
}

// FetchPendingLatencyCompensationRows exposes the raw rows for the handler,
// which pays each user via AdminService (outside this package) and then
// marks exactly those row IDs compensated — see the handler for why paying
// and marking must share one row set instead of the summary and a fresh mark
// query.
func (s *UsageService) FetchPendingLatencyCompensationRows(ctx context.Context, from, to time.Time, thresholdMs int) ([]LatencyCompensationRow, error) {
	return s.usageRepo.FetchPendingLatencyCompensationRows(ctx, from, to, thresholdMs)
}

// MarkLatencyCompensated stamps latency_compensated_at on the given row IDs
// so a later preview/apply over an overlapping window skips them.
func (s *UsageService) MarkLatencyCompensated(ctx context.Context, ids []int64) error {
	return s.usageRepo.MarkLatencyCompensated(ctx, ids)
}

// UnmarkLatencyCompensated reopens every row in [from, to) at or above
// thresholdMs for compensation again — the revoke counterpart to
// MarkLatencyCompensated.
func (s *UsageService) UnmarkLatencyCompensated(ctx context.Context, from, to time.Time, thresholdMs int) error {
	return s.usageRepo.UnmarkLatencyCompensated(ctx, from, to, thresholdMs)
}
