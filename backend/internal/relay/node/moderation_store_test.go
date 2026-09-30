package node

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodestore"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 从节点的审核记录写本机：按单机的条件筛选、分页，邮件已发的回填生效，命中与未命中按各自的保留期清理。
func TestModerationStore(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	st, err := nodestore.Open(t.TempDir(), nodestore.Options{Now: clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	m := NewModerationStore(st)
	m.now = clock

	g := int64(5)
	hit := &service.ContentModerationLog{Flagged: true, Action: "block", GroupID: &g, Endpoint: "/v1/responses", InputExcerpt: "bad words", Model: "gpt-5"}
	require.NoError(t, m.CreateLog(ctx, hit))
	require.NotZero(t, hit.ID)
	now = now.Add(time.Minute)
	pass := &service.ContentModerationLog{Flagged: false, Action: "allow", Endpoint: "/v1/chat/completions"}
	require.NoError(t, m.CreateLog(ctx, pass))
	require.NoError(t, m.UpdateLogEmailSent(ctx, hit.ID, true))

	all, page, err := m.ListLogs(ctx, service.ContentModerationLogFilter{Pagination: pagination.PaginationParams{Page: 1, PageSize: 10}})
	require.NoError(t, err)
	require.Equal(t, int64(2), page.Total)
	require.Equal(t, pass.ID, all[0].ID, "newest first")
	require.True(t, all[1].EmailSent, "the email-sent patch applies")

	hits, _, err := m.ListLogs(ctx, service.ContentModerationLogFilter{Result: "hit"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	found, _, err := m.ListLogs(ctx, service.ContentModerationLogFilter{Search: "BAD"})
	require.NoError(t, err)
	require.Len(t, found, 1)
	byGroup, _, err := m.ListLogs(ctx, service.ContentModerationLogFilter{GroupID: &g})
	require.NoError(t, err)
	require.Len(t, byGroup, 1)

	_, err = m.CountFlaggedByUserSince(ctx, 1, now, false)
	require.Error(t, err, "violation counts live on the master")

	// 未命中保留 3 天、命中 180 天：4 天后清理只删未命中。
	now = now.Add(4 * 24 * time.Hour)
	res, err := m.CleanupExpiredLogs(ctx, now.Add(-180*24*time.Hour), now.Add(-3*24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(0), res.DeletedHit)
	require.Equal(t, int64(1), res.DeletedNonHit)
	left, _, err := m.ListLogs(ctx, service.ContentModerationLogFilter{})
	require.NoError(t, err)
	require.Len(t, left, 1)
	require.Equal(t, hit.ID, left[0].ID)
}
