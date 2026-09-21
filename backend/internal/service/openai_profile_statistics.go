package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	codexProfileStatisticsURL = "https://chatgpt.com/backend-api/wham/profiles/me"
	// codexProfileStatisticsMaxBodyBytes guards against an upstream returning an
	// abusive payload; the real response is a small JSON document.
	codexProfileStatisticsMaxBodyBytes = 1024 * 1024
	codexProfileStatisticsTimeout      = 15 * time.Second
)

var (
	ErrCodexProfileStatisticsUnavailable     = infraerrors.ServiceUnavailable("CODEX_PROFILE_UNAVAILABLE", "failed to fetch Codex profile statistics")
	ErrCodexProfileStatisticsInvalidResponse = infraerrors.New(http.StatusBadGateway, "CODEX_PROFILE_INVALID_RESPONSE", "upstream returned an invalid profile response")
)

// CodexProfileStatistics is our stable representation of the OpenAI/Codex
// "profiles/me" response. Field names intentionally diverge from the upstream
// wire format so our API/cache contract doesn't move if OpenAI renames theirs.
type CodexProfileStatistics struct {
	DisplayName            string                   `json:"display_name,omitempty"`
	Username               string                   `json:"username,omitempty"`
	AvatarURL              string                   `json:"avatar_url,omitempty"`
	HasStatsError          bool                     `json:"has_stats_error"`
	LifetimeTokens         *int64                   `json:"lifetime_tokens,omitempty"`
	PeakDailyTokens        *int64                   `json:"peak_daily_tokens,omitempty"`
	LongestTurnSeconds     *int64                   `json:"longest_turn_seconds,omitempty"`
	CurrentStreakDays      *int64                   `json:"current_streak_days,omitempty"`
	LongestStreakDays      *int64                   `json:"longest_streak_days,omitempty"`
	DailyUsage             []CodexProfileDailyUsage `json:"daily_usage,omitempty"`
	FastModePercent        *float64                 `json:"fast_mode_percent,omitempty"`
	ReasoningEffort        string                   `json:"reasoning_effort,omitempty"`
	ReasoningEffortPercent *float64                 `json:"reasoning_effort_percent,omitempty"`
	UniqueSkillsUsed       *int64                   `json:"unique_skills_used,omitempty"`
	TotalSkillsUsed        *int64                   `json:"total_skills_used,omitempty"`
	TotalThreads           *int64                   `json:"total_threads,omitempty"`
	TopInvocations         []CodexProfileInvocation `json:"top_invocations,omitempty"`
	FetchedAt              time.Time                `json:"fetched_at"`
}

type CodexProfileDailyUsage struct {
	Date   string `json:"date"`
	Tokens int64  `json:"tokens"`
}

type CodexProfileInvocation struct {
	Type       string `json:"type"`
	PluginID   string `json:"plugin_id,omitempty"`
	PluginName string `json:"plugin_name,omitempty"`
	SkillID    string `json:"skill_id,omitempty"`
	SkillName  string `json:"skill_name,omitempty"`
	UsageCount *int64 `json:"usage_count,omitempty"`
}

// --- upstream wire format (unexported; never leaves this file as-is) ---

type codexProfileStatisticsWire struct {
	Profile  codexProfileWire         `json:"profile"`
	Stats    codexStatisticsWire      `json:"stats"`
	Metadata codexProfileMetadataWire `json:"metadata"`
}

type codexProfileWire struct {
	DisplayName       *string `json:"display_name"`
	Username          *string `json:"username"`
	ProfilePictureURL *string `json:"profile_picture_url"`
}

type codexStatisticsWire struct {
	LifetimeTokens                    *int64                `json:"lifetime_tokens"`
	PeakDailyTokens                   *int64                `json:"peak_daily_tokens"`
	LongestRunningTurnSec             *int64                `json:"longest_running_turn_sec"`
	CurrentStreakDays                 *int64                `json:"current_streak_days"`
	LongestStreakDays                 *int64                `json:"longest_streak_days"`
	DailyUsageBuckets                 []codexDailyUsageWire `json:"daily_usage_buckets"`
	FastModeUsagePercentage           *float64              `json:"fast_mode_usage_percentage"`
	MostUsedReasoningEffort           *string               `json:"most_used_reasoning_effort"`
	MostUsedReasoningEffortPercentage *float64              `json:"most_used_reasoning_effort_percentage"`
	UniqueSkillsUsed                  *int64                `json:"unique_skills_used"`
	TotalSkillsUsed                   *int64                `json:"total_skills_used"`
	TotalThreads                      *int64                `json:"total_threads"`
	TopInvocations                    []codexInvocationWire `json:"top_invocations"`
}

type codexDailyUsageWire struct {
	StartDate string `json:"start_date"`
	Tokens    int64  `json:"tokens"`
}

type codexInvocationWire struct {
	Type       string  `json:"type"`
	PluginID   *string `json:"plugin_id"`
	PluginName *string `json:"plugin_name"`
	SkillID    *string `json:"skill_id"`
	SkillName  *string `json:"skill_name"`
	UsageCount *int64  `json:"usage_count"`
}

type codexProfileMetadataWire struct {
	StatsError *string `json:"stats_error"`
}

// fetchCodexProfileStatistics calls OpenAI/Codex's "profiles/me" endpoint
// through the shared privacy-client factory (same ImpersonateChrome + proxy
// dial path already used for accounts/check and the training opt-out call).
// The access token is never logged; only status codes, sizes, and derived
// (non-secret) fields appear in log output.
func fetchCodexProfileStatistics(ctx context.Context, clientFactory PrivacyClientFactory, accessToken, proxyURL, chatGPTAccountID string) (*CodexProfileStatistics, error) {
	if accessToken == "" {
		return nil, infraerrors.BadRequest("CODEX_PROFILE_NO_CREDENTIAL", "account has no access token")
	}
	if clientFactory == nil {
		return nil, ErrCodexProfileStatisticsUnavailable
	}

	ctx, cancel := context.WithTimeout(ctx, codexProfileStatisticsTimeout)
	defer cancel()

	client, err := clientFactory(proxyURL)
	if err != nil {
		slog.Warn("codex_profile_statistics_client_error", "error", err.Error())
		return nil, ErrCodexProfileStatisticsUnavailable
	}

	reqBuilder := client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+accessToken).
		SetHeader("Origin", "https://chatgpt.com").
		SetHeader("Referer", "https://chatgpt.com/").
		SetHeader("Accept", "application/json")
	if chatGPTAccountID != "" {
		reqBuilder = reqBuilder.SetHeader("chatgpt-account-id", chatGPTAccountID)
	}

	resp, err := reqBuilder.Get(codexProfileStatisticsURL)
	if err != nil {
		slog.Warn("codex_profile_statistics_request_error", "error", err.Error())
		return nil, ErrCodexProfileStatisticsUnavailable
	}

	if !resp.IsSuccessState() {
		slog.Warn("codex_profile_statistics_failed", "status", resp.StatusCode, "body", truncate(resp.String(), 200))
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, infraerrors.New(http.StatusUnauthorized, "CODEX_PROFILE_UNAUTHORIZED", "upstream rejected the account credential")
		case http.StatusTooManyRequests:
			return nil, infraerrors.New(http.StatusTooManyRequests, "CODEX_PROFILE_RATE_LIMITED", "upstream rate-limited the profile request")
		default:
			return nil, ErrCodexProfileStatisticsUnavailable
		}
	}

	body := resp.Bytes()
	if len(body) > codexProfileStatisticsMaxBodyBytes {
		slog.Warn("codex_profile_statistics_body_too_large", "size", len(body))
		return nil, ErrCodexProfileStatisticsInvalidResponse
	}

	var wire codexProfileStatisticsWire
	if err := json.Unmarshal(body, &wire); err != nil {
		slog.Warn("codex_profile_statistics_decode_error", "error", err.Error())
		return nil, ErrCodexProfileStatisticsInvalidResponse
	}

	stats := mapCodexProfileStatisticsWire(&wire)
	slog.Info("codex_profile_statistics_success",
		"has_stats_error", stats.HasStatsError,
		"daily_usage_points", len(stats.DailyUsage),
		"top_invocations", len(stats.TopInvocations))
	return stats, nil
}

func mapCodexProfileStatisticsWire(wire *codexProfileStatisticsWire) *CodexProfileStatistics {
	stats := &CodexProfileStatistics{
		DisplayName:            strings.TrimSpace(derefString(wire.Profile.DisplayName)),
		Username:               strings.TrimSpace(derefString(wire.Profile.Username)),
		AvatarURL:              strings.TrimSpace(derefString(wire.Profile.ProfilePictureURL)),
		LifetimeTokens:         wire.Stats.LifetimeTokens,
		PeakDailyTokens:        wire.Stats.PeakDailyTokens,
		LongestTurnSeconds:     wire.Stats.LongestRunningTurnSec,
		CurrentStreakDays:      wire.Stats.CurrentStreakDays,
		LongestStreakDays:      wire.Stats.LongestStreakDays,
		FastModePercent:        wire.Stats.FastModeUsagePercentage,
		ReasoningEffort:        strings.TrimSpace(derefString(wire.Stats.MostUsedReasoningEffort)),
		ReasoningEffortPercent: wire.Stats.MostUsedReasoningEffortPercentage,
		UniqueSkillsUsed:       wire.Stats.UniqueSkillsUsed,
		TotalSkillsUsed:        wire.Stats.TotalSkillsUsed,
		TotalThreads:           wire.Stats.TotalThreads,
		FetchedAt:              time.Now(),
	}
	if wire.Metadata.StatsError != nil && strings.TrimSpace(*wire.Metadata.StatsError) != "" {
		stats.HasStatsError = true
	}
	for _, bucket := range wire.Stats.DailyUsageBuckets {
		stats.DailyUsage = append(stats.DailyUsage, CodexProfileDailyUsage{
			Date:   bucket.StartDate,
			Tokens: bucket.Tokens,
		})
	}
	for _, inv := range wire.Stats.TopInvocations {
		stats.TopInvocations = append(stats.TopInvocations, CodexProfileInvocation{
			Type:       inv.Type,
			PluginID:   derefString(inv.PluginID),
			PluginName: derefString(inv.PluginName),
			SkillID:    derefString(inv.SkillID),
			SkillName:  derefString(inv.SkillName),
			UsageCount: inv.UsageCount,
		})
	}
	return stats
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// AccountProfileStatisticsCache caches a fetched profile so repeated admin
// panel views don't re-hit OpenAI on every render.
type AccountProfileStatisticsCache interface {
	Get(ctx context.Context, accountID int64) (*CodexProfileStatistics, bool)
	Set(ctx context.Context, accountID int64, stats *CodexProfileStatistics) error
}

// AccountProfileStatisticsService resolves an admin-managed OpenAI OAuth
// account's proxy and credential, then fetches (or serves cached) Codex
// profile statistics for it. Deliberately independent of the scheduling
// algorithm and TransitHub latency-compensation code paths — this is an
// on-demand admin lookup, not part of the request-forwarding hot path.
type AccountProfileStatisticsService struct {
	accountRepo   AccountRepository
	proxyRepo     ProxyRepository
	clientFactory PrivacyClientFactory
	cache         AccountProfileStatisticsCache
}

func NewAccountProfileStatisticsService(
	accountRepo AccountRepository,
	proxyRepo ProxyRepository,
	clientFactory PrivacyClientFactory,
	cache AccountProfileStatisticsCache,
) *AccountProfileStatisticsService {
	return &AccountProfileStatisticsService{
		accountRepo:   accountRepo,
		proxyRepo:     proxyRepo,
		clientFactory: clientFactory,
		cache:         cache,
	}
}

// GetProfileStatistics returns cached statistics unless forceRefresh is set
// or nothing is cached yet, in which case it dials the upstream account
// directly through the account's configured proxy (if any).
func (s *AccountProfileStatisticsService) GetProfileStatistics(ctx context.Context, accountID int64, forceRefresh bool) (*CodexProfileStatistics, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrCodexProfileStatisticsUnavailable
	}

	if !forceRefresh && s.cache != nil {
		if cached, ok := s.cache.Get(ctx, accountID); ok {
			return cached, nil
		}
	}

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, infraerrors.NotFound("ACCOUNT_NOT_FOUND", "account not found")
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return nil, infraerrors.BadRequest("CODEX_PROFILE_UNSUPPORTED_ACCOUNT", "profile statistics are only available for OpenAI OAuth accounts")
	}

	accessToken := account.GetCredential("access_token")
	chatGPTAccountID := account.GetCredential("chatgpt_account_id")

	var proxyURL string
	if account.ProxyID != nil && s.proxyRepo != nil {
		if proxy, perr := s.proxyRepo.GetByID(ctx, *account.ProxyID); perr == nil && proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	stats, err := fetchCodexProfileStatistics(ctx, s.clientFactory, accessToken, proxyURL, chatGPTAccountID)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		if cerr := s.cache.Set(ctx, accountID, stats); cerr != nil {
			slog.Warn("codex_profile_statistics_cache_set_failed", "account_id", strconv.FormatInt(accountID, 10), "error", cerr.Error())
		}
	}
	return stats, nil
}
