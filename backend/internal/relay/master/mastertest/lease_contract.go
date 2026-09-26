package mastertest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// LeaseHarness 由各 LeaseStore 实现提供。
type LeaseHarness struct {
	// New 返回一个空的存储。
	New func(t *testing.T) master.LeaseStore
	// NewUser 建一个用户，返回 ID。
	NewUser func(t *testing.T, s master.LeaseStore) int64
	// NewNode 建一台节点，返回 ID。
	NewNode func(t *testing.T, s master.LeaseStore) int64
	// DeleteUser 删除（软删除）一个用户。
	DeleteUser func(t *testing.T, s master.LeaseStore, userID int64)
	// Reserved 读 users.relay_reserved_balance（微单位）。
	Reserved func(t *testing.T, s master.LeaseStore, userID int64) master.Micros
}

var errRollback = errors.New("rollback")

// RunLeaseStore 跑租约存储的契约测试。
func RunLeaseStore(t *testing.T, h LeaseHarness) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	later := now.Add(10 * time.Minute)
	balance := master.LeaseScope{Dimension: service.QuotaDimBalance}
	daily := master.LeaseScope{Dimension: service.QuotaDimSubscriptionDaily, ScopeID: 5}
	platform := master.LeaseScope{Dimension: service.QuotaDimPlatformDaily, ScopeKey: "anthropic"}

	t.Run("grant adds to the one active lease per key and moves the reserve", func(t *testing.T) {
		s := h.New(t)
		u, n1, n2 := h.NewUser(t, s), h.NewNode(t, s), h.NewNode(t, s)
		var first, second *master.Lease
		require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			var err error
			first, err = tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n1, LeaseScope: balance}, 499_999_999, later, "e1", now)
			if err != nil {
				return err
			}
			second, err = tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n1, LeaseScope: balance}, 1, later.Add(time.Minute), "e2", now)
			if err != nil {
				return err
			}
			if _, err := tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n2, LeaseScope: balance}, 250_000_000, later, "e2", now); err != nil {
				return err
			}
			if _, err := tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n1, LeaseScope: daily}, 7, later, "e2", now); err != nil {
				return err
			}
			_, err = tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n1, LeaseScope: platform}, 9, later, "e2", now)
			return err
		}))
		require.Equal(t, first.ID, second.ID, "a top-up adds to the same lease")
		require.Equal(t, master.Micros(500_000_000), second.Granted, "exact to the last unit")
		require.Equal(t, "e2", second.MasterEpoch)
		require.WithinDuration(t, later.Add(time.Minute), second.ExpiresAt, time.Millisecond)
		require.Equal(t, master.Micros(750_000_000), h.Reserved(t, s, u), "only the balance dimension moves the reserve")

		var active []*master.Lease
		require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			var err error
			active, err = tx.Active(ctx)
			return err
		}))
		require.Len(t, active, 4)
		got := map[master.LeaseScope]master.Micros{}
		for _, l := range active {
			got[l.LeaseScope] += l.Granted
			require.Equal(t, master.LeaseActive, l.Status)
		}
		require.Equal(t, master.Micros(9), got[platform])
		require.Equal(t, master.Micros(7), got[daily])

		reserved, err := s.ReservedBalances(ctx)
		require.NoError(t, err)
		require.Equal(t, master.Micros(750_000_000), reserved[u])
	})

	t.Run("reduce and close return money and keep the reserve in step", func(t *testing.T) {
		s := h.New(t)
		u, n := h.NewUser(t, s), h.NewNode(t, s)
		var lease *master.Lease
		require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			var err error
			lease, err = tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n, LeaseScope: balance}, 300, later, "e", now)
			return err
		}))
		require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			if _, err := tx.Reduce(ctx, lease.ID, 301, now); !errors.Is(err, master.ErrLeaseAmount) {
				return errors.New("reducing more than granted must fail")
			}
			l, err := tx.Reduce(ctx, lease.ID, 100, now)
			if err != nil {
				return err
			}
			if l.Granted != 200 {
				return errors.New("granted should be 200")
			}
			return nil
		}))
		require.Equal(t, master.Micros(200), h.Reserved(t, s, u))

		var returned master.Micros
		require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			var err error
			returned, err = tx.Close(ctx, lease.ID, master.LeaseVoided, "admin_reclaim", now)
			return err
		}))
		require.Equal(t, master.Micros(200), returned)
		require.Equal(t, master.Micros(0), h.Reserved(t, s, u))
		err := s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			_, err := tx.Close(ctx, lease.ID, master.LeaseVoided, "again", now)
			return err
		})
		require.ErrorIs(t, err, master.ErrLeaseNotFound, "a closed lease cannot be closed or used again")

		// 关闭后同一个键可以再开一份新租约。
		var again *master.Lease
		require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			var err error
			again, err = tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n, LeaseScope: balance}, 5, later, "e", now)
			return err
		}))
		require.NotEqual(t, lease.ID, again.ID)
		require.Equal(t, master.Micros(5), again.Granted)
	})

	t.Run("returns apply only the part above the persisted cumulative total", func(t *testing.T) {
		s := h.New(t)
		u, n := h.NewUser(t, s), h.NewNode(t, s)
		var lease *master.Lease
		require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			var err error
			lease, err = tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n, LeaseScope: balance}, 500, later, "e", now)
			return err
		}))
		apply := func(total master.Micros) master.Micros {
			var d master.Micros
			require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
				var err error
				d, err = tx.ApplyReturned(ctx, lease.ID, total, now)
				return err
			}))
			return d
		}
		require.Equal(t, master.Micros(200), apply(200))
		require.Equal(t, master.Micros(0), apply(200), "the same total twice applies once")
		require.Equal(t, master.Micros(0), apply(100), "a lower total applies nothing")
		require.Equal(t, master.Micros(50), apply(250), "a higher total applies the difference")
		require.Equal(t, master.Micros(250), h.Reserved(t, s, u))
		require.Equal(t, master.Micros(250), apply(900), "never more than is still locked")
		require.Equal(t, master.Micros(0), h.Reserved(t, s, u))
		active, err := s.ListActiveByNode(ctx, n)
		require.NoError(t, err)
		require.Len(t, active, 1)
		require.Equal(t, master.Micros(900), active[0].ReturnedTotal, "the larger total is kept")
		require.Equal(t, master.Micros(0), active[0].Granted)
	})

	t.Run("a failed transaction changes nothing", func(t *testing.T) {
		s := h.New(t)
		u, n := h.NewUser(t, s), h.NewNode(t, s)
		err := s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			if _, err := tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n, LeaseScope: balance}, 100, later, "e", now); err != nil {
				return err
			}
			return errRollback
		})
		require.ErrorIs(t, err, errRollback)
		require.Equal(t, master.Micros(0), h.Reserved(t, s, u))
		active, err := s.ListActiveByNode(ctx, n)
		require.NoError(t, err)
		require.Empty(t, active)
	})

	t.Run("renew extends and records the latest use", func(t *testing.T) {
		s := h.New(t)
		u, n := h.NewUser(t, s), h.NewNode(t, s)
		var lease *master.Lease
		require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			var err error
			lease, err = tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n, LeaseScope: daily}, 10, later, "e", now)
			return err
		}))
		used := now.Add(time.Minute)
		older := now
		require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			if _, err := tx.Renew(ctx, lease.ID, later.Add(time.Hour), &used, now); err != nil {
				return err
			}
			if _, err := tx.Renew(ctx, lease.ID, later.Add(2*time.Hour), &older, now); err != nil {
				return err
			}
			l, err := tx.Renew(ctx, lease.ID, later.Add(3*time.Hour), nil, now)
			if err != nil {
				return err
			}
			lease = l
			return nil
		}))
		require.WithinDuration(t, later.Add(3*time.Hour), lease.ExpiresAt, time.Millisecond)
		require.NotNil(t, lease.LastUsedAt)
		require.WithinDuration(t, used, *lease.LastUsedAt, time.Millisecond, "the last-used time never moves backwards")
	})

	t.Run("listing by node and by expiry", func(t *testing.T) {
		s := h.New(t)
		u, n1, n2 := h.NewUser(t, s), h.NewNode(t, s), h.NewNode(t, s)
		require.NoError(t, s.WithUser(ctx, u, func(tx master.LeaseTx) error {
			if _, err := tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n1, LeaseScope: balance}, 1, now.Add(-time.Second), "e", now); err != nil {
				return err
			}
			_, err := tx.Grant(ctx, master.LeaseKey{UserID: u, NodeID: n2, LeaseScope: balance}, 2, later, "e", now)
			return err
		}))
		byNode, err := s.ListActiveByNode(ctx, n1)
		require.NoError(t, err)
		require.Len(t, byNode, 1)
		require.Equal(t, master.Micros(1), byNode[0].Granted)
		expired, err := s.ListExpired(ctx, now, 10)
		require.NoError(t, err)
		require.Len(t, expired, 1)
		require.Equal(t, n1, expired[0].NodeID)
	})

	t.Run("a deleted user has no leases to change", func(t *testing.T) {
		s := h.New(t)
		u := h.NewUser(t, s)
		h.DeleteUser(t, s, u)
		err := s.WithUser(ctx, u, func(master.LeaseTx) error { return nil })
		require.ErrorIs(t, err, master.ErrLeaseUserNotFound)
	})
}
