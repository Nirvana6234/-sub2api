package master_test

import (
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/master/mastertest"
)

func TestMemoryLeaseStoreContract(t *testing.T) {
	var ids atomic.Int64
	mastertest.RunLeaseStore(t, mastertest.LeaseHarness{
		New:     func(*testing.T) master.LeaseStore { return master.NewMemoryLeaseStore() },
		NewUser: func(*testing.T, master.LeaseStore) int64 { return ids.Add(1) },
		NewNode: func(*testing.T, master.LeaseStore) int64 { return ids.Add(1) },
		DeleteUser: func(_ *testing.T, s master.LeaseStore, userID int64) {
			memLeases(s).DeleteUser(userID)
		},
		Reserved: func(_ *testing.T, s master.LeaseStore, userID int64) master.Micros {
			return memLeases(s).ReservedBalance(userID)
		},
	})
}

func TestMemoryQuotaNeverOverGrants(t *testing.T) {
	var ids atomic.Int64
	mastertest.RunQuotaNeverOverGrants(t, mastertest.LeaseHarness{
		New:     func(*testing.T) master.LeaseStore { return master.NewMemoryLeaseStore() },
		NewUser: func(*testing.T, master.LeaseStore) int64 { return ids.Add(1) },
		NewNode: func(*testing.T, master.LeaseStore) int64 { return ids.Add(1) },
		Reserved: func(_ *testing.T, s master.LeaseStore, userID int64) master.Micros {
			return memLeases(s).ReservedBalance(userID)
		},
	})
}

func memLeases(s master.LeaseStore) *master.MemoryLeaseStore {
	m, _ := s.(*master.MemoryLeaseStore)
	return m
}
