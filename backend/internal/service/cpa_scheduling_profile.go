package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// These constants are also used by the handler retry loop and health tracker so
// that the read-only export cannot silently drift from execution defaults.
const (
	DefaultSameAccountRetryDelay = 500 * time.Millisecond
	MaxRequestScopedRetryDelay   = 8 * time.Second
	openAIAccountHealthEWMAAlpha = 0.2
)

// CPASchedulingProfile exports the values used by this running S2 instance.
// Runtime concurrency/health counters remain local to the process executing
// real accounts; they are deliberately not copied as authoritative CPA load.
func (s *OpenAIGatewayService) CPASchedulingProfile(ctx context.Context) (*CPASchedulingProfile, error) {
	if s == nil || s.cfg == nil || s.accountRepo == nil || s.cfg.CPAScheduling.SourceID == "" {
		return nil, errors.New("CPA scheduling source is unavailable")
	}
	accounts, err := s.accountRepo.ListAllWithFilters(ctx, "", "", "", "", 0, "")
	if err != nil {
		return nil, err
	}
	settings := s.openAIAdvancedSchedulerRuntimeSettings(ctx)
	weights := s.openAIWSSchedulerWeightsForRequest(ctx)
	escape := s.openAIStickyEscapeConfig()
	waiting := s.schedulingConfig()
	now := time.Now().UTC()
	profile := &CPASchedulingProfile{
		SchemaVersion:    1,
		SourceInstanceID: s.cfg.CPAScheduling.SourceID,
		GeneratedAt:      &now,
		Scheduler: CPASchedulerPolicy{
			AdvancedEnabled: settings.enabled,
			TopK:            s.openAIWSLBTopKForRequest(ctx),
			Weights: CPASchedulerWeights{
				Priority: weights.Priority, Load: weights.Load, Queue: weights.Queue,
				ErrorRate: weights.ErrorRate, TTFT: weights.TTFT, Reset: weights.Reset,
				QuotaHeadroom: weights.QuotaHeadroom, UpstreamCost: weights.UpstreamCost,
				PreviousResponse: weights.Previous, SessionSticky: weights.SessionSticky,
			},
			StickyWeightedEnabled:          settings.enabled && settings.stickyWeightedEnabled,
			SubscriptionPriorityEnabled:    settings.enabled && settings.subscriptionPriorityEnabled,
			PriorityOrder:                  "ascending",
			LowUpstreamRatePriorityEnabled: settings.lowUpstreamRatePriorityEnabled,
			OAuthSchedulingRateMultiplier:  cpaOAuthSchedulingRateMultiplier(settings.oauthSchedulingRateMultiplier),
			Sticky: CPAStickyPolicy{
				SessionTTLSeconds:    int64(s.openAIWSSessionStickyTTL() / time.Second),
				ResponseIDTTLSeconds: int(s.openAIWSResponseStickyTTL() / time.Second),
				EscapeEnabled:        escape.enabled, EscapeTTFTMS: escape.ttftMs,
				EscapeErrorRate: escape.errorRate, EscapeComparison: "greater_than",
			},
			Health: CPAHealthPolicy{EWMAAlpha: openAIAccountHealthEWMAAlpha, EWMATTLSeconds: 0},
			LatencyFallback: CPALatencyPolicy{
				Enabled:     settings.latencyAwareFallbackEnabled,
				ThresholdMS: settings.latencyThresholdMs, SpeedupRatio: settings.fallbackSpeedupRatio,
				Percentile: openAILatencyTailPercentile, MinSamples: openAILatencyMinSamples,
				SampleTTLSeconds: int64(openAILatencyTrackerTTL / time.Second),
				SampleWindowSize: openAILatencySampleWindowSize,
				ReasoningBuckets: openAILatencyBuckets(),
			},
		},
		Waiting: CPAWaitingPolicy{
			Scope:                     "account",
			Sticky:                    CPAAccountWaitPolicy{MaxWaiting: waiting.StickySessionMaxWaiting, TimeoutMS: waiting.StickySessionWaitTimeout.Milliseconds()},
			Fallback:                  CPAAccountWaitPolicy{MaxWaiting: waiting.FallbackMaxWaiting, TimeoutMS: waiting.FallbackWaitTimeout.Milliseconds()},
			FallbackSelectionMode:     waiting.FallbackSelectionMode,
			PreferSoonestReset:        waiting.PreferSoonestReset,
			ConcurrencySlotTTLSeconds: s.cfg.Gateway.ConcurrencySlotTTLMinutes * 60,
		},
		Retry: s.cpaRetryPolicy(),
		ProviderCreationDefaults: CPAProviderCreationDefaults{
			Scope: "new_accounts_only", Source: "sub2api_account_creation_ui",
			OrdinaryConcurrency: 10, GrokConcurrency: 1, OfficialPlanLimits: false,
		},
		Accounts: make([]CPAAccountScheduling, 0, len(accounts)),
	}
	settingService := s.settingService
	if settingService == nil && s.rateLimitService != nil { // relay:master-only 调度
		settingService = s.rateLimitService.settingService
	}
	if settingService == nil {
		return nil, errors.New("CPA scheduling settings are unavailable")
	}
	overload, err := settingService.GetOverloadCooldownSettings(ctx)
	if err != nil {
		return nil, err
	}
	rateLimit, err := settingService.GetRateLimit429CooldownSettings(ctx)
	if err != nil {
		return nil, err
	}
	profile.Cooldown = CPACooldownPolicy{
		Overload:     CPACooldownSetting{Enabled: overload.Enabled, DurationMS: int64(overload.CooldownMinutes) * 60000},
		RateLimit429: CPACooldownSetting{Enabled: rateLimit.Enabled, DurationMS: int64(rateLimit.CooldownSeconds) * 1000},
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	for i := range accounts {
		profile.Accounts = append(profile.Accounts, cpaAccountScheduling(&accounts[i], now, settings.oauthSchedulingRateMultiplier))
	}
	profile.Revision, err = cpaSchedulingRevision(profile)
	if err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *OpenAIGatewayService) cpaRetryPolicy() CPARetryPolicy {
	policy := CPARetryPolicy{
		MaxAccountSwitches: 3, MaxAccountSwitchesGemini: 3,
		SameAccountDefault: defaultPoolModeRetryCount, SameAccountMax: maxPoolModeRetryCount,
		SameAccountDelayMS:      int(DefaultSameAccountRetryDelay.Milliseconds()),
		RequestScopedMaxDelayMS: int(MaxRequestScopedRetryDelay.Milliseconds()),
		Websocket: CPAWebsocketRetryPolicy{
			MaxRetries:       openAIWSReconnectRetryLimit,
			BackoffInitialMS: int(openAIWSRetryBackoffInitialDefault.Milliseconds()),
			BackoffMaxMS:     int(openAIWSRetryBackoffMaxDefault.Milliseconds()),
			JitterRatio:      openAIWSRetryJitterRatioDefault,
			TotalBudgetMS:    s.openAIWSRetryTotalBudget().Milliseconds(),
		},
	}
	if s.cfg == nil {
		return policy
	}
	if s.cfg.Gateway.MaxAccountSwitches > 0 {
		policy.MaxAccountSwitches = s.cfg.Gateway.MaxAccountSwitches
	}
	if s.cfg.Gateway.MaxAccountSwitchesGemini > 0 {
		policy.MaxAccountSwitchesGemini = s.cfg.Gateway.MaxAccountSwitchesGemini
	}
	ws := s.cfg.Gateway.OpenAIWS
	if ws.RetryBackoffInitialMS > 0 {
		policy.Websocket.BackoffInitialMS = ws.RetryBackoffInitialMS
	}
	if ws.RetryBackoffMaxMS > 0 {
		policy.Websocket.BackoffMaxMS = ws.RetryBackoffMaxMS
	}
	if policy.Websocket.BackoffMaxMS < policy.Websocket.BackoffInitialMS {
		policy.Websocket.BackoffMaxMS = policy.Websocket.BackoffInitialMS
	}
	if ws.RetryJitterRatio >= 0 {
		policy.Websocket.JitterRatio = clamp01(ws.RetryJitterRatio)
	}
	return policy
}

// cpaOAuthSchedulingRateMultiplier 把「未设置（nil）」折算成 CPA 协议里的数值：
// CPA 侧只认 float64，未设置时沿用历史默认倍率；每个账号的实际成本倍率
// 仍由 openAISchedulingRate 逐账号算好随 profile 下发。
func cpaOAuthSchedulingRateMultiplier(rate *float64) float64 {
	if rate == nil {
		return defaultOpenAIOAuthSchedulingRateMultiplier
	}
	return *rate
}

func cpaAccountScheduling(account *Account, now time.Time, oauthRate *float64) CPAAccountScheduling {
	identity := CPAAccountIdentity{
		ChatGPTAccountID: account.GetChatGPTAccountID(), ChatGPTUserID: account.GetChatGPTUserID(),
		WorkspaceID:       firstStringValue(account.Credentials, "workspace_id", "chatgpt_workspace_id"),
		OrganizationID:    firstStringValue(account.Credentials, "organization_id", "org_id"),
		ProviderAccountID: firstStringValue(account.Credentials, "account_id", "account_uuid"),
	}
	isShadow := account.ParentAccountID != nil || account.QuotaDimension != ""
	isPool := account.IsPoolMode()
	hasIdentity := identity.ChatGPTAccountID != "" || identity.ProviderAccountID != ""
	out := CPAAccountScheduling{
		SourceAccountID: account.ID, Name: account.Name, Platform: account.Platform, Type: account.Type,
		Identity: identity, IsShadow: isShadow, ParentAccountID: account.ParentAccountID,
		QuotaDimension:         account.QuotaDimension,
		OAuthIdentityMatchable: account.IsOAuth() && !isShadow && !isPool && hasIdentity,
		IsPoolMode:             isPool, Concurrency: account.Concurrency, LoadFactor: account.LoadFactor,
		EffectiveLoadFactor: account.EffectiveLoadFactor(), Priority: account.Priority,
		GroupIDs:        make([]int64, 0, len(account.GroupIDs)),
		GroupPriorities: make([]CPAGroupPriority, 0, len(account.AccountGroups)),
		Status:          account.Status, Schedulable: account.Schedulable,
		ExpiresAt: account.ExpiresAt, AutoPauseOnExpired: account.AutoPauseOnExpired,
		RateLimitResetAt: account.RateLimitResetAt, OverloadUntil: account.OverloadUntil,
		TempUnschedulableUntil:       account.TempUnschedulableUntil,
		PoolModeRetryCount:           account.GetPoolModeRetryCount(),
		PoolModeRetryStatusCodes:     []int{},
		SubscriptionPriorityEligible: account.IsOpenAIChatGPTSubscription(),
		QuotaHeadroomFactor:          openAIQuotaHeadroomFactor(account, now), UpdatedAt: account.UpdatedAt,
	}
	retryCodes := account.GetPoolModeRetryStatusCodes()
	if retryCodes == nil {
		retryCodes = defaultPoolModeRetryableStatusCodes
	}
	out.PoolModeRetryStatusCodes = append(out.PoolModeRetryStatusCodes, retryCodes...)
	sort.Ints(out.PoolModeRetryStatusCodes)
	groups := make(map[int64]bool, len(account.GroupIDs)+len(account.AccountGroups))
	for _, id := range account.GroupIDs {
		groups[id] = true
	}
	for _, relation := range account.AccountGroups {
		groups[relation.GroupID] = true
		out.GroupPriorities = append(out.GroupPriorities, CPAGroupPriority{GroupID: relation.GroupID, Priority: relation.Priority})
	}
	for id := range groups {
		out.GroupIDs = append(out.GroupIDs, id)
	}
	sort.Slice(out.GroupIDs, func(i, j int) bool { return out.GroupIDs[i] < out.GroupIDs[j] })
	sort.Slice(out.GroupPriorities, func(i, j int) bool { return out.GroupPriorities[i].GroupID < out.GroupPriorities[j].GroupID })
	if end, ok := openAISchedulingResetWindowEnd(account, now); ok {
		out.ResetWindowEnd = &end
	}
	if rate, ok := openAISchedulingRate(account, now, oauthRate); ok {
		out.UpstreamCostRate = &rate
	}
	return out
}

// Canonicalize recursively through maps using json.Number. CPA can independently
// verify this digest without sharing Go struct field order or losing integer IDs.
func cpaSchedulingRevision(profile *CPASchedulingProfile) (string, error) {
	raw, err := json.Marshal(profile)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var canonical map[string]any
	if err := decoder.Decode(&canonical); err != nil {
		return "", err
	}
	delete(canonical, "revision")
	delete(canonical, "generated_at")
	raw, err = json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return strings.ToLower(hex.EncodeToString(hash[:])), nil
}
