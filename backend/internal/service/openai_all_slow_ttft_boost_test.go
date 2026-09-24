package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 整池都慢时，排序的第一层（健康/慢硬隔离）会塌缩成单一档位，只剩加权评分。
// 生产权重里延迟只有 0.5、优先级 1.0，于是排序被优先级主导——而 puls-兜底组的
// 优先级恰好和速度相反（priority=100 的 212 是 p90 103.7 秒，最快的 220 是 10000），
// 流量长期压在最慢的号上。提权就是为了把这种情况下的主导权还给延迟。
func TestBoostTTFTWeightWhenAllSlow(t *testing.T) {
	t.Run("并非全慢时权重原样不动", func(t *testing.T) {
		require.Equal(t, 0.5, openAIBoostTTFTWeightWhenAllSlow(0.5, 1.0, false),
			"主力池行为必须完全不变")
	})

	t.Run("全慢时延迟权重必须压过优先级", func(t *testing.T) {
		// 生产配置：ttft=0.5, priority=1.0
		got := openAIBoostTTFTWeightWhenAllSlow(0.5, 1.0, true)
		require.Greater(t, got, 1.0,
			"提权后延迟必须真的成为主导，否则等于没做")
		require.Equal(t, 1.5, got)
	})

	t.Run("延迟配得极低时靠优先级下限兜住", func(t *testing.T) {
		// 只按倍数放大会压不过优先级：0.1×3=0.3 < 2.0
		got := openAIBoostTTFTWeightWhenAllSlow(0.1, 2.0, true)
		require.Equal(t, 3.0, got, "应取 priority×1.5 这个下限")
		require.Greater(t, got, 2.0)
	})

	t.Run("延迟权重被关闭时不擅自打开", func(t *testing.T) {
		require.Equal(t, 0.0, openAIBoostTTFTWeightWhenAllSlow(0, 1.0, true),
			"运营显式关掉延迟权重时，不该被这个机制绕过")
	})
}

func TestAllOpenAICandidatesSlow(t *testing.T) {
	setup := func(t *testing.T) *OpenAIGatewayService {
		resetOpenAIAdvancedSchedulerSettingCacheForTest()
		openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
			latencyAwareFallbackEnabled: true, latencyThresholdMs: 30000,
			fallbackSpeedupRatio: 0.6, expiresAt: time.Now().Add(time.Hour).UnixNano(),
		})
		t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
		return &OpenAIGatewayService{}
	}
	cands := func(ids ...int64) []openAIAccountCandidateScore {
		out := make([]openAIAccountCandidateScore, 0, len(ids))
		for _, id := range ids {
			out = append(out, openAIAccountCandidateScore{account: &Account{ID: id}})
		}
		return out
	}

	t.Run("全部超阈值判为全慢", func(t *testing.T) {
		svc := setup(t)
		observeHealthy(svc, 212, 103729) // 生产实测值
		observeHealthy(svc, 220, 39971)
		require.True(t, svc.allOpenAICandidatesSlow(context.Background(), cands(212, 220)))
	})

	t.Run("有一个达标就不算全慢", func(t *testing.T) {
		svc := setup(t)
		observeHealthy(svc, 212, 103729)
		observeHealthy(svc, 183, 7500) // 快号
		require.False(t, svc.allOpenAICandidatesSlow(context.Background(), cands(212, 183)),
			"还有健康账号时第一层仍然有效，不该提权")
	})

	t.Run("有账号没读数时不下结论", func(t *testing.T) {
		svc := setup(t)
		observeHealthy(svc, 212, 103729) // 220 没有任何样本
		require.False(t, svc.allOpenAICandidatesSlow(context.Background(), cands(212, 220)),
			"没观测过的账号不能算作慢，否则新池子会被误判")
	})

	t.Run("延迟感知关闭时不提权", func(t *testing.T) {
		resetOpenAIAdvancedSchedulerSettingCacheForTest()
		openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
			latencyAwareFallbackEnabled: false, latencyThresholdMs: 30000,
			expiresAt: time.Now().Add(time.Hour).UnixNano(),
		})
		t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
		svc := &OpenAIGatewayService{}
		observeHealthy(svc, 212, 103729)
		require.False(t, svc.allOpenAICandidatesSlow(context.Background(), cands(212)))
	})

	t.Run("空候选池不提权", func(t *testing.T) {
		svc := setup(t)
		require.False(t, svc.allOpenAICandidatesSlow(context.Background(), nil))
	})
}
