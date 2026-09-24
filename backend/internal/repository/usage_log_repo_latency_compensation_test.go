package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// FetchPendingLatencyCompensationRows must exclude rows served by an
// apikey-type contributed account (self-use only, see
// contribution_room_routing_repo.go) so the platform never subsidizes a
// contributor's own usage of their own account.
func TestFetchPendingLatencyCompensationRowsExcludesAPIKeyContributions(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	rows := sqlmock.NewRows([]string{"id", "user_id", "email", "first_token_ms", "actual_cost", "account_cost"}).
		AddRow(int64(1), int64(10), "user@example.com", int64(25000), 0.5, 0.3)

	mock.ExpectQuery(`(?s)FROM usage_logs ul.*LEFT JOIN accounts acc ON acc\.id = ul\.account_id.*NOT \(\s*COALESCE\(acc\.type, ''\) = 'apikey'\s*AND COALESCE\(acc\.extra ->> 'import_source', ''\) = 'user_contribution'\s*\).*ORDER BY ul\.id ASC`).
		WithArgs(start, end, 20000).
		WillReturnRows(rows)

	result, err := repo.FetchPendingLatencyCompensationRows(context.Background(), start, end, 20000)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, int64(1), result[0].ID)
	require.Equal(t, int64(10), result[0].UserID)
	require.Equal(t, "user@example.com", result[0].Email)
	require.Equal(t, int64(25000), result[0].FirstTokenMs)
	require.NoError(t, mock.ExpectationsWereMet())
}
