//go:build unit

package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStrictPassthroughAccount(extra map[string]any) *Account {
	return &Account{
		ID:       7,
		Name:     "strict-test",
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    extra,
	}
}

func newStrictPassthroughContext(identity *CodexClientRestrictionDetectionResult) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if identity != nil {
		stageCodexClientIdentity(c, *identity)
	}
	return c
}

// --- IsOpenAIPassthroughStrictEnabled: 零值与错值一律落在「现状」一侧 ---

func TestIsOpenAIPassthroughStrictEnabled_ZeroValuesAreOff(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]any
	}{
		{"nil extra", nil},
		{"空 extra", map[string]any{}},
		{
			"只开了 strict、没开透传——叠加语义要求它无效",
			map[string]any{"openai_passthrough_strict": true},
		},
		{
			"strict 值是字符串 true",
			map[string]any{"openai_passthrough": true, "openai_passthrough_strict": "true"},
		},
		{
			"strict 值是数字 1",
			map[string]any{"openai_passthrough": true, "openai_passthrough_strict": 1},
		},
		{
			"strict 值是 nil",
			map[string]any{"openai_passthrough": true, "openai_passthrough_strict": nil},
		},
		{
			"strict 显式 false",
			map[string]any{"openai_passthrough": true, "openai_passthrough_strict": false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.False(t, newStrictPassthroughAccount(tc.extra).IsOpenAIPassthroughStrictEnabled())
		})
	}
}

func TestIsOpenAIPassthroughStrictEnabled_NilAccount(t *testing.T) {
	var account *Account
	assert.False(t, account.IsOpenAIPassthroughStrictEnabled())
}

func TestIsOpenAIPassthroughStrictEnabled_OnlyWhenBothBoolsSet(t *testing.T) {
	account := newStrictPassthroughAccount(map[string]any{
		"openai_passthrough":        true,
		"openai_passthrough_strict": true,
	})
	assert.True(t, account.IsOpenAIPassthroughStrictEnabled())
}

func TestIsOpenAIPassthroughStrictEnabled_LegacyPassthroughKeyCounts(t *testing.T) {
	// 历史 OAuth 开关 openai_oauth_passthrough 同样满足叠加前提。
	account := newStrictPassthroughAccount(map[string]any{
		"openai_oauth_passthrough":  true,
		"openai_passthrough_strict": true,
	})
	assert.True(t, account.IsOpenAIPassthroughStrictEnabled())
}

func TestIsOpenAIPassthroughStrictEnabled_NonOpenAIPlatform(t *testing.T) {
	account := newStrictPassthroughAccount(map[string]any{
		"openai_passthrough":        true,
		"openai_passthrough_strict": true,
	})
	account.Platform = PlatformAnthropic
	assert.False(t, account.IsOpenAIPassthroughStrictEnabled(),
		"非 OpenAI 平台连透传前提都不成立")
}

// --- resolveOpenAIStrictPassthrough: 四种组合 ---

func TestResolveOpenAIStrictPassthrough_Combinations(t *testing.T) {
	strictExtra := map[string]any{
		"openai_passthrough":        true,
		"openai_passthrough_strict": true,
	}
	authOnlyExtra := map[string]any{"openai_passthrough": true}

	isCodex := &CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: true,
		Reason:  CodexClientRestrictionReasonMatchedUA,
	}
	notCodex := &CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: false,
		Reason:  CodexClientRestrictionReasonNotMatchedUA,
	}

	cases := []struct {
		name       string
		extra      map[string]any
		identity   *CodexClientRestrictionDetectionResult
		wantStrict bool
		wantReason string
	}{
		{
			name:       "账号没开 strict",
			extra:      authOnlyExtra,
			identity:   isCodex,
			wantStrict: false,
			wantReason: OpenAIStrictPassthroughReasonAccountDisabled,
		},
		{
			name:       "开了 strict 但这条请求不是 Codex 发的",
			extra:      strictExtra,
			identity:   notCodex,
			wantStrict: false,
			wantReason: OpenAIStrictPassthroughReasonGateNotMatched,
		},
		{
			name:       "开了 strict 且这条请求是 Codex 发的",
			extra:      strictExtra,
			identity:   isCodex,
			wantStrict: true,
			wantReason: "",
		},
	}

	svc := &OpenAIGatewayService{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			strict, reason := svc.resolveOpenAIStrictPassthrough(
				context.Background(),
				newStrictPassthroughContext(tc.identity),
				newStrictPassthroughAccount(tc.extra),
			)
			assert.Equal(t, tc.wantStrict, strict)
			assert.Equal(t, tc.wantReason, reason)
		})
	}
}

// strict 与 codex_cli_only 是两个独立开关，这是本设计的要害：codex_cli_only 的语义是
// 「不是 Codex 就 403」，strict 的语义是「是 Codex 就多给保真度，不是就按老样子发」。
// 账号完全不开 codex_cli_only 时，strict 照样必须能生效。
func TestResolveOpenAIStrictPassthrough_DoesNotRequireCodexCLIOnly(t *testing.T) {
	svc := &OpenAIGatewayService{}
	strict, reason := svc.resolveOpenAIStrictPassthrough(
		context.Background(),
		newStrictPassthroughContext(&CodexClientRestrictionDetectionResult{
			Enabled: true,
			Matched: true,
			Reason:  CodexClientRestrictionReasonMatchedUA,
		}),
		newStrictPassthroughAccount(map[string]any{
			"openai_passthrough":        true,
			"openai_passthrough_strict": true,
			// 刻意不写 codex_cli_only
		}),
	)

	assert.True(t, strict, "strict 不得依赖 codex_cli_only —— 那个开关会 403，这个只降级")
	assert.Empty(t, reason)
}

// --- fail-closed：门禁判定缺席时绝不能进 strict ---

func TestResolveOpenAIStrictPassthrough_FailsClosedWithoutStagedGate(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := newStrictPassthroughAccount(map[string]any{
		"openai_passthrough":        true,
		"openai_passthrough_strict": true,
	})

	strict, reason := svc.resolveOpenAIStrictPassthrough(
		context.Background(),
		newStrictPassthroughContext(nil), // 没有 stage 过判定
		account,
	)

	assert.False(t, strict, "没跑过身份判定的路径不得进 strict")
	assert.Equal(t, OpenAIStrictPassthroughReasonGateNotEvaluated, reason)
}

func TestResolveOpenAIStrictPassthrough_FailsClosedWithNilContext(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := newStrictPassthroughAccount(map[string]any{
		"openai_passthrough":        true,
		"openai_passthrough_strict": true,
	})

	strict, reason := svc.resolveOpenAIStrictPassthrough(context.Background(), nil, account)

	assert.False(t, strict)
	assert.Equal(t, OpenAIStrictPassthroughReasonGateNotEvaluated, reason)
}

// 判定为「不是 Codex」是设计内的正常路径（Chat 前端、curl、各类 SDK 都会落在这里），
// 请求照常按普通透传服务，只是不进 strict。
func TestResolveOpenAIStrictPassthrough_FallsBackWhenClientIsNotCodex(t *testing.T) {
	svc := &OpenAIGatewayService{}
	strict, reason := svc.resolveOpenAIStrictPassthrough(
		context.Background(),
		newStrictPassthroughContext(&CodexClientRestrictionDetectionResult{
			Enabled: true,
			Matched: false,
			Reason:  CodexClientRestrictionReasonNotMatchedUA,
		}),
		newStrictPassthroughAccount(map[string]any{
			"openai_passthrough":        true,
			"openai_passthrough_strict": true,
		}),
	)

	assert.False(t, strict)
	assert.Equal(t, OpenAIStrictPassthroughReasonGateNotMatched, reason)
}

// --- 暂存与覆写 ---

func TestStageCodexClientIdentity_OverwritesAcrossAttempts(t *testing.T) {
	c := newStrictPassthroughContext(&CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: true,
		Reason:  CodexClientRestrictionReasonMatchedUA,
	})

	// failover 换到一个没开 strict 的账号：Forward 会暂存零值，上一账号的结论不得残留。
	stageCodexClientIdentity(c, CodexClientRestrictionDetectionResult{})

	got, ok := stagedCodexClientIdentity(c)
	require.True(t, ok)
	assert.False(t, got.Enabled)
	assert.Equal(t, OpenAIStrictPassthroughReasonGateNotEvaluated,
		openAIStrictPassthroughDegradeReason(c))
}

func TestStagedCodexClientIdentity_WrongTypeIsTreatedAsAbsent(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(codexClientIdentityContextKey, "not-a-result")

	_, ok := stagedCodexClientIdentity(c)
	assert.False(t, ok)
	assert.Equal(t, OpenAIStrictPassthroughReasonGateNotEvaluated,
		openAIStrictPassthroughDegradeReason(c))
}

// --- ops 档位 ---

func TestOpenAIPassthroughModeForOps(t *testing.T) {
	assert.Equal(t, OpenAIPassthroughModeStrict, OpenAIPassthroughModeForOps(true, true))
	assert.Equal(t, OpenAIPassthroughModeAuthOnly, OpenAIPassthroughModeForOps(false, true))
	assert.Equal(t, OpenAIPassthroughModeOff, OpenAIPassthroughModeForOps(false, false))
}
