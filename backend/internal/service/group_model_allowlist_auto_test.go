package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubGroupAutoModelLister struct {
	accounts map[int64][]Account
	err      error
	calls    int
}

func (s *stubGroupAutoModelLister) ListSchedulableByGroupID(_ context.Context, groupID int64) ([]Account, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.accounts[groupID], nil
}

func autoAllowlistAccount(platform string, mapping map[string]any) Account {
	credentials := map[string]any{"api_key": "sk-test", "base_url": "https://relay.example"}
	if mapping != nil {
		credentials["model_mapping"] = mapping
	}
	return Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: credentials}
}

func TestApplyAutoModelAllowlistsKeepsManualAllowlist(t *testing.T) {
	lister := &stubGroupAutoModelLister{accounts: map[int64][]Account{
		2: {autoAllowlistAccount(PlatformOpenAI, map[string]any{"gpt-9": "gpt-9"})},
	}}
	svc := &APIKeyService{}
	svc.SetGroupAutoModelAccountLister(lister)

	groups := []Group{{ID: 2, Platform: PlatformOpenAI, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5.6"}}}}
	svc.ApplyAutoModelAllowlists(context.Background(), groups)

	require.Equal(t, []string{"gpt-5.6"}, groups[0].ModelAllowlist.Models)
	require.Equal(t, GroupModelAllowlistSourceManual, groups[0].ModelAllowlistSource)
	require.Zero(t, lister.calls, "manual allowlist must not query accounts")
}

func TestApplyAutoModelAllowlistsDerivesFromMatchingAccounts(t *testing.T) {
	lister := &stubGroupAutoModelLister{accounts: map[int64][]Account{
		46: {
			autoAllowlistAccount(PlatformGrok, map[string]any{"grok-4.7": "grok-4.7", "grok-4.6": "grok-4.6"}),
			autoAllowlistAccount(PlatformGrok, map[string]any{"grok-4.7": "grok-4.7", "grok-*": "grok-4.6"}),
			// 平台不一致的账号调度器不会选，不能算进分组支持的模型。
			autoAllowlistAccount(PlatformOpenAI, map[string]any{"gpt-5.5": "gpt-5.5"}),
		},
	}}
	svc := &APIKeyService{}
	svc.SetGroupAutoModelAccountLister(lister)

	// 关着的白名单即使留有旧内容也算"没手动设置"。
	groups := []Group{{ID: 46, Platform: PlatformGrok, ModelAllowlist: GroupModelAllowlist{Models: []string{"grok-4.3"}}}}
	svc.ApplyAutoModelAllowlists(context.Background(), groups)

	require.True(t, groups[0].ModelAllowlist.Enabled)
	require.Equal(t, []string{"grok-4.6", "grok-4.7"}, groups[0].ModelAllowlist.Models)
	require.Equal(t, GroupModelAllowlistSourceAuto, groups[0].ModelAllowlistSource)
}

func TestAutoModelAllowlistOpenAIUnmappedAccountAddsDefaults(t *testing.T) {
	models := autoModelAllowlistFromAccounts(PlatformOpenAI, []Account{
		autoAllowlistAccount(PlatformOpenAI, map[string]any{"custom-model": "gpt-5.5"}),
		autoAllowlistAccount(PlatformOpenAI, nil),
	})
	require.Contains(t, models, "custom-model")
	for _, model := range defaultModelsListCandidateIDs(PlatformOpenAI) {
		require.Contains(t, models, model)
	}
}

func TestAutoModelAllowlistCNProviderWithoutMappingIsOmitted(t *testing.T) {
	// kimi 没有可靠的默认模型列表（默认候选会退回 Claude 模型），宁可不下发。
	require.Nil(t, autoModelAllowlistFromAccounts(PlatformKimi, []Account{autoAllowlistAccount(PlatformKimi, nil)}))
	require.Equal(t, []string{"kimi-k3"}, autoModelAllowlistFromAccounts(PlatformKimi, []Account{
		autoAllowlistAccount(PlatformKimi, map[string]any{"kimi-k3": "kimi-k3"}),
	}))
}

func TestAutoModelAllowlistNoMatchingAccountsIsOmitted(t *testing.T) {
	require.Nil(t, autoModelAllowlistFromAccounts(PlatformGrok, nil))
	require.Nil(t, autoModelAllowlistFromAccounts(PlatformGrok, []Account{autoAllowlistAccount(PlatformOpenAI, nil)}))
}

func TestGroupAutoModelAllowlistCachesAndRetriesErrors(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	lister := &stubGroupAutoModelLister{err: errors.New("db down")}
	auto := &groupAutoModelAllowlist{lister: lister, now: func() time.Time { return now }}
	group := &Group{ID: 7, Platform: PlatformGrok}

	require.Nil(t, auto.modelsFor(context.Background(), group))
	lister.err = nil
	lister.accounts = map[int64][]Account{7: {autoAllowlistAccount(PlatformGrok, map[string]any{"grok-4.7": "grok-4.7"})}}
	require.Equal(t, []string{"grok-4.7"}, auto.modelsFor(context.Background(), group), "errors must not be cached")
	require.Equal(t, 2, lister.calls)

	lister.accounts[7] = []Account{autoAllowlistAccount(PlatformGrok, map[string]any{"grok-5": "grok-5"})}
	require.Equal(t, []string{"grok-4.7"}, auto.modelsFor(context.Background(), group), "served from cache within TTL")
	require.Equal(t, 2, lister.calls)

	now = now.Add(groupAutoModelAllowlistTTL + time.Second)
	require.Equal(t, []string{"grok-5"}, auto.modelsFor(context.Background(), group))
	require.Equal(t, 3, lister.calls)
}

func TestAutoModelAllowlistDropsCrossVendorAliases(t *testing.T) {
	models := autoModelAllowlistFromAccounts(PlatformZhipu, []Account{
		autoAllowlistAccount(PlatformZhipu, map[string]any{"glm-5.3": "glm-5.3", "glm-5.2": "glm-5.2", "gpt-5.6-sol": "glm-5.3"}),
		autoAllowlistAccount(PlatformZhipu, map[string]any{"gpt-5.6-terra": "glm-5.3", "claude-opus-4-8": "glm-5.3"}),
	})
	require.Equal(t, []string{"glm-5.2", "glm-5.3"}, models)

	// 厂商认不出的自定义名保留。
	require.Equal(t, []string{"Kun", "deepseek-v4.1-flash"}, autoModelAllowlistFromAccounts(PlatformDeepseek, []Account{
		autoAllowlistAccount(PlatformDeepseek, map[string]any{"Kun": "Kun", "deepseek-v4.1-flash": "deepseek-v4.1-flash", "gpt-5.5": "deepseek-v4.1-flash"}),
	}))
}

func TestAutoModelAllowlistFallsBackToAliasesWhenGroupHasNoNativeModel(t *testing.T) {
	// 只有别名时别名就是唯一能用的模型名，不能列成空。
	models := autoModelAllowlistFromAccounts(PlatformZhipu, []Account{
		autoAllowlistAccount(PlatformZhipu, map[string]any{"gpt-5.6-sol": "glm-5.3"}),
	})
	require.Equal(t, []string{"gpt-5.6-sol"}, models)
}

func TestModelsListCandidatesCNProviderListsOnlyVendorModels(t *testing.T) {
	candidates := modelsListCandidatesFromAccounts(PlatformZhipu, []Account{
		autoAllowlistAccount(PlatformZhipu, map[string]any{"glm-5.3": "glm-5.3", "gpt-5.6-sol": "glm-5.3", "claude-opus-4-8": "glm-5.3"}),
		autoAllowlistAccount(PlatformOpenAI, map[string]any{"gpt-5.5": "gpt-5.5"}),
	})
	require.Equal(t, []string{"glm-5.3"}, candidates)
	require.Empty(t, modelsListCandidatesFromAccounts(PlatformKimi, nil), "no Claude fallback for CN providers")

	openAI := modelsListCandidatesFromAccounts(PlatformOpenAI, nil)
	require.Equal(t, defaultModelsListCandidateIDs(PlatformOpenAI), openAI)
}

func TestModelVendorPlatform(t *testing.T) {
	cases := map[string]string{
		"gpt-5.6-sol": PlatformOpenAI, "o3-mini": PlatformOpenAI, "claude-opus-4-8": PlatformAnthropic,
		"gemini-2.5-pro": PlatformGemini, "grok-4.7": PlatformGrok, "GLM-5.3": PlatformZhipu,
		"kimi-k3": PlatformKimi, "moonshot-v1-8k": PlatformKimi, "deepseek-v4-pro": PlatformDeepseek,
		"MiniMax-M2.7": PlatformMiniMax, "Kun": "",
	}
	for model, want := range cases {
		require.Equal(t, want, modelVendorPlatform(model), model)
	}
	require.True(t, groupListsModel(PlatformAntigravity, "claude-sonnet-5"))
	require.True(t, groupListsModel(PlatformAntigravity, "gemini-3-pro"))
	require.False(t, groupListsModel(PlatformAntigravity, "gpt-5.5"))
	require.True(t, groupListsModel(PlatformComposite, "gpt-5.5"))
}
