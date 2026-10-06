package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type usageNodeRepoStub struct {
	UsageLogRepository
	logs  []UsageLog
	nodes map[int64]int64
}

func (s *usageNodeRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error) {
	return append([]UsageLog(nil), s.logs...), &pagination.PaginationResult{Total: int64(len(s.logs))}, nil
}

func (s *usageNodeRepoStub) LoadUsageLogNodeIDs(_ context.Context, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for _, id := range ids {
		if n, ok := s.nodes[id]; ok {
			out[id] = n
		}
	}
	return out, nil
}

// 后台使用记录的"节点"列（设计 12.4）：从节点上报的记录带上转发节点，主节点自己转发的（列为 NULL）没有值。
func TestUsageListAttachesForwardingNodes(t *testing.T) {
	repo := &usageNodeRepoStub{logs: []UsageLog{{ID: 1}, {ID: 2}, {ID: 3}}, nodes: map[int64]int64{1: 7, 3: 9}}
	svc := &UsageService{usageRepo: repo}
	logs, _, err := svc.ListWithFilters(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 10}, usagestats.UsageLogFilters{})
	require.NoError(t, err)
	require.NotNil(t, logs[0].NodeID)
	require.EqualValues(t, 7, *logs[0].NodeID)
	require.Nil(t, logs[1].NodeID, "the master forwarded it itself")
	require.EqualValues(t, 9, *logs[2].NodeID)

	// 仓储不支持（或没有记录）时照常返回列表。
	plain := &UsageService{usageRepo: &usageRepoStub{}}
	require.NotPanics(t, func() { plain.attachNodeIDs(context.Background(), nil) })
}
