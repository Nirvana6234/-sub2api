package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBindGroupPrioritiesKeepsCurrentInGroupPriority(t *testing.T) {
	current := map[int64]int{10: 10000, 20: 3}

	// Re-saving the same groups (an unrelated account edit) must not touch them.
	require.Equal(t, []int{10000, 3}, bindGroupPriorities([]int64{10, 20}, current))
	require.Equal(t, []int{3, 10000}, bindGroupPriorities([]int64{20, 10}, current))

	// A newly joined group gets the positional default; kept groups stay.
	require.Equal(t, []int{10000, 2}, bindGroupPriorities([]int64{10, 30}, current))

	// First binding of an account keeps the historical 1, 2, 3 defaults.
	require.Equal(t, []int{1, 2}, bindGroupPriorities([]int64{10, 20}, nil))
}
