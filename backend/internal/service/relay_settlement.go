package service

import (
	"context"
	"time"
)

// RelaySettlement 是主从分流下，一条从节点上报的用量在主节点入账时随扣费一起做的事（设计 5.2）：
// 在扣费的同一个事务里认领扣费凭证（每张只入账一次），并按实扣金额消耗这台节点为这个用户锁的额度租约。
//
// 入账仍走现有的 RecordUsage：主节点把它挂在 ctx 上（WithRelaySettlement），扣费仓储在 Apply 的
// 事务里处理，并把结果写回这个结构。
type RelaySettlement struct {
	// 扣费凭证：UUID（文本）和签发时间（relay_voucher_consumed 的主键）。
	VoucherID string
	IssuedAt  time.Time
	NodeID    int64
	UserID    int64
	// GroupID、APIKeyID、Platform 定位这次请求用到的租约（订阅窗口按分组、Key 维度按 Key、平台配额按平台）。
	GroupID  int64
	APIKeyID int64
	Platform string
	// ReviewStatus：吊销节点签发的凭证记为 pending_review（设计 5.4），其余为空。
	ReviewStatus string

	// PlatformQuotaCost 由入账填：平台配额这次累加的金额（非订阅计费、有平台时等于实扣）。
	PlatformQuotaCost float64

	// 以下由扣费仓储填。
	// Handled：凭证已在扣费事务里处理（认领或发现已入账）。为 false 时入账没走到扣费仓储，
	// 调用方要单独把凭证记成零扣费（RelayVoucherRecorder）。
	Handled bool
	// AlreadySettled：这张凭证之前已经入账，这次什么都没做；Consumed 是当时的消耗。
	AlreadySettled bool
	// Consumed：按租约消耗的金额（微单位，1 = 10⁻⁸），从节点据此修正本地额度。
	Consumed []RelayLeaseConsumption
}

// RelayLeaseConsumption 是一份租约被入账消耗的金额。
type RelayLeaseConsumption struct {
	LeaseID   int64  `json:"lease_id"`
	Dimension string `json:"dimension"`
	ScopeID   int64  `json:"scope_id"`
	ScopeKey  string `json:"scope_key"`
	Amount    int64  `json:"amount"`
}

type relaySettlementKey struct{}

// WithRelaySettlement 把主从分流的入账信息挂到入账用的 ctx 上。
func WithRelaySettlement(ctx context.Context, s *RelaySettlement) context.Context {
	return context.WithValue(ctx, relaySettlementKey{}, s)
}

func relaySettlementFromContext(ctx context.Context) *RelaySettlement {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(relaySettlementKey{}).(*RelaySettlement)
	return s
}

// RelayVoucherRecorder 把一张凭证记成已入账、零消耗：入账没有走到扣费仓储时（没有扣费命令），
// 也要记下，否则重发会再入账一次。已入账过时返回当时的记录（AlreadySettled、Consumed）。
type RelayVoucherRecorder interface {
	RecordRelayVoucher(ctx context.Context, s *RelaySettlement) error
}
