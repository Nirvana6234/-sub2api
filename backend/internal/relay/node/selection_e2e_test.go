package node_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeSelector struct {
	mu       sync.Mutex
	selects  []*relayv1.SelectRequest
	nodes    []int64
	releases chan *relayv1.SelectionRelease
	respond  func(*relayv1.SelectRequest) (*relayv1.SelectResponse, error)
	closed   bool
}

func newFakeSelector() *fakeSelector {
	return &fakeSelector{releases: make(chan *relayv1.SelectionRelease, 16)}
}

func (f *fakeSelector) Select(_ context.Context, nodeID int64, req *relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
	f.mu.Lock()
	f.selects = append(f.selects, req)
	f.nodes = append(f.nodes, nodeID)
	respond := f.respond
	f.mu.Unlock()
	if respond != nil {
		return respond(req)
	}
	return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Selection{Selection: &relayv1.Selection{
		SelectionId: "sel-" + req.GetRequestId(), Account: &relayv1.AccountSnapshot{Id: 7}, ConfigVersion: "v1",
	}}}, nil
}

func (f *fakeSelector) FetchCredentials(_ context.Context, _ int64, req *relayv1.FetchCredentialsRequest) (*relayv1.FetchCredentialsResponse, error) {
	if req.GetSelectionId() != "sel-r1" {
		return nil, master.ErrSelectionNotFound
	}
	return &relayv1.FetchCredentialsResponse{Account: &relayv1.AccountSnapshot{Id: 7, CredentialVersion: "c1", SealedCredentials: []byte("sealed")}}, nil
}

func (f *fakeSelector) RefillQuota(context.Context, int64, *relayv1.RefillQuotaRequest) (*relayv1.RefillQuotaResponse, error) {
	return nil, errors.New("database is down: secret detail")
}

func (f *fakeSelector) Release(nodeID int64, rel *relayv1.SelectionRelease) {
	f.mu.Lock()
	f.nodes = append(f.nodes, nodeID)
	f.mu.Unlock()
	f.releases <- rel
}

func (f *fakeSelector) Close() { f.closed = true }

func (f *fakeSelector) selectCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.selects)
}

// 选号、取凭据、补额度、释放经真实 TLS 连接走通：节点身份取自证书，重发不重复选号，
// 拒绝原样带回，内部错误不外泄。
func TestSelectionCallsOverTheWire(t *testing.T) {
	ctx := context.Background()
	m := startMaster(t)
	n := startNode(t, m)
	sel := newFakeSelector()
	outbox := node.NewEventOutbox(0)
	client := node.NewSelectClient(n.client, outbox)

	_, err := client.Select(ctx, &relayv1.SelectRequest{RequestId: "r0", Attempt: 1})
	require.ErrorIs(t, err, transport.ErrEpochChanged, "a node that has not learned the epoch cannot select")
	require.NoError(t, n.syncer.Sync(ctx), "learns the master epoch")
	_, err = client.Select(ctx, &relayv1.SelectRequest{RequestId: "r0", Attempt: 2})
	require.Equal(t, codes.Unavailable, status.Code(err), "no selector attached yet")

	m.control.AttachSelector(sel, m.srv.Epoch())
	master.RouteSelectionReleases(m.events, sel)

	req := &relayv1.SelectRequest{RequestId: "r1", Attempt: 1, Credential: &relayv1.SelectRequest_ApiKey{ApiKey: "sk-test"}, Model: "gpt-5"}
	resp, err := client.Select(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "sel-r1", resp.GetSelection().GetSelectionId())
	again, err := client.Select(ctx, req)
	require.NoError(t, err)
	require.Equal(t, resp.GetSelection().GetSelectionId(), again.GetSelection().GetSelectionId())
	require.Equal(t, 1, sel.selectCount(), "a resend with the same request and attempt is answered from the first result")
	require.Equal(t, m.nodeID, sel.nodes[0], "the node comes from the certificate")
	require.Equal(t, "sk-test", sel.selects[0].GetApiKey())

	req.Attempt = 2
	_, err = client.Select(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 2, sel.selectCount(), "the next attempt selects again")

	_, err = client.Select(ctx, &relayv1.SelectRequest{RequestId: "r2"})
	require.Equal(t, codes.InvalidArgument, status.Code(err), "attempt is required")

	sel.respond = func(*relayv1.SelectRequest) (*relayv1.SelectResponse, error) {
		return &relayv1.SelectResponse{Result: &relayv1.SelectResponse_Rejection{Rejection: &relayv1.SelectRejection{
			Status: 401, Code: "INVALID_API_KEY", Message: "Invalid API key", Format: relayv1.RejectionFormat_REJECTION_FORMAT_AUTH,
		}}}, nil
	}
	resp, err = client.Select(ctx, &relayv1.SelectRequest{RequestId: "r3", Attempt: 1})
	require.NoError(t, err, "a rejection is a normal reply")
	require.Equal(t, int32(401), resp.GetRejection().GetStatus())

	creds, err := client.FetchCredentials(ctx, "sel-r1")
	require.NoError(t, err)
	require.Equal(t, []byte("sealed"), creds.GetSealedCredentials())
	_, err = client.FetchCredentials(ctx, "sel-gone")
	require.Equal(t, codes.NotFound, status.Code(err))

	_, err = client.RefillQuota(ctx, &relayv1.RefillQuotaRequest{SelectionId: "sel-r1"})
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.NotContains(t, err.Error(), "secret detail", "internal errors are not sent to the node")

	// 释放在事件流连上之前入队，连上后送达。
	client.Release(&relayv1.SelectionRelease{SelectionId: "sel-r1", RequestDone: true, ResponseIds: []string{"resp_1"}})
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	go node.RunEvents(runCtx, n.client, n.syncer, node.EventHandlers{Outbox: outbox})
	select {
	case rel := <-sel.releases:
		require.Equal(t, "sel-r1", rel.GetSelectionId())
		require.True(t, rel.GetRequestDone())
		require.Equal(t, []string{"resp_1"}, rel.GetResponseIds())
	case <-time.After(5 * time.Second):
		t.Fatal("release was not delivered")
	}
	require.Eventually(t, func() bool { return outbox.Len() == 0 }, time.Second, 10*time.Millisecond)
}

func TestEventOutboxDropsTheOldestWhenFull(t *testing.T) {
	o := node.NewEventOutbox(2)
	for _, id := range []string{"a", "b", "c"} {
		o.Enqueue(&relayv1.NodeEnvelope{Body: &relayv1.NodeEnvelope_SelectionRelease{SelectionRelease: &relayv1.SelectionRelease{SelectionId: id}}})
	}
	require.Equal(t, 2, o.Len())
}
