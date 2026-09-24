package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type priceReviewPeerMetricsRepo struct {
	peer map[int64]map[int64][]int64
}

func (priceReviewPeerMetricsRepo) ListRecentGroupFirstTokenSamples(context.Context, int64, []int64, string, time.Time, int) (map[int64][]int64, error) {
	return nil, nil
}

func (r priceReviewPeerMetricsRepo) ListRecentPeerGroupFirstTokenSamples(context.Context, []int64, string, time.Time, int) (map[int64]map[int64][]int64, error) {
	return r.peer, nil
}

func priceReviewGroups() []Group {
	return []Group{
		{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: .10, ActiveAccountCount: 1},
		{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: .20, ActiveAccountCount: 1},
		{ID: 30, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: .30, ActiveAccountCount: 1},
	}
}

// 把 key 的选择预置为「已定在 groupID 上、价格复查已到期」。
func settleForPriceReview(svc *APIKeyService, key *APIKey, model string, groupID int64) {
	for _, group := range priceReviewGroups() {
		if group.ID != groupID {
			continue
		}
		snapshot := group
		svc.storeAutoGroupSelection(autoGroupSelectionKey(key, model), autoGroupSelection{
			groupID:           groupID,
			selectedGroup:     &snapshot,
			configFingerprint: autoGroupConfigFingerprint(key),
			settled:           true,
			priceReviewAt:     time.Now().Add(-time.Second),
		})
	}
}

// price 策略下，价格复查只验证出 B（0.20）可靠时，必须切到 B，而不是全体最便宜、
// 从未被验证过的 A（0.10）——A 可能正是此前让这个 key 切走的故障组。
func TestAutoGroupPriceReviewCommitsPeerVerifiedGroup(t *testing.T) {
	const model = "gpt-price-review"
	svc := newFailoverPersistService(priceReviewGroups())
	svc.SetAutoGroupMetricRepository(priceReviewPeerMetricsRepo{peer: map[int64]map[int64][]int64{
		20: {99: {1000, 1100, 1200, 1300}},
	}})
	key := &APIKey{ID: 4401, UserID: 7, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyPrice, AutoGroupIDs: []int64{10, 20, 30}}
	settleForPriceReview(svc, key, model, 30)

	resolved, err := svc.ResolveAutoGroupForModel(context.Background(), key, model)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := *resolved.GroupID; got != 20 {
		t.Fatalf("price review switched to group %d, want the peer-verified group 20", got)
	}
}

// 价格复查没有发现更便宜的已验证组时，会把同一个选择写回并推迟下次复查。
// 路由决策没变，revision 不因统计计数推进；写回时必须保留快照之后才到达的
// 故障计数，否则这次失败被抹掉，自动切组会被推迟。
func TestAutoGroupPriceReviewDeferralKeepsConcurrentFailureCount(t *testing.T) {
	const model = "gpt-price-review"
	svc := newFailoverPersistService(priceReviewGroups())
	key := &APIKey{ID: 4402, UserID: 7, AutoGroup: true, AutoGroupStrategy: autoGroupStrategyPrice, AutoGroupIDs: []int64{10, 20, 30}}
	settleForPriceReview(svc, key, model, 10)

	onTen := resolveAPIKeyWithAutoGroup(key, &Group{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: .10, ActiveAccountCount: 1})
	base := svc.groupRepo.(failoverPersistGroupRepo)
	svc.groupRepo = regressionHookGroupRepo{failoverPersistGroupRepo: base, hook: func() {
		// 解析器取完快照后，同组另一个请求报告了一次（未达阈值的）失败。
		svc.ObserveAutoGroupRequestResult(onTen, model, 503, nil)
	}}

	resolved, err := svc.ResolveAutoGroupForModel(context.Background(), key, model)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := *resolved.GroupID; got != 10 {
		t.Fatalf("resolved group %d, want the settled group 10", got)
	}
	stored := svc.autoGroupSelection(autoGroupSelectionKey(key, model))
	if stored.transientFailureStreak != 1 {
		t.Fatalf("transientFailureStreak = %d after price review write-back, want 1", stored.transientFailureStreak)
	}
	if stored.priceReviewAt.Before(time.Now()) {
		t.Fatalf("price review was not deferred: priceReviewAt=%v", stored.priceReviewAt)
	}
}

// handler 与兜底错误聚合共用 NoAccountRateLimitedCount，同一个错误在两边必须同一分类。
func TestNoAccountRateLimitedCountMatchesHandlerContract(t *testing.T) {
	cases := map[string]int{
		"no available accounts (rate_limited=3)":       3,
		"no available accounts (rate_limited=01)":      1,
		"no available accounts (model_rate_limited=2)": 2,
		"no available accounts (rate_limited=0)":       0,
		"no available accounts":                        0,
	}
	for text, want := range cases {
		if got := NoAccountRateLimitedCount(errors.New(text)); got != want {
			t.Errorf("NoAccountRateLimitedCount(%q) = %d, want %d", text, got, want)
		}
	}
	if NoAccountRateLimitedCount(nil) != 0 {
		t.Error("nil error must count as 0")
	}
}

// 多个池子都没号时保留信息量最大的错误；同级保留先出现的（源组的错误先参与比较）。
func TestPreferNoAccountErrorKeepsMostInformative(t *testing.T) {
	source := fmt.Errorf("%w (rate_limited=01)", ErrNoAvailableAccounts)
	plain := ErrNoAvailableAccounts
	compact := ErrNoAvailableCompactAccounts

	if got := preferNoAccountError(preferNoAccountError(nil, source), plain); got != source {
		t.Fatalf("source rate-limit diagnosis was replaced by %v", got)
	}
	if got := preferNoAccountError(plain, compact); got != compact {
		t.Fatalf("compact classification lost: %v", got)
	}
	if got := preferNoAccountError(compact, source); got != source {
		t.Fatalf("rate-limit diagnosis must outrank compact: %v", got)
	}
	first := fmt.Errorf("%w: first", ErrNoAvailableAccounts)
	if got := preferNoAccountError(first, plain); got != first {
		t.Fatalf("ties must keep the earlier error, got %v", got)
	}
}
