package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestGetAccountUsageGroupBreakdownUsesRecordedUsageGroup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 30)
	mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("FROM usage_logs ul")+`.*`+regexp.QuoteMeta("LEFT JOIN groups g ON g.id = ul.group_id")+`.*GROUP BY ul.group_id, g.name`).
		WithArgs(int64(42), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "group_name", "requests", "total_tokens", "standard_cost", "account_cost", "user_cost"}).
			AddRow(int64(19), "订阅", int64(8), int64(1200), 2.0, 1.2, 3.4).
			AddRow(int64(2), "plus", int64(3), int64(300), 0.5, 0.3, 0.8))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	items, err := repo.getAccountUsageGroupBreakdown(context.Background(), 42, start, end)

	require.NoError(t, err)
	require.Equal(t, []AccountUsageGroupBreakdown{
		{GroupID: 19, GroupName: "订阅", Requests: 8, TotalTokens: 1200, StandardCost: 2, AccountCost: 1.2, UserCost: 3.4},
		{GroupID: 2, GroupName: "plus", Requests: 3, TotalTokens: 300, StandardCost: 0.5, AccountCost: 0.3, UserCost: 0.8},
	}, items)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAccountWindowGroupBreakdownBatchUsesRecordedUsageGroup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("FROM usage_logs ul")+`.*`+regexp.QuoteMeta("WHERE ul.account_id = ANY($1) AND ul.created_at >= $2 AND ul.created_at < $3")+`.*`+regexp.QuoteMeta("GROUP BY ul.account_id, ul.group_id, g.name")).
		WithArgs(sqlmock.AnyArg(), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id", "group_name", "requests", "total_tokens", "standard_cost", "account_cost", "user_cost"}).
			AddRow(int64(42), int64(19), "订阅", int64(8), int64(1200), 2.0, 1.2, 3.4).
			AddRow(int64(42), int64(2), "plus", int64(3), int64(300), 0.5, 0.3, 0.8).
			AddRow(int64(7), int64(19), "订阅", int64(1), int64(50), 0.1, 0.05, 0.2))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	items, err := repo.GetAccountWindowGroupBreakdownBatch(context.Background(), []int64{42, 7}, start, end)

	require.NoError(t, err)
	require.Len(t, items[42], 2)
	require.Equal(t, int64(19), items[42][0].GroupID)
	require.Equal(t, int64(19), items[7][0].GroupID)
	require.NoError(t, mock.ExpectationsWereMet())
}
