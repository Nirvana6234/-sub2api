package master_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type runtimeHarness struct {
	cfg      *config.Config
	store    *master.MemoryStore
	settings service.SettingRepository
	runtime  *master.Runtime
}

func newRuntime(t *testing.T, mutate func(*config.Config)) *runtimeHarness {
	t.Helper()
	kek := make([]byte, 32)
	_, err := rand.Read(kek)
	require.NoError(t, err)
	cfg := &config.Config{Relay: config.RelayConfig{
		NodeRole:         config.RelayNodeRoleMaster,
		MasterListenAddr: "127.0.0.1:0",
		KeyDir:           t.TempDir(),
		KeyEncryptionKey: hex.EncodeToString(kek),
	}}
	if mutate != nil {
		mutate(cfg)
	}
	hub := service.NewSettingChangeHub()
	h := &runtimeHarness{cfg: cfg, store: master.NewMemoryStore(), settings: service.NewObservedSettingRepository(newMemSettings(), hub)}
	h.runtime = master.NewRuntime(master.RuntimeDeps{Config: cfg, Store: h.store, Settings: h.settings, Hub: hub})
	t.Cleanup(h.runtime.Close)
	h.runtime.Init(context.Background())
	return h
}

// hello 连到运行中的主从通信端口，用匿名身份握手。
func hello(t *testing.T, st master.RuntimeStatus) error {
	t.Helper()
	c, err := transport.NewClient(transport.ClientOptions{
		Address: st.ListenAddr,
		TLS:     transport.ClientTLSOptions{PinnedRootFingerprints: func() []string { return st.RootFingerprints }},
	})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := relayv1.NewRelayEnrollmentClient(c.Conn(transport.TierControl)).Hello(ctx, &relayv1.HelloRequest{})
	if err == nil {
		require.Equal(t, st.Epoch, resp.MasterEpoch)
	}
	return err
}

func TestRuntimeIsInertUnlessRoleSwitchAndConfigAllAgree(t *testing.T) {
	ctx := context.Background()

	relayRole := newRuntime(t, func(c *config.Config) { c.Relay.NodeRole = config.RelayNodeRoleRelay })
	require.Equal(t, master.StateNotMaster, relayRole.runtime.Status().State)

	h := newRuntime(t, nil)
	require.Equal(t, master.StateOff, h.runtime.Status().State, "the switch is off by default")

	noAddr := newRuntime(t, func(c *config.Config) { c.Relay.MasterListenAddr = "" })
	st, err := noAddr.runtime.SetEnabled(ctx, true)
	require.NoError(t, err)
	require.Equal(t, master.StateNotConfigured, st.State)
	require.Contains(t, st.Reason, "master_listen_addr")

	noKEK := newRuntime(t, func(c *config.Config) { c.Relay.KeyEncryptionKey = "" })
	st, err = noKEK.runtime.SetEnabled(ctx, true)
	require.NoError(t, err)
	require.Equal(t, master.StateNotConfigured, st.State)
	require.Contains(t, st.Reason, "key_encryption_key", "the KEK is never generated automatically")
}

func TestRuntimeStartsAndStopsWithTheSwitch(t *testing.T) {
	ctx := context.Background()
	h := newRuntime(t, nil)

	st, err := h.runtime.SetEnabled(ctx, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)
	require.NotEmpty(t, st.RootFingerprints)
	require.NoError(t, hello(t, st))

	// 对齐是幂等的：再来一次不会重复开端口。
	h.runtime.Reconcile(ctx)
	require.Equal(t, st.ListenAddr, h.runtime.Status().ListenAddr)

	// 有已激活的节点时不能关。
	n, err := h.store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	require.NoError(t, h.store.Activate(ctx, n.ID, master.Activation{PublicDomain: "r.example.com", At: time.Now()}))
	_, err = h.runtime.SetEnabled(ctx, false)
	require.ErrorIs(t, err, master.ErrNodesStillServing)
	require.Equal(t, master.StateRunning, h.runtime.Status().State)

	// 停用节点后可以关：端口随之关闭。
	require.NoError(t, h.runtime.Nodes().Disable(ctx, n.ID, 1))
	st2, err := h.runtime.SetEnabled(ctx, false)
	require.NoError(t, err)
	require.Equal(t, master.StateOff, st2.State)
	_, dialErr := net.DialTimeout("tcp", st.ListenAddr, time.Second)
	require.Error(t, dialErr, "the relay port is closed when relay is off")
	require.Nil(t, h.runtime.Nodes())

	// 再打开：同一套私钥（根证书指纹不变），新纪元。
	st3, err := h.runtime.SetEnabled(ctx, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st3.State, st3.Reason)
	require.Equal(t, st.RootFingerprints, st3.RootFingerprints, "the root certificate survives a restart of the relay")
	require.NotEqual(t, st.Epoch, st3.Epoch)
	require.NoError(t, hello(t, st3))
}

func TestRuntimeFollowsSwitchWrittenElsewhere(t *testing.T) {
	ctx := context.Background()
	h := newRuntime(t, nil)
	require.NoError(t, h.settings.Set(ctx, master.SettingKeyRelayEnabled, "true"))
	require.Eventually(t, func() bool { return h.runtime.Status().State == master.StateRunning }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, h.settings.Set(ctx, master.SettingKeyRelayEnabled, "false"))
	require.Eventually(t, func() bool { return h.runtime.Status().State == master.StateOff }, 5*time.Second, 20*time.Millisecond)
}

func TestRuntimeReportsAWrongKeyEncryptionKey(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first := newRuntime(t, func(c *config.Config) { c.Relay.KeyDir = dir })
	st, err := first.runtime.SetEnabled(ctx, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State)
	first.runtime.Close()

	other := newRuntime(t, func(c *config.Config) { c.Relay.KeyDir = dir })
	st, err = other.runtime.SetEnabled(ctx, true)
	require.NoError(t, err)
	require.Equal(t, master.StateFailed, st.State)
	require.Contains(t, st.Reason, "wrong key encryption key")
}

// ---- 内存设置仓储 ----

type memSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemSettings() *memSettings { return &memSettings{values: map[string]string{}} }

func (s *memSettings) Get(_ context.Context, key string) (*service.Setting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return nil, service.ErrSettingNotFound
	}
	return &service.Setting{Key: key, Value: v}, nil
}

func (s *memSettings) GetValue(ctx context.Context, key string) (string, error) {
	st, err := s.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return st.Value, nil
}

func (s *memSettings) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
	return nil
}

func (s *memSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (s *memSettings) SetMultiple(ctx context.Context, m map[string]string) error {
	for k, v := range m {
		_ = s.Set(ctx, k, v)
	}
	return nil
}

func (s *memSettings) GetAll(context.Context) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.values {
		out[k] = v
	}
	return out, nil
}

func (s *memSettings) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.values, key)
	s.mu.Unlock()
	return nil
}
