package master_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/stretchr/testify/require"
)

// startWithNode 打开主从分流并放一台已激活、拉过一次配置的节点。
func startWithNode(t *testing.T) (*runtimeHarness, *time.Time, int64) {
	t.Helper()
	ctx := context.Background()
	h := newRuntime(t, nil)
	clock := time.Now()
	master.SetRuntimeClock(h.runtime, func() time.Time { return clock })
	st, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)
	n, err := h.store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	require.NoError(t, h.store.Activate(ctx, n.ID, master.Activation{PublicDomain: "r.example.com", At: time.Now()}))
	fetch(t, h, n.ID)
	return h, &clock, n.ID
}

// fetch 模拟节点拉配置，返回它按快照建出的票据验证器。
func fetch(t *testing.T, h *runtimeHarness, nodeID int64) *sign.TicketVerifier {
	t.Helper()
	snap, err := h.runtime.Publisher().FetchConfig(context.Background(), nodeID, "")
	require.NoError(t, err)
	pub, err := sign.NewPublicKeys(snap.TicketPublicKeys)
	require.NoError(t, err)
	return &sign.TicketVerifier{Keys: func() *sign.PublicKeys { return pub }, NodeID: func() int64 { return nodeID }}
}

func TestSigningNeedsARunningRelay(t *testing.T) {
	h := newRuntime(t, nil)
	_, _, err := h.runtime.IssueTicket(1, 2, 3)
	require.ErrorIs(t, err, master.ErrSigningUnavailable)
	_, _, err = h.runtime.IssueVoucher(&relayv1.Voucher{NodeId: 2, UserId: 1})
	require.ErrorIs(t, err, master.ErrSigningUnavailable)
}

// 票据公钥随配置下发；轮换时新公钥送达所有在服务节点才能启用；
// 旧公钥等新版本签满"票据有效期 + 偏差"才能停用，停用后旧票据被拒。
func TestTicketKeyRotation(t *testing.T) {
	ctx := context.Background()
	h, clock, nodeID := startWithNode(t)
	v := fetch(t, h, nodeID)
	oldTicket, _, err := h.runtime.IssueTicket(42, nodeID, 7)
	require.NoError(t, err)
	v.Now = func() time.Time { return *clock }
	_, err = v.Verify(oldTicket)
	require.NoError(t, err, "the node verifies tickets with the public keys from its config snapshot")

	keys, err := h.runtime.Keys(ctx, keystore.PurposeTicket)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	oldVersion := keys[0].Version

	staged, err := h.runtime.StageKey(ctx, 1, keystore.PurposeTicket)
	require.NoError(t, err)
	keys, err = h.runtime.Keys(ctx, keystore.PurposeTicket)
	require.NoError(t, err)
	require.Equal(t, []int64{nodeID}, keys[1].PendingNodeIDs)
	require.ErrorIs(t, h.runtime.ActivateKey(ctx, 1, keystore.PurposeTicket, staged.Version), master.ErrKeyNotDelivered)

	v = fetch(t, h, nodeID)
	v.Now = func() time.Time { return *clock }
	require.NoError(t, h.runtime.ActivateKey(ctx, 1, keystore.PurposeTicket, staged.Version))
	newTicket, _, err := h.runtime.IssueTicket(42, nodeID, 7)
	require.NoError(t, err)
	for _, tk := range []string{oldTicket, newTicket} {
		_, err = v.Verify(tk)
		require.NoError(t, err, "both versions verify during the overlap")
	}

	require.ErrorIs(t, h.runtime.RetireKey(ctx, 1, keystore.PurposeTicket, oldVersion), master.ErrKeyRetireTooEarly)
	*clock = clock.Add(sign.TicketLifetime + sign.ClockSkew + 2*time.Minute)
	require.NoError(t, h.runtime.RetireKey(ctx, 1, keystore.PurposeTicket, oldVersion))
	v = fetch(t, h, nodeID)
	v.Now = func() time.Time { return *clock }
	_, err = v.Verify(oldTicket)
	require.ErrorIs(t, err, sign.ErrUnknownKey)
	fresh, _, err := h.runtime.IssueTicket(42, nodeID, 7)
	require.NoError(t, err)
	_, err = v.Verify(fresh)
	require.NoError(t, err)
}

// 凭证公钥不下发，启用新版本不等送达；旧版本要等它签的凭证都超过入账期限（60 天）才能停用。
func TestVoucherKeyRotation(t *testing.T) {
	ctx := context.Background()
	h, clock, nodeID := startWithNode(t)
	raw, issued, err := h.runtime.IssueVoucher(&relayv1.Voucher{NodeId: nodeID, UserId: 42, RequestedModel: "gpt-5"})
	require.NoError(t, err)
	keys, err := h.runtime.Keys(ctx, keystore.PurposeVoucher)
	require.NoError(t, err)
	oldVersion := keys[0].Version

	staged, err := h.runtime.StageKey(ctx, 1, keystore.PurposeVoucher)
	require.NoError(t, err)
	keys, err = h.runtime.Keys(ctx, keystore.PurposeVoucher)
	require.NoError(t, err)
	require.Empty(t, keys[1].PendingNodeIDs, "voucher keys are never sent to nodes")
	require.NoError(t, h.runtime.ActivateKey(ctx, 1, keystore.PurposeVoucher, staged.Version), "no delivery needed")

	_, err = h.runtime.VerifyVoucher(raw, nodeID+1)
	require.ErrorIs(t, err, sign.ErrWrongNode, "only the node the voucher was issued for may report it")
	got, err := h.runtime.VerifyVoucher(raw, nodeID)
	require.NoError(t, err, "vouchers signed before the rotation still verify")
	require.Equal(t, issued.VoucherId, got.VoucherId)

	*clock = clock.Add(sign.VoucherMaxAge)
	require.ErrorIs(t, h.runtime.RetireKey(ctx, 1, keystore.PurposeVoucher, oldVersion), master.ErrKeyRetireTooEarly)
	*clock = clock.Add(2 * 24 * time.Hour)
	require.NoError(t, h.runtime.RetireKey(ctx, 1, keystore.PurposeVoucher, oldVersion))

	snap, err := h.runtime.Publisher().FetchConfig(ctx, nodeID, "")
	require.NoError(t, err)
	require.Len(t, snap.TicketPublicKeys, 1, "only ticket keys are in the snapshot")
}

func TestParseKeyPurpose(t *testing.T) {
	for _, ok := range []string{"root_ca", "ticket", "voucher"} {
		_, err := master.ParseKeyPurpose(ok)
		require.NoError(t, err)
	}
	_, err := master.ParseKeyPurpose("tls")
	require.ErrorIs(t, err, master.ErrUnknownKeyPurpose)
}
