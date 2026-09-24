package schema

import (
	"testing"

	"entgo.io/ent/entc/load"
	"github.com/stretchr/testify/require"
)

func TestContributionRoomSchemas(t *testing.T) {
	spec, err := (&load.Config{Path: "."}).Load()
	require.NoError(t, err)

	schemas := map[string]*load.Schema{}
	for _, loaded := range spec.Schemas {
		schemas[loaded.Name] = loaded
	}

	room := requireSchema(t, schemas, "ContributionRoom")
	requireSchemaFields(t, room,
		"owner_user_id",
		"name",
		"consumer_rate_multiplier",
		"status",
		"visibility",
		"created_at",
		"updated_at",
	)

	verification := requireSchema(t, schemas, "ContributionAccountVerification")
	requireSchemaFields(t, verification,
		"account_id",
		"platform",
		"status",
		"model_family",
		"source_kind",
		"tested_model",
		"tested_at",
		"redacted_error_summary",
	)
	require.True(t, requireSchemaField(t, verification, "account_id").Unique)

	roomAccount := requireSchema(t, schemas, "ContributionRoomAccount")
	requireSchemaFields(t, roomAccount,
		"room_id",
		"account_id",
		"enabled",
		"share_budget_usd",
		"share_used_usd",
		"verified_at",
	)
	require.True(t, requireSchemaField(t, roomAccount, "account_id").Unique)

	preference := requireSchema(t, schemas, "UserContributionRoomPreference")
	requireSchemaFields(t, preference,
		"user_id",
		"api_key_id",
		"room_id",
		"allow_pool_fallback",
		"fallback_group_id",
	)
	require.False(t, requireSchemaField(t, preference, "user_id").Unique)
	requireHasUniqueIndex(t, preference, "api_key_id", "room_id")
}
