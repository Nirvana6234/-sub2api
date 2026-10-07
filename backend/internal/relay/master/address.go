package master

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// addressCacheTTL：节点域名、主节点地址的短缓存（Key 列表、导出配置每把 Key 都要解析一次）。
const addressCacheTTL = 10 * time.Second

type addressCache struct {
	mu      sync.Mutex
	entries map[int64]addressEntry
}

type addressEntry struct {
	addr service.RelayAddress
	ok   bool
	at   time.Time
}

// ResolveRelayAddress 实现 service.RelayAddressResolver：从节点是 https://<管理页填写的地址>，主节点是 api_base_url。
func (r *Runtime) ResolveRelayAddress(ctx context.Context, nodeID int64) (service.RelayAddress, bool) {
	now := r.now()
	r.addresses.mu.Lock()
	if e, hit := r.addresses.entries[nodeID]; hit && now.Sub(e.at) < addressCacheTTL {
		r.addresses.mu.Unlock()
		return e.addr, e.ok
	}
	r.addresses.mu.Unlock()

	addr, ok := r.lookupAddress(ctx, nodeID)
	r.addresses.mu.Lock()
	if r.addresses.entries == nil {
		r.addresses.entries = map[int64]addressEntry{}
	}
	r.addresses.entries[nodeID] = addressEntry{addr: addr, ok: ok, at: now}
	r.addresses.mu.Unlock()
	return addr, ok
}

func (r *Runtime) lookupAddress(ctx context.Context, nodeID int64) (service.RelayAddress, bool) {
	if nodeID == 0 {
		addr := service.RelayAddress{Role: service.RelayRoleMaster}
		if v, err := r.deps.Settings.GetValue(ctx, service.SettingKeyAPIBaseURL); err == nil {
			addr.BaseURL = strings.TrimRight(strings.TrimSpace(v), "/")
		}
		return addr, true
	}
	n, err := r.deps.Store.GetByID(ctx, nodeID)
	if err != nil {
		if !errors.Is(err, ErrNodeNotFound) {
			return service.RelayAddress{}, false
		}
		return service.RelayAddress{}, false
	}
	if n.PublicDomain == "" {
		return service.RelayAddress{}, false
	}
	endpoint, err := ParseRelayEndpoint(n.PublicDomain)
	if err != nil {
		return service.RelayAddress{}, false
	}
	return service.RelayAddress{Role: service.RelayRoleRelay, NodeID: n.ID, BaseURL: "https://" + endpoint.URLHost()}, true
}

// invalidateAddresses 清掉地址缓存（节点域名、激活状态变了）。
func (r *Runtime) invalidateAddresses() {
	r.addresses.mu.Lock()
	r.addresses.entries = nil
	r.addresses.mu.Unlock()
}

// nodeAddress 是拒绝提示里用的地址文本（取不到时为空）。
func (r *Runtime) nodeAddress(ctx context.Context, nodeID int64) string {
	if addr, ok := r.ResolveRelayAddress(ctx, nodeID); ok {
		return addr.BaseURL
	}
	return ""
}
