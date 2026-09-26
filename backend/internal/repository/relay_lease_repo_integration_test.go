//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/master/mastertest"
	"github.com/stretchr/testify/require"
)

// SQL 实现跑与内存实现相同的租约契约测试（internal/relay/master/mastertest）。
func TestRelayLeaseRepositoryContract(t *testing.T) {
	mastertest.RunLeaseStore(t, relayLeaseHarness(integrationDB))
}

// 并发申请在真实行锁下也不超额。
func TestRelayQuotaNeverOverGrantsOnPostgres(t *testing.T) {
	lat := mastertest.RunQuotaNeverOverGrants(t, relayLeaseHarness(integrationDB))
	t.Logf("acquire latency p50=%s p99=%s (n=%d)", lat[len(lat)/2], lat[len(lat)*99/100], len(lat))
}

var relayLeaseSeq atomic.Int64

func relayLeaseHarness(db *sql.DB) mastertest.LeaseHarness {
	ctx := context.Background()
	return mastertest.LeaseHarness{
		New: func(t *testing.T) master.LeaseStore {
			_, err := db.ExecContext(ctx, `TRUNCATE relay_quota_leases RESTART IDENTITY`)
			require.NoError(t, err)
			return NewRelayLeaseRepository(db)
		},
		NewUser: func(t *testing.T, _ master.LeaseStore) int64 {
			var id int64
			require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`,
				fmt.Sprintf("lease-%d-%d@test.local", time.Now().UnixNano(), relayLeaseSeq.Add(1))).Scan(&id))
			return id
		},
		NewNode: func(t *testing.T, _ master.LeaseStore) int64 {
			n := relayLeaseSeq.Add(1)
			var id int64
			require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO relay_nodes (identity_public_key, identity_fingerprint)
				VALUES ('00', $1) RETURNING id`, fmt.Sprintf("lease-node-%d-%d", time.Now().UnixNano(), n)).Scan(&id))
			return id
		},
		DeleteUser: func(t *testing.T, _ master.LeaseStore, userID int64) {
			_, err := db.ExecContext(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, userID)
			require.NoError(t, err)
		},
		Reserved: func(t *testing.T, _ master.LeaseStore, userID int64) master.Micros {
			var v master.Micros
			require.NoError(t, db.QueryRowContext(ctx, `SELECT (relay_reserved_balance * 100000000)::bigint FROM users WHERE id = $1`, userID).Scan(&v))
			return v
		},
	}
}
