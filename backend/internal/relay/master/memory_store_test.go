package master_test

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/master/mastertest"
)

func TestMemoryStoreContract(t *testing.T) {
	mastertest.Run(t, mastertest.Harness{
		New: func(*testing.T) master.NodeStore { return master.NewMemoryStore() },
		Age: func(t *testing.T, s master.NodeStore, id int64, at time.Time) {
			mem, ok := s.(*master.MemoryStore)
			if !ok {
				t.Fatalf("unexpected store %T", s)
			}
			mem.SetCreatedAt(id, at)
		},
	})
}
