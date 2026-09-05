//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestListRecentPeerGroupFirstTokenSamplesPartitionsSharedSamplesByUser(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	since := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE user_rn <= 3")).
		WithArgs(sqlmock.AnyArg(), "gpt-test", since, 20).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "user_id", "first_token_ms"}).
			AddRow(int64(20), int64(7), int64(1_000)).
			AddRow(int64(20), int64(8), int64(1_100)))

	metrics, err := repo.ListRecentPeerGroupFirstTokenSamples(context.Background(), []int64{20}, "gpt-test", since, 20)

	require.NoError(t, err)
	require.Equal(t, []int64{1_000}, metrics[20][7])
	require.Equal(t, []int64{1_100}, metrics[20][8])
	require.NoError(t, mock.ExpectationsWereMet())
}
