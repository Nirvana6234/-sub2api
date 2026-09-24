//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPrepareUsageLogInsert_AccountSourceWiring(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "own is kept", in: service.UsageLogAccountSourceOwn, want: service.UsageLogAccountSourceOwn},
		{name: "room is kept", in: service.UsageLogAccountSourceRoom, want: service.UsageLogAccountSourceRoom},
		{name: "empty becomes pool", in: "", want: service.UsageLogAccountSourcePool},
		{name: "unknown becomes pool", in: "bogus", want: service.UsageLogAccountSourcePool},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepared := prepareUsageLogInsert(&service.UsageLog{
				UserID:        3,
				APIKeyID:      39,
				AccountID:     795,
				RequestID:     "req-account-source",
				Model:         "gpt-6-astra",
				AccountSource: tc.in,
				CreatedAt:     time.Now().UTC(),
			})
			require.Len(t, prepared.args, len(usageLogInsertArgTypes))
			require.Equal(t, tc.want, prepared.args[len(prepared.args)-1])
			require.Equal(t, "text", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-1])
		})
	}
}

func TestUsageLogRepositoryListWithFiltersAccountSource(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	filters := usagestats.UsageLogFilters{UserID: 3, AccountSource: service.UsageLogAccountSourcePool, ExactTotal: true}

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM usage_logs WHERE user_id = \$1 AND account_source = \$2`).
		WithArgs(int64(3), service.UsageLogAccountSourcePool).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery(`SELECT .* FROM usage_logs WHERE user_id = \$1 AND account_source = \$2 ORDER BY id DESC LIMIT \$3 OFFSET \$4`).
		WithArgs(int64(3), service.UsageLogAccountSourcePool, 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	logs, page, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 20}, filters)
	require.NoError(t, err)
	require.Empty(t, logs)
	require.NotNil(t, page)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryGetStatsWithFiltersAccountSource(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	filters := usagestats.UsageLogFilters{UserID: 3, AccountID: 795, AccountSource: service.UsageLogAccountSourceOwn}

	mock.ExpectQuery(`(?s)FROM usage_logs\s+WHERE user_id = \$1 AND account_id = \$2 AND account_source = \$3.*GROUP BY GROUPING SETS`).
		WithArgs(int64(3), int64(795), service.UsageLogAccountSourceOwn).
		WillReturnRows(sqlmock.NewRows([]string{
			"inbound_grouped", "upstream_grouped", "inbound_endpoint", "upstream_endpoint",
			"requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
			"cost", "actual_cost", "account_cost", "avg_duration_ms",
		}).AddRow(1, 1, nil, nil, int64(5), int64(10), int64(4), int64(0), int64(6), 0.5, 0.05, 0.5, 30.0))

	stats, err := repo.GetStatsWithFilters(context.Background(), filters)
	require.NoError(t, err)
	require.Equal(t, int64(5), stats.TotalRequests)
	require.NoError(t, mock.ExpectationsWereMet())
}
