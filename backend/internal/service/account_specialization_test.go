package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func specializedAccountWithModels(id int64, models ...string) Account {
	mapping := make(map[string]any, len(models))
	for _, m := range models {
		mapping[m] = m
	}
	return Account{
		ID:          id,
		Platform:    PlatformAnthropic,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"model_mapping": mapping},
	}
}

func TestAccountModelBreadth(t *testing.T) {
	t.Parallel()

	narrow := specializedAccountWithModels(1, "gpt-5.5")
	wide := specializedAccountWithModels(2, "gpt-5.5", "gpt-5.6")
	unlimited := Account{ID: 3, Platform: PlatformAnthropic}
	wildcard := specializedAccountWithModels(4, "gpt-5.*")

	require.Equal(t, 1, accountModelBreadth(&narrow))
	require.Equal(t, 2, accountModelBreadth(&wide))
	// 空映射 = 放行所有模型，属于最宽。
	require.Equal(t, modelBreadthUnbounded, accountModelBreadth(&unlimited))
	// 通配条目数少但覆盖面无界，不能被当成「最专精」而抢走全部流量。
	require.Equal(t, modelBreadthUnbounded, accountModelBreadth(&wildcard))
	require.Equal(t, modelBreadthUnbounded, accountModelBreadth(nil))
}

func TestKeepMostSpecialized(t *testing.T) {
	t.Parallel()

	identity := func(a *Account) *Account { return a }

	narrow := specializedAccountWithModels(1, "gpt-5.5")
	wide := specializedAccountWithModels(2, "gpt-5.5", "gpt-5.6")
	wider := specializedAccountWithModels(3, "gpt-5.5", "gpt-5.6", "gpt-5.7")

	t.Run("只保留最窄的一档", func(t *testing.T) {
		got := keepMostSpecialized([]*Account{&wide, &narrow, &wider}, identity)
		require.Len(t, got, 1)
		require.Equal(t, int64(1), got[0].ID)
	})

	t.Run("同宽度时原样返回", func(t *testing.T) {
		a := specializedAccountWithModels(10, "gpt-5.5")
		b := specializedAccountWithModels(11, "gpt-5.6")
		got := keepMostSpecialized([]*Account{&a, &b}, identity)
		require.Len(t, got, 2)
	})

	t.Run("全部无限制时不做取舍", func(t *testing.T) {
		a := Account{ID: 20}
		b := Account{ID: 21}
		got := keepMostSpecialized([]*Account{&a, &b}, identity)
		require.Len(t, got, 2)
	})

	t.Run("少于两个直接返回", func(t *testing.T) {
		got := keepMostSpecialized([]*Account{&wide}, identity)
		require.Len(t, got, 1)
	})
}

// 复现问题场景：分组里既有只支持 5.5 的号，也有 5.5/5.6 都支持的号。
// 请求 5.5 时应当用掉专精号，把双能力号留给非它不可的 5.6 请求。
func TestGatewaySchedulingPrefersSpecializedAccount(t *testing.T) {
	groupID := int64(700)
	group := &Group{ID: groupID, Platform: PlatformAnthropic, Status: StatusActive}

	newService := func(prefer bool) *GatewayService {
		onlyFiveFive := specializedAccountWithModels(801, "gpt-5.5")
		bothModels := specializedAccountWithModels(802, "gpt-5.5", "gpt-5.6")
		// 让双能力账号在「最久未使用」这一维度上更占优，确保胜出只可能来自专精度判定。
		onlyFiveFive.LastUsedAt = specializedNowPtr()
		return &GatewayService{
			accountRepo: &anthropicFallbackAccountRepo{
				byGroup: map[int64][]Account{groupID: {onlyFiveFive, bothModels}},
			},
			groupRepo: &anthropicFallbackGroupRepo{groups: map[int64]*Group{groupID: group}},
			cfg: &config.Config{
				RunMode: config.RunModeStandard,
				Gateway: config.GatewayConfig{PreferSpecializedAccounts: prefer},
			},
		}
	}

	t.Run("开启后请求 5.5 派给专精账号", func(t *testing.T) {
		account, err := newService(true).SelectAccountForModelWithExclusions(
			context.Background(), &groupID, "", "gpt-5.5", nil)
		require.NoError(t, err)
		require.NotNil(t, account)
		require.Equal(t, int64(801), account.ID, "应当选中只支持 5.5 的账号")
	})

	t.Run("关闭时维持原有行为：最久未使用胜出", func(t *testing.T) {
		account, err := newService(false).SelectAccountForModelWithExclusions(
			context.Background(), &groupID, "", "gpt-5.5", nil)
		require.NoError(t, err)
		require.NotNil(t, account)
		require.Equal(t, int64(802), account.ID, "关闭时不应受专精度影响")
	})

	t.Run("请求 5.6 时专精账号本就不在候选内", func(t *testing.T) {
		account, err := newService(true).SelectAccountForModelWithExclusions(
			context.Background(), &groupID, "", "gpt-5.6", nil)
		require.NoError(t, err)
		require.NotNil(t, account)
		require.Equal(t, int64(802), account.ID)
	})
}

func specializedNowPtr() *time.Time { now := time.Now(); return &now }
