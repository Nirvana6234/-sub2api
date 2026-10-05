package relayselect

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodegw"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// videoTaskCache 是主节点上的视频任务状态（会话绑定、待计费快照、认领）：测试里的主节点网关用它。
type videoTaskCache struct {
	nodegw.NoopGatewayCache
	mu      sync.Mutex
	bound   map[string]int64
	pending map[string][]byte
}

func newVideoTaskCache() *videoTaskCache {
	return &videoTaskCache{bound: map[string]int64{}, pending: map[string][]byte{}}
}

func (c *videoTaskCache) SetSessionAccountID(_ context.Context, _ int64, key string, id int64, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bound[key] = id
	return nil
}

func (c *videoTaskCache) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.bound[key]; ok {
		return id, nil
	}
	return 0, service.ErrStickySessionNotFound
}

func (c *videoTaskCache) SetGrokVideoPendingBilling(_ context.Context, key string, payload []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[key] = payload
	return nil
}

func (c *videoTaskCache) GetGrokVideoPendingBilling(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending[key], nil
}

func grokMediaAccount(id int64) service.Account {
	return service.Account{ID: id, Name: "grok", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 2,
		Credentials:   map[string]any{"api_key": "SECRET-grok"},
		AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: 41}}}
}

func (e *e2e) get(t *testing.T, path, key string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.gateway.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(out)
}

func (l *localOpenAI) get(t *testing.T, path, key string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, l.server.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+key)
	return doRequest(t, req)
}

func (e *e2e) lastRecord(t *testing.T, n int) *relayv1.UsageRecord {
	t.Helper()
	require.Eventually(t, func() bool { return len(e.settler.records()) == n }, 5*time.Second, 20*time.Millisecond)
	return e.settler.records()[n-1]
}

// Grok 媒体入口经从节点：图片按次入账（OpenAI 记录种类）；视频创建登记任务、轮询第一次看到完成的视频时报计费，任务状态在主节点；
// 查不到任务回 404；没有生成资格的账号回"没有可用的媒体账号"；响应与单机一致。
func TestNodeServesGrokMediaLikeASingleServer(t *testing.T) {
	useGatewayCache(t, newVideoTaskCache())
	account := grokMediaAccount(1)
	accounts := []service.Account{account}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		return accounts
	})
	e.world.keys.keys["sk-grokgroup"].Group.AllowImageGeneration = true
	local := startLocalOpenAI(t, e, accounts, func(g *gin.RouterGroup, h *handler.OpenAIGatewayHandler) {
		g.POST("/images/generations", h.GrokImages)
		g.POST("/videos/generations", h.GrokVideoGeneration)
		g.GET("/videos/:request_id", h.GrokVideoStatus)
	})

	// 图片：同样的响应，一条 OpenAI 记录，渠道用量字段按媒体入口的口径。
	const image = `{"model":"grok-imagine-image","prompt":"a cat"}`
	nodeStatus, nodeBody := e.post(t, "/v1/images/generations", "sk-grokgroup", image)
	e.world.waitReleased(t)
	localStatus, localBody := local.post(t, "/v1/images/generations", "sk-grokgroup", image)
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, localBody, nodeBody)
	rec := e.lastRecord(t, 1)
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI, rec.GetKind())
	v := mustVoucher(t, e, rec)
	require.Equal(t, int64(41), v.GetGroupId())
	require.True(t, v.GetContext().GetMediaChannelUsageFields())

	// 视频创建：同样的响应；记录是任务登记（带任务 ID 和创建时的快照），不扣费。
	const video = `{"model":"grok-imagine-video","prompt":"a cat","duration":6,"resolution":"720p"}`
	nodeStatus, nodeBody = e.post(t, "/v1/videos/generations", "sk-grokgroup", video)
	e.world.waitReleased(t)
	localStatus, localBody = local.post(t, "/v1/videos/generations", "sk-grokgroup", video)
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, localBody, nodeBody)
	rec = e.lastRecord(t, 2)
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI_VIDEO_TASK, rec.GetKind())
	require.Equal(t, "vid_e2e1", rec.GetTaskId())
	require.Equal(t, "grok-imagine-video", gjson.GetBytes(rec.GetTaskPendingJson(), "model").String())
	require.Equal(t, "720p", gjson.GetBytes(rec.GetTaskPendingJson(), "video_resolution").String())

	// 任务还没登记到主节点（记录还没入账）：查不到，与单机没有绑定时一样 404。
	nodeStatus, nodeBody = e.get(t, "/v1/videos/vid_e2e1", "sk-grokgroup")
	e.world.waitReleased(t)
	require.Equal(t, http.StatusNotFound, nodeStatus, nodeBody)
	require.Contains(t, nodeBody, "Video request not found")

	// 主节点执行登记（入账的单元测试在 relaysettle）：之后轮询落在从节点，按任务绑定的账号选号。
	gw := e.world.sel.deps.Gateway
	gid := int64(41)
	require.NoError(t, gw.BindGrokMediaVideoRequestAccount(context.Background(), &gid, "vid_e2e1", 3, 16, 1))
	nodeStatus, nodeBody = e.get(t, "/v1/videos/vid_e2e1", "sk-grokgroup")
	e.world.waitReleased(t)
	localStatus, localBody = local.get(t, "/v1/videos/vid_e2e1", "sk-grokgroup")
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, gjson.Get(localBody, "status").Raw, gjson.Get(nodeBody, "status").Raw)
	require.Equal(t, gjson.Get(localBody, "video.duration").Raw, gjson.Get(nodeBody, "video.duration").Raw)
	rec = e.lastRecord(t, 3)
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI_VIDEO_COMPLETION, rec.GetKind())
	require.Equal(t, "vid_e2e1", rec.GetTaskId())
	require.Equal(t, gjson.Get(string(rec.GetResultJson()), "VideoCount").Int(), int64(1))

	// 没有生成资格的账号：没有可用的媒体账号（两边一致）。
	accounts[0].Extra = map[string]any{service.GrokMediaEligibleExtraKey: false}
	nodeStatus, nodeBody = e.post(t, "/v1/videos/generations", "sk-grokgroup", video)
	e.world.waitReleased(t)
	localStatus, localBody = local.post(t, "/v1/videos/generations", "sk-grokgroup", video)
	require.Equal(t, http.StatusServiceUnavailable, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, localBody, nodeBody)
	require.Contains(t, nodeBody, "grok_media_no_eligible_account")

	// 没有账号：错误一致。
	empty := startStandardE2E(t, func(string) []service.Account { return nil })
	empty.world.keys.keys["sk-grokgroup"].Group.AllowImageGeneration = true
	emptyLocal := startLocalOpenAI(t, empty, nil, func(g *gin.RouterGroup, h *handler.OpenAIGatewayHandler) {
		g.POST("/videos/generations", h.GrokVideoGeneration)
	})
	nodeStatus, nodeBody = empty.post(t, "/v1/videos/generations", "sk-grokgroup", video)
	empty.world.waitReleased(t)
	localStatus, localBody = emptyLocal.post(t, "/v1/videos/generations", "sk-grokgroup", video)
	require.Equal(t, localStatus, nodeStatus)
	require.Equal(t, localBody, nodeBody)
}

// Seedance 任务入口（OpenAI 分组、带 seedance 能力的 API Key 账号）经从节点：创建登记任务、状态轮询第一次看到成功时报计费。
func TestNodeServesSeedanceLikeASingleServer(t *testing.T) {
	useGatewayCache(t, newVideoTaskCache())
	account := apiKeyAccount(1, "seed")
	account.Credentials["openai_capabilities"] = []any{"seedance"}
	account.AccountGroups = []service.AccountGroup{{AccountID: 1, GroupID: 5}}
	accounts := []service.Account{account}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		return accounts
	})
	e.world.keys.keys["sk-a"].Group.AllowImageGeneration = true
	local := startLocalOpenAI(t, e, accounts, func(g *gin.RouterGroup, h *handler.OpenAIGatewayHandler) {
		g.POST("/contents/generations/tasks", h.SeedanceTasks)
		g.GET("/contents/generations/tasks/:task_id", h.SeedanceTasks)
	})
	const create = `{"model":"seedance-1","content":[{"type":"text","text":"a cat"}]}`
	nodeStatus, nodeBody := e.post(t, "/v1/contents/generations/tasks", "sk-a", create)
	e.world.waitReleased(t)
	localStatus, localBody := local.post(t, "/v1/contents/generations/tasks", "sk-a", create)
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, localBody, nodeBody)
	rec := e.lastRecord(t, 1)
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI_VIDEO_TASK, rec.GetKind())
	require.True(t, service.IsSeedanceTaskKey(rec.GetTaskId()), rec.GetTaskId())

	gid := int64(5)
	require.NoError(t, e.world.sel.deps.Gateway.BindGrokMediaVideoRequestAccount(context.Background(), &gid, rec.GetTaskId(), 3, 11, 1))
	path := "/v1/contents/generations/tasks/cgt-e2e1"
	nodeStatus, nodeBody = e.get(t, path, "sk-a")
	e.world.waitReleased(t)
	localStatus, localBody = local.get(t, path, "sk-a")
	require.Equal(t, http.StatusOK, localStatus, localBody)
	require.Equal(t, localStatus, nodeStatus, nodeBody)
	require.Equal(t, localBody, nodeBody)
	rec = e.lastRecord(t, 2)
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI_VIDEO_COMPLETION, rec.GetKind())
	require.Equal(t, rec.GetTaskId(), service.SeedanceTaskKey("cgt-e2e1"))

	// 非 OpenAI 分组的 Seedance 请求交给主节点（本地回 403）。
	status, body := e.post(t, "/v1/contents/generations/tasks", "sk-grokgroup", create)
	require.NotEqual(t, http.StatusOK, status, strings.TrimSpace(body))
}
