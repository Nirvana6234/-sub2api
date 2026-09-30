package node

import (
	"context"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// pagedHashes 是主节点的名单（分两页返回）；duringFetch 在拉第二页之前调用（模拟拉取期间的推送）。
type pagedHashes struct {
	relayv1.RelayControlClient
	mu          sync.Mutex
	pages       [][]string
	duringFetch func()
	recorded    []string
}

func (p *pagedHashes) FetchFlaggedHashes(_ context.Context, in *relayv1.FetchFlaggedHashesRequest, _ ...grpc.CallOption) (*relayv1.FetchFlaggedHashesResponse, error) {
	i := int(in.GetCursor())
	if i == 1 && p.duringFetch != nil {
		p.duringFetch()
	}
	next := uint64(i + 1)
	if i+1 >= len(p.pages) {
		next = 0
	}
	return &relayv1.FetchFlaggedHashesResponse{Hashes: p.pages[i], NextCursor: next}, nil
}

func (p *pagedHashes) RecordFlaggedHash(_ context.Context, in *relayv1.RecordFlaggedHashRequest, _ ...grpc.CallOption) (*relayv1.RecordFlaggedHashResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recorded = append(p.recorded, in.GetHash())
	return &relayv1.RecordFlaggedHashResponse{}, nil
}

func has(t *testing.T, r *FlaggedHashReplica, h string) bool {
	t.Helper()
	ok, err := r.HasFlaggedInputHash(context.Background(), h)
	require.NoError(t, err)
	return ok
}

func TestFlaggedHashReplica(t *testing.T) {
	ctx := context.Background()
	master := &pagedHashes{pages: [][]string{{"a", "b"}, {"c"}}}
	r := newFlaggedHashReplica(master)

	_, err := r.HasFlaggedInputHash(ctx, "a")
	require.ErrorIs(t, err, ErrFlaggedHashesNotReady, "not fetched yet: moderation treats it like a hash check error")

	// 拉取期间：主节点推来一个新增和一个删除，这台自己又命中一个。
	master.duringFetch = func() {
		r.Apply(&relayv1.FlaggedHashes{Added: []string{"d"}, Removed: []string{"a"}})
		require.NoError(t, r.RecordFlaggedInputHash(ctx, "local"))
	}
	require.NoError(t, r.Resync(ctx))
	for _, h := range []string{"b", "c", "d", "local"} {
		require.True(t, has(t, r, h), h)
	}
	require.False(t, has(t, r, "a"), "a removal pushed during the fetch is kept")
	require.Equal(t, []string{"local"}, master.recorded, "a new local hit is reported to the master")

	// 增量。
	r.Apply(&relayv1.FlaggedHashes{Added: []string{"e"}})
	require.True(t, has(t, r, "e"))
	r.Apply(&relayv1.FlaggedHashes{Cleared: true, Added: []string{"f"}})
	require.False(t, has(t, r, "b"))
	require.True(t, has(t, r, "f"), "clear applies before the additions in the same change")

	// 断线期间错过的删除：重连后整份拉取以主节点为准。
	master.duringFetch = nil
	master.pages = [][]string{{"f"}}
	r.Apply(&relayv1.FlaggedHashes{Added: []string{"stale"}})
	require.NoError(t, r.Resync(ctx))
	require.False(t, has(t, r, "stale"))
	require.True(t, has(t, r, "f"))

	_, err = r.DeleteFlaggedInputHash(ctx, "f")
	require.Error(t, err, "list management is a master operation")
}
