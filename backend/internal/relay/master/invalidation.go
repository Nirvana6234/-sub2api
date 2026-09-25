package master

import (
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
)

// invalidationFlushInterval 是作废通知的合并间隔（开发计划 3.2：每 50 毫秒合并一次）。
const invalidationFlushInterval = 50 * time.Millisecond

// Invalidator 收集缓存作废，合并后推给所有从节点（设计 6 第三类）：
// 用户、Key、分组、订阅、配额、Key 分配节点的修改，以及新建 Key（清掉"查不到"缓存）。
type Invalidator struct {
	events *EventHub

	mu     sync.Mutex
	keys   map[string]struct{}
	users  map[int64]struct{}
	groups map[int64]struct{}
	timer  *time.Timer
}

// NewInvalidator 创建作废推送。
func NewInvalidator(events *EventHub) *Invalidator {
	return &Invalidator{events: events, keys: map[string]struct{}{}, users: map[int64]struct{}{}, groups: map[int64]struct{}{}}
}

// APIKeyHash 登记一个 API Key 鉴权缓存键（原始 Key 的 SHA-256）作废。
// 作为 service.APIKeyService 的鉴权缓存作废监听使用。
func (v *Invalidator) APIKeyHash(hash string) {
	if hash == "" {
		return
	}
	v.mu.Lock()
	v.keys[hash] = struct{}{}
	v.scheduleLocked()
	v.mu.Unlock()
}

// User 登记一个用户的缓存作废。
func (v *Invalidator) User(userID int64) {
	if userID <= 0 {
		return
	}
	v.mu.Lock()
	v.users[userID] = struct{}{}
	v.scheduleLocked()
	v.mu.Unlock()
}

// Group 登记一个分组的缓存作废。
func (v *Invalidator) Group(groupID int64) {
	if groupID <= 0 {
		return
	}
	v.mu.Lock()
	v.groups[groupID] = struct{}{}
	v.scheduleLocked()
	v.mu.Unlock()
}

func (v *Invalidator) scheduleLocked() {
	if v.timer == nil {
		v.timer = time.AfterFunc(invalidationFlushInterval, v.flush)
	}
}

func (v *Invalidator) flush() {
	v.mu.Lock()
	msg := &relayv1.Invalidation{}
	for k := range v.keys {
		msg.ApiKeyHashes = append(msg.ApiKeyHashes, k)
	}
	for u := range v.users {
		msg.UserIds = append(msg.UserIds, u)
	}
	for g := range v.groups {
		msg.GroupIds = append(msg.GroupIds, g)
	}
	v.keys, v.users, v.groups = map[string]struct{}{}, map[int64]struct{}{}, map[int64]struct{}{}
	v.timer = nil
	v.mu.Unlock()
	if len(msg.ApiKeyHashes)+len(msg.UserIds)+len(msg.GroupIds) == 0 {
		return
	}
	v.events.Broadcast(&relayv1.MasterEnvelope{Body: &relayv1.MasterEnvelope_Invalidation{Invalidation: msg}})
}
