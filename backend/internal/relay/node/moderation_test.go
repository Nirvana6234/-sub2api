package node

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type flakyControl struct {
	relayv1.RelayControlClient
	mu    sync.Mutex
	errs  []error
	keys  []string
	calls int
}

func (f *flakyControl) ModerationViolation(ctx context.Context, _ *relayv1.ModerationViolationRequest, _ ...grpc.CallOption) (*relayv1.ModerationViolationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.keys = append(f.keys, transport.IdempotencyKey(ctx))
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return nil, err
	}
	return &relayv1.ModerationViolationResponse{ViolationCount: 5}, nil
}

func (f *flakyControl) snapshot() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, append([]string(nil), f.keys...)
}

// 上报调不通：按第 1 次记、不封号，后台用同一个幂等键重试直到主节点记上；主节点明确拒绝的不再重试。
func TestModerationViolationReportIsRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctl := &flakyControl{errs: []error{status.Error(codes.Unavailable, "down"), status.Error(codes.Unavailable, "down")}}
	r := newRemoteModerationActions(ctx, ctl, 10*time.Millisecond)
	uid := int64(3)
	log := &service.ContentModerationLog{UserID: &uid, Flagged: true}
	require.False(t, r.Apply(ctx, nil, log))
	require.Equal(t, 1, log.ViolationCount)
	require.Eventually(t, func() bool { n, _ := ctl.snapshot(); return n == 3 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	n, keys := ctl.snapshot()
	require.Equal(t, 3, n, "stops once the master has it")
	require.NotEmpty(t, keys[0])
	require.Equal(t, keys[0], keys[1])
	require.Equal(t, keys[0], keys[2], "retries reuse the idempotency key so the master counts it once")

	denied := &flakyControl{errs: []error{status.Error(codes.Unavailable, "down"), status.Error(codes.PermissionDenied, "not admitted")}}
	r2 := newRemoteModerationActions(ctx, denied, 10*time.Millisecond)
	r2.Apply(ctx, nil, &service.ContentModerationLog{UserID: &uid, Flagged: true})
	require.Eventually(t, func() bool { n, _ := denied.snapshot(); return n == 2 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	n, _ = denied.snapshot()
	require.Equal(t, 2, n, "a permanent rejection is not retried")
}
