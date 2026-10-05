package nodegw

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
)

// 从节点的 API Key 负缓存（设计 8.2）：主节点回"查不到这个 Key"（401 INVALID_API_KEY）时，把这次拒绝记 30 秒，
// 期间同一个 Key 的请求在从节点本地直接拒绝，不再去主节点——随机 Key 攻击压不到主节点。
//
// 只缓存"查不到"这一种拒绝，不缓存别的：Key 的状态、额度、余额、IP 白名单、节点规则都会变，每次请求仍由主节点复查
// （准入就是主节点上整条鉴权链的一次评估，选号时还会再查一遍），"查到了"的结果不在从节点缓存。
// 缓存键是原始 Key 的 SHA-256（与主节点 Key 鉴权缓存同一个键，作废推送按它清），原文不留。
// 新建 Key 时主节点推送它的哈希，清掉对应条目，刚建的 Key 马上能用；事件流断开重连、主节点纪元变化时整个清空。

const (
	// DefaultKeyNegativeTTL 是负缓存的默认时间。
	DefaultKeyNegativeTTL = 30 * time.Second
	// negativeKeyCacheMax 是条目上限：满了先清过期的，还满就整个清空（攻击者能灌的只是自己的未命中）。
	negativeKeyCacheMax = 50000
)

type negativeKeyCache struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[negativeKey]negativeEntry
}

// negativeKey 带上入口格式：同一个 Key 在 Google 入口和 OpenAI / Anthropic 入口的拒绝写法不同。
type negativeKey struct {
	hash   string
	google bool
}

type negativeEntry struct {
	rejection *relayv1.SelectRejection
	expires   time.Time
}

func newNegativeKeyCache(ttl time.Duration, now func() time.Time) *negativeKeyCache {
	if ttl == 0 {
		ttl = DefaultKeyNegativeTTL
	}
	if now == nil {
		now = time.Now
	}
	return &negativeKeyCache{ttl: ttl, now: now, entries: map[negativeKey]negativeEntry{}}
}

// keyHash 是主节点 Key 鉴权缓存用的键（原始 Key 的 SHA-256 十六进制）。
func keyHash(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}

// get 返回还没过期的"查不到"拒绝。
func (n *negativeKeyCache) get(hash string, google bool) *relayv1.SelectRejection {
	if n == nil || n.ttl < 0 {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	k := negativeKey{hash, google}
	e, ok := n.entries[k]
	if !ok {
		return nil
	}
	if !n.now().Before(e.expires) {
		delete(n.entries, k)
		return nil
	}
	return e.rejection
}

// put 记一次"查不到"拒绝。
func (n *negativeKeyCache) put(hash string, google bool, rej *relayv1.SelectRejection) {
	if n == nil || n.ttl < 0 || rej == nil {
		return
	}
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.entries) >= negativeKeyCacheMax {
		for k, e := range n.entries {
			if !now.Before(e.expires) {
				delete(n.entries, k)
			}
		}
		if len(n.entries) >= negativeKeyCacheMax {
			n.entries = map[negativeKey]negativeEntry{}
		}
	}
	n.entries[negativeKey{hash, google}] = negativeEntry{rejection: rej, expires: now.Add(n.ttl)}
}

// invalidate 清掉这些 Key 的条目（主节点的作废推送）。
func (n *negativeKeyCache) invalidate(hashes []string) {
	if n == nil || len(hashes) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, h := range hashes {
		delete(n.entries, negativeKey{h, false})
		delete(n.entries, negativeKey{h, true})
	}
}

// clear 清空（事件流重连、主节点纪元变化：作废推送可能漏了）。
func (n *negativeKeyCache) clear() {
	if n == nil {
		return
	}
	n.mu.Lock()
	n.entries = map[negativeKey]negativeEntry{}
	n.mu.Unlock()
}

// OnInvalidation 处理主节点的缓存作废推送：清 Key 负缓存。
func (d *Dispatcher) OnInvalidation(inv *relayv1.Invalidation) {
	d.negKeys.invalidate(inv.GetApiKeyHashes())
}

// ClearKeyCache 清空 Key 缓存（事件流重连、主节点纪元变化）。
func (d *Dispatcher) ClearKeyCache() { d.negKeys.clear() }
