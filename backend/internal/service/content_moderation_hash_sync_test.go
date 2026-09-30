package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 命中过的输入名单的每次变化（审核命中写入、后台删除、清空、从节点上报）都通知出去（主节点推给各从节点）。
func TestFlaggedHashChangesArePublished(t *testing.T) {
	ctx := context.Background()
	cache := &contentModerationTestHashCache{}
	svc := NewContentModerationService(&contentModerationTestSettingRepo{values: map[string]string{SettingKeyRiskControlEnabled: "true"}},
		&contentModerationTestRepo{}, cache, nil, nil, nil, nil, nil)
	var got []ContentModerationHashChange
	svc.SetHashChangeListener(func(ch ContentModerationHashChange) { got = append(got, ch) })
	h1, h2 := strings.Repeat("a", 64), strings.Repeat("b", 64)

	svc.persistContentModerationLog(ctx, &ContentModerationConfig{}, &ContentModerationLog{Flagged: true}, h1, true, false)
	require.NoError(t, svc.RecordRelayFlaggedHash(ctx, strings.ToUpper(h2)))
	_, err := svc.DeleteFlaggedInputHash(ctx, h1)
	require.NoError(t, err)
	_, err = svc.DeleteFlaggedInputHash(ctx, strings.Repeat("c", 64))
	require.NoError(t, err)
	_, err = svc.ClearFlaggedInputHashes(ctx)
	require.NoError(t, err)
	require.Equal(t, []ContentModerationHashChange{
		{Added: []string{h1}},
		{Added: []string{h2}},
		{Removed: []string{h1}},
		{Cleared: true},
	}, got, "deleting a hash that is not listed publishes nothing")
	require.Error(t, svc.RecordRelayFlaggedHash(ctx, "not-a-hash"))
}
