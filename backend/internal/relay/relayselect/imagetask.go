package relayselect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ImageTask 异步图片任务（设计 14）：任务状态（Redis）和结果转存（对象存储）都在主节点，用的是单机同一个
// service.ImageTaskService；从节点执行任务、报结果。创建、取结果、报结果只认这台节点最近准入过的用户，且任务要属于
// 请求里的用户和 Key（报结果前先按归属取一次任务）。
func (s *selector) ImageTask(ctx context.Context, nodeID int64, req *relayv1.ImageTaskRequest) (*relayv1.ImageTaskResponse, error) {
	tasks := s.deps.ImageTasks
	resp := &relayv1.ImageTaskResponse{}
	if tasks != nil {
		resp.Enabled, resp.Pollable, resp.ExecutionTimeoutMs = tasks.Enabled(), tasks.Pollable(), tasks.ExecutionTimeout().Milliseconds()
	}
	if req.GetOp() == relayv1.ImageTaskRequest_OP_STATUS {
		return resp, nil
	}
	if tasks == nil {
		return failedImageTask(resp, service.ErrImageTaskUnavailable), nil
	}
	if !s.admitted.recent(nodeID, req.GetUserId(), s.now()) {
		slog.Warn("relay: image task for a user this node has not admitted recently", "node_id", nodeID, "user_id", req.GetUserId())
		return nil, status.Error(codes.PermissionDenied, "user was not admitted by this node recently")
	}
	owner := service.ImageTaskOwner{UserID: req.GetUserId(), APIKeyID: req.GetApiKeyId()}
	var task *service.ImageTask
	var err error
	switch req.GetOp() {
	case relayv1.ImageTaskRequest_OP_CREATE:
		if !tasks.Enabled() {
			return failedImageTask(resp, service.ErrImageTaskUnavailable), nil
		}
		task, err = tasks.Create(ctx, owner)
	case relayv1.ImageTaskRequest_OP_GET:
		task, err = tasks.Get(ctx, owner, req.GetTaskId())
		if err == nil {
			task, err = s.failStuckImageTask(ctx, tasks, owner, task)
		}
	case relayv1.ImageTaskRequest_OP_COMPLETE, relayv1.ImageTaskRequest_OP_FAIL:
		if _, err = tasks.Get(ctx, owner, req.GetTaskId()); err != nil {
			break
		}
		if req.GetOp() == relayv1.ImageTaskRequest_OP_COMPLETE {
			err = tasks.Complete(ctx, req.GetTaskId(), int(req.GetHttpStatus()), json.RawMessage(req.GetPayload()))
		} else {
			err = tasks.Fail(ctx, req.GetTaskId(), int(req.GetHttpStatus()), json.RawMessage(req.GetPayload()))
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown image task operation")
	}
	if err != nil {
		var coded *infraerrors.ApplicationError
		if errors.As(err, &coded) {
			return failedImageTask(resp, err), nil
		}
		return nil, err
	}
	if task != nil {
		if resp.TaskJson, err = json.Marshal(task); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func failedImageTask(resp *relayv1.ImageTaskResponse, err error) *relayv1.ImageTaskResponse {
	resp.ErrorReason, resp.ErrorStatus, resp.ErrorMessage = infraerrors.Reason(err), int32(infraerrors.Code(err)), infraerrors.Message(err)
	return resp
}

// imageTaskStuckMargin 是任务执行超时之后再多等多久才认定执行它的从节点已经不在了。
const imageTaskStuckMargin = 5 * time.Minute

// failStuckImageTask 执行中的从节点挂了（设计 14）：任务一直"处理中"，超过执行超时加余量后查询时标为失败（不另扣费：用量在执行
// 时已按同步图片入口入账，或根本没有入账）。
func (s *selector) failStuckImageTask(ctx context.Context, tasks *service.ImageTaskService, owner service.ImageTaskOwner, task *service.ImageTask) (*service.ImageTask, error) {
	if task.Status != service.ImageTaskStatusProcessing {
		return task, nil
	}
	if s.now().Sub(time.Unix(task.CreatedAt, 0)) <= tasks.ExecutionTimeout()+imageTaskStuckMargin {
		return task, nil
	}
	taskErr, _ := json.Marshal(map[string]string{"type": "timeout_error", "message": "image generation task timed out"})
	if err := tasks.Fail(ctx, task.ID, http.StatusGatewayTimeout, taskErr); err != nil {
		return nil, err
	}
	return tasks.Get(ctx, owner, task.ID)
}
