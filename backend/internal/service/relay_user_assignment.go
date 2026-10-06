package service

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// 小白端用户的中转分配（docs/MASTER_RELAY_NODES.md 10.1、10.9）：登录后和定期询问时，客户端向主节点要分到的转发地址和中转票据。

// ErrRelayUnavailable：没有可用的中转（从节点都不可用且主节点分配比例为 0）。
var ErrRelayUnavailable = errors.New("no relay is available")

// ErrRelayNotEnabled：主从分流没开。
var ErrRelayNotEnabled = errors.New("relay is not enabled")

// RelayUserAssignment 是分配结果。Role 为 RelayRoleRelay 时带中转票据；RelayRoleMaster 时沿用现有登录态访问 /api/v1/paw/*。
type RelayUserAssignment struct {
	Role   string `json:"role"`
	NodeID int64  `json:"node_id"`
	// BaseURL 是转发地址 https://<域名>；主节点时是 api_base_url（没配置时为空，调用方用当前请求的主机）。
	BaseURL         string     `json:"base_url"`
	Ticket          string     `json:"ticket,omitempty"`
	TicketExpiresAt *time.Time `json:"ticket_expires_at,omitempty"`
	// RefreshAfter 是建议多少秒后再查。
	RefreshAfter int `json:"refresh_after"`
}

// RelayUserAssigner 给用户定节点并签发票据（主从分流运行时实现）。
type RelayUserAssigner interface {
	// AssignUser 返回用户当前的分配；unreachableNodeID 非 0 表示客户端报告连不上这台（要换一台）。
	AssignUser(ctx context.Context, userID, unreachableNodeID int64) (*RelayUserAssignment, error)
}

type relayUserAssignerHolder struct{ a RelayUserAssigner }

var activeRelayUserAssigner atomic.Pointer[relayUserAssignerHolder]

// SetRelayUserAssigner 设置（nil 取消）用户分配。
func SetRelayUserAssigner(a RelayUserAssigner) {
	if a == nil {
		activeRelayUserAssigner.Store(nil)
		return
	}
	activeRelayUserAssigner.Store(&relayUserAssignerHolder{a: a})
}

// AssignRelayUser 给用户分配中转；主从分流没开时返回 ErrRelayNotEnabled。
func AssignRelayUser(ctx context.Context, userID, unreachableNodeID int64) (*RelayUserAssignment, error) {
	h := activeRelayUserAssigner.Load()
	if h == nil {
		return nil, ErrRelayNotEnabled
	}
	return h.a.AssignUser(ctx, userID, unreachableNodeID)
}
