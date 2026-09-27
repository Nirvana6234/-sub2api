package nodegw

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 错误透传规则随配置快照下发：换快照时整体替换；分段解不开时保留原来的规则。
func TestErrorPassthroughRulesFollowTheConfigSnapshot(t *testing.T) {
	cache := node.NewConfigCache()
	svc := service.NewStaticErrorPassthroughService(nil)
	cache.OnSwap(func(*relayv1.ConfigSnapshot) { applyErrorPassthroughRules(cache, svc) })
	body := []byte(`{"error":{"message":"quota exceeded for this workspace"}}`)

	rules, err := json.Marshal([]*model.ErrorPassthroughRule{{
		ID: 1, Name: "quota", Enabled: true, ErrorCodes: []int{429}, Keywords: []string{"quota exceeded"},
		MatchMode: model.MatchModeAll, Platforms: []string{service.PlatformOpenAI}, PassthroughCode: true, PassthroughBody: true,
	}})
	require.NoError(t, err)
	require.NoError(t, cache.Apply(&relayv1.ConfigSnapshot{Version: "v1", Sections: map[string][]byte{master.SectionErrorPassthroughRules: rules}}))
	matched := svc.MatchRule(service.PlatformOpenAI, 429, body)
	require.NotNil(t, matched)
	require.Equal(t, "quota", matched.Name)

	require.NoError(t, cache.Apply(&relayv1.ConfigSnapshot{Version: "v2", Sections: map[string][]byte{master.SectionErrorPassthroughRules: []byte("not json")}}))
	require.NotNil(t, svc.MatchRule(service.PlatformOpenAI, 429, body), "a malformed section keeps the previous rules")

	require.NoError(t, cache.Apply(&relayv1.ConfigSnapshot{Version: "v3"}))
	require.Nil(t, svc.MatchRule(service.PlatformOpenAI, 429, body), "rules removed on the master are removed here")
}
