package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// openAIAccountChooser 与 OpenAIGatewayService.SelectAccountWithSchedulerForCapability 同签名（测试替换用）。
type openAIAccountChooser func(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport service.OpenAIUpstreamTransport,
	requiredCapability service.OpenAIEndpointCapability,
	requireCompact bool,
	previousResponseCanMove bool,
	useUpstreamTokenCost bool,
	platformOverride ...string,
) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error)

// OpenAISelectKind 是一次"选号 + 准入"的结果。
type OpenAISelectKind int

const (
	// OpenAISelected：选到账号、拿到槽位、通过利润终检；Release 在这次尝试结束时调用。
	OpenAISelected OpenAISelectKind = iota
	// OpenAISelectFailed：调度器返回错误（Err）。本请求还没排除过账号时按"无可用账号"分类，
	// 否则按换号耗尽处理。
	OpenAISelectFailed
	// OpenAISelectNone：调度器没返回账号，按"无可用账号"分类。
	OpenAISelectNone
	// OpenAISelectVetoExhausted：利润终检否决次数到了上限。
	OpenAISelectVetoExhausted
	// OpenAISelectQueueFull：账号等待队列已满（429）。
	OpenAISelectQueueFull
	// OpenAISelectSlotError：抢槽出错或排队超时（Err，按 concurrencyErrorResponse 转响应）。
	OpenAISelectSlotError
	// OpenAISelectNoWaitPlan：选到的账号没有等待计划（503 No available accounts）。
	OpenAISelectNoWaitPlan
	// OpenAISelectAborted：内部重选之间请求已取消。
	OpenAISelectAborted
	// OpenAISelectIneligible：媒体入口选到的账号没有生成资格（Account；已放掉槽位、已排除），调用方按本地的换号计数处理。
	OpenAISelectIneligible
)

// OpenAISelectRequest 是 Responses 选号一次尝试的输入。
type OpenAISelectRequest struct {
	GroupID            *int64
	PreviousResponseID string
	SessionHash        string
	// ForwardModel 是渠道映射后的模型。
	ForwardModel       string
	RequestPlatform    string
	RequiredCapability service.OpenAIEndpointCapability
	// ImagesCapability 非空时是同步图片入口的选号（SelectAccountWithSchedulerForImages，不装利润门、只走 HTTP/SSE）。
	ImagesCapability service.OpenAIImagesCapability
	// NoUpstreamTokenCost：选号不按上游 token 成本比较（alpha search 这类按次计费的入口）。
	NoUpstreamTokenCost bool
	// Transport 为空时是 OpenAIUpstreamTransportAny（Embeddings 用 HTTP/SSE）。
	Transport      service.OpenAIUpstreamTransport
	RequireCompact bool
	ImageIntent    bool
	// BoundAccountID 非 0 时是媒体任务查询（视频状态 / 内容、Seedance）：只准入任务绑定的账号
	// （SelectMediaVideoRequestAccount），不走调度器、不刷新绑定，准入时不带会话。
	BoundAccountID int64
	// Eligibility 非 nil 时在选中账号之后、准入之前检查账号有没有生成资格（Grok 媒体生成）；不满足返回 OpenAISelectIneligible。
	Eligibility func(ctx context.Context, account *service.Account) (bool, string, error)
	// Excluded 是本请求已排除的账号；续链不支持、利润否决的账号会被加进去（调用方的同一个 map）。
	Excluded map[int64]struct{}
	// OnTick、CannotWait 见 OpenAIAccountAdmitter.Admit。
	OnTick     func() error
	CannotWait error
	// OnAccountChosen 在选中账号、准入之前调用，返回之后使用的 ctx（本地：记运维选中账号、
	// 把兜底事实放回请求 ctx）。nil 时只把兜底事实放进 ctx。
	OnAccountChosen func(ctx context.Context, selection *service.AccountSelectionResult) context.Context
}

// OpenAISelectState 是按请求累计、跨尝试保留的选号状态。
type OpenAISelectState struct {
	ProfitVetoCount int
	// LastFailoverErr：续链不支持时跳过账号会写入它（全部跳过后按它返回 400）。
	LastFailoverErr *service.UpstreamFailoverError
}

// OpenAISelectOutcome 是 SelectAndAdmit 的结果。
type OpenAISelectOutcome struct {
	Kind      OpenAISelectKind
	Selection *service.AccountSelectionResult
	Account   *service.Account
	// SessionHash 是实际使用的会话哈希（池模式账号可能改写）。
	SessionHash string
	// Release 没有绑定 ctx（主节点上要占到从节点释放）。
	Release func()
	// Ctx 带着选号结果的利润门和兜底事实。
	Ctx context.Context
	Err error
}

func transportOrAny(t service.OpenAIUpstreamTransport) service.OpenAIUpstreamTransport {
	if t == "" {
		return service.OpenAIUpstreamTransportAny
	}
	return t
}

// SelectAndAdmit 是 Responses 选号循环里"选号 → 续链检查 → 准入 → 利润否决重选"这一段。
// 本地的 Responses 处理函数和主从分流主节点的选号（relayselect）共用它，两边结果一致（开发计划 WP7）。
// 它不写客户端响应：各种失败以 Kind 返回，由调用方按原来的格式写出。
func (a OpenAIAccountAdmitter) SelectAndAdmit(ctx context.Context, req OpenAISelectRequest, state *OpenAISelectState, reqLog *zap.Logger) OpenAISelectOutcome {
	sessionHash := req.SessionHash
	for {
		if ctx.Err() != nil {
			return OpenAISelectOutcome{Kind: OpenAISelectAborted, Ctx: ctx, SessionHash: sessionHash, Err: ctx.Err()}
		}
		reqLog.Debug("openai.account_selecting", zap.Int("excluded_account_count", len(req.Excluded)))
		var (
			selection        *service.AccountSelectionResult
			scheduleDecision service.OpenAIAccountScheduleDecision
			err              error
		)
		if req.BoundAccountID > 0 {
			selection, scheduleDecision, err = a.Gateway.SelectMediaVideoRequestAccount(ctx, req.GroupID, sessionHash, req.BoundAccountID, req.ForwardModel, req.RequestPlatform)
		} else if req.ImagesCapability != "" {
			selection, scheduleDecision, err = a.Gateway.SelectAccountWithSchedulerForImages(ctx, req.GroupID, sessionHash, req.ForwardModel, req.Excluded, req.ImagesCapability)
		} else {
			choose := a.choose
			if choose == nil {
				choose = a.Gateway.SelectAccountWithSchedulerForCapability
			}
			selection, scheduleDecision, err = choose(
				ctx,
				req.GroupID,
				req.PreviousResponseID,
				sessionHash,
				req.ForwardModel,
				req.Excluded,
				transportOrAny(req.Transport),
				req.RequiredCapability,
				req.RequireCompact,
				false,
				!req.ImageIntent && !req.NoUpstreamTokenCost,
				req.RequestPlatform,
			)
		}
		if err != nil {
			return OpenAISelectOutcome{Kind: OpenAISelectFailed, Ctx: ctx, SessionHash: sessionHash, Err: err}
		}
		if selection == nil || selection.Account == nil {
			return OpenAISelectOutcome{Kind: OpenAISelectNone, Ctx: ctx, SessionHash: sessionHash}
		}
		if req.PreviousResponseID != "" {
			reqLog.Debug("openai.account_selected_with_previous_response_id", zap.Int64("account_id", selection.Account.ID))
		}
		reqLog.Debug("openai.account_schedule_decision",
			zap.String("layer", scheduleDecision.Layer),
			zap.Bool("sticky_previous_hit", scheduleDecision.StickyPreviousHit),
			zap.Bool("sticky_session_hit", scheduleDecision.StickySessionHit),
			zap.Int("candidate_count", scheduleDecision.CandidateCount),
			zap.Int("top_k", scheduleDecision.TopK),
			zap.Int64("latency_ms", scheduleDecision.LatencyMs),
			zap.Float64("load_skew", scheduleDecision.LoadSkew),
		)
		account := selection.Account
		if req.PreviousResponseID != "" && req.RequestPlatform == service.PlatformOpenAI && !account.IsOpenAIApiKey() {
			// The public Responses HTTP API supports previous_response_id on API-key
			// accounts. OAuth/SetupToken upstreams do not, so keep searching instead
			// of silently deleting continuation state from a mixed account pool.
			req.Excluded[account.ID] = struct{}{}
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
				selection.ReleaseFunc = nil
			}
			state.LastFailoverErr = openAIHTTPContinuationUnsupportedError()
			reqLog.Debug("openai.account_skipped_http_continuation_unsupported",
				zap.Int64("account_id", account.ID),
				zap.String("account_type", account.Type),
			)
			continue
		}
		if req.Eligibility != nil {
			eligible, _, _ := req.Eligibility(ctx, account)
			if !eligible {
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
					selection.ReleaseFunc = nil
				}
				req.Excluded[account.ID] = struct{}{}
				return OpenAISelectOutcome{Kind: OpenAISelectIneligible, Account: account, Ctx: ctx, SessionHash: sessionHash}
			}
		}
		sessionHash = ensureOpenAIPoolModeSessionHash(sessionHash, account)
		reqLog.Debug("openai.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))
		if req.OnAccountChosen != nil {
			ctx = req.OnAccountChosen(ctx, selection)
		} else {
			ctx = service.ContextWithSelectionFallbackTrace(ctx, selection)
		}

		admissionSession := sessionHash
		if req.BoundAccountID > 0 {
			// 等待不能用文本粘性的 TTL 顶掉视频任务的归属绑定。
			admissionSession = ""
		}
		admitCtx, admission := a.Admit(ctx, req.GroupID, admissionSession, selection, req.OnTick, req.CannotWait, reqLog)
		switch admission.Kind {
		case OpenAIAdmitted:
			return OpenAISelectOutcome{Kind: OpenAISelected, Selection: selection, Account: selection.Account, SessionHash: sessionHash, Release: admission.Release, Ctx: admitCtx}
		case OpenAIAdmissionProfitVetoed:
			// 利润终检否决：排除该账号重新选号，全池耗尽由下一轮选号报错；
			// 否决次数达上限则直接终止，避免排队抢槽后才终检的延迟放大。
			if !recordOpenAIProfitVeto(req.Excluded, account.ID, &state.ProfitVetoCount) {
				return OpenAISelectOutcome{Kind: OpenAISelectVetoExhausted, Ctx: ctx, SessionHash: sessionHash}
			}
			continue
		case OpenAIAdmissionQueueFull:
			return OpenAISelectOutcome{Kind: OpenAISelectQueueFull, Account: account, Ctx: ctx, SessionHash: sessionHash}
		case OpenAIAdmissionSlotError:
			return OpenAISelectOutcome{Kind: OpenAISelectSlotError, Account: account, Ctx: ctx, SessionHash: sessionHash, Err: admission.Err}
		default:
			return OpenAISelectOutcome{Kind: OpenAISelectNoWaitPlan, Account: account, Ctx: ctx, SessionHash: sessionHash}
		}
	}
}
