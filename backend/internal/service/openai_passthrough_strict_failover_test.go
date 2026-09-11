//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failover 场景下所有 attempt 共用同一个 *gin.Context，strict 判定又是账号级的：
// 上一个账号的结论必须在下一个 attempt 开始前被清掉。危险的方向只有一个——
// 换到**非透传**账号，因为那条路径根本不进 forwardOpenAIPassthrough，
// 「判定时顺手覆写」的老办法覆盖不到。复位因此放在 Forward 顶部。

func newStrictFailoverAccount(id int64, extra map[string]any) *Account {
	return &Account{
		ID:          id,
		Name:        "strict-failover",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
		Status:      StatusActive,
		Schedulable: true,
		Extra:       extra,
	}
}

func strictFailoverExtra(strict bool) map[string]any {
	extra := map[string]any{
		"codex_cli_only":     true,
		"openai_passthrough": true,
	}
	if strict {
		extra["openai_passthrough_strict"] = true
	}
	return extra
}

// newStrictFailoverService 用**真实**的身份判定：strict 档位由 EvaluateCodexClientIdentity
// 决定，注入桩只管 codex_cli_only 的执法，两者刻意不共用一个桩。
func newStrictFailoverService(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
}

func newStrictFailoverContext(body []byte) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0 (Windows 10.0.19045; x86_64) unknown")
	// 默认指纹种子里 `header_prefix: x-codex-` 是 Required：光有官方 UA 过不了指纹门。
	// 真 Codex 客户端每轮都会带 x-codex-* 头，这里照实模拟。
	c.Request.Header.Set("x-codex-turn-state", "turn-state-blob")
	return c
}

func opsPassthroughMode(t *testing.T, c *gin.Context) (string, string) {
	t.Helper()
	mode, _ := c.Value(OpsOpenAIPassthroughModeKey).(string)
	reason, _ := c.Value(OpsOpenAIStrictDegradedReasonKey).(string)
	return mode, reason
}

// TestForward_StrictDecisionDoesNotLeakIntoNonPassthroughAttempt 是本阶段唯一有判别力的
// 用例：把两个 attempt 跑在同一个 context 上，第二个换成非透传账号。
//
// 今天可观测的后果落在 ops 记录上——第二次 attempt 的上游行为会被记成 strict。
// 暂存值的残留暂时没有读取方（非透传路径不读），但它和 §1.2 已经拆掉的那类
// 「靠调用顺序成立、没人断言」的依赖是同一种，留着迟早被第三个调用方踩到。
func TestForward_StrictDecisionDoesNotLeakIntoNonPassthroughAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := newStrictFailoverService(upstream)
	c := newStrictFailoverContext(body)

	// attempt 1：strict 账号。
	_, err := svc.Forward(context.Background(), c, newStrictFailoverAccount(9401, strictFailoverExtra(true)), body)
	require.Error(t, err, "上游桩必然报错，这里只关心出站构造之前的状态")

	// 前置断言：attempt 1 必须真的进了 strict，否则下面的"没泄漏"什么都没证明。
	require.True(t, stagedOpenAIStrictPassthrough(c), "attempt 1 应当判定为 strict")
	mode, reason := opsPassthroughMode(t, c)
	require.Equal(t, OpenAIPassthroughModeStrict, mode)
	require.Empty(t, reason)
	require.Equal(t, "zstd", upstream.lastReq.Header.Get("Content-Encoding"),
		"strict 出站应当是 zstd —— 这条同时证明 attempt 1 确实走完了 strict 出站构造")

	// attempt 2：同一个 context，换到既不透传也不 strict 的账号。
	_, err = svc.Forward(context.Background(), c, newStrictFailoverAccount(9402, nil), body)
	require.Error(t, err)

	assert.False(t, stagedOpenAIStrictPassthrough(c),
		"上一个 attempt 的 strict 判定泄漏到了非透传账号")
	mode, reason = opsPassthroughMode(t, c)
	assert.Equal(t, OpenAIPassthroughModeOff, mode,
		"非透传账号的 attempt 被记成了上一个 attempt 的档位")
	assert.Empty(t, reason, "档位复位时降级原因必须一并清掉，否则会挂在一次根本没降级的 attempt 上")
	assert.NotEqual(t, "zstd", upstream.lastReq.Header.Get("Content-Encoding"),
		"非透传出站不得带 strict 的传输编码")
}

// 反方向：非 strict → strict 必须能正常升档，复位不能把后续判定一起压死。
func TestForward_StrictDecisionAppliesAfterNonStrictAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := newStrictFailoverService(upstream)
	c := newStrictFailoverContext(body)

	_, err := svc.Forward(context.Background(), c, newStrictFailoverAccount(9403, strictFailoverExtra(false)), body)
	require.Error(t, err)
	require.False(t, stagedOpenAIStrictPassthrough(c))
	mode, reason := opsPassthroughMode(t, c)
	require.Equal(t, OpenAIPassthroughModeAuthOnly, mode)
	require.Equal(t, OpenAIStrictPassthroughReasonAccountDisabled, reason)

	_, err = svc.Forward(context.Background(), c, newStrictFailoverAccount(9404, strictFailoverExtra(true)), body)
	require.Error(t, err)

	assert.True(t, stagedOpenAIStrictPassthrough(c), "复位不得阻断后续 attempt 升到 strict")
	mode, reason = opsPassthroughMode(t, c)
	assert.Equal(t, OpenAIPassthroughModeStrict, mode)
	assert.Empty(t, reason, "真正进了 strict 就不该留着上一个 attempt 的降级原因")
}

// TestForward_ResetsStrictStateBeforeClientRestrictionRejection 锁死复位的位置：
// 它必须早于 codex_cli_only 的 403 分支，否则被门禁挡掉的那个 attempt 会把上一个
// attempt 的档位一路带进 ops 记录——那条记录里根本没有上游请求。
func TestForward_ResetsStrictStateBeforeClientRestrictionRejection(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5","stream":true,"input":[]}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := newStrictFailoverService(upstream)
	c := newStrictFailoverContext(body)

	_, err := svc.Forward(context.Background(), c, newStrictFailoverAccount(9405, strictFailoverExtra(true)), body)
	require.Error(t, err)
	require.True(t, stagedOpenAIStrictPassthrough(c))

	// 同一个 context 换到一个会被 codex_cli_only 拒掉的账号（注入执法桩）。
	// 注意这只影响 403 执法，不影响身份判定——后者始终走真实评估。
	svc.codexDetector = &stubCodexRestrictionDetector{
		result: CodexClientRestrictionDetectionResult{
			Enabled: true,
			Matched: false,
			Reason:  CodexClientRestrictionReasonNotMatchedUA,
		},
	}
	_, err = svc.Forward(context.Background(), c, newStrictFailoverAccount(9406, strictFailoverExtra(true)), body)
	require.Error(t, err, "门禁不匹配应当 403")

	assert.False(t, stagedOpenAIStrictPassthrough(c))
	mode, reason := opsPassthroughMode(t, c)
	assert.Equal(t, OpenAIPassthroughModeAuthOnly, mode,
		"被门禁拒掉的 attempt 只该记账号自身的档位，不该继承上一个 attempt 的 strict")
	assert.Empty(t, reason)
}

// TestWSBridgeContextNeverStagesStrict 对应 V-14：WS 入站用的是 upgrade 请求自己的
// gin.Context，它永远不流经 Forward（Forward 只服务 HTTP POST /v1/responses），
// 所以 buildUpstreamRequestOpenAIPassthrough 在 WS 路径上读到的永远是「没暂存」。
// 这条用例把那个前提钉成断言：出站字节必须与 auth_only 完全一致。
func TestWSBridgeContextNeverStagesStrict(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 模拟 WS 侧那个从未 stage 过的 context：账号开着 strict，也必须落在 auth_only。
	c := newFingerprintStageTestContext(t)
	for name, value := range strictWireProbeHeaders {
		c.Request.Header.Set(name, value)
	}
	require.False(t, stagedOpenAIStrictPassthrough(c), "WS 的 context 不应带任何 strict 暂存")

	req, err := (&OpenAIGatewayService{}).buildUpstreamRequestOpenAIPassthrough(
		context.Background(), c, newStrictWireAccount(),
		[]byte(`{"model":"gpt-5","input":[],"stream":true}`), "server-token")
	require.NoError(t, err)

	assert.Empty(t, req.Header.Get("Content-Encoding"), "WS 路径不得触发 strict 的 zstd 出站")
	assert.Empty(t, req.Header.Get("x-openai-subagent"), "WS 路径应当仍走白名单，strict 增量不得出现")
	assert.Empty(t, req.Header.Get("x-unknown-noise"))
}

// --- 用户的实际拓扑：apikey 账号 + 不开 codex_cli_only ---

// TestForward_StrictAppliesToAPIKeyAccountWithoutCodexCLIOnly 锁死本变更 2026-09-11
// 调整后的核心语义：strict 是**逐请求**判定、与 codex_cli_only **完全独立**的开关。
//
// 场景取自真实部署：apikey 账号，base_url 指向另一台 sub2api 中继。那台中继自己也在
// 判「流量像不像官方 Codex」，所以字节保真在这条链路上比直连 chatgpt.com 更有价值。
// 账号上没有、也不可能有 codex_cli_only（该开关只对 oauth/setup-token 开放）。
func TestForward_StrictAppliesToAPIKeyAccountWithoutCodexCLIOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := newStrictFailoverService(upstream)
	svc.cfg = rawChatCompletionsTestConfig()

	account := &Account{
		ID:          9501,
		Name:        "gpt-relay",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-relay",
			"base_url": "http://relay.example:8080/",
		},
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"openai_passthrough":        true,
			"openai_passthrough_strict": true,
			// 刻意不写 codex_cli_only——这正是要证明的那一点
		},
	}

	c := newStrictFailoverContext(body)
	_, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err, "上游桩必然报错，这里只关心出站构造")

	assert.True(t, stagedOpenAIStrictPassthrough(c),
		"apikey 账号 + 不开 codex_cli_only，只要请求来自 Codex 就该进 strict")
	mode, reason := opsPassthroughMode(t, c)
	assert.Equal(t, OpenAIPassthroughModeStrict, mode)
	assert.Empty(t, reason)
	require.NotNil(t, upstream.lastReq)
	assert.Equal(t, "zstd", upstream.lastReq.Header.Get("Content-Encoding"))
	assert.Equal(t, "http://relay.example:8080/v1/responses", upstream.lastReq.URL.String(),
		"apikey 走 base_url，不是 chatgpt.com")
}

// 同一个账号、同一条链路，换一个**不是 Codex** 的客户端：必须回落到普通自动透传
// 照常服务，不是 403、也不是 strict。这是用户明确要的行为。
func TestForward_NonCodexClientFallsBackToAuthOnlyPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := newStrictFailoverService(upstream)
	svc.cfg = rawChatCompletionsTestConfig()

	account := &Account{
		ID:          9502,
		Name:        "gpt-relay",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-relay",
			"base_url": "http://relay.example:8080/",
		},
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"openai_passthrough":        true,
			"openai_passthrough_strict": true,
		},
	}

	// 普通 SDK/curl：没有官方 UA，也没有 x-codex-* 指纹。
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "curl/8.0")

	_, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err, "上游桩报错，不是 403——请求本身必须被照常服务")

	assert.False(t, stagedOpenAIStrictPassthrough(c))
	mode, reason := opsPassthroughMode(t, c)
	assert.Equal(t, OpenAIPassthroughModeAuthOnly, mode, "必须回落到普通自动透传")
	assert.Equal(t, OpenAIStrictPassthroughReasonGateNotMatched, reason)
	require.NotNil(t, upstream.lastReq)
	assert.NotEqual(t, "zstd", upstream.lastReq.Header.Get("Content-Encoding"))
}

// 全局黑名单/版本门等策略对「只开 strict、不开 codex_cli_only」的账号同样要生效。
// 取策略的条件一旦漏了 strict，这些配置在最常见的用法上就成了摆设。
func TestEvaluateCodexClientIdentity_HonoursPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := newStrictPassthroughAccount(map[string]any{
		"openai_passthrough":        true,
		"openai_passthrough_strict": true,
	})

	newCtx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0 (Windows 10.0.19045; x86_64) unknown")
		c.Request.Header.Set("x-codex-turn-state", "turn-state-blob")
		return c
	}

	base := CodexRestrictionPolicy{EngineFingerprintSignals: openai.DefaultEngineFingerprintSignals}

	t.Run("基线放行", func(t *testing.T) {
		got := EvaluateCodexClientIdentity(newCtx(), account, base, nil)
		require.True(t, got.Enabled)
		assert.True(t, got.Matched)
	})

	t.Run("黑名单命中即拒", func(t *testing.T) {
		policy := base
		policy.Blacklist = []openai.AllowedClientEntry{{UAContains: []string{"codex_cli_rs/0.98"}}}
		got := EvaluateCodexClientIdentity(newCtx(), account, policy, nil)
		assert.False(t, got.Matched, "黑名单对 strict 路径必须同样生效")
		assert.Equal(t, CodexClientRestrictionReasonBlacklisted, got.Reason)
	})

	t.Run("版本下限拦得住", func(t *testing.T) {
		policy := base
		policy.MinCodexVersion = "99.0.0"
		got := EvaluateCodexClientIdentity(newCtx(), account, policy, nil)
		assert.False(t, got.Matched)
		assert.Equal(t, CodexClientRestrictionReasonVersionTooLow, got.Reason)
	})

	t.Run("指纹门拦得住", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0 (Windows 10.0.19045; x86_64) unknown")
		// 官方 UA 但没有任何 x-codex-* 头
		got := EvaluateCodexClientIdentity(c, account, base, nil)
		assert.False(t, got.Matched)
		assert.Equal(t, CodexClientRestrictionReasonMissingEngineFingerprint, got.Reason)
	})
}

// force_codex_cli 是无条件放行，证明不了来路，因此不得让 strict 生效。
func TestEvaluateCodexClientIdentity_IgnoresForceCodexCLI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := newStrictPassthroughAccount(map[string]any{
		"openai_passthrough":        true,
		"openai_passthrough_strict": true,
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "curl/8.0")

	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{ForceCodexCLI: true}}}
	got := svc.detectCodexClientIdentity(c, account, nil)

	assert.False(t, got.Matched, "force_codex_cli 只是放行，不能当作「这是 Codex」的证据")
}
