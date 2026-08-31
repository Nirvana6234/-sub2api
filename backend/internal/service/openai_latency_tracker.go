package service

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 窗口取 20 而不是 10：判定统计量从中位数改成 p90 之后，样本太少会让单个离群点
// 直接决定结论。20 个样本下 p90 约等于「第 2 慢」，既能反映长尾又不至于一跳就翻。
const openAILatencySampleWindowSize = 20

// openAILatencyTailPercentile 是判定「组是否变慢」使用的分位。
//
// 原实现用中位数，对长尾免疫：一个组 10% 的请求 200 秒、中位数 6 秒，
// 显然已经病了，但中位数触发永远不会响——要它响得等一半以上请求都超阈值，
// 那时整池已经塌方。p90 直接表达「至少 10% 的请求超过阈值」。
const openAILatencyTailPercentile = 0.9

const (
	defaultOpenAILatencyThresholdMs   = 30000
	defaultOpenAIFallbackSpeedupRatio = 0.6
	minOpenAILatencyThresholdMs       = 1000
	maxOpenAILatencyThresholdMs       = 600000
	minOpenAIFallbackSpeedupRatio     = 0.1
	maxOpenAIFallbackSpeedupRatio     = 1.0
	openAILatencyTrackerTTL           = time.Hour
	openAILatencyTrackerSweepInterval = 10 * time.Minute
	// openAILatencyMinSamples 是给出读数所需的最小样本数。
	openAILatencyMinSamples = 5
)

// 推理强度分桶。
//
// 不同强度的 TTFT 基线差一个量级（实测同一组内 max 的 p50 约 6 秒、xhigh 约 12 秒、
// low 的长尾能到 350 秒），把它们混在一个窗口里，「组延迟」反映的是流量构成而不是
// 健康度，跨组比较更是拿苹果比橘子。这里按强度分桶，比较时同桶对齐。
//
// 分桶口径与 openAIFirstOutputTimeout 的高强度判定保持一致（high/xhigh/max），
// 避免两套机制对「什么算高强度」有不同理解。
const (
	openAILatencyBucketNormal = "normal"
	openAILatencyBucketHigh   = "high"
)

// openAILatencyBucketFor 把 reasoning_effort 归一化到分桶。
func openAILatencyBucketFor(reasoningEffort string) string {
	switch strings.ToLower(strings.TrimSpace(reasoningEffort)) {
	case "high", "xhigh", "max":
		return openAILatencyBucketHigh
	default:
		return openAILatencyBucketNormal
	}
}

func openAILatencyBuckets() []string {
	return []string{openAILatencyBucketNormal, openAILatencyBucketHigh}
}

// openAILatencyKey 是样本窗口的键：实体 ID + 强度桶。
type openAILatencyKey struct {
	id     int64
	bucket string
}

func parseOpenAILatencyThresholdMs(raw string) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < minOpenAILatencyThresholdMs || value > maxOpenAILatencyThresholdMs {
		return defaultOpenAILatencyThresholdMs
	}
	return value
}

func parseOpenAIFallbackSpeedupRatio(raw string) float64 {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < minOpenAIFallbackSpeedupRatio || value > maxOpenAIFallbackSpeedupRatio {
		return defaultOpenAIFallbackSpeedupRatio
	}
	return value
}

func normalizeOpenAILatencyThresholdMs(value int) int {
	if value == 0 {
		return defaultOpenAILatencyThresholdMs
	}
	return value
}

func normalizeOpenAIFallbackSpeedupRatio(value float64) float64 {
	if value == 0 {
		return defaultOpenAIFallbackSpeedupRatio
	}
	return value
}

// openAILatencyTracker keeps bounded recent TTFT samples for accounts and groups,
// segmented by reasoning-effort bucket.
type openAILatencyTracker struct {
	accounts  sync.Map // map[openAILatencyKey]*openAILatencyWindow
	groups    sync.Map // map[openAILatencyKey]*openAILatencyWindow
	lastSweep atomic.Int64
}

type openAILatencyWindow struct {
	mu                   sync.RWMutex
	values               [openAILatencySampleWindowSize]int
	next                 int
	count                int
	lastObservedUnixNano atomic.Int64
}

func newOpenAILatencyTracker() *openAILatencyTracker {
	return &openAILatencyTracker{}
}

func (t *openAILatencyTracker) ObserveAccount(accountID int64, bucket string, ttftMs int) {
	if t == nil || accountID <= 0 || ttftMs <= 0 {
		return
	}
	t.observe(&t.accounts, openAILatencyKey{id: accountID, bucket: bucket}, ttftMs)
}

// ObserveGroup 记录一次样本到指定分组。
//
// 调用方必须传入**本次请求实际服务的分组**。历史实现把样本扇出到账号所属的
// 每一个分组，导致「同一账号同时属于主组和兜底组」这种完全合法的编排下，
// 兜底组的延迟画像被主组流量污染，两组读数强相关，speedup 比较永远判定
// 「兜底组不够快」。归属必须跟着真实调度走，而不是跟着账号的组成员关系走。
func (t *openAILatencyTracker) ObserveGroup(groupID int64, bucket string, ttftMs int) {
	if t == nil || groupID <= 0 || ttftMs <= 0 {
		return
	}
	t.observe(&t.groups, openAILatencyKey{id: groupID, bucket: bucket}, ttftMs)
}

func (t *openAILatencyTracker) Observe(accountID, groupID int64, bucket string, ttftMs int) {
	if t == nil || ttftMs <= 0 {
		return
	}
	t.ObserveAccount(accountID, bucket, ttftMs)
	t.ObserveGroup(groupID, bucket, ttftMs)
}

// AccountTail 返回账号最慢分桶的 p90。
//
// 调度排序时拿不到本次请求的推理强度（强度要等请求体解析后才知道，而排序发生在
// 选账号阶段），所以这里取所有分桶里最差的那个：任一强度档慢，就认为该账号慢。
func (t *openAILatencyTracker) AccountTail(accountID int64) (int, bool) {
	if t == nil || accountID <= 0 {
		return 0, false
	}
	return t.worstTail(&t.accounts, accountID)
}

// GroupTail 返回分组最慢分桶的 p90，以及该分桶名。
func (t *openAILatencyTracker) GroupTail(groupID int64) (int, string, bool) {
	if t == nil || groupID <= 0 {
		return 0, "", false
	}
	worst := 0
	worstBucket := ""
	found := false
	for _, bucket := range openAILatencyBuckets() {
		value, _ := t.groups.Load(openAILatencyKey{id: groupID, bucket: bucket})
		tail, ok := openAILatencyWindowTail(value)
		if !ok {
			continue
		}
		if !found || tail > worst {
			worst = tail
			worstBucket = bucket
			found = true
		}
	}
	return worst, worstBucket, found
}

// GroupTailForBucket 返回分组在指定分桶上的 p90。
func (t *openAILatencyTracker) GroupTailForBucket(groupID int64, bucket string) (int, bool) {
	if t == nil || groupID <= 0 {
		return 0, false
	}
	value, _ := t.groups.Load(openAILatencyKey{id: groupID, bucket: bucket})
	return openAILatencyWindowTail(value)
}

func (t *openAILatencyTracker) worstTail(windows *sync.Map, id int64) (int, bool) {
	worst := 0
	found := false
	for _, bucket := range openAILatencyBuckets() {
		value, _ := windows.Load(openAILatencyKey{id: id, bucket: bucket})
		tail, ok := openAILatencyWindowTail(value)
		if !ok {
			continue
		}
		if !found || tail > worst {
			worst = tail
			found = true
		}
	}
	return worst, found
}

func (t *openAILatencyTracker) observe(windows *sync.Map, key openAILatencyKey, ttftMs int) {
	openAILatencyWindowFor(windows, key).observe(ttftMs)
	t.cleanupExpired(time.Now())
}

func openAILatencyWindowFor(windows *sync.Map, key openAILatencyKey) *openAILatencyWindow {
	if window, ok := windows.Load(key); ok {
		return window.(*openAILatencyWindow)
	}
	window := &openAILatencyWindow{}
	actual, _ := windows.LoadOrStore(key, window)
	return actual.(*openAILatencyWindow)
}

func (w *openAILatencyWindow) observe(ttftMs int) {
	w.mu.Lock()
	w.values[w.next] = ttftMs
	w.next = (w.next + 1) % openAILatencySampleWindowSize
	if w.count < openAILatencySampleWindowSize {
		w.count++
	}
	w.mu.Unlock()
	w.lastObservedUnixNano.Store(time.Now().UnixNano())
}

func (t *openAILatencyTracker) cleanupExpired(now time.Time) {
	if t == nil {
		return
	}
	nowUnixNano := now.UnixNano()
	lastSweep := t.lastSweep.Load()
	if lastSweep > 0 && nowUnixNano-lastSweep < openAILatencyTrackerSweepInterval.Nanoseconds() {
		return
	}
	if !t.lastSweep.CompareAndSwap(lastSweep, nowUnixNano) {
		return
	}
	cutoff := nowUnixNano - openAILatencyTrackerTTL.Nanoseconds()
	expire := func(windows *sync.Map) {
		windows.Range(func(key, value any) bool {
			window, ok := value.(*openAILatencyWindow)
			if !ok || window == nil || window.lastObservedUnixNano.Load() < cutoff {
				windows.Delete(key)
			}
			return true
		})
	}
	expire(&t.accounts)
	expire(&t.groups)
}

// openAILatencyWindowTail 返回窗口的 p90（向上取整到最近的样本），
// 样本不足 openAILatencyMinSamples 时不给读数。
func openAILatencyWindowTail(value any) (int, bool) {
	w, ok := value.(*openAILatencyWindow)
	if !ok || w == nil {
		return 0, false
	}
	w.mu.RLock()
	if w.count < openAILatencyMinSamples {
		w.mu.RUnlock()
		return 0, false
	}
	values := append([]int(nil), w.values[:w.count]...)
	w.mu.RUnlock()
	sort.Ints(values)

	// 最近秩法：index = ceil(p * n) - 1，保证 p90 落在真实样本上且不会取到越界。
	index := int(math.Ceil(openAILatencyTailPercentile*float64(len(values)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index], true
}
