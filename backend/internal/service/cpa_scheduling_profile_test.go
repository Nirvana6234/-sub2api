package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cpaProfileAccountRepo struct {
	AccountRepository
	accounts []Account
	err      error
}

func (r *cpaProfileAccountRepo) ListAllWithFilters(_ context.Context, platform, kind, status, search string, groupID int64, privacy string) ([]Account, error) {
	if platform != "" || kind != "" || status != "" || search != "" || groupID != 0 || privacy != "" {
		return nil, errors.New("unexpected filtered account export")
	}
	return append([]Account(nil), r.accounts...), r.err
}

func newCPAProfileTestService(t *testing.T) (*OpenAIGatewayService, *cpaProfileAccountRepo, *openAIAdvancedSchedulerSettingRepoStub) {
	t.Helper()
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	repo := &cpaProfileAccountRepo{}
	settings := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{
		openAIAdvancedSchedulerSettingKey:                            "true",
		SettingKeyOpenAIAdvancedSchedulerLBTopK:                      "11",
		SettingKeyOpenAIAdvancedSchedulerWeightLoad:                  "2.25",
		SettingKeyOpenAIAdvancedSchedulerWeightPriority:              "0",
		SettingKeyOpenAIAdvancedSchedulerStickyWeightedEnabled:       "true",
		SettingKeyOpenAIAdvancedSchedulerSubscriptionPriorityEnabled: "true",
		SettingKeyOverloadCooldownSettings:                           `{"enabled":false,"cooldown_minutes":4}`,
		SettingKeyRateLimit429CooldownSettings:                       `{"enabled":true,"cooldown_seconds":9}`,
	}}
	cfg := &config.Config{
		CPAScheduling: config.CPASchedulingConfig{SourceID: "source-test", SyncToken: "secret-not-exported-secret-not-exported"},
		Gateway: config.GatewayConfig{
			MaxAccountSwitches: 8, MaxAccountSwitchesGemini: 2,
			ConcurrencySlotTTLMinutes: 30,
			OpenAIWS: config.GatewayOpenAIWSConfig{
				LBTopK: 7, StickySessionTTLSeconds: 1200, StickyResponseIDTTLSeconds: 900,
				RetryTotalBudgetMS: 7777, RetryBackoffInitialMS: 150, RetryBackoffMaxMS: 900, RetryJitterRatio: 0,
				SchedulerScoreWeights: config.GatewayOpenAIWSSchedulerScoreWeights{Priority: 1, Load: 1, Queue: .7, ErrorRate: .8, TTFT: .5, PreviousResponse: 5, SessionSticky: 3},
			},
			OpenAIScheduler: config.GatewayOpenAISchedulerConfig{StickyEscapeEnabled: true, StickyEscapeTTFTMs: 23456, StickyEscapeErrorRate: .4},
			Scheduling:      config.GatewaySchedulingConfig{StickySessionMaxWaiting: 2, StickySessionWaitTimeout: 90 * time.Second, FallbackMaxWaiting: 45, FallbackWaitTimeout: 12 * time.Second, FallbackSelectionMode: "last_used"},
		},
	}
	settingSvc := &SettingService{settingRepo: settings, cfg: cfg}
	s := &OpenAIGatewayService{cfg: cfg, accountRepo: repo, settingService: settingSvc, rateLimitService: &RateLimitService{settingService: settingSvc}}
	return s, repo, settings
}

func TestCPASchedulingExportsEffectivePolicyAndCompleteSafeAccounts(t *testing.T) {
	s, repo, _ := newCPAProfileTestService(t)
	for id := int64(251); id > 0; id-- {
		repo.accounts = append(repo.accounts, Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true})
	}
	zero, parent := 0, int64(1)
	repo.accounts[0].Credentials = map[string]any{
		"access_token": "secret-access", "refresh_token": "secret-refresh", "id_token": "secret-id", "api_key": "secret-api", "email": "private@example.test",
		"chatgpt_account_id": "chatgpt-stable", "chatgpt_user_id": "user-stable", "pool_mode": true,
		"pool_mode_retry_count": 0, "pool_mode_retry_status_codes": []any{},
	}
	repo.accounts[0].Extra = map[string]any{"unknown_secret": "secret-extra"}
	repo.accounts[0].ParentAccountID = &parent
	repo.accounts[0].Concurrency = 0
	repo.accounts[0].LoadFactor = &zero
	repo.accounts[0].GroupIDs = []int64{9, 2}
	repo.accounts[0].AccountGroups = []AccountGroup{{GroupID: 9, Priority: 4}, {GroupID: 2, Priority: 1}}
	repo.accounts[0].Status = StatusDisabled
	profile, err := s.CPASchedulingProfile(context.Background())
	require.NoError(t, err)
	require.Len(t, profile.Accounts, 251)
	require.Equal(t, int64(1), profile.Accounts[0].SourceAccountID)
	a := profile.Accounts[250]
	require.Zero(t, a.Concurrency)
	require.Equal(t, 1, a.EffectiveLoadFactor)
	require.Empty(t, a.Identity.ChatGPTAccountID)
	require.False(t, a.OAuthIdentityMatchable)
	require.True(t, a.IsShadow)
	require.True(t, a.IsPoolMode)
	require.Empty(t, a.PoolModeRetryStatusCodes)
	require.NotNil(t, a.PoolModeRetryStatusCodes)
	require.Equal(t, []int64{2, 9}, a.GroupIDs)
	require.Equal(t, int64(2), a.GroupPriorities[0].GroupID)
	require.Equal(t, StatusDisabled, a.Status)
	require.Equal(t, 11, profile.Scheduler.TopK)
	require.Equal(t, 2.25, profile.Scheduler.Weights.Load)
	require.Zero(t, profile.Scheduler.Weights.Priority)
	require.Equal(t, .7, profile.Scheduler.Weights.Queue)
	require.True(t, profile.Scheduler.StickyWeightedEnabled)
	require.True(t, profile.Scheduler.SubscriptionPriorityEnabled)
	require.Equal(t, "ascending", profile.Scheduler.PriorityOrder)
	require.Equal(t, float64(23456), profile.Scheduler.Sticky.EscapeTTFTMS)
	require.Equal(t, int64(1200), profile.Scheduler.Sticky.SessionTTLSeconds)
	require.Equal(t, 900, profile.Scheduler.Sticky.ResponseIDTTLSeconds)
	require.Zero(t, profile.Scheduler.Health.EWMATTLSeconds)
	require.Equal(t, []string{"normal", "high"}, profile.Scheduler.LatencyFallback.ReasoningBuckets)
	require.Equal(t, 20, profile.Scheduler.LatencyFallback.SampleWindowSize)
	require.Equal(t, int64(90000), profile.Waiting.Sticky.TimeoutMS)
	require.Equal(t, 45, profile.Waiting.Fallback.MaxWaiting)
	require.Equal(t, 8, profile.Retry.MaxAccountSwitches)
	require.Equal(t, int64(7777), profile.Retry.Websocket.TotalBudgetMS)
	require.Zero(t, profile.Retry.Websocket.JitterRatio)
	require.Equal(t, int64(9000), profile.Cooldown.RateLimit429.DurationMS)
	require.False(t, profile.Cooldown.Overload.Enabled)
	require.Equal(t, 10, profile.ProviderCreationDefaults.OrdinaryConcurrency)
	require.False(t, profile.ProviderCreationDefaults.OfficialPlanLimits)
	raw, err := json.Marshal(profile)
	require.NoError(t, err)
	for _, secret := range []string{"secret-access", "secret-refresh", "secret-id", "secret-api", "secret-extra", "private@example.test", s.cfg.CPAScheduling.SyncToken, "credentials", "extra"} {
		require.NotContains(t, string(raw), secret)
	}
}

func TestCPASchedulingRevisionIsCanonicalAndIgnoresGenerationTime(t *testing.T) {
	s, repo, settings := newCPAProfileTestService(t)
	repo.accounts = []Account{{ID: 9007199254740993, Platform: PlatformOpenAI, Type: AccountTypeOAuth}}
	one, err := s.CPASchedulingProfile(context.Background())
	require.NoError(t, err)
	raw, err := json.Marshal(one)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var independent map[string]any
	require.NoError(t, decoder.Decode(&independent))
	delete(independent, "revision")
	delete(independent, "generated_at")
	canonical, err := json.Marshal(independent)
	require.NoError(t, err)
	sum := sha256.Sum256(canonical)
	require.Equal(t, hex.EncodeToString(sum[:]), one.Revision)
	two, err := s.CPASchedulingProfile(context.Background())
	require.NoError(t, err)
	require.Equal(t, one.Revision, two.Revision)
	settings.values[SettingKeyOpenAIAdvancedSchedulerLBTopK] = "12"
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	three, err := s.CPASchedulingProfile(context.Background())
	require.NoError(t, err)
	require.NotEqual(t, one.Revision, three.Revision)
	require.Equal(t, 12, three.Scheduler.TopK)
}

func TestCPASchedulingHonorsDisabledOverrideGateAndAccountErrors(t *testing.T) {
	s, repo, settings := newCPAProfileTestService(t)
	settings.values[openAIAdvancedSchedulerSettingKey] = "false"
	profile, err := s.CPASchedulingProfile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 7, profile.Scheduler.TopK)
	require.Equal(t, 1.0, profile.Scheduler.Weights.Load)
	require.False(t, profile.Scheduler.StickyWeightedEnabled)
	require.False(t, profile.Scheduler.SubscriptionPriorityEnabled)
	require.Empty(t, profile.Accounts)
	require.NotNil(t, profile.Accounts)
	repo.err = errors.New("database unavailable")
	profile, err = s.CPASchedulingProfile(context.Background())
	require.Error(t, err)
	require.Nil(t, profile)
}

func TestCPASchedulingDefaultRetryCodesAndIdentitySafety(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "stable"}}
	profile := cpaAccountScheduling(a, time.Now(), floatPtr(1))
	require.True(t, profile.OAuthIdentityMatchable)
	require.Equal(t, []int{401, 403, 429}, profile.PoolModeRetryStatusCodes)
	a.Type = AccountTypeAPIKey
	profile = cpaAccountScheduling(a, time.Now(), floatPtr(1))
	require.False(t, profile.OAuthIdentityMatchable)
	a.Type = AccountTypeOAuth
	delete(a.Credentials, "chatgpt_account_id")
	profile = cpaAccountScheduling(a, time.Now(), floatPtr(1))
	require.False(t, profile.OAuthIdentityMatchable)
}
