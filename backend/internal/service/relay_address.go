package service

import (
	"context"
	"sync/atomic"
)

// 主从分流下 API Key 的接入地址（docs/MASTER_RELAY_NODES.md 10.2、10.9）：Key 上记分配的节点 ID，
// 导出配置、"使用 Key"、地址查询接口用的是这台节点现取的域名，不是全局的 api_base_url。

const (
	// RelayRoleRelay：分配给从节点；RelayRoleMaster：分配给主节点；RelayRoleUnassigned：还没分配（沿用现在的地址）。
	RelayRoleRelay      = "relay"
	RelayRoleMaster     = "master"
	RelayRoleUnassigned = "unassigned"
)

// RelayAddress 是一把 Key 的接入地址。
type RelayAddress struct {
	Role   string `json:"role"`
	NodeID int64  `json:"node_id"`
	// BaseURL 是 https://<域名>；主节点时是 api_base_url（没配置时为空，调用方用当前请求的主机）。
	BaseURL string `json:"base_url"`
}

// RelayAddressResolver 把节点 ID 解析成接入地址（主从分流运行时实现；开关关闭时没有）。
type RelayAddressResolver interface {
	// ResolveRelayAddress 返回节点（0 为主节点）的接入地址；节点不存在、没填域名时 ok 为 false。
	ResolveRelayAddress(ctx context.Context, nodeID int64) (addr RelayAddress, ok bool)
}

type relayAddressResolverHolder struct{ r RelayAddressResolver }

var activeRelayAddressResolver atomic.Pointer[relayAddressResolverHolder]

// SetRelayAddressResolver 设置（nil 取消）地址解析。
func SetRelayAddressResolver(r RelayAddressResolver) {
	if r == nil {
		activeRelayAddressResolver.Store(nil)
		return
	}
	activeRelayAddressResolver.Store(&relayAddressResolverHolder{r: r})
}

// ResolveRelayAddressForKey 返回一把 Key 的接入地址；主从分流没开、Key 没分配或节点没有域名时 ok 为 false。
func ResolveRelayAddressForKey(ctx context.Context, nodeID *int64) (RelayAddress, bool) {
	h := activeRelayAddressResolver.Load()
	if h == nil || nodeID == nil {
		return RelayAddress{}, false
	}
	return h.r.ResolveRelayAddress(ctx, *nodeID)
}
