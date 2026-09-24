package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type bulkGroupPriorityRepoStub struct {
	AccountRepository
	bindCalls       []int64
	bindErrByID     map[int64]error
	priorityUpdates []AccountGroupPriorityUpdate
	events          []string
}

func (r *bulkGroupPriorityRepoStub) BulkUpdate(_ context.Context, ids []int64, _ AccountBulkUpdate) (int64, error) {
	return int64(len(ids)), nil
}

func (r *bulkGroupPriorityRepoStub) BindGroups(_ context.Context, accountID int64, _ []int64) error {
	r.events = append(r.events, "bind")
	r.bindCalls = append(r.bindCalls, accountID)
	return r.bindErrByID[accountID]
}

func (r *bulkGroupPriorityRepoStub) UpdateGroupPriorities(_ context.Context, updates []AccountGroupPriorityUpdate) (int, error) {
	r.events = append(r.events, "priority")
	r.priorityUpdates = append(r.priorityUpdates, updates...)
	return len(updates), nil
}

type bulkGroupPriorityGroupRepoStub struct {
	GroupRepository
}

func (bulkGroupPriorityGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	return &Group{ID: id, Status: StatusActive, Platform: PlatformOpenAI}, nil
}

func TestBulkUpdateAccountsSetsInGroupPriorityWithoutTouchingGlobalPriority(t *testing.T) {
	repo := &bulkGroupPriorityRepoStub{}
	svc := &adminServiceImpl{accountRepo: repo}

	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs:    []int64{7, 8},
		GroupPriority: &BulkGroupPriorityUpdate{GroupID: 23, Priority: 5},
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.Success)
	require.Equal(t, []AccountGroupPriorityUpdate{
		{AccountID: 7, GroupID: 23, Priority: 5},
		{AccountID: 8, GroupID: 23, Priority: 5},
	}, repo.priorityUpdates)
}

func TestBulkUpdateAccountsWritesInGroupPriorityAfterRebindingAndSkipsFailures(t *testing.T) {
	repo := &bulkGroupPriorityRepoStub{bindErrByID: map[int64]error{8: errors.New("bind failed")}}
	svc := &adminServiceImpl{accountRepo: repo, groupRepo: bulkGroupPriorityGroupRepoStub{}}
	groupIDs := []int64{23}

	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs:            []int64{7, 8},
		GroupIDs:              &groupIDs,
		GroupPriority:         &BulkGroupPriorityUpdate{GroupID: 23, Priority: 10000},
		SkipMixedChannelCheck: true,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{7}, result.SuccessIDs)
	require.Equal(t, []AccountGroupPriorityUpdate{{AccountID: 7, GroupID: 23, Priority: 10000}}, repo.priorityUpdates)
	require.Equal(t, "priority", repo.events[len(repo.events)-1], "in-group priority is written after rebinding")
}
