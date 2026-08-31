package service

import (
	"context"
	"sync"
	"testing"
)

func TestOpenAILatencyTrackerTailAndRollingWindow(t *testing.T) {
	tracker := newOpenAILatencyTracker()
	for _, sample := range []int{10, 20, 30, 40} {
		tracker.ObserveAccount(1, openAILatencyBucketNormal, sample)
	}
	if _, ok := tracker.AccountTail(1); ok {
		t.Fatal("tail should be unavailable before five samples")
	}
	// 5 个样本时 p90 的最近秩 = ceil(0.9*5)-1 = 4，即最大值。
	tracker.ObserveAccount(1, openAILatencyBucketNormal, 50)
	if tail, ok := tracker.AccountTail(1); !ok || tail != 50 {
		t.Fatalf("tail after five samples = (%d, %t), want (50, true)", tail, ok)
	}
	// 补到 11 个样本：排序后 p90 的最近秩 = ceil(0.9*11)-1 = 9，即第 10 小 = 100。
	for _, sample := range []int{60, 70, 80, 90, 100, 110} {
		tracker.ObserveAccount(1, openAILatencyBucketNormal, sample)
	}
	if tail, ok := tracker.AccountTail(1); !ok || tail != 100 {
		t.Fatalf("rolling tail = (%d, %t), want (100, true)", tail, ok)
	}
}

// 长尾必须能被检出：中位数低但尾部集中变慢时，p90 要反映尾部。
// 这正是线上「组中位数 6.6 秒、却存在成片 60~220 秒请求」时兜底不触发的场景。
//
// 同时固化灵敏度边界：p90 用最近秩，n=10 时落在第 9 小，
// 意味着要 ≥20% 的样本变慢才算「组变慢」。单个离群点不触发是**刻意**的——
// 10 个请求里 1 个慢就把整组流量切走属于过度反应，代价比收益大。
func TestOpenAILatencyTrackerTailDetectsSlowTailMedianMisses(t *testing.T) {
	t.Run("尾部成片变慢时触发", func(t *testing.T) {
		tracker := newOpenAILatencyTracker()
		// 7 快 + 3 慢：中位数仍是 6 秒（远低于 30 秒阈值），但 p90 命中慢样本。
		for i := 0; i < 7; i++ {
			tracker.ObserveGroup(5, openAILatencyBucketNormal, 6000)
		}
		for i := 0; i < 3; i++ {
			tracker.ObserveGroup(5, openAILatencyBucketNormal, 220000)
		}

		tail, bucket, ok := tracker.GroupTail(5)
		if !ok {
			t.Fatal("group tail should be available")
		}
		if bucket != openAILatencyBucketNormal {
			t.Fatalf("bucket = %q, want %q", bucket, openAILatencyBucketNormal)
		}
		if tail <= 30000 {
			t.Fatalf("tail = %d, want > 30000 so the slow tail triggers fallback", tail)
		}
	})

	t.Run("单个离群点不触发", func(t *testing.T) {
		tracker := newOpenAILatencyTracker()
		for i := 0; i < 9; i++ {
			tracker.ObserveGroup(6, openAILatencyBucketNormal, 6000)
		}
		tracker.ObserveGroup(6, openAILatencyBucketNormal, 220000)

		tail, _, ok := tracker.GroupTail(6)
		if !ok {
			t.Fatal("group tail should be available")
		}
		if tail > 30000 {
			t.Fatalf("tail = %d, want <= 30000: a lone outlier must not reroute the whole group", tail)
		}
	})
}

func TestOpenAILatencyTrackerIgnoresInvalidSamplesAndSeparatesGroups(t *testing.T) {
	tracker := newOpenAILatencyTracker()
	for _, sample := range []int{0, -1, 10, 20, 30, 40, 50, 60} {
		tracker.ObserveAccount(7, openAILatencyBucketNormal, sample)
	}
	for _, sample := range []int{100, 200, 300, 400, 500} {
		tracker.ObserveGroup(9, openAILatencyBucketNormal, sample)
	}
	if tail, ok := tracker.AccountTail(7); !ok || tail != 60 {
		t.Fatalf("account tail = (%d, %t), want (60, true)", tail, ok)
	}
	if tail, ok := tracker.GroupTailForBucket(9, openAILatencyBucketNormal); !ok || tail != 500 {
		t.Fatalf("group tail = (%d, %t), want (500, true)", tail, ok)
	}
	if _, _, ok := tracker.GroupTail(7); ok {
		t.Fatal("account samples must not leak into a group window")
	}
}

// 分桶隔离：高强度样本不得污染普通档读数，反之亦然。
func TestOpenAILatencyTrackerSeparatesEffortBuckets(t *testing.T) {
	tracker := newOpenAILatencyTracker()
	for i := 0; i < 5; i++ {
		tracker.ObserveGroup(3, openAILatencyBucketNormal, 5000)
		tracker.ObserveGroup(3, openAILatencyBucketHigh, 90000)
	}

	normal, ok := tracker.GroupTailForBucket(3, openAILatencyBucketNormal)
	if !ok || normal != 5000 {
		t.Fatalf("normal bucket tail = (%d, %t), want (5000, true)", normal, ok)
	}
	high, ok := tracker.GroupTailForBucket(3, openAILatencyBucketHigh)
	if !ok || high != 90000 {
		t.Fatalf("high bucket tail = (%d, %t), want (90000, true)", high, ok)
	}
	// GroupTail 取最差分桶。
	worst, bucket, ok := tracker.GroupTail(3)
	if !ok || worst != 90000 || bucket != openAILatencyBucketHigh {
		t.Fatalf("worst bucket = (%d, %q, %t), want (90000, high, true)", worst, bucket, ok)
	}
}

func TestOpenAILatencyBucketFor(t *testing.T) {
	for _, effort := range []string{"high", "xhigh", "max", "HIGH", " XHigh "} {
		if got := openAILatencyBucketFor(effort); got != openAILatencyBucketHigh {
			t.Fatalf("bucket(%q) = %q, want high", effort, got)
		}
	}
	for _, effort := range []string{"", "low", "medium", "minimal", "unknown"} {
		if got := openAILatencyBucketFor(effort); got != openAILatencyBucketNormal {
			t.Fatalf("bucket(%q) = %q, want normal", effort, got)
		}
	}
}

func TestSplitOpenAIAccountCandidatesByLatencyKeepsUnknownCandidates(t *testing.T) {
	tracker := newOpenAILatencyTracker()
	for _, sample := range []int{40000, 40000, 40000, 40000, 40000} {
		tracker.ObserveAccount(2, openAILatencyBucketNormal, sample)
	}
	pool := []openAIAccountCandidateScore{
		{account: &Account{ID: 1}},
		{account: &Account{ID: 2}},
	}
	healthy, slow := splitOpenAIAccountCandidatesByLatency(pool, tracker, 30000)
	if len(healthy) != 1 || healthy[0].account.ID != 1 {
		t.Fatalf("healthy candidates = %#v, want unknown account 1", healthy)
	}
	if len(slow) != 1 || slow[0].account.ID != 2 {
		t.Fatalf("slow candidates = %#v, want account 2", slow)
	}
}

func TestSplitOpenAIAccountCandidatesByLatencyModes(t *testing.T) {
	makePool := func(ids ...int64) []openAIAccountCandidateScore {
		pool := make([]openAIAccountCandidateScore, 0, len(ids))
		for _, id := range ids {
			pool = append(pool, openAIAccountCandidateScore{account: &Account{ID: id}})
		}
		return pool
	}
	observe := func(tracker *openAILatencyTracker, id int64, sample int) {
		for i := 0; i < 5; i++ {
			tracker.ObserveAccount(id, openAILatencyBucketNormal, sample)
		}
	}

	tests := []struct {
		name        string
		healthyIDs  []int64
		slowIDs     []int64
		unknownIDs  []int64
		wantHealthy []int64
		wantSlow    []int64
	}{
		{
			name:        "全健康",
			healthyIDs:  []int64{1, 2},
			wantHealthy: []int64{1, 2},
		},
		{
			name:     "全慢",
			slowIDs:  []int64{3, 4},
			wantSlow: []int64{3, 4},
		},
		{
			name:        "混合",
			healthyIDs:  []int64{5},
			slowIDs:     []int64{6},
			wantHealthy: []int64{5},
			wantSlow:    []int64{6},
		},
		{
			name:        "无样本按健康处理",
			unknownIDs:  []int64{7, 8},
			wantHealthy: []int64{7, 8},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newOpenAILatencyTracker()
			for _, id := range tc.healthyIDs {
				observe(tracker, id, 20000)
			}
			for _, id := range tc.slowIDs {
				observe(tracker, id, 40000)
			}
			pool := makePool(append(append(append([]int64{}, tc.healthyIDs...), tc.slowIDs...), tc.unknownIDs...)...)
			healthy, slow := splitOpenAIAccountCandidatesByLatency(pool, tracker, 30000)
			gotHealthy := make([]int64, 0, len(healthy))
			for _, candidate := range healthy {
				gotHealthy = append(gotHealthy, candidate.account.ID)
			}
			gotSlow := make([]int64, 0, len(slow))
			for _, candidate := range slow {
				gotSlow = append(gotSlow, candidate.account.ID)
			}
			if len(gotHealthy) != len(tc.wantHealthy) {
				t.Fatalf("healthy IDs = %v, want %v", gotHealthy, tc.wantHealthy)
			}
			for i, id := range tc.wantHealthy {
				if gotHealthy[i] != id {
					t.Fatalf("healthy IDs = %v, want %v", gotHealthy, tc.wantHealthy)
				}
			}
			if len(gotSlow) != len(tc.wantSlow) {
				t.Fatalf("slow IDs = %v, want %v", gotSlow, tc.wantSlow)
			}
			for i, id := range tc.wantSlow {
				if gotSlow[i] != id {
					t.Fatalf("slow IDs = %v, want %v", gotSlow, tc.wantSlow)
				}
			}
		})
	}
}

func newLatencyFallbackTestService(tracker *openAILatencyTracker) *OpenAIGatewayService {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		latencyAwareFallbackEnabled: true,
		latencyThresholdMs:          30000,
		fallbackSpeedupRatio:        0.6,
		expiresAt:                   1<<63 - 1,
	})
	return &OpenAIGatewayService{openaiLatencyTracker: tracker}
}

func TestOpenAILatencyFallbackRequiresSpeedup(t *testing.T) {
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	tracker := newOpenAILatencyTracker()
	for i := 0; i < 5; i++ {
		tracker.ObserveGroup(1, openAILatencyBucketNormal, 40000)
		tracker.ObserveGroup(2, openAILatencyBucketNormal, 20000)
	}
	svc := newLatencyFallbackTestService(tracker)

	if !svc.shouldUseOpenAILatencyFallbackGroup(1, 2, openAILatencyBucketNormal) {
		t.Fatal("fallback should be allowed when the target is meaningfully faster")
	}

	for i := 0; i < 5; i++ {
		tracker.ObserveGroup(2, openAILatencyBucketNormal, 30000)
	}
	if svc.shouldUseOpenAILatencyFallbackGroup(1, 2, openAILatencyBucketNormal) {
		t.Fatal("fallback should be rejected when target is not 40% faster")
	}
}

// 兜底组没有样本时必须放行。
//
// 历史实现在这里拒绝，导致冷启动死锁：兜底组没流量 → 攒不到样本 → 不给读数 →
// 不切过去 → 永远没流量。源组已确认变慢的前提下，切到未知池子不会更糟，
// 而且能把样本喂起来让下次判断有据可依。
func TestOpenAILatencyFallbackAllowsUnmeasuredTarget(t *testing.T) {
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	tracker := newOpenAILatencyTracker()
	for i := 0; i < 5; i++ {
		tracker.ObserveGroup(1, openAILatencyBucketNormal, 40000)
	}
	svc := newLatencyFallbackTestService(tracker)

	if !svc.shouldUseOpenAILatencyFallbackGroup(1, 3, openAILatencyBucketNormal) {
		t.Fatal("fallback must be allowed when the target has no samples yet")
	}
}

// 比较必须同桶对齐：源组高强度档慢，不能拿兜底组普通档的读数来判定。
func TestOpenAILatencyFallbackComparesWithinSameBucket(t *testing.T) {
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	tracker := newOpenAILatencyTracker()
	for i := 0; i < 5; i++ {
		tracker.ObserveGroup(1, openAILatencyBucketHigh, 90000)
		// 兜底组只有普通档样本，且很快；不得被误用来放行高强度档。
		tracker.ObserveGroup(2, openAILatencyBucketNormal, 1000)
		// 兜底组高强度档同样慢。
		tracker.ObserveGroup(2, openAILatencyBucketHigh, 88000)
	}
	svc := newLatencyFallbackTestService(tracker)

	if svc.shouldUseOpenAILatencyFallbackGroup(1, 2, openAILatencyBucketHigh) {
		t.Fatal("high-effort comparison must use the high bucket, where the target is not faster")
	}
	if _, ok := tracker.GroupTailForBucket(1, openAILatencyBucketNormal); ok {
		t.Fatal("source group must not have normal-bucket samples in this fixture")
	}
}

func TestOpenAILatencyFallbackContextMarker(t *testing.T) {
	bucket, ok := isOpenAILatencyFallbackTrigger(withOpenAILatencyFallbackTrigger(context.Background(), openAILatencyBucketHigh))
	if !ok || bucket != openAILatencyBucketHigh {
		t.Fatalf("marker = (%q, %t), want (high, true)", bucket, ok)
	}
	if _, ok := isOpenAILatencyFallbackTrigger(
		withOpenAILatencyFallbackSuppressed(withOpenAILatencyFallbackTrigger(context.Background(), openAILatencyBucketHigh)),
	); ok {
		t.Fatal("suppressed latency fallback context marker must stop recursive latency fallback")
	}
	if _, ok := isOpenAILatencyFallbackTrigger(context.Background()); ok {
		t.Fatal("plain context must not look like a latency fallback trigger")
	}
}

func TestOpenAILatencyTrackerConcurrentObservationAndReads(t *testing.T) {
	tracker := newOpenAILatencyTracker()
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for i := 0; i < 1000; i++ {
				tracker.ObserveAccount(int64(worker+1), openAILatencyBucketNormal, 100+i%20)
				tracker.ObserveGroup(int64(worker+1), openAILatencyBucketHigh, 100+i%20)
				_, _ = tracker.AccountTail(int64(worker + 1))
				_, _, _ = tracker.GroupTail(int64(worker + 1))
			}
		}(worker)
	}
	group.Wait()
}

func TestOpenAILatencyTrackerObserveRecordsBothDimensions(t *testing.T) {
	tracker := newOpenAILatencyTracker()
	for i := 0; i < 5; i++ {
		tracker.Observe(11, 22, openAILatencyBucketNormal, 1234)
	}
	if tail, ok := tracker.AccountTail(11); !ok || tail != 1234 {
		t.Fatalf("account tail = (%d, %t), want (1234, true)", tail, ok)
	}
	if tail, ok := tracker.GroupTailForBucket(22, openAILatencyBucketNormal); !ok || tail != 1234 {
		t.Fatalf("group tail = (%d, %t), want (1234, true)", tail, ok)
	}
}
