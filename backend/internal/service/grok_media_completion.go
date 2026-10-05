package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// PrepareGrokVideoCompletionBilling claims one-shot billing for official done+video.url
// observations (status poll or content download). Duration/model prefer status body;
// resolution uses create-time request (status response does not document resolution).
// 单机的媒体处理函数和主从分流主节点入账视频任务完成记录共用这一段（任务状态都在这个服务的缓存里）。
func (s *OpenAIGatewayService) PrepareGrokVideoCompletionBilling(
	ctx context.Context,
	userID, apiKeyID int64,
	taskRequestID string,
	statusResult *OpenAIForwardResult,
) *OpenAIForwardResult {
	if s == nil || apiKeyID <= 0 || statusResult == nil {
		return nil
	}
	reqLog := logger.L().With(zap.String("component", "service.grok_media_completion"))
	// Forward already set VideoCount only when status=done && video.url (official).
	if statusResult.VideoCount <= 0 {
		return nil
	}
	taskRequestID = strings.TrimSpace(firstNonEmptyTrimmed(taskRequestID, statusResult.ResponseID))
	if taskRequestID == "" {
		return nil
	}
	// Load create-time snapshot before claim so we can fail-closed without burning the claim
	// when Redis lost pending and status cannot price the job.
	pending, loadErr := s.LoadGrokVideoPendingBilling(ctx, taskRequestID, userID, apiKeyID)
	if loadErr != nil {
		reqLog.Warn("grok_media.video_pending_billing_load_failed", zap.String("request_id", taskRequestID), zap.Error(loadErr))
	}
	if pending == nil {
		// Status omits resolution; without pending we would silently default to 480p and underbill.
		// Allow billing only when official status carries duration (still may default resolution).
		if statusResult.VideoDurationSeconds <= 0 {
			reqLog.Error("grok_media.video_billing_skipped_missing_pending",
				zap.String("request_id", taskRequestID),
				zap.String("reason", "no create-time snapshot and status has no video.duration"),
			)
			return nil
		}
		reqLog.Error("grok_media.video_billing_without_pending",
			zap.String("request_id", taskRequestID),
			zap.Int("status_duration_seconds", statusResult.VideoDurationSeconds),
			zap.String("note", "resolution falls back to default 480p; investigate pending store failures"),
		)
	}
	claimed, err := s.ClaimGrokVideoBilling(ctx, taskRequestID, userID, apiKeyID)
	if err != nil {
		reqLog.Warn("grok_media.video_billing_claim_failed", zap.String("request_id", taskRequestID), zap.Error(err))
		return nil
	}
	if !claimed {
		reqLog.Debug("grok_media.video_billing_already_claimed", zap.String("request_id", taskRequestID))
		return nil
	}
	// Re-merge with pending: resolution is request-only; model/duration fill gaps.
	merged := *statusResult
	if pending != nil {
		if strings.TrimSpace(merged.Model) == "" {
			merged.Model = firstNonEmptyTrimmed(pending.BillingModel, pending.Model, pending.OriginalModel)
		}
		if strings.TrimSpace(merged.BillingModel) == "" {
			merged.BillingModel = firstNonEmptyTrimmed(pending.BillingModel, pending.Model, merged.Model)
		}
		if strings.TrimSpace(merged.UpstreamModel) == "" {
			merged.UpstreamModel = pending.UpstreamModel
		}
		// Official status omits resolution — always prefer create request.
		if strings.TrimSpace(pending.VideoResolution) != "" {
			merged.VideoResolution = pending.VideoResolution
		}
		if merged.VideoDurationSeconds <= 0 {
			merged.VideoDurationSeconds = pending.VideoDurationSeconds
		}
		if strings.TrimSpace(merged.ResponseID) == "" {
			merged.ResponseID = taskRequestID
		}
	}
	if strings.TrimSpace(merged.Model) == "" {
		merged.Model = "grok-imagine-video"
	}
	if strings.TrimSpace(merged.BillingModel) == "" {
		merged.BillingModel = merged.Model
	}
	// Always force durable task id so usage_billing_dedup survives multi-poll +
	// context-local request ids (do not prefer empty-only fill).
	merged.RequestID = StableGrokVideoBillingRequestID(firstNonEmptyTrimmed(merged.ResponseID, taskRequestID))
	merged.ResponseID = firstNonEmptyTrimmed(merged.ResponseID, taskRequestID)
	merged.VideoCount = 1
	// Pure video: do not keep legacy ImageCount (avoids image-path heuristics).
	merged.ImageCount = 0
	// Official default resolution is 480p when the create request omitted it.
	merged.VideoResolution = NormalizeVideoBillingResolutionOrDefault(merged.VideoResolution)
	// Official default duration is 8s when neither status nor create provided it.
	merged.VideoDurationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(merged.VideoDurationSeconds)
	// E2E latency for async video: create accept → this discovery of done+url.
	// Bill on discovery (status/content), not after further client polls; duration
	// must not be only the single discovery hop (~hundreds of ms).
	if pending != nil {
		if e2e := GrokVideoE2EDuration(pending.CreatedAt, time.Now()); e2e > 0 {
			merged.Duration = e2e
		}
	}
	return &merged
}

// PrepareSeedanceCompletionBilling 认领 Seedance 任务的计费（Ark 回报的实际 completion tokens；不按时长推算）：
// 没有创建时的快照、没有 token 用量、已被认领都不计费。
func (s *OpenAIGatewayService) PrepareSeedanceCompletionBilling(
	ctx context.Context,
	userID, apiKeyID int64,
	taskID string,
	result *OpenAIForwardResult,
) *OpenAIForwardResult {
	if s == nil || result == nil || result.Usage.OutputTokens <= 0 {
		return nil
	}
	pending, err := s.LoadGrokVideoPendingBilling(ctx, taskID, userID, apiKeyID)
	if err != nil || pending == nil {
		return nil
	}
	claimed, err := s.ClaimGrokVideoBilling(ctx, taskID, userID, apiKeyID)
	if err != nil || !claimed {
		return nil
	}
	merged := *result
	merged.Model = pending.Model
	merged.BillingModel = firstNonEmptyTrimmed(pending.BillingModel, pending.Model)
	merged.UpstreamModel = firstNonEmptyTrimmed(pending.UpstreamModel, result.UpstreamModel)
	merged.RequestID = StableGrokVideoBillingRequestID(taskID)
	merged.ResponseID = taskID
	merged.Duration = GrokVideoE2EDuration(pending.CreatedAt, time.Now())
	return &merged
}

// IsSeedanceTaskKey 报告任务 ID 是不是 Seedance 的（SeedanceTaskKey 加的前缀）。
func IsSeedanceTaskKey(taskID string) bool {
	return strings.HasPrefix(strings.TrimSpace(taskID), "seedance:")
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
