package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

// QuotaSync 把本地额度和主节点对上：重连核对、续期、退回闲置、响应收回（设计 4.2、4.4）。
// 调用都带幂等键（这样才带上纪元）；遇到纪元变化先重新核对，再用同样的累计值重发，不清零。
type QuotaSync struct {
	local  *LocalQuota
	client *transport.Client
}

// NewQuotaSync 创建额度同步。
func NewQuotaSync(local *LocalQuota, client *transport.Client) *QuotaSync {
	return &QuotaSync{local: local, client: client}
}

func (s *QuotaSync) control() relayv1.RelayControlClient {
	return relayv1.NewRelayControlClient(s.client.Conn(transport.TierEvents))
}

func withNewKey(ctx context.Context) context.Context {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return transport.WithIdempotencyKey(ctx, hex.EncodeToString(b))
}

// retryOnEpoch：纪元变了（主节点重启）先核对租约，再重试一次。
func (s *QuotaSync) retryOnEpoch(ctx context.Context, call func(context.Context) error) error {
	err := call(withNewKey(ctx))
	if !errors.Is(err, transport.ErrEpochChanged) {
		return err
	}
	if rerr := s.Report(ctx); rerr != nil {
		return rerr
	}
	return call(withNewKey(ctx))
}

// Report 上报手里的租约（重连、纪元变化后，发选号之前）：主节点关掉没上报的，这里丢掉主节点不认的。
func (s *QuotaSync) Report(ctx context.Context) error {
	resp, err := s.control().ReportLeases(withNewKey(ctx), &relayv1.ReportLeasesRequest{Leases: s.local.Held()})
	if errors.Is(err, transport.ErrEpochChanged) {
		// 刚得知新纪元（首次通信或主节点重启）：带上它再报一次。
		resp, err = s.control().ReportLeases(withNewKey(ctx), &relayv1.ReportLeasesRequest{Leases: s.local.Held()})
	}
	if err != nil {
		return err
	}
	s.local.Drop(resp.GetDroppedLeaseIds())
	return nil
}

// Renew 续期全部租约（带最后使用时间）。
func (s *QuotaSync) Renew(ctx context.Context) error {
	leases := s.local.Renewals()
	if len(leases) == 0 {
		return nil
	}
	return s.retryOnEpoch(ctx, func(ctx context.Context) error {
		resp, err := s.control().RenewLeases(ctx, &relayv1.RenewLeasesRequest{Leases: leases})
		if err != nil {
			return err
		}
		s.local.ApplyRenewed(resp.GetRenewed())
		s.local.Drop(resp.GetDroppedLeaseIds())
		return nil
	})
}

// ReleaseIdle 退回闲置超过 idleAfter 的额度，连同上次没送到的退回。
func (s *QuotaSync) ReleaseIdle(ctx context.Context, idleAfter time.Duration) error {
	s.local.IdleReturns(idleAfter)
	return s.sendReturns(ctx)
}

func (s *QuotaSync) sendReturns(ctx context.Context) error {
	rets := s.local.PendingReturns()
	if len(rets) == 0 {
		return nil
	}
	return s.retryOnEpoch(ctx, func(ctx context.Context) error {
		resp, err := s.control().ReleaseQuota(ctx, &relayv1.ReleaseQuotaRequest{Returns: rets})
		if err != nil {
			return err
		}
		s.local.ConfirmReturned(rets)
		s.local.Drop(resp.GetDroppedLeaseIds())
		return nil
	})
}

// HandleRecall 响应主节点的收回：退回（闲置判断在本机），回复确认。回复没送到时，
// 取出的钱留在"未确认"里，下一次退回一并重发。
func (s *QuotaSync) HandleRecall(ctx context.Context, rc *relayv1.QuotaRecall) error {
	rets := s.local.Recall(rc)
	return s.retryOnEpoch(ctx, func(ctx context.Context) error {
		resp, err := s.control().AckQuotaRecall(ctx, &relayv1.AckQuotaRecallRequest{RecallId: rc.GetRecallId(), Returns: rets})
		if err != nil {
			return err
		}
		s.local.ConfirmReturned(rets)
		s.local.Drop(resp.GetDroppedLeaseIds())
		return nil
	})
}
