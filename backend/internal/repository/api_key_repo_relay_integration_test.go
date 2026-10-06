//go:build integration || localpg

package repository

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// runAPIKeyRelayContract 检查 Key 仓储里和主从分流节点分配有关的部分（设计 10.2）：
// 新建时写入分配、批量重新分配（记改变时间、返回 Key 原文用于作废缓存）、按条件列出、统计。
func runAPIKeyRelayContract(t *testing.T, client *dbent.Client) {
	t.Helper()
	ctx := context.Background()
	repo := newAPIKeyRepositoryWithSQL(client, nil)
	user, err := client.User.Create().SetEmail("relay-keys@test.com").SetPasswordHash("x").
		SetStatus(service.StatusActive).SetRole(service.RoleUser).Save(ctx)
	require.NoError(t, err)

	node := int64(3)
	assigned := &service.APIKey{UserID: user.ID, Key: "sk-relay-assigned", Name: "a", Status: service.StatusActive, RelayNodeID: &node}
	plain := &service.APIKey{UserID: user.ID, Key: "sk-relay-plain", Name: "b", Status: service.StatusActive}
	onMaster := &service.APIKey{UserID: user.ID, Key: "sk-relay-master", Name: "c", Status: service.StatusActive}
	deleted := &service.APIKey{UserID: user.ID, Key: "sk-relay-deleted", Name: "d", Status: service.StatusActive}
	for _, k := range []*service.APIKey{assigned, plain, onMaster, deleted} {
		require.NoError(t, repo.Create(ctx, k))
	}

	// 新建时写入分配；没分配的保持 NULL。
	got, err := repo.GetByID(ctx, assigned.ID)
	require.NoError(t, err)
	require.NotNil(t, got.RelayNodeID)
	require.Equal(t, node, *got.RelayNodeID)
	got, err = repo.GetByID(ctx, plain.ID)
	require.NoError(t, err)
	require.Nil(t, got.RelayNodeID)

	// 第一次分配（不记改变时间）：主节点 = 0。
	keys, err := repo.AssignRelayNode(ctx, []int64{onMaster.ID}, 0, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"sk-relay-master"}, keys)
	got, err = repo.GetByID(ctx, onMaster.ID)
	require.NoError(t, err)
	require.NotNil(t, got.RelayNodeID)
	require.Equal(t, int64(0), *got.RelayNodeID, "0 is the master node, not unassigned")
	require.Nil(t, got.RelayNodeChangedAt)

	// 软删除的 Key 不动、不返回。
	require.NoError(t, repo.Delete(ctx, deleted.ID))
	keys, err = repo.AssignRelayNode(ctx, []int64{deleted.ID}, 3, nil)
	require.NoError(t, err)
	require.Empty(t, keys)

	// 按条件列出、计数：未分配的、分配给某个节点的（含主节点）。
	ids, err := repo.ListRelayKeyIDs(ctx, service.RelayKeyFilter{Unassigned: true}, 0, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{plain.ID}, ids)
	ids, err = repo.ListRelayKeyIDs(ctx, service.RelayKeyFilter{NodeID: 0}, 0, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{onMaster.ID}, ids)
	n, err := repo.CountRelayKeys(ctx, service.RelayKeyFilter{NodeID: 3})
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	ids, err = repo.ListRelayKeyIDs(ctx, service.RelayKeyFilter{Unassigned: true}, plain.ID, 10)
	require.NoError(t, err)
	require.Empty(t, ids, "the cursor skips what was already seen")

	// 重新分配：记改变时间，返回 Key 原文。
	at := time.Now().UTC().Truncate(time.Second)
	keys, err = repo.AssignRelayNode(ctx, []int64{assigned.ID, plain.ID}, 7, &at)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sk-relay-assigned", "sk-relay-plain"}, keys)
	got, err = repo.GetByID(ctx, assigned.ID)
	require.NoError(t, err)
	require.Equal(t, int64(7), *got.RelayNodeID)
	require.NotNil(t, got.RelayNodeChangedAt)
	require.WithinDuration(t, at, *got.RelayNodeChangedAt, time.Second)

	// 统计：每台节点上的 Key 数和近期用过的数（不含未分配的和已删除的）。
	require.NoError(t, repo.UpdateLastUsed(ctx, assigned.ID, time.Now()))
	stats, err := repo.RelayKeyStats(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, service.RelayKeyStat{Total: 2, Active: 1}, stats[7])
	require.Equal(t, service.RelayKeyStat{Total: 1}, stats[0])
	require.NotContains(t, stats, int64(3))
}
