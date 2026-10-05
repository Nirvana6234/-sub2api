package relayselect

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 从节点的"查不到的 Key"负缓存（设计 8.2）：主节点回过 401 后的 30 秒内，同一个 Key 的请求在从节点本地直接拒绝，
// 不再准入调用主节点；新建这把 Key 时主节点推送它的哈希，清掉这条缓存，Key 马上能用。
func TestNodeNegativeKeyCacheStopsRepeatedLookupsUntilKeyIsCreated(t *testing.T) {
	e := startE2E(t)
	inv := master.NewInvalidator(e.events)
	e.world.sel.deps.APIKeys.SetAuthCacheInvalidationListener(inv.APIKeyHash)
	const responses = `{"model":"gpt-5","input":"hi"}`

	for i := 0; i < 3; i++ {
		status, body := e.post(t, "/v1/responses", "sk-later", responses)
		require.Equal(t, http.StatusUnauthorized, status)
		require.JSONEq(t, `{"code":"INVALID_API_KEY","message":"Invalid API key"}`, body, "the cached rejection is written exactly as the master wrote it")
	}
	require.Equal(t, int64(1), e.admits.Load(), "only the first miss reached the master")

	// Key 在主节点建好了，但还没推送：从节点按负缓存继续拒绝。
	created := *e.world.keys.keys["sk-a"]
	created.Key = "sk-later"
	created.ID += 100
	e.world.keys.keys["sk-later"] = &created
	status, _ := e.post(t, "/v1/responses", "sk-later", responses)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, int64(1), e.admits.Load())

	// 建 Key 时主节点推送哈希：缓存清掉，下一个请求去主节点查到并放行。
	e.world.sel.deps.APIKeys.InvalidateAuthCacheByKey(context.Background(), "sk-later")
	require.Eventually(t, func() bool {
		status, _ := e.post(t, "/v1/responses", "sk-later", responses)
		return status == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond, "the pushed invalidation clears the negative entry")
	e.world.waitReleased(t)

	// 别的 Key 的拒绝互不影响，Google 入口的写法不同、各记各的。
	status, body := e.post(t, "/v1/responses", "sk-other", responses)
	require.Equal(t, http.StatusUnauthorized, status, body)
	for i := 0; i < 2; i++ {
		before := e.admits.Load()
		req, err := http.NewRequest(http.MethodPost, e.gateway.URL+"/v1beta/models/gemini-2.5-pro:generateContent", strings.NewReader(`{"contents":[]}`))
		require.NoError(t, err)
		req.Header.Set("x-goog-api-key", "sk-other")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		out, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(out))
		require.Equal(t, "UNAUTHENTICATED", gjson.GetBytes(out, "error.status").String(), string(out))
		if i == 1 {
			require.Equal(t, before, e.admits.Load(), "the second Google miss is served from the cache")
		}
	}
}

// 无效鉴权防刷（设计 8.2）：同一来源鉴权失败太多后暂时拒绝（连有效 Key 也拒），在从节点本地做，不去主节点；写法与单机一致。
func TestNodeRejectsClientAfterTooManyInvalidAuthentications(t *testing.T) {
	useInvalidAuthAbuse(t, config.InvalidAuthAbuseConfig{Enabled: true, Threshold: 4, WindowSeconds: 60, BlockSeconds: 60, Capacity: 256})
	e := startE2E(t)
	const responses = `{"model":"gpt-5","input":"hi"}`

	// 同一个无效 Key 重复试也算（负缓存命中同样计一次失败）。
	for i := 0; i < 4; i++ {
		status, _ := e.post(t, "/v1/responses", "sk-guess", responses)
		require.Equal(t, http.StatusUnauthorized, status, "attempt %d", i)
	}
	admitsAtBlock := e.admits.Load()

	req, err := http.NewRequest(http.MethodPost, e.gateway.URL+"/v1/responses", strings.NewReader(responses))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer sk-a")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	out, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, string(out))
	require.JSONEq(t, `{"code":"INVALID_AUTH_RATE_LIMITED","message":"Too many invalid authentication attempts; retry later"}`, string(out))
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
	require.Equal(t, admitsAtBlock, e.admits.Load(), "blocked requests never reach the master")

	// Google 入口写 Google 格式。
	greq, err := http.NewRequest(http.MethodPost, e.gateway.URL+"/v1beta/models/gemini-2.5-pro:generateContent", strings.NewReader(`{"contents":[]}`))
	require.NoError(t, err)
	greq.Header.Set("x-goog-api-key", "sk-a")
	gresp, err := http.DefaultClient.Do(greq)
	require.NoError(t, err)
	gout, _ := io.ReadAll(gresp.Body)
	_ = gresp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, gresp.StatusCode, string(gout))
	require.Equal(t, int64(429), gjson.GetBytes(gout, "error.code").Int(), string(gout))
	require.Equal(t, admitsAtBlock, e.admits.Load())
}

// 防刷没有打开（默认配置关闭时）：失败多少次都照常问主节点（查不到的由负缓存挡）。
func TestNodeWithoutInvalidAuthGuardNeverBlocks(t *testing.T) {
	e := startE2E(t)
	for i := 0; i < 30; i++ {
		status, _ := e.post(t, "/v1/responses", "sk-guess", `{"model":"gpt-5","input":"hi"}`)
		require.Equal(t, http.StatusUnauthorized, status)
	}
	status, body := e.post(t, "/v1/responses", "sk-a", `{"model":"gpt-5","input":"hi"}`)
	require.Equal(t, http.StatusOK, status, body)
	e.world.waitReleased(t)
}
