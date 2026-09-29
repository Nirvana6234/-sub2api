package relayselect

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodegw"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// stickyCache 是只记粘性会话绑定的网关缓存。
type stickyCache struct {
	nodegw.NoopGatewayCache
	bindings map[string]int64
}

func (c *stickyCache) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	if id, ok := c.bindings[key]; ok {
		return id, nil
	}
	return 0, service.ErrStickySessionNotFound
}

func (c *stickyCache) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

// Codex 审查子代理：从节点带上父会话哈希，主节点选号时优先用父会话绑定的账号（与单机一样）。
func TestSelectFollowsTheGuardianParentSession(t *testing.T) {
	useGatewayCache(t, &stickyCache{bindings: map[string]int64{"openai:parent-hash": 2}})
	preferred := apiKeyAccount(1, "one")
	preferred.Priority = 1
	parent := apiKeyAccount(2, "two")
	parent.Priority = 100
	w := newWorld(t, config.RunModeSimple, preferred, parent)
	ctx := context.Background()

	plain := responsesRequest("r1", 1, "sk-a")
	plain.SessionHash = "child-hash"
	resp, err := w.sel.Select(ctx, testNode, plain)
	require.NoError(t, err)
	require.Equal(t, int64(1), resp.GetSelection().GetAccount().GetId(), "without the hint the higher-priority account wins")

	req := responsesRequest("r2", 1, "sk-a")
	req.SessionHash = "child-hash"
	req.GuardianParentSessionHash = "parent-hash"
	resp, err = w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	require.Equal(t, int64(2), resp.GetSelection().GetAccount().GetId(), "the review sub-agent follows its parent session's account")
}
