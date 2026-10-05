package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Regression: the authentication snapshot is what the gateway hot path
// reconstructs the API key from, both on a cache hit and right after a DB
// lookup. Dropping the auto-group fields there makes every automatic key
// arrive at the middleware chain with AutoGroup=false and no group, so
// autoGroupModelRoutingMiddleware skips it and RequireGroupAssignment answers
// 403 "API Key is not assigned to any group" — regardless of what the database
// holds.
func TestAuthSnapshotRoundTripCarriesAutoGroupState(t *testing.T) {
	svc := &APIKeyService{}
	source := &APIKey{
		ID:                301,
		UserID:            7,
		Status:            StatusActive,
		Name:              "auto",
		AutoGroup:         true,
		AutoGroupStrategy: "balanced",
		AutoGroupIDs:      []int64{2, 12, 20},
		User:              &User{ID: 7, Status: StatusActive, Role: RoleUser},
	}

	snapshot := svc.snapshotFromAPIKey(context.Background(), source)
	require.NotNil(t, snapshot)
	require.True(t, snapshot.AutoGroup)
	require.Equal(t, "balanced", snapshot.AutoGroupStrategy)
	require.Equal(t, []int64{2, 12, 20}, snapshot.AutoGroupIDs)

	// The snapshot travels through Redis as JSON, so the fields must survive
	// serialization as well as the in-process struct copy.
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var decoded APIKeyAuthSnapshot
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	restored := svc.snapshotToAPIKey("sk-auto", &decoded)
	require.NotNil(t, restored)
	require.True(t, restored.AutoGroup,
		"an automatic key must still look automatic after a cache round trip")
	require.Equal(t, "balanced", restored.AutoGroupStrategy)
	require.Equal(t, []int64{2, 12, 20}, restored.AutoGroupIDs)
}

// Cached snapshots written before auto-group state existed must be discarded
// rather than served, otherwise an automatic key keeps failing until its TTL
// expires.
func TestAuthSnapshotVersionRejectsPreAutoGroupPayloads(t *testing.T) {
	require.Equal(t, 27, apiKeyAuthSnapshotVersion,
		"bump the snapshot version whenever the payload gains auth-relevant fields")

	stale := &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{Version: 26, APIKeyID: 1}}
	require.NotEqual(t, apiKeyAuthSnapshotVersion, stale.Snapshot.Version)
}

// 主从分流的节点规则看 Key 分配的节点：认证快照（缓存命中和查库后重建 Key 都用它）必须带着 relay_node_id，否则规则对缓存里的 Key 不生效。
func TestAuthSnapshotRoundTripCarriesRelayNodeID(t *testing.T) {
	svc := &APIKeyService{}
	assigned := int64(0) // 主节点
	for _, id := range []*int64{nil, &assigned} {
		source := &APIKey{ID: 5, UserID: 7, Status: StatusActive, RelayNodeID: id, User: &User{ID: 7, Status: StatusActive, Role: RoleUser}}
		snapshot := svc.snapshotFromAPIKey(context.Background(), source)
		encoded, err := json.Marshal(snapshot)
		require.NoError(t, err)
		var decoded APIKeyAuthSnapshot
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		restored := svc.snapshotToAPIKey("sk-x", &decoded)
		require.Equal(t, id, restored.RelayNodeID)
	}
}
