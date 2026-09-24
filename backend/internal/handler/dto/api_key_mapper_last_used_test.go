package dto

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyFromService_MapsLastUsedAt(t *testing.T) {
	lastUsed := time.Now().UTC().Truncate(time.Second)
	lastUsedIP := "203.0.113.10"
	src := &service.APIKey{
		ID:                 1,
		UserID:             2,
		Key:                "sk-map-last-used",
		Name:               "Mapper",
		Status:             service.StatusActive,
		LastUsedAt:         &lastUsed,
		LastUsedIP:         &lastUsedIP,
		CurrentConcurrency: 3,
	}

	out := APIKeyFromService(src)
	require.NotNil(t, out)
	require.NotNil(t, out.LastUsedAt)
	require.WithinDuration(t, lastUsed, *out.LastUsedAt, time.Second)
	require.NotNil(t, out.LastUsedIP)
	require.Equal(t, lastUsedIP, *out.LastUsedIP)
	require.Equal(t, 3, out.CurrentConcurrency)
}

func TestAPIKeyFromService_MapsNilLastUsedAt(t *testing.T) {
	src := &service.APIKey{
		ID:     1,
		UserID: 2,
		Key:    "sk-map-last-used-nil",
		Name:   "MapperNil",
		Status: service.StatusActive,
	}

	out := APIKeyFromService(src)
	require.NotNil(t, out)
	require.Nil(t, out.LastUsedAt)
	require.Nil(t, out.LastUsedIP)
}

// Regression: the response mapper must surface auto-group state, or a key
// that successfully enabled automatic routing renders as if it had no group
// at all once the list reloads (frontend falls through both the auto-group
// badge and the static group badge).
func TestAPIKeyFromService_MapsAutoGroupState(t *testing.T) {
	selectedAt := time.Now().UTC().Truncate(time.Second)
	src := &service.APIKey{
		ID:                         1,
		UserID:                     2,
		Key:                        "sk-map-auto-group",
		Name:                       "AutoGroupMapper",
		Status:                     service.StatusActive,
		AutoGroup:                  true,
		AutoGroupStrategy:          "speed",
		AutoGroupIDs:               []int64{10, 20},
		AutoGroupCurrentGroup:      &service.Group{ID: 20, Name: "fast-pool"},
		AutoGroupCurrentModel:      "gpt-5.5",
		AutoGroupCurrentSelectedAt: &selectedAt,
	}

	out := APIKeyFromService(src)
	require.NotNil(t, out)
	require.True(t, out.AutoGroup)
	require.Equal(t, "speed", out.AutoGroupStrategy)
	require.Equal(t, []int64{10, 20}, out.AutoGroupIDs)
	require.NotNil(t, out.AutoGroupCurrentGroup)
	require.Equal(t, "fast-pool", out.AutoGroupCurrentGroup.Name)
	require.Equal(t, "gpt-5.5", out.AutoGroupCurrentModel)
	require.NotNil(t, out.AutoGroupCurrentSelectedAt)
	require.WithinDuration(t, selectedAt, *out.AutoGroupCurrentSelectedAt, time.Second)
}
