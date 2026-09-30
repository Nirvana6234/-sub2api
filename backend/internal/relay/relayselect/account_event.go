package relayselect

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// recentUseTTL 是一个账号在从节点上释放后，这台节点还能为它上报账号事件的时间。
// 调度结果在放槽之后才上报（与单机顺序一致），事件排在释放后面到达；与租约有效期一致。
const recentUseTTL = 10 * time.Minute

// accountEventQueue、accountEventWorkers：账号事件在自己的协程里执行（不能阻塞事件流的接收）。
const (
	accountEventQueue   = 4096
	accountEventWorkers = 4
)

type nodeAccount struct {
	nodeID    int64
	accountID int64
}

type recentUse struct {
	account *service.Account
	until   time.Time
}

type queuedAccountEvent struct {
	nodeID int64
	ev     *relayv1.AccountEvent
}

// rememberUse 记下这台节点刚用过这个账号（释放、清理时）。调用方持有 s.mu。
func (s *selector) rememberUseLocked(sel *selectionRecord) {
	if sel == nil || sel.account == nil {
		return
	}
	s.recent[nodeAccount{nodeID: sel.nodeID, accountID: sel.account.ID}] = recentUse{account: sel.account, until: s.now().Add(recentUseTTL)}
}

// accountForEvent 返回这台节点正在用或刚用过的账号对象；都不是时返回 nil。
func (s *selector) accountForEvent(nodeID, accountID int64) *service.Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sel := range s.selections {
		if sel.nodeID == nodeID && sel.account != nil && sel.account.ID == accountID {
			return sel.account
		}
	}
	if r, ok := s.recent[nodeAccount{nodeID: nodeID, accountID: accountID}]; ok && s.now().Before(r.until) {
		return r.account
	}
	return nil
}

// AccountEvent 收下一个账号事件，排队执行。队列满时丢弃（与释放消息一样由节点尽力发送）。
func (s *selector) AccountEvent(nodeID int64, ev *relayv1.AccountEvent) {
	select {
	case s.events <- queuedAccountEvent{nodeID: nodeID, ev: ev}:
	default:
		slog.Warn("relay account event queue is full, dropping", "node_id", nodeID, "account_id", ev.GetAccountId())
	}
}

func (s *selector) runAccountEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case q := <-s.events:
			s.applyAccountEvent(q.nodeID, q.ev)
		}
	}
}

// applyAccountEvent 用单机同一段代码（LocalAccountReporter）执行账号事件。
func (s *selector) applyAccountEvent(nodeID int64, ev *relayv1.AccountEvent) {
	reporter := s.localReporter()
	if ev.GetAccountSwitch() != nil {
		reporter.RecordAccountSwitch() // 调度器指标，不涉及具体账号
		return
	}
	account := s.accountForEvent(nodeID, ev.GetAccountId())
	if account == nil {
		slog.Warn("relay account event for an account the node is not using", "node_id", nodeID, "account_id", ev.GetAccountId())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch kind := ev.Kind.(type) {
	case *relayv1.AccountEvent_ScheduleResult:
		r := kind.ScheduleResult
		var firstToken *int
		if r.GetHasFirstTokenMs() {
			v := int(r.GetFirstTokenMs())
			firstToken = &v
		}
		reporter.ReportScheduleResult(account, r.GetModel(), r.GetSuccess(), firstToken, r.GetServingGroupId(), r.GetReasoningEffort(), healthFailure(r.GetFailure()))
	case *relayv1.AccountEvent_HealthFailure:
		if err := healthFailure(kind.HealthFailure.GetFailure()); err != nil {
			reporter.ObserveHealthFailure(ctx, account, err)
		}
	case *relayv1.AccountEvent_CodexUsage:
		var snapshot service.OpenAICodexUsageSnapshot
		if err := json.Unmarshal(kind.CodexUsage.GetSnapshotJson(), &snapshot); err != nil {
			slog.Warn("relay codex usage event is malformed", "node_id", nodeID, "account_id", account.ID, "error", err)
			return
		}
		reporter.UpdateCodexUsageSnapshot(ctx, account.ID, &snapshot)
	case *relayv1.AccountEvent_TransportError:
		reporter.TempUnscheduleTransportError(ctx, account, kind.TransportError.GetMessage())
	case *relayv1.AccountEvent_OllamaActivity:
		reporter.OllamaCloudUsageActivity(account)
	case *relayv1.AccountEvent_TempUnschedulable:
		s.applyTempUnschedulable(ctx, nodeID, account, kind.TempUnschedulable)
	case *relayv1.AccountEvent_SessionWindow:
		reporter.UpdateSessionWindow(ctx, account, service.SessionWindowHeaders(headersFromProto(kind.SessionWindow.GetHeaders())))
	}
}

// healthFailure 由事实还原出分类相同的错误；不计入熔断时为 nil。
func healthFailure(f *relayv1.HealthFailureFacts) error {
	if f == nil {
		return nil
	}
	return service.OpenAIHealthFailureError(int(f.GetStatusCode()), f.GetBody(), f.GetEligible())
}

// maxRelayTempUnschedulable 是从节点报来的临时不可调度的最长时长：非 OpenAI 网关转发路径上写账号仓储的几处
// 用的是 1 分钟（同账号重试用尽的 400/502）和 10 分钟（持久的传输错误），超出的按它截断。
const maxRelayTempUnschedulable = 10 * time.Minute

// applyTempUnschedulable 照写从节点转发路径上的临时不可调度（本地这几处直接写 accountRepo.SetTempUnschedulable）。
func (s *selector) applyTempUnschedulable(ctx context.Context, nodeID int64, account *service.Account, ev *relayv1.TempUnschedulableEvent) {
	if s.deps.AnthropicGateway == nil {
		return
	}
	until := time.UnixMilli(ev.GetUntilUnixMs())
	if limit := s.now().Add(maxRelayTempUnschedulable); until.After(limit) {
		until = limit
	}
	if !until.After(s.now()) {
		return
	}
	reason := ev.GetReason()
	if len(reason) > 256 {
		reason = reason[:256]
	}
	if err := s.deps.AnthropicGateway.SetAccountTempUnschedulable(ctx, account.ID, until, reason); err != nil {
		slog.Warn("relay temp unschedulable event failed", "node_id", nodeID, "account_id", account.ID, "error", err)
	}
}
