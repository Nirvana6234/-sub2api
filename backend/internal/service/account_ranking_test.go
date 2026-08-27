package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func rankAcc(id int64, priority int, lastUsed *time.Time, typ string, platform string) *Account {
	return &Account{ID: id, Priority: priority, LastUsedAt: lastUsed, Type: typ, Platform: platform}
}

func TestAccountRankBetter(t *testing.T) {
	t.Parallel()

	early := time.Now().Add(-time.Hour)
	late := time.Now()

	t.Run("优先级数字小的胜出", func(t *testing.T) {
		a := rankAcc(1, 1, &late, AccountTypeAPIKey, PlatformAnthropic)
		b := rankAcc(2, 5, &early, AccountTypeAPIKey, PlatformAnthropic)
		require.True(t, accountRankBetter(a, b, accountRankPolicy{}))
	})

	t.Run("从未使用的优先于用过的", func(t *testing.T) {
		a := rankAcc(1, 1, nil, AccountTypeAPIKey, PlatformAnthropic)
		b := rankAcc(2, 1, &early, AccountTypeAPIKey, PlatformAnthropic)
		require.True(t, accountRankBetter(a, b, accountRankPolicy{}))
	})

	t.Run("都用过时最久未用的胜出", func(t *testing.T) {
		a := rankAcc(1, 1, &early, AccountTypeAPIKey, PlatformAnthropic)
		b := rankAcc(2, 1, &late, AccountTypeAPIKey, PlatformAnthropic)
		require.True(t, accountRankBetter(a, b, accountRankPolicy{}))
	})

	t.Run("使用时间持平时偏好 OAuth", func(t *testing.T) {
		same := late
		a := rankAcc(1, 1, &same, AccountTypeOAuth, PlatformAnthropic)
		b := rankAcc(2, 1, &same, AccountTypeAPIKey, PlatformAnthropic)
		require.True(t, accountRankBetter(a, b, accountRankPolicy{preferOAuth: true}))
		require.False(t, accountRankBetter(a, b, accountRankPolicy{}), "未开启时不应偏好")
	})

	t.Run("混合调度：跨平台不比较账号类型", func(t *testing.T) {
		same := late
		p := accountRankPolicy{preferOAuth: true, oauthPreferenceGeminiOnly: true}
		// 两侧都是 Gemini 才比
		a := rankAcc(1, 1, &same, AccountTypeOAuth, PlatformGemini)
		b := rankAcc(2, 1, &same, AccountTypeAPIKey, PlatformGemini)
		require.True(t, accountRankBetter(a, b, p))
		// 一侧是 antigravity 时不比，避免跨平台按类型分优劣
		c := rankAcc(3, 1, &same, AccountTypeOAuth, PlatformAntigravity)
		require.False(t, accountRankBetter(c, b, p))
	})

	t.Run("专精度排在最久未用之前", func(t *testing.T) {
		narrow := specializedAccountWithModels(1, "gpt-5.5")
		wide := specializedAccountWithModels(2, "gpt-5.5", "gpt-5.6")
		// 让专精号在「最久未用」上处于劣势，确认它仍然胜出
		narrow.LastUsedAt = &late
		wide.LastUsedAt = &early
		require.True(t, accountRankBetter(&narrow, &wide, accountRankPolicy{preferSpecialized: true}))
		require.False(t, accountRankBetter(&narrow, &wide, accountRankPolicy{}),
			"未开启专精保护时应回到最久未用口径")
	})

	t.Run("ignoreLastUsed 跳过使用时间维度", func(t *testing.T) {
		a := rankAcc(1, 1, &late, AccountTypeAPIKey, PlatformAnthropic)
		b := rankAcc(2, 1, &early, AccountTypeAPIKey, PlatformAnthropic)
		require.False(t, accountRankBetter(b, a, accountRankPolicy{ignoreLastUsed: true}))
		require.True(t, accountRankTied(a, b, accountRankPolicy{ignoreLastUsed: true}))
	})

	t.Run("nil 处理", func(t *testing.T) {
		a := rankAcc(1, 1, nil, AccountTypeAPIKey, PlatformAnthropic)
		require.True(t, accountRankBetter(a, nil, accountRankPolicy{}))
		require.False(t, accountRankBetter(nil, a, accountRankPolicy{}))
	})
}
