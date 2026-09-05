package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2ConfigRepairMigrationRestoresReadableConfig(t *testing.T) {
	content, err := FS.ReadFile("232_repair_channel_monitor_v2_config.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS channel_monitor_v2_config")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS ignored_error_categories")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS health_thresholds")
	require.Contains(t, sql, "INSERT INTO channel_monitor_v2_config (id)")
	require.Contains(t, sql, "ON CONFLICT (id) DO NOTHING")
}
