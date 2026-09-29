package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// OpenAIWSSelectKind 是 Responses WebSocket 一次"选号 + 准入"的结果。
type OpenAIWSSelectKind int

const (
	// OpenAIWSSelected：选到账号、拿到账号槽、通过利润终检、做了粘性绑定。
	OpenAIWSSelected OpenAIWSSelectKind = iota
	// OpenAIWSSelectFailed：调度器返回错误（Err）或没返回账号：有上一次换号的错误时按换号耗尽关闭，
	// 否则按"无可用账号"关闭。
	OpenAIWSSelectFailed
	// OpenAIWSSelectVetoExhausted：利润终检否决次数到了上限（按"无可用账号"关闭）。
	OpenAIWSSelectVetoExhausted
	// OpenAIWSSelectBusy：账号没有等待计划或不排队抢不到槽（"account is busy"）。
	OpenAIWSSelectBusy
	// OpenAIWSSelectSlotError：抢账号槽出错（Err）。
	OpenAIWSSelectSlotError
	// OpenAIWSSelectAborted：连接的 ctx 已取消。
	OpenAIWSSelectAborted
)

// OpenAIWSSelectRequest 是 Responses WebSocket 选号一次尝试的输入。
type OpenAIWSSelectRequest struct {
	GroupID            *int64
	PreviousResponseID string
	SessionHash        string
	// ForwardModel 是渠道映射后的模型。
	ForwardModel            string
	RequiredTransport       service.OpenAIUpstreamTransport
	RequiredCapability      service.OpenAIEndpointCapability
	PreviousResponseCanMove bool
	ImageIntent             bool
	RequestPlatform         string
	// Excluded 是本连接已排除的账号；利润否决的账号会被加进去（调用方的同一个 map）。
	Excluded map[int64]struct{}
}

// OpenAIWSSelectOutcome 是 SelectAndAdmitWS 的结果。
type OpenAIWSSelectOutcome struct {
	Kind    OpenAIWSSelectKind
	Account *service.Account
	// MaxConcurrency 是之后每一轮重新抢这个账号的槽时用的上限。
	MaxConcurrency int
	// StickyPreviousHit：previous_response_id 命中了这个分组里的粘连账号。没命中时首帧的
	// previous_response_id 要剥掉（切组 / 会话失配防护）。
	StickyPreviousHit bool
	Decision          service.OpenAIAccountScheduleDecision
	// Release 放这次选中的账号槽（没有绑定 ctx）。
	Release func()
	// Ctx 是之后连接要用的 ctx：选中时带着选号结果的利润门和兜底事实；没选中时带着最后一次选号的兜底事实。
	Ctx context.Context
	Err error
}

// SelectAndAdmitWS 是 Responses WebSocket 选号循环里"选号 → 不排队抢账号槽 → 利润终检 → 粘性绑定"这一段。
// 本地的 ResponsesWebSocket 处理函数和主从分流主节点的连接选号（relayselect）共用它（开发计划 WP10-3）。
// 它不关闭客户端连接：各种失败以 Kind 返回，由调用方按原来的关闭码关闭。
func (a OpenAIAccountAdmitter) SelectAndAdmitWS(ctx context.Context, req OpenAIWSSelectRequest, profitVetoCount *int, reqLog *zap.Logger) OpenAIWSSelectOutcome {
	for {
		if ctx.Err() != nil {
			return OpenAIWSSelectOutcome{Kind: OpenAIWSSelectAborted, Ctx: ctx, Err: ctx.Err()}
		}
		reqLog.Debug("openai.websocket_account_selecting", zap.Int("excluded_account_count", len(req.Excluded)))
		choose := a.choose
		if choose == nil {
			choose = a.Gateway.SelectAccountWithSchedulerForCapability
		}
		selection, scheduleDecision, err := choose(
			ctx,
			req.GroupID,
			req.PreviousResponseID,
			req.SessionHash,
			req.ForwardModel,
			req.Excluded,
			req.RequiredTransport,
			req.RequiredCapability,
			false,
			req.PreviousResponseCanMove,
			!req.ImageIntent,
			req.RequestPlatform,
		)
		if err != nil {
			reqLog.Warn("openai.websocket_account_select_failed",
				zap.Error(openAICompatibleSelectionErrorForLog(err, req.RequestPlatform)),
				zap.Int("excluded_account_count", len(req.Excluded)),
			)
			return OpenAIWSSelectOutcome{Kind: OpenAIWSSelectFailed, Ctx: ctx, Err: err}
		}
		if selection == nil || selection.Account == nil {
			return OpenAIWSSelectOutcome{Kind: OpenAIWSSelectFailed, Ctx: ctx}
		}

		account := selection.Account
		accountMaxConcurrency := account.Concurrency
		if selection.WaitPlan != nil && selection.WaitPlan.MaxConcurrency > 0 {
			accountMaxConcurrency = selection.WaitPlan.MaxConcurrency
		}
		// 终检、准入后绑定与后续 turn 级复核都使用选号结果携带的门（composite
		// 等跨分组调度的门只存在于调度栈局部 ctx）；准入成功后并入连接 ctx。
		// 兜底事实同样在此并入：长连接的用量任务以这个 ctx 为 parent。
		ctx = service.ContextWithSelectionFallbackTrace(ctx, selection)
		admissionCtx := service.ContextWithSelectionProfitGate(ctx, selection)
		accountReleaseFunc := selection.ReleaseFunc
		if selection.Acquired {
			// 调度器已抢槽路径同样终检：选号与抢槽之间账号倍率可能刷新。
			latest, vetoed, reason := a.Gateway.ProfitControlVetoLatest(admissionCtx, account)
			if vetoed {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
				reqLog.Debug("openai.websocket_account_slot_profit_vetoed", zap.Int64("account_id", account.ID), zap.String("reason", reason))
				if !recordOpenAIProfitVeto(req.Excluded, account.ID, profitVetoCount) {
					reqLog.Warn("openai.websocket_profit_veto_attempts_exhausted", zap.Int("profit_veto_count", *profitVetoCount))
					return OpenAIWSSelectOutcome{Kind: OpenAIWSSelectVetoExhausted, Ctx: ctx}
				}
				continue
			}
			account = latest
			selection.Account = latest
		}
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				return OpenAIWSSelectOutcome{Kind: OpenAIWSSelectBusy, Account: account, Ctx: ctx}
			}
			fastReleaseFunc, fastAcquired, err := a.Concurrency.TryAcquireAccountSlot(
				ctx,
				account.ID,
				selection.WaitPlan.MaxConcurrency,
			)
			if err != nil {
				reqLog.Warn("openai.websocket_account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				return OpenAIWSSelectOutcome{Kind: OpenAIWSSelectSlotError, Account: account, Ctx: ctx, Err: err}
			}
			if !fastAcquired {
				return OpenAIWSSelectOutcome{Kind: OpenAIWSSelectBusy, Account: account, Ctx: ctx}
			}
			// 分组利润控制：WS 快速抢槽成功后终检，越线则释放
			// 槽位、排除该账号重新选号，全池耗尽由下一轮选号关闭连接。
			latest, vetoed, reason := a.Gateway.ProfitControlVetoLatest(admissionCtx, account)
			if vetoed {
				if fastReleaseFunc != nil {
					fastReleaseFunc()
				}
				reqLog.Debug("openai.websocket_account_slot_profit_vetoed", zap.Int64("account_id", account.ID), zap.String("reason", reason))
				if !recordOpenAIProfitVeto(req.Excluded, account.ID, profitVetoCount) {
					reqLog.Warn("openai.websocket_profit_veto_attempts_exhausted", zap.Int("profit_veto_count", *profitVetoCount))
					return OpenAIWSSelectOutcome{Kind: OpenAIWSSelectVetoExhausted, Ctx: ctx}
				}
				continue
			}
			account = latest
			selection.Account = latest
			accountReleaseFunc = fastReleaseFunc
		}
		// 准入完成：门并入连接 ctx，turn 级复核与 failover 重选共用。
		if err := a.Gateway.BindStickySessionAfterProfitAdmission(admissionCtx, req.GroupID, req.SessionHash, account.ID); err != nil {
			reqLog.Warn("openai.websocket_bind_sticky_session_after_profit_admission_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
		return OpenAIWSSelectOutcome{
			Kind: OpenAIWSSelected, Account: account, MaxConcurrency: accountMaxConcurrency,
			StickyPreviousHit: scheduleDecision.StickyPreviousHit, Decision: scheduleDecision,
			Release: accountReleaseFunc, Ctx: admissionCtx,
		}
	}
}
