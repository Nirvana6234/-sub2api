package repository

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// 主从分流的迁移必须是纯加法（开发计划第 1 节）：只加表、加列、加索引，
// 不改、不删现有列，才能在开关关闭时对现有部署零影响。
func TestRelayNodeMigrationsAreAdditiveOnly(t *testing.T) {
	content, err := migrations.FS.ReadFile("259_relay_nodes.sql")
	require.NoError(t, err)

	sql := strings.ToUpper(stripAllSQLLineComments(string(content)))
	for _, forbidden := range []string{"DROP ", "RENAME ", "ALTER COLUMN", "TRUNCATE", "DELETE FROM", "UPDATE "} {
		require.NotContains(t, sql, forbidden, "259_relay_nodes.sql must be additive only")
	}

	// 每个 ALTER TABLE 都只能 ADD。
	alters := regexp.MustCompile(`ALTER TABLE\s+\w+\s+ADD\s+(COLUMN IF NOT EXISTS|CONSTRAINT)`).FindAllString(sql, -1)
	require.Equal(t, strings.Count(sql, "ALTER TABLE"), len(alters), "every ALTER TABLE must only add")

	// 现有表加的列必须可空或带常量默认值，PostgreSQL 11+ 只改元数据、不重写大表。
	for _, column := range []string{
		"USERS\n    ADD COLUMN IF NOT EXISTS RELAY_RESERVED_BALANCE DECIMAL(20,8) NOT NULL DEFAULT 0",
		"API_KEYS\n    ADD COLUMN IF NOT EXISTS RELAY_NODE_ID BIGINT;",
		"ACCOUNTS\n    ADD COLUMN IF NOT EXISTS MASTER_ONLY BOOLEAN NOT NULL DEFAULT FALSE",
		"USAGE_LOGS\n    ADD COLUMN IF NOT EXISTS NODE_ID BIGINT;",
		"OPS_ERROR_LOGS\n    ADD COLUMN IF NOT EXISTS NODE_ID BIGINT;",
		"OPS_SYSTEM_LOGS\n    ADD COLUMN IF NOT EXISTS NODE_ID BIGINT;",
	} {
		require.Contains(t, sql, column)
	}
}

func TestRelayNodeMigrationsExecutionMode(t *testing.T) {
	tx, err := migrations.FS.ReadFile("259_relay_nodes.sql")
	require.NoError(t, err)
	nonTx, err := validateMigrationExecutionMode("259_relay_nodes.sql", string(tx))
	require.NoError(t, err)
	require.False(t, nonTx)

	idx, err := migrations.FS.ReadFile("260_relay_nodes_indexes_notx.sql")
	require.NoError(t, err)
	nonTx, err = validateMigrationExecutionMode("260_relay_nodes_indexes_notx.sql", string(idx))
	require.NoError(t, err)
	require.True(t, nonTx)
}

// 扣费凭证去重表按签发时间分区，唯一键必须包含分区键；默认分区保证
// 开关关闭（没有预建月分区的后台任务）时插入也不会失败。
func TestRelayVoucherConsumedIsPartitionedWithDefault(t *testing.T) {
	content, err := migrations.FS.ReadFile("259_relay_nodes.sql")
	require.NoError(t, err)
	sql := string(content)
	require.Contains(t, sql, "PRIMARY KEY (voucher_id, issued_at)")
	require.Contains(t, sql, ") PARTITION BY RANGE (issued_at);")
	require.Contains(t, sql, "PARTITION OF relay_voucher_consumed DEFAULT;")
}

func stripAllSQLLineComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}
