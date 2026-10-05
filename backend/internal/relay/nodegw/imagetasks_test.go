package nodegw

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type flakyTasks struct {
	failures int
	calls    int
	last     *relayv1.ImageTaskRequest
}

func (f *flakyTasks) ImageTask(_ context.Context, in *relayv1.ImageTaskRequest, _ ...grpc.CallOption) (*relayv1.ImageTaskResponse, error) {
	f.calls++
	f.last = in
	if in.GetOp() == relayv1.ImageTaskRequest_OP_CREATE {
		task, _ := json.Marshal(service.ImageTask{ID: "imgtask_1"})
		return &relayv1.ImageTaskResponse{TaskJson: task}, nil
	}
	if f.failures > 0 {
		f.failures--
		return nil, errors.New("master unreachable")
	}
	return &relayv1.ImageTaskResponse{}, nil
}

// 报结果时主节点暂时不可达：有限次退避重发，结果不丢；重发用完才放弃。
func TestRemoteImageTasksRetriesTheResultReport(t *testing.T) {
	fake := &flakyTasks{}
	r := newRemoteImageTasks(fake)
	r.retryDelay = time.Millisecond
	ctx := context.Background()
	task, err := r.Create(ctx, service.ImageTaskOwner{UserID: 3, APIKeyID: 11})
	require.NoError(t, err)

	fake.failures = 3
	require.NoError(t, r.Complete(ctx, task.ID, 200, json.RawMessage(`{"data":[]}`)))
	require.Equal(t, 1+4, fake.calls, "three failures and the fourth try succeeds")

	task2, err := r.Create(ctx, service.ImageTaskOwner{UserID: 3, APIKeyID: 11})
	require.NoError(t, err)
	fake.failures = 100
	fake.calls = 0
	err = r.Fail(ctx, task2.ID, 502, json.RawMessage(`{"type":"api_error"}`))
	require.ErrorIs(t, err, service.ErrImageTaskUnavailable)
	require.Equal(t, 1+imageTaskFinishRetries, fake.calls)

	// 没登记过的任务（不是这台创建的）：不发。
	fake.calls = 0
	require.ErrorIs(t, r.Complete(ctx, "imgtask_unknown", 200, json.RawMessage(`{}`)), service.ErrImageTaskNotFound)
	require.Zero(t, fake.calls)
}
