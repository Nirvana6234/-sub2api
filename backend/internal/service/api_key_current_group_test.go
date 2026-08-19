package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDecorateAutoGroupCurrentSelectionsUsesMostRecentModelSelection(t *testing.T) {
	service := &APIKeyService{}
	apiKey := APIKey{
		ID:                7,
		UserID:            42,
		AutoGroup:         true,
		AutoGroupStrategy: autoGroupStrategyPrice,
		AutoGroupIDs:      []int64{10, 11},
	}
	older := time.Now().Add(-time.Minute)
	newer := time.Now()
	service.autoGroupSelections.Store("7:price:gpt-4o", autoGroupSelection{
		configFingerprint: autoGroupConfigFingerprint(&apiKey),
		selectedGroup:     &Group{ID: 10, Name: "plus"},
		lastSelectedAt:    older,
	})
	service.autoGroupSelections.Store("7:price:gpt-image-1", autoGroupSelection{
		configFingerprint: autoGroupConfigFingerprint(&apiKey),
		selectedGroup:     &Group{ID: 11, Name: "plus-福利"},
		lastSelectedAt:    newer,
	})

	keys := []APIKey{apiKey}
	service.decorateAutoGroupCurrentSelections(keys)

	require.NotNil(t, keys[0].AutoGroupCurrentGroup)
	require.Equal(t, int64(11), keys[0].AutoGroupCurrentGroup.ID)
	require.Equal(t, "plus-福利", keys[0].AutoGroupCurrentGroup.Name)
	require.Equal(t, "gpt-image-1", keys[0].AutoGroupCurrentModel)
	require.NotNil(t, keys[0].AutoGroupCurrentSelectedAt)
}

func TestDecorateAutoGroupCurrentSelectionsDoesNotDecorateFixedKeys(t *testing.T) {
	service := &APIKeyService{}
	keys := []APIKey{{ID: 7, GroupID: func() *int64 { id := int64(10); return &id }()}}
	service.autoGroupSelections.Store("7:price:gpt-4o", autoGroupSelection{
		selectedGroup:  &Group{ID: 10, Name: "plus"},
		lastSelectedAt: time.Now(),
	})

	service.decorateAutoGroupCurrentSelections(keys)

	require.Nil(t, keys[0].AutoGroupCurrentGroup)
}
