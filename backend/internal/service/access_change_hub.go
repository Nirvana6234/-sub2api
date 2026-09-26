package service

import (
	"context"
	"sync"
)

// AccessChangeKind 是影响"谁能用、能用多少"的一类改动。
type AccessChangeKind string

const (
	// AccessChangeUser：用户本身（状态、余额、并发、分组权限、删除等）。
	AccessChangeUser AccessChangeKind = "user"
	// AccessChangeGroup：分组（倍率、模型、状态、删除等）。
	AccessChangeGroup AccessChangeKind = "group"
	// AccessChangeSubscription：用户在某个订阅分组上的订阅（分配、续期、撤销、改额度）。
	AccessChangeSubscription AccessChangeKind = "subscription"
	// AccessChangePlatformQuota：用户的平台日/周/月配额上限。
	AccessChangePlatformQuota AccessChangeKind = "platform_quota"
)

// AccessChange 是一条改动。UserID / GroupID 按 Kind 填写：
// user 只有 UserID；group 只有 GroupID；subscription 两个都有；platform_quota 只有 UserID。
type AccessChange struct {
	Kind    AccessChangeKind
	UserID  int64
	GroupID int64
}

// AccessChangeHub 在用户、分组、订阅、平台配额改动后通知订阅者。
//
// 主从分流的主节点靠它把改动推给从节点，让从节点清掉按用户、分组缓存的状态
// （docs/MASTER_RELAY_NODES.md 第 6 节第三类）；额度收回（设计 4.4）也订阅它。
// 没有订阅者时 Publish 什么都不做，单机部署的行为不变。
//
// 通知挂在业务层现有的缓存作废入口上（APIKeyService.InvalidateAuthCacheByUserID/GroupID、
// BillingCacheService.InvalidateSubscription、平台配额写入），所以漏了作废的改动在单机上
// 也已经是缺陷；新增改动点照旧调用这些作废函数即可。
type AccessChangeHub struct {
	mu   sync.RWMutex
	next int
	subs map[int]func(AccessChange)
}

// NewAccessChangeHub 创建订阅中心。
func NewAccessChangeHub() *AccessChangeHub {
	return &AccessChangeHub{subs: map[int]func(AccessChange){}}
}

// Subscribe 注册回调，返回取消函数。回调在改动的调用方协程里同步执行，必须很快返回。
func (h *AccessChangeHub) Subscribe(fn func(AccessChange)) (unsubscribe func()) {
	if h == nil || fn == nil {
		return func() {}
	}
	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = fn
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.subs, id)
		h.mu.Unlock()
	}
}

// Publish 通知订阅者。nil 接收者安全。
func (h *AccessChangeHub) Publish(c AccessChange) {
	if h == nil {
		return
	}
	h.mu.RLock()
	if len(h.subs) == 0 {
		h.mu.RUnlock()
		return
	}
	subs := make([]func(AccessChange), 0, len(h.subs))
	for _, fn := range h.subs {
		subs = append(subs, fn)
	}
	h.mu.RUnlock()
	for _, fn := range subs {
		fn(c)
	}
}

// SubscriberCount 返回当前订阅者数量（诊断和测试用）。
func (h *AccessChangeHub) SubscriberCount() int {
	if h == nil {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// AttachAccessChangeHub 让已构造的服务在作废缓存时顺带发布改动。
// 由主程序装配调用（relaywire）；这些服务在很多测试里直接构造，所以用挂接而不是构造参数。
func AttachAccessChangeHub(hub *AccessChangeHub, apiKeys *APIKeyService, billing *BillingCacheService) {
	if apiKeys != nil {
		apiKeys.accessChanges.Store(hub)
	}
	if billing != nil {
		billing.accessChanges.Store(hub)
	}
}

// observedUserPlatformQuotaRepository 在配额上限写入成功后发布改动。
// 只看上限写入（UpsertForUser）：用量累加每个请求都有，窗口重置按设计 4.4 不处理。
type observedUserPlatformQuotaRepository struct {
	UserPlatformQuotaRepository
	hub *AccessChangeHub
}

// NewObservedUserPlatformQuotaRepository 给平台配额仓储加上改动通知。hub 为 nil 时原样返回。
func NewObservedUserPlatformQuotaRepository(inner UserPlatformQuotaRepository, hub *AccessChangeHub) UserPlatformQuotaRepository {
	if hub == nil || inner == nil {
		return inner
	}
	return &observedUserPlatformQuotaRepository{UserPlatformQuotaRepository: inner, hub: hub}
}

func (r *observedUserPlatformQuotaRepository) UpsertForUser(ctx context.Context, userID int64, records []UserPlatformQuotaRecord) error {
	if err := r.UserPlatformQuotaRepository.UpsertForUser(ctx, userID, records); err != nil {
		return err
	}
	r.hub.Publish(AccessChange{Kind: AccessChangePlatformQuota, UserID: userID})
	return nil
}
