//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/master/mastertest"
	"github.com/stretchr/testify/require"
)

// SQL 实现跑与内存实现相同的契约测试（internal/relay/master/mastertest）。
func TestRelayNodeRepositoryContract(t *testing.T) {
	mastertest.Run(t, mastertest.Harness{
		New: func(t *testing.T) master.NodeStore {
			_, err := integrationDB.ExecContext(context.Background(),
				`TRUNCATE relay_node_audit_logs, relay_node_certificates, relay_nodes RESTART IDENTITY CASCADE`)
			require.NoError(t, err)
			return NewRelayNodeRepository(integrationDB)
		},
		Age: func(t *testing.T, _ master.NodeStore, id int64, at time.Time) {
			_, err := integrationDB.ExecContext(context.Background(), `UPDATE relay_nodes SET created_at = $2 WHERE id = $1`, id, at)
			require.NoError(t, err)
		},
	})
}
