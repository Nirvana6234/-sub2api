package service

import (
	"context"
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type fallbackGroupRepoStub struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *fallbackGroupRepoStub) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	group := r.groups[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	cloned := *group
	return &cloned, nil
}

func fallbackTestService(groups ...*Group) *OpenAIGatewayService {
	byID := make(map[int64]*Group, len(groups))
	for _, group := range groups {
		if group == nil {
			continue
		}
		cloned := *group
		byID[group.ID] = &cloned
	}
	groupRepo := &fallbackGroupRepoStub{groups: byID}
	return &OpenAIGatewayService{
		schedulerSnapshot: NewSchedulerSnapshotService(nil, nil, nil, groupRepo, nil),
	}
}

func fallbackIDPtr(id int64) *int64 { return &id }

// 兜底比常规调度只多一条规则：成本没被声明过的账号不参与兜底。
//
// 这条规则的方向和常规路径是相反的 —— 常规对未声明成本的账号是「放行并告警」，
// 因为拦掉它等于让本该被服务的请求直接失败。而兜底账号是从别的分组临时借来的，
// 拿不知道成本的号去顶，等于用未知成本换未知收益，代价还落在被兜底分组的利润上。
func TestOpenAIFallbackPoolRejectReason(t *testing.T) {
	fallbackCtx := withOpenAIFallbackPoolSourcing(context.Background())

	cases := []struct {
		name     string
		ctx      context.Context
		account  *Account
		rejected bool
	}{
		{
			name:     "非兜底模式下不干预任何账号",
			ctx:      context.Background(),
			account:  &Account{RateMultiplierUndeclared: true},
			rejected: false,
		},
		{
			name:     "兜底模式：成本未声明的账号被拒",
			ctx:      fallbackCtx,
			account:  &Account{RateMultiplierUndeclared: true},
			rejected: true,
		},
		{
			name:     "兜底模式：成本已声明的账号放行",
			ctx:      fallbackCtx,
			account:  &Account{RateMultiplier: floatPtr(0.5)},
			rejected: false,
		},
		{
			// 倍率缺失属于坏数据（DB 列非空且有默认值，nil 只可能来自缓存漏字段），
			// 与「未声明」一样不能拿去兜底。
			name:     "兜底模式：倍率缺失的账号被拒",
			ctx:      fallbackCtx,
			account:  &Account{RateMultiplier: nil},
			rejected: true,
		},
		{
			name:     "兜底模式：倍率为 NaN 的账号被拒",
			ctx:      fallbackCtx,
			account:  &Account{RateMultiplier: floatPtr(math.NaN())},
			rejected: true,
		},
		{
			name:     "兜底模式：倍率为负的账号被拒",
			ctx:      fallbackCtx,
			account:  &Account{RateMultiplier: floatPtr(-1)},
			rejected: true,
		},
		{
			// 倍率 0 是合法声明（该账号计费为 0），不是「未声明」。
			name:     "兜底模式：倍率为 0 属于已声明，放行",
			ctx:      fallbackCtx,
			account:  &Account{RateMultiplier: floatPtr(0)},
			rejected: false,
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
			reason := openAIFallbackPoolRejectReason(tc.ctx, tc.account)
			if got := reason != ""; got != tc.rejected {
				t.Fatalf("rejected = %v（reason=%q），期望 %v", got, reason, tc.rejected)
			}
			if tc.rejected && reason != openAIFallbackFilterReasonUndeclaredRate {
				t.Fatalf("拒绝原因应为 %q，实际 %q", openAIFallbackFilterReasonUndeclaredRate, reason)
			}
		})
	}
}

// 兜底标记必须是「取号来源」的开关，不能泄漏成分组身份的一部分：
// 利润门与计费始终挂在原分组上，标记只切换候选从哪来。
func TestOpenAIFallbackPoolSourcingMarker(t *testing.T) {
	base := context.Background()
	if isOpenAIFallbackPoolSourcing(base) {
		t.Fatal("裸 context 不应处于兜底取号模式")
	}
	if !isOpenAIFallbackPoolSourcing(withOpenAIFallbackPoolSourcing(base)) {
		t.Fatal("标记后应处于兜底取号模式")
	}
	// nil context 出现在测试替身和部分后台任务里，不能 panic。
	if isOpenAIFallbackPoolSourcing(nil) {
		t.Fatal("nil context 不应被判定为兜底取号模式")
	}
}

// 防递归：兜底自己失败时不能再兜底自己，否则调度会无限套娃。
// 这里验证标记是幂等的 —— 已在兜底模式下再标记一次仍是兜底模式，
// 调度侧据此的 !isOpenAIFallbackPoolSourcing 判断才能可靠地终止递归。
func TestOpenAIFallbackPoolSourcingIsIdempotent(t *testing.T) {
	once := withOpenAIFallbackPoolSourcing(context.Background())
	twice := withOpenAIFallbackPoolSourcing(once)
	if !isOpenAIFallbackPoolSourcing(twice) {
		t.Fatal("重复标记后应仍处于兜底取号模式")
	}
}

func TestNextOpenAIFallbackGroupFollowsPerGroupChain(t *testing.T) {
	const (
		groupA = int64(10)
		groupB = int64(20)
		groupC = int64(30)
	)
	svc := fallbackTestService(
		&Group{ID: groupA, Platform: PlatformOpenAI, Status: StatusActive, FallbackGroupID: fallbackIDPtr(groupB)},
		&Group{ID: groupB, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true, FallbackGroupID: fallbackIDPtr(groupC)},
		&Group{ID: groupC, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
	)

	ctx, nextID := svc.nextOpenAIFallbackGroup(context.Background(), fallbackIDPtr(groupA), PlatformOpenAI)
	if nextID == nil || *nextID != groupB {
		t.Fatalf("第一跳应到 B，实际 %#v", nextID)
	}
	ctx, nextID = svc.nextOpenAIFallbackGroup(ctx, nextID, PlatformOpenAI)
	if nextID == nil || *nextID != groupC {
		t.Fatalf("第二跳应到 C，实际 %#v", nextID)
	}
	_, nextID = svc.nextOpenAIFallbackGroup(ctx, nextID, PlatformOpenAI)
	if nextID != nil {
		t.Fatalf("C 未配置兜底，应停止，实际 %#v", nextID)
	}
}

func TestNextOpenAIFallbackGroupRejectsInvalidTarget(t *testing.T) {
	cases := []struct {
		name   string
		target Group
	}{
		{
			name:   "目标未标记兜底池",
			target: Group{ID: 20, Platform: PlatformOpenAI, Status: StatusActive},
		},
		{
			name:   "目标平台不一致",
			target: Group{ID: 20, Platform: PlatformGrok, Status: StatusActive, IsFallbackPool: true},
		},
		{
			name:   "目标未启用",
			target: Group{ID: 20, Platform: PlatformOpenAI, Status: StatusDisabled, IsFallbackPool: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := fallbackTestService(
				&Group{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, FallbackGroupID: fallbackIDPtr(20)},
				&tc.target,
			)
			_, nextID := svc.nextOpenAIFallbackGroup(context.Background(), fallbackIDPtr(10), PlatformOpenAI)
			if nextID != nil {
				t.Fatalf("非法兜底目标应被拒绝，实际 %#v", nextID)
			}
		})
	}
}

func TestNextOpenAIFallbackGroupStopsCycleAndMaxHops(t *testing.T) {
	t.Run("A 到 B 后 B 不能再兜回 A", func(t *testing.T) {
		svc := fallbackTestService(
			&Group{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, FallbackGroupID: fallbackIDPtr(20)},
			&Group{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true, FallbackGroupID: fallbackIDPtr(10)},
		)
		ctx, nextID := svc.nextOpenAIFallbackGroup(context.Background(), fallbackIDPtr(10), PlatformOpenAI)
		if nextID == nil || *nextID != 20 {
			t.Fatalf("第一跳应到 B，实际 %#v", nextID)
		}
		_, nextID = svc.nextOpenAIFallbackGroup(ctx, nextID, PlatformOpenAI)
		if nextID != nil {
			t.Fatalf("循环兜底应停止，实际 %#v", nextID)
		}
	})

	t.Run("最多三跳", func(t *testing.T) {
		svc := fallbackTestService(
			&Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, FallbackGroupID: fallbackIDPtr(2)},
			&Group{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true, FallbackGroupID: fallbackIDPtr(3)},
			&Group{ID: 3, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true, FallbackGroupID: fallbackIDPtr(4)},
			&Group{ID: 4, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true, FallbackGroupID: fallbackIDPtr(5)},
			&Group{ID: 5, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
		)
		ctx := context.Background()
		current := fallbackIDPtr(1)
		for _, want := range []int64{2, 3, 4} {
			var nextID *int64
			ctx, nextID = svc.nextOpenAIFallbackGroup(ctx, current, PlatformOpenAI)
			if nextID == nil || *nextID != want {
				t.Fatalf("应跳到 %d，实际 %#v", want, nextID)
			}
			current = nextID
		}
		_, nextID := svc.nextOpenAIFallbackGroup(ctx, current, PlatformOpenAI)
		if nextID != nil {
			t.Fatalf("超过最大跳数后应停止，实际 %#v", nextID)
		}
	})
}

func TestOpenAIFallbackKeepsOriginalProfitGate(t *testing.T) {
	const (
		groupA = int64(10)
		groupB = int64(20)
	)
	groupAConfig := &Group{
		ID:                   groupA,
		Platform:             PlatformOpenAI,
		Status:               StatusActive,
		RateMultiplier:       1,
		ProfitControlEnabled: true,
		ProfitMinMargin:      0.2,
		FallbackGroupID:      fallbackIDPtr(groupB),
	}
	svc := fallbackTestService(
		groupAConfig,
		&Group{ID: groupB, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true, RateMultiplier: 9, ProfitControlEnabled: true},
	)
	ctx := context.WithValue(context.Background(), ctxkey.Group, groupAConfig)
	ctx = svc.withOpenAIProfitControlGate(ctx, fallbackIDPtr(groupA))
	ctx, fallbackGroupID := svc.nextOpenAIFallbackGroup(ctx, fallbackIDPtr(groupA), PlatformOpenAI)
	if fallbackGroupID == nil || *fallbackGroupID != groupB {
		t.Fatalf("应进入 B 兜底，实际 %#v", fallbackGroupID)
	}
	ctx = svc.withOpenAIProfitControlGate(ctx, fallbackGroupID)
	gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	if gate == nil {
		t.Fatal("兜底上下文应保留原分组利润门")
	}
	if gate.groupID != groupA {
		t.Fatalf("利润门应仍属于源分组 A，实际 group_id=%d", gate.groupID)
	}
	if gate.threshold != 0.8 {
		t.Fatalf("利润门阈值应沿用 A 的配置 0.8，实际 %.4f", gate.threshold)
	}
}

func TestOpenAIFallbackKeepsOriginalPrivacyRequirement(t *testing.T) {
	const (
		groupA = int64(10)
		groupB = int64(20)
	)
	groupAConfig := &Group{
		ID:                groupA,
		Platform:          PlatformOpenAI,
		Status:            StatusActive,
		RequirePrivacySet: true,
		FallbackGroupID:   fallbackIDPtr(groupB),
	}
	svc := fallbackTestService(
		groupAConfig,
		&Group{ID: groupB, Platform: PlatformOpenAI, Status: StatusActive, IsFallbackPool: true},
	)
	ctx := context.WithValue(context.Background(), ctxkey.Group, groupAConfig)
	ctx = context.WithValue(ctx, openAIGroupPrivacyRequirementContextKey{}, openAIGroupPrivacyRequirement{
		groupID:  groupA,
		required: true,
	})
	ctx, fallbackGroupID := svc.nextOpenAIFallbackGroup(ctx, fallbackIDPtr(groupA), PlatformOpenAI)
	if fallbackGroupID == nil || *fallbackGroupID != groupB {
		t.Fatalf("应进入 B 兜底，实际 %#v", fallbackGroupID)
	}
	ctx = svc.withOpenAIGroupPrivacyRequirement(ctx, fallbackGroupID)
	if !svc.openAIGroupRequiresPrivacySet(ctx, fallbackGroupID) {
		t.Fatal("兜底上下文应保留源分组 A 的隐私账号要求")
	}
}
