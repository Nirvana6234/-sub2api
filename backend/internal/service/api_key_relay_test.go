//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type relayAssignerStub struct {
	id    *int64
	err   error
	calls int
}

func (s *relayAssignerStub) AssignNewKeyNode(context.Context) (*int64, error) {
	s.calls++
	return s.id, s.err
}

func newRelayKeyTestService() (*APIKeyService, *playgroundAPIKeyRepoStub) {
	repo := &playgroundAPIKeyRepoStub{}
	svc := NewAPIKeyService(repo, &userRepoStub{user: &User{ID: 7, Status: StatusActive}}, &playgroundGroupRepoStub{},
		playgroundSubscriptionRepoStub{}, nil, nil, &config.Config{})
	return svc, repo
}

// 主从分流（设计 10.2）：新建 Key 时由分配器定节点；开关关闭（没有分配器）、分配失败、没有可分配的节点时保持未分配；
// Playground 内部 Key 不分配（设计 8.1）。
func TestAPIKeyCreateAssignsRelayNode(t *testing.T) {
	ctx := context.Background()
	node := int64(12)
	svc, repo := newRelayKeyTestService()

	_, err := svc.Create(ctx, 7, CreateAPIKeyRequest{Name: "no assigner"})
	require.NoError(t, err)
	require.Nil(t, repo.keys[0].RelayNodeID, "relay switch off: unassigned")

	assigner := &relayAssignerStub{id: &node}
	svc.SetRelayKeyAssigner(assigner)
	_, err = svc.Create(ctx, 7, CreateAPIKeyRequest{Name: "assigned"})
	require.NoError(t, err)
	require.NotNil(t, repo.keys[1].RelayNodeID)
	require.Equal(t, node, *repo.keys[1].RelayNodeID)
	require.Nil(t, repo.keys[1].RelayNodeChangedAt, "the first assignment is not a change")

	_, err = svc.Create(ctx, 7, CreateAPIKeyRequest{Name: PlaygroundChatAPIKeyName, SkipRelayAssignment: true})
	require.NoError(t, err)
	require.Nil(t, repo.keys[2].RelayNodeID, "internal keys skip assignment")
	require.Equal(t, 1, assigner.calls)

	assigner.id = nil
	_, err = svc.Create(ctx, 7, CreateAPIKeyRequest{Name: "nothing to pick"})
	require.NoError(t, err)
	require.Nil(t, repo.keys[3].RelayNodeID)

	assigner.err = errors.New("store down")
	_, err = svc.Create(ctx, 7, CreateAPIKeyRequest{Name: "assigner failed"})
	require.NoError(t, err, "a failed assignment never blocks creating the key")
	require.Nil(t, repo.keys[4].RelayNodeID)

	svc.SetRelayKeyAssigner(nil)
	assigner.err = nil
	assigner.id = &node
	_, err = svc.Create(ctx, 7, CreateAPIKeyRequest{Name: "switched off again"})
	require.NoError(t, err)
	require.Nil(t, repo.keys[5].RelayNodeID)
}

func TestAPIKeyRelayAssignmentNeedsRepositorySupport(t *testing.T) {
	svc, _ := newRelayKeyTestService()
	_, err := svc.AssignRelayNode(context.Background(), []int64{1}, 3, true)
	require.ErrorIs(t, err, ErrRelayKeyAssignmentUnsupported)
	n, err := svc.AssignRelayNode(context.Background(), nil, 3, true)
	require.NoError(t, err)
	require.Zero(t, n)
}
