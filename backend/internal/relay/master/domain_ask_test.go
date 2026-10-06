package master_test

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/stretchr/testify/require"
)

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// 设计 10.3：Caddy on_demand TLS 申请证书前先问主节点"这是不是已登记的从节点域名"（只听本机回环地址）；
// 已登记（激活或排空中）的域名 200，其余 404。
func TestDomainAskEndpointOnlyApprovesRegisteredNodeDomains(t *testing.T) {
	ctx := context.Background()
	addr := freeLoopbackAddr(t)
	h := newRuntimeWith(t, func(c *config.Config) { c.Relay.DomainAskAddr = addr }, nil)
	st, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)

	active, err := h.store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp1", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	require.NoError(t, h.store.Activate(ctx, active.ID, master.Activation{PublicDomain: "Relay1.Example.com", At: time.Now()}))
	pending, err := h.store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp2", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	_ = pending
	require.NoError(t, h.runtime.Nodes().Load(ctx))
	// 域名缓存 10 秒：节点操作会清掉它，这里用一次无害的节点操作触发。
	require.NoError(t, h.runtime.SetNodeAllowMultiIP(ctx, active.ID, 1, true))

	ask := func(domain string) int {
		resp, err := http.Get("http://" + addr + "/?domain=" + url.QueryEscape(domain))
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(t, http.StatusOK, ask("relay1.example.com"))
	require.Equal(t, http.StatusOK, ask("RELAY1.EXAMPLE.COM"))
	require.Equal(t, http.StatusNotFound, ask("evil.example.com"))
	require.Equal(t, http.StatusNotFound, ask(""))
	resp, err := http.Post("http://"+addr+"/?domain=relay1.example.com", "text/plain", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

	require.True(t, h.runtime.IsNodeDomain("relay1.example.com:443"), "the port is ignored")
	require.False(t, h.runtime.IsNodeDomain("api.example.com"))
}

// ask 接口只能监听本机回环地址：配成对外地址时不启动（运行时照常运行）。
func TestDomainAskRefusesNonLoopbackAddresses(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeWith(t, func(c *config.Config) { c.Relay.DomainAskAddr = "0.0.0.0:0" }, nil)
	st, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)
}
