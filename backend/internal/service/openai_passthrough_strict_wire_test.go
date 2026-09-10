//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStrictWireAccount() *Account {
	return newTestOAuthAccount(9001, map[string]any{
		"openai_passthrough":        true,
		"openai_passthrough_strict": true,
	})
}

// --- 请求头：黑名单 vs 白名单 ---

func TestStrictPassthroughForwardableHeader_ForwardsOfficialCodexHeaders(t *testing.T) {
	forwarded := []string{
		"x-openai-subagent",
		"x-responsesapi-include-timing-metrics",
		"x-codex-parent-thread-id",
		"session-id", // 连字符形式；白名单里只有下划线的 session_id
		"thread-id",
		"x-client-request-id",
		"x-codex-turn-state",
		"x-codex-turn-metadata",
		"x-codex-window-id",
		"x-codex-beta-features",
		"x-codex-installation-id",
		"accept",
		"content-type",
		"openai-beta",
		"accept-language",
	}
	for _, name := range forwarded {
		t.Run(name, func(t *testing.T) {
			assert.True(t, isOpenAIStrictPassthroughForwardableHeader(name, false),
				"strict 应转发官方客户端会发的 %s", name)
		})
	}
}

func TestStrictPassthroughForwardableHeader_BlocksManagedHeaders(t *testing.T) {
	blocked := []string{
		// 凭据与账号身份：客户端自报的一律不可信
		"authorization", "cookie", "cookie2", "x-api-key", "x-goog-api-key",
		"x-openai-actor-authorization", "chatgpt-account-id", "openai-organization",
		"x-oai-attestation", "x-oai-is", "x-oai-is-update",
		"x-openai-internal-codex-residency",
		// 出站身份与传输：由画像/压缩逻辑统一生成
		"user-agent", "originator", "version", "host", "content-length", "content-encoding",
		// 逐跳与来源标识
		"connection", "transfer-encoding", "upgrade", "te", "trailer",
		"x-forwarded-for", "x-real-ip", "cf-connecting-ip", "x-request-id",
	}
	for _, name := range blocked {
		t.Run(name, func(t *testing.T) {
			assert.False(t, isOpenAIStrictPassthroughForwardableHeader(name, true),
				"strict 不得把 %s 交给上游", name)
		})
	}
}

// x-codex-installation-id 是本仓库与 codex-proxy-rs 的一处**刻意分歧**。
// 那边总是按租约生成一个所以挡掉客户端值；本仓库只在指纹收敛开启时生成，
// 收敛默认关——挡掉它会让上游看到一个没有安装标识的请求，比放行更不像官方。
func TestStrictPassthroughForwardableHeader_KeepsInstallationID(t *testing.T) {
	assert.True(t, isOpenAIStrictPassthroughForwardableHeader("x-codex-installation-id", false),
		"指纹收敛关闭时挡掉安装标识会让请求更不像官方客户端；收敛开启时它会被覆写")
}

func TestStrictPassthroughForwardableHeader_TimeoutHeadersFollowConfig(t *testing.T) {
	// 超时类头是运维策略，与"像不像官方"无关，strict 不该顺手改这条配置门的语义。
	assert.False(t, isOpenAIStrictPassthroughForwardableHeader("x-stainless-timeout", false))
	assert.True(t, isOpenAIStrictPassthroughForwardableHeader("x-stainless-timeout", true))
}

func TestStrictPassthroughForwardableHeader_EmptyKey(t *testing.T) {
	assert.False(t, isOpenAIStrictPassthroughForwardableHeader("", true))
}

// --- 出站请求构造：端到端 ---

// strictWireProbeHeaders 是一份覆盖各类的客户端入站头，用于量出两种模式的出站差集。
var strictWireProbeHeaders = map[string]string{
	// 官方 Codex 会发、白名单没收录的
	"x-openai-subagent":                     "thread_spawn",
	"x-responsesapi-include-timing-metrics": "true",
	"x-codex-parent-thread-id":              "client-parent",
	"session-id":                            "client-session-hyphen",
	"thread-id":                             "client-thread",
	"x-client-request-id":                   "client-request",
	// 官方会发、白名单已收录的
	"x-codex-turn-state":      "client-turn-state",
	"x-codex-window-id":       "client-window",
	"x-codex-installation-id": "client-install",
	"x-codex-beta-features":   "remote_compaction_v2",
	"accept":                  "text/event-stream",
	"accept-language":         "en-US",
	"openai-beta":             "responses=v1",
	// 网关自有，客户端值必须被无视
	"x-codex-routing-hint": "model=client;tier=client",
	// 伪造的凭据与来源标识
	"authorization":      "Bearer client-forged",
	"cookie":             "client=forged",
	"chatgpt-account-id": "client-forged-account",
	"x-oai-attestation":  "client-forged-attestation",
	"x-forwarded-for":    "203.0.113.9",
	// 非标准噪声头
	"x-unknown-noise": "noise",
}

func buildStrictWireRequest(t *testing.T, strict bool, accountID int64) *http.Request {
	t.Helper()
	extra := map[string]any{"openai_passthrough": true}
	if strict {
		extra["openai_passthrough_strict"] = true
	}
	c := newFingerprintStageTestContext(t)
	for name, value := range strictWireProbeHeaders {
		c.Request.Header.Set(name, value)
	}
	stageOpenAIStrictPassthrough(c, strict)

	req, err := (&OpenAIGatewayService{}).buildUpstreamRequestOpenAIPassthrough(
		context.Background(), c, newTestOAuthAccount(accountID, extra),
		[]byte(`{"model":"gpt-5","input":[],"stream":true}`), "server-token")
	require.NoError(t, err)
	return req
}

// TestBuildUpstreamRequestOpenAIPassthrough_StrictHeaderDelta 锁死 strict 在请求头上
// 的**全部**增量。
//
// 用差集而不是逐条断言"strict 应转发 X"：后者容易写成对设计文档的复述，而设计初稿
// 在这里就猜错过两条——`x-codex-routing-hint` 其实是网关自铸的（setOpenAICodexRoutingHint
// 会先删掉客户端每一种拼写再合成），`x-codex-installation-id` 今天两种模式都已转发。
// 差集断言会因为任何一侧多出或少掉一条而变红，包括我们没预料到的那条。
func TestBuildUpstreamRequestOpenAIPassthrough_StrictHeaderDelta(t *testing.T) {
	authOnly := buildStrictWireRequest(t, false, 9101).Header
	strict := buildStrictWireRequest(t, true, 9102).Header

	// strict 独有的头即全部增量。
	onlyInStrict := map[string]string{}
	for name := range strict {
		if authOnly.Get(name) == "" && strict.Get(name) != "" {
			onlyInStrict[strings.ToLower(name)] = strict.Get(name)
		}
	}

	assert.Equal(t, map[string]string{
		"session-id":                            "client-session-hyphen",
		"thread-id":                             "client-thread",
		"x-client-request-id":                   "client-request",
		"x-codex-parent-thread-id":              "client-parent",
		"x-openai-subagent":                     "thread_spawn",
		"x-responsesapi-include-timing-metrics": "true",
		// 未知头也会透过去：这是黑名单的直接后果，也是它成立的前提——门禁保证
		// 客户端是官方 Codex，"非标准噪声头"这个风险来源在 strict 下不存在。
		"x-unknown-noise": "noise",
	}, onlyInStrict, "strict 的请求头增量与实测不符：多出或少掉的那条要先解释再改断言")

	// 反向：strict 不得丢掉任何 auth_only 已经在发的头。
	for name := range authOnly {
		assert.NotEmpty(t, strict.Get(name),
			"strict 丢掉了 auth_only 会发的 %s —— strict 只该多发，不该少发", name)
	}
}

func TestBuildUpstreamRequestOpenAIPassthrough_BothModesBlockForgedCredentials(t *testing.T) {
	for _, strict := range []bool{false, true} {
		name := "auth_only"
		accountID := int64(9201)
		if strict {
			name, accountID = "strict", 9202
		}
		t.Run(name, func(t *testing.T) {
			req := buildStrictWireRequest(t, strict, accountID)

			assert.Equal(t, "Bearer server-token", req.Header.Get("authorization"),
				"上游认证必须来自服务端账号，不是客户端自报")
			assert.Empty(t, req.Header.Get("cookie"))
			assert.NotEqual(t, "client-forged-account", req.Header.Get("chatgpt-account-id"))
			assert.Empty(t, req.Header.Get("x-oai-attestation"))
			assert.Empty(t, req.Header.Get("x-forwarded-for"))
		})
	}
}

// x-codex-routing-hint 是网关自有头：setOpenAICodexRoutingHint 会先删光客户端的每一种
// 拼写再按最终上游模型合成。两种模式都不得让客户端值透过去。
func TestBuildUpstreamRequestOpenAIPassthrough_RoutingHintIsGatewayOwned(t *testing.T) {
	for _, strict := range []bool{false, true} {
		req := buildStrictWireRequest(t, strict, 9301+int64(boolToInt(strict)))
		hint := req.Header.Get("x-codex-routing-hint")
		assert.NotContains(t, hint, "tier=client", "客户端提供的 routing hint 不得出站")
		assert.Contains(t, hint, "model=gpt-5", "网关应按最终上游模型合成 routing hint")
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// --- 出站 zstd ---

func TestCompressOpenAIStrictPassthroughBody_RoundTrips(t *testing.T) {
	fixture := loadCodexResponsesFixture(t)

	compressed, ok := compressOpenAIStrictPassthroughBody(fixture.Body)
	require.True(t, ok, "真实 Codex 报文体量下 zstd 必须压得动")
	assert.Less(t, len(compressed), len(fixture.Body))

	decoder, err := zstd.NewReader(nil)
	require.NoError(t, err)
	defer decoder.Close()

	plain, err := decoder.DecodeAll(compressed, nil)
	require.NoError(t, err)
	assert.Equal(t, []byte(fixture.Body), plain,
		"解压结果必须与压缩前逐字节相同——压缩是传输层形态，不得改变语义")
}

func TestCompressOpenAIStrictPassthroughBody_SkipsWhenNotSmaller(t *testing.T) {
	// 小请求体加上 zstd 帧头反而更大。官方客户端自己也是按需压，
	// 强行压缩既没收益又多一层解释成本。
	body, ok := compressOpenAIStrictPassthroughBody([]byte(`{}`))
	assert.False(t, ok)
	assert.Equal(t, []byte(`{}`), body)
}

func TestCompressOpenAIStrictPassthroughBody_EmptyBody(t *testing.T) {
	body, ok := compressOpenAIStrictPassthroughBody(nil)
	assert.False(t, ok)
	assert.Nil(t, body)
}

func TestBuildUpstreamRequestOpenAIPassthrough_StrictCompressesBody(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := newStrictWireAccount()
	fixture := loadCodexResponsesFixture(t)

	c := newFingerprintStageTestContext(t)
	stageOpenAIStrictPassthrough(c, true)

	req, err := svc.buildUpstreamRequestOpenAIPassthrough(
		context.Background(), c, account, fixture.Body, "server-token")
	require.NoError(t, err)

	require.Equal(t, "zstd", req.Header.Get("content-encoding"))
	assert.Less(t, req.ContentLength, int64(len(fixture.Body)),
		"Content-Length 必须是压缩后的长度")

	sent, err := readAllUpstreamRequestBody(t, req)
	require.NoError(t, err)

	decoder, err := zstd.NewReader(nil)
	require.NoError(t, err)
	defer decoder.Close()
	plain, err := decoder.DecodeAll(sent, nil)
	require.NoError(t, err)
	assert.Equal(t, []byte(fixture.Body), plain)
}

func TestBuildUpstreamRequestOpenAIPassthrough_AuthOnlySendsPlaintext(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := newTestOAuthAccount(9003, map[string]any{"openai_passthrough": true})
	fixture := loadCodexResponsesFixture(t)

	c := newFingerprintStageTestContext(t)

	req, err := svc.buildUpstreamRequestOpenAIPassthrough(
		context.Background(), c, account, fixture.Body, "server-token")
	require.NoError(t, err)

	assert.Empty(t, req.Header.Get("content-encoding"), "非 strict 出站仍是明文")
	sent, err := readAllUpstreamRequestBody(t, req)
	require.NoError(t, err)
	assert.Equal(t, []byte(fixture.Body), sent)
}

func readAllUpstreamRequestBody(t *testing.T, req *http.Request) ([]byte, error) {
	t.Helper()
	require.NotNil(t, req.Body)
	defer func() { _ = req.Body.Close() }()
	recorder := httptest.NewRecorder()
	_, err := recorder.Body.ReadFrom(req.Body)
	return recorder.Body.Bytes(), err
}
