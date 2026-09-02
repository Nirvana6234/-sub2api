package service

import (
	"context"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// openAIModelAvailabilityCache is request-scoped. Fallback traversal can
// revisit the same target group through different scheduler paths, so keep
// the persistent model diagnosis for the lifetime of one request instead of
// issuing the same account query repeatedly. It is deliberately not a
// process-wide cache: account/group edits must take effect on the next
// request without an invalidation protocol.
type openAIModelAvailabilityCache struct {
	mu      sync.Mutex
	entries map[openAIModelAvailabilityCacheEntryKey]ModelAvailabilityDiagnosis
}

type openAIModelAvailabilityCacheEntryKey struct {
	groupID  int64
	platform string
	model    string
}

type openAIModelAvailabilityCacheContextKey struct{}

func withOpenAIModelAvailabilityCache(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, ok := ctx.Value(openAIModelAvailabilityCacheContextKey{}).(*openAIModelAvailabilityCache); ok && existing != nil {
		return ctx
	}
	return context.WithValue(ctx, openAIModelAvailabilityCacheContextKey{}, &openAIModelAvailabilityCache{
		entries: make(map[openAIModelAvailabilityCacheEntryKey]ModelAvailabilityDiagnosis),
	})
}

func openAIModelAvailabilityCacheFromContext(ctx context.Context) *openAIModelAvailabilityCache {
	if ctx == nil {
		return nil
	}
	cache, _ := ctx.Value(openAIModelAvailabilityCacheContextKey{}).(*openAIModelAvailabilityCache)
	return cache
}

// DiagnoseModelAvailabilityForPlatform reports whether the requested model
// is configured to be served by any persistently eligible OpenAI-compatible
// account in the group for the given platform (e.g. PlatformOpenAI,
// PlatformGrok). The platform scopes the candidate pool so distinct
// OpenAI-compatible platforms do not cross-contaminate diagnosis results.
// The query bypasses scheduler snapshots and ignores transient runtime state.
//
// Safe to call on the error path: returns {true,true} on any internal
// failure or when the inputs preclude meaningful diagnosis (empty model,
// nil service), so callers stay on the 503 fallback branch.
func (s *OpenAIGatewayService) DiagnoseModelAvailabilityForPlatform(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	platform string,
) ModelAvailabilityDiagnosis {
	if s == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	if s.accountRepo == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	platform = NormalizeOpenAICompatiblePlatform(platform)
	queryGroupID := groupID
	includeGrouped := false
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		queryGroupID = nil
		includeGrouped = true
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(
		ctx,
		queryGroupID,
		[]string{platform},
		includeGrouped,
	)
	if err != nil {
		// Conservative fallback so the caller keeps returning 503; we do not
		// want a transient lookup failure to flip into 404 model_not_found.
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	diag := ModelAvailabilityDiagnosis{}
	for i := range accounts {
		diag.HasAccountsInPool = true
		// Mirrors the per-candidate filter used during account selection
		// (openai_account_scheduler.isAccountRequestCompatible): empty
		// model_mapping accepts everything for ordinary API-key accounts;
		// passthrough accounts also accept every model because the actual
		// scheduler uses the same IsModelSupported predicate.
		if accounts[i].IsModelSupported(requestedModel) {
			diag.HasModelSupport = true
			return diag
		}
	}
	return diag
}
