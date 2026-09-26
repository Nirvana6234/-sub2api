package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// 子额度维度，与 relay_quota_leases.dimension 一致（docs/MASTER_RELAY_NODES.md 4.1）。
// 分组的日/周/月上限只在订阅模式下拿订阅用量比，和订阅窗口是同一个维度，不单列。
const (
	QuotaDimBalance             = "balance"
	QuotaDimSubscriptionDaily   = "subscription_daily"
	QuotaDimSubscriptionWeekly  = "subscription_weekly"
	QuotaDimSubscriptionMonthly = "subscription_monthly"
	QuotaDimPlatformDaily       = "platform_daily"
	QuotaDimPlatformWeekly      = "platform_weekly"
	QuotaDimPlatformMonthly     = "platform_monthly"
	QuotaDimAPIKeyTotal         = "api_key_total"
	QuotaDimAPIKey5h            = "api_key_5h"
	QuotaDimAPIKey1d            = "api_key_1d"
	QuotaDimAPIKey7d            = "api_key_7d"
)

// QuotaHeadroom 是一项子额度现在还剩多少（主从分流按它锁给从节点，设计 4.1、4.2）。
type QuotaHeadroom struct {
	Dimension string
	// ScopeID：订阅窗口是分组 ID（与订阅缓存、订阅作废同一个键），Key 维度是 API Key ID，其余为 0。
	ScopeID int64
	// ScopeKey：平台配额是平台名，其余为空。
	ScopeKey string
	// Remaining = 上限 − 用量（窗口已按规则重置）；余额是"余额 − 最低保留额"，
	// 不减锁在从节点上的部分（那部分由额度服务按租约自己减）。可能为负。
	Remaining float64
	// ExpiresAt：这项子额度的有效期上限（订阅、Key 的到期时间），没有为 nil。
	ExpiresAt *time.Time
}

// QuotaRequest 与 CheckBillingEligibility 的入参一致。
type QuotaRequest struct {
	User         *User
	APIKey       *APIKey
	Group        *Group
	Subscription *UserSubscription
	Platform     string
}

// QuotaHeadroom 返回这个请求会用到的每一项有上限的子额度还剩多少。
//
// 与 CheckBillingEligibility 走同一套模式判断、读同一份数据（余额缓存、订阅缓存、平台配额缓存、
// Key 限额缓存）、用同样的窗口重置规则；没有上限的窗口不返回。
// 和资格检查不同的是取不到数据时一律返回错误（资格检查在部分维度上放行）：
// 锁定额度宁可不给，不能多给（开发计划第 4 节"主节点 PG 或 Redis 故障"）。
// RPM、并发不在这里：它们不锁给从节点，选号时在主节点执行。
func (s *BillingCacheService) QuotaHeadroom(ctx context.Context, req QuotaRequest) ([]QuotaHeadroom, error) {
	if s.cfg.RunMode == config.RunModeSimple || req.User == nil {
		return nil, nil
	}
	var out []QuotaHeadroom
	isSubscriptionMode := req.Group != nil && req.Group.IsSubscriptionType() && req.Subscription != nil
	if isSubscriptionMode {
		subData, err := s.GetSubscriptionStatus(ctx, req.User.ID, req.Group.ID)
		if err != nil {
			return nil, ErrBillingServiceUnavailable.WithCause(err)
		}
		if subData.Status != SubscriptionStatusActive || time.Now().After(subData.ExpiresAt) {
			return nil, ErrSubscriptionInvalid
		}
		expires := subData.ExpiresAt
		for _, w := range []struct {
			dim   string
			limit *float64
			usage float64
		}{
			{QuotaDimSubscriptionDaily, req.Group.DailyLimitUSD, subData.DailyUsage},
			{QuotaDimSubscriptionWeekly, req.Group.WeeklyLimitUSD, subData.WeeklyUsage},
			{QuotaDimSubscriptionMonthly, req.Group.MonthlyLimitUSD, subData.MonthlyUsage},
		} {
			if w.limit != nil && *w.limit > 0 {
				out = append(out, QuotaHeadroom{Dimension: w.dim, ScopeID: req.Group.ID, Remaining: *w.limit - w.usage, ExpiresAt: &expires})
			}
		}
	} else {
		balance, err := s.GetUserBalance(ctx, req.User.ID)
		if err != nil {
			return nil, ErrBillingServiceUnavailable.WithCause(err)
		}
		out = append(out, QuotaHeadroom{Dimension: QuotaDimBalance, Remaining: balance - s.minimumBalanceReserve()})

		st, err := s.loadUserPlatformQuotaState(ctx, req.User.ID, req.Platform)
		if err != nil {
			return nil, ErrBillingServiceUnavailable.WithCause(err)
		}
		if st != nil {
			out = append(out, st.headroom(req.Platform)...)
		}
	}

	if k := req.APIKey; k != nil {
		var expires *time.Time
		if k.ExpiresAt != nil {
			e := *k.ExpiresAt
			expires = &e
		}
		if k.Quota > 0 {
			out = append(out, QuotaHeadroom{Dimension: QuotaDimAPIKeyTotal, ScopeID: k.ID, Remaining: k.Quota - k.QuotaUsed, ExpiresAt: expires})
		}
		if k.HasRateLimits() {
			usage, err := s.loadAPIKeyRateLimitUsage(ctx, k)
			if err != nil {
				return nil, ErrBillingServiceUnavailable.WithCause(err)
			}
			if usage == nil {
				usage = &apiKeyRateLimitUsage{}
			}
			out = append(out, usage.headroom(k, expires)...)
		}
	}
	return out, nil
}

func (st *platformQuotaState) headroom(platform string) []QuotaHeadroom {
	var out []QuotaHeadroom
	for _, w := range []struct {
		dim   string
		limit *float64
		usage float64
	}{
		{QuotaDimPlatformDaily, st.dailyLimit, st.daily},
		{QuotaDimPlatformWeekly, st.weeklyLimit, st.weekly},
		{QuotaDimPlatformMonthly, st.monthlyLimit, st.monthly},
	} {
		if w.limit != nil {
			out = append(out, QuotaHeadroom{Dimension: w.dim, ScopeKey: platform, Remaining: *w.limit - w.usage})
		}
	}
	return out
}

func (u *apiKeyRateLimitUsage) headroom(k *APIKey, expires *time.Time) []QuotaHeadroom {
	var out []QuotaHeadroom
	for _, w := range []struct {
		dim   string
		limit float64
		usage float64
	}{
		{QuotaDimAPIKey5h, k.RateLimit5h, u.usage5h},
		{QuotaDimAPIKey1d, k.RateLimit1d, u.usage1d},
		{QuotaDimAPIKey7d, k.RateLimit7d, u.usage7d},
	} {
		if w.limit > 0 {
			out = append(out, QuotaHeadroom{Dimension: w.dim, ScopeID: k.ID, Remaining: w.limit - w.usage, ExpiresAt: expires})
		}
	}
	return out
}
