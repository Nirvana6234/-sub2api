package service

import "time"

// CPASchedulingProfile is an explicit allowlist. Never embed Config, Account,
// Credentials, Extra, proxy definitions, or administrator settings in this DTO.
type CPASchedulingProfile struct {
	SchemaVersion            int                         `json:"schema_version"`
	SourceInstanceID         string                      `json:"source_instance_id"`
	Revision                 string                      `json:"revision,omitempty"`
	GeneratedAt              *time.Time                  `json:"generated_at,omitempty"`
	Scheduler                CPASchedulerPolicy          `json:"scheduler"`
	Waiting                  CPAWaitingPolicy            `json:"waiting"`
	Retry                    CPARetryPolicy              `json:"retry"`
	Cooldown                 CPACooldownPolicy           `json:"cooldown"`
	ProviderCreationDefaults CPAProviderCreationDefaults `json:"provider_creation_defaults"`
	Accounts                 []CPAAccountScheduling      `json:"accounts"`
}

type CPASchedulerPolicy struct {
	AdvancedEnabled                bool                `json:"advanced_enabled"`
	TopK                           int                 `json:"top_k"`
	Weights                        CPASchedulerWeights `json:"weights"`
	StickyWeightedEnabled          bool                `json:"sticky_weighted_enabled"`
	SubscriptionPriorityEnabled    bool                `json:"subscription_priority_enabled"`
	PriorityOrder                  string              `json:"priority_order"`
	LowUpstreamRatePriorityEnabled bool                `json:"low_upstream_rate_priority_enabled"`
	OAuthSchedulingRateMultiplier  float64             `json:"oauth_scheduling_rate_multiplier"`
	Sticky                         CPAStickyPolicy     `json:"sticky"`
	Health                         CPAHealthPolicy     `json:"health"`
	LatencyFallback                CPALatencyPolicy    `json:"latency_fallback"`
}

type CPASchedulerWeights struct {
	Priority         float64 `json:"priority"`
	Load             float64 `json:"load"`
	Queue            float64 `json:"queue"`
	ErrorRate        float64 `json:"error_rate"`
	TTFT             float64 `json:"ttft"`
	Reset            float64 `json:"reset"`
	QuotaHeadroom    float64 `json:"quota_headroom"`
	UpstreamCost     float64 `json:"upstream_cost"`
	PreviousResponse float64 `json:"previous_response"`
	SessionSticky    float64 `json:"session_sticky"`
}

type CPAStickyPolicy struct {
	SessionTTLSeconds    int64   `json:"session_ttl_seconds"`
	ResponseIDTTLSeconds int     `json:"response_id_ttl_seconds"`
	EscapeEnabled        bool    `json:"escape_enabled"`
	EscapeTTFTMS         float64 `json:"escape_ttft_ms"`
	EscapeErrorRate      float64 `json:"escape_error_rate"`
	EscapeComparison     string  `json:"escape_comparison"`
}

type CPAHealthPolicy struct {
	EWMAAlpha      float64 `json:"ewma_alpha"`
	EWMATTLSeconds int     `json:"ewma_ttl_seconds"`
}

type CPALatencyPolicy struct {
	Enabled          bool     `json:"enabled"`
	ThresholdMS      int      `json:"threshold_ms"`
	SpeedupRatio     float64  `json:"speedup_ratio"`
	Percentile       float64  `json:"percentile"`
	MinSamples       int      `json:"min_samples"`
	SampleTTLSeconds int64    `json:"sample_ttl_seconds"`
	SampleWindowSize int      `json:"sample_window_size"`
	ReasoningBuckets []string `json:"reasoning_buckets"`
}

type CPAAccountWaitPolicy struct {
	MaxWaiting int   `json:"max_waiting"`
	TimeoutMS  int64 `json:"timeout_ms"`
}

type CPAWaitingPolicy struct {
	Scope                     string               `json:"scope"`
	Sticky                    CPAAccountWaitPolicy `json:"sticky"`
	Fallback                  CPAAccountWaitPolicy `json:"fallback"`
	FallbackSelectionMode     string               `json:"fallback_selection_mode"`
	PreferSoonestReset        bool                 `json:"prefer_soonest_reset"`
	ConcurrencySlotTTLSeconds int                  `json:"concurrency_slot_ttl_seconds"`
}

type CPARetryPolicy struct {
	MaxAccountSwitches       int                     `json:"max_account_switches"`
	MaxAccountSwitchesGemini int                     `json:"max_account_switches_gemini"`
	SameAccountDefault       int                     `json:"same_account_default"`
	SameAccountMax           int                     `json:"same_account_max"`
	SameAccountDelayMS       int                     `json:"same_account_delay_ms"`
	RequestScopedMaxDelayMS  int                     `json:"request_scoped_max_delay_ms"`
	Websocket                CPAWebsocketRetryPolicy `json:"websocket"`
}

type CPAWebsocketRetryPolicy struct {
	MaxRetries       int     `json:"max_retries"`
	BackoffInitialMS int     `json:"backoff_initial_ms"`
	BackoffMaxMS     int     `json:"backoff_max_ms"`
	JitterRatio      float64 `json:"jitter_ratio"`
	TotalBudgetMS    int64   `json:"total_budget_ms"`
}

type CPACooldownSetting struct {
	Enabled    bool  `json:"enabled"`
	DurationMS int64 `json:"duration_ms"`
}

type CPACooldownPolicy struct {
	Overload     CPACooldownSetting `json:"overload"`
	RateLimit429 CPACooldownSetting `json:"rate_limit_429"`
}

type CPAProviderCreationDefaults struct {
	Scope               string `json:"scope"`
	Source              string `json:"source"`
	OrdinaryConcurrency int    `json:"ordinary_concurrency"`
	GrokConcurrency     int    `json:"grok_concurrency"`
	OfficialPlanLimits  bool   `json:"official_plan_limits"`
}

type CPAAccountIdentity struct {
	ChatGPTAccountID  string `json:"chatgpt_account_id"`
	ChatGPTUserID     string `json:"chatgpt_user_id"`
	WorkspaceID       string `json:"workspace_id"`
	OrganizationID    string `json:"organization_id"`
	ProviderAccountID string `json:"provider_account_id"`
}

type CPAGroupPriority struct {
	GroupID  int64 `json:"group_id"`
	Priority int   `json:"priority"`
}

type CPAAccountScheduling struct {
	SourceAccountID              int64              `json:"source_account_id"`
	Name                         string             `json:"name"`
	Platform                     string             `json:"platform"`
	Type                         string             `json:"type"`
	Identity                     CPAAccountIdentity `json:"identity"`
	IsShadow                     bool               `json:"is_shadow"`
	ParentAccountID              *int64             `json:"parent_account_id"`
	QuotaDimension               string             `json:"quota_dimension"`
	OAuthIdentityMatchable       bool               `json:"oauth_identity_matchable"`
	IsPoolMode                   bool               `json:"is_pool_mode"`
	Concurrency                  int                `json:"concurrency"`
	LoadFactor                   *int               `json:"load_factor"`
	EffectiveLoadFactor          int                `json:"effective_load_factor"`
	Priority                     int                `json:"priority"`
	GroupIDs                     []int64            `json:"group_ids"`
	GroupPriorities              []CPAGroupPriority `json:"group_priorities"`
	Status                       string             `json:"status"`
	Schedulable                  bool               `json:"schedulable"`
	ExpiresAt                    *time.Time         `json:"expires_at"`
	AutoPauseOnExpired           bool               `json:"auto_pause_on_expired"`
	RateLimitResetAt             *time.Time         `json:"rate_limit_reset_at"`
	OverloadUntil                *time.Time         `json:"overload_until"`
	TempUnschedulableUntil       *time.Time         `json:"temp_unschedulable_until"`
	PoolModeRetryCount           int                `json:"pool_mode_retry_count"`
	PoolModeRetryStatusCodes     []int              `json:"pool_mode_retry_status_codes"`
	SubscriptionPriorityEligible bool               `json:"subscription_priority_eligible"`
	ResetWindowEnd               *time.Time         `json:"reset_window_end"`
	QuotaHeadroomFactor          float64            `json:"quota_headroom_factor"`
	UpstreamCostRate             *float64           `json:"upstream_cost_rate"`
	UpdatedAt                    time.Time          `json:"updated_at"`
}
