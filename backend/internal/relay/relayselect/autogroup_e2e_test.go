package relayselect

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type autoGroupUsers struct {
	service.UserRepository
}

func (autoGroupUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	return &service.User{ID: id, Status: service.StatusActive, Balance: 10, Concurrency: 5}, nil
}

type autoGroupGroups struct {
	service.GroupRepository
	groups []service.Group
}

func (r autoGroupGroups) ListActive(context.Context) ([]service.Group, error) {
	return append([]service.Group(nil), r.groups...), nil
}

type autoGroupSubscriptions struct {
	service.UserSubscriptionRepository
}

func (autoGroupSubscriptions) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

// useAutoGroupKey 给测试世界装上一把自动分组 Key（sk-auto）：候选是 30（Anthropic，最便宜，只接 claude-*，
// 也是鉴权时的冷启动分组）和 31（OpenAI）。
func useAutoGroupKey(w *world) {
	cheap := openAIGroup(30)
	cheap.Platform = service.PlatformAnthropic
	cheap.RateMultiplier = 0.5
	cheap.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"claude-*"}}
	cheap.ActiveAccountCount = 1
	openAI := openAIGroup(31)
	openAI.ActiveAccountCount = 1
	key := testKey("sk-auto", 15, nil)
	key.AutoGroup, key.AutoGroupIDs, key.AutoGroupStrategy = true, []int64{30, 31}, "price"
	w.keys.keys["sk-auto"] = key
	w.sel.deps.APIKeys = service.NewAPIKeyService(w.keys, autoGroupUsers{},
		autoGroupGroups{groups: []service.Group{*cheap, *openAI}}, autoGroupSubscriptions{}, nil, nil, w.sel.deps.Config)
}

// 自动分组 Key 经从节点（本地 autoGroupModelRoutingMiddleware，设计 3.2）：从节点按请求体里的模型问主节点选分组，
// 换上选定分组的 Key；之后的选号带着这个分组，主节点只核对它是这把 Key 的候选。选到的分组不是 OpenAI 的交给主节点。
func TestNodeServesAutoGroupKeys(t *testing.T) {
	e := startE2E(t)
	useAutoGroupKey(e.world)

	status, body := e.post(t, "/v1/responses", "sk-auto", `{"model":"gpt-5","input":"hi"}`)
	require.Equal(t, http.StatusOK, status, body)
	select {
	case r := <-e.hits:
		require.Equal(t, "/v1/responses", r.URL.Path)
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not called")
	}
	require.Eventually(t, func() bool { return len(e.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	voucher, err := sign.VerifyVoucher(e.settler.records()[0].GetVoucher(), e.world.pub, e.nodeID, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(31), voucher.GetGroupId(), "billed against the group chosen for the model, not the cold-start group")
	e.world.waitReleased(t)

	// claude-* 选到 Anthropic 分组：交给主节点（测试世界没有主节点转发，按 503 写）。
	status, body = e.post(t, "/v1/responses", "sk-auto", `{"model":"claude-sonnet-4-5","input":"hi"}`)
	require.Equal(t, http.StatusServiceUnavailable, status, body)
	require.Len(t, e.hits, 0)
}

// 主节点只接受这把 Key 的候选分组：从节点带来别的分组时按本地选组失败的写法拒绝；带来的候选分组照用。
func TestSelectChecksPinnedAutoGroup(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, "", apiKeyAccount(1, "one"))
	useAutoGroupKey(w)

	req := responsesRequest("r-auto-1", 1, "sk-auto")
	req.AutoGroupId = 5
	resp, err := w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	rej := resp.GetRejection()
	require.NotNil(t, rej, "a group outside the key's candidates is refused")
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_RAW, rej.GetFormat())
	require.Equal(t, int32(http.StatusForbidden), rej.GetStatus())
	require.Equal(t, "AUTO_GROUP_UNAVAILABLE", gjson.GetBytes(rej.GetBody(), "code").String())

	req = responsesRequest("r-auto-2", 1, "sk-auto")
	req.AutoGroupId, req.Model = 30, "claude-sonnet-4-5"
	resp, err = w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED, resp.GetRejection().GetFormat(), "an Anthropic candidate goes to the master")

	req = responsesRequest("r-auto-3", 1, "sk-auto")
	req.AutoGroupId = 31
	resp, err = w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	require.NotNil(t, resp.GetSelection(), "rejection: %+v", resp.GetRejection())
	require.Equal(t, int64(31), resp.GetSelection().GetGroupId())
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: resp.GetSelection().GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
}
