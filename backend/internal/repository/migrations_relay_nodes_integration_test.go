//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 主从分流（259、260）迁移后的表、列、索引，以及凭证去重表的分区行为。
func TestRelayNodeMigrations_SchemaAndVoucherDedup(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	requireColumn(t, tx, "users", "relay_reserved_balance", "numeric", 0, false)
	requireColumnDefaultContains(t, tx, "users", "relay_reserved_balance", "0")
	requireColumn(t, tx, "api_keys", "relay_node_id", "bigint", 0, true)
	requireColumn(t, tx, "api_keys", "relay_node_changed_at", "timestamp with time zone", 0, true)
	requireColumn(t, tx, "accounts", "master_only", "boolean", 0, false)
	requireColumnDefaultContains(t, tx, "accounts", "master_only", "false")
	requireColumn(t, tx, "usage_logs", "node_id", "bigint", 0, true)
	requireColumn(t, tx, "ops_error_logs", "node_id", "bigint", 0, true)
	requireColumn(t, tx, "ops_system_logs", "node_id", "bigint", 0, true)

	requireIndex(t, tx, "relay_nodes", "idx_relay_nodes_fingerprint")
	requireIndex(t, tx, "relay_quota_leases", "idx_relay_quota_leases_active_scope")
	requireIndex(t, tx, "relay_node_certificates", "idx_relay_node_certificates_renewed_from")
	requireIndex(t, tx, "api_keys", "idx_api_keys_relay_node_id")
	requireIndex(t, tx, "usage_logs", "idx_usage_logs_node_id_created_at")

	// 没有月分区时落进默认分区；同一张凭证第二次入账被主键挡住。
	_, err := tx.ExecContext(ctx, `SAVEPOINT relay_voucher`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO relay_voucher_consumed (voucher_id, issued_at, node_id, user_id)
		VALUES ('00000000-0000-0000-0000-0000000000a1', '2026-09-26T00:00:00Z', 1, 1)`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO relay_voucher_consumed (voucher_id, issued_at, node_id, user_id)
		VALUES ('00000000-0000-0000-0000-0000000000a1', '2026-09-26T00:00:00Z', 2, 2)`)
	require.Error(t, err)
	_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT relay_voucher`)
	require.NoError(t, err)
}
