package relaysettle

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// videoTaskRegistration 登记异步视频任务（Grok 视频、Seedance）：任务绑定到选号的账号、写创建时的待计费快照。任务状态在主节点
// 的 Redis 里，状态 / 内容轮询落在任何节点都按它选号和计费（设计 14）。登记不扣费；执行失败时让从节点重发（两步都是覆盖写，
// 重发无副作用）。
func (s *Settler) videoTaskRegistration(ctx context.Context, v *relayv1.Voucher, rec *relayv1.UsageRecord) (func(context.Context) error, error) {
	taskID := strings.TrimSpace(rec.GetTaskId())
	if taskID == "" {
		return nil, reject("video task record without a task id")
	}
	var pending service.GrokVideoPendingBilling
	if raw := rec.GetTaskPendingJson(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &pending); err != nil {
			return nil, reject("malformed video task snapshot: %v", err)
		}
	}
	f, err := s.selectionFacts(ctx, v)
	if err != nil {
		return nil, err
	}
	groupID, userID, apiKeyID, accountID := f.apiKey.GroupID, f.apiKey.User.ID, f.apiKey.ID, f.account.ID
	return func(ctx context.Context) error {
		if err := s.deps.Gateway.BindGrokMediaVideoRequestAccount(ctx, groupID, taskID, userID, apiKeyID, accountID); err != nil {
			return err
		}
		// 本地：快照存失败时重试一次（缺快照会让状态轮询按默认分辨率少收），这里失败由从节点重发。
		return s.deps.Gateway.StoreGrokVideoPendingBilling(ctx, taskID, userID, apiKeyID, pending)
	}, nil
}

// videoCompletion 入账视频任务完成：状态 / 内容轮询第一次看到 done + 视频地址时从节点上报；主节点按任务认领计费（只入账一次，
// 与单机同一段 service.PrepareGrokVideoCompletionBilling / PrepareSeedanceCompletionBilling），合并创建时的快照后用 RecordUsage 入账，
// 入账失败时放掉认领让下一次轮询重试（与单机一致）。认领不到（别的轮询已认领）、没有可计的内容时不入账。
func (s *Settler) videoCompletion(ctx context.Context, v *relayv1.Voucher, rec *relayv1.UsageRecord) (func(context.Context) error, []string, error) {
	taskID := strings.TrimSpace(rec.GetTaskId())
	if taskID == "" {
		return nil, nil, reject("video completion record without a task id")
	}
	var result service.OpenAIForwardResult
	if err := json.Unmarshal(rec.GetResultJson(), &result); err != nil {
		return nil, nil, reject("malformed forward result: %v", err)
	}
	input, err := s.buildOpenAIInput(ctx, v, rec, &result)
	if err != nil {
		return nil, nil, err
	}
	userID, apiKeyID := input.APIKey.User.ID, input.APIKey.ID
	reported := []string{result.Model, result.UpstreamModel, result.BillingModel}
	return func(ctx context.Context) error {
		var billed *service.OpenAIForwardResult
		if service.IsSeedanceTaskKey(taskID) {
			billed = s.deps.Gateway.PrepareSeedanceCompletionBilling(ctx, userID, apiKeyID, taskID, &result)
		} else {
			billed = s.deps.Gateway.PrepareGrokVideoCompletionBilling(ctx, userID, apiKeyID, taskID, &result)
		}
		if billed == nil {
			return nil
		}
		input.Result = billed
		// 本地 recordGrokMediaUsage：渠道用量字段取合并后的模型（请求的模型 = 映射后的模型）。
		input.ChannelUsageFields = service.ChannelUsageFields{OriginalModel: billed.Model, ChannelMappedModel: billed.Model}
		if err := s.deps.Gateway.RecordUsage(ctx, input); err != nil {
			if releaseErr := s.deps.Gateway.ReleaseGrokVideoBilling(ctx, taskID, userID, apiKeyID); releaseErr != nil {
				slog.Warn("relay: release video billing claim failed", "task_id", taskID, "error", releaseErr)
			}
			return err
		}
		return nil
	}, reported, nil
}
