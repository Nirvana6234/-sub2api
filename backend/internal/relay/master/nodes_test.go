package master_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/stretchr/testify/require"
)

// 主节点重启后（新的 Nodes，从库里 Load），被续签替换的旧证书仍然不能建新连接，
// 被吊销的证书也一样；新证书照常。
func TestRestartedMasterStillRefusesRenewedAndRevokedCertificates(t *testing.T) {
	ctx := context.Background()
	store := master.NewMemoryStore()
	node, err := store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	require.NoError(t, store.Activate(ctx, node.ID, master.Activation{PublicDomain: "r.example.com", At: time.Now()}))

	now := time.Now()
	for _, c := range []*master.Certificate{
		{NodeID: node.ID, Serial: "old", PublicKey: []byte{1}, NotBefore: now, NotAfter: now.Add(time.Hour)},
		{NodeID: node.ID, Serial: "new", PublicKey: []byte{2}, NotBefore: now, NotAfter: now.Add(time.Hour), RenewedFromSerial: "old"},
	} {
		require.NoError(t, store.InsertCertificate(ctx, c))
	}

	restarted := master.NewNodes(store, nil, nil, master.NodesOptions{})
	require.NoError(t, restarted.Load(ctx))

	peer := func(serial string) transport.PeerIdentity {
		return transport.PeerIdentity{Class: transport.PeerIssued, NodeID: node.ID, CertSerial: serial,
			RemoteAddr: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 1}}
	}
	require.ErrorContains(t, restarted.AdmitConn(peer("old")), "renewed")
	require.NoError(t, restarted.AdmitConn(peer("new")))
	require.NoError(t, restarted.AuthorizeIssued(ctx, peer("new")))

	_, err = store.RevokeCertificates(ctx, node.ID, "test", now)
	require.NoError(t, err)
	again := master.NewNodes(store, nil, nil, master.NodesOptions{})
	require.NoError(t, again.Load(ctx))
	require.ErrorContains(t, again.AdmitConn(peer("new")), "revoked")
	require.Error(t, again.AuthorizeIssued(ctx, peer("new")))
}

func TestUpdateDomainNormalizesServingNode(t *testing.T) {
	ctx := context.Background()
	store := master.NewMemoryStore()
	node, err := store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp-domain", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	require.NoError(t, store.Activate(ctx, node.ID, master.Activation{PublicDomain: "old.example.com", At: time.Now()}))

	nodes := master.NewNodes(store, nil, nil, master.NodesOptions{})
	require.NoError(t, nodes.UpdateDomain(ctx, node.ID, " New.Example.com ", 7))

	updated, err := store.GetByID(ctx, node.ID)
	require.NoError(t, err)
	require.Equal(t, "new.example.com", updated.PublicDomain)
	require.NoError(t, nodes.UpdateDomain(ctx, node.ID, "192.0.2.10:18081", 7))
	updated, err = store.GetByID(ctx, node.ID)
	require.NoError(t, err)
	require.Equal(t, "192.0.2.10:18081", updated.PublicDomain)

	require.ErrorIs(t, nodes.UpdateDomain(ctx, node.ID, "", 7), master.ErrDomainRequired)
	require.NoError(t, nodes.UpdateDomain(ctx, node.ID, "new.example.com", 7))
}
