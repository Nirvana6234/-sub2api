//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type recordedChanges struct{ got []AccessChange }

func (r *recordedChanges) add(c AccessChange) { r.got = append(r.got, c) }

func TestAccessChangeHubSubscribeAndUnsubscribe(t *testing.T) {
	var nilHub *AccessChangeHub
	nilHub.Publish(AccessChange{Kind: AccessChangeUser, UserID: 1}) // nil 安全
	require.NotPanics(t, func() { nilHub.Subscribe(func(AccessChange) {})() })

	hub := NewAccessChangeHub()
	var rec recordedChanges
	unsub := hub.Subscribe(rec.add)
	require.Equal(t, 1, hub.SubscriberCount())
	hub.Publish(AccessChange{Kind: AccessChangeGroup, GroupID: 3})
	unsub()
	require.Equal(t, 0, hub.SubscriberCount())
	hub.Publish(AccessChange{Kind: AccessChangeGroup, GroupID: 4})
	require.Equal(t, []AccessChange{{Kind: AccessChangeGroup, GroupID: 3}}, rec.got)
}

// 用户、分组作废顺带发布改动；删除用户时 Key 可能已查不到，用户这一级也不能丢。
func TestAPIKeyInvalidationPublishesUserAndGroupChanges(t *testing.T) {
	repo := &authRepoStub{
		listKeysByUserID:  func(context.Context, int64) ([]string, error) { return nil, errors.New("user is gone") },
		listKeysByGroupID: func(context.Context, int64) ([]string, error) { return nil, nil },
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, nil, &config.Config{})
	ctx := context.Background()

	svc.InvalidateAuthCacheByUserID(ctx, 9) // 没挂中心时照常工作
	hub := NewAccessChangeHub()
	var rec recordedChanges
	hub.Subscribe(rec.add)
	AttachAccessChangeHub(hub, svc, nil)

	svc.InvalidateAuthCacheByUserID(ctx, 11)
	svc.InvalidateAuthCacheByGroupID(ctx, 5)
	svc.InvalidateAuthCacheByUserID(ctx, 0)
	require.Equal(t, []AccessChange{
		{Kind: AccessChangeUser, UserID: 11},
		{Kind: AccessChangeGroup, GroupID: 5},
	}, rec.got)
}

func TestSubscriptionInvalidationPublishesAChange(t *testing.T) {
	billing := &BillingCacheService{}
	hub := NewAccessChangeHub()
	var rec recordedChanges
	hub.Subscribe(rec.add)
	AttachAccessChangeHub(hub, nil, billing)

	require.NoError(t, billing.InvalidateSubscription(context.Background(), 11, 5))
	require.Equal(t, []AccessChange{{Kind: AccessChangeSubscription, UserID: 11, GroupID: 5}}, rec.got)
}

type quotaRepoStub struct {
	UserPlatformQuotaRepository
	err error
}

func (s *quotaRepoStub) UpsertForUser(context.Context, int64, []UserPlatformQuotaRecord) error {
	return s.err
}

// 配额上限写入成功才发布；写失败不发。
func TestPlatformQuotaWritesPublishAChange(t *testing.T) {
	hub := NewAccessChangeHub()
	var rec recordedChanges
	hub.Subscribe(rec.add)
	inner := &quotaRepoStub{}
	repo := NewObservedUserPlatformQuotaRepository(inner, hub)
	ctx := context.Background()

	inner.err = errors.New("db down")
	require.Error(t, repo.UpsertForUser(ctx, 11, nil))
	require.Empty(t, rec.got)

	inner.err = nil
	require.NoError(t, repo.UpsertForUser(ctx, 11, nil))
	require.Equal(t, []AccessChange{{Kind: AccessChangePlatformQuota, UserID: 11}}, rec.got)

	require.Same(t, inner, NewObservedUserPlatformQuotaRepository(inner, nil), "no hub, no wrapper")
}

// Key 被删除、停用、额度用尽时发布改动（主从分流收回它在从节点上的额度）；没挂中心和无效 ID 不发。
func TestAPIKeyAccessChangeIsPublished(t *testing.T) {
	svc := NewAPIKeyService(&authRepoStub{}, nil, nil, nil, nil, nil, &config.Config{})
	svc.publishAPIKeyAccessChange(5) // 没挂中心时照常工作
	hub := NewAccessChangeHub()
	var rec recordedChanges
	hub.Subscribe(rec.add)
	AttachAccessChangeHub(hub, svc, nil)

	svc.publishAPIKeyAccessChange(5)
	svc.publishAPIKeyAccessChange(0)
	require.Equal(t, []AccessChange{{Kind: AccessChangeAPIKey, KeyID: 5}}, rec.got)
}
