package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type anthropicFallbackAccountRepo struct {
	AccountRepository
	byGroup map[int64][]Account
}

func (r *anthropicFallbackAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, _ string) ([]Account, error) {
	return append([]Account(nil), r.byGroup[groupID]...), nil
}

func (r *anthropicFallbackAccountRepo) ListSchedulableByGroupIDAndPlatforms(_ context.Context, groupID int64, _ []string) ([]Account, error) {
	return append([]Account(nil), r.byGroup[groupID]...), nil
}

func (r *anthropicFallbackAccountRepo) GetByID(_ context.Context, accountID int64) (*Account, error) {
	for _, accounts := range r.byGroup {
		for i := range accounts {
			if accounts[i].ID == accountID {
				account := accounts[i]
				return &account, nil
			}
		}
	}
	return nil, ErrAccountNotFound
}

type anthropicFallbackGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
}

type anthropicFallbackStickyCache struct {
	GatewayCache
	bindings map[string]int64
}

func (c *anthropicFallbackStickyCache) GetSessionAccountID(_ context.Context, _ int64, sessionHash string) (int64, error) {
	if accountID, ok := c.bindings[sessionHash]; ok {
		return accountID, nil
	}
	return 0, ErrStickySessionNotFound
}

func (c *anthropicFallbackStickyCache) SetSessionAccountID(_ context.Context, _ int64, sessionHash string, accountID int64, _ time.Duration) error {
	if c.bindings == nil {
		c.bindings = make(map[string]int64)
	}
	c.bindings[sessionHash] = accountID
	return nil
}

func (*anthropicFallbackStickyCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (c *anthropicFallbackStickyCache) DeleteSessionAccountID(_ context.Context, _ int64, sessionHash string) error {
	delete(c.bindings, sessionHash)
	return nil
}

func (r *anthropicFallbackGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	group := r.groups[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	return group, nil
}

func (r *anthropicFallbackGroupRepo) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	group := r.groups[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	return group, nil
}

func TestGatewayAnthropicFallbackPoolSelectsTargetAccount(t *testing.T) {
	t.Parallel()

	sourceID, fallbackID := int64(100), int64(200)
	source := &Group{
		ID:              sourceID,
		Platform:        PlatformAnthropic,
		Status:          StatusActive,
		FallbackGroupID: &fallbackID,
	}
	fallback := &Group{
		ID:             fallbackID,
		Platform:       PlatformAnthropic,
		Status:         StatusActive,
		IsFallbackPool: true,
	}
	accountRepo := &anthropicFallbackAccountRepo{
		byGroup: map[int64][]Account{
			sourceID: nil,
			fallbackID: {{ID: 901, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true,
				// 兜底取号要求成本已声明，未声明的号会被 gatewayFallbackPoolRejectReason 拒掉。
				RateMultiplier: gatewayFallbackFloatPtr(1)}},
		},
	}
	groupRepo := &anthropicFallbackGroupRepo{
		groups: map[int64]*Group{sourceID: source, fallbackID: fallback},
	}
	svc := &GatewayService{
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	account, err := svc.SelectAccountForModelWithExclusions(
		context.Background(),
		&sourceID,
		"",
		"",
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(901), account.ID)
}

func TestGatewayAnthropicFallbackPoolKeepsPrimaryPoolPriority(t *testing.T) {
	t.Parallel()

	sourceID, fallbackID := int64(101), int64(201)
	source := &Group{
		ID:              sourceID,
		Platform:        PlatformAnthropic,
		Status:          StatusActive,
		FallbackGroupID: &fallbackID,
	}
	fallback := &Group{
		ID:             fallbackID,
		Platform:       PlatformAnthropic,
		Status:         StatusActive,
		IsFallbackPool: true,
	}
	accountRepo := &anthropicFallbackAccountRepo{
		byGroup: map[int64][]Account{
			sourceID: {{ID: 911, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: 100}},
			// 即使兜底账号的优先级更高，也必须等主力池官方选择流程耗尽后才能使用。
			fallbackID: {{ID: 921, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: -100,
				RateMultiplier: gatewayFallbackFloatPtr(1)}},
		},
	}
	groupRepo := &anthropicFallbackGroupRepo{
		groups: map[int64]*Group{sourceID: source, fallbackID: fallback},
	}
	svc := &GatewayService{
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	account, err := svc.SelectAccountForModelWithExclusions(
		context.Background(),
		&sourceID,
		"",
		"",
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(911), account.ID)
}

func TestGatewayAnthropicFallbackPoolPreservesStickySession(t *testing.T) {
	t.Parallel()

	sourceID, fallbackID := int64(102), int64(202)
	source := &Group{
		ID:              sourceID,
		Platform:        PlatformAnthropic,
		Status:          StatusActive,
		FallbackGroupID: &fallbackID,
	}
	fallback := &Group{
		ID:             fallbackID,
		Platform:       PlatformAnthropic,
		Status:         StatusActive,
		IsFallbackPool: true,
	}
	stickyID, otherID := int64(931), int64(932)
	accountRepo := &anthropicFallbackAccountRepo{
		byGroup: map[int64][]Account{
			sourceID: nil,
			fallbackID: {
				{ID: stickyID, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: 100,
					AccountGroups: []AccountGroup{{GroupID: fallbackID}}, RateMultiplier: gatewayFallbackFloatPtr(1)},
				{ID: otherID, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: -100,
					AccountGroups: []AccountGroup{{GroupID: fallbackID}}, RateMultiplier: gatewayFallbackFloatPtr(1)},
			},
		},
	}
	groupRepo := &anthropicFallbackGroupRepo{
		groups: map[int64]*Group{sourceID: source, fallbackID: fallback},
	}
	cache := &anthropicFallbackStickyCache{bindings: map[string]int64{"session-primary": stickyID}}
	svc := &GatewayService{
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		cache:       cache,
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	account, err := svc.SelectAccountForModelWithExclusions(
		context.Background(),
		&sourceID,
		"session-primary",
		"",
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, stickyID, account.ID)
}

func TestGatewayAnthropicFallbackPoolStopsCycle(t *testing.T) {
	t.Parallel()

	sourceID, fallbackID := int64(110), int64(210)
	source := &Group{
		ID:              sourceID,
		Platform:        PlatformAnthropic,
		Status:          StatusActive,
		FallbackGroupID: &fallbackID,
	}
	fallback := &Group{
		ID:              fallbackID,
		Platform:        PlatformAnthropic,
		Status:          StatusActive,
		IsFallbackPool:  true,
		FallbackGroupID: &sourceID,
	}
	accountRepo := &anthropicFallbackAccountRepo{
		byGroup: map[int64][]Account{sourceID: nil, fallbackID: nil},
	}
	groupRepo := &anthropicFallbackGroupRepo{
		groups: map[int64]*Group{sourceID: source, fallbackID: fallback},
	}
	svc := &GatewayService{
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	_, err := svc.SelectAccountForModelWithExclusions(
		context.Background(),
		&sourceID,
		"",
		"",
		nil,
	)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestGatewayAnthropicFallbackPoolLoadAwareEntrySelectsTargetAccount(t *testing.T) {
	t.Parallel()

	sourceID, fallbackID := int64(120), int64(220)
	source := &Group{
		ID:              sourceID,
		Platform:        PlatformAnthropic,
		Status:          StatusActive,
		FallbackGroupID: &fallbackID,
	}
	fallback := &Group{
		ID:             fallbackID,
		Platform:       PlatformAnthropic,
		Status:         StatusActive,
		IsFallbackPool: true,
	}
	accountRepo := &anthropicFallbackAccountRepo{
		byGroup: map[int64][]Account{
			sourceID: nil,
			fallbackID: {{ID: 902, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true,
				// 兜底取号要求成本已声明，未声明的号会被 gatewayFallbackPoolRejectReason 拒掉。
				RateMultiplier: gatewayFallbackFloatPtr(1)}},
		},
	}
	groupRepo := &anthropicFallbackGroupRepo{
		groups: map[int64]*Group{sourceID: source, fallbackID: fallback},
	}
	svc := &GatewayService{
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	result, err := svc.SelectAccountWithLoadAwareness(
		context.Background(),
		&sourceID,
		"",
		"",
		nil,
		"",
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Account)
	require.Equal(t, int64(902), result.Account.ID)
}

// 兜底取号时，成本从未声明过的账号必须被拒。这条规则此前只有 OpenAI 侧有，
// Anthropic/Gemini 侧漏了，等于兜底池可以派出成本未知的号去顶目标分组的利润。
func TestGatewayFallbackPoolRejectsUndeclaredRate(t *testing.T) {
	t.Parallel()

	fallbackCtx := withGatewayFallbackPoolSourcing(context.Background())

	cases := []struct {
		name     string
		ctx      context.Context
		account  *Account
		rejected bool
	}{
		{
			name:     "非兜底模式：未声明倍率也放行",
			ctx:      context.Background(),
			account:  &Account{},
			rejected: false,
		},
		{
			name:     "兜底模式：未声明倍率被拒",
			ctx:      fallbackCtx,
			account:  &Account{},
			rejected: true,
		},
		{
			// 倍率 0 是合法声明（该账号计费为 0），不是「未声明」。
			name:     "兜底模式：倍率为 0 属于已声明，放行",
			ctx:      fallbackCtx,
			account:  &Account{RateMultiplier: gatewayFallbackFloatPtr(0)},
			rejected: false,
		},
		{
			name:     "兜底模式：倍率为负被拒",
			ctx:      fallbackCtx,
			account:  &Account{RateMultiplier: gatewayFallbackFloatPtr(-1)},
			rejected: true,
		},
		{
			name:     "账号为 nil 时不判定",
			ctx:      fallbackCtx,
			account:  nil,
			rejected: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := gatewayFallbackPoolRejectReason(tc.ctx, tc.account)
			if got := reason != ""; got != tc.rejected {
				t.Fatalf("rejected = %v（reason=%q），期望 %v", got, reason, tc.rejected)
			}
			if tc.rejected && reason != fallbackFilterReasonUndeclaredRate {
				t.Fatalf("拒绝原因应为 %q，实际 %q", fallbackFilterReasonUndeclaredRate, reason)
			}
		})
	}
}

// 平台白名单：GatewayService 只负责 anthropic / gemini，OpenAI 与 Grok 由
// OpenAIGatewayService 自己那条链路兜底，这里必须不接管，否则同一请求会被两套逻辑
// 各推进一次。
func TestGatewayPlatformSupportsFallbackPool(t *testing.T) {
	t.Parallel()

	for platform, want := range map[string]bool{
		PlatformAnthropic:   true,
		PlatformGemini:      true,
		PlatformOpenAI:      false,
		PlatformGrok:        false,
		PlatformAntigravity: false,
		PlatformComposite:   false,
		"":                  false,
	} {
		if got := gatewayPlatformSupportsFallbackPool(platform); got != want {
			t.Fatalf("gatewayPlatformSupportsFallbackPool(%q) = %v, 期望 %v", platform, got, want)
		}
	}
}

func gatewayFallbackFloatPtr(v float64) *float64 { return &v }
