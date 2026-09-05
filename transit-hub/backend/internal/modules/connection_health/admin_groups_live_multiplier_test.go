package connection_health

import (
	"context"
	"errors"
	"testing"

	"transithub/backend/internal/modules/my_sites"
)

// liveSnapshotMySites 在 fakeMySitesReader 之上补齐实时倍率快照读取能力。
type liveSnapshotMySites struct {
	fakeMySitesReader
	snapshots map[string][]my_sites.UpstreamKeyGroupSnapshot
	err       error
	calls     int
}

func (f *liveSnapshotMySites) ListUpstreamKeyGroupSnapshotsForWorkspace(
	_ context.Context, _ string, _ string, siteID string,
) ([]my_sites.UpstreamKeyGroupSnapshot, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshots[siteID], nil
}

func liveMultiplierConnection(keyID string) my_sites.RealConnection {
	return my_sites.RealConnection{
		UserID:                  "user1",
		WorkspaceAdminAccountID: "ws1",
		AdminAccountID:          "99",
		AdminPlatform:           "sub2api",
		UpstreamSiteID:          "site-1",
		UpstreamKeyID:           keyID,
		UpstreamGroupID:         "15",
		UpstreamGroupName:       "激励gpt",
	}
}

func floatRef(v float64) *float64 { return &v }

// 倍率快照采集器每小时才跑一次，刚配好的渠道在下一个整点之前没有任何历史行。
// 但它的倍率此刻已经完全可解析（渠道 → key → 分组 → 分组倍率），页面不该让
// 运营等一小时、还显示成"关联后展示倍率"让人以为关联没配好。
func TestUpstreamKeyGroupsFallsBackToLiveSnapshotWhenHistoryEmpty(t *testing.T) {
	repo := newFakeRepository()
	mySites := &liveSnapshotMySites{
		fakeMySitesReader: fakeMySitesReader{connections: []my_sites.RealConnection{liveMultiplierConnection("5585")}},
		snapshots: map[string][]my_sites.UpstreamKeyGroupSnapshot{
			"site-1": {{KeyID: "5585", GroupID: "15", GroupName: "激励gpt", Multiplier: floatRef(0.06)}},
		},
	}
	svc := &Service{repo: repo, mySites: mySites, accounts: fakeAdminAccountResolver{id: "ws1"}}

	result := svc.upstreamKeyGroupsByAdminAccount(context.Background(), "user1", "ws1", "sub2api")

	info, ok := result["99"]
	if !ok {
		t.Fatalf("expected the freshly configured channel to resolve a multiplier, got %+v", result)
	}
	if info.multiplier == nil || *info.multiplier != 0.06 {
		t.Fatalf("multiplier = %+v, want 0.06", info.multiplier)
	}
	if info.source != "detected" {
		t.Fatalf("source = %q, want detected (值确实来自对上游的观测，只是尚未落历史)", info.source)
	}
	if info.name != "激励gpt" {
		t.Fatalf("group name = %q, want 激励gpt", info.name)
	}
}

// 同一站点下的多个渠道必须共用一次上游查询，否则站点里每多一个渠道就多打一次接口。
func TestUpstreamKeyGroupsCachesLiveSnapshotPerSite(t *testing.T) {
	first := liveMultiplierConnection("5585")
	second := liveMultiplierConnection("5586")
	second.AdminAccountID = "100"

	mySites := &liveSnapshotMySites{
		fakeMySitesReader: fakeMySitesReader{connections: []my_sites.RealConnection{first, second}},
		snapshots: map[string][]my_sites.UpstreamKeyGroupSnapshot{
			"site-1": {
				{KeyID: "5585", GroupID: "15", GroupName: "激励gpt", Multiplier: floatRef(0.06)},
				{KeyID: "5586", GroupID: "20", GroupName: "gpt 蒸馏分组", Multiplier: floatRef(0.07)},
			},
		},
	}
	svc := &Service{repo: newFakeRepository(), mySites: mySites, accounts: fakeAdminAccountResolver{id: "ws1"}}

	result := svc.upstreamKeyGroupsByAdminAccount(context.Background(), "user1", "ws1", "sub2api")

	if len(result) != 2 {
		t.Fatalf("expected both channels resolved, got %+v", result)
	}
	if mySites.calls != 1 {
		t.Fatalf("expected a single upstream lookup per site, got %d", mySites.calls)
	}
}

// 上游读取失败时不得反复重试，且必须留下可检索的痕迹（此前这条路径完全静默）。
func TestUpstreamKeyGroupsDoesNotRetryFailedSite(t *testing.T) {
	second := liveMultiplierConnection("5586")
	second.AdminAccountID = "100"
	mySites := &liveSnapshotMySites{
		fakeMySitesReader: fakeMySitesReader{connections: []my_sites.RealConnection{liveMultiplierConnection("5585"), second}},
		err:               errors.New("upstream unreachable"),
	}
	svc := &Service{repo: newFakeRepository(), mySites: mySites, accounts: fakeAdminAccountResolver{id: "ws1"}}

	result := svc.upstreamKeyGroupsByAdminAccount(context.Background(), "user1", "ws1", "sub2api")

	if len(result) != 0 {
		t.Fatalf("failed lookups must not fabricate multipliers, got %+v", result)
	}
	if mySites.calls != 1 {
		t.Fatalf("failed site must be retried at most once per request, got %d calls", mySites.calls)
	}
}

// 没有倍率的快照不能被当成 0：宁可显示为待关联，也不能把未知成本记成免费。
func TestUpstreamKeyGroupsIgnoresSnapshotWithoutMultiplier(t *testing.T) {
	mySites := &liveSnapshotMySites{
		fakeMySitesReader: fakeMySitesReader{connections: []my_sites.RealConnection{liveMultiplierConnection("5585")}},
		snapshots: map[string][]my_sites.UpstreamKeyGroupSnapshot{
			"site-1": {{KeyID: "5585", GroupID: "15", GroupName: "激励gpt"}},
		},
	}
	svc := &Service{repo: newFakeRepository(), mySites: mySites, accounts: fakeAdminAccountResolver{id: "ws1"}}

	if result := svc.upstreamKeyGroupsByAdminAccount(context.Background(), "user1", "ws1", "sub2api"); len(result) != 0 {
		t.Fatalf("snapshot without a multiplier must not resolve, got %+v", result)
	}
}
