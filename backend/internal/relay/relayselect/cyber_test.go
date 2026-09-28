package relayselect

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type recordedCyber struct {
	hit        handler.CyberPolicyHit
	subj       handler.CyberPolicySubject
	blockScope string
	blockKeys  []string
}

// cyber 命中：归属取自选号记录，屏蔽的键由选号时的查询键推导（不信消息），每次请求只记一次；
// 不是这台节点进行中的选号、账号对不上的一律不认。
func TestCyberPolicyHitOnTheMaster(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))
	var got []recordedCyber
	w.sel.recordCyber = func(hit handler.CyberPolicyHit, subj handler.CyberPolicySubject, scope string, keys []string) {
		got = append(got, recordedCyber{hit: hit, subj: subj, blockScope: scope, blockKeys: keys})
	}

	req := responsesRequest("r1", 1, "sk-a")
	lookup := &relayv1.CyberSessionLookup{ExplicitKey: "explicit", ScopeKey: "scope", TranscriptKeys: []string{"t1", "t2"}, PreLatestUserKey: "t1"}
	req.Cyber = lookup
	resp, err := w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", resp.GetRejection())

	hit := &relayv1.CyberPolicyHitRequest{
		SelectionId: sel.GetSelectionId(), AccountId: 1,
		Message: "blocked", Body: strings.Repeat("x", 20<<10), UpstreamStatus: 400, UpstreamInputTokens: 9,
		Model: "gpt-5", Stream: true, Platform: "openai", RequestPath: "/v1/responses", InboundEndpoint: "/v1/responses",
		UserAgent: "codex/1.0", ClientIp: "5.6.7.8", RequestId: "req-1", CreatedAtUnixMs: 1_800_000_000_000,
	}
	_, err = w.sel.CyberPolicyHit(ctx, testNode+1, hit)
	require.ErrorIs(t, err, master.ErrSelectionNotFound, "another node's selection")
	wrongAccount := proto.Clone(hit).(*relayv1.CyberPolicyHitRequest)
	wrongAccount.AccountId = 2
	_, err = w.sel.CyberPolicyHit(ctx, testNode, wrongAccount)
	require.ErrorIs(t, err, master.ErrSelectionNotFound, "not the selected account")
	require.Empty(t, got)

	_, err = w.sel.CyberPolicyHit(ctx, testNode, hit)
	require.NoError(t, err)
	_, err = w.sel.CyberPolicyHit(ctx, testNode, hit)
	require.NoError(t, err)
	require.Len(t, got, 1, "recorded once per request")
	r := got[0]
	require.Equal(t, "sk-a", r.subj.APIKey.Key, "the key admitted at selection")
	require.Equal(t, int64(3), r.subj.APIKey.User.ID)
	require.Equal(t, int64(5), r.subj.APIKey.Group.ID)
	require.Equal(t, int64(1), r.subj.Account.ID)
	require.Equal(t, testNode, *r.subj.NodeID)
	wantScope, wantKeys := cyberLookup(lookup).BlockWritePlan()
	require.Equal(t, wantScope, r.blockScope)
	require.Equal(t, wantKeys, r.blockKeys)
	require.Equal(t, []string{"explicit", "t2", "t1"}, r.blockKeys)
	require.Equal(t, "cyber_policy", r.hit.Mark.Code)
	require.Equal(t, "blocked", r.hit.Mark.Message)
	require.Len(t, r.hit.Mark.Body, cyberHitTextLimit, "oversized bodies are cut")
	require.Equal(t, 400, r.hit.Mark.UpstreamStatus)
	require.Equal(t, 9, r.hit.Mark.UpstreamInTok)
	require.Equal(t, "req-1", r.hit.RequestID)
	require.True(t, r.hit.Stream)
	require.Equal(t, time.UnixMilli(1_800_000_000_000), r.hit.CreatedAt)

	// 释放之后不再认。
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
	_, err = w.sel.CyberPolicyHit(ctx, testNode, hit)
	require.ErrorIs(t, err, master.ErrSelectionNotFound)
}

// 选号时没带查询键（从节点上屏蔽开关是关的）：不写屏蔽表，风控记录照记。
func TestCyberPolicyHitWithoutLookupKeys(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))
	var got []recordedCyber
	w.sel.recordCyber = func(hit handler.CyberPolicyHit, subj handler.CyberPolicySubject, scope string, keys []string) {
		got = append(got, recordedCyber{hit: hit, subj: subj, blockScope: scope, blockKeys: keys})
	}
	resp, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.NoError(t, err)
	_, err = w.sel.CyberPolicyHit(ctx, testNode, &relayv1.CyberPolicyHitRequest{SelectionId: resp.GetSelection().GetSelectionId(), AccountId: 1})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Empty(t, got[0].blockKeys)
	require.Empty(t, got[0].blockScope)
}

func TestTruncateTextKeepsUTF8(t *testing.T) {
	require.Equal(t, "ab", truncateText("ab", 4))
	require.Equal(t, "a", truncateText("a中", 3), "does not cut a character in half")
	require.Equal(t, "a中", truncateText("a中b", 4))
}

// cyber 会话屏蔽在选号时命中：主节点记运维日志（单机 writeCyberSessionBlocked 记的那条），请求事实取自选号请求，
// 归属取自准入的 Key，带上转发的节点。
func TestCyberSessionBlockedIsLoggedOnTheMaster(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))
	w.sel.findCyberBlocked = func(context.Context, service.CyberSessionLookup) string { return "blocked-key" }
	type logged struct {
		key *service.APIKey
		r   handler.CyberSessionBlockedRequest
	}
	var got []logged
	w.sel.recordCyberBlocked = func(_ context.Context, apiKey *service.APIKey, r handler.CyberSessionBlockedRequest) {
		got = append(got, logged{key: apiKey, r: r})
	}

	req := responsesRequest("r1", 1, "sk-a")
	req.Cyber = &relayv1.CyberSessionLookup{ExplicitKey: "k"}
	req.Stream, req.UserAgent, req.HttpRequestId, req.ClientRequestId = true, "codex/1.0", "req-node-1", "client-1"
	resp, err := w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	require.Equal(t, "blocked-key", resp.GetRejection().GetCyberBlockKey())
	require.Len(t, got, 1)
	require.Equal(t, "sk-a", got[0].key.Key)
	r := got[0].r
	require.Equal(t, "blocked-key", r.SessionBlockKey)
	require.Equal(t, "req-node-1", r.RequestID)
	require.Equal(t, "client-1", r.ClientRequestID)
	require.Equal(t, "codex/1.0", r.UserAgent)
	require.Equal(t, "5.6.7.8", r.ClientIP)
	require.Equal(t, "gpt-5", r.Model)
	require.Equal(t, "/v1/responses", r.RequestPath)
	require.Equal(t, handler.EndpointResponses, r.InboundEndpoint)
	require.True(t, r.Stream)
	require.Equal(t, testNode, *r.NodeID)

	// 没命中不记。
	w.sel.findCyberBlocked = func(context.Context, service.CyberSessionLookup) string { return "" }
	_, err = w.sel.Select(ctx, testNode, responsesRequest("r2", 1, "sk-a"))
	require.NoError(t, err)
	require.Len(t, got, 1)
}
