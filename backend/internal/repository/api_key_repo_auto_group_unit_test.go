package repository

import (
	"context"
	"database/sql"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

// newAutoGroupRepoSQLite mirrors newAPIKeyRepoSQLite but adds the
// auto_group_ids column. That column lives outside the Ent model on purpose
// (see storeAutoGroupIDs), so the generated test schema does not create it.
func newAutoGroupRepoSQLite(t *testing.T) (*apiKeyRepository, *dbent.Client) {
	t.Helper()

	db, err := sql.Open("sqlite", "file:api_key_repo_auto_group?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	_, err = db.Exec("ALTER TABLE api_keys ADD COLUMN auto_group_ids TEXT")
	require.NoError(t, err)

	return &apiKeyRepository{client: client, sql: db}, client
}

// Regression: the repository silently dropped auto_group on write and never
// read it back, so enabling automatic routing cleared group_id (a supported
// column) while auto_group stayed false. The key then rendered as "no group"
// even though the save reported success.
func TestAPIKeyRepositoryPersistsAutoGroupSelection(t *testing.T) {
	ctx := context.Background()
	repo, client := newAutoGroupRepoSQLite(t)
	owner := mustCreateAPIKeyRepoUser(t, ctx, client, "auto-group-owner@example.com")

	key := &service.APIKey{
		UserID:            owner.ID,
		Key:               "sk-auto-group-roundtrip",
		Name:              "auto-group",
		Status:            service.StatusActive,
		AutoGroupStrategy: "price",
	}
	require.NoError(t, repo.Create(ctx, key))
	require.NotZero(t, key.ID)

	stored, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.False(t, stored.AutoGroup, "a freshly created key must not silently enable automatic routing")

	key.AutoGroup = true
	key.AutoGroupStrategy = "speed"
	key.GroupID = nil
	require.NoError(t, repo.Update(ctx, key, service.APIKeyUpdateFields{
		AutoGroup:         true,
		AutoGroupStrategy: true,
		GroupID:           true,
	}))

	reloaded, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.True(t, reloaded.AutoGroup, "auto_group must survive the write/read round trip")
	require.Equal(t, "speed", reloaded.AutoGroupStrategy)
	require.Nil(t, reloaded.GroupID)

	// Turning automatic routing back off must also persist.
	key.AutoGroup = false
	require.NoError(t, repo.Update(ctx, key, service.APIKeyUpdateFields{AutoGroup: true}))
	reloaded, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.False(t, reloaded.AutoGroup)
}

// Regression: list responses feed the key table, and every list path maps
// entities through apiKeyEntityToService. Dropping auto_group in that mapper
// made every automatic key render as "no group" in the UI.
//
// The list helpers themselves resolve auto_group_ids with PostgreSQL-only SQL
// (id = ANY($1)), so the candidate hydration is covered by the PostgreSQL
// integration tests rather than here; this asserts the mapper contract that
// the whole read path depends on.
func TestAPIKeyEntityToServiceCarriesAutoGroupFlag(t *testing.T) {
	ctx := context.Background()
	repo, client := newAutoGroupRepoSQLite(t)
	owner := mustCreateAPIKeyRepoUser(t, ctx, client, "auto-group-list@example.com")

	key := &service.APIKey{
		UserID:            owner.ID,
		Key:               "sk-auto-group-list",
		Name:              "auto-group-list",
		Status:            service.StatusActive,
		AutoGroupStrategy: "balanced",
	}
	require.NoError(t, repo.Create(ctx, key))

	key.AutoGroup = true
	require.NoError(t, repo.Update(ctx, key, service.APIKeyUpdateFields{AutoGroup: true}))

	entity, err := repo.activeQuery().Where(apikey.IDEQ(key.ID)).Only(ctx)
	require.NoError(t, err)

	mapped := apiKeyEntityToService(entity)
	require.NotNil(t, mapped)
	require.True(t, mapped.AutoGroup)
	require.Equal(t, "balanced", mapped.AutoGroupStrategy)
}
