package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration186RestoresContributorOwnershipWithoutLosingImportMethod(t *testing.T) {
	content, err := FS.ReadFile("186_restore_user_contribution_source.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "submitted_by_user_id")
	require.Contains(t, sql, "contribution_import_method")
	require.Contains(t, sql, "to_jsonb(extra->>'import_source')")
	require.Contains(t, sql, "'\"user_contribution\"'::jsonb")
	require.Contains(t, sql, "extra->>'import_source' <> 'user_contribution'")
}
