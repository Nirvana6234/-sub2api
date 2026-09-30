package node

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

// webSearchReportInterval 是联网搜索用量的上报间隔。
const webSearchReportInterval = 10 * time.Second

// webSearchProxyDownFor 与单机的"代理暂不可用"标记同样长（websearch 包的 proxyUnavailableTTL）。
const webSearchProxyDownFor = 5 * time.Minute

var errWebSearchQuotaOnMaster = errors.New("web search quota is managed on the master")

// WebSearchQuota 是从节点上的联网搜索配额（websearch.QuotaStore，设计 3.3）：搜索在从节点执行，配额是全局的。
// 在主节点给的份额内本地计数，定期把新用的次数报给主节点汇总，拿回新的份额；还没拿到过份额时放行（与单机
// Redis 不可用时一样）。节点之间报得不及时时可能略超配额。代理"暂不可用"的标记只在本机（各节点的网络各自不同）。
type WebSearchQuota struct {
	control relayv1.RelayControlClient
	now     func() time.Time

	mu sync.Mutex
	// shares：主节点回的每个服务的份额；spent：拿到这份份额之后本机已用的（含还没报的）。
	shares map[string]*relayv1.WebSearchShare
	spent  map[string]int64
	// pending：还没报给主节点的用量；inflight：已经发出、还没确认的那一份（失败时用同一个幂等键重发）。
	pending  map[string]int64
	inflight *webSearchReport
	proxies  map[int64]time.Time
}

type webSearchReport struct {
	key  string
	used map[string]int64
}

var _ websearch.QuotaStore = (*WebSearchQuota)(nil)

// NewWebSearchQuota 创建配额（控制连接）。
func NewWebSearchQuota(client *transport.Client) *WebSearchQuota {
	return newWebSearchQuota(relayv1.NewRelayControlClient(client.Conn(transport.TierControl)))
}

func newWebSearchQuota(control relayv1.RelayControlClient) *WebSearchQuota {
	return &WebSearchQuota{control: control, now: time.Now, shares: map[string]*relayv1.WebSearchShare{},
		spent: map[string]int64{}, pending: map[string]int64{}, proxies: map[int64]time.Time{}}
}

// Reserve 见 websearch.QuotaStore：份额用完时不再给这个服务（与单机配额用完一样换下一个服务）。
func (q *WebSearchQuota) Reserve(_ context.Context, cfg websearch.ProviderConfig) (bool, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if share, ok := q.shares[cfg.Type]; ok && q.spent[cfg.Type] >= share.GetAllowance() {
		slog.Info("websearch: provider share on this node exhausted", "provider", cfg.Type, "allowance", share.GetAllowance())
		return false, false
	}
	q.spent[cfg.Type]++
	q.pending[cfg.Type]++
	return true, true
}

// Rollback 见 websearch.QuotaStore（这次搜索失败，退回占的一次）。
func (q *WebSearchQuota) Rollback(_ context.Context, cfg websearch.ProviderConfig) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.spent[cfg.Type] > 0 {
		q.spent[cfg.Type]--
	}
	// 占的那一次可能已经报给主节点了：待报的记成负数，下次上报时主节点减回去（与单机失败时回退一致）。
	q.pending[cfg.Type]--
}

// Usage 见 websearch.QuotaStore：主节点上次回的全局已用，加上本机还没报的。
func (q *WebSearchQuota) Usage(_ context.Context, providerType string) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	used := q.pending[providerType]
	if q.inflight != nil {
		used += q.inflight.used[providerType]
	}
	if share, ok := q.shares[providerType]; ok {
		used += share.GetUsed()
	}
	return used, nil
}

func (q *WebSearchQuota) Reset(context.Context, string) error { return errWebSearchQuotaOnMaster }

func (q *WebSearchQuota) MarkProxyUnavailable(_ context.Context, proxyID int64) {
	q.mu.Lock()
	q.proxies[proxyID] = q.now().Add(webSearchProxyDownFor)
	q.mu.Unlock()
}

func (q *WebSearchQuota) ProxyAvailable(_ context.Context, proxyID int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	until, ok := q.proxies[proxyID]
	if !ok {
		return true
	}
	if q.now().After(until) {
		delete(q.proxies, proxyID)
		return true
	}
	return false
}

// Report 把用量报给主节点并换上新的份额。上一份没确认时先用同一个幂等键重发它（主节点只加一次）。
func (q *WebSearchQuota) Report(ctx context.Context) error {
	q.mu.Lock()
	if q.inflight == nil {
		q.inflight = &webSearchReport{key: "websearch/" + NewRequestID(), used: q.pending}
		q.pending = map[string]int64{}
	}
	report := q.inflight
	q.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, moderationCallTimeout)
	defer cancel()
	resp, err := q.control.ReportWebSearchUsage(transport.WithIdempotencyKey(cctx, report.key), &relayv1.WebSearchUsageReport{Used: report.used})
	if err != nil {
		if errors.Is(err, transport.ErrEpochChanged) {
			// 主节点重启过（幂等结果丢了）：这一份并回下一次，换新键发，宁可少记也不重复记。
			q.mu.Lock()
			for k, v := range report.used {
				q.pending[k] += v
			}
			q.inflight = nil
			q.mu.Unlock()
		}
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inflight = nil
	shares := make(map[string]*relayv1.WebSearchShare, len(resp.GetShares()))
	for _, s := range resp.GetShares() {
		shares[s.GetProvider()] = s
	}
	q.shares = shares
	// 新份额按主节点刚算的剩余给出；报完之后本机又用掉的记在它上面。
	spent := make(map[string]int64, len(q.pending))
	for k, v := range q.pending {
		spent[k] = v
	}
	q.spent = spent
	return nil
}

// Run 定期上报，直到 ctx 结束。
func (q *WebSearchQuota) Run(ctx context.Context) {
	t := time.NewTicker(webSearchReportInterval)
	defer t.Stop()
	for {
		if err := q.Report(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("relay web search usage report failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
