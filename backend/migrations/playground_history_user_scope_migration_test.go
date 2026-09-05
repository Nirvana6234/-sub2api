package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration196UsesValidJSONDefaultForMissingModel(t *testing.T) {
	content, err := FS.ReadFile("196_playground_history_user_scope.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "COALESCE(latest.model, '\"\"'::jsonb)")
	require.NotContains(t, sql, "COALESCE(latest.model, ''::jsonb)")
}
