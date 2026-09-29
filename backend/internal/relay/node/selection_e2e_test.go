package node_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
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
	upstream []*relayv1.UpstreamErrorRequest

	accountEvents chan *relayv1.AccountEvent
}

func newFakeSelector() *fakeSelector {
	return &fakeSelector{releases: make(chan *relayv1.SelectionRelease, 16), accountEvents: make(chan *relayv1.AccountEvent, 16)}
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

func (f *fakeSelector) Admit(_ context.Context, _ int64, req *relayv1.AdmitRequest) (*relayv1.AdmitResponse, error) {
	if req.GetApiKey() == "" {
		return &relayv1.AdmitResponse{Result: &relayv1.AdmitResponse_Rejection{Rejection: &relayv1.SelectRejection{Status: 401, Format: relayv1.RejectionFormat_REJECTION_FORMAT_RAW}}}, nil
	}
	return &relayv1.AdmitResponse{Result: &relayv1.AdmitResponse_Admission{Admission: &relayv1.Admission{ApiKey: []byte(`{"ID":1}`)}}}, nil
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

func (f *fakeSelector) BeginTurn(context.Context, int64, *relayv1.BeginTurnRequest) (*relayv1.BeginTurnResponse, error) {
	return &relayv1.BeginTurnResponse{}, nil
}

func (f *fakeSelector) TurnMapping(context.Context, int64, *relayv1.TurnMappingRequest) (*relayv1.TurnMappingResponse, error) {
	return &relayv1.TurnMappingResponse{}, nil
}

func (f *fakeSelector) WebSocketLease(context.Context, int64, *relayv1.WebSocketLeaseRequest) (*relayv1.WebSocketLeaseResponse, error) {
	return &relayv1.WebSocketLeaseResponse{}, nil
}

func (f *fakeSelector) CyberPolicyHit(context.Context, int64, *relayv1.CyberPolicyHitRequest) (*relayv1.CyberPolicyHitResponse, error) {
	return &relayv1.CyberPolicyHitResponse{}, nil
}

func (f *fakeSelector) UpstreamError(_ context.Context, nodeID int64, req *relayv1.UpstreamErrorRequest) (*relayv1.UpstreamErrorResponse, error) {
	f.mu.Lock()
	f.upstream = append(f.upstream, req)
	f.nodes = append(f.nodes, nodeID)
	f.mu.Unlock()
	if req.GetAccountId() != 7 {
		return nil, master.ErrSelectionNotFound
	}
	switch req.GetKind() {
	case relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_OAUTH429_RETRY:
		return &relayv1.UpstreamErrorResponse{RetrySameAccount: true, RetryDeadlineUnixMs: 1_900_000_000_000}, nil
	case relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_ERROR_POLICY:
		return &relayv1.UpstreamErrorResponse{ErrorPolicy: int32(service.ErrorPolicyTempUnscheduled)}, nil
	default:
		return &relayv1.UpstreamErrorResponse{ShouldDisable: true}, nil
	}
}

func (f *fakeSelector) Release(nodeID int64, rel *relayv1.SelectionRelease) {
	f.mu.Lock()
	f.nodes = append(f.nodes, nodeID)
	f.mu.Unlock()
	f.releases <- rel
}

func (f *fakeSelector) AccountEvent(nodeID int64, ev *relayv1.AccountEvent) {
	f.accountEvents <- ev
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
	master.RouteNodeEvents(m.events, sel)

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
			Status: 401, Code: "INVALID_API_KEY", Message: "Invalid API key", Format: relayv1.RejectionFormat_REJECTION_FORMAT_RAW,
		}}}, nil
	}
	resp, err = client.Select(ctx, &relayv1.SelectRequest{RequestId: "r3", Attempt: 1})
	require.NoError(t, err, "a rejection is a normal reply")
	require.Equal(t, int32(401), resp.GetRejection().GetStatus())

	creds, err := client.FetchCredentials(ctx, "sel-r1")
	require.NoError(t, err)
	require.Equal(t, []byte("sealed"), creds.GetAccount().GetSealedCredentials())
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

// 上游错误决策经真实连接：带上 ctx 里的选号 ID 和请求标记，响应头去掉 Set-Cookie；
// 主节点不认这个账号（或不可达）时按"不改变账号状态"回答。
func TestRemoteUpstreamErrorDecider(t *testing.T) {
	ctx := context.Background()
	m := startMaster(t)
	n := startNode(t, m)
	sel := newFakeSelector()
	m.control.AttachSelector(sel, m.srv.Epoch())
	d := node.NewRemoteUpstreamErrorDecider(n.client)
	account := &service.Account{ID: 7}

	rctx := service.WithOpenAIUpstreamErrorFlags(node.WithSelectionID(ctx, "sel-9"), service.OpenAIUpstreamErrorFlags{ImagesSelfBuilt: true})
	headers := http.Header{"X-Codex-Primary-Used-Percent": {"100"}, "Set-Cookie": {"session=secret"}}
	require.True(t, d.HandleUpstreamError(rctx, account, 429, headers, []byte(`{"error":{}}`), "gpt-5"))
	req := sel.upstream[0]
	require.Equal(t, relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RESPONSE, req.GetKind())
	require.Equal(t, "sel-9", req.GetSelectionId())
	require.Equal(t, int64(7), req.GetAccountId())
	require.Equal(t, int32(429), req.GetStatusCode())
	require.True(t, req.GetHasModel())
	require.Equal(t, "gpt-5", req.GetModel())
	require.True(t, req.GetImagesSelfBuilt())
	require.Equal(t, m.nodeID, sel.nodes[0])
	for _, h := range req.GetHeaders() {
		require.NotEqual(t, "Set-Cookie", h.GetName(), "cookies are not sent to the master")
	}
	require.Len(t, req.GetHeaders(), 1)

	retry, deadline := d.OAuth429SameAccountRetry(context.Background(), account)
	require.True(t, retry)
	require.Equal(t, int64(1_900_000_000_000), deadline.UnixMilli())
	require.Equal(t, service.ErrorPolicyTempUnscheduled, d.CheckErrorPolicy(ctx, account, 400, nil, "gpt-5"))
	require.True(t, d.HandleStreamTimeout(ctx, account, "gpt-5"))

	other := &service.Account{ID: 8}
	require.False(t, d.HandleUpstreamError(ctx, other, 500, nil, nil), "an account the node is not using changes nothing")
	retry, _ = d.OAuth429SameAccountRetry(ctx, other)
	require.False(t, retry)
	require.Equal(t, service.ErrorPolicyNone, d.CheckErrorPolicy(ctx, other, 400, nil, "gpt-5"))
}

// 账号事件经事件连接送到主节点：健康失败按分类发事实，不计入熔断的不发；Codex 快照补上收到响应的时间。
func TestRemoteAccountReporterOverTheWire(t *testing.T) {
	ctx := context.Background()
	m := startMaster(t)
	n := startNode(t, m)
	sel := newFakeSelector()
	m.control.AttachSelector(sel, m.srv.Epoch())
	master.RouteNodeEvents(m.events, sel)
	outbox := node.NewEventOutbox(0)
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	go node.RunEvents(runCtx, n.client, n.syncer, node.EventHandlers{Outbox: outbox})

	r := node.NewRemoteAccountReporter(outbox)
	account := &service.Account{ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}
	first := 90
	r.ReportScheduleResult(account, "gpt-5", false, &first, 5, "high", &service.UpstreamFailoverError{StatusCode: 500, ResponseBody: []byte("oops")})
	r.ObserveHealthFailure(ctx, account, &service.UpstreamFailoverError{StatusCode: 400}) // 不计入熔断：不发
	r.RecordAccountSwitch()
	r.UpdateCodexUsageSnapshot(ctx, 7, &service.OpenAICodexUsageSnapshot{})
	r.TempUnscheduleTransportError(ctx, account, "dial tcp: refused")
	r.OllamaCloudUsageActivity(account) // 不是 Ollama Cloud 账号：不发

	next := func() *relayv1.AccountEvent {
		select {
		case ev := <-sel.accountEvents:
			return ev
		case <-time.After(5 * time.Second):
			t.Fatal("account event was not delivered")
			return nil
		}
	}
	ev := next()
	res := ev.GetScheduleResult()
	require.Equal(t, int64(7), ev.GetAccountId())
	require.Equal(t, "gpt-5", res.GetModel())
	require.True(t, res.GetHasFirstTokenMs())
	require.Equal(t, int32(90), res.GetFirstTokenMs())
	require.Equal(t, int64(5), res.GetServingGroupId())
	require.True(t, res.GetFailure().GetEligible())
	require.Equal(t, int32(500), res.GetFailure().GetStatusCode())
	require.NotNil(t, next().GetAccountSwitch())
	codex := next().GetCodexUsage()
	var snap service.OpenAICodexUsageSnapshot
	require.NoError(t, json.Unmarshal(codex.GetSnapshotJson(), &snap))
	_, err := time.Parse(time.RFC3339, snap.UpdatedAt)
	require.NoError(t, err, "the node stamps when it saw the headers")
	require.Equal(t, "dial tcp: refused", next().GetTransportError().GetMessage())
	select {
	case ev := <-sel.accountEvents:
		t.Fatalf("unexpected event %v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

type recordingSettler struct {
	mu    sync.Mutex
	nodes []int64
	seqs  []uint64
}

func (s *recordingSettler) Settle(_ context.Context, nodeID int64, rec *relayv1.UsageRecord) *relayv1.UsageRecordResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes = append(s.nodes, nodeID)
	s.seqs = append(s.seqs, rec.GetSeq())
	status := relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED
	if len(rec.GetVoucher()) == 0 {
		status = relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED
	}
	return &relayv1.UsageRecordResult{Seq: rec.GetSeq(), Status: status}
}

// 扣费批次经扣费连接送到主节点：逐条入账、逐条回结果，节点身份取自证书；一批条数有上限。
func TestUsageBatchesOverTheWire(t *testing.T) {
	ctx := context.Background()
	m := startMaster(t)
	settler := &recordingSettler{}
	m.settler.set(settler)
	n := startNode(t, m)
	client := node.NewBillingClient(n.client)

	ack, err := client.Submit(ctx, &relayv1.UsageBatch{BatchSeq: 1, Records: []*relayv1.UsageRecord{
		{Seq: 10, Voucher: []byte("v")}, {Seq: 11},
	}})
	require.NoError(t, err)
	require.Len(t, ack.GetResults(), 2)
	require.Equal(t, uint64(10), ack.GetResults()[0].GetSeq())
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_SETTLED, ack.GetResults()[0].GetStatus())
	require.Equal(t, relayv1.UsageRecordStatus_USAGE_RECORD_STATUS_REJECTED, ack.GetResults()[1].GetStatus())
	require.Equal(t, []int64{m.nodeID, m.nodeID}, settler.nodes)

	big := &relayv1.UsageBatch{BatchSeq: 2}
	for i := 0; i < 501; i++ {
		big.Records = append(big.Records, &relayv1.UsageRecord{Seq: uint64(i)})
	}
	_, err = client.Submit(ctx, big)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
