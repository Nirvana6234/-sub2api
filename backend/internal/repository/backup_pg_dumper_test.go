package repository

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPSQLArgsHaveNoDestructiveFlags(t *testing.T) {
	args := psqlArgs(&config.DatabaseConfig{Host: "db", Port: 5432, User: "app", DBName: "sub2api"})
	require.Equal(t, []string{"-X", "-h", "db", "-p", "5432", "-U", "app", "-d", "sub2api"}, args)
	require.NotContains(t, args, "--clean")
	require.NotContains(t, args, "--if-exists")
}

func TestSystemConfigSnapshotQueryHasOnlyApprovedReadTargets(t *testing.T) {
	query := systemConfigSnapshotQuery([]string{"site_name", "account_share_reward_rate"})

	require.Contains(t, query, "'sub2api-system-config-v1'")
	require.Contains(t, query, "public.error_passthrough_rules")
	require.Contains(t, query, "public.tls_fingerprint_profiles")
	require.Contains(t, query, "public.settings")
	require.Contains(t, query, "'site_name'")
	require.Contains(t, query, "'account_share_reward_rate'")
	for _, forbidden := range []string{
		"public.users",
		"public.accounts",
		"public.api_keys",
		"public.account_groups",
		"public.contribution_rooms",
		"public.contribution_room_accounts",
		"public.contribution_account_verifications",
		"public.user_contribution_room_preferences",
		"public.usage_logs",
		"public.payment_orders",
		"public.payment_provider_instances",
		"public.security_secrets",
	} {
		require.NotContains(t, query, forbidden)
	}
}

func TestReadSystemConfigSnapshotRejectsLegacyAndUnapprovedData(t *testing.T) {
	_, err := readSystemConfigSnapshot(strings.NewReader("-- PostgreSQL database dump"))
	require.Error(t, err)

	_, err = readSystemConfigSnapshot(strings.NewReader(`{"format":"sub2api-system-config-v1","settings":[{"key":"admin_api_key","value":"secret","updated_at":"2026-07-13T00:00:00Z"}]}`))
	require.Error(t, err)
}

func TestSystemConfigSnapshotRestoreSQLHasOnlyApprovedWriteTargets(t *testing.T) {
	snapshot := &systemConfigSnapshot{
		Format: systemConfigBackupFormat,
		Settings: []systemConfigKV{{
			Key:       "site_name",
			Value:     "O'Reilly API",
			UpdatedAt: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
		}},
		ErrorPassthroughRules:  []json.RawMessage{json.RawMessage(`{"id":7,"name":"retry","enabled":true}`)},
		TLSFingerprintProfiles: []json.RawMessage{json.RawMessage(`{"id":8,"name":"chrome"}`)},
	}

	script, err := snapshot.restoreSQL()
	require.NoError(t, err)
	require.Contains(t, script, "BEGIN;")
	require.Contains(t, script, "COMMIT;")
	require.Contains(t, script, "DELETE FROM public.error_passthrough_rules;")
	require.Contains(t, script, "DELETE FROM public.tls_fingerprint_profiles;")
	require.Contains(t, script, "'O''Reilly API'")
	for _, forbidden := range []string{"public.users", "public.accounts", "public.api_keys", "public.contribution_rooms", "public.usage_logs", "public.payment_orders"} {
		require.NotContains(t, script, forbidden)
	}
}

func TestSQLStringLiteralEscapesSingleQuotes(t *testing.T) {
	require.Equal(t, "'owner''s setting'", sqlStringLiteral("owner's setting"))
}
