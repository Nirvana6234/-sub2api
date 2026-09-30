package relayselect

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// useAutoGroupCandidates 给测试世界装上一把自动分组 Key（sk-switch，按价格选）：候选是 41（最便宜）和 42，都是 OpenAI。
func useAutoGroupCandidates(w *world) {
	cheap := openAIGroup(41)
	cheap.RateMultiplier, cheap.ActiveAccountCount, cheap.AllowMessagesDispatch = 0.5, 1, true
	other := openAIGroup(42)
	other.ActiveAccountCount, other.AllowMessagesDispatch = 1, true
	key := testKey("sk-switch", 16, nil)
	key.AutoGroup, key.AutoGroupIDs, key.AutoGroupStrategy = true, []int64{41, 42}, "price"
	w.keys.keys["sk-switch"] = key
	w.sel.deps.APIKeys = service.NewAPIKeyService(w.keys, autoGroupUsers{},
		autoGroupGroups{groups: []service.Group{*cheap, *other}}, autoGroupSubscriptions{}, nil, nil, w.sel.deps.Config)
}

func inGroup(a service.Account, groupID int64) service.Account {
	a.AccountGroups = []service.AccountGroup{{AccountID: a.ID, GroupID: groupID}}
	return a
}

type observedAutoGroup struct {
	groupID int64
	model   string
	status  int
}

func recordAutoGroupObservations(sel *selector) func() []observedAutoGroup {
	var mu sync.Mutex
	var got []observedAutoGroup
	sel.observeAutoGroup = func(apiKey *service.APIKey, model string, status int, _ *int64) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, observedAutoGroup{groupID: *apiKey.GroupID, model: model, status: status})
	}
	return func() []observedAutoGroup {
		mu.Lock()
		defer mu.Unlock()
		return append([]observedAutoGroup(nil), got...)
	}
}

// startStandardE2E 同 startE2EWith，按标准模式跑（简易模式不看账号属于哪个分组）。
func startStandardE2E(t *testing.T, accounts func(upstreamURL string) []service.Account) *e2e {
	t.Helper()
	return startE2EWithConfig(t, func(cfg *config.Config) { cfg.RunMode = config.RunModeStandard }, accounts)
}

func e2eAccount(id int64, name, baseURL string) service.Account {
	return service.Account{ID: id, Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 2,
		Credentials: map[string]any{"api_key": "SECRET-" + name, "base_url": baseURL}}
}

// 选到的分组一个账号都没有（本地 tryOpenAIAutoGroupFailover 的第一处）：从节点经主节点换到下一个候选再选，
// 请求结束时的结果报给主节点，记在换到的分组上。Responses、Chat Completions、Messages 三个入口各走一遍。
func TestNodeSwitchesAutoGroupWhenGroupHasNoAccount(t *testing.T) {
	e := startStandardE2E(t, func(upstream string) []service.Account {
		return []service.Account{inGroup(e2eAccount(1, "one", upstream), 42)}
	})
	useAutoGroupCandidates(e.world)
	observed := recordAutoGroupObservations(e.world.sel)

	for i, tc := range []struct{ path, body string }{
		{"/v1/responses", `{"model":"gpt-5","input":"hi"}`},
		{"/v1/chat/completions", `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/messages", `{"model":"gpt-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`},
	} {
		status, body := e.post(t, tc.path, "sk-switch", tc.body)
		require.Equal(t, http.StatusOK, status, "%s: %s", tc.path, body)
		require.Eventually(t, func() bool { return len(e.settler.records()) == i+1 }, 5*time.Second, 20*time.Millisecond, tc.path)
		voucher, err := sign.VerifyVoucher(e.settler.records()[i].GetVoucher(), e.world.pub, e.nodeID, time.Now())
		require.NoError(t, err)
		require.Equal(t, int64(42), voucher.GetGroupId(), tc.path)
		require.Eventually(t, func() bool { return len(observed()) == i+1 }, 5*time.Second, 20*time.Millisecond, tc.path)
		require.Equal(t, observedAutoGroup{groupID: 42, model: "gpt-5", status: http.StatusOK}, observed()[i],
			"%s: the result is recorded against the group that served the request", tc.path)
		e.world.waitReleased(t)
	}
}

// 当前分组的账号都失败了（换号用完，本地第二处）：换到下一个候选接着选；本地这时复查计费资格，从节点用主节点换组时
// 顺带查好的结果。
func TestNodeSwitchesAutoGroupAfterAccountsExhausted(t *testing.T) {
	e := startStandardE2E(t, func(upstream string) []service.Account {
		return []service.Account{
			inGroup(e2eAccount(1, "limited", upstream+"/status-429"), 41),
			inGroup(e2eAccount(2, "fine", upstream), 42),
		}
	})
	useAutoGroupCandidates(e.world)

	status, body := e.post(t, "/v1/responses", "sk-switch", `{"model":"gpt-5","input":"hi"}`)
	require.Equal(t, http.StatusOK, status, body)
	require.Eventually(t, func() bool {
		for _, r := range e.settler.records() {
			v, err := sign.VerifyVoucher(r.GetVoucher(), e.world.pub, e.nodeID, time.Now())
			if err == nil && v.GetGroupId() == 42 {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "the request is served by the next candidate group")
	e.world.waitReleased(t)
}

// 没有可换的候选：按主节点原来的拒绝写，与不是自动分组的 Key 遇到同样情况时一样。
func TestNodeWritesMasterRejectionWhenNoAutoGroupLeft(t *testing.T) {
	e := startStandardE2E(t, func(upstream string) []service.Account {
		return []service.Account{inGroup(e2eAccount(1, "elsewhere", upstream), 99)}
	})
	useAutoGroupCandidates(e.world)
	plain := openAIGroup(41)
	e.world.keys.keys["sk-plain"] = testKey("sk-plain", 17, plain)

	wantStatus, wantBody := e.post(t, "/v1/responses", "sk-plain", `{"model":"gpt-5","input":"hi"}`)
	require.NotEqual(t, http.StatusOK, wantStatus)
	status, body := e.post(t, "/v1/responses", "sk-switch", `{"model":"gpt-5","input":"hi"}`)
	require.Equal(t, wantStatus, status)
	require.JSONEq(t, wantBody, body)
	require.Len(t, e.hits, 0)
	e.world.waitReleased(t)
}

// 同一次请求换了分组：换号记录从头来（本地换组后清空已失败的账号），分组记成新的。
func TestSelectResetsRequestOnAutoGroupChange(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeStandard, apiKeyAccount(1, "one"), apiKeyAccount(2, "two"))
	useAutoGroupCandidates(w)

	first := responsesRequest("r-switch", 1, "sk-switch")
	first.AutoGroupId = 41
	resp, err := w.sel.Select(ctx, testNode, first)
	require.NoError(t, err)
	require.NotNil(t, resp.GetSelection(), "rejection: %+v", resp.GetRejection())
	used := resp.GetSelection().GetAccount().GetId()

	second := responsesRequest("r-switch", 2, "sk-switch")
	second.AutoGroupId, second.ExcludedAccountIds = 41, []int64{used}
	resp2, err := w.sel.Select(ctx, testNode, second)
	require.NoError(t, err)
	require.NotNil(t, resp2.GetSelection(), "rejection: %+v", resp2.GetRejection())
	record := w.sel.requests[requestKey{nodeID: testNode, requestID: "r-switch"}]
	require.Len(t, record.excluded, 1)

	third := responsesRequest("r-switch", 3, "sk-switch")
	third.AutoGroupId = 42
	resp3, err := w.sel.Select(ctx, testNode, third)
	require.NoError(t, err)
	require.NotNil(t, resp3.GetSelection(), "rejection: %+v", resp3.GetRejection())
	require.Equal(t, int64(42), resp3.GetSelection().GetGroupId())
	require.Empty(t, record.excluded, "accounts that failed in the previous group are selectable again")
	require.Equal(t, int64(42), record.groupID)

	for _, r := range []*relayv1.SelectResponse{resp, resp2} {
		w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: r.GetSelection().GetSelectionId()})
	}
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: resp3.GetSelection().GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
}

// 同一次请求后来的选号在查到请求之前就被拒（这里是带来的分组不是候选）：请求到此为止，用户槽和还占着的选号放掉
// （交给主节点转发时主节点要重新占用户槽）。
func TestSelectEndsRequestRejectedBeforeLookup(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeStandard, apiKeyAccount(1, "one"))
	useAutoGroupCandidates(w)

	first := responsesRequest("r-end", 1, "sk-switch")
	first.AutoGroupId = 41
	resp, err := w.sel.Select(ctx, testNode, first)
	require.NoError(t, err)
	require.NotNil(t, resp.GetSelection(), "rejection: %+v", resp.GetRejection())
	require.EqualValues(t, 1, w.slots.held.Load())

	second := responsesRequest("r-end", 2, "sk-switch")
	second.AutoGroupId = 5
	resp, err = w.sel.Select(ctx, testNode, second)
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_RAW, resp.GetRejection().GetFormat())
	w.waitReleased(t)
}

// 主节点换组：记当前分组失败，选下一个这次请求没试过的候选；都试过了就不换。
func TestSwitchAutoGroupPicksNextUntriedCandidate(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeStandard, apiKeyAccount(1, "one"))
	useAutoGroupCandidates(w)
	req := &relayv1.SwitchAutoGroupRequest{ApiKey: "sk-switch", Method: "POST", Path: "/v1/responses", Model: "gpt-5", CurrentGroupId: 41}

	resp, err := w.sel.SwitchAutoGroup(ctx, testNode, req)
	require.NoError(t, err)
	require.True(t, resp.GetSwitched())
	require.Nil(t, resp.GetBillingRejection())

	req.FailedGroupIds = []int64{42}
	resp, err = w.sel.SwitchAutoGroup(ctx, testNode, req)
	require.NoError(t, err)
	require.False(t, resp.GetSwitched(), "every candidate has been tried in this request")

	req.CurrentGroupId = 5
	req.FailedGroupIds = nil
	resp, err = w.sel.SwitchAutoGroup(ctx, testNode, req)
	require.NoError(t, err)
	require.False(t, resp.GetSwitched(), "a group outside the key's candidates is not switched from")
}

// 结果上报只认这台节点最近准入过的用户。
func TestReportAutoGroupResultNeedsRecentAdmission(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeStandard, apiKeyAccount(1, "one"))
	useAutoGroupCandidates(w)
	observed := recordAutoGroupObservations(w.sel)
	report := &relayv1.AutoGroupResult{ApiKey: "sk-switch", Model: "gpt-5", GroupId: 42, Status: http.StatusTooManyRequests}

	_, err := w.sel.ReportAutoGroupResult(ctx, testNode, report)
	require.NoError(t, err)
	require.Empty(t, observed(), "a user this node never admitted is ignored")

	_, err = w.sel.ResolveRoute(ctx, testNode, &relayv1.ResolveRouteRequest{ApiKey: "sk-switch", Method: "POST", Path: "/v1/responses", Model: "gpt-5"})
	require.NoError(t, err)
	_, err = w.sel.ReportAutoGroupResult(ctx, testNode, report)
	require.NoError(t, err)
	require.Equal(t, []observedAutoGroup{{groupID: 42, model: "gpt-5", status: http.StatusTooManyRequests}}, observed())
}
