package service

import (
	"context"
	"sync"
)

// SettingChangeHub 在系统设置写入成功后通知订阅者。
//
// 主从分流的主节点靠它得知"配置改了"：合并后生成新的配置快照推给从节点，
// 以及监听主从分流总开关（docs/MASTER_RELAY_NODES.md 第 6 节）。
// 没有订阅者时 Notify 什么都不做，单机部署的行为不变。
//
// 它只看得到经过 SettingRepository 的写入；绕开仓储直接改库的（备份恢复、
// 手工 SQL、securityaudit 的提示词配置）由订阅方自己定时比对兜底。
type SettingChangeHub struct {
	mu   sync.RWMutex
	next int
	subs map[int]func(keys []string)
}

// NewSettingChangeHub 创建订阅中心。
func NewSettingChangeHub() *SettingChangeHub {
	return &SettingChangeHub{subs: map[int]func(keys []string){}}
}

// Subscribe 注册回调，返回取消函数。回调在写入的调用方协程里同步执行，必须很快返回
// （只做标记、投递，不做耗时工作）。
func (h *SettingChangeHub) Subscribe(fn func(keys []string)) (unsubscribe func()) {
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

// Notify 通知订阅者这些键被写过。
func (h *SettingChangeHub) Notify(keys []string) {
	if h == nil || len(keys) == 0 {
		return
	}
	h.mu.RLock()
	subs := make([]func([]string), 0, len(h.subs))
	for _, fn := range h.subs {
		subs = append(subs, fn)
	}
	h.mu.RUnlock()
	for _, fn := range subs {
		fn(keys)
	}
}

// observedSettingRepository 在写入成功后通知 SettingChangeHub。
type observedSettingRepository struct {
	SettingRepository
	hub *SettingChangeHub
}

// NewObservedSettingRepository 给设置仓储加上写入通知。hub 为 nil 时原样返回。
func NewObservedSettingRepository(inner SettingRepository, hub *SettingChangeHub) SettingRepository {
	if hub == nil || inner == nil {
		return inner
	}
	return &observedSettingRepository{SettingRepository: inner, hub: hub}
}

func (r *observedSettingRepository) Set(ctx context.Context, key, value string) error {
	if err := r.SettingRepository.Set(ctx, key, value); err != nil {
		return err
	}
	r.hub.Notify([]string{key})
	return nil
}

func (r *observedSettingRepository) SetMultiple(ctx context.Context, settings map[string]string) error {
	if err := r.SettingRepository.SetMultiple(ctx, settings); err != nil {
		return err
	}
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	r.hub.Notify(keys)
	return nil
}

func (r *observedSettingRepository) Delete(ctx context.Context, key string) error {
	if err := r.SettingRepository.Delete(ctx, key); err != nil {
		return err
	}
	r.hub.Notify([]string{key})
	return nil
}
