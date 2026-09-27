package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// AppliesTo 说"不会审核"时，Check 必须真的什么都不做（主从分流据此把请求交给从节点，设计 3.4）；
// 说"会"时 Check 确实调了审核接口。
func TestContentModerationAppliesToAgreesWithCheck(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{CategoryScores: map[string]float64{"sexual": 0.1}}}})
	}))
	defer server.Close()
	groupID := int64(5)
	otherGroup := int64(6)

	cases := []struct {
		name   string
		risk   string
		mutate func(cfg *ContentModerationConfig)
		group  *int64
		model  string
	}{
		{name: "in scope", risk: "true"},
		{name: "risk control off", risk: "false"},
		{name: "config disabled", risk: "true", mutate: func(cfg *ContentModerationConfig) { cfg.Enabled = false }},
		{name: "mode off", risk: "true", mutate: func(cfg *ContentModerationConfig) { cfg.Mode = ContentModerationModeOff }},
		{name: "other group", risk: "true", group: &otherGroup, mutate: func(cfg *ContentModerationConfig) {
			cfg.AllGroups, cfg.GroupIDs = false, []int64{groupID}
		}},
		{name: "ungrouped", risk: "true", group: nil, mutate: func(cfg *ContentModerationConfig) {
			cfg.AllGroups, cfg.GroupIDs = false, []int64{groupID}
		}},
		{name: "listed group", risk: "true", mutate: func(cfg *ContentModerationConfig) {
			cfg.AllGroups, cfg.GroupIDs = false, []int64{groupID}
		}},
		{name: "model excluded", risk: "true", model: "gpt-x", mutate: func(cfg *ContentModerationConfig) {
			cfg.ModelFilter = ContentModerationModelFilter{Type: ContentModerationModelFilterExclude, Models: []string{"gpt-x"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultContentModerationConfig()
			cfg.Enabled = true
			cfg.AllGroups = true
			cfg.Mode = ContentModerationModePreBlock
			cfg.BaseURL = server.URL
			cfg.APIKeys = []string{"sk-test"}
			cfg.SampleRate = 100
			if tc.mutate != nil {
				tc.mutate(cfg)
			}
			rawCfg, err := json.Marshal(cfg)
			require.NoError(t, err)
			svc := NewContentModerationService(&contentModerationTestSettingRepo{values: map[string]string{
				SettingKeyRiskControlEnabled:      tc.risk,
				SettingKeyContentModerationConfig: string(rawCfg),
			}}, &contentModerationTestRepo{}, &contentModerationTestHashCache{}, nil, nil, nil, nil, nil)
			group := &groupID
			if tc.name == "other group" || tc.name == "ungrouped" {
				group = tc.group
			}
			model := tc.model
			if model == "" {
				model = "gpt-5"
			}
			applies := svc.AppliesTo(context.Background(), group, model)
			before := hits.Load()
			_, err = svc.Check(context.Background(), ContentModerationCheckInput{
				Endpoint: "/v1/responses", Provider: "openai", Protocol: ContentModerationProtocolOpenAIResponses,
				GroupID: group, Model: model, Body: []byte(`{"input":"hello there"}`),
			})
			require.NoError(t, err)
			checked := hits.Load() > before
			if !applies {
				require.False(t, checked, "AppliesTo said no but Check moderated the request")
			}
			if tc.name == "in scope" || tc.name == "listed group" {
				require.True(t, applies)
				require.True(t, checked, "sanity: an in-scope request is moderated")
			}
		})
	}
}
