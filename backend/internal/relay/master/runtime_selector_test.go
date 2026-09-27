package master_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type recordingSelector struct {
	env    master.SelectEnv
	closed bool
}

func (s *recordingSelector) Select(context.Context, int64, *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	return &relayv1.SelectResponse{}, nil
}

func (s *recordingSelector) FetchCredentials(context.Context, int64, *relayv1.FetchCredentialsRequest) (*relayv1.FetchCredentialsResponse, error) {
	return nil, master.ErrSelectionNotFound
}

func (s *recordingSelector) RefillQuota(context.Context, int64, *relayv1.RefillQuotaRequest) (*relayv1.RefillQuotaResponse, error) {
	return &relayv1.RefillQuotaResponse{}, nil
}

func (s *recordingSelector) UpstreamError(context.Context, int64, *relayv1.UpstreamErrorRequest) (*relayv1.UpstreamErrorResponse, error) {
	return &relayv1.UpstreamErrorResponse{}, nil
}

func (s *recordingSelector) Release(int64, *relayv1.SelectionRelease) {}

func (s *recordingSelector) AccountEvent(int64, *relayv1.AccountEvent) {}

func (s *recordingSelector) Close() { s.closed = true }

// 选号实现随主从分流启停：每次启动用本次的纪元、签名密钥、节点信息新建一个，停止时关闭。
func TestRuntimeCreatesAndClosesTheSelector(t *testing.T) {
	ctx := context.Background()
	cfg := testRelayConfig(t)
	hub := service.NewSettingChangeHub()
	store := master.NewMemoryStore()
	var mu sync.Mutex
	var made []*recordingSelector
	var settleEnvs []master.SettleEnv
	rt := master.NewRuntime(master.RuntimeDeps{
		Config: cfg, Store: store, Settings: service.NewObservedSettingRepository(newMemSettings(), hub), Hub: hub,
		NewSettler: func(env master.SettleEnv) master.Settler {
			settleEnvs = append(settleEnvs, env)
			return nil
		},
		NewSelector: func(env master.SelectEnv) master.Selector {
			s := &recordingSelector{env: env}
			mu.Lock()
			made = append(made, s)
			mu.Unlock()
			return s
		},
	})
	t.Cleanup(rt.Close)
	rt.Init(ctx)
	require.Empty(t, made, "nothing is created while relay is off")

	st, err := rt.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)
	require.Len(t, made, 1)
	env := made[0].env
	require.Equal(t, st.Epoch, env.Epoch)

	n, err := store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	require.NoError(t, store.Activate(ctx, n.ID, master.Activation{PublicDomain: "r.example.com", At: time.Now()}))
	version, err := env.ConfigVersion(ctx, n.ID)
	require.NoError(t, err)
	require.NotEmpty(t, version)
	again, err := env.ConfigVersion(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, version, again)
	_, ok := env.NodeEncryptionKey(n.ID)
	require.False(t, ok, "no encryption key until the node obtains a certificate")

	raw, signed, err := env.IssueVoucher(&relayv1.Voucher{NodeId: n.ID, SelectionId: "sel-1", UserId: 9})
	require.NoError(t, err)
	verified, err := rt.VerifyVoucher(raw, n.ID)
	require.NoError(t, err, "vouchers issued through the selection environment verify with the runtime's key")
	require.Equal(t, signed.GetVoucherId(), verified.GetVoucherId())
	viaEnv, err := env.VerifyVoucher(raw, n.ID)
	require.NoError(t, err)
	require.Equal(t, signed.GetVoucherId(), viaEnv.GetVoucherId())
	require.Len(t, settleEnvs, 1, "the settler is created with the relay")
	fromSettleEnv, err := settleEnvs[0].VerifyVoucher(raw, n.ID)
	require.NoError(t, err)
	require.Equal(t, signed.GetVoucherId(), fromSettleEnv.GetVoucherId())
	_, err = env.VerifyVoucher(raw, n.ID+1)
	require.Error(t, err)

	require.NoError(t, rt.Nodes().Disable(ctx, n.ID, 1))
	_, err = rt.SetEnabled(ctx, 1, false)
	require.NoError(t, err)
	require.True(t, made[0].closed, "stopping relay closes the selector (frees held slots)")

	st2, err := rt.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Len(t, made, 2)
	require.Equal(t, st2.Epoch, made[1].env.Epoch)
}
