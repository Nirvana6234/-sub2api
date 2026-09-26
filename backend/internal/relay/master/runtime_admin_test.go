package master_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 根证书轮换：预备 → 所有在服务节点拿到新指纹才能启用 → 启用满 25 小时才能停用旧根。
func TestRootRotationWaitsForEveryServingNode(t *testing.T) {
	ctx := master.WithSourceIP(context.Background(), "203.0.113.9")
	h := newRuntime(t, nil)
	clock := time.Now()
	master.SetRuntimeClock(h.runtime, func() time.Time { return clock })

	_, err := h.runtime.StageKey(ctx, 7, keystore.PurposeRootCA)
	require.ErrorIs(t, err, master.ErrRelayNotRunning)

	st, err := h.runtime.SetEnabled(ctx, 7, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)
	oldFP := st.RootFingerprints[0]

	n, err := h.store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	require.NoError(t, h.store.Activate(ctx, n.ID, master.Activation{PublicDomain: "r.example.com", At: time.Now()}))
	_, err = h.runtime.Publisher().FetchConfig(ctx, n.ID, "")
	require.NoError(t, err)

	staged, err := h.runtime.StageKey(ctx, 7, keystore.PurposeRootCA)
	require.NoError(t, err)
	_, err = h.runtime.StageKey(ctx, 7, keystore.PurposeRootCA)
	require.ErrorIs(t, err, keystore.ErrAlreadyStaged, "one staged root at a time")

	roots, err := h.runtime.Keys(ctx, keystore.PurposeRootCA)
	require.NoError(t, err)
	require.Len(t, roots, 2)
	require.True(t, roots[0].Signing)
	require.Equal(t, []int64{n.ID}, roots[1].PendingNodeIDs, "the node fetched before the new root existed")

	// 节点还没拿到新指纹：切换签发会让它连不上，拒绝。
	err = h.runtime.ActivateKey(ctx, 7, keystore.PurposeRootCA, staged.Version)
	require.ErrorIs(t, err, master.ErrKeyNotDelivered)
	var nd *master.KeyNotDeliveredError
	require.ErrorAs(t, err, &nd)
	require.Equal(t, []int64{n.ID}, nd.NodeIDs)
	require.NoError(t, hello(t, h.runtime.Status()), "still signing with the old root")

	// 节点拉到新配置（含新指纹）后可以启用；主节点证书改由新根签发。
	snap, err := h.runtime.Publisher().FetchConfig(ctx, n.ID, "")
	require.NoError(t, err)
	require.Contains(t, snap.RootFingerprints, staged.Fingerprint)
	require.ErrorIs(t, h.runtime.ActivateKey(ctx, 7, keystore.PurposeRootCA, roots[0].Version), master.ErrKeyNotStaged)
	require.NoError(t, h.runtime.ActivateKey(ctx, 7, keystore.PurposeRootCA, staged.Version))

	st = h.runtime.Status()
	st.RootFingerprints = []string{staged.Fingerprint}
	require.NoError(t, hello(t, st), "a node pinning only the new root trusts the master")
	st.RootFingerprints = []string{oldFP}
	require.Error(t, hello(t, st), "the master certificate is no longer signed by the old root")

	// 停用：签发中的不能停；旧根要等新根签满 25 小时。
	require.ErrorIs(t, h.runtime.RetireKey(ctx, 7, keystore.PurposeRootCA, staged.Version), master.ErrKeyInUse)
	require.ErrorIs(t, h.runtime.RetireKey(ctx, 7, keystore.PurposeRootCA, roots[0].Version), master.ErrKeyRetireTooEarly)
	clock = clock.Add(26 * time.Hour)
	require.NoError(t, h.runtime.RetireKey(ctx, 7, keystore.PurposeRootCA, roots[0].Version))
	require.ErrorIs(t, h.runtime.RetireKey(ctx, 7, keystore.PurposeRootCA, roots[0].Version), master.ErrKeyNotFound)
	roots, err = h.runtime.Keys(ctx, keystore.PurposeRootCA)
	require.NoError(t, err)
	require.Len(t, roots, 1)
	require.Equal(t, staged.Fingerprint, roots[0].Fingerprint)

	actions := map[string]master.AuditEntry{}
	for _, a := range h.store.Audits() {
		actions[a.Action] = a
	}
	for _, action := range []string{master.AuditRelaySwitched, master.AuditKeyStaged, master.AuditKeyActivated, master.AuditKeyRetired} {
		a, ok := actions[action]
		require.True(t, ok, action)
		require.Equal(t, int64(7), a.ActorUserID, action)
		require.Equal(t, "203.0.113.9", a.SourceIP, action)
	}
}

// 预备中的根证书随时可以放弃，放弃后可以重新预备。
func TestStagedRootCanBeAbandoned(t *testing.T) {
	ctx := context.Background()
	h := newRuntime(t, nil)
	_, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	staged, err := h.runtime.StageKey(ctx, 1, keystore.PurposeRootCA)
	require.NoError(t, err)
	require.NoError(t, h.runtime.RetireKey(ctx, 1, keystore.PurposeRootCA, staged.Version))
	_, err = h.runtime.StageKey(ctx, 1, keystore.PurposeRootCA)
	require.NoError(t, err)
}

func TestGeneralConfigIsValidatedAndAudited(t *testing.T) {
	ctx := master.WithSourceIP(context.Background(), "198.51.100.1")
	h := newRuntime(t, nil)

	bad := 101
	_, err := h.runtime.SetGeneralConfig(ctx, 3, master.GeneralConfig{MasterRatioPercent: &bad})
	require.ErrorIs(t, err, master.ErrInvalidGeneralConfig)

	zero := 0
	got, err := h.runtime.SetGeneralConfig(ctx, 3, master.GeneralConfig{MasterRatioPercent: &zero})
	require.NoError(t, err, "the general config can be edited while relay is off")
	require.Equal(t, 0, *got.MasterRatioPercent)
	require.Equal(t, master.APIKeyNodeRuleAssigned, got.APIKeyNodeRule, "defaults are filled in")

	audits := h.store.Audits()
	require.NotEmpty(t, audits)
	last := audits[len(audits)-1]
	require.Equal(t, master.AuditGeneralConfigChanged, last.Action)
	require.Equal(t, int64(3), last.ActorUserID)
	require.Equal(t, "198.51.100.1", last.SourceIP)
}

// 管理员的节点操作也带上来源 IP。
func TestNodeAdminAuditCarriesSourceIP(t *testing.T) {
	ctx := master.WithSourceIP(context.Background(), "192.0.2.4")
	h := newRuntime(t, nil)
	_, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	n, err := h.store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	require.NoError(t, h.runtime.Nodes().Load(ctx))
	require.ErrorIs(t, h.runtime.ActivateNode(ctx, n.ID, "other", master.Activation{PublicDomain: "r.example.com", ActorUserID: 1}), master.ErrFingerprintMismatch)
	require.NoError(t, h.runtime.RejectNode(ctx, n.ID, 1))
	audits := h.store.Audits()
	last := audits[len(audits)-1]
	require.Equal(t, master.AuditRejected, last.Action)
	require.Equal(t, "192.0.2.4", last.SourceIP)
}

// 主从分流运行时才订阅业务层改动；关掉即取消，开关关闭时不挂任何东西。
func TestRuntimeSubscribesToAccessChangesOnlyWhileRunning(t *testing.T) {
	ctx := context.Background()
	hub := service.NewAccessChangeHub()
	rt := master.NewRuntime(master.RuntimeDeps{Config: testRelayConfig(t), Store: master.NewMemoryStore(), Settings: newMemSettings(), AccessChanges: hub, Users: noUsers{}})
	t.Cleanup(rt.Close)
	rt.Init(ctx)
	require.Equal(t, 0, hub.SubscriberCount())

	_, err := rt.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, 2, hub.SubscriberCount(), "cache invalidation and ticket revocation")
	_, err = rt.SetEnabled(ctx, 1, false)
	require.NoError(t, err)
	require.Equal(t, 0, hub.SubscriberCount())
}

type noUsers struct{}

func (noUsers) GetByID(context.Context, int64) (*service.User, error) {
	return nil, service.ErrUserNotFound
}
