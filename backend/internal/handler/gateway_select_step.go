package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// anthropicAccountChooser 与 GatewayService.SelectAccountWithLoadAwareness 同签名（测试替换用）。
type anthropicAccountChooser func(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, metadataUserID string, sub2apiUserID int64) (*service.AccountSelectionResult, error)

// AnthropicSelectKind 是 /v1/messages 一次"选号 + 准入"的结果。
type AnthropicSelectKind int

const (
	// AnthropicSelected：选到账号、拿到槽位、通过利润终检；Release 在这次尝试结束时调用。
	AnthropicSelected AnthropicSelectKind = iota
	// AnthropicSelectFailed：调度器返回错误（Err）。本请求还没排除过账号时按"无可用账号"分类，
	// 否则按选号耗尽处理（FailoverState.HandleSelectionExhausted）。
	AnthropicSelectFailed
	// AnthropicSelectIntercepted：选到的账号开了预热拦截、请求是要拦的（Intercept），槽位已放。
	AnthropicSelectIntercepted
	// AnthropicSelectNoWaitPlan：没拿到槽位也没有等待计划（503 No available accounts）。
	AnthropicSelectNoWaitPlan
	// AnthropicSelectQueueFull：账号等待队列已满（429）。
	AnthropicSelectQueueFull
	// AnthropicSelectSlotError：抢槽出错或排队超时（Err，按 handleConcurrencyError 转响应）。
	AnthropicSelectSlotError
	// AnthropicSelectProfitVetoed：拿到槽位后利润终检否决，槽位已放；调用方记一次否决（FailoverState.RecordProfitVeto）再重选。
	AnthropicSelectProfitVetoed
)

// AnthropicSelectRequest 是 /v1/messages（GatewayHandler.Messages）选号一次尝试的输入。
type AnthropicSelectRequest struct {
	GroupID    *int64
	SessionKey string
	Model      string
	// Excluded 是本请求已排除的账号（FailoverState.FailedAccountIDs）。
	Excluded       map[int64]struct{}
	MetadataUserID string
	UserID         int64
	// Intercept 返回这次请求的预热拦截类型（detectInterceptType），只在选到开了拦截的账号时调用。
	Intercept func() InterceptType
	// OnAccountChosen 在选中账号、准入之前调用（本地：记运维选中账号、打日志）。
	OnAccountChosen func(selection *service.AccountSelectionResult)
	// OnTick、CannotWait：排队期间的保活（本地流式发 SSE ping）；CannotWait 非空时不排队，抢不到就以它失败。
	OnTick     func() error
	CannotWait error
}

// AnthropicSelectOutcome 是 SelectAndAdmit 的结果。
type AnthropicSelectOutcome struct {
	Kind      AnthropicSelectKind
	Selection *service.AccountSelectionResult
	Account   *service.Account
	// Release 放账号槽位，没有绑定 ctx（主节点上要占到从节点释放）。
	Release func()
	// Ctx 带着选号结果里的利润门（后续终检、准入后绑定用）。
	Ctx       context.Context
	Err       error
	Intercept InterceptType
	// VetoReason：利润终检否决的原因（日志用）。
	VetoReason string
}

// AnthropicAccountAdmitter 是 /v1/messages 的"选号 → 预热拦截检查 → 抢槽（抢不到按等待计划排队）→ 利润终检 →
// 粘性绑定"这一段。本地 Messages 处理函数和主从分流主节点的选号共用它，两边结果一致（开发计划 WP10）。
// 一次调用只做一轮：否决、选号失败等由调用方按原来的循环处理（换号状态 FailoverState 在调用方）。
// 它不写客户端响应。
type AnthropicAccountAdmitter struct {
	Gateway     *service.GatewayService
	Concurrency *ConcurrencyHelper
	// choose 为空时用 Gateway 的调度器选号（测试替换用）。
	choose anthropicAccountChooser
}

// SelectAndAdmit 做一轮选号与准入。
func (a AnthropicAccountAdmitter) SelectAndAdmit(ctx context.Context, req AnthropicSelectRequest, reqLog *zap.Logger) AnthropicSelectOutcome {
	choose := a.choose
	if choose == nil {
		choose = a.Gateway.SelectAccountWithLoadAwareness
	}
	selection, err := choose(ctx, req.GroupID, req.SessionKey, req.Model, req.Excluded, req.MetadataUserID, req.UserID)
	if err != nil {
		return AnthropicSelectOutcome{Kind: AnthropicSelectFailed, Ctx: ctx, Err: err}
	}
	account := selection.Account
	if req.OnAccountChosen != nil {
		req.OnAccountChosen(selection)
	}

	// 检查请求拦截（预热请求、SUGGESTION MODE等）
	if account.IsInterceptWarmupEnabled() && req.Intercept != nil {
		if interceptType := req.Intercept(); interceptType != InterceptTypeNone {
			if selection.Acquired && selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			return AnthropicSelectOutcome{Kind: AnthropicSelectIntercepted, Selection: selection, Account: account, Ctx: ctx, Intercept: interceptType}
		}
	}

	// 获取账号并发槽位
	accountReleaseFunc := selection.ReleaseFunc
	if !selection.Acquired {
		if selection.WaitPlan == nil {
			return AnthropicSelectOutcome{Kind: AnthropicSelectNoWaitPlan, Selection: selection, Account: account, Ctx: ctx}
		}
		accountWaitCounted := false
		canWait, err := a.Concurrency.IncrementAccountWaitCount(ctx, account.ID, selection.WaitPlan.MaxWaiting)
		if err != nil {
			reqLog.Warn("gateway.account_wait_counter_increment_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		} else if !canWait {
			reqLog.Info("gateway.account_wait_queue_full",
				zap.Int64("account_id", account.ID),
				zap.Int("max_waiting", selection.WaitPlan.MaxWaiting),
			)
			return AnthropicSelectOutcome{Kind: AnthropicSelectQueueFull, Selection: selection, Account: account, Ctx: ctx}
		}
		if err == nil && canWait {
			accountWaitCounted = true
		}
		releaseWait := func() {
			if accountWaitCounted {
				a.Concurrency.DecrementAccountWaitCount(ctx, account.ID)
				accountWaitCounted = false
			}
		}

		accountReleaseFunc, err = a.acquireSlot(ctx, account.ID, selection.WaitPlan, req.OnTick, req.CannotWait)
		if err != nil {
			reqLog.Warn("gateway.account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			releaseWait()
			return AnthropicSelectOutcome{Kind: AnthropicSelectSlotError, Selection: selection, Account: account, Ctx: ctx, Err: err}
		}
		// Slot acquired: no longer waiting in queue.
		releaseWait()
	}
	// 终检与准入后绑定使用选号结果携带的门（见 responses 同名注释）。
	admissionCtx := service.ContextWithSelectionProfitGate(ctx, selection)
	latest, vetoed, reason := a.Gateway.GatewayProfitControlVetoLatest(admissionCtx, account)
	if vetoed {
		if accountReleaseFunc != nil {
			accountReleaseFunc()
		}
		return AnthropicSelectOutcome{Kind: AnthropicSelectProfitVetoed, Selection: selection, Account: account, Ctx: admissionCtx, VetoReason: reason}
	}
	selection.Account = latest
	// 等待路径保持既有 eager 绑定（无门时 helper 直接绑定）；调度器已
	// 抢槽的直达路径无门时由选号内部绑定，这里只在门下补准入后绑定。
	if selection.ProfitGateActive() || !selection.Acquired {
		if err := a.Gateway.BindStickySessionAfterProfitAdmission(admissionCtx, req.GroupID, req.SessionKey, latest.ID); err != nil {
			reqLog.Warn("gateway.bind_sticky_session_after_profit_admission_failed", zap.Int64("account_id", latest.ID), zap.Error(err))
		}
	}
	return AnthropicSelectOutcome{Kind: AnthropicSelected, Selection: selection, Account: latest, Release: accountReleaseFunc, Ctx: admissionCtx}
}

// acquireSlot 按等待计划抢账号槽位（AcquireAccountSlotWithWaitTimeout 不带 gin 的那一半）：先试一次，
// 不能排队时抢不到就以 cannotWait 失败，否则排队到超时。
func (a AnthropicAccountAdmitter) acquireSlot(ctx context.Context, accountID int64, plan *service.AccountWaitPlan, onTick func() error, cannotWait error) (func(), error) {
	if cannotWait != nil {
		release, acquired, err := a.Concurrency.acquireSlotOnce(ctx, "account", accountID, plan.MaxConcurrency)
		if err != nil || acquired {
			return release, err
		}
		return nil, cannotWait
	}
	return a.Concurrency.waitForSlot(ctx, "account", accountID, plan.MaxConcurrency, plan.Timeout, true, onTick)
}

// anthropicAdmissionWaitHooks 是账号排队期间的保活（与 waitForSlotWithPingTimeout 一致）：流式请求发 SSE ping；
// 响应不能刷新时不排队。
func (h *GatewayHandler) anthropicAdmissionWaitHooks(c *gin.Context, reqStream bool, streamStarted *bool) (onTick func() error, cannotWait error) {
	if reqStream && h.concurrencyHelper.pingFormat != "" {
		if flusher, ok := c.Writer.(http.Flusher); ok {
			onTick = h.concurrencyHelper.sseTick(c, flusher, streamStarted)
		} else {
			cannotWait = errors.New("streaming not supported")
		}
	}
	return onTick, cannotWait
}

// AnthropicFirstSelectFailureRejection 是 /v1/messages 第一次就选不出账号时的错误（本地与主从分流共用）。
// modelNotFound：按模型不存在分类（日志用）。
func AnthropicFirstSelectFailureRejection(ctx context.Context, diag service.ModelAvailabilityDiagnoser, apiKey *service.APIKey, model, platform string, selectErr error) (r OpenAIGatewayRejection, modelNotFound bool) {
	cls := classifyNoAccountError(ctx, diag, apiKey, model, model, platform)
	r = OpenAIGatewayRejection{Status: cls.Status, ErrType: cls.ErrType, Message: cls.Message}
	if cls.ModelNotFound {
		r.OpsBusinessLimitedReason = service.OpsClientBusinessLimitedReasonLocalModelConfiguration
	} else {
		r.RoutingCapacityLimited = isOpsNoAvailableAccountError(selectErr)
		r.Message = "No available accounts: " + selectErr.Error()
	}
	return r, cls.ModelNotFound
}

// AnthropicCountTokensSelectFailureRejection 是 count_tokens 选不出账号时的错误（本地与主从分流共用）：
// 与 Messages 不同，文案不带调度器的错误。
func AnthropicCountTokensSelectFailureRejection(ctx context.Context, diag service.ModelAvailabilityDiagnoser, apiKey *service.APIKey, model string, selectErr error) OpenAIGatewayRejection {
	cls := classifyNoAccountError(ctx, diag, apiKey, model, model, service.PlatformAnthropic)
	r := OpenAIGatewayRejection{Status: cls.Status, ErrType: cls.ErrType, Message: cls.Message}
	if cls.ModelNotFound {
		r.OpsBusinessLimitedReason = service.OpsClientBusinessLimitedReasonLocalModelConfiguration
	} else {
		r.RoutingCapacityLimited = isOpsNoAvailableAccountError(selectErr)
	}
	return r
}

// AnthropicSelectOutcomeRejection 是准入失败（没有等待计划、队列满、抢槽出错）的错误（本地与主从分流共用）。
func AnthropicSelectOutcomeRejection(outcome AnthropicSelectOutcome) OpenAIGatewayRejection {
	switch outcome.Kind {
	case AnthropicSelectQueueFull:
		return OpenAIGatewayRejection{Status: http.StatusTooManyRequests, ErrType: "rate_limit_error", Code: gatewayQueueFullCode, Message: "Too many pending requests, please retry later"}
	case AnthropicSelectSlotError:
		status, errType, code, message := concurrencyErrorResponse(outcome.Err, "account")
		return OpenAIGatewayRejection{Status: status, ErrType: errType, Code: code, Message: message}
	default:
		return OpenAIGatewayRejection{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: "No available accounts", RoutingCapacityLimited: true}
	}
}

// writeGatewayRejection 按 Messages 处理函数的写法写出选号阶段的错误（运维标记、Retry-After、流式感知的错误）。
func (h *GatewayHandler) writeGatewayRejection(c *gin.Context, r OpenAIGatewayRejection, streamStarted bool) {
	if r.OpsBusinessLimitedReason != "" {
		service.MarkOpsClientBusinessLimited(c, r.OpsBusinessLimitedReason)
	}
	if r.RoutingCapacityLimited {
		markOpsRoutingCapacityLimited(c)
	}
	if r.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(r.RetryAfter))
	}
	h.handleStreamingAwareErrorWithCode(c, r.Status, r.ErrType, r.Code, r.Message, streamStarted)
}
