//go:build unit

package service

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

type headroomCacheStub struct {
	billingCacheWorkerStub
	balance    float64
	balanceErr error
	sub        *SubscriptionCacheData
	quota      *UserPlatformQuotaCacheEntry
	quotaErr   error
	rate       *APIKeyRateLimitCacheData
}

func (s *headroomCacheStub) GetUserBalance(context.Context, int64) (float64, error) {
	return s.balance, s.balanceErr
}

func (s *headroomCacheStub) GetSubscriptionCache(context.Context, int64, int64) (*SubscriptionCacheData, error) {
	if s.sub == nil {
		return nil, errors.New("miss")
	}
	return s.sub, nil
}

func (s *headroomCacheStub) GetUserPlatformQuotaCache(context.Context, int64, string) (*UserPlatformQuotaCacheEntry, bool, error) {
	if s.quotaErr != nil {
		return nil, false, s.quotaErr
	}
	return s.quota, s.quota != nil, nil
}

func (s *headroomCacheStub) GetAPIKeyRateLimit(context.Context, int64) (*APIKeyRateLimitCacheData, error) {
	if s.rate == nil {
		return nil, errors.New("miss")
	}
	return s.rate, nil
}

func f64(v float64) *float64 { return &v }

func byDim(hs []QuotaHeadroom) map[string]QuotaHeadroom {
	out := map[string]QuotaHeadroom{}
	for _, h := range hs {
		out[h.Dimension] = h
	}
	return out
}

// 余额模式：余额减最低保留额；平台配额按缓存（窗口已重置）；Key 总额度与窗口限额；没上限的窗口不出现。
func TestQuotaHeadroomBalanceMode(t *testing.T) {
	now := time.Now()
	dayStart, weekStart := timezone.StartOfDay(now), timezone.StartOfWeek(now)
	yesterday := dayStart.Add(-time.Hour)
	cache := &headroomCacheStub{
		balance: 12,
		quota: &UserPlatformQuotaCacheEntry{
			SchemaVersion: UserPlatformQuotaCacheSchemaV1,
			DailyLimitUSD: f64(5), DailyUsageUSD: 4, DailyWindowStart: &yesterday, // 窗口已过期 → 用量归零
			WeeklyLimitUSD: f64(20), WeeklyUsageUSD: 7, WeeklyWindowStart: &weekStart,
			MonthlyWindowStart: &now,
		},
		rate: &APIKeyRateLimitCacheData{Usage5h: 1.5, Usage1d: 3, Window5h: now.Unix(), Window1d: now.Unix()},
	}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.5
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, &quotaRepoStub{})
	t.Cleanup(svc.Stop)
	expires := now.Add(48 * time.Hour)
	key := &APIKey{ID: 9, Quota: 10, QuotaUsed: 2.5, RateLimit5h: 4, RateLimit7d: 50, ExpiresAt: &expires}

	hs, err := svc.QuotaHeadroom(context.Background(), QuotaRequest{User: &User{ID: 1}, APIKey: key, Platform: "anthropic"})
	require.NoError(t, err)
	m := byDim(hs)
	require.InDelta(t, 11.5, m[QuotaDimBalance].Remaining, 1e-9, "balance minus the minimum reserve")
	require.InDelta(t, 5, m[QuotaDimPlatformDaily].Remaining, 1e-9, "the expired daily window starts from zero")
	require.Equal(t, "anthropic", m[QuotaDimPlatformDaily].ScopeKey)
	require.InDelta(t, 13, m[QuotaDimPlatformWeekly].Remaining, 1e-9)
	require.NotContains(t, m, QuotaDimPlatformMonthly, "no limit, no sub-quota")
	require.InDelta(t, 7.5, m[QuotaDimAPIKeyTotal].Remaining, 1e-9)
	require.Equal(t, int64(9), m[QuotaDimAPIKeyTotal].ScopeID)
	require.Equal(t, expires, *m[QuotaDimAPIKeyTotal].ExpiresAt)
	require.InDelta(t, 2.5, m[QuotaDimAPIKey5h].Remaining, 1e-9)
	require.InDelta(t, 50, m[QuotaDimAPIKey7d].Remaining, 1e-9)
	require.NotContains(t, m, QuotaDimAPIKey1d)
	require.NotContains(t, m, QuotaDimSubscriptionDaily)
}

// 订阅模式：订阅窗口按分组上限比订阅用量，scope 是分组 ID，有效期不超过订阅到期；不看余额和平台配额。
func TestQuotaHeadroomSubscriptionMode(t *testing.T) {
	expires := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	cache := &headroomCacheStub{balance: 0, sub: &SubscriptionCacheData{Status: SubscriptionStatusActive, ExpiresAt: expires, DailyUsage: 1, MonthlyUsage: 30}}
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, &config.Config{}, &quotaRepoStub{})
	t.Cleanup(svc.Stop)
	group := &Group{ID: 5, SubscriptionType: SubscriptionTypeSubscription, DailyLimitUSD: f64(3), MonthlyLimitUSD: f64(30)}
	req := QuotaRequest{User: &User{ID: 1}, Group: group, Subscription: &UserSubscription{ID: 77}, Platform: "openai"}

	hs, err := svc.QuotaHeadroom(context.Background(), req)
	require.NoError(t, err)
	m := byDim(hs)
	require.Len(t, m, 2)
	require.InDelta(t, 2, m[QuotaDimSubscriptionDaily].Remaining, 1e-9)
	require.Equal(t, int64(5), m[QuotaDimSubscriptionDaily].ScopeID, "scope is the group id")
	require.Equal(t, expires, *m[QuotaDimSubscriptionDaily].ExpiresAt)
	require.InDelta(t, 0, m[QuotaDimSubscriptionMonthly].Remaining, 1e-9)
	require.Error(t, svc.CheckBillingEligibility(context.Background(), req.User, nil, group, req.Subscription, "openai"),
		"the check rejects exactly when a window has nothing left")

	cache.sub.Status = "expired"
	_, err = svc.QuotaHeadroom(context.Background(), req)
	require.ErrorIs(t, err, ErrSubscriptionInvalid)
}

// 资格检查在部分维度上取不到数据时放行；锁定额度不行，一律报错。
func TestQuotaHeadroomFailsClosed(t *testing.T) {
	// 余额缓存和数据库都读不到。
	cache := &headroomCacheStub{balanceErr: errors.New("redis down")}
	svc := NewBillingCacheService(cache, &mockUserRepo{getByIDErr: errors.New("db down")}, nil, nil, nil, nil, &config.Config{}, &quotaRepoStub{})
	t.Cleanup(svc.Stop)
	_, err := svc.QuotaHeadroom(context.Background(), QuotaRequest{User: &User{ID: 1}})
	require.ErrorIs(t, err, ErrBillingServiceUnavailable)

	// 平台配额：Redis 和数据库都读不到。
	cache.balanceErr = nil
	cache.balance = 5
	cache.quotaErr = errors.New("redis down")
	svc = NewBillingCacheService(cache, nil, nil, nil, nil, nil, &config.Config{}, &platformQuotaDBStub{err: errors.New("db down")})
	t.Cleanup(svc.Stop)
	_, err = svc.QuotaHeadroom(context.Background(), QuotaRequest{User: &User{ID: 1}, Platform: "anthropic"})
	require.ErrorIs(t, err, ErrBillingServiceUnavailable, "the platform quota row cannot be read")
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "anthropic"),
		"while the eligibility check fails open")

	simple := &config.Config{RunMode: config.RunModeSimple}
	svc2 := NewBillingCacheService(cache, nil, nil, nil, nil, nil, simple, nil)
	t.Cleanup(svc2.Stop)
	hs, err := svc2.QuotaHeadroom(context.Background(), QuotaRequest{User: &User{ID: 1}})
	require.NoError(t, err)
	require.Empty(t, hs, "simple mode has no billing limits")
}

// 资格检查拒绝 ⇔ 某项剩余 ≤ 0（平台配额、Key 窗口限额；随机数据）。
func TestHeadroomAgreesWithTheEligibilityChecks(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	pick := func() *float64 {
		switch rng.Intn(3) {
		case 0:
			return nil
		case 1:
			return f64(0)
		}
		return f64(float64(rng.Intn(10)))
	}
	for i := 0; i < 2000; i++ {
		st := &platformQuotaState{now: time.Now(), dailyLimit: pick(), weeklyLimit: pick(), monthlyLimit: pick(),
			daily: float64(rng.Intn(12)), weekly: float64(rng.Intn(12)), monthly: float64(rng.Intn(12))}
		exhausted := false
		for _, h := range st.headroom("p") {
			exhausted = exhausted || h.Remaining <= 0
		}
		require.Equal(t, st.exhaustedErr() != nil, exhausted, "platform %+v", st)

		k := &APIKey{ID: 1, RateLimit5h: float64(rng.Intn(3) * rng.Intn(6)), RateLimit1d: float64(rng.Intn(6)), RateLimit7d: float64(rng.Intn(2) * 5)}
		u := &apiKeyRateLimitUsage{usage5h: float64(rng.Intn(8)), usage1d: float64(rng.Intn(8)), usage7d: float64(rng.Intn(8))}
		exhausted = false
		for _, h := range u.headroom(k, nil) {
			exhausted = exhausted || h.Remaining <= 0
		}
		require.Equal(t, u.exhaustedErr(k) != nil, exhausted, "rate limits %+v %+v", k, u)
	}
}

type platformQuotaDBStub struct {
	UserPlatformQuotaRepository
	err error
}

func (s *platformQuotaDBStub) GetByUserPlatform(context.Context, int64, string) (*UserPlatformQuotaRecord, error) {
	return nil, s.err
}
