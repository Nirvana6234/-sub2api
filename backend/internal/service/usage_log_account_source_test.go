package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func contributedAccount(contributorID int64) *Account {
	return &Account{
		ID: 795,
		Extra: map[string]any{
			AccountContributionSourceKey: AccountContributionSourceValue,
			AccountContributorUserIDKey:  float64(contributorID),
		},
	}
}

func TestUsageLogAccountSourceFor(t *testing.T) {
	t.Run("nil account is pool", func(t *testing.T) {
		require.Equal(t, UsageLogAccountSourcePool, UsageLogAccountSourceFor(nil, 3))
	})

	t.Run("admin account is pool", func(t *testing.T) {
		require.Equal(t, UsageLogAccountSourcePool, UsageLogAccountSourceFor(&Account{ID: 773}, 3))
	})

	t.Run("contributor using own account is own", func(t *testing.T) {
		require.Equal(t, UsageLogAccountSourceOwn, UsageLogAccountSourceFor(contributedAccount(3), 3))
	})

	t.Run("own wins even when routed through the contributor's own room", func(t *testing.T) {
		account := contributedAccount(3)
		account.ContributionRouteSource = ContributionRouteSourceRoom
		account.ContributionRoomID = 11
		require.Equal(t, UsageLogAccountSourceOwn, UsageLogAccountSourceFor(account, 3))
	})

	t.Run("another member's account routed through a room is room", func(t *testing.T) {
		account := contributedAccount(88)
		account.ContributionRouteSource = ContributionRouteSourceRoom
		account.ContributionRoomID = 11
		require.Equal(t, UsageLogAccountSourceRoom, UsageLogAccountSourceFor(account, 3))
	})

	t.Run("another member's account merged into the admin pool is pool", func(t *testing.T) {
		account := contributedAccount(88)
		account.Extra[AccountShareModeKey] = AccountShareModePool
		require.Equal(t, UsageLogAccountSourcePool, UsageLogAccountSourceFor(account, 3))
	})
}

func TestParseUsageLogAccountSourceFilter(t *testing.T) {
	for _, in := range []string{"", "all", " ALL "} {
		got, err := ParseUsageLogAccountSourceFilter(in)
		require.NoError(t, err)
		require.Empty(t, got, "input %q means no filter", in)
	}
	for _, in := range []string{"pool", "own", "room", " Own "} {
		got, err := ParseUsageLogAccountSourceFilter(in)
		require.NoError(t, err)
		require.Contains(t, []string{UsageLogAccountSourcePool, UsageLogAccountSourceOwn, UsageLogAccountSourceRoom}, got)
	}
	_, err := ParseUsageLogAccountSourceFilter("admin")
	require.Error(t, err)
}
