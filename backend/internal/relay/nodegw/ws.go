package nodegw

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// Responses WebSocket（开发计划 WP10-3）：连接选号见 Select（WS 为 true）；这里是每一轮的准入与结束、
// 换模型时的渠道映射、每 Key 连接数租约。

const (
	wsUnavailableReason = "Service temporarily unavailable"
	wsReconnectReason   = "relay session expired, please reconnect"
)

func wsCloseError(code coderws.StatusCode, reason string, cause error) error {
	return service.NewOpenAIWSClientCloseError(code, reason, cause)
}

// BeginTurn 一轮开始（handler.OpenAIRelayDispatcher）：主节点复核、定价、占这一轮的槽、签凭证；
// 从节点尽量预扣这一轮的额度（额度不够也照常转发：单机只在建连时查计费资格）。
func (d *Dispatcher) BeginTurn(c *gin.Context, conn *handler.OpenAIRelayAttempt, turn int, model string) (*handler.OpenAIRelayAttempt, error) {
	a, ok := conn.State.(*attemptState)
	if !ok {
		return nil, wsCloseError(coderws.StatusTryAgainLater, wsUnavailableReason, nil)
	}
	ctx := c.Request.Context()
	callKey := strconv.FormatUint(uint64(a.turnCalls.Add(1)), 10)
	resp, err := d.deps.Select.BeginTurn(ctx, callKey, &relayv1.BeginTurnRequest{
		SelectionId: a.selectionID, Turn: int32(turn), Model: model, HeldQuota: d.heldQuota(a.userID, a.apiKeyID),
	})
	if errors.Is(err, transport.ErrEpochChanged) {
		// 主节点重启过：这条连接的记录已不在，核对租约后请客户端重连。
		if d.deps.AfterEpochChange != nil {
			if rerr := d.deps.AfterEpochChange(ctx); rerr != nil {
				slog.Warn("relay lease report after an epoch change failed", "error", rerr)
			}
		}
		return nil, wsCloseError(coderws.StatusTryAgainLater, wsReconnectReason, err)
	}
	if err != nil {
		slog.Warn("relay websocket turn admission failed", "selection_id", a.selectionID, "turn", turn, "error", err)
		return nil, wsCloseError(coderws.StatusTryAgainLater, wsUnavailableReason, err)
	}
	if code := resp.GetCloseStatus(); code != 0 {
		return nil, wsCloseError(coderws.StatusCode(code), resp.GetCloseReason(), nil)
	}
	// 下一轮的安全审计按这一轮时的策略（审计在 BeginTurn 之前做）。
	stateOf(c).auditPolicy.Store(int32(resp.GetAuditPolicy()))
	t := &attemptState{selectionID: a.selectionID, voucher: resp.GetVoucher(), userID: a.userID, apiKeyID: a.apiKeyID, turnID: resp.GetTurnId()}
	d.deps.Quota.ApplyGrants(resp.GetGrants())
	if len(resp.GetQuotaScopes()) > 0 {
		scopes := make([]node.QuotaScope, 0, len(resp.GetQuotaScopes()))
		for _, s := range resp.GetQuotaScopes() {
			scopes = append(scopes, node.QuotaScope{Dimension: s.GetDimension(), ScopeID: s.GetScopeId(), ScopeKey: s.GetScopeKey()})
		}
		d.scopes.Store(a.apiKeyID, scopes)
		if res, err := d.deps.Quota.Reserve(ctx, a.userID, scopes, resp.GetQuotaNeed()); err == nil {
			t.reservation = res
		}
	}
	return &handler.OpenAIRelayAttempt{
		Account: conn.Account, SessionHash: conn.SessionHash, ChannelMapping: conn.ChannelMapping,
		ForwardModel: conn.ForwardModel, MaxAccountSwitches: conn.MaxAccountSwitches, State: t,
	}, nil
}

// EndTurn 一轮结束（handler.OpenAIRelayDispatcher）：没入队用量的预扣退回；主节点放这一轮的槽，记这一轮产生的
// response id。这一轮的 cyber 命中报告还没发完时等它（连接结束的释放也等它）。
func (d *Dispatcher) EndTurn(_ *gin.Context, conn, turn *handler.OpenAIRelayAttempt) {
	a, ok := conn.State.(*attemptState)
	if !ok {
		return
	}
	a.mu.Lock()
	if a.released {
		a.mu.Unlock()
		return
	}
	ids := append([]string(nil), a.responseIDs...)
	a.responseIDs = nil
	a.mu.Unlock()
	rel := &relayv1.SelectionRelease{SelectionId: a.selectionID, TurnEnd: true, ResponseIds: ids}
	var cyberDone chan struct{}
	if turn != nil {
		if t, ok := turn.State.(*attemptState); ok {
			t.mu.Lock()
			submitted := t.usageSubmitted
			t.released = true
			cyberDone = t.cyberDone
			t.mu.Unlock()
			if !submitted && t.reservation != nil {
				t.reservation.Cancel()
			}
			rel.Voucher, rel.TurnId = t.voucher, t.turnID
		}
	}
	if cyberDone != nil {
		a.mu.Lock()
		a.cyberDone = cyberDone
		a.mu.Unlock()
		go func() {
			<-cyberDone
			d.deps.Select.Release(rel)
		}()
		return
	}
	d.deps.Select.Release(rel)
}

// TurnMapping 一轮的渠道映射（handler.OpenAIRelayDispatcher）。
func (d *Dispatcher) TurnMapping(c *gin.Context, conn *handler.OpenAIRelayAttempt, model string) (service.ChannelMappingResult, error) {
	a, ok := conn.State.(*attemptState)
	if !ok {
		return service.ChannelMappingResult{}, errors.New("relay websocket connection has no selection")
	}
	resp, err := d.deps.Select.TurnMapping(c.Request.Context(), &relayv1.TurnMappingRequest{SelectionId: a.selectionID, Model: model})
	if err != nil {
		return service.ChannelMappingResult{}, err
	}
	return service.ChannelMappingResult{
		Mapped: resp.GetChannelMapped(), MappedModel: resp.GetChannelMappedModel(), ChannelID: resp.GetChannelId(),
		BillingModelSource: resp.GetBillingModelSource(),
	}, nil
}

// WSIngressLeaseCache 每 Key 的 WebSocket 连接数租约存储：经主节点申请（handler.OpenAIRelayDispatcher）。
func (d *Dispatcher) WSIngressLeaseCache(_ *gin.Context, apiKey *service.APIKey) service.OpenAIWSIngressLeaseCache {
	return remoteLeaseCache{client: d.deps.Select, rawKey: apiKey.Key}
}

// remoteLeaseCache 实现 service.OpenAIWSIngressLeaseCache：申请带 Key 原文（主节点自己验证、上限用主节点的配置），
// 续期和释放按租约 ID（主节点只认申请它的节点）。
type remoteLeaseCache struct {
	client *node.SelectClient
	rawKey string
}

func (r remoteLeaseCache) AcquireOpenAIWSIngressLease(ctx context.Context, _ int64, _ int, leaseID string) (bool, error) {
	resp, err := r.client.WebSocketLease(ctx, &relayv1.WebSocketLeaseRequest{Op: relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_ACQUIRE, ApiKey: r.rawKey, LeaseId: leaseID})
	if err != nil {
		return false, err
	}
	return resp.GetOk(), nil
}

func (r remoteLeaseCache) RefreshOpenAIWSIngressLease(ctx context.Context, _ int64, leaseID string) (bool, error) {
	resp, err := r.client.WebSocketLease(ctx, &relayv1.WebSocketLeaseRequest{Op: relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_REFRESH, LeaseId: leaseID})
	if err != nil {
		return false, err
	}
	return resp.GetOk(), nil
}

func (r remoteLeaseCache) ReleaseOpenAIWSIngressLease(ctx context.Context, _ int64, leaseID string) error {
	_, err := r.client.WebSocketLease(ctx, &relayv1.WebSocketLeaseRequest{Op: relayv1.WebSocketLeaseOp_WEB_SOCKET_LEASE_OP_RELEASE, LeaseId: leaseID})
	return err
}
