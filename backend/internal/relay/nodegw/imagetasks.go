package nodegw

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 异步图片任务的状态（Redis）和结果转存（对象存储）都在主节点（设计 14）：从节点接到提交后向主节点登记、自己执行（经本机的
// 同步图片处理函数，选号和扣费照常）、执行完把结果报给主节点，由主节点转存图片后落库；轮询落在任何节点都向主节点查。
// 从节点不持有对象存储凭据。
//
// 与设计的差别：转存的图片数据先随结果走主从连接送到主节点（设计里是从节点用主节点签发的一次性上传地址直传对象存储）。
const (
	imageTaskCallTimeout = 30 * time.Second
	// imageTaskCompleteTimeout 比普通调用长：结果里可能带几 MB 的 base64 图片，主节点要先转存。
	imageTaskCompleteTimeout = 3 * time.Minute
	imageTaskStatusTTL       = 30 * time.Second
	imageTaskStatusTimeout   = 3 * time.Second
)

// RemoteImageTasks 是经主节点的异步图片任务存取（handler.AsyncImageTasks）。
type RemoteImageTasks struct {
	control relayv1.RelayControlClient
	now     func() time.Time

	mu       sync.Mutex
	status   *relayv1.ImageTaskResponse
	fetched  time.Time
	pending  map[string]service.ImageTaskOwner // 本机创建、还没报结果的任务归属（报结果时带上）
	statusMu sync.Mutex
}

var _ handler.AsyncImageTasks = (*RemoteImageTasks)(nil)

// NewRemoteImageTasks 创建。
func NewRemoteImageTasks(client *transport.Client) *RemoteImageTasks {
	return newRemoteImageTasks(relayv1.NewRelayControlClient(client.Conn(transport.TierControl)))
}

func newRemoteImageTasks(control relayv1.RelayControlClient) *RemoteImageTasks {
	return &RemoteImageTasks{control: control, now: time.Now, pending: map[string]service.ImageTaskOwner{}}
}

// current 是主节点上这个功能的状态（缓存 30 秒；取不到时沿用上一次的，从没取到过按"不可用"）。
func (r *RemoteImageTasks) current() *relayv1.ImageTaskResponse {
	r.mu.Lock()
	status, fetched := r.status, r.fetched
	r.mu.Unlock()
	if status != nil && r.now().Sub(fetched) < imageTaskStatusTTL {
		return status
	}
	// 同一时刻只让一个调用去取。
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	r.mu.Lock()
	if r.status != nil && r.now().Sub(r.fetched) < imageTaskStatusTTL {
		status = r.status
		r.mu.Unlock()
		return status
	}
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), imageTaskStatusTimeout)
	defer cancel()
	resp, err := r.control.ImageTask(ctx, &relayv1.ImageTaskRequest{Op: relayv1.ImageTaskRequest_OP_STATUS})
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.status, r.fetched = resp, r.now()
	} else if r.status == nil {
		return &relayv1.ImageTaskResponse{}
	}
	return r.status
}

// Enabled 见 service.ImageTaskService.Enabled（主节点的开关和对象存储配置）。
func (r *RemoteImageTasks) Enabled() bool { return r.current().GetEnabled() }

// Pollable 见 service.ImageTaskService.Pollable。
func (r *RemoteImageTasks) Pollable() bool { return r.current().GetPollable() }

// ExecutionTimeout 见 service.ImageTaskService.ExecutionTimeout。
func (r *RemoteImageTasks) ExecutionTimeout() time.Duration {
	if ms := r.current().GetExecutionTimeoutMs(); ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return 30 * time.Minute
}

func (r *RemoteImageTasks) call(ctx context.Context, timeout time.Duration, req *relayv1.ImageTaskRequest) (*relayv1.ImageTaskResponse, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := r.control.ImageTask(cctx, req)
	if err != nil {
		return nil, service.ErrImageTaskUnavailable.WithCause(err)
	}
	if reason := resp.GetErrorReason(); reason != "" {
		status := int(resp.GetErrorStatus())
		if status <= 0 {
			status = http.StatusInternalServerError
		}
		return nil, infraerrors.New(status, reason, resp.GetErrorMessage())
	}
	return resp, nil
}

func decodeImageTask(resp *relayv1.ImageTaskResponse) (*service.ImageTask, error) {
	var task service.ImageTask
	if err := json.Unmarshal(resp.GetTaskJson(), &task); err != nil {
		return nil, service.ErrImageTaskUnavailable.WithCause(err)
	}
	return &task, nil
}

// Create 在主节点登记任务。
func (r *RemoteImageTasks) Create(ctx context.Context, owner service.ImageTaskOwner) (*service.ImageTask, error) {
	resp, err := r.call(ctx, imageTaskCallTimeout, &relayv1.ImageTaskRequest{Op: relayv1.ImageTaskRequest_OP_CREATE, UserId: owner.UserID, ApiKeyId: owner.APIKeyID})
	if err != nil {
		return nil, err
	}
	task, err := decodeImageTask(resp)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.pending[task.ID] = owner
	r.mu.Unlock()
	return task, nil
}

// Get 向主节点查任务（任务在哪台节点执行都一样）。
func (r *RemoteImageTasks) Get(ctx context.Context, owner service.ImageTaskOwner, id string) (*service.ImageTask, error) {
	resp, err := r.call(ctx, imageTaskCallTimeout, &relayv1.ImageTaskRequest{Op: relayv1.ImageTaskRequest_OP_GET, UserId: owner.UserID, ApiKeyId: owner.APIKeyID, TaskId: id})
	if err != nil {
		return nil, err
	}
	return decodeImageTask(resp)
}

// Complete 把执行结果报给主节点（主节点转存图片后落库）。
func (r *RemoteImageTasks) Complete(ctx context.Context, id string, statusCode int, result json.RawMessage) error {
	return r.finish(ctx, relayv1.ImageTaskRequest_OP_COMPLETE, id, statusCode, result)
}

// Fail 把执行失败报给主节点。
func (r *RemoteImageTasks) Fail(ctx context.Context, id string, statusCode int, taskErr json.RawMessage) error {
	return r.finish(ctx, relayv1.ImageTaskRequest_OP_FAIL, id, statusCode, taskErr)
}

func (r *RemoteImageTasks) finish(ctx context.Context, op relayv1.ImageTaskRequest_Op, id string, statusCode int, payload json.RawMessage) error {
	r.mu.Lock()
	owner, ok := r.pending[id]
	r.mu.Unlock()
	if !ok {
		return service.ErrImageTaskNotFound
	}
	_, err := r.call(ctx, imageTaskCompleteTimeout, &relayv1.ImageTaskRequest{
		Op: op, UserId: owner.UserID, ApiKeyId: owner.APIKeyID, TaskId: id, HttpStatus: int32(statusCode), Payload: payload,
	})
	if err != nil && !errors.Is(err, service.ErrImageTaskUnavailable) {
		// 主节点明确拒绝（任务不存在、不属于这个用户）：不会因重试而成功。
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
		return err
	}
	if err == nil {
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
	}
	return err
}
