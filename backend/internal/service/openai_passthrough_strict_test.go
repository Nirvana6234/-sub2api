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

func newStrictPassthroughContext(restriction *CodexClientRestrictionDetectionResult) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if restriction != nil {
		stageCodexClientRestrictionResult(c, *restriction)
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

	passedGate := &CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: true,
		Reason:  CodexClientRestrictionReasonMatchedUA,
	}

	cases := []struct {
		name        string
		extra       map[string]any
		restriction *CodexClientRestrictionDetectionResult
		wantStrict  bool
		wantReason  string
	}{
		{
			name:        "账号没开 strict",
			extra:       authOnlyExtra,
			restriction: passedGate,
			wantStrict:  false,
			wantReason:  OpenAIStrictPassthroughReasonAccountDisabled,
		},
		{
			name:  "strict 开了但账号没开 codex_cli_only",
			extra: strictExtra,
			restriction: &CodexClientRestrictionDetectionResult{
				Enabled: false,
				Matched: false,
				Reason:  CodexClientRestrictionReasonDisabled,
			},
			wantStrict: false,
			wantReason: OpenAIStrictPassthroughReasonCodexCLIOnlyDisabled,
		},
		{
			name:  "门禁被 force_codex_cli 旁路放行",
			extra: strictExtra,
			restriction: &CodexClientRestrictionDetectionResult{
				Enabled: true,
				Matched: true,
				Reason:  CodexClientRestrictionReasonForceCodexCLI,
			},
			wantStrict: false,
			wantReason: OpenAIStrictPassthroughReasonForceCodexCLIEnabled,
		},
		{
			name:        "三者齐备",
			extra:       strictExtra,
			restriction: passedGate,
			wantStrict:  true,
			wantReason:  "",
		},
	}

	svc := &OpenAIGatewayService{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			strict, reason := svc.resolveOpenAIStrictPassthrough(
				context.Background(),
				newStrictPassthroughContext(tc.restriction),
				newStrictPassthroughAccount(tc.extra),
			)
			assert.Equal(t, tc.wantStrict, strict)
			assert.Equal(t, tc.wantReason, reason)
		})
	}
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

	assert.False(t, strict, "没跑过 codex_cli_only 判定的路径不得进 strict")
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

// 门禁明确判定为「没过」的请求正常会在 Forward 入口 403，走不到 strict 分叉；
// 这条锁死万一走到了也不会进 strict。
func TestResolveOpenAIStrictPassthrough_FailsClosedWhenGateRejected(t *testing.T) {
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

func TestStageCodexClientRestrictionResult_OverwritesAcrossAttempts(t *testing.T) {
	c := newStrictPassthroughContext(&CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: true,
		Reason:  CodexClientRestrictionReasonMatchedUA,
	})

	// failover 换到一个没开 codex_cli_only 的账号：上一账号的结论不得残留。
	stageCodexClientRestrictionResult(c, CodexClientRestrictionDetectionResult{
		Enabled: false,
		Matched: false,
		Reason:  CodexClientRestrictionReasonDisabled,
	})

	got, ok := stagedCodexClientRestrictionResult(c)
	require.True(t, ok)
	assert.False(t, got.Enabled)
	assert.Equal(t, CodexClientRestrictionReasonDisabled, got.Reason)
	assert.Equal(t, OpenAIStrictPassthroughReasonCodexCLIOnlyDisabled,
		openAIStrictPassthroughDegradeReason(c))
}

func TestStagedCodexClientRestrictionResult_WrongTypeIsTreatedAsAbsent(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(codexClientRestrictionResultContextKey, "not-a-result")

	_, ok := stagedCodexClientRestrictionResult(c)
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
