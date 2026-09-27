//go:build integration || localpg

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// runUsageBillingRelayContract：主从分流入账在扣费的同一事务里认领凭证、消耗租约、同步冻结额（设计 5.2、4.3）。
func runUsageBillingRelayContract(t *testing.T, db *sql.DB) {
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	var userID, keyID, nodeID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users (email, password_hash, balance) VALUES ($1, 'x', 10) RETURNING id`,
		fmt.Sprintf("relay-settle-%d@test.local", stamp)).Scan(&userID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO api_keys (user_id, key, name) VALUES ($1, $2, 'k') RETURNING id`,
		userID, fmt.Sprintf("sk-relay-settle-%d", stamp)).Scan(&keyID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO relay_nodes (identity_public_key, identity_fingerprint) VALUES ('00', $1) RETURNING id`,
		fmt.Sprintf("settle-node-%d", stamp)).Scan(&nodeID))

	leases := NewRelayLeaseRepository(db)
	var balanceLease, otherKeyLease int64
	require.NoError(t, leases.WithUser(ctx, userID, func(tx master.LeaseTx) error {
		l, err := tx.Grant(ctx, master.LeaseKey{UserID: userID, NodeID: nodeID, LeaseScope: master.LeaseScope{Dimension: service.QuotaDimBalance}},
			master.ToMicros(5), time.Now().Add(10*time.Minute), "e1", time.Now())
		if err != nil {
			return err
		}
		balanceLease = l.ID
		// 另一个 Key 的额度租约：这次请求用不到，不能被消耗。
		o, err := tx.Grant(ctx, master.LeaseKey{UserID: userID, NodeID: nodeID, LeaseScope: master.LeaseScope{Dimension: service.QuotaDimAPIKeyTotal, ScopeID: keyID + 1000}},
			master.ToMicros(2), time.Now().Add(10*time.Minute), "e1", time.Now())
		otherKeyLease = o.ID
		return err
	}))

	read := func() (balance, reserved, leaseGranted, otherGranted master.Micros) {
		require.NoError(t, db.QueryRowContext(ctx, `SELECT (balance * 100000000)::bigint, (relay_reserved_balance * 100000000)::bigint FROM users WHERE id = $1`, userID).Scan(&balance, &reserved))
		require.NoError(t, db.QueryRowContext(ctx, `SELECT (granted * 100000000)::bigint FROM relay_quota_leases WHERE id = $1`, balanceLease).Scan(&leaseGranted))
		require.NoError(t, db.QueryRowContext(ctx, `SELECT (granted * 100000000)::bigint FROM relay_quota_leases WHERE id = $1`, otherKeyLease).Scan(&otherGranted))
		return
	}
	repo := NewUsageBillingRepository(nil, db)
	relay := func() *service.RelaySettlement {
		return &service.RelaySettlement{VoucherID: uuid.NewString(), IssuedAt: time.Now().UTC().Truncate(time.Microsecond), NodeID: nodeID, UserID: userID, APIKeyID: keyID}
	}
	command := func(requestID string, cost float64, r *service.RelaySettlement) *service.UsageBillingCommand {
		return &service.UsageBillingCommand{RequestID: requestID, APIKeyID: keyID, UserID: userID, Model: "gpt-5.1", BalanceCost: cost, Relay: r}
	}

	// 入账：扣余额、租约减实扣、冻结额同步减；另一个 Key 的租约不动。
	r1 := relay()
	res, err := repo.Apply(ctx, command("req-1", 1.25, r1))
	require.NoError(t, err)
	require.True(t, res.Applied)
	require.True(t, r1.Handled)
	require.False(t, r1.AlreadySettled)
	require.Equal(t, []service.RelayLeaseConsumption{{LeaseID: balanceLease, Dimension: service.QuotaDimBalance, Amount: master.ToMicros(1.25)}}, r1.Consumed)
	balance, reserved, granted, other := read()
	require.Equal(t, master.ToMicros(8.75), balance)
	require.Equal(t, master.ToMicros(3.75), reserved, "spendable balance does not change: the money moved from locked to spent")
	require.Equal(t, master.ToMicros(3.75), granted)
	require.Equal(t, master.ToMicros(2), other)

	// 重发同一张凭证：不再扣，返回当时的消耗。
	replay := &service.RelaySettlement{VoucherID: r1.VoucherID, IssuedAt: r1.IssuedAt, NodeID: nodeID, UserID: userID, APIKeyID: keyID}
	res, err = repo.Apply(ctx, command("req-1-retry", 1.25, replay))
	require.NoError(t, err)
	require.False(t, res.Applied)
	require.True(t, replay.Handled)
	require.True(t, replay.AlreadySettled)
	require.Equal(t, r1.Consumed, replay.Consumed)
	b2, _, _, _ := read()
	require.Equal(t, balance, b2, "charged once")

	// 另一张凭证撞上同一个扣费请求 ID（与单机一样不重复扣）：凭证记成零消耗，重发也返回零。
	r3 := relay()
	res, err = repo.Apply(ctx, command("req-1", 1.25, r3))
	require.NoError(t, err)
	require.False(t, res.Applied)
	require.True(t, r3.Handled)
	require.Empty(t, r3.Consumed)
	again := &service.RelaySettlement{VoucherID: r3.VoucherID, IssuedAt: r3.IssuedAt, NodeID: nodeID, UserID: userID, APIKeyID: keyID}
	require.NoError(t, repo.(*usageBillingRepository).RecordRelayVoucher(ctx, again))
	require.True(t, again.AlreadySettled)
	require.Empty(t, again.Consumed)

	// 实扣超过租约剩下的：租约扣到零，超出部分照扣余额（超支，设计 4.5）。
	r4 := relay()
	res, err = repo.Apply(ctx, command("req-4", 5, r4))
	require.NoError(t, err)
	require.True(t, res.Applied)
	require.Equal(t, master.ToMicros(3.75), r4.Consumed[0].Amount)
	balance, reserved, granted, _ = read()
	require.Equal(t, master.ToMicros(3.75), balance)
	require.Zero(t, reserved, "the balance lease is used up; key-quota leases do not count toward reserved balance")
	require.Zero(t, granted)

	// 零消耗的凭证记录（入账没走到扣费事务时）。
	r5 := relay()
	require.NoError(t, repo.(*usageBillingRepository).RecordRelayVoucher(ctx, r5))
	require.True(t, r5.Handled)
	require.False(t, r5.AlreadySettled)
}
