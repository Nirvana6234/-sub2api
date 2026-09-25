package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestCachedActiveAccountCountsQueriesOnlyMissingGroups(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &groupRepository{sql: db}

	// Group 2 has no accounts, so the aggregate returns no row for it.
	mock.ExpectQuery("FROM account_groups ag").
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "total", "active", "rate_limited"}).AddRow(1, 3, 2, 1))

	counts, err := repo.cachedActiveAccountCounts(context.Background(), []int64{1, 2})
	require.NoError(t, err)
	require.Equal(t, groupAccountCounts{Total: 3, Active: 2, RateLimited: 1}, counts[1])
	require.Equal(t, groupAccountCounts{}, counts[2])

	// A second call within the TTL must not query again, including for the
	// group that had no accounts.
	counts, err = repo.cachedActiveAccountCounts(context.Background(), []int64{1, 2})
	require.NoError(t, err)
	require.EqualValues(t, 2, counts[1].Active)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCachedActiveAccountCountsReloadsExpiredGroups(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &groupRepository{sql: db}
	repo.activeCounts.entries = map[int64]cachedGroupAccountCounts{
		1: {counts: groupAccountCounts{Active: 9}, expiresAt: time.Now().Add(time.Minute)},
		2: {counts: groupAccountCounts{Active: 9}, expiresAt: time.Now().Add(-time.Second)},
	}

	mock.ExpectQuery("FROM account_groups ag").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "total", "active", "rate_limited"}).AddRow(2, 1, 1, 0))

	counts, err := repo.cachedActiveAccountCounts(context.Background(), []int64{1, 2})
	require.NoError(t, err)
	require.EqualValues(t, 9, counts[1].Active, "unexpired counts come from the cache")
	require.EqualValues(t, 1, counts[2].Active, "expired counts are reloaded")
	require.NoError(t, mock.ExpectationsWereMet())
}
