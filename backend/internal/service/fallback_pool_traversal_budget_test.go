// 这些回归用例来自 2026-09-24 对自动分组/兜底池改动的外部评审复现。

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type regressionGraphAccountRepo struct {
	*anthropicFallbackAccountRepo
	calls []int64
}

func (r *regressionGraphAccountRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]Account, error) {
	r.calls = append(r.calls, groupID)
	return r.anthropicFallbackAccountRepo.ListSchedulableByGroupIDAndPlatforms(ctx, groupID, platforms)
}

type regressionGraphConcurrencyCache struct{ ConcurrencyCache }

func (*regressionGraphConcurrencyCache) GetAccountsLoadBatch(_ context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	out := make(map[int64]*AccountLoadInfo, len(accounts))
	for _, account := range accounts {
		load := 0
		if account.ID == 201 {
			load = 100
		}
		out[account.ID] = &AccountLoadInfo{AccountID: account.ID, LoadRate: load}
	}
	return out, nil
}

func regressionGraphService(groups map[int64]*Group, accounts map[int64][]Account, loadAware bool) (*GatewayService, *regressionGraphAccountRepo) {
	repo := &regressionGraphAccountRepo{anthropicFallbackAccountRepo: &anthropicFallbackAccountRepo{byGroup: accounts}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.Scheduling = config.GatewaySchedulingConfig{LoadBatchEnabled: loadAware, FallbackWaitTimeout: time.Second, FallbackMaxWaiting: 3}
	svc := &GatewayService{accountRepo: repo, groupRepo: &anthropicFallbackGroupRepo{groups: groups}, cfg: cfg}
	if loadAware {
		svc.concurrencyService = NewConcurrencyService(&regressionGraphConcurrencyCache{})
	}
	return svc, repo
}

// Regression assertion: a shared descendant must not be selected twice within one request.
func TestGraphSharedDescendantIsAttemptedOnce(t *testing.T) {
	groups := map[int64]*Group{
		1: {ID: 1, Platform: PlatformAnthropic, Status: StatusActive, FallbackGroupIDs: []int64{2, 3}},
		2: {ID: 2, Platform: PlatformAnthropic, Status: StatusActive, IsFallbackPool: true, FallbackGroupIDs: []int64{4}},
		3: {ID: 3, Platform: PlatformAnthropic, Status: StatusActive, IsFallbackPool: true, FallbackGroupIDs: []int64{4}},
		4: {ID: 4, Platform: PlatformAnthropic, Status: StatusActive, IsFallbackPool: true},
	}
	for _, loadAware := range []bool{false, true} {
		name := "legacy"
		if loadAware {
			name = "load_aware"
		}
		t.Run(name, func(t *testing.T) {
			svc, repo := regressionGraphService(groups, nil, loadAware)
			groupID := int64(1)
			_, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil, "", 0)
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			t.Logf("selection order: %v", repo.calls)
			require.Equal(t, []int64{1, 2, 4, 3}, repo.calls)
		})
	}
}

// 三跳只限深度不限宽度：宽度 3、深度 3 的配置不设预算时要串行选号 40 次。
// 请求级尝试预算把兜底分组的尝试数封顶，源组那一次之外最多 fallbackGroupMaxAttempts 次。
func TestFallbackAttemptBudgetBoundsWideTrees(t *testing.T) {
	groups := make(map[int64]*Group)
	nextID := int64(0)
	var addTree func(int) int64
	addTree = func(depth int) int64 {
		nextID++
		id := nextID
		group := &Group{ID: id, Platform: PlatformAnthropic, Status: StatusActive, IsFallbackPool: true}
		groups[id] = group
		if depth < fallbackGroupMaxHops {
			for i := 0; i < 3; i++ {
				group.FallbackGroupIDs = append(group.FallbackGroupIDs, addTree(depth+1))
			}
		}
		return id
	}
	sourceID := addTree(0)
	svc, repo := regressionGraphService(groups, nil, true)
	_, err := svc.SelectAccountWithLoadAwareness(context.Background(), &sourceID, "", "", nil, "", 0)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Len(t, repo.calls, 1+fallbackGroupMaxAttempts)
}

// Passing contract: a wait plan is a successful selection, and must not be discarded for C.
func TestGraphGatewayWaitPlanStopsSiblingAttempts(t *testing.T) {
	groups := map[int64]*Group{
		1: {ID: 1, Platform: PlatformAnthropic, Status: StatusActive, FallbackGroupIDs: []int64{2, 3}},
		2: {ID: 2, Platform: PlatformAnthropic, Status: StatusActive, IsFallbackPool: true},
		3: {ID: 3, Platform: PlatformAnthropic, Status: StatusActive, IsFallbackPool: true},
	}
	accounts := map[int64][]Account{
		2: {{ID: 201, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, RateMultiplier: gatewayFallbackFloatPtr(1)}},
		3: {{ID: 301, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, RateMultiplier: gatewayFallbackFloatPtr(1)}},
	}
	svc, repo := regressionGraphService(groups, accounts, true)
	groupID := int64(1)
	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(201), selection.Account.ID)
	require.NotNil(t, selection.WaitPlan)
	require.False(t, selection.Acquired)
	require.Nil(t, selection.ReleaseFunc)
	require.Equal(t, []int64{1, 2}, repo.calls)
	require.NotNil(t, selection.fallbackTrace)
	require.Equal(t, int64(1), selection.fallbackTrace.SourceGroupID)
	require.Equal(t, int64(2), selection.fallbackTrace.TargetGroupID)
}
